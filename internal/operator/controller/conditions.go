// Package controller — conditions.go centralises the standard
// Kubernetes Conditions reported on Pack.Status, plus the small helper
// used by the reconciler to set them. Conditions are the source of
// truth for "what is this Pack doing right now"; Phase is a coarse
// summary derived from them for printer columns and humans.
package controller

import (
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	apiv1 "github.com/example/observability-pack/api/v1alpha1"
)

// Condition types reported on Pack.Status.
const (
	// ConditionValidated is true when the manifest passed lint
	// (refs resolved, no SeverityError findings).
	ConditionValidated = "Validated"

	// ConditionProgressing is true while the controller is planning or
	// applying. It flips back to false on Ready or terminal Degraded.
	ConditionProgressing = "Progressing"

	// ConditionReady is the standard "everything succeeded this pass"
	// signal. For target=azure it means the pipeline was triggered;
	// pipeline completion is reported on Status.AzurePipelineRun.
	ConditionReady = "Ready"

	// ConditionDegraded is true when at least one tool adapter
	// reported a failure during apply.
	ConditionDegraded = "Degraded"
)

// Standard Reason values. Reasons are CamelCase per Kubernetes API
// conventions and stable across releases (consumers may match on them).
const (
	ReasonReconciling      = "Reconciling"
	ReasonValidated        = "Validated"
	ReasonValidationFailed = "ValidationFailed"
	ReasonApplied          = "Applied"
	ReasonApplyFailed      = "ApplyFailed"
	ReasonPipelineQueued   = "PipelineTriggered"
	ReasonPipelineFailed   = "PipelineFailed"
	ReasonReady            = "Ready"
	ReasonDegraded         = "Degraded"
	ReasonBlocked          = "Blocked"
)

// setCondition stamps a condition idempotently using
// apimachinery's meta.SetStatusCondition, which preserves
// LastTransitionTime when Status doesn't change. ObservedGeneration is
// always set to the current generation so consumers can tell stale
// entries from fresh ones.
func setCondition(p *apiv1.Pack, condType string, status metav1.ConditionStatus, reason, message string) {
	meta.SetStatusCondition(&p.Status.Conditions, metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: p.Generation,
	})
}

// markReady sets the canonical Ready=True/Progressing=False/Degraded=False trio.
func markReady(p *apiv1.Pack, message string) {
	setCondition(p, ConditionReady, metav1.ConditionTrue, ReasonReady, message)
	setCondition(p, ConditionProgressing, metav1.ConditionFalse, ReasonReady, message)
	setCondition(p, ConditionDegraded, metav1.ConditionFalse, ReasonReady, "")
}

// markDegraded flips Ready=False/Degraded=True. Progressing is left to
// the caller (the controller will requeue, so Progressing stays True
// until the next pass succeeds).
func markDegraded(p *apiv1.Pack, reason, message string) {
	setCondition(p, ConditionReady, metav1.ConditionFalse, reason, message)
	setCondition(p, ConditionDegraded, metav1.ConditionTrue, reason, message)
}

// markBlocked is the lint-block terminal: Validated=False, Ready=False.
func markBlocked(p *apiv1.Pack, message string) {
	setCondition(p, ConditionValidated, metav1.ConditionFalse, ReasonValidationFailed, message)
	setCondition(p, ConditionReady, metav1.ConditionFalse, ReasonBlocked, message)
	setCondition(p, ConditionProgressing, metav1.ConditionFalse, ReasonBlocked, message)
}

// markProgressing announces "we are working" at the start of a pass.
func markProgressing(p *apiv1.Pack, message string) {
	setCondition(p, ConditionProgressing, metav1.ConditionTrue, ReasonReconciling, message)
}

// markValidated records that lint passed.
func markValidated(p *apiv1.Pack) {
	setCondition(p, ConditionValidated, metav1.ConditionTrue, ReasonValidated, "")
}
