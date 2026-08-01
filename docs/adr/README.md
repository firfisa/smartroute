# Architecture Decision Records

ADRs are append-only decision records. Supersede an old decision with a new ADR instead of rewriting history.

| ID | Decision | Status | Date |
| --- | --- | --- | --- |
| [0001](0001-sidecar-first.md) | Use a sidecar before forking Mihomo or Clash Verge Rev | Accepted | 2026-08-02 |
| [0002](0002-isolated-test-lab.md) | Keep automated network tests isolated from the active Clash environment | Accepted | 2026-08-02 |
| [0003](0003-read-only-clash-inspection-and-live-rollout.md) | Allow redacted read-only inspection and require coordinated live writes | Accepted | 2026-08-02 |
| [0004](0004-mihomo-socks-ack-is-not-target-readiness.md) | Treat Mihomo SOCKS success as outbound admission, not target readiness | Accepted | 2026-08-02 |
| [0005](0005-safe-tls-first-flight-racing.md) | Race only a parsed TLS first flight without early data | Accepted | 2026-08-02 |

Status values: `Proposed`, `Accepted`, `Superseded`, `Rejected`.
