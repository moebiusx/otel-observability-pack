// Package v1alpha1 defines the Kubernetes types the ObservabilityPack
// meta-operator reconciles.
//
// The types are intentionally kept simple (Manifest is a free-form map)
// so the CRD stays forward-compatible with schema additions to the
// underlying observability-pack document. Validation of Manifest is
// performed by the operator's lint/refs passes against the current
// JSON Schema, not by Kubernetes API server validation, which would
// otherwise hard-pin the CRD to one schema revision.
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// Target is the execution target the Pack Operator routes to.
type Target string

const (
	// TargetSKE is the in-cluster meta-operator path on SKE.
	TargetSKE Target = "ske"
	// TargetBareK8s is the in-cluster path on a generic Kubernetes
	// cluster, with capability discovery for whichever tool operators
	// are installed.
	TargetBareK8s Target = "bare-k8s"
	// TargetAzure is the pipeline tool path: the operator emits an
	// auditable pipeline run rather than mutating cloud resources directly.
	TargetAzure Target = "azure"
)

// Phase is the high-level reconciliation phase reported on Pack.Status.
type Phase string

const (
	PhasePending  Phase = "Pending"
	PhasePlanning Phase = "Planning"
	PhaseApplying Phase = "Applying"
	PhaseReady    Phase = "Ready"
	PhaseDegraded Phase = "Degraded"
	PhaseBlocked  Phase = "Blocked"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// Pack is the namespaced custom resource the meta-operator owns. Each
// Pack maps to one observability-pack manifest applied to one execution
// target.
type Pack struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PackSpec   `json:"spec,omitempty"`
	Status PackStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// PackList is a list of Pack custom resources.
type PackList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Pack `json:"items"`
}

// PackSpec is the desired state.
type PackSpec struct {
	// Target selects the execution path. Required.
	Target Target `json:"target"`

	// Namespace is the cluster namespace the in-cluster CRDs (Prometheus,
	// Grafana, OTel) should land in. Ignored for target=azure. Defaults
	// to the Pack's own namespace when empty.
	Namespace string `json:"namespace,omitempty"`

	// AzurePipeline is consulted only when Target=azure.
	AzurePipeline *AzurePipelineRef `json:"azurePipeline,omitempty"`

	// Manifest is the literal observability-pack document. The operator
	// resolves imports/composes against it and produces the effective
	// snapshot reported on Status.
	// +kubebuilder:pruning:PreserveUnknownFields
	Manifest map[string]any `json:"manifest"`
}

// AzurePipelineRef points at the pipeline used to apply the pack on
// Azure-native targets.
type AzurePipelineRef struct {
	// Provider is "azure-devops" or "github-actions".
	Provider string `json:"provider"`
	// Organization / Project / Pipeline identify the run target.
	Organization string `json:"organization,omitempty"`
	Project      string `json:"project,omitempty"`
	Pipeline     string `json:"pipeline"`
	// Branch pins the IaC revision. Optional.
	Branch string `json:"branch,omitempty"`
	// CredentialsSecretRef names a Kubernetes secret in the same
	// namespace as the Pack containing the pipeline-trigger credentials.
	// The secret must contain a `token` key. The operator never inlines
	// tokens.
	CredentialsSecretRef string `json:"credentialsSecretRef,omitempty"`
}

// PackStatus reports observed reconciliation state.
type PackStatus struct {
	Phase              Phase              `json:"phase,omitempty"`
	ObservedGeneration int64              `json:"observedGeneration,omitempty"`
	EffectiveHash      string             `json:"effectiveHash,omitempty"`
	Tools              []ToolStatus       `json:"tools,omitempty"`
	Conditions         []metav1.Condition `json:"conditions,omitempty"`
	LastError          string             `json:"lastError,omitempty"`
	AzurePipelineRun   *PipelineRunInfo   `json:"azurePipelineRun,omitempty"`
}

// ToolStatus reports per-adapter outcome for a single reconciliation.
type ToolStatus struct {
	Tool    string `json:"tool"`
	Phase   Phase  `json:"phase"`
	Objects int    `json:"objects,omitempty"`
	Message string `json:"message,omitempty"`
}

// PipelineRunInfo records the most recent Azure pipeline invocation.
type PipelineRunInfo struct {
	RunID   string      `json:"runId,omitempty"`
	URL     string      `json:"url,omitempty"`
	Status  string      `json:"status,omitempty"`
	Started metav1.Time `json:"started,omitempty"`
}

// DeepCopyObject implements runtime.Object.
func (in *Pack) DeepCopyObject() runtime.Object {
	if in == nil {
		return nil
	}
	out := new(Pack)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject implements runtime.Object.
func (in *PackList) DeepCopyObject() runtime.Object {
	if in == nil {
		return nil
	}
	out := new(PackList)
	in.DeepCopyInto(out)
	return out
}
