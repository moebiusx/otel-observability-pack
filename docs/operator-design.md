# ObservabilityPack Operator — Design Sketch

**Document status:** Design proposal
**Companion to:** *ObservabilityPack — Platform Engineering Standard*
**Binding:** `otel-elastic-prometheus-grafana` (see `bindings/otel-elastic-prometheus-grafana.md`)
**Audience:** Platform engineers building the reconciliation layer.

---

## 1. Purpose

The ObservabilityPack manifest is declarative and binding-aware. The operator is the controller that turns a pack into the concrete, native resources that the **OTel Collector**, **Prometheus**, **Elasticsearch**, **Grafana**, **Alertmanager**, **Argo Workflows**, and **Chaos Mesh** understand. It is the only component permitted to write to those backends; everything else reads pack manifests from Git.

This document describes the operator's responsibilities, control loops, reconciliation model, failure handling, and the contract it exposes to other platform components.

---

## 2. High-level architecture

```
┌────────────────────────┐            ┌────────────────────────┐
│  Git (service repos)   │   GitOps   │  Pack Registry         │
│  /observability/*.yaml │ ─────────▶ │  (ConfigMap + index)   │
└────────────────────────┘            └────────────┬───────────┘
                                                   │
                                                   ▼
                                       ┌─────────────────────────┐
                                       │  Pack Operator          │
                                       │  (Kubernetes controller)│
                                       └────┬────────┬───────────┘
                                            │        │
   ┌────────────────────────────────────────┘        └────────────────────────────────────┐
   ▼                                                                                      ▼
┌──────────────────────┐  ┌─────────────────────────┐  ┌─────────────────────┐  ┌─────────────────────┐
│ OpenTelemetry Operator│ │ Prometheus + Mimir rules│  │ Grafana provisioning│  │ Alertmanager + PD   │
│ (Collector CR agents/ │ │ (vmalert-equiv ruler)   │  │ (folder per pack)   │  │ (routes + receivers)│
│  gateway, Instr CRs)  │ │                         │  │                     │  │                     │
└──────────────────────┘  └─────────────────────────┘  └─────────────────────┘  └─────────────────────┘
┌──────────────────────┐  ┌─────────────────────────┐  ┌─────────────────────┐  ┌─────────────────────┐
│ Elasticsearch ILM    │  │ Argo Workflows + Sensors│  │ Chaos Mesh CRDs     │  │ Elastic Synthetics  │
│  + index templates   │  │ (self-healing)          │  │ (validation)        │  │ + blackbox-exporter │
└──────────────────────┘  └─────────────────────────┘  └─────────────────────┘  └─────────────────────┘
```

Three logical components:

1. **Pack Registry** — the in-cluster materialisation of every Git-stored pack, plus a derived index that lets reverse-lookup ("which packs depend on platform/std-alert-routes@3.0?") run in O(1).
2. **Pack Operator** — a Kubernetes controller that watches the registry and reconciles each pack into per-section sub-controllers.
3. **Sub-controllers** — one per backend, each owning a small, focused reconciliation against its target system.

The split between the operator and the sub-controllers exists for blast-radius reasons: a bug in the chaos-experiment renderer should not be able to take down the alert routing pipeline.

---

## 3. Pack lifecycle

### 3.1 Submission and validation

1. Service team commits a pack change to their service repo.
2. Repo CI runs `packlint`:
   - JSON Schema validation
   - Reference-integrity check (every `binds_to`, `ref:`, `trigger:` resolves)
   - Conformance check against the pack's declared criticality tier
   - Cardinality estimate vs. live staging metrics
3. PR merges. A GitOps agent (Argo CD / Flux) syncs the pack into the registry namespace as a `Pack` custom resource.

### 3.2 Reconciliation

The operator runs a standard observe → diff → apply loop, with the following stages per pack:

| Stage | Inputs | Outputs |
|---|---|---|
| Resolve | Pack manifest + transitive imports | Effective pack (overlaid) |
| Plan | Effective pack + last-applied snapshot | Per-backend deltas |
| Apply | Per-backend deltas | Native resources written |
| Verify | Native resource status | Pack `.status` updated |

The Plan stage is idempotent and side-effect-free; it is exposed as a dry-run mode for review.

### 3.3 Removal

Deleting a pack triggers a cascade through every sub-controller in reverse order: chaos and synthetic first (so we stop generating alerts), then alerting routes (so we stop notifying), then dashboards, then recording rules, and finally scrape jobs. The order matters — if we tear down recording rules before alerts, the alerts go silent for the wrong reason.

---

## 4. Sub-controllers

Each sub-controller owns one section of the pack and one backend. They share a common framework: a label-scoped owner reference, a finalizer, and a status sub-resource that surfaces reconciliation health back to the parent `Pack`.

### 4.1 SLI / SLO sub-controller

**Owns:** `spec.slis`, `spec.slos`
**Renders to:** Sloth `PrometheusServiceLevel` CRDs (or OpenSLO custom resources where Sloth is unavailable).

Responsibilities:
- Materialise each SLI as a recording rule under the canonical `<service>:<sli>:<aggregation>_<window>` naming.
- Materialise each SLO with its objective and window. The error-budget burn-rate recording rules follow.
- Resolve `error_budget_policy` references (typically to a platform-default policy) and inline the policy into the generated CRD.

### 4.1b OTel sub-controller

**Owns:** `spec.otel`
**Renders to:** OpenTelemetry Operator `Instrumentation` CRs + gateway Collector validation rules.

Responsibilities:
- Materialise an `Instrumentation` CR per declared SDK language (java, node, python, ...) so application pods get auto-instrumentation injected via the OTel Operator's mutating webhook.
- Inject `OTEL_RESOURCE_ATTRIBUTES` env vars into application pods, populated from `metadata.bindings.service` / `criticality` / `environments` and the declared `resource_attributes.custom` list.
- Configure the gateway Collector with a validation policy that drops or flags telemetry missing any attribute in `resource_attributes.required`. The default action is `flag-and-emit-anomaly-metric`; tier-1 packs may upgrade to `drop`.
- Pin the SemConv version: the operator refuses to reconcile a pack whose `otel.semconv` is below the binding's floor or above the latest known release.

### 4.2 Pipelines sub-controller

**Owns:** `spec.pipelines`
**Renders to:** OTel Collector configs (agent + gateway), via the OpenTelemetry Operator's `OpenTelemetryCollector` CR.

Responsibilities:
- Generate two collector deployments per cluster: a DaemonSet **agent** (per-node, receives OTLP from local apps and prometheusreceiver scrapes for nearby pods), and a Deployment **gateway** (receives from agents, runs tail sampling and heavy processing, exports to backends).
- Compose the agent and gateway configs as the union of all packs in the cluster, keyed by `service.name`. Per-service receivers, processors, and exporters live as named blocks; the operator assembles them at apply time.
- Validate that every declared SLI's `semconv_metric` is present in the OpenTelemetry Semantic Conventions for the pinned version. Unknown metric names are a hard reject — they typically indicate a typo or unintentional drift from SemConv.
- Enforce cardinality protection: at apply time, compare the declared per-receiver series budget to the live Prometheus cardinality. Reject configs that would exceed 5x the budget without an explicit override.

### 4.3 Storage sub-controller

**Owns:** `spec.storage`
**Renders to:** Prometheus retention flags + Mimir/Thanos remote_write configs (metrics); Elasticsearch index templates + ILM policies + data streams (logs and traces).

Responsibilities:
- **Metrics:** generate the `remote_write` block on the local Prometheus pointed at the Mimir/Thanos endpoint declared in `storage.metrics.remote_write`. Local TSDB retention is set per pack tier (default 30 days for tier-1, 14 for tier-2). Long-window queries are served by Mimir transparently behind the Prom query frontend.
- **Logs:** ensure the named ES data stream exists, attach the referenced ILM policy (`storage.logs.ilm_policy`), and apply a per-service index template that maps OTel attributes to ES fields per the OTLP-to-ECS mapping conventions.
- **Traces:** same pattern as logs, but using the `traces-apm-default` data stream and `std-traces-ilm-14d` ILM policy by default. Tail sampling decisions are recorded as a span attribute so investigators know why a trace was kept.
- Validate retention against compliance constraints stored in the platform's data-classification registry — a pack labelled `pii_class: high` cannot declare a logs retention longer than the regulatory ceiling.

### 4.4 Queries sub-controller

**Owns:** `spec.queries.recording_rules`, `spec.queries.derived_views`
**Renders to:** vmalert recording-rule files.

Responsibilities:
- Produce recording rules with `ref:slis.<id>` references resolved against the pack's SLI definitions.
- Validate that every recording rule's expression parses as a valid MetricsQL/PromQL expression at apply time.

### 4.5 Dashboards sub-controller

**Owns:** `spec.dashboards`
**Renders to:** Grafana provisioned dashboards.

Responsibilities:
- For source-based dashboards, fetch the JSON from the declared file:// URI (resolved to the service repo at the pack's revision), apply panel bindings (substituting target query expressions in named panels), and PUT to Grafana via its provisioning API.
- For template-based dashboards, fetch the platform template, apply parameters, and PUT.
- Tag every dashboard with `pack=<name>` and `version=<version>` for ownership traceability.

### 4.6 Policy sub-controller

**Owns:** `spec.policy`
**Renders to:** vmalert alert rules.

Responsibilities:
- Generate multi-window burn-rate alert rules following the pattern from the SRE workbook: `(burn_rate_short > factor) AND (burn_rate_long > factor)`.
- Generate forecast alert rules for declared forecasts (Holt-Winters projections evaluated by the platform's forecasting service, exposed as derived metrics).

### 4.7 Alerting sub-controller

**Owns:** `spec.alerting`
**Renders to:** Alertmanager routing trees, PagerDuty service mappings.

Responsibilities:
- The Alertmanager config is global and shared across all packs; the controller maintains it as an aggregate, with a per-pack route block matched on the `pack=<name>` label injected into every alert.
- Provision PagerDuty services and integration keys via the PagerDuty API for any pack declaring a `voice:` channel; rotate keys on a quarterly schedule.
- Apply suppression contexts: `maintenance_windows` come from a platform CR; `deploy_freezes` come from the change-management system; `dependency_outage` is auto-applied when an upstream pack's primary SLO breaches.

### 4.8 Remediation sub-controller

**Owns:** `spec.remediation`
**Renders to:** Argo Workflows `WorkflowTemplate` + `EventSource` + `Sensor` resources.

Responsibilities:
- For each remediation, materialise an Argo Events Sensor that listens for the alert webhook and triggers the named WorkflowTemplate.
- Materialise guardrails as preconditions in the Sensor: `max_invocations_per_hour` becomes a rate-limited Sensor trigger; `requires_human_above` becomes a conditional that pauses the workflow for approval at higher severities; `circuit_breaker` is a counter resource that the Sensor checks before firing.

### 4.9 Baselines sub-controller

**Owns:** `spec.baselines`
**Renders to:** A platform observability service that derives MTTD/MTTR continuously.

Responsibilities:
- Subscribe to alert-fired events and incident-management lifecycle events; compute MTTD and MTTR per incident; aggregate to p50 and p95 over a rolling 30-day window.
- Compare against declared targets; if rolling p50/p95 breaches the target, write the regression to the pack's `.status` and (per `regression_gate`) signal the release gate.

### 4.10 Validation sub-controller

**Owns:** `spec.validation`
**Renders to:** Chaos Mesh `Workflow` CRDs, Blackbox/Checkly probe configs, scheduled CronJobs that orchestrate experiment runs.

Responsibilities:
- Schedule chaos experiments at the declared cadence, in the declared environment.
- Snapshot the steady-state hypothesis (the SLO ratio) before each run; assert that during the fault the expected alerts fire within `expected_mttd`; assert recovery to steady-state after the fault.
- Record pass/fail to the pack's validation history, surfaced on the conformance dashboard.

---

## 5. Cross-cutting concerns

### 5.1 Change ordering and atomicity

Within a single reconciliation, sub-controllers apply in this order:

1. SLI/SLO (and recording rules) — produce the data.
2. Storage — ensure retention is in place before alerts depend on it.
3. Queries (additional recording rules) — derived signals.
4. Dashboards — readers.
5. Policy — define when "bad" is significant.
6. Alerting — route significance.
7. Remediation — react to routing.
8. Baselines, Validation — reflect on the whole.

If any stage fails, downstream stages are skipped for this pack and the pack's `.status.phase` is set to `Degraded` with a precise failure reference. Already-applied stages are not rolled back unless an explicit `rollback` annotation is set.

### 5.2 Drift detection

A periodic reconcile (default 5m) compares the live state of every sub-controller's target backend against the desired state derived from the pack. Any out-of-band change is reverted, with the diff logged to an audit channel. This is what enforces the rule that direct edits in Grafana, Alertmanager, etc. are non-binding.

### 5.3 Multi-tenant isolation

Every native resource the operator creates is labelled with `pack=<name>` and `team=<owner>`. Backends that support label-scoped RBAC (Grafana folders, Alertmanager routes, vmalert rule groups) are configured so that a pack can only ever modify resources tagged with its own label. A bug or compromise in one pack cannot delete another pack's dashboards or alerts.

### 5.4 Observability of the operator

The operator must itself be observable. It exposes its own metrics (reconciliation latency, queue depth, error rate, drift events per minute) and emits Kubernetes events for every reconciliation. The platform team maintains a meta-pack named `platform/observability-operator` that governs the operator the same way every other pack governs its service. This is how we avoid the "the watchman is unwatched" anti-pattern.

### 5.5 Versioning

The operator and the pack schema are versioned independently. Backwards compatibility for one minor version is required: an operator at v1.4.x must accept and correctly reconcile any pack whose `apiVersion` is `observability.platform/v1`. Major version bumps follow a deprecation cycle of at least one full release window.

---

## 6. Failure modes and mitigations

| Failure | Detection | Mitigation |
|---|---|---|
| Sub-controller panics on a malformed pack | Operator metrics + Kubernetes restart loop | Validation in pack lint should make this rare; a panic is treated as a P1 platform incident. |
| Backend unavailable (Grafana down) | Apply-stage error | Pack status set to `Degraded`; reconciliation retried with exponential backoff; alerts already in place continue to fire. |
| Cardinality explosion mid-reconcile | Cardinality probe at apply time | Apply rejected; pack status set to `Blocked`; PagerDuty notification to the platform team. |
| Drift loop with a downstream tool | Drift events per minute exceeds threshold | Sub-controller pauses drift correction for the affected resource and surfaces the conflict for human review. |
| Schema-only changes that the running operator can't parse | Pack lint at PR time uses operator-shipped schema | Operator and schema are released together; service teams may pin their `packlint` to the operator version they target. |
| Imported pack version goes missing | Resolve stage error | Pack status set to `Degraded`; uses last successfully-applied effective pack until the import is restored. |

---

## 7. Out-of-scope (for v1)

- **Multi-cluster federation.** v1 of the operator runs per cluster; cross-cluster aggregation (e.g. a single dashboard spanning two clusters) is delivered by the dashboard layer's federated data sources, not by the operator.
- **Self-modification.** The operator does not modify packs in response to observed behaviour. Forecast-driven warnings open tickets; they don't automatically lower SLOs.
- **Cost optimisation suggestions.** A separate FinOps controller may consume pack manifests to recommend retention or sampling changes, but it does not write packs.

---

## 8. Implementation notes

- **Language:** Go, using controller-runtime for the controller framework.
- **CRDs:** `Pack`, `EffectivePack` (read-only, post-import resolution), per-backend status CRs (`PackSlothStatus`, `PackGrafanaStatus`, etc.).
- **Storage of last-applied state:** Annotation on each native resource (`platform.observability/last-applied-hash`), plus a per-pack ConfigMap snapshot for diffing.
- **Testing:** Each sub-controller has a fake-backend test rig; integration tests run the full operator against ephemeral instances of VictoriaMetrics, Grafana, Alertmanager, and Chaos Mesh in CI.
- **Rollout:** the operator is deployed by the platform itself via the same GitOps flow it serves, with the meta-pack as its first-class governance.
