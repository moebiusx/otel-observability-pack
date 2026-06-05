// Package operator wires together resolve, plan, and render. It is the
// pure-Go core that a controller-runtime Reconcile loop drives. Keeping
// this layer free of K8s client dependencies means dry-run, CLI, and
// unit-test entrypoints all share the same code path the cluster does.
package operator

import (
	"encoding/json"
	"fmt"

	apiv1 "github.com/example/observability-pack/api/v1alpha1"
	"github.com/example/observability-pack/internal/lint"
	"github.com/example/observability-pack/internal/operator/azure"
	"github.com/example/observability-pack/internal/operator/plan"
	"github.com/example/observability-pack/internal/operator/render"
	"github.com/example/observability-pack/internal/pack"
)

// parsePack converts the raw manifest map into a typed pack.Pack while
// preserving the original document on Pack.Raw, matching what pack.Load
// produces from disk. We do not change pack.Load: this keeps the
// CLI-on-disk and operator-from-CRD paths reading the same structure.
func parsePack(manifest map[string]any) (*pack.Pack, error) {
	b, err := json.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("marshal manifest: %w", err)
	}
	var p pack.Pack
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, fmt.Errorf("unmarshal manifest: %w", err)
	}
	p.Raw = manifest
	return &p, nil
}

// Result is the outcome of one reconciliation pass through the
// pure-logic core. Adapters consume Plan and either Objects (Kubernetes
// targets) or AzureRequest (Azure target). The controller copies
// LintFindings onto Pack.Status.
type Result struct {
	Plan          *plan.Plan
	Objects       []render.Object
	AzureRequest  *azure.Request
	LintFindings  []lint.Finding
	BlockedReason string
}

// Reconcile runs the full Resolve -> Validate -> Plan -> Render cycle.
// It does not write anywhere; the controller (or dry-run CLI) decides
// whether to apply Result.Objects or call Azure.
//
// `manifest` is the literal observability-pack JSON document, namespace
// is the cluster namespace the in-cluster CRDs should land in, and
// azureRef is consulted only when target=azure.
func Reconcile(manifest map[string]any, target apiv1.Target, namespace string, azureRef *apiv1.AzurePipelineRef) (*Result, error) {
	if manifest == nil {
		return nil, fmt.Errorf("operator.Reconcile: empty manifest")
	}
	if target == "" {
		target = apiv1.TargetSKE
	}

	p, err := parsePack(manifest)
	if err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}

	res := &Result{}
	lintRes := &lint.Result{
		Pack:        p.Metadata.Name,
		Version:     p.Metadata.Version,
		Service:     p.Metadata.Bindings.Service,
		Criticality: p.Metadata.Bindings.Criticality,
	}
	lint.Refs(p, lintRes)
	res.LintFindings = append(res.LintFindings, lintRes.Findings...)

	for _, f := range res.LintFindings {
		if f.Severity == lint.SeverityError {
			res.BlockedReason = "reference-integrity errors prevent reconciliation"
			return res, nil
		}
	}

	pl, err := plan.Build(p, target, azureRef)
	if err != nil {
		return nil, fmt.Errorf("plan: %w", err)
	}
	res.Plan = pl

	switch target {
	case apiv1.TargetSKE, apiv1.TargetBareK8s:
		objs := []render.Object{render.Prometheus(pl, namespace)}
		objs = append(objs, render.Grafana(pl, namespace)...)
		objs = append(objs, render.OTel(pl, namespace))
		res.Objects = objs
	case apiv1.TargetAzure:
		req, err := azure.BuildRequest(pl, azureRef)
		if err != nil {
			return nil, fmt.Errorf("azure request: %w", err)
		}
		res.AzureRequest = req
	default:
		return nil, fmt.Errorf("operator.Reconcile: unknown target %q", target)
	}
	return res, nil
}
