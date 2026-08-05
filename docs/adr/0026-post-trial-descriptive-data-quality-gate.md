# ADR-0026: Gate post-trial descriptive analysis on observation data quality

Status: Accepted
Date: 2026-08-02

## Context

Observation report v5 contains useful readiness, pairing, declared-baseline, relay, and bounded end-reason aggregates. A syntactically valid report is not necessarily a valid controlled-trial sample: it may mix sessions, include legacy unscoped rows, have too few committed selections, lose terminal/outcome pairs at the report boundary, omit baseline or connection scopes, or be dominated by lifecycle cancellation.

Directly interpreting such a window can manufacture apparent route-change or reliability conclusions. Conversely, passing data-quality checks still cannot provide a verified static counterfactual, client-visible outcome, statistical significance, or authorization to enable automatic policy.

## Decision

Add read-only `smartroute trial assess`. It builds the current strict observation report directly from the configured managed JSONL directory and evaluates a pure deterministic data-quality gate. The original CLI accepted a bounded time window and configurable thresholds with these defaults; ADR-0027 supersedes that timing and requires them to be pre-registered during preflight:

| Gate | Default | Meaning |
| --- | --- | --- |
| Minimum committed selections | 20 | Avoid interpreting an empty or trivially small window |
| Minimum committed connection-scope coverage | 0.99 | Random per-connection scope is present on committed decisions |
| Minimum declared-baseline coverage | 0.99 | Committed selections carry the operator declaration |
| Minimum terminal/relay pair completeness | 0.95 | `paired / (paired + unmatched outcome + committed decision without outcome)` |
| Maximum lifecycle cancellation ratio | 0.10 | Explicitly canceled relay outcomes do not dominate the window |

The input must also:

- use the current observation report version;
- be paused;
- contain exactly one `trial_session_id` and zero session-unscoped events;
- have internally consistent committed-decision, relay-outcome, and pair counts;
- contain every required privacy and interpretation limitation flag.

Connection scope adds explicit scoped/unscoped committed-decision counters to observation report v5 so the denominator is not inferred from all decision events. Exact report invariants require:

- scoped committed decisions = paired relay outcomes + committed decisions without outcome;
- scoped relay outcomes = paired relay outcomes + unmatched relay outcomes;
- connection-scoped plus connection-unscoped committed selections = baseline-scoped plus baseline-unscoped committed selections.

The output field is `ready_for_descriptive_analysis`, not “trial successful.” It includes the evaluated thresholds and identity-free metrics for committed selections, scope/pair/cancellation ratios, and declared-baseline route changes. It always emits a warning that this gate does not prove product improvement.

## Safety and interpretation review

Assessment opens no listener, makes no network connection, does not read active Clash, and changes no persistent state. It returns `authorizes_policy_change=false`, `static_baseline_verified=false`, and `client_outcome_available=false` unconditionally.

Passing means only that the selected bounded observation window is structurally suitable for descriptive analysis. It does not:

- verify `original_fallback` against an active rule trace;
- show what the unexecuted baseline would have transferred;
- establish application success, page latency, or user satisfaction;
- perform an A/B comparison, causal estimate, confidence interval, or significance test;
- authorize Suggest/Auto, generated rules, Clash writes, reload, or rollout continuation.

## Alternatives

| Alternative | Reason rejected |
| --- | --- |
| Let analysts inspect report JSON manually | Easy to miss mixed sessions, truncation, or denominator drift |
| Gate on readiness success ratio | Conflates data completeness with product performance |
| Gate on changed selection ratio | Selects for a desired result and biases the trial |
| Accept an arbitrary prebuilt report file | Bypasses strict current JSONL/source validation and can be stale or edited |
| Automatically enable policy after passing | Data quality is not evidence of benefit or safety |
| Require zero cancellations | A bounded stop window can legitimately cancel a small number of active relays |

## Consequences

- Trial handoff gains a reproducible “is this window analyzable?” decision before product claims are discussed.
- Thresholds are explicit and recorded in output rather than hidden in prose.
- Old or mixed observation data can still be reported but will not pass a strict controlled-trial assessment without adequate current scoping.
- A separate experiment design and client-visible/static control surface is still required for causal benefit evaluation.

## Validation and migration

Pure-function tests cover a valid window, every fail-closed gate, threshold validation, invariant contradictions, and immutable safety claims. CLI tests verify machine-readable failure output. ADR-0027 moves threshold validation to preflight and tests strict plan loading before analysis. Observation tests cover the committed-scope counters.

No config schema, observation schema, SQLite data, Clash state, or network behavior changes.
