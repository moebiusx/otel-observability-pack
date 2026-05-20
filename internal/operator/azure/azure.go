// Package azure builds the payload the Azure pipeline tool adapter sends
// to Azure DevOps or GitHub Actions to apply an effective ObservabilityPack
// against Azure-native targets. Building the payload is pure; actually
// triggering the run is the controller's responsibility, so this code can
// be exercised offline.
package azure

import (
	"encoding/json"
	"fmt"

	apiv1 "github.com/example/observability-pack/api/v1alpha1"
	"github.com/example/observability-pack/internal/operator/plan"
)

// Request is a wire-format trigger payload independent of the specific
// pipeline provider. The Azure adapter wraps it in the provider-native
// envelope (azure-devops "runs" body, GitHub Actions workflow_dispatch
// body) at submission time.
type Request struct {
	Provider     string         `json:"provider"`
	Pipeline     string         `json:"pipeline"`
	Branch       string         `json:"branch,omitempty"`
	Organization string         `json:"organization,omitempty"`
	Project      string         `json:"project,omitempty"`
	Pack         string         `json:"pack"`
	Version      string         `json:"version"`
	Service      string         `json:"service"`
	Target       apiv1.Target   `json:"target"`
	Hash         string         `json:"effectiveHash"`
	Parameters   map[string]any `json:"parameters"`
}

// BuildRequest derives a Request from a Plan and the pipeline ref on
// the Pack spec. It returns an error if the plan was not built for an
// Azure target — calling code should not reach this otherwise.
func BuildRequest(pl *plan.Plan, ref *apiv1.AzurePipelineRef) (*Request, error) {
	if pl == nil {
		return nil, fmt.Errorf("azure.BuildRequest: nil plan")
	}
	if pl.Target != apiv1.TargetAzure {
		return nil, fmt.Errorf("azure.BuildRequest: plan target is %q, want %q", pl.Target, apiv1.TargetAzure)
	}
	if ref == nil || ref.Pipeline == "" {
		return nil, fmt.Errorf("azure.BuildRequest: pipeline reference required")
	}

	return &Request{
		Provider:     ref.Provider,
		Pipeline:     ref.Pipeline,
		Branch:       ref.Branch,
		Organization: ref.Organization,
		Project:      ref.Project,
		Pack:         pl.Pack,
		Version:      pl.Version,
		Service:      pl.Service,
		Target:       pl.Target,
		Hash:         pl.Hash,
		Parameters: map[string]any{
			"effectivePlan": pl,
			"call":          pl.Azure,
		},
	}, nil
}

// Marshal returns the canonical JSON encoding the controller stores
// alongside the PackToolRun status record.
func (r *Request) Marshal() ([]byte, error) {
	return json.MarshalIndent(r, "", "  ")
}
