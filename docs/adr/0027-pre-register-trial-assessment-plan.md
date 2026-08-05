# ADR-0027: Pre-register and bind the controlled-trial assessment plan

Status: Accepted
Date: 2026-08-02

## Context

ADR-0026 introduced a deterministic post-trial data-quality gate, but its CLI still accepted the time window and thresholds after observations existed. It also verified only that one trial session appeared in the window, not that the session was the one intended by preflight. An operator could therefore select a favorable window or threshold after seeing results, reuse a preflight after configuration drift, or accidentally assess a different otherwise well-scoped session.

These are experiment-integrity failures even though none changes routing behavior.

## Decision

Every successful `trial preflight` emits a versioned assessment plan containing:

| Field | Binding |
| --- | --- |
| `trial_session_id` | One random non-semantic session to pass to `supervise` |
| `config_sha256` | SHA-256 over the fully decoded SmartRoute configuration |
| `not_before` | Preflight UTC time; earlier observations are ineligible |
| `window_seconds` | Positive whole-second observation window |
| `thresholds` | Sample, scope, pairing, and cancellation gates from ADR-0026 |
| `plan_sha256` | SHA-256 over the preceding canonical plan fields |

The plan is created before the trial. `trial assess` requires the saved successful preflight JSON and no longer accepts replacement window or threshold flags. It strictly decodes the report, verifies its current version, ready state, generation/not-before equality, immutable safety claims, plan digest, and current configuration fingerprint. It builds observation report v6 from the later of the preflight time and `now - window`, using the expected session; observations that predate preflight are therefore ineligible.

Report v6 exposes only `expected_trial_session_matched` and `unexpected_trial_session_events`; it continues to omit the concrete session identifier. Assessment fails unless the expected session is present, no other session event is present, and no event is unscoped.

## Safety and privacy review

This change opens no listener, makes no network connection, and changes neither JSONL schema 5 nor runtime routing. The preflight report contains its random session scope because the operator must pass it to `supervise`; it is local trial-linkage metadata and must not be committed or uploaded. Aggregate observation and assessment output never contains the identifier or decoded configuration, only booleans, counts, thresholds, and metrics.

The configuration fingerprint detects drift; it is not a secret-hiding mechanism and does not prove that active Clash matches the declared listener policy. A valid plan still does not authorize activation, policy changes, generated rules, or a Clash write/reload.

## Alternatives

| Alternative | Reason rejected |
| --- | --- |
| Keep post-trial CLI threshold flags | Permits outcome-aware threshold selection |
| Accept any single observed session | Can silently assess the wrong trial |
| Put the expected session ID in the aggregate report | Unnecessary linkage disclosure |
| Sign with a long-lived local key | Adds key lifecycle without protecting against the local operator editing both report and key |
| Hash the raw config file bytes | Formatting-only changes would invalidate an otherwise identical decoded configuration |

## Consequences

- The operator must preserve the successful preflight report and launch the trial with its session ID.
- Configuration, window, or threshold changes require a new preflight.
- A mixed, unscoped, missing, or different session fails closed without disclosing IDs.
- The digest catches accidental or unsophisticated edits; it is tamper-evident, not an authenticated signature.
- ADR-0026 remains the definition of the data-quality metrics, but its post-trial-configurable CLI surface is superseded by this pre-registration requirement.

## Validation and migration

Tests cover plan construction, invalid inputs, digest tampering, unready reports, configuration drift, expected/unexpected session aggregation without identity leakage, and CLI assessment through a saved plan. Existing JSONL schema-1 through schema-5 inputs remain readable. Observation aggregate consumers must accept report v6.
