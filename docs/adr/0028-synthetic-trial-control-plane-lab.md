# ADR-0028: Rehearse the trial control plane with synthetic local observations

Status: Accepted
Date: 2026-08-02

## Context

The loopback Test Lab verifies the data plane and the pinned-Mihomo lab verifies the integration topology. The post-trial pipeline—bounded recording, schema-5 persistence, report-v6 expected-session matching, pair aggregation, and descriptive data-quality assessment—also needs an executable integration surface that does not depend on the user's active Clash environment or on the sandbox permitting loopback listeners.

Unit tests cover the individual packages, but they do not provide one operator-visible rehearsal report for the complete local analysis chain.

## Decision

Add `internal/triallab` and `smartroute-trial-lab`. The command creates a private temporary workspace and runs two synthetic scenarios:

| Scenario | Expected contract |
| --- | --- |
| `planned_session_complete_window` | Twenty fully scoped committed decisions and relay outcomes produce a report-v6 window ready for descriptive analysis |
| `unexpected_session_fails_closed` | The same complete sample plus one event from another valid session fails only the session-integrity requirement |

The lab uses the real observation recorder, pause control, strict report builder, and assessment function. It returns only identity-free counts and ratios, removes its workspace, opens no listener, makes no external connection, and never reads or writes active Clash or the system proxy.

Its report always declares `synthetic_inputs=true`, `preflight_evidence=false`, `authorizes_live_trial=false`, and `authorizes_policy_change=false`. Its schema and scenario names are deliberately different from `testlab.Report`, so `trial preflight` cannot accept it as isolated data-plane evidence.

## Safety and privacy review

Synthetic target, network-profile, connection, and trial-session values exist only in the removed temporary JSONL workspace. The returned report contains no identifiers. No payload is recorded: synthetic byte counters are numeric metadata, and the lab does not open a relay.

The tool verifies plumbing and fail-closed behavior only. It does not establish real connectivity, application success, latency benefit, proxy savings, a correct original-policy declaration, or permission for a live trial.

## Alternatives

| Alternative | Reason rejected |
| --- | --- |
| Add the scenarios to the network Test Lab | A sandbox that denies loopback bind would prevent testing the otherwise network-free control plane |
| Emit a synthetic preflight report | Could be mistaken for accepted readiness evidence |
| Test only with package unit tests | Provides no standalone rehearsal command or machine-readable integration result |
| Preserve the temporary JSONL for debugging | Creates unnecessary pseudonymous artifacts and cleanup burden |

## Consequences

- Developers can verify the observation/assessment chain without moving traffic or touching Clash.
- The negative mixed-session case is exercised every run rather than only in a unit test.
- Passing this lab is necessary engineering evidence but never a live-trial prerequisite by itself.
- Real data-plane, Mihomo topology, and user-coordinated active-environment gates remain separate.

## Validation and migration

Tests assert both scenario outcomes, all negative authority fields, workspace removal, network/Clash isolation claims, cancellation behavior, and absence of synthetic identifiers from JSON output. No config, observation, SQLite, or preflight report schema changes.
