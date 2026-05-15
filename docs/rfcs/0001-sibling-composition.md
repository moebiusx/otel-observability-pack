# RFC-0001 — Sibling Composition

| | |
|---|---|
| RFC | 0001 |
| Title | Sibling composition of ObservabilityPacks |
| Status | **Draft — request for comments** |
| Author | Platform Engineering |
| Created | 2026-05-13 |
| Target version | v1.2 |
| Discussion | issues/comments on the repo, or platform-engineering@ |

---

## 1. Summary

v1.1 of the ObservabilityPack standard supports *vertical* composition: a service pack inherits from one or more platform-level base packs via `metadata.imports`, with later imports overriding earlier ones. This RFC proposes *horizontal* composition: multiple sibling packs that together describe a single service's observability surface, each owned by a different team and contributing a disjoint slice of the spec.

The target use case is a service whose observability is genuinely co-owned by multiple parties — a contractually-driven slice (SLA, audit, governance), a platform-driven slice (vendor / infrastructure telemetry), and an application-driven slice (per-application SLIs and alerts). v1.1 forces these into a single file with mixed ownership; v1.2 should let each owner ship their slice independently while the operator composes them into one effective pack at apply time.

The proposal is intentionally additive. `metadata.imports` keeps its existing semantics. A new `metadata.composes` block opts a pack into sibling composition. Packs that don't use `composes` are unaffected.

---

## 2. Motivation

### 2.1 The pattern shows up in practice

A real-world inventory of a tier-1 service often splits naturally along three axes:

| Slice | Owner | Driving force |
|---|---|---|
| SLA / contractual | Risk & compliance, KrystalineX Platform / incident management | External SLA, regulatory reporting |
| Platform / BAU | Platform engineering, observability infrastructure | Vendor telemetry, infrastructure health |
| Application / client | Application team | Per-feature SLIs, business-domain alerts |

These three groups have:

- Different release cadences (compliance changes quarterly; platform changes monthly; application changes weekly or daily).
- Different review processes (compliance changes need legal sign-off; platform changes need infra peer review; application changes need feature-team peer review).
- Different blast-radius tolerances (a misconfigured contractual alert is a regulatory event; a misconfigured application alert is a Slack-channel embarrassment).
- Different on-call escalation paths.

v1.1 makes all three slices share a single YAML file. That forces:

- All three owners to review every PR that touches any part of the pack.
- One team's release cadence to bottleneck the others.
- `CODEOWNERS` rules that span line ranges rather than files (impossible in GitHub today).
- One team's typo or regression to block the others' deploys via the operator's atomic-reconcile model.

### 2.2 The mechanism is already half-built

v1.1's `metadata.imports` is conceptually similar to what we want, but its merge semantics are wrong for this use case:

| | `imports` (v1.1) | `composes` (proposed v1.2) |
|---|---|---|
| Direction | Vertical (parent → child) | Horizontal (sibling ↔ sibling) |
| Merge semantics | Overlay; later wins | Disjoint union; conflict = error |
| Typical use | Pull in platform defaults | Split co-owned service across teams |
| ID collisions | Silently overridden | CI failure |
| Ownership | Whoever owns the leaf | Whoever owns each contributing sibling |

Both mechanisms coexist in v1.2. A sibling slice can still `imports` platform defaults; the parent aggregator just `composes` the siblings.

---

## 3. Detailed design

### 3.1 New top-level shape

A pack opts into sibling composition by declaring `metadata.composes`. When present, `spec` is typically empty or near-empty on the parent and is computed from the union of the siblings' `spec` blocks.

```yaml
# kx-exchange.pack.yaml — the aggregator
apiVersion: observability.platform/v1
kind: ObservabilityPack
metadata:
  name: kx-exchange
  version: 1.0.0
  binding: otel-elastic-prometheus-grafana
  owners: [team-platform-sre]              # owners of the AGGREGATION, not the contents
  composes:
    - ref: ./kx-exchange-sla.pack.yaml
    - ref: ./kx-exchange-bau.pack.yaml
    - ref: ./kx-exchange-client.pack.yaml
  bindings:
    service: kx-exchange
    environments: [prod, staging]
    criticality: tier-1
spec: {}                                    # nothing — siblings provide it
```

Each sibling is itself a full pack manifest, but scoped to the slice its owner is accountable for, and declares `metadata.contributes` listing the sections it owns:

```yaml
# kx-exchange-sla.pack.yaml
apiVersion: observability.platform/v1
kind: ObservabilityPack
metadata:
  name: kx-exchange-sla
  version: 2.4.1
  owners: [team-krystalinex-platform, team-compliance]
  contributes:
    - slis
    - slos
    - dashboards
    - alerting.routes:           # nested-section ownership
        match: { source: sla }
    - policy.burn_rate_alerts:
        match: { tag: sla }
    - validation.governance      # the G 01, G 02 audit-evidence items
  bindings:
    service: kx-exchange              # MUST match the aggregator's service
    environments: [prod, staging]
    criticality: tier-1
spec:
  slis:
    - id: sla.service_availability_composite
      type: ratio
      # ...
  slos:
    - id: sla.error_budget_99_95
      sli: sla.service_availability_composite
      objective: 0.9995
      # ...
  # ... etc, only the SLA-owned content
```

### 3.2 ID namespacing

Each sibling MUST namespace its identifiers under a single prefix (typically derived from `metadata.name`'s suffix after the last hyphen, but explicit is better).

```yaml
metadata:
  name: kx-exchange-bau
  namespace: bau              # all IDs in this sibling MUST start with "bau."
  contributes: [...]
```

In the composed effective pack, an ID looks like `<namespace>.<local-id>`. Cross-sibling references resolve through these namespaced IDs:

```yaml
# inside kx-exchange-bau.pack.yaml — references its own SLI
policy:
  burn_rate_alerts:
    - slo: bau.broker_availability_99_999
      windows: [...]

# inside kx-exchange-sla.pack.yaml — references a BAU SLI (cross-sibling, allowed)
slos:
  - id: sla.error_budget_99_95
    sli: bau.broker_availability_ratio    # explicit cross-sibling ref
    # ...
```

Cross-sibling references are *allowed but must be acyclic*. The composer builds a dependency graph; cycles are a CI error. This permits the natural shape where SLA-defined SLOs reference BAU-collected SLIs.

### 3.3 Merge semantics

The operator's `Resolve` stage gains a new `Compose` sub-stage when `composes` is present:

1. **Load all siblings.** Fetch each ref, validate each individually against the schema.
2. **Check ownership claims.** Each sibling's `contributes` block is matched against its `spec`. A sibling that declares it contributes only `slis` but actually contains `dashboards` is a CI failure.
3. **Check namespacing.** Every ID in a sibling MUST start with `<sibling.namespace>.`. Violations are CI errors.
4. **Check disjointness.** No two siblings may declare the same fully-namespaced ID. Collisions are CI errors.
5. **Compose.** Build the effective pack by union: each section is the concatenation of that section's contributions across siblings. Map/object sections (e.g. `pipelines.exporters`) require an explicit conflict resolution policy — see §3.4.
6. **Resolve cross-references.** Validate every `ref:`, `binds_to:`, `sli:`, `slo:`, `trigger:` resolves to a declared ID in the composed pack.
7. **Validate the effective pack** against the full v1.2 schema and the conformance rubric. Tier-1 requirements apply to the *composed* pack, not to each sibling — a sibling alone need not be tier-1-conformant; only their composition must be.

### 3.4 Singleton sections

Some sections are inherently singletons — there's exactly one `metadata.bindings`, one `spec.storage`, one `spec.otel` per effective pack. For these, sibling composition uses one of three resolution policies, declared per section in the aggregator:

```yaml
metadata:
  name: kx-exchange
  composes:
    - ref: ./kx-exchange-sla.pack.yaml
    - ref: ./kx-exchange-bau.pack.yaml
    - ref: ./kx-exchange-client.pack.yaml
  resolution:
    spec.otel:                  exclusive_to: kx-exchange-bau    # only BAU may declare otel
    spec.storage:               exclusive_to: kx-exchange-bau
    spec.pipelines.receivers:   merge_by_name               # union; conflict on name = error
    spec.pipelines.exporters:   exclusive_to: kx-exchange-bau
    spec.alerting.routes:       partition_by_severity       # each route's severity is the partition key; no two siblings may declare the same severity
    spec.baselines:             exclusive_to: kx-exchange-sla    # SLA owns the contractual targets
```

Three resolution modes in v1.2:

- `exclusive_to: <sibling>` — exactly one sibling may declare this section; others MUST NOT.
- `merge_by_<key>` — union the entries, but each entry's `<key>` must be unique across siblings.
- `partition_by_<key>` — like `merge_by_<key>`, but additionally requires every `<key>` value to be owned by exactly one sibling. Useful for severity-tier ownership.

The default resolution for any unspecified section is `merge_by_id` (i.e. union by `id` field).

### 3.5 Versioning of siblings

Each sibling has its own SemVer. The aggregator pins sibling versions:

```yaml
metadata:
  composes:
    - { ref: ./kx-exchange-sla.pack.yaml,    pin: ~2.4.0 }
    - { ref: ./kx-exchange-bau.pack.yaml,    pin: ~3.1.0 }
    - { ref: ./kx-exchange-client.pack.yaml, pin: ~1.0.0 }
```

`pin` follows the NPM-style range syntax. Sibling teams release on their own cadence; the aggregator owner periodically advances the pins after a brief composition test.

A sibling's *contributes list* is part of its public contract — adding a new contributed section is a minor bump; removing or moving a section is a major bump.

### 3.6 Conformance scoring on composed packs

The composed effective pack is what the conformance scanner evaluates. Each MUST clause is satisfied by *the composition*, not by any individual sibling. This means:

- The L5 chaos requirement for tier-1 can be satisfied by the BAU sibling (which owns validation), even though the SLA sibling owns no validation artefacts.
- The L4 self-healing requirement can be satisfied by the client sibling, even if BAU has no remediation.

A sibling's solo conformance score is undefined; only the aggregator has one. The conformance dashboard surfaces *the aggregator* with annotations indicating which sibling contributed which satisfied clauses, so each owner can see their footprint.

### 3.7 The packtool CLI gains two subcommands

```
packtool compose <aggregator.pack.yaml>      → prints effective pack to stdout
packtool validate <aggregator.pack.yaml>     → composes first, then validates
packtool lint --sibling <sibling.pack.yaml>  → validates a single sibling in isolation
                                               (skips clauses that require composition)
```

`packtool compose` is the canonical way to inspect what the operator will see. CI runs `validate` on every PR. Sibling teams can run `lint --sibling` in their own subfolder's CI for fast feedback without pulling the whole aggregator.

---

## 4. Worked example: kx-exchange

```
observability/
├── kx-exchange.pack.yaml                  # aggregator, owned by team-platform-sre
├── kx-exchange-sla.pack.yaml              # owned by team-krystalinex-platform + team-compliance
├── kx-exchange-bau.pack.yaml              # owned by team-platform-sre
└── kx-exchange-client.pack.yaml           # owned by team-app-payments
```

With `CODEOWNERS`:

```
observability/kx-exchange.pack.yaml           @team-platform-sre
observability/kx-exchange-sla.pack.yaml       @team-krystalinex-platform @team-compliance
observability/kx-exchange-bau.pack.yaml       @team-platform-sre
observability/kx-exchange-client.pack.yaml    @team-app-payments
```

Contribution map:

| Section | SLA sibling | BAU sibling | Client sibling |
|---|---|---|---|
| `spec.otel` | — | ✓ exclusive | — |
| `spec.slis` | 1 (composite) | 2 (broker, HA) | many |
| `spec.slos` | 1 (99.95 external) | 3 (99.999 internal + burns) | many |
| `spec.pipelines.receivers` | — | platform scrape jobs | SCM scrapers |
| `spec.pipelines.exporters` | — | ✓ exclusive | — |
| `spec.storage` | — | ✓ exclusive | — |
| `spec.queries.recording_rules` | — | core SLI/SLO rules | SCM queue rules |
| `spec.dashboards` | D EXT (audit) | Repo + D INT + D APP | SCM dashboards |
| `spec.policy.burn_rate_alerts` | sla.external_99_95 | bau.broker_burn_* | client.* |
| `spec.alerting.routes` | SEV1→KrystalineX Platform + IM | SEV1→kx-exchange On Call | SEV1/2/3→App Team |
| `spec.remediation` | — | — | (future) |
| `spec.baselines` | ✓ exclusive (SLA-driven targets) | — | — |
| `spec.validation` | — | (future chaos) | synthetic probes |
| Governance evidence | G 01, G 02 | G 03, G 04 | SCM 01 (scrape config) |

Each team's PR cadence is independent. The aggregator owner advances pins after a brief integration check, typically weekly.

---

## 5. Migration path

### 5.1 v1.1 packs are unchanged

A pack without `metadata.composes` continues to behave exactly as v1.1 specifies. v1.2 is backwards compatible.

### 5.2 Splitting an existing pack

`packtool split` (proposed; not in scope for this RFC's implementation, but worth flagging):

```
packtool split kx-exchange.pack.yaml --by source --emit aggregator+siblings
```

Heuristic: the tool reads ownership/source annotations (the by-source diagram's classification) and produces a starter aggregator + N siblings. Manual cleanup of cross-references follows.

### 5.3 Deprecation

`composes` is opt-in. No v1.1 pack is forced to migrate. If the platform later mandates composition for tier-1 packs (unlikely), the deprecation cycle would be a year minimum.

---

## 6. Alternatives considered

### 6.1 Per-section ownership tags (lightweight)

Keep one file; tag each item with `source` and `owner` metadata; use the conformance scanner to surface ownership in the dashboard.

- **Pros**: zero schema change beyond optional metadata fields; very low friction.
- **Cons**: doesn't fix the review-cadence problem (still one PR for any change); doesn't enable independent release cadence; doesn't help CODEOWNERS in Git.
- **Verdict**: useful complement, not a substitute. Could ship in v1.1.1 as a small additive change and coexist with v1.2 composition.

### 6.2 Build-time concatenation (no schema change)

Keep one *effective* pack file; generate it from per-team source files via a build step (Make, Bazel, npm script).

- **Pros**: ships today, no spec change.
- **Cons**: build step lives outside the platform — every team reinvents it; no validation of disjointness; cross-references are textual; no first-class ownership concept; the operator only sees the concatenated blob.
- **Verdict**: viable as a stop-gap. Use it if v1.2 is more than a quarter away.

### 6.3 Multiple packs per service (no aggregator)

Allow N packs to declare the same `bindings.service`; the operator unions them at apply time.

- **Pros**: no aggregator-file boilerplate.
- **Cons**: no single source of truth for the service's effective observability; no place to declare composition resolution policies; conformance scoring becomes ill-defined.
- **Verdict**: rejected. The aggregator is cheap (mostly empty) and provides the necessary coordination point.

### 6.4 Kustomize-style overlays

Use a Kustomize-like mechanism where each "sibling" is a strategic merge patch on the base.

- **Pros**: familiar pattern for Kubernetes-native teams.
- **Cons**: strategic-merge-patch semantics are notoriously hard to reason about for lists; same overlay-precedence problems that motivate this RFC in the first place.
- **Verdict**: rejected. Disjoint-union is conceptually cleaner for our case than patch overlays.

---

## 7. Drawbacks

- **Cognitive load.** Engineers must understand both `imports` (vertical, overlay) and `composes` (horizontal, disjoint union). Documentation cost is real.
- **Operational complexity.** The operator now resolves composition before validation. A failed compose blocks the entire service's reconcile; the diagnostic must clearly point at the offending sibling.
- **Cross-sibling refactors are harder.** Moving an SLI from BAU to SLA changes its namespaced ID, which means every downstream reference must move with it. The tooling needs a `rename` command.
- **Conformance attribution.** "Which sibling caused the tier-1 failure?" needs to be obvious in the conformance dashboard. Otherwise teams blame each other.
- **The aggregator is a single point of merge.** If the aggregator owner is on vacation, sibling teams can't independently advance pins. Mitigation: aggregator co-ownership by all sibling owners.

---

## 8. Unresolved questions

- **Should siblings declare their own `imports`?** Tentative yes — a sibling can pull in platform defaults independently. But the composer must dedup transitively imported items.
- **Is `validation.governance` a real section, or is governance evidence always synthesised from other sections?** This RFC assumes the former for the contributes-list example, but the underlying spec doesn't have an explicit `governance` block. Either way works; the contribute syntax should generalise to whatever section name lands.
- **Do we need a `metadata.contributes.exclusive` flag** to mark a sibling as the *only* permitted contributor to a section, separately from the `resolution` block on the aggregator? Currently the same constraint can be expressed in two places. Pick one.
- **What about partial / conditional contribution?** "SLA owns dashboards in `prod` only" — does composition need environment-aware partitioning, or is that better handled by separate aggregators per environment? Recommend the latter.
- **Sibling version drift detection.** Should the platform warn when a sibling has been at the same version for > N months relative to its peers? Probably a conformance scanner enhancement, not a schema concern.

---

## 9. Implementation plan

| Phase | Effort | Deliverable |
|---|---|---|
| 1. Schema additions | ~1 week | v1.2 schema with `metadata.composes`, `metadata.contributes`, `metadata.namespace`, aggregator `resolution` block |
| 2. Composer logic | ~2 weeks | `packtool compose`, `packtool validate` extended to compose first |
| 3. Operator support | ~2 weeks | Operator's `Resolve` stage gains the compose step; conformance attribution surfaces sibling provenance |
| 4. CLI ergonomics | ~1 week | `packtool lint --sibling`, helpful error messages for namespace/contributes/disjointness violations |
| 5. Documentation + examples | ~1 week | `examples/composed-payment-service/` with three siblings; spec amendment |
| 6. Optional: `packtool split` | ~1 week | Heuristic splitter from an annotated v1.1 pack |

Total: ~7-8 weeks of focused work. Phase 1+2 alone is enough to support an early adopter who's willing to write the composer by hand; phase 3 is required for general availability.

---

## 10. Decision request

We are seeking review and comments from:

- Platform engineering (operator implementation, conformance scanner)
- SRE practice leads (whether the model captures real ownership splits in their services)
- Compliance & risk (whether the SLA-sibling pattern correctly isolates regulated content)
- Application teams (whether the per-team pin-advancing flow is workable)

Please file comments as GitHub issues with the `rfc-0001` label, or comment directly on this file in PRs. Decision target: 30 days from RFC publication.

---

## Appendix A — Schema delta sketch (informal)

```json
{
  "$defs": {
    "Metadata": {
      "properties": {
        "composes": {
          "type": "array",
          "items": {
            "type": "object",
            "required": ["ref"],
            "properties": {
              "ref": { "type": "string" },
              "pin": { "type": "string", "description": "SemVer range" }
            }
          }
        },
        "contributes": {
          "type": "array",
          "items": {
            "oneOf": [
              { "type": "string", "enum": ["slis","slos","pipelines","storage","queries","dashboards","policy","alerting","remediation","baselines","validation"] },
              { "type": "object", "additionalProperties": { "type": "object" } }
            ]
          }
        },
        "namespace": {
          "type": "string",
          "pattern": "^[a-z][a-z0-9_-]*$",
          "description": "Required when 'contributes' is declared. All IDs in this pack must start with '<namespace>.'."
        },
        "resolution": {
          "type": "object",
          "description": "On aggregator packs (those with 'composes'), declares per-section conflict resolution.",
          "additionalProperties": {
            "type": "object",
            "oneOf": [
              { "required": ["exclusive_to"] },
              { "required": ["merge_by"] },
              { "required": ["partition_by"] }
            ]
          }
        }
      }
    }
  }
}
```

The above is illustrative. The full schema lands with the v1.2 release; the only contract this RFC commits to is the *names* and *semantics* of these fields.

---

## Change log

| Date | Author | Change |
|---|---|---|
| 2026-05-13 | Platform Engineering | Initial draft (RFC-0001) |
