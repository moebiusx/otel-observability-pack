// Package v1alpha1 defines the Kubernetes-style types the ObservabilityPack
// superoperator reconciles. These types are deliberately decoupled from
// controller-runtime so the planning and rendering logic can be unit-tested
// without an apiserver in the loop.
//
// The full apimachinery wrapping (deep-copy, scheme registration, CRD
// manifests) lives next to the manager binary in cmd/operator and the
// generated CRD yaml under config/crd. Anything in this package must remain
// safe to import from CLI or test code.
package v1alpha1

// GroupVersion identifies the API surface.
const (
	GroupName = "observability.platform"
	Version   = "v1alpha1"
)

// Target is the execution target the Pack Operator routes to.
type Target string

const (
	// TargetSKE is the in-cluster operator-of-operators path on SKE.
	TargetSKE Target = "ske"
	// TargetBareK8s is the in-cluster operator-of-operators path on a
	// generic Kubernetes cluster, with capability discovery for whichever
	// tool operators are installed.
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

// PackSpec is the desired state. Manifest holds the raw observability-pack
// document (the same JSON the schema validates) so the CRD stays
// forward-compatible with spec evolution.
type PackSpec struct {
	// Target selects the execution path. Required.
	Target Target `json:"target"`

	// AzurePipeline is consulted only when Target=azure. It points at the
	// pipeline that will apply the effective pack on Azure-native targets.
	AzurePipeline *AzurePipelineRef `json:"azurePipeline,omitempty"`

	// Manifest is the literal observability-pack document. The operator
	// resolves imports/composes against it and produces the EffectivePack
	// snapshot reported on Status.
	Manifest map[string]any `json:"manifest"`
}

// AzurePipelineRef points at the pipeline used to apply the pack on Azure.
// The pipeline is the only thing allowed to write Azure-native resources;
// the operator never mutates Azure directly.
type AzurePipelineRef struct {
	// Provider is "azure-devops" or "github-actions".
	Provider string `json:"provider"`
	// Organization / Project / Pipeline identify the run target.
	Organization string `json:"organization,omitempty"`
	Project      string `json:"project,omitempty"`
	Pipeline     string `json:"pipeline"`
	// Branch pins the IaC revision. Optional; defaults to the pipeline's
	// configured default branch.
	Branch string `json:"branch,omitempty"`
	// CredentialsSecretRef names a Kubernetes secret containing the
	// pipeline-trigger credentials. The operator never inlines tokens.
	CredentialsSecretRef string `json:"credentialsSecretRef,omitempty"`
}

// PackStatus reports observed reconciliation state.
type PackStatus struct {
	Phase              Phase            `json:"phase,omitempty"`
	ObservedGeneration int64            `json:"observedGeneration,omitempty"`
	EffectiveHash      string           `json:"effectiveHash,omitempty"`
	Tools              []ToolStatus     `json:"tools,omitempty"`
	Conditions         []Condition      `json:"conditions,omitempty"`
	LastError          string           `json:"lastError,omitempty"`
	AzurePipelineRun   *PipelineRunInfo `json:"azurePipelineRun,omitempty"`
}

// ToolStatus reports per-adapter outcome for a single reconciliation.
type ToolStatus struct {
	Tool    string `json:"tool"`
	Phase   Phase  `json:"phase"`
	Objects int    `json:"objects,omitempty"`
	Message string `json:"message,omitempty"`
}

// Condition is a Kubernetes-style status condition.
type Condition struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
}

// PipelineRunInfo records the most recent Azure pipeline invocation.
type PipelineRunInfo struct {
	RunID   string `json:"runId,omitempty"`
	URL     string `json:"url,omitempty"`
	Status  string `json:"status,omitempty"`
	Started string `json:"started,omitempty"`
}
