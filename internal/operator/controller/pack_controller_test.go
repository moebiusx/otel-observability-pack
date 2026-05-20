package controller_test

import (
	"context"
	"path/filepath"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
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

	r := &opcontroller.PackReconciler{Client: c, Applier: upsertApplier{}}
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
	r := &opcontroller.PackReconciler{Client: c, Azure: azure}
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
}
