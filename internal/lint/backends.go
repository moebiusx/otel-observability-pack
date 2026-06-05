package lint

import (
	"strings"

	"github.com/example/observability-pack/internal/pack"
)

// knownProducts is the "known" set of the open product registry. Unknown
// products are accepted (open registry) but flagged so authors catch typos.
// The set tracks the products the otel-mcp-server can address.
var knownProducts = map[string]struct{}{
	// traces
	"jaeger": {}, "tempo": {}, "zipkin": {}, "skywalking": {}, "pinpoint": {},
	// metrics
	"prometheus": {}, "mimir": {}, "thanos": {}, "victoriametrics": {},
	"influxdb": {}, "opentsdb": {},
	// logs
	"elasticsearch": {}, "opensearch": {}, "loki": {}, "clickhouse": {}, "graylog": {},
	// dashboards / visualisation
	"grafana": {}, "kibana": {}, "perses": {},
	// profiling / networking / policy
	"pyroscope": {}, "cilium": {}, "kubernetes": {}, "opa": {},
	// service mesh / gateways
	"envoy": {}, "consul": {}, "kong": {}, "traefik": {},
	// collection pipelines
	"fluentbit": {}, "beats": {}, "vector": {}, "alloy": {},
	// alerting
	"alertmanager": {},
}

// Backends validates the generalized telemetry backend catalog, environment
// overlays, the optional signal dimensions (profiling/network/policy_engine/
// mesh/collection), and version gating. It enforces:
//
//   - unique backend ids;
//   - product names against the open registry (warn on unknown);
//   - version gating (declared vs min/max) per the backend's gating mode;
//   - that every intra-pack backend reference resolves to a declared backend.
//
// Gating semantics mirror the MCP server's MCP_VERSION_GATING:
// enforce -> error, warn -> warn, off -> skipped. An absent gating mode
// defaults to warn.
func Backends(p *pack.Pack, r *Result) {
	ids := stringSet()

	// 1. Backend catalog.
	for i, b := range p.Spec.Telemetry.Backends {
		base := refPath("spec.telemetry.backends", i, "")
		base = strings.TrimSuffix(base, ".")
		if ids.has(b.ID) {
			r.AddFindingf(SeverityError, "backends/duplicate_id", base,
				"duplicate backend id %q", b.ID)
		}
		ids.add(b.ID)
		checkProduct(r, base+".product", b.Product)
		evalVersion(r, base+".version", b.Product, b.Version)
	}

	// 2. Environment overlays — backend refs and criticality overrides.
	for name, env := range p.Spec.Environments {
		for signal, ref := range env.Backends {
			path := "spec.environments." + name + ".backends." + signal
			resolveBackendRef(r, path, ref, ids)
		}
	}

	// 3. Optional signal dimensions.
	checkSignalBlock(r, "spec.profiling", p.Spec.Profiling, ids)
	checkSignalBlock(r, "spec.network", p.Spec.Network, ids)
	checkSignalBlock(r, "spec.policy_engine", p.Spec.PolicyEngine, ids)

	for i, c := range p.Spec.Mesh {
		base := refPath("spec.mesh", i, "")
		base = strings.TrimSuffix(base, ".")
		checkProduct(r, base+".product", c.Product)
		evalVersion(r, base+".version", c.Product, c.Version)
		if c.Backend != "" {
			resolveBackendRef(r, base+".backend", c.Backend, ids)
		}
	}
	for i, c := range p.Spec.Collection {
		base := refPath("spec.collection", i, "")
		base = strings.TrimSuffix(base, ".")
		checkProduct(r, base+".product", c.Product)
		evalVersion(r, base+".version", c.Product, c.Version)
		if c.Backend != "" {
			resolveBackendRef(r, base+".backend", c.Backend, ids)
		}
	}

	// 4. Storage version gating (min_version/gating live alongside version).
	for _, signal := range []string{"metrics", "logs", "traces"} {
		blk, ok := p.Spec.Storage[signal].(map[string]any)
		if !ok {
			continue
		}
		path := "spec.storage." + signal
		product, _ := blk["backend"].(string)
		checkProduct(r, path+".backend", product)
		declared, _ := blk["version"].(string)
		min, _ := blk["min_version"].(string)
		gating, _ := blk["gating"].(string)
		evalVersion(r, path, product, &pack.VersionSpec{Declared: declared, Min: min, Gating: gating})
		if ref, ok := blk["backend_ref"].(string); ok {
			resolveBackendRef(r, path+".backend_ref", ref, ids)
		}
	}
}

func checkSignalBlock(r *Result, path string, b *pack.SignalBlock, ids *strSet) {
	if b == nil {
		return
	}
	if b.Backend != "" {
		resolveBackendRef(r, path+".backend", b.Backend, ids)
	}
	if b.Product != "" {
		checkProduct(r, path+".product", b.Product)
	}
	evalVersion(r, path+".version", b.Product, b.Version)
}

// checkProduct flags products not in the known registry. Open registry:
// unknown products are allowed but surfaced as a warning.
func checkProduct(r *Result, path, product string) {
	if product == "" {
		return
	}
	if _, ok := knownProducts[product]; !ok {
		r.AddFindingf(SeverityWarn, "registry/unknown_product", path,
			"product %q is not in the known registry; accepted but verify it is supported by the MCP server", product)
	}
}

// evalVersion applies version gating for a single product/version pair.
func evalVersion(r *Result, path, product string, v *pack.VersionSpec) {
	if v == nil || v.Declared == "" {
		return
	}
	gating := v.Gating
	if gating == "" {
		gating = "warn"
	}
	if gating == "off" {
		return
	}
	sev := SeverityWarn
	if gating == "enforce" {
		sev = SeverityError
	}
	if v.Min != "" && versionLT(v.Declared, v.Min) {
		r.AddFindingf(sev, "versions/below_min", path,
			"%s version %s is below the minimum %s (gating=%s)", product, v.Declared, v.Min, gating)
	}
	if v.Max != "" && versionLT(v.Max, v.Declared) {
		r.AddFindingf(sev, "versions/above_max", path,
			"%s version %s is above the maximum %s (gating=%s)", product, v.Declared, v.Max, gating)
	}
}

// resolveBackendRef verifies a backend reference resolves to a declared
// backend id. External refs (ref:...) are deferred to the import resolver.
func resolveBackendRef(r *Result, path, ref string, ids *strSet) {
	if ref == "" {
		r.AddFindingf(SeverityError, "backends/empty_ref", path, "backend reference is empty")
		return
	}
	if strings.HasPrefix(ref, "ref:") {
		r.AddFindingf(SeverityInfo, "backends/external", path,
			"external backend reference %q deferred to import resolver", ref)
		return
	}
	if !ids.has(ref) {
		r.AddFindingf(SeverityError, "backends/unresolved_ref", path,
			"backend reference %q does not resolve to any spec.telemetry.backends id", ref)
	}
}

// versionLT reports whether version a is strictly less than b. It tolerates
// 1-, 2-, or 3-component numeric versions (e.g. "8.15", "2.55", "1.27.0"),
// padding missing components with zero and ignoring any non-numeric suffix.
func versionLT(a, b string) bool {
	av := parseVersionParts(a)
	bv := parseVersionParts(b)
	for i := 0; i < 3; i++ {
		if av[i] != bv[i] {
			return av[i] < bv[i]
		}
	}
	return false
}

func parseVersionParts(s string) [3]int {
	var out [3]int
	parts := strings.SplitN(strings.TrimSpace(s), ".", 3)
	for i := 0; i < 3 && i < len(parts); i++ {
		n := 0
		for _, c := range parts[i] {
			if c < '0' || c > '9' {
				break
			}
			n = n*10 + int(c-'0')
		}
		out[i] = n
	}
	return out
}
