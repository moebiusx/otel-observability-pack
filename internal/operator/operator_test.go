package operator_test

import (
	"encoding/json"
	"path/filepath"
	"testing"

	apiv1 "github.com/example/observability-pack/api/v1alpha1"
	"github.com/example/observability-pack/internal/operator"
	"github.com/example/observability-pack/internal/pack"
)

// loadExample reads the canonical example pack used by the lint tests.
// We rely on it being valid; if upstream changes break it, the lint
// suite will fail first.
func loadExample(t *testing.T) *pack.Pack {
	t.Helper()
	p, err := pack.Load(filepath.Join("..", "..", "examples", "payment-service.pack.yaml"))
	if err != nil {
		t.Fatalf("load example pack: %v", err)
	}
	return p
}

func TestReconcile_SKE_RendersCRDs(t *testing.T) {
	p := loadExample(t)
	res, err := operator.Reconcile(p.Raw, apiv1.TargetSKE, "observability", nil)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if res.BlockedReason != "" {
		t.Fatalf("unexpected block: %s; findings=%+v", res.BlockedReason, res.LintFindings)
	}
	if res.Plan == nil {
		t.Fatal("plan must not be nil")
	}
	if res.Plan.Hash == "" {
		t.Error("plan hash must be populated")
	}
	if len(res.Objects) == 0 {
		t.Fatal("expected at least PrometheusRule + OTel objects")
	}

	// The first object is always the PrometheusRule.
	if got := res.Objects[0]["kind"]; got != "PrometheusRule" {
		t.Errorf("first rendered object kind = %v, want PrometheusRule", got)
	}
	// The last object is the OpenTelemetryCollector.
	last := res.Objects[len(res.Objects)-1]
	if got := last["kind"]; got != "OpenTelemetryCollector" {
		t.Errorf("last rendered object kind = %v, want OpenTelemetryCollector", got)
	}
}

func TestReconcile_Azure_BuildsRequest(t *testing.T) {
	p := loadExample(t)
	res, err := operator.Reconcile(p.Raw, apiv1.TargetAzure, "observability",
		&apiv1.AzurePipelineRef{
			Provider: "azure-devops",
			Pipeline: "deploy-observability",
			Branch:   "main",
		})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if res.AzureRequest == nil {
		t.Fatal("expected AzureRequest to be populated for azure target")
	}
	if res.AzureRequest.Pipeline != "deploy-observability" {
		t.Errorf("pipeline = %q, want deploy-observability", res.AzureRequest.Pipeline)
	}
	if len(res.Objects) != 0 {
		t.Errorf("Azure target must not render in-cluster objects; got %d", len(res.Objects))
	}

	// Round-trip the request to make sure the payload is JSON-clean.
	if _, err := json.Marshal(res.AzureRequest); err != nil {
		t.Errorf("azure request not JSON-marshalable: %v", err)
	}
}

func TestReconcile_Azure_MissingPipelineFails(t *testing.T) {
	p := loadExample(t)
	if _, err := operator.Reconcile(p.Raw, apiv1.TargetAzure, "observability", nil); err == nil {
		t.Fatal("expected error when target=azure has no pipeline reference")
	}
}

func TestReconcile_DeterministicHash(t *testing.T) {
	p := loadExample(t)
	a, err := operator.Reconcile(p.Raw, apiv1.TargetSKE, "observability", nil)
	if err != nil {
		t.Fatalf("reconcile a: %v", err)
	}
	b, err := operator.Reconcile(p.Raw, apiv1.TargetSKE, "observability", nil)
	if err != nil {
		t.Fatalf("reconcile b: %v", err)
	}
	if a.Plan.Hash != b.Plan.Hash {
		t.Errorf("plan hash not deterministic: %s vs %s", a.Plan.Hash, b.Plan.Hash)
	}
}
