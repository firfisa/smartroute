# Architecture Decision Records

ADRs are append-only decision records. Supersede an old decision with a new ADR instead of rewriting history.

| ID | Decision | Status | Date |
| --- | --- | --- | --- |
| [0001](0001-sidecar-first.md) | Use a sidecar before forking Mihomo or Clash Verge Rev | Accepted | 2026-08-02 |
| [0002](0002-isolated-test-lab.md) | Keep automated network tests isolated from the active Clash environment | Accepted | 2026-08-02 |
| [0003](0003-read-only-clash-inspection-and-live-rollout.md) | Allow redacted read-only inspection and require coordinated live writes | Accepted | 2026-08-02 |
| [0004](0004-mihomo-socks-ack-is-not-target-readiness.md) | Treat Mihomo SOCKS success as outbound admission, not target readiness | Accepted | 2026-08-02 |
| [0005](0005-safe-tls-first-flight-racing.md) | Race only a parsed TLS first flight without early data | Accepted | 2026-08-02 |
| [0006](0006-separate-availability-guard.md) | Put a small availability guard in front of the adaptive engine | Accepted | 2026-08-02 |
| [0007](0007-enforce-direct-probe-privacy.md) | Enforce Direct-probe privacy before opening candidates | Accepted | 2026-08-02 |
| [0008](0008-supervise-guard-and-engine.md) | Supervise Guard and adaptive engine as independent child processes | Accepted | 2026-08-02 |
| [0009](0009-bounded-local-observation-recorder.md) | Use a bounded local observation recorder for live trials | Accepted | 2026-08-02 |
| [0010](0010-preserve-only-completed-counterfactual-evidence.md) | Preserve only completed counterfactual path evidence | Accepted | 2026-08-02 |
| [0011](0011-ephemeral-learning-and-preferred-racing.md) | Learn ephemeral preferences and apply them through preferred racing | Accepted | 2026-08-02 |
| [0012](0012-sqlite-strong-evidence-store.md) | Persist strong evidence in a pseudonymous SQLite store | Accepted | 2026-08-02 |
| [0013](0013-opt-in-async-durable-evidence-writer.md) | Wire durable shadow evidence through an opt-in asynchronous writer | Accepted | 2026-08-02 |
| [0014](0014-durable-evidence-lifecycle.md) | Manage durable evidence with read-only status and verified snapshots | Accepted | 2026-08-02 |
| [0015](0015-cross-session-shadow-assessment.md) | Evaluate cross-session evidence as shadow suggestions only | Accepted | 2026-08-02 |
| [0016](0016-privacy-safe-shadow-report.md) | Aggregate Shadow assessments without exposing target identity | Accepted | 2026-08-02 |
| [0017](0017-freeze-learning-on-systemic-failure.md) | Freeze learning on systemic path failures | Accepted | 2026-08-02 |
| [0018](0018-identity-free-observation-readiness-report.md) | Report identity-free adaptive readiness metrics | Accepted | 2026-08-02 |
| [0019](0019-random-shared-trial-session-scope.md) | Scope observations with a random shared trial session | Accepted | 2026-08-02 |
| [0020](0020-read-only-evidence-based-trial-preflight.md) | Gate controlled trials with fresh read-only evidence | Accepted | 2026-08-02 |
| [0021](0021-context-cancel-and-drain-runtime-connections.md) | Cancel and drain accepted connections before runtime server exit | Accepted | 2026-08-02 |
| [0022](0022-post-commit-relay-telemetry.md) | Record bounded post-commit relay telemetry without claiming application success | Accepted | 2026-08-02 |
| [0023](0023-random-connection-observation-scope.md) | Correlate adaptive terminal events with random per-connection scopes | Accepted | 2026-08-02 |
| [0024](0024-declared-original-policy-baseline.md) | Compare adaptive selections with an explicitly declared original-policy baseline | Accepted | 2026-08-02 |
| [0025](0025-bounded-relay-direction-end-reasons.md) | Record bounded directional relay end reasons without raw I/O errors | Accepted | 2026-08-02 |
| [0026](0026-post-trial-descriptive-data-quality-gate.md) | Gate post-trial descriptive analysis on observation data quality | Accepted | 2026-08-02 |
| [0027](0027-pre-register-trial-assessment-plan.md) | Bind assessment to a preflight session, config, window, and thresholds | Accepted | 2026-08-02 |
| [0028](0028-synthetic-trial-control-plane-lab.md) | Rehearse the observation and assessment control plane with synthetic local data | Accepted | 2026-08-02 |
| [0029](0029-paired-loopback-sidecar-overhead-benchmark.md) | Measure local sidecar overhead with alternating paired loopback samples | Accepted | 2026-08-02 |
| [0030](0030-paired-pinned-mihomo-forced-direct-benchmark.md) | Add an isolated pinned Mihomo forced-DIRECT benchmark tier | Accepted | 2026-08-02 |
| [0031](0031-paired-tls-serverhello-readiness-benchmark.md) | Benchmark exact ClientHello-to-ServerHello readiness across both gateway tiers | Accepted | 2026-08-02 |
| [0032](0032-separate-concurrent-chunked-relay-load-lab.md) | Keep concurrent chunked relay load separate and retain the provisional gate miss | Accepted | 2026-08-02 |
| [0033](0033-retain-standard-library-tcp-copy-after-load-sweep.md) | Retain standard-library TCP copying after the fixed load sweep | Accepted | 2026-08-02 |
| [0034](0034-client-paced-offered-load-capacity-lab.md) | Add a client-paced offered-load capacity lab | Accepted | 2026-08-02 |
| [0035](0035-manual-fixed-policy-management-plane.md) | Add a manual fixed-policy management plane without runtime activation | Accepted | 2026-08-03 |
| [0036](0036-opt-in-automatic-durable-policy-layer.md) | Remember the last ready path without per-target approval | Superseded in part by 0037 | 2026-08-03 |
| [0037](0037-make-automatic-last-known-good-the-product-default.md) | Make automatic last-known-good routing the product default | Accepted | 2026-08-03 |
| [0038](0038-private-checksum-gated-live-candidate.md) | Use a private checksum-gated package for live integration | Accepted | 2026-08-03 |
| [0039](0039-reserve-readiness-time-for-automatic-fallback.md) | Reserve readiness time for automatic fallback | Accepted | 2026-08-04 |
| [0040](0040-arm-runtime-before-active-reload.md) | Arm Engine and Guard before activating the Clash transform | Accepted | 2026-08-04 |
| [0041](0041-report-automatic-policy-effect.md) | Report automatic-policy learning and reuse without target identity | Accepted | 2026-08-04 |
| [0042](0042-verify-semantic-active-script-binding.md) | Verify the resolved active script binding instead of whole-profile metadata | Accepted | 2026-08-05 |
| [0043](0043-own-live-supervisor-with-macos-launchagent.md) | Own an active macOS trial Supervisor with a user LaunchAgent | Accepted | 2026-08-05 |

Status values: `Proposed`, `Accepted`, `Superseded`, `Rejected`.
