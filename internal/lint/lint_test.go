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

func TestBackendsPassIsCleanOnExample(t *testing.T) {
	root := repoRoot(t)
	p, err := pack.Load(filepath.Join(root, "examples", "payment-service.pack.yaml"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	r := &lint.Result{}
	lint.Backends(p, r)
	for _, f := range r.Findings {
		if f.Severity == lint.SeverityError {
			t.Errorf("unexpected backends error: [%s] %s %s", f.Code, f.Path, f.Message)
		}
	}
}

func TestBackendsFlagsRegistryVersionAndRefs(t *testing.T) {
	p := &pack.Pack{
		Spec: pack.Spec{
			Telemetry: pack.Telemetry{
				Backends: []pack.Backend{
					{ID: "m1", Signal: "metrics", Product: "prometheus",
						Version: &pack.VersionSpec{Declared: "2.40", Min: "2.53", Gating: "enforce"}},
					{ID: "x1", Signal: "logs", Product: "notareal",
						Version: &pack.VersionSpec{Declared: "1.0", Min: "0.5", Gating: "warn"}},
				},
			},
			Profiling: &pack.SignalBlock{Backend: "does-not-exist"},
		},
	}
	r := &lint.Result{}
	lint.Backends(p, r)

	want := map[string]lint.Severity{
		"versions/below_min":       lint.SeverityError, // enforce -> error
		"registry/unknown_product": lint.SeverityWarn,
		"backends/unresolved_ref":  lint.SeverityError,
	}
	got := map[string]lint.Severity{}
	for _, f := range r.Findings {
		got[f.Code] = f.Severity
	}
	for code, sev := range want {
		if got[code] != sev {
			t.Errorf("expected finding %q at severity %q; got %q", code, sev, got[code])
		}
	}
}

func TestBackendsGatingOffSkipsVersionCheck(t *testing.T) {
	p := &pack.Pack{
		Spec: pack.Spec{
			Telemetry: pack.Telemetry{
				Backends: []pack.Backend{
					{ID: "p1", Signal: "profiles", Product: "pyroscope",
						Version: &pack.VersionSpec{Declared: "0.1", Min: "9.9", Gating: "off"}},
				},
			},
		},
	}
	r := &lint.Result{}
	lint.Backends(p, r)
	for _, f := range r.Findings {
		if f.Code == "versions/below_min" {
			t.Errorf("gating=off must skip version check, but got %s", f.Message)
		}
	}
}
