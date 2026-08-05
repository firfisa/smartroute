# ADR-0033: Retain standard-library TCP copying after the fixed load sweep

Status: Accepted
Date: 2026-08-02

## Context

ADR-0032 recorded a provisional 0.70 sidecar/baseline throughput-ratio miss in both fake and pinned-Mihomo loopback tiers. A single 16-connection, 1 MiB cell could not distinguish per-connection allocation, sustained copying, scheduler variance, or a Mihomo-specific effect.

SmartRoute currently relays each direction with `io.Copy` between owned `net.Conn` values. On macOS, TCP-to-TCP copies use the Go standard library's generic buffered path; on Linux, the same API may use `splice`. Replacing it globally with an explicit pooled buffer is therefore a platform-sensitive data-plane change, not a mechanical allocation tweak.

## Decision

Add a fixed six-cell `smartroute-load-sweep` over concurrency and payload size. Each arm records current-Go-process runtime deltas in addition to the existing exact correctness and throughput contract. Runtime scope and limits are machine-readable: kernel work and the Mihomo child are excluded, and short-window CPU deltas are diagnostic only.

Retain the existing `io.Copy` relay implementation. Do not add a cross-connection payload buffer pool or lower the provisional 0.70 ratio gate from the observed results.

| Evidence | Observation | Interpretation boundary |
| --- | --- | --- |
| 16 × 8 MiB, fake | median ratio 0.665; worst 0.661 | Stable extra-hop cost without Mihomo |
| 16 × 8 MiB, pinned Mihomo | median ratio 0.665; worst 0.663 | Same shape through the real upstream boundary |
| Same concurrency, 64 KiB to 8 MiB | allocation totals stay approximately flat within each tier | Extra allocations are predominantly per connection, not per payload byte |
| Concurrency 16 to 64 | allocation totals scale approximately with connections | Copy/setup buffer lifetime is the likely allocation shape |
| Short runtime user-CPU deltas | zero, coarse, or scheduler-inconsistent in short cells | Not suitable for optimization claims or gates |

The matching long-cell ratios across gateway tiers are stronger evidence for the unavoidable extra local relay/copy boundary than for an allocation-driven throughput defect. A pool could remove some allocations but would retain application bytes across connections unless cleared, and no current evidence shows it would materially change sustained throughput. Preserving `io.Copy` also preserves possible Linux zero-copy behavior.

## Safety and privacy review

The sweep uses deterministic fixture bytes on ephemeral loopback listeners. It exports aggregate counters only and never raw per-connection rows or payload. Reusing payload-bearing buffers across connections would expand sensitive-data lifetime, so such a change requires separate evidence, explicit zeroing semantics, benchmarks, and review.

The optional Mihomo tier accepts only an explicit pinned lab executable, owns its child and temporary home, and does not discover or access the active Clash environment.

## Alternatives

| Alternative | Reason rejected |
| --- | --- |
| Lower the gate to approximately 0.65 | Changes the hypothesis after observing a miss and says nothing about user experience |
| Add a global `sync.Pool` of copy buffers | Payload-lifetime/privacy cost; likely disables or bypasses platform fast paths; no demonstrated throughput benefit |
| Use short-window CPU deltas as the cause | Measurements are process-only and visibly scheduler-sensitive |
| Attribute the miss to Mihomo | Fake and pinned-Mihomo long cells converge to the same ratio |
| Remove the throughput gate | The miss is useful falsifying evidence even while report-only |

## Consequences

- The project has a reproducible scaling/allocation diagnostic surface without changing production relay semantics.
- The current macOS loopback high-throughput limitation remains explicit.
- Future optimization requires an isolated before/after benchmark, byte/end/cancellation equivalence, platform-specific analysis, and payload-memory review.
- Controlled bandwidth/latency experiments remain necessary before judging real user impact.

## Validation and rollback

Sweep tests cover cell validation, duplicate rejection, deterministic loopback execution, correctness propagation, runtime availability, and aggregate summaries. The CLI has no production listener and no persistent state. Removing the sweep is a test-tool rollback only; changing `netrelay` is explicitly outside this ADR.
