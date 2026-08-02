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

Status values: `Proposed`, `Accepted`, `Superseded`, `Rejected`.
