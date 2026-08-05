# ADR-0024: Compare adaptive selections with an explicitly declared original-policy baseline

Status: Accepted
Date: 2026-08-02

## Context

SmartRoute's product hypothesis is not merely that Direct and Proxy can be raced. It must show whether the adaptive lane improves on the route the user's selected `Other`/`MATCH` traffic would otherwise have taken. Existing readiness and relay reports expose the selected adaptive path but contain no baseline, so they cannot count changed selections or even distinguish a Proxy choice that preserved the original policy from one that replaced Direct.

The runtime already has `original_fallback` and a separately configured `original_endpoint`. Before this ADR, the field was used only by the synthetic paired evaluator and did not prove what the listener behind `original_endpoint` actually does. SmartRoute does not inspect active Clash during isolated operation, so treating that field as an observed rule result would be false precision.

## Decision

`original_fallback` now has a second explicit contract: it is the operator-declared Direct/Proxy category of the original catch-all policy exposed through `original_endpoint`. The Sidecar copies it into `decision`, `diagnostic`, and `relay_outcome` as `declared_baseline_path`.

This value is a declaration, not a live Mihomo rule trace or executed counterfactual. A controlled-trial preflight therefore requires a separate `-acknowledge-original-baseline` confirmation that the value was checked against the planned original-policy listener. Preflight remains read-only and does not itself verify or inspect active Clash.

Observation JSONL advances to schema 4. The report reader remains compatible with schemas 1–3. The identity-free observation report advances to version 4 and emits:

| Metric | Meaning | Limitation |
| --- | --- | --- |
| Scoped/unscoped selections | Committed adaptive selections with/without a declared baseline | Unscoped historical rows remain usable for other metrics |
| Same/changed from declared | Whether the selected path equals the declaration | Does not prove the counterfactual path would have connected |
| Direct instead of Proxy | Committed selection changed from declared Proxy to observed Direct readiness | Not automatically a saved request, byte, or latency amount |
| Proxy instead of Direct | Committed selection changed from declared Direct to observed Proxy readiness | Not automatically a rescued application request |
| Changed selection ratio | Changed committed selections divided by baseline-scoped committed selections | Covers the adaptive lane only, not all system traffic |
| Changed relay outcomes/bytes | Actual post-commit transfer on selections differing from the declaration | These are observed winner bytes, not counterfactual savings |

When a schema-4 connection scope pairs a decision and relay outcome, their target, selected path, and declared baseline must all agree. Invalid baseline values are rejected before persistence, and contradictions fail report construction without exposing connection identifiers.

## Privacy and safety review

The field contains only `direct` or `proxy`; it does not contain a rule name, group name, subscription, target, controller secret, or profile text. It creates no network operation and is not a learning input. Reports include only aggregate counts and bytes.

An incorrect declaration can bias product conclusions even though it cannot change routing. Requiring explicit preflight confirmation, preserving unscoped legacy counts, and labeling every interpretation as declared-not-observed prevents the report from silently presenting it as measured truth. A later active integration may replace the declaration with a bounded verified rule-lane signal only under a separate ADR.

## Alternatives

| Alternative | Reason rejected |
| --- | --- |
| Continue without a baseline | Cannot evaluate the core unnecessary-proxy hypothesis |
| Infer baseline as always Proxy | Incorrect for Direct catch-alls and privacy-first setups |
| Infer baseline from selected path | Makes the comparison tautological |
| Read active Clash automatically during reporting | Violates isolation and still may not prove the historical rule result |
| Race the original policy as a third live path | Adds connection duplication, unclear readiness semantics, and replay/privacy risk |
| Call changed winner bytes “saved bytes” | The unexecuted baseline could have transferred a different amount or failed |

## Consequences

- A controlled trial can count how often SmartRoute changes the declared `Other`/`MATCH` route.
- The report can show actual traffic carried on those changed winners without claiming counterfactual savings.
- Operators must keep `original_fallback` aligned with the listener behind `original_endpoint` and acknowledge that alignment before preflight passes.
- Existing schemas 1–3 remain readable but contribute only to baseline-unscoped counts.

## Validation and migration

Sidecar lifecycle tests verify the declaration is identical on decision and relay outcome. Recorder tests verify schema-4 persistence and invalid-value rejection. Report tests cover same/changed aggregation, actual changed-winner bytes, schema-3 compatibility, pair contradiction, identity omission, and overflow refusal. Preflight tests require the dedicated acknowledgment.

The JSON configuration version does not change because `original_fallback` already exists and already requires Direct or Proxy. Its documented meaning becomes stricter. No active Clash write, SQLite migration, or network probe is performed.
