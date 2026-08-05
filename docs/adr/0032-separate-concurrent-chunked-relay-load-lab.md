# ADR-0032: Keep concurrent chunked relay load separate from latency benchmarks

Status: Accepted
Date: 2026-08-02

## Context

The paired connection benchmark answers the cost of one fresh admission/readiness path. It cannot establish how the extra local relay behaves when many connections repeatedly carry bytes. Adding load to the same runner would change scheduler, allocation, and denominator behavior and make the existing latency result incomparable.

## Decision

Add `internal/loadlab` and `smartroute-load-lab` as a separate loopback-only experiment. Each run compares two sequential arms, with arm order alternating by run:

| Arm | Path |
| --- | --- |
| Baseline | concurrent clients → selected Direct gateway → echo target |
| Sidecar | concurrent clients → SmartRoute → the same Direct gateway → echo target |

The gateway tier is either the in-process fake SOCKS gateway or the explicit pinned-Mihomo forced-DIRECT topology from ADR-0030. Every arm starts fresh connections behind one barrier. Each connection repeatedly writes a deterministic chunk and reads the exact echo before continuing until its byte budget is complete. This is sustained chunked request/echo relay load, not simultaneous bidirectional traffic and not a maximum-throughput claim.

The default contract is three runs, 16 concurrent measured connections per arm, 1 MiB verified one-way payload per connection, 32 KiB chunks, and four 64 KiB warmup connections per arm per run. Throughput uses verified one-way payload bytes divided by batch wall time. The report also exposes the sum of both relay directions, connection-completion distributions, alternating order, and sidecar/baseline throughput ratio.

Correctness always fails closed on a missing connection, byte mismatch, missing Direct selection, unexpected gateway attempt, Proxy attempt, domain mismatch, cancellation, or child failure. The provisional minimum per-run throughput ratio is 0.70. It is report-only by default and affects exit status only with `-enforce`, because shared-machine loopback throughput is environment-dependent.

## Initial evidence and interpretation

On the 2026-08-02 macOS arm64 run, both fake and pinned-Mihomo tiers completed every measured connection and byte, but each missed the provisional ratio gate:

| Gateway | Baseline median | Sidecar median | Median ratio | Worst ratio | Gate |
| --- | ---: | ---: | ---: | ---: | --- |
| Fake SOCKS | 1326.37 MiB/s | 891.81 MiB/s | 0.672 | 0.668 | Missed |
| Pinned Mihomo | 1366.12 MiB/s | 939.24 MiB/s | 0.688 | 0.677 | Missed |

The similar steady ratios indicate a measurable extra-hop copy/scheduling cost rather than a fake-gateway-only artifact. Absolute sidecar throughput remained roughly 0.89–0.94 GiB/s in this synthetic setup. Neither fact predicts throughput on a real WAN, proves user-visible harm, or permits moving the threshold after observing the result. The miss remains visible and motivates profiling plus controlled-size/concurrency sweeps.

Decision record summary: provisional 0.70 gate missed in both initial tiers; no threshold change was made.

## Safety and privacy review

All targets/listeners are loopback and OS-assigned. The optional Mihomo tier owns an explicit pinned child and temporary home. There is no external network, active Clash access, TUN, system-proxy change, persistence, hostname history, payload export, or TLS secret. Payload bytes are deterministic local fixtures and are not included in the JSON report.

## Alternatives

| Alternative | Reason rejected |
| --- | --- |
| Add concurrency to `benchlab` | Would invalidate the single-connection latency contract |
| Stream one large write before reading | Can deadlock an echo path when socket buffers fill |
| Call both-direction byte sums full duplex | Chunks are request/echo, not simultaneous opposing streams |
| Enforce 0.70 in default CI | Shared runners are not controlled throughput environments |
| Lower the threshold after the first miss | Would erase falsifying evidence rather than investigate it |
| Claim maximum relay throughput | Chunk pacing, echo RTT, loopback, and short runs deliberately bound the interpretation |

## Consequences

- Phase 0 gains deterministic concurrent byte-integrity and relay-load coverage.
- A real local high-bandwidth overhead signal is recorded rather than hidden.
- Profiling, payload-size/concurrency sweeps, longer unidirectional streams, CPU/memory measurement, and real-network trials remain separate follow-up gates.
- Report authority fields remain false even when correctness and an explicitly enforced gate pass.

## Validation and migration

Tests cover option bounds, nearest-rank summaries, alternating order, exact measured/warmup counts, exact directional byte totals, no raw per-connection rows in JSON, cancellation, and optional pinned-Mihomo execution. Default and manual CI workflows run small non-enforcing smoke loads. No runtime routing, configuration, persistence, or active integration changes.
