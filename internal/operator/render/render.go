// Package render produces the unstructured Kubernetes object payloads
// that the in-cluster tool adapters submit to other operators. Renderers
// are pure functions: input is a Plan, output is one or more
// unstructured-shaped maps. The controller decides whether to apply
// them, dry-run them, or surface them on Pack.Status.
//
// Keeping rendering pure means we can test the operator's behaviour
// against Prometheus Operator, Grafana Operator, and OpenTelemetry
// Operator contracts without bringing their type imports into this repo.
package render

import (
	"fmt"

	"github.com/example/observability-pack/internal/operator/plan"
)

// Object is the minimal shape every rendered payload satisfies. It maps
// directly to *unstructured.Unstructured.Object in controller-runtime
// without forcing this package to depend on apimachinery.
type Object map[string]any

// Prometheus renders a single PrometheusRule custom resource owned by
// the Prometheus Operator (monitoring.coreos.com/v1). One rule group
// per pack keeps reverse lookups simple ("which alerts come from pack
// X?" is a label match).
func Prometheus(pl *plan.Plan, namespace string) Object {
	if pl == nil {
		return nil
	}
	rules := make([]any, 0, len(pl.Prometheus.RecordingRules)+len(pl.Prometheus.AlertRules))
	for _, rr := range pl.Prometheus.RecordingRules {
		entry := map[string]any{
			"record": rr.Name,
			"expr":   rr.Expr,
		}
		if len(rr.Labels) > 0 {
			entry["labels"] = stringMap(rr.Labels)
		}
		rules = append(rules, entry)
	}
	for _, ar := range pl.Prometheus.AlertRules {
		entry := map[string]any{
			"alert": ar.Alert,
			"expr":  ar.Expr,
		}
		if ar.For != "" {
			entry["for"] = ar.For
		}
		if len(ar.Labels) > 0 {
			entry["labels"] = stringMap(ar.Labels)
		}
		if len(ar.Annotations) > 0 {
			entry["annotations"] = stringMap(ar.Annotations)
		}
		rules = append(rules, entry)
	}

	return Object{
		"apiVersion": "monitoring.coreos.com/v1",
		"kind":       "PrometheusRule",
		"metadata": map[string]any{
			"name":      fmt.Sprintf("pack-%s", pl.Pack),
			"namespace": namespace,
			"labels":    ownerLabels(pl),
		},
		"spec": map[string]any{
			"groups": []any{
				map[string]any{
					"name":  pl.Prometheus.GroupName,
					"rules": rules,
				},
			},
		},
	}
}

// Grafana renders one GrafanaDashboard CR per dashboard in the plan, as
// owned by the Grafana Operator (grafana.integreatly.org/v1beta1).
// File-sourced dashboards are referenced by URL; template dashboards
// embed their parameters so the operator's renderer can hydrate them.
func Grafana(pl *plan.Plan, namespace string) []Object {
	if pl == nil {
		return nil
	}
	out := make([]Object, 0, len(pl.Grafana.Dashboards))
	for _, d := range pl.Grafana.Dashboards {
		spec := map[string]any{
			"folder": pl.Grafana.Folder,
		}
		switch {
		case d.Source != "":
			spec["url"] = d.Source
		case d.Template != "":
			spec["template"] = d.Template
			if len(d.Params) > 0 {
				spec["params"] = d.Params
			}
		}
		out = append(out, Object{
			"apiVersion": "grafana.integreatly.org/v1beta1",
			"kind":       "GrafanaDashboard",
			"metadata": map[string]any{
				"name":      fmt.Sprintf("pack-%s-%s", pl.Pack, d.ID),
				"namespace": namespace,
				"labels":    ownerLabels(pl),
			},
			"spec": spec,
		})
	}
	return out
}

// OTel renders an OpenTelemetryCollector CR (opentelemetry.io/v1beta1)
// describing the gateway pipeline. The OpenTelemetry Operator owns the
// pod lifecycle; we only own the configuration intent.
func OTel(pl *plan.Plan, namespace string) Object {
	if pl == nil {
		return nil
	}
	receivers := map[string]any{}
	for _, r := range pl.OTel.Receivers {
		if r.Name == "" {
			continue
		}
		receivers[r.Name] = stripName(r.Config)
	}
	processors := map[string]any{}
	for _, p := range pl.OTel.Processors {
		if p.Name == "" {
			continue
		}
		processors[p.Name] = stripName(p.Config)
	}
	exporters := map[string]any{}
	if pl.OTel.Exporters.Metrics != nil {
		exporters["metrics"] = pl.OTel.Exporters.Metrics
	}
	if pl.OTel.Exporters.Logs != nil {
		exporters["logs"] = pl.OTel.Exporters.Logs
	}
	if pl.OTel.Exporters.Traces != nil {
		exporters["traces"] = pl.OTel.Exporters.Traces
	}

	return Object{
		"apiVersion": "opentelemetry.io/v1beta1",
		"kind":       "OpenTelemetryCollector",
		"metadata": map[string]any{
			"name":      fmt.Sprintf("pack-%s-gateway", pl.Pack),
			"namespace": namespace,
			"labels":    ownerLabels(pl),
		},
		"spec": map[string]any{
			"mode": "deployment",
			"config": map[string]any{
				"receivers":  receivers,
				"processors": processors,
				"exporters":  exporters,
			},
			"resourceAttributes": map[string]any{
				"required": stringSliceToAny(pl.OTel.RequiredAttr),
				"semconv":  pl.OTel.SemConv,
			},
		},
	}
}

func ownerLabels(pl *plan.Plan) map[string]any {
	return map[string]any{
		"app.kubernetes.io/managed-by":          "observability-pack-operator",
		"observability.platform/pack":           pl.Pack,
		"observability.platform/pack-version":   pl.Version,
		"observability.platform/pack-service":   pl.Service,
		"observability.platform/effective-hash": pl.Hash,
	}
}

func stringMap(in map[string]string) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// stringSliceToAny widens a []string to []any so the value satisfies
// k8s DeepCopyJSON (which rejects typed slices).
func stringSliceToAny(in []string) []any {
	out := make([]any, len(in))
	for i, v := range in {
		out[i] = v
	}
	return out
}

// stripName removes the synthetic `name` field we use internally so the
// rendered config matches what the OTel Collector schema expects.
func stripName(in map[string]any) map[string]any {
	if in == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		if k == "name" {
			continue
		}
		out[k] = v
	}
	return out
}
