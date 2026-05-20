// Package plan turns an effective ObservabilityPack into per-adapter
// intents. It is the side-effect-free middle of the reconciliation loop:
// the controller calls Build, validates the result, and only then hands
// the intents to the tool adapters. The Plan is also exposed verbatim by
// the `--dry-run` mode of the operator and the equivalent CLI command.
package plan

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	apiv1 "github.com/example/observability-pack/api/v1alpha1"
	"github.com/example/observability-pack/internal/pack"
)

// Plan is the deterministic output of the Plan stage. Adapters consume
// the slices that match their responsibility; the controller does not
// inspect intent contents.
type Plan struct {
	Pack       string             `json:"pack"`
	Version    string             `json:"version"`
	Service    string             `json:"service"`
	Target     apiv1.Target       `json:"target"`
	Hash       string             `json:"hash"`
	Prometheus PrometheusIntent   `json:"prometheus"`
	Grafana    GrafanaIntent      `json:"grafana"`
	OTel       OTelIntent         `json:"otel"`
	Azure      *AzurePipelineCall `json:"azure,omitempty"`
}

// PrometheusIntent collects the rules the Prometheus Operator should own
// for this pack. Burn-rate alerts derive from spec.policy; recording
// rules derive from spec.queries.recording_rules.
type PrometheusIntent struct {
	GroupName      string          `json:"groupName"`
	RecordingRules []RecordingRule `json:"recordingRules"`
	AlertRules     []AlertRule     `json:"alertRules"`
}

// RecordingRule is a Prometheus recording rule, expressed in the smallest
// shape needed for rendering. We intentionally do not depend on
// prometheus-operator's Go types here.
type RecordingRule struct {
	Name   string            `json:"name"`
	Expr   string            `json:"expr"`
	Labels map[string]string `json:"labels,omitempty"`
}

// AlertRule is a Prometheus alert rule.
type AlertRule struct {
	Alert       string            `json:"alert"`
	Expr        string            `json:"expr"`
	For         string            `json:"for,omitempty"`
	Severity    string            `json:"severity"`
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// GrafanaIntent collects dashboards to provision.
type GrafanaIntent struct {
	Folder     string         `json:"folder"`
	Dashboards []DashboardRef `json:"dashboards"`
}

// DashboardRef points at the dashboard source. Either Source (URL/file)
// or Template + Params is set.
type DashboardRef struct {
	ID       string         `json:"id"`
	Source   string         `json:"source,omitempty"`
	Template string         `json:"template,omitempty"`
	Params   map[string]any `json:"params,omitempty"`
}

// OTelIntent collects the OpenTelemetry Collector contract.
type OTelIntent struct {
	SemConv      string         `json:"semconv"`
	RequiredAttr []string       `json:"requiredAttributes"`
	Receivers    []OTelReceiver `json:"receivers"`
	Processors   []OTelProc     `json:"processors"`
	Exporters    OTelExporters  `json:"exporters"`
}

type OTelReceiver struct {
	Name   string         `json:"name"`
	Config map[string]any `json:"config,omitempty"`
}

type OTelProc struct {
	Name   string         `json:"name"`
	Config map[string]any `json:"config,omitempty"`
}

type OTelExporters struct {
	Metrics map[string]any `json:"metrics,omitempty"`
	Logs    map[string]any `json:"logs,omitempty"`
	Traces  map[string]any `json:"traces,omitempty"`
}

// AzurePipelineCall describes the pipeline invocation the Azure tool
// adapter will perform. The actual trigger happens in the adapter; this
// is the planned payload, captured deterministically so a dry-run can
// surface it for review.
type AzurePipelineCall struct {
	Provider string         `json:"provider"`
	Pipeline string         `json:"pipeline"`
	Branch   string         `json:"branch,omitempty"`
	Payload  map[string]any `json:"payload"`
}

// Build constructs a Plan from a parsed pack and the requested target.
// Build never returns nil: errors propagate from missing required fields,
// not from optional sections being absent.
func Build(p *pack.Pack, target apiv1.Target, azure *apiv1.AzurePipelineRef) (*Plan, error) {
	if p == nil {
		return nil, fmt.Errorf("plan.Build: nil pack")
	}
	if p.Metadata.Name == "" {
		return nil, fmt.Errorf("plan.Build: metadata.name required")
	}

	pl := &Plan{
		Pack:    p.Metadata.Name,
		Version: p.Metadata.Version,
		Service: p.Metadata.Bindings.Service,
		Target:  target,
	}
	pl.Prometheus = buildPrometheus(p)
	pl.Grafana = buildGrafana(p)
	pl.OTel = buildOTel(p)

	if target == apiv1.TargetAzure {
		if azure == nil || azure.Pipeline == "" {
			return nil, fmt.Errorf("plan.Build: target=azure requires azurePipeline.pipeline")
		}
		pl.Azure = &AzurePipelineCall{
			Provider: azure.Provider,
			Pipeline: azure.Pipeline,
			Branch:   azure.Branch,
			Payload: map[string]any{
				"pack":          p.Metadata.Name,
				"version":       p.Metadata.Version,
				"service":       p.Metadata.Bindings.Service,
				"environments":  p.Metadata.Bindings.Environments,
				"criticality":   p.Metadata.Bindings.Criticality,
				"effectiveSpec": p.Raw,
			},
		}
	}

	pl.Hash = hashPlan(pl)
	return pl, nil
}

func buildPrometheus(p *pack.Pack) PrometheusIntent {
	out := PrometheusIntent{GroupName: p.Metadata.Bindings.Service}

	for _, rr := range p.Spec.Queries.RecordingRules {
		out.RecordingRules = append(out.RecordingRules, RecordingRule{
			Name:   rr.Name,
			Expr:   rr.Expr,
			Labels: cloneLabels(rr.Labels, p),
		})
	}

	for _, br := range p.Spec.Policy.BurnRateAlerts {
		for _, w := range br.Windows {
			alert := AlertRule{
				Alert:    fmt.Sprintf("%s_burn_%s_%s", br.SLO, w.Short, w.Long),
				Expr:     fmt.Sprintf("burn_rate{slo=\"%s\",window=\"%s\"} > %g and burn_rate{slo=\"%s\",window=\"%s\"} > %g", br.SLO, w.Short, w.Factor, br.SLO, w.Long, w.Factor),
				For:      w.Short,
				Severity: w.Severity,
				Labels: map[string]string{
					"pack":     p.Metadata.Name,
					"service":  p.Metadata.Bindings.Service,
					"slo":      br.SLO,
					"severity": w.Severity,
				},
				Annotations: map[string]string{
					"summary":     fmt.Sprintf("SLO %s burn rate exceeded (%s/%s)", br.SLO, w.Short, w.Long),
					"description": fmt.Sprintf("Burn rate factor %g over short=%s and long=%s windows.", w.Factor, w.Short, w.Long),
				},
			}
			out.AlertRules = append(out.AlertRules, alert)
		}
	}
	return out
}

func buildGrafana(p *pack.Pack) GrafanaIntent {
	out := GrafanaIntent{Folder: p.Metadata.Bindings.Service}
	for _, d := range p.Spec.Dashboards {
		out.Dashboards = append(out.Dashboards, DashboardRef{
			ID:       d.ID,
			Source:   d.Source,
			Template: d.Template,
			Params:   d.Params,
		})
	}
	return out
}

func buildOTel(p *pack.Pack) OTelIntent {
	out := OTelIntent{
		SemConv:      p.Spec.Otel.SemConv,
		RequiredAttr: append([]string(nil), p.Spec.Otel.ResourceAttributes.Required...),
	}
	for _, r := range p.Spec.Pipelines.Receivers {
		out.Receivers = append(out.Receivers, OTelReceiver{
			Name:   stringField(r, "name"),
			Config: copyMap(r),
		})
	}
	for _, pr := range p.Spec.Pipelines.Processors {
		out.Processors = append(out.Processors, OTelProc{
			Name:   stringField(pr, "name"),
			Config: copyMap(pr),
		})
	}
	out.Exporters = OTelExporters{
		Metrics: p.Spec.Pipelines.Exporters.Metrics,
		Logs:    p.Spec.Pipelines.Exporters.Logs,
		Traces:  p.Spec.Pipelines.Exporters.Traces,
	}
	return out
}

func cloneLabels(labels map[string]string, p *pack.Pack) map[string]string {
	out := map[string]string{
		"pack":    p.Metadata.Name,
		"service": p.Metadata.Bindings.Service,
	}
	for k, v := range labels {
		out[k] = v
	}
	return out
}

func copyMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func stringField(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// hashPlan returns a stable digest of the plan, used as the
// `last-applied` annotation source so adapters can detect drift without
// re-comparing every field.
func hashPlan(p *Plan) string {
	type sortable struct {
		Pack       string
		Version    string
		Service    string
		Target     apiv1.Target
		Prometheus []byte
		Grafana    []byte
		OTel       []byte
		Azure      []byte
	}

	sortStrings := func(s []string) []string { c := append([]string(nil), s...); sort.Strings(c); return c }

	prom := PrometheusIntent{GroupName: p.Prometheus.GroupName}
	prom.RecordingRules = append(prom.RecordingRules, p.Prometheus.RecordingRules...)
	prom.AlertRules = append(prom.AlertRules, p.Prometheus.AlertRules...)
	sort.Slice(prom.RecordingRules, func(i, j int) bool { return prom.RecordingRules[i].Name < prom.RecordingRules[j].Name })
	sort.Slice(prom.AlertRules, func(i, j int) bool { return prom.AlertRules[i].Alert < prom.AlertRules[j].Alert })

	graf := GrafanaIntent{Folder: p.Grafana.Folder}
	graf.Dashboards = append(graf.Dashboards, p.Grafana.Dashboards...)
	sort.Slice(graf.Dashboards, func(i, j int) bool { return graf.Dashboards[i].ID < graf.Dashboards[j].ID })

	otel := p.OTel
	otel.RequiredAttr = sortStrings(otel.RequiredAttr)

	s := sortable{
		Pack:    p.Pack,
		Version: p.Version,
		Service: p.Service,
		Target:  p.Target,
	}
	s.Prometheus, _ = json.Marshal(prom)
	s.Grafana, _ = json.Marshal(graf)
	s.OTel, _ = json.Marshal(otel)
	if p.Azure != nil {
		s.Azure, _ = json.Marshal(p.Azure)
	}
	b, _ := json.Marshal(s)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
