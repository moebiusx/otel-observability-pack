// Package lint runs validation passes against a parsed ObservabilityPack.
//
// Three independent checks compose the lint result:
//
//  1. Schema     — JSON Schema (draft 2020-12) compliance.
//  2. References — every ref:/slis./slos. token resolves.
//  3. Conformance — tier-3/2/1 maturity rubric from docs/maturity-model.md §3.
package lint

import (
	"fmt"
	"strings"
)

// Severity ranks a finding.
type Severity string

const (
	SeverityError Severity = "error"
	SeverityWarn  Severity = "warn"
	SeverityInfo  Severity = "info"
)

// Finding is a single issue surfaced by a lint pass.
type Finding struct {
	Severity Severity `json:"severity" yaml:"severity"`
	Code     string   `json:"code"     yaml:"code"`
	Path     string   `json:"path"     yaml:"path"`
	Message  string   `json:"message"  yaml:"message"`
}

// Result aggregates findings across all passes plus a conformance verdict
// matching docs/maturity-model.md §7 output shape.
type Result struct {
	Pack        string `json:"pack"        yaml:"pack"`
	Version     string `json:"version"     yaml:"version"`
	Service     string `json:"service"     yaml:"service"`
	Criticality string `json:"criticality" yaml:"criticality"`

	SchemaOK bool `json:"schema_ok"    yaml:"schema_ok"`
	RefsOK   bool `json:"refs_ok"      yaml:"refs_ok"`

	TierTarget  string `json:"tier_target"  yaml:"tier_target"`
	TierReached string `json:"tier_reached" yaml:"tier_reached"`

	Findings []Finding `json:"findings"     yaml:"findings"`

	Conformance ConformanceReport `json:"conformance"  yaml:"conformance"`
}

// ConformanceReport reports which rubric items passed/failed per tier.
type ConformanceReport struct {
	Tier3 []Check `json:"tier_3" yaml:"tier_3"`
	Tier2 []Check `json:"tier_2" yaml:"tier_2"`
	Tier1 []Check `json:"tier_1" yaml:"tier_1"`
}

// Check is one rubric line item.
type Check struct {
	ID     string `json:"id"      yaml:"id"`
	Title  string `json:"title"   yaml:"title"`
	Pass   bool   `json:"pass"    yaml:"pass"`
	Detail string `json:"detail,omitempty" yaml:"detail,omitempty"`
}

// HasErrors returns true if any finding is at error severity.
func (r *Result) HasErrors() bool {
	for _, f := range r.Findings {
		if f.Severity == SeverityError {
			return true
		}
	}
	return false
}

// AddFinding appends a finding to the result.
func (r *Result) AddFinding(sev Severity, code, path, msg string) {
	r.Findings = append(r.Findings, Finding{
		Severity: sev,
		Code:     code,
		Path:     path,
		Message:  msg,
	})
}

// AddFindingf is the formatted version of AddFinding.
func (r *Result) AddFindingf(sev Severity, code, path, format string, a ...any) {
	r.AddFinding(sev, code, path, fmt.Sprintf(format, a...))
}

// Summary renders a one-line human verdict for CI logs.
func (r *Result) Summary() string {
	var b strings.Builder
	if r.HasErrors() {
		b.WriteString("FAIL")
	} else {
		b.WriteString("PASS")
	}
	fmt.Fprintf(&b, " pack=%s service=%s criticality=%s tier_target=%s tier_reached=%s schema=%v refs=%v findings=%d",
		r.Pack, r.Service, r.Criticality, r.TierTarget, r.TierReached, r.SchemaOK, r.RefsOK, len(r.Findings))
	return b.String()
}
