package controller_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	apiv1 "github.com/example/observability-pack/api/v1alpha1"
	opcontroller "github.com/example/observability-pack/internal/operator/controller"
	"github.com/example/observability-pack/internal/pack"
)

// fakeAzure captures the trigger call for assertions.
type fakeAzure struct {
	called  bool
	gotRef  *apiv1.AzurePipelineRef
	gotBody []byte
	resp    apiv1.PipelineRunInfo
}

func (f *fakeAzure) Trigger(_ context.Context, ref *apiv1.AzurePipelineRef, payload []byte) (*apiv1.PipelineRunInfo, error) {
	f.called = true
	f.gotRef = ref
	f.gotBody = payload
	return &f.resp, nil
}

func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := apiv1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	return s
}

func loadManifest(t *testing.T) map[string]any {
	t.Helper()
	p, err := pack.Load(filepath.Join("..", "..", "..", "examples", "payment-service.pack.yaml"))
	if err != nil {
		t.Fatalf("load example: %v", err)
	}
	return p.Raw
}

func TestPackReconciler_SKE_AppliesObjectsAndReportsReady(t *testing.T) {
	scheme := newScheme(t)

	pk := &apiv1.Pack{
		ObjectMeta: metav1.ObjectMeta{Name: "payments", Namespace: "obs", Generation: 1},
		Spec: apiv1.PackSpec{
			Target:    apiv1.TargetSKE,
			Manifest:  loadManifest(t),
			Namespace: "obs",
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(pk).
		WithStatusSubresource(&apiv1.Pack{}).
		Build()

	rec := record.NewFakeRecorder(64)

	r := &opcontroller.PackReconciler{Client: c, Applier: upsertApplier{}, Recorder: rec}
	res, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "payments", Namespace: "obs"}})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if res.RequeueAfter == 0 {
		t.Error("expected periodic requeue on success")
	}

	var got apiv1.Pack
	if err := c.Get(context.Background(), types.NamespacedName{Name: "payments", Namespace: "obs"}, &got); err != nil {
		t.Fatal(err)
	}
	if got.Status.Phase != apiv1.PhaseReady {
		t.Errorf("phase = %q, want Ready (lastError=%q)", got.Status.Phase, got.Status.LastError)
	}
	if got.Status.EffectiveHash == "" {
		t.Error("effectiveHash must be set")
	}
	if len(got.Status.Tools) == 0 {
		t.Error("tools status must be populated")
	}

	assertCondition(t, &got, opcontroller.ConditionValidated, metav1.ConditionTrue)
	assertCondition(t, &got, opcontroller.ConditionReady, metav1.ConditionTrue)
	assertCondition(t, &got, opcontroller.ConditionDegraded, metav1.ConditionFalse)
	assertCondition(t, &got, opcontroller.ConditionProgressing, metav1.ConditionFalse)
	assertEvent(t, rec, opcontroller.ReasonApplied)
	assertEvent(t, rec, opcontroller.ReasonReady)

	// Verify a PrometheusRule was applied via SSA into the fake client.
	pr := &unstructured.Unstructured{}
	pr.SetGroupVersionKind(promRuleGVK)
	prList := &unstructured.UnstructuredList{}
	prList.SetGroupVersionKind(promRuleGVK)
	if err := c.List(context.Background(), prList, client.InNamespace("obs")); err != nil {
		t.Fatalf("list PrometheusRule: %v", err)
	}
	if len(prList.Items) == 0 {
		t.Error("expected at least one PrometheusRule applied")
	}
}

func TestPackReconciler_Azure_TriggersPipeline(t *testing.T) {
	scheme := newScheme(t)
	pk := &apiv1.Pack{
		ObjectMeta: metav1.ObjectMeta{Name: "payments", Namespace: "obs", Generation: 1},
		Spec: apiv1.PackSpec{
			Target:   apiv1.TargetAzure,
			Manifest: loadManifest(t),
			AzurePipeline: &apiv1.AzurePipelineRef{
				Provider: "azure-devops",
				Pipeline: "deploy-observability",
			},
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(pk).
		WithStatusSubresource(&apiv1.Pack{}).
		Build()

	azure := &fakeAzure{resp: apiv1.PipelineRunInfo{RunID: "42", URL: "https://dev.azure.com/run/42", Status: "Triggered"}}
	rec := record.NewFakeRecorder(64)
	r := &opcontroller.PackReconciler{Client: c, Azure: azure, Recorder: rec}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "payments", Namespace: "obs"}}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if !azure.called {
		t.Fatal("azure trigger was not invoked")
	}
	if azure.gotRef.Pipeline != "deploy-observability" {
		t.Errorf("pipeline ref = %+v", azure.gotRef)
	}

	var got apiv1.Pack
	if err := c.Get(context.Background(), types.NamespacedName{Name: "payments", Namespace: "obs"}, &got); err != nil {
		t.Fatal(err)
	}
	if got.Status.Phase != apiv1.PhaseApplying {
		t.Errorf("phase = %q, want Applying", got.Status.Phase)
	}
	if got.Status.AzurePipelineRun == nil || got.Status.AzurePipelineRun.RunID != "42" {
		t.Errorf("AzurePipelineRun = %+v", got.Status.AzurePipelineRun)
	}
	assertCondition(t, &got, opcontroller.ConditionReady, metav1.ConditionTrue)
	assertEvent(t, rec, opcontroller.ReasonPipelineQueued)
}

func assertCondition(t *testing.T, p *apiv1.Pack, condType string, want metav1.ConditionStatus) {
	t.Helper()
	cond := meta.FindStatusCondition(p.Status.Conditions, condType)
	if cond == nil {
		t.Errorf("condition %q not present", condType)
		return
	}
	if cond.Status != want {
		t.Errorf("condition %q = %s, want %s (reason=%s msg=%q)", condType, cond.Status, want, cond.Reason, cond.Message)
	}
	if cond.ObservedGeneration == 0 {
		t.Errorf("condition %q has zero observedGeneration", condType)
	}
}

func assertEvent(t *testing.T, rec *record.FakeRecorder, reason string) {
	t.Helper()
	for {
		select {
		case ev := <-rec.Events:
			if strings.Contains(ev, reason) {
				return
			}
		default:
			t.Errorf("no event with reason %q observed", reason)
			return
		}
	}
}
