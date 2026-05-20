package lint

import (
	"regexp"
	"strings"

	"github.com/example/observability-pack/internal/pack"
)

// Refs walks the pack and verifies that every intra-pack reference
// (slis.<id>, slos.<id>, or bare alert/SLI id used by remediation/dashboards)
// resolves to a defined object. Cross-pack imports (ref:platform/...) are
// recorded as info findings — they require the import resolver, not yet
// implemented here.
func Refs(p *pack.Pack, r *Result) {
	sliIDs := stringSet()
	for _, s := range p.Spec.SLIs {
		sliIDs.add(s.ID)
	}
	sloIDs := stringSet()
	for _, s := range p.Spec.SLOs {
		sloIDs.add(s.ID)
	}
	alertIDs := stringSet() // alert names referenced by remediation triggers

	// SLO -> SLI
	for i, slo := range p.Spec.SLOs {
		checkRef(r, "slos."+slo.ID+".sli", slo.SLI, sliIDs, sloIDs, "sli")
		_ = i
	}

	// Policy burn-rate alerts -> SLO; record their derived alert names
	for i, br := range p.Spec.Policy.BurnRateAlerts {
		path := refPath("spec.policy.burn_rate_alerts", i, "slo")
		checkRef(r, path, br.SLO, sliIDs, sloIDs, "slo")
		// Sloth-style alert naming: <slo>_burn_<short>_<long>
		for _, w := range br.Windows {
			alertIDs.add(burnAlertName(stripRef(br.SLO), w))
		}
	}

	// Forecasts -> SLO
	for i, f := range p.Spec.Policy.Forecasts {
		path := refPath("spec.policy.forecasts", i, "slo")
		checkRef(r, path, f.SLO, sliIDs, sloIDs, "slo")
	}

	// Remediation triggers -> known alert
	for i, rm := range p.Spec.Remediation {
		path := refPath("spec.remediation", i, "trigger")
		name := strings.TrimPrefix(rm.Trigger, "alert:")
		if name == rm.Trigger {
			r.AddFindingf(SeverityError, "refs/bad_trigger", path,
				"remediation trigger %q must start with alert:", rm.Trigger)
			continue
		}
		if !alertIDs.has(name) {
			// Soft warning — generators may name alerts differently; we
			// surface it so an author can verify.
			r.AddFindingf(SeverityWarn, "refs/unknown_alert", path,
				"remediation references alert %q which is not produced by any policy.burn_rate_alerts entry", name)
		}
	}

	// Dashboard panel bindings -> SLI / SLO
	for di, d := range p.Spec.Dashboards {
		for pi, pb := range d.PanelBindings {
			path := refPath("spec.dashboards", di, "panel_bindings."+itoa(pi)+".binds_to")
			checkRef(r, path, pb.BindsTo, sliIDs, sloIDs, "sli_or_slo")
		}
	}

	// Validation expected_alerts -> known alert (warn only)
	for vi, exp := range p.Spec.Validation.ChaosExperiments {
		if list, ok := exp["expected_alerts"].([]any); ok {
			for ai, name := range list {
				if n, ok := name.(string); ok && !alertIDs.has(n) {
					path := refPath("spec.validation.chaos_experiments", vi, "expected_alerts."+itoa(ai))
					r.AddFindingf(SeverityWarn, "refs/unknown_alert", path,
						"chaos experiment expects alert %q which is not produced by any policy.burn_rate_alerts entry", n)
				}
			}
		}
	}

	r.RefsOK = !r.HasErrors()
}

// checkRef resolves ref under the most permissive interpretation requested
// by kind (sli|slo|sli_or_slo). External refs (ref:...) are accepted.
func checkRef(r *Result, path, ref string, slis, slos *strSet, kind string) {
	if ref == "" {
		r.AddFindingf(SeverityError, "refs/empty", path, "reference is empty")
		return
	}
	if strings.HasPrefix(ref, "ref:") {
		// External / platform-import ref. Not resolvable here.
		r.AddFindingf(SeverityInfo, "refs/external", path,
			"external reference %q deferred to import resolver", ref)
		return
	}
	target, id := splitRef(ref)
	switch kind {
	case "sli":
		if (target == "" || target == "slis") && slis.has(id) {
			return
		}
	case "slo":
		if (target == "" || target == "slos") && slos.has(id) {
			return
		}
	case "sli_or_slo":
		if target == "" || target == "slis" {
			if slis.has(id) {
				return
			}
		}
		if target == "" || target == "slos" {
			if slos.has(id) {
				return
			}
		}
	}
	r.AddFindingf(SeverityError, "refs/unresolved", path,
		"reference %q does not resolve to a defined %s", ref, kind)
}

// splitRef handles "slis.foo", "slos.bar", or bare "foo".
func splitRef(ref string) (target, id string) {
	if i := strings.IndexByte(ref, '.'); i > 0 {
		return ref[:i], ref[i+1:]
	}
	return "", ref
}

func stripRef(ref string) string {
	_, id := splitRef(ref)
	return id
}

// burnAlertName mirrors the convention emitted by the policy renderer.
// Kept stable so triggers in spec.remediation can match.
func burnAlertName(sloID string, w pack.BurnWindow) string {
	short := sanitize(w.Short)
	long := sanitize(w.Long)
	return sloID + "_burn_" + short + "_" + long
}

var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

func sanitize(s string) string {
	return nonAlnum.ReplaceAllString(strings.ToLower(s), "")
}

// --- tiny string-set helper to avoid pulling in a dependency ---

type strSet struct{ m map[string]struct{} }

func stringSet() *strSet            { return &strSet{m: map[string]struct{}{}} }
func (s *strSet) add(v string)      { s.m[v] = struct{}{} }
func (s *strSet) has(v string) bool { _, ok := s.m[v]; return ok }

func itoa(i int) string {
	// avoid strconv import noise
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

func refPath(base string, idx int, leaf string) string {
	return base + "[" + itoa(idx) + "]." + leaf
}
