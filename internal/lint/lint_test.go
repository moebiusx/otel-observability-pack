package lint_test

import (
	"path/filepath"
	"testing"

	"github.com/example/observability-pack/internal/lint"
	"github.com/example/observability-pack/internal/pack"
)

// repoRoot resolves paths relative to the repo root regardless of where
// `go test` is invoked from. The test file lives at internal/lint/.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
	return root
}

func TestPaymentServiceExampleLoads(t *testing.T) {
	root := repoRoot(t)
	p, err := pack.Load(filepath.Join(root, "examples", "payment-service.pack.yaml"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if p.Metadata.Name != "payment-service" {
		t.Fatalf("name: got %q", p.Metadata.Name)
	}
	if p.Metadata.Bindings.Criticality != "tier-1" {
		t.Fatalf("criticality: got %q", p.Metadata.Bindings.Criticality)
	}
	if got := len(p.Spec.SLIs); got < 1 {
		t.Fatalf("expected ≥1 SLI, got %d", got)
	}
}

func TestSchemaPassesOnExample(t *testing.T) {
	root := repoRoot(t)
	p, err := pack.Load(filepath.Join(root, "examples", "payment-service.pack.yaml"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	r := &lint.Result{}
	if err := lint.Schema(p, filepath.Join(root, "schema", "observability-pack.schema.json"), r); err != nil {
		t.Fatalf("schema: %v", err)
	}
	if !r.SchemaOK {
		t.Errorf("expected schema to pass; findings: %v", r.Findings)
	}
}

func TestConformanceProducesAllTiersForTier1(t *testing.T) {
	root := repoRoot(t)
	p, err := pack.Load(filepath.Join(root, "examples", "payment-service.pack.yaml"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	r := &lint.Result{Criticality: p.Metadata.Bindings.Criticality}
	lint.Conformance(p, r)
	if r.TierTarget != "tier-1" {
		t.Fatalf("tier_target: got %q", r.TierTarget)
	}
	if len(r.Conformance.Tier3) == 0 || len(r.Conformance.Tier2) == 0 || len(r.Conformance.Tier1) == 0 {
		t.Fatalf("expected all three tier rubrics, got 3=%d 2=%d 1=%d",
			len(r.Conformance.Tier3), len(r.Conformance.Tier2), len(r.Conformance.Tier1))
	}
}
