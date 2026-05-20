package lint

import (
	"fmt"
	"strings"

	"github.com/example/observability-pack/internal/pack"
)

// Conformance scores the pack against the tier required by its criticality
// binding (per docs/maturity-model.md §2) and populates result.Conformance
// plus result.TierTarget/TierReached.
//
// The tiers are cumulative: tier-1 = tier-3 ∪ tier-2 ∪ tier-1 additions.
// A pack is conformant at tier T if every MUST clause at every tier T..3
// passes.
func Conformance(p *pack.Pack, r *Result) {
	target := p.Metadata.Bindings.Criticality
	if target == "" {
		target = "tier-3"
	}
	r.TierTarget = target

	r.Conformance.Tier3 = scoreTier3(p)
	if target == "tier-2" || target == "tier-1" {
		r.Conformance.Tier2 = scoreTier2(p)
	}
	if target == "tier-1" {
		r.Conformance.Tier1 = scoreTier1(p)
	}

	r.TierReached = reachedTier(r.Conformance)

	// MUST failures at the *target* tier and below are error findings; failures
	// at tiers above the target are info-only (aspirational).
	emitFindings(r, r.Conformance.Tier3, "tier-3", target)
	if len(r.Conformance.Tier2) > 0 {
		emitFindings(r, r.Conformance.Tier2, "tier-2", target)
	}
	if len(r.Conformance.Tier1) > 0 {
		emitFindings(r, r.Conformance.Tier1, "tier-1", target)
	}
}

// reachedTier returns the highest tier where every check passes.
func reachedTier(c ConformanceReport) string {
	t3 := allPass(c.Tier3)
	t2 := len(c.Tier2) > 0 && allPass(c.Tier2)
	t1 := len(c.Tier1) > 0 && allPass(c.Tier1)
	switch {
	case t3 && t2 && t1:
		return "tier-1"
	case t3 && t2:
		return "tier-2"
	case t3:
		return "tier-3"
	default:
		return "none"
	}
}

func allPass(checks []Check) bool {
	for _, c := range checks {
		if !c.Pass {
			return false
		}
	}
	return true
}

func emitFindings(r *Result, checks []Check, tier, target string) {
	sev := SeverityError
	if tierRank(tier) < tierRank(target) {
		// Tier above target — informational, not blocking
		sev = SeverityInfo
	}
	for _, c := range checks {
		if c.Pass {
			continue
		}
		r.AddFinding(sev, "conformance/"+tier+"/"+c.ID,
			"spec", c.Title+": "+c.Detail)
	}
}

// tierRank: lower number = stricter tier. Used to compare "is tier-1 stricter than tier-3?".
func tierRank(t string) int {
	switch t {
	case "tier-1":
		return 1
	case "tier-2":
		return 2
	default:
		return 3
	}
}

// --- rubric implementations (one check per clause in the maturity model) ---

func scoreTier3(p *pack.Pack) []Check {
	out := []Check{}

	// 3.1 SLIs/SLOs
	out = append(out, Check{
		ID:     "3.1",
		Title:  "At least one SLI and one SLO declared",
		Pass:   len(p.Spec.SLIs) >= 1 && len(p.Spec.SLOs) >= 1,
		Detail: detailf("len(slis)=%d len(slos)=%d", len(p.Spec.SLIs), len(p.Spec.SLOs)),
	})

	// 3.2 Pipelines — otlp or prometheus receiver
	hasDefaultReceiver := false
	for _, rx := range p.Spec.Pipelines.Receivers {
		if n, _ := rx["name"].(string); n == "otlp" || n == "prometheus" {
			hasDefaultReceiver = true
			break
		}
	}
	out = append(out, Check{
		ID:    "3.2",
		Title: "Default OTel Collector pipeline (otlp or prometheus receiver)",
		Pass:  hasDefaultReceiver,
	})

	// 3.2b OTel resource.required contains service.name
	hasSvcName := containsStr(p.Spec.Otel.ResourceAttributes.Required, "service.name")
	out = append(out, Check{
		ID:    "3.2b",
		Title: "spec.otel.resource_attributes.required includes service.name",
		Pass:  hasSvcName,
	})

	// 3.3 Storage default (we treat "absent" as pass; an explicit storage block
	// is allowed to set anything — schema covers correctness).
	out = append(out, Check{
		ID:     "3.3",
		Title:  "Platform-default retention applied (no overrides forbidden, just absent at tier-3)",
		Pass:   true,
		Detail: "informational — schema enforces values when storage is present",
	})

	// 3.4 Recording rule per SLO
	rrSLO := stringSet()
	for _, rr := range p.Spec.Queries.RecordingRules {
		for _, sloID := range sloIDs(p) {
			if strings.Contains(rr.Name, sloID) || strings.Contains(rr.Expr, sloID) {
				rrSLO.add(sloID)
			}
		}
	}
	missing := []string{}
	for _, id := range sloIDs(p) {
		if !rrSLO.has(id) {
			missing = append(missing, id)
		}
	}
	out = append(out, Check{
		ID:     "3.4",
		Title:  "Recording rule references every SLO id",
		Pass:   len(missing) == 0,
		Detail: detailf("missing SLOs: %v", missing),
	})

	// 3.5 service-overview dashboard with panel bindings covering every SLI
	soDash, soFound := findDashboard(p, "service-overview")
	soOK := false
	soDetail := "no dashboard with id=service-overview"
	if soFound {
		covered := stringSet()
		for _, pb := range soDash.PanelBindings {
			_, id := splitRef(pb.BindsTo)
			covered.add(id)
		}
		missingSLI := []string{}
		for _, s := range p.Spec.SLIs {
			if !covered.has(s.ID) {
				missingSLI = append(missingSLI, s.ID)
			}
		}
		soOK = len(missingSLI) == 0
		if !soOK {
			soDetail = detailf("missing SLI bindings: %v", missingSLI)
		} else {
			soDetail = ""
		}
	}
	out = append(out, Check{
		ID:     "3.5",
		Title:  "Service-overview dashboard binds every SLI",
		Pass:   soOK,
		Detail: soDetail,
	})

	// 3.6 At least one alert rule per SLO
	out = append(out, Check{
		ID:     "3.6",
		Title:  "At least one burn-rate alert per SLO",
		Pass:   len(p.Spec.Policy.BurnRateAlerts) >= len(p.Spec.SLOs),
		Detail: detailf("burn_rate_alerts=%d slos=%d", len(p.Spec.Policy.BurnRateAlerts), len(p.Spec.SLOs)),
	})

	// 3.7 At least one chat (msteams) channel for SEV3+
	chatOK := false
	for _, route := range p.Spec.Alerting.Routes {
		if route.Severity == "SEV3" || route.Severity == "SEV2" || route.Severity == "SEV1" {
			for _, ch := range route.Channels {
				if _, ok := ch["msteams"]; ok {
					chatOK = true
				}
			}
		}
	}
	out = append(out, Check{
		ID:    "3.7",
		Title: "Chat channel (msteams) routed for SEV3 or higher",
		Pass:  chatOK,
	})

	// 3.8 Self-healing optional — always pass
	out = append(out, Check{
		ID:    "3.8",
		Title: "Self-healing optional at tier-3",
		Pass:  true,
	})

	// 3.9 MTTD + MTTR p50 declared
	out = append(out, Check{
		ID:    "3.9",
		Title: "MTTD and MTTR p50 targets declared",
		Pass:  p.Spec.Baselines.MTTDTargetP50 != "" && p.Spec.Baselines.MTTRTargetP50 != "",
	})

	// 3.10 At least one synthetic probe
	out = append(out, Check{
		ID:    "3.10",
		Title: "At least one synthetic probe declared",
		Pass:  len(p.Spec.Validation.SyntheticChecks) >= 1,
	})

	return out
}

func scoreTier2(p *pack.Pack) []Check {
	out := []Check{}

	// 2.1 At least one latency-style SLI (threshold or distribution)
	hasLat := false
	for _, s := range p.Spec.SLIs {
		if s.Type == "threshold" || s.Type == "distribution" {
			hasLat = true
			break
		}
	}
	out = append(out, Check{
		ID:    "2.1",
		Title: "At least one latency SLI (type=threshold|distribution)",
		Pass:  hasLat,
	})

	// 2.2 metrics exporter is prometheusremotewrite
	mkind, _ := p.Spec.Pipelines.Exporters.Metrics["kind"].(string)
	out = append(out, Check{
		ID:     "2.2",
		Title:  "Metrics exporter kind == prometheusremotewrite",
		Pass:   mkind == "prometheusremotewrite",
		Detail: detailf("got kind=%q", mkind),
	})

	// 2.2b SemConv ≥ 1.26.0 and at least one SDK language
	out = append(out, Check{
		ID:     "2.2b",
		Title:  "SemConv ≥ 1.26.0 and ≥1 SDK language declared",
		Pass:   semverGTE(p.Spec.Otel.SemConv, "1.26.0") && len(p.Spec.Otel.SDK.Languages) >= 1,
		Detail: detailf("semconv=%s languages=%v", p.Spec.Otel.SemConv, p.Spec.Otel.SDK.Languages),
	})

	// 2.3 At least one derived_view
	out = append(out, Check{
		ID:    "2.3",
		Title: "≥1 derived_view referencing a platform template",
		Pass:  len(p.Spec.Queries.DerivedViews) >= 1,
	})

	// 2.4 slo-burn dashboard exists
	_, found := findDashboard(p, "slo-burn")
	out = append(out, Check{
		ID:    "2.4",
		Title: "slo-burn dashboard present",
		Pass:  found,
	})

	// 2.5 Multi-window burn-rate (≥2 windows, distinct factors) on availability SLOs
	allMW := true
	for _, br := range p.Spec.Policy.BurnRateAlerts {
		if len(br.Windows) < 2 {
			allMW = false
			break
		}
		factors := stringSet()
		for _, w := range br.Windows {
			factors.add(detailf("%v", w.Factor))
		}
		if len(factors.m) < 2 {
			allMW = false
			break
		}
	}
	out = append(out, Check{
		ID:    "2.5",
		Title: "Every burn-rate alert has ≥2 windows with distinct factors",
		Pass:  allMW && len(p.Spec.Policy.BurnRateAlerts) > 0,
	})

	// 2.6 SEV1 route has voice channel
	voiceOK := false
	for _, route := range p.Spec.Alerting.Routes {
		if route.Severity != "SEV1" {
			continue
		}
		for _, ch := range route.Channels {
			if _, ok := ch["voice"]; ok {
				voiceOK = true
			}
		}
	}
	out = append(out, Check{
		ID:    "2.6",
		Title: "SEV1 route includes a voice channel",
		Pass:  voiceOK,
	})

	// 2.7 regression_gate set
	out = append(out, Check{
		ID:    "2.7",
		Title: "Regression detection enabled (any regression_gate value)",
		Pass:  p.Spec.Baselines.RegressionGate != "",
	})

	// 2.8 Monthly+ chaos in staging covering primary SLO
	monthly := false
	for _, exp := range p.Spec.Validation.ChaosExperiments {
		sched, _ := exp["schedule"].(string)
		_, hasHyp := exp["steady_state_hypothesis"]
		if (sched == "monthly" || sched == "weekly" || sched == "daily") && hasHyp {
			monthly = true
			break
		}
	}
	out = append(out, Check{
		ID:    "2.8",
		Title: "Chaos experiment scheduled monthly+ with SLO hypothesis",
		Pass:  monthly,
	})

	return out
}

func scoreTier1(p *pack.Pack) []Check {
	out := []Check{}

	// 1.1 ≥3 distinct SLI types
	types := stringSet()
	for _, s := range p.Spec.SLIs {
		types.add(s.Type)
	}
	out = append(out, Check{
		ID:     "1.1",
		Title:  "≥3 distinct SLI types (proxy for domain coverage)",
		Pass:   len(types.m) >= 3,
		Detail: detailf("got types=%v", keysOf(types)),
	})

	// 1.2 logs + traces to elasticsearch + tail_sampling processor
	lkind, _ := p.Spec.Pipelines.Exporters.Logs["kind"].(string)
	tkind, _ := p.Spec.Pipelines.Exporters.Traces["kind"].(string)
	tail := false
	for _, pr := range p.Spec.Pipelines.Processors {
		if n, _ := pr["name"].(string); n == "tail_sampling" {
			tail = true
			break
		}
	}
	out = append(out, Check{
		ID:     "1.2",
		Title:  "logs+traces exported to elasticsearch and tail_sampling processor present",
		Pass:   lkind == "elasticsearch" && tkind == "elasticsearch" && tail,
		Detail: detailf("logs.kind=%q traces.kind=%q tail_sampling=%v", lkind, tkind, tail),
	})

	// 1.2b SemConv pinned to current (1.27.0), log_correlation, ≥5 required attrs
	out = append(out, Check{
		ID:    "1.2b",
		Title: "SemConv == 1.27.0, log_correlation=true, ≥5 required resource attributes",
		Pass:  p.Spec.Otel.SemConv == "1.27.0" && p.Spec.Otel.SDK.LogCorrelation && len(p.Spec.Otel.ResourceAttributes.Required) >= 5,
		Detail: detailf("semconv=%s log_correlation=%v required=%d",
			p.Spec.Otel.SemConv, p.Spec.Otel.SDK.LogCorrelation, len(p.Spec.Otel.ResourceAttributes.Required)),
	})

	// 1.2c at least one OTel-instrumented synthetic check
	otelSyn := false
	for _, s := range p.Spec.Validation.SyntheticChecks {
		if s.OTelInstrumentation {
			otelSyn = true
			break
		}
	}
	out = append(out, Check{
		ID:    "1.2c",
		Title: "≥1 OTel-instrumented synthetic check",
		Pass:  otelSyn,
	})

	// 1.3 per-tenant / per-region rollup derived view
	rollup := false
	for _, dv := range p.Spec.Queries.DerivedViews {
		if strings.Contains(dv.Bind, "per-tenant") || strings.Contains(dv.Bind, "per-region") ||
			strings.Contains(dv.ID, "per-tenant") || strings.Contains(dv.ID, "per-region") {
			rollup = true
			break
		}
	}
	out = append(out, Check{
		ID:    "1.3",
		Title: "Per-tenant or per-region rollup derived view present",
		Pass:  rollup,
	})

	// 1.4 deployment + customer-impact dashboards
	_, dep := findDashboard(p, "deployment")
	_, ci := findDashboard(p, "customer-impact")
	out = append(out, Check{
		ID:    "1.4",
		Title: "deployment and customer-impact dashboards both present",
		Pass:  dep && ci,
	})

	// 1.5 forecast on availability SLO
	out = append(out, Check{
		ID:    "1.5",
		Title: "≥1 policy.forecasts entry",
		Pass:  len(p.Spec.Policy.Forecasts) >= 1,
	})

	// 1.6 SEV1 third channel beyond chat + voice (whatsapp / email / webhook)
	third := false
	for _, route := range p.Spec.Alerting.Routes {
		if route.Severity != "SEV1" {
			continue
		}
		have := stringSet()
		for _, ch := range route.Channels {
			for k := range ch {
				have.add(k)
			}
		}
		extras := 0
		for _, k := range []string{"whatsapp", "email", "webhook"} {
			if have.has(k) {
				extras++
			}
		}
		if extras >= 1 && have.has("msteams") && have.has("voice") {
			third = true
			break
		}
	}
	out = append(out, Check{
		ID:    "1.6",
		Title: "SEV1 route has chat + voice + out-of-band channel (whatsapp|email|webhook)",
		Pass:  third,
	})

	// 1.7 ≥1 remediation with max_invocations_per_hour
	remOK := len(p.Spec.Remediation) >= 1
	if remOK {
		for _, rm := range p.Spec.Remediation {
			if rm.Guardrails.MaxInvocationsPerHour < 1 {
				remOK = false
				break
			}
		}
	}
	out = append(out, Check{
		ID:    "1.7",
		Title: "≥1 remediation with max_invocations_per_hour guardrail",
		Pass:  remOK,
	})

	// 1.8 regression_gate starts with block_release_if_ and p95 targets set
	rg := p.Spec.Baselines.RegressionGate
	out = append(out, Check{
		ID:    "1.8",
		Title: "regression_gate is a block_release_if_* variant and p95 targets set",
		Pass:  strings.HasPrefix(rg, "block_release_if_") && p.Spec.Baselines.MTTDTargetP95 != "" && p.Spec.Baselines.MTTRTargetP95 != "",
		Detail: detailf("regression_gate=%q mttd_p95=%q mttr_p95=%q",
			rg, p.Spec.Baselines.MTTDTargetP95, p.Spec.Baselines.MTTRTargetP95),
	})

	// 1.9 weekly chaos in prod
	wp := false
	for _, exp := range p.Spec.Validation.ChaosExperiments {
		sched, _ := exp["schedule"].(string)
		env, _ := exp["environment"].(string)
		if sched == "weekly" && env == "prod" {
			wp = true
			break
		}
	}
	out = append(out, Check{
		ID:    "1.9",
		Title: "Weekly production chaos experiment present",
		Pass:  wp,
	})

	// 1.10 synthetic at ≤1m interval
	fast := false
	for _, s := range p.Spec.Validation.SyntheticChecks {
		if isDurationLEMinute(s.Interval) {
			fast = true
			break
		}
	}
	out = append(out, Check{
		ID:    "1.10",
		Title: "≥1 synthetic check at ≤1m interval",
		Pass:  fast,
	})

	return out
}

// --- helpers ---

func sloIDs(p *pack.Pack) []string {
	out := make([]string, 0, len(p.Spec.SLOs))
	for _, s := range p.Spec.SLOs {
		out = append(out, s.ID)
	}
	return out
}

func findDashboard(p *pack.Pack, id string) (pack.Dashboard, bool) {
	for _, d := range p.Spec.Dashboards {
		if d.ID == id {
			return d, true
		}
	}
	return pack.Dashboard{}, false
}

func containsStr(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func keysOf(s *strSet) []string {
	out := make([]string, 0, len(s.m))
	for k := range s.m {
		out = append(out, k)
	}
	return out
}

// semverGTE returns true if a >= b for simple semver (MAJOR.MINOR.PATCH, no
// pre-release). Returns false on parse failure.
func semverGTE(a, b string) bool {
	av, ok := parseSemver(a)
	if !ok {
		return false
	}
	bv, ok := parseSemver(b)
	if !ok {
		return false
	}
	for i := 0; i < 3; i++ {
		if av[i] != bv[i] {
			return av[i] > bv[i]
		}
	}
	return true
}

func parseSemver(s string) ([3]int, bool) {
	var out [3]int
	parts := strings.SplitN(s, ".", 3)
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n := 0
		for _, c := range p {
			if c < '0' || c > '9' {
				return out, false
			}
			n = n*10 + int(c-'0')
		}
		out[i] = n
	}
	return out, true
}

// isDurationLEMinute returns true for durations <= 1m. Crude but sufficient
// for tier-1 §1.10: covers s, ms, us, ns, and exactly "1m".
func isDurationLEMinute(d string) bool {
	d = strings.TrimSpace(d)
	if d == "" {
		return false
	}
	if d == "1m" || d == "60s" {
		return true
	}
	for _, suf := range []string{"ms", "us", "µs", "ns", "s"} {
		if strings.HasSuffix(d, suf) {
			if suf == "s" {
				// parse number, must be <= 60
				numStr := strings.TrimSuffix(d, "s")
				n := 0
				for _, c := range numStr {
					if c < '0' || c > '9' {
						return false
					}
					n = n*10 + int(c-'0')
				}
				return n <= 60
			}
			return true
		}
	}
	return false
}

func detailf(format string, a ...any) string {
	return fmt.Sprintf(format, a...)
}
