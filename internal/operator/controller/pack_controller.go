// Package controller hosts the controller-runtime Reconciler that drives
// an in-cluster ObservabilityPack. The Reconciler is intentionally
// thin: it fetches the Pack CR, hands it to internal/operator for the
// pure planning + rendering work, and then either server-side-applies
// the rendered objects (Kubernetes targets) or invokes the Azure
// pipeline tool adapter.
package controller

import (
	"context"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	apiv1 "github.com/example/observability-pack/api/v1alpha1"
	"github.com/example/observability-pack/internal/operator"
	"github.com/example/observability-pack/internal/operator/render"
)

// FieldOwner is the SSA field-manager identity owned by this meta-operator.
const FieldOwner client.FieldOwner = "observability-pack-operator"

// AzureTrigger abstracts the pipeline call so unit tests can supply a
// stub. Implementations must be safe to call concurrently.
type AzureTrigger interface {
	Trigger(ctx context.Context, ref *apiv1.AzurePipelineRef, payload []byte) (*apiv1.PipelineRunInfo, error)
}

// ObjectApplier upserts a rendered tool object into the cluster. The
// production implementation uses Kubernetes server-side apply; tests
// substitute a create-or-update applier because the controller-runtime
// fake client does not support SSA on arbitrary unstructured types.
type ObjectApplier interface {
	Apply(ctx context.Context, c client.Client, obj *unstructured.Unstructured) error
}

// SSAApplier applies via server-side apply with this operator's field
// manager. Production default.
type SSAApplier struct{}

// Apply implements ObjectApplier.
func (SSAApplier) Apply(ctx context.Context, c client.Client, obj *unstructured.Unstructured) error {
	return c.Patch(ctx, obj, client.Apply, FieldOwner, client.ForceOwnership)
}

// PackReconciler reconciles a Pack object.
type PackReconciler struct {
	client.Client
	Azure   AzureTrigger
	Applier ObjectApplier
}

func (r *PackReconciler) applier() ObjectApplier {
	if r.Applier != nil {
		return r.Applier
	}
	return SSAApplier{}
}

// +kubebuilder:rbac:groups=observability.platform,resources=packs,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=observability.platform,resources=packs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=monitoring.coreos.com,resources=prometheusrules,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=grafana.integreatly.org,resources=grafanadashboards,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=opentelemetry.io,resources=opentelemetrycollectors;instrumentations,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// Reconcile drives one pass of Resolve -> Validate -> Plan -> Apply -> Status.
func (r *PackReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("pack", req.NamespacedName)

	var p apiv1.Pack
	if err := r.Get(ctx, req.NamespacedName, &p); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Resolve namespace for in-cluster CRDs (defaults to the Pack's own).
	ns := p.Spec.Namespace
	if ns == "" {
		ns = p.Namespace
	}

	// Run the pure core.
	result, err := operator.Reconcile(p.Spec.Manifest, p.Spec.Target, ns, p.Spec.AzurePipeline)
	if err != nil {
		return r.fail(ctx, &p, fmt.Errorf("reconcile core: %w", err))
	}

	if result.BlockedReason != "" {
		p.Status.Phase = apiv1.PhaseBlocked
		p.Status.LastError = result.BlockedReason
		p.Status.ObservedGeneration = p.Generation
		return ctrl.Result{}, r.Status().Update(ctx, &p)
	}

	// Apply per target.
	switch p.Spec.Target {
	case apiv1.TargetSKE, apiv1.TargetBareK8s:
		toolStatus, err := r.applyObjects(ctx, &p, result.Objects)
		if err != nil {
			p.Status.Tools = toolStatus
			return r.fail(ctx, &p, err)
		}
		p.Status.Tools = toolStatus
		p.Status.Phase = apiv1.PhaseReady
	case apiv1.TargetAzure:
		runInfo, err := r.triggerAzure(ctx, &p, result)
		if err != nil {
			return r.fail(ctx, &p, err)
		}
		p.Status.AzurePipelineRun = runInfo
		p.Status.Tools = []apiv1.ToolStatus{{Tool: "azure-pipeline", Phase: apiv1.PhaseApplying, Message: runInfo.Status}}
		p.Status.Phase = apiv1.PhaseApplying
	default:
		return r.fail(ctx, &p, fmt.Errorf("unknown target %q", p.Spec.Target))
	}

	p.Status.LastError = ""
	p.Status.EffectiveHash = result.Plan.Hash
	p.Status.ObservedGeneration = p.Generation
	if err := r.Status().Update(ctx, &p); err != nil {
		return ctrl.Result{}, err
	}
	logger.Info("reconciled", "phase", p.Status.Phase, "hash", p.Status.EffectiveHash)
	// Resync periodically to detect drift; SSA already idempotent.
	return ctrl.Result{RequeueAfter: 10 * time.Minute}, nil
}

// applyObjects server-side-applies each rendered tool CRD. Failures on
// one adapter don't block the rest: the per-tool ToolStatus surfaces
// which one degraded.
func (r *PackReconciler) applyObjects(ctx context.Context, p *apiv1.Pack, objs []render.Object) ([]apiv1.ToolStatus, error) {
	counts := map[string]int{}
	errs := map[string]error{}
	owner := ownerRef(p)

	for _, raw := range objs {
		u := &unstructured.Unstructured{Object: map[string]any(raw)}
		u.SetOwnerReferences([]metav1.OwnerReference{owner})
		if err := r.applier().Apply(ctx, r.Client, u); err != nil {
			errs[u.GetKind()] = err
			continue
		}
		counts[u.GetKind()]++
	}

	out := []apiv1.ToolStatus{}
	for kind, n := range counts {
		ts := apiv1.ToolStatus{Tool: toolFromKind(kind), Phase: apiv1.PhaseReady, Objects: n}
		if e, ok := errs[kind]; ok {
			ts.Phase = apiv1.PhaseDegraded
			ts.Message = e.Error()
		}
		out = append(out, ts)
	}
	for kind, e := range errs {
		if _, applied := counts[kind]; applied {
			continue
		}
		out = append(out, apiv1.ToolStatus{Tool: toolFromKind(kind), Phase: apiv1.PhaseDegraded, Message: e.Error()})
	}
	if len(errs) > 0 {
		return out, fmt.Errorf("%d adapter(s) degraded", len(errs))
	}
	return out, nil
}

func toolFromKind(kind string) string {
	switch kind {
	case "PrometheusRule":
		return "prometheus"
	case "GrafanaDashboard":
		return "grafana"
	case "OpenTelemetryCollector", "Instrumentation":
		return "otel"
	default:
		return kind
	}
}

// triggerAzure resolves credentials and invokes the Azure pipeline.
func (r *PackReconciler) triggerAzure(ctx context.Context, p *apiv1.Pack, res *operator.Result) (*apiv1.PipelineRunInfo, error) {
	if r.Azure == nil {
		return nil, fmt.Errorf("azure trigger not configured")
	}
	if res.AzureRequest == nil {
		return nil, fmt.Errorf("azure target produced no request")
	}
	payload, err := res.AzureRequest.Marshal()
	if err != nil {
		return nil, fmt.Errorf("marshal azure request: %w", err)
	}
	return r.Azure.Trigger(ctx, p.Spec.AzurePipeline, payload)
}

// fail records the error on Status and short-circuits the rest of the
// reconcile. The controller does not push the error up to the manager
// so a single bad Pack can't take down the work loop.
func (r *PackReconciler) fail(ctx context.Context, p *apiv1.Pack, err error) (ctrl.Result, error) {
	p.Status.Phase = apiv1.PhaseDegraded
	p.Status.LastError = err.Error()
	p.Status.ObservedGeneration = p.Generation
	if uerr := r.Status().Update(ctx, p); uerr != nil {
		return ctrl.Result{}, uerr
	}
	log.FromContext(ctx).Error(err, "pack degraded", "name", p.Name)
	return ctrl.Result{RequeueAfter: time.Minute}, nil
}

// SetupWithManager registers the reconciler.
func (r *PackReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&apiv1.Pack{}).
		Complete(r)
}

// PackGVK is the GVK of our root resource, exported for tests/tooling.
var PackGVK = schema.GroupVersionKind{Group: apiv1.GroupVersion.Group, Version: apiv1.GroupVersion.Version, Kind: "Pack"}

// ownerRef constructs a controller owner reference pointing at p.
func ownerRef(p *apiv1.Pack) metav1.OwnerReference {
	trueVar := true
	return metav1.OwnerReference{
		APIVersion:         apiv1.GroupVersion.String(),
		Kind:               "Pack",
		Name:               p.Name,
		UID:                p.UID,
		Controller:         &trueVar,
		BlockOwnerDeletion: &trueVar,
	}
}
