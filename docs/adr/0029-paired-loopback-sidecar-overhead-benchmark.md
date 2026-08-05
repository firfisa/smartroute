# ADR-0029: Measure sidecar overhead with paired alternating loopback samples

Status: Accepted
Date: 2026-08-02

## Context

Phase 0 requires evidence that the extra local sidecar hop does not consume the latency benefit SmartRoute is intended to create. Existing Test Lab elapsed times include injected path behavior and are not a controlled baseline/sidecar comparison. A one-off wall-clock number is also vulnerable to scheduler drift and does not expose tail behavior.

## Decision

Add `internal/benchlab` and `smartroute-benchmark-lab`. Both arms use the same in-process loopback echo target and Direct SOCKS gateway:

| Arm | Measured path |
| --- | --- |
| Baseline | client → Direct fake SOCKS gateway → echo target |
| Sidecar | client → SmartRoute → the same Direct fake SOCKS gateway → echo target |

Each sample starts a fresh TCP connection, completes SOCKS CONNECT, writes one byte, and waits for the exact echoed byte. Baseline-first and sidecar-first ordering alternates within pairs. Warmups are excluded. The report uses nearest-rank p50/p95/p99 in microseconds for each arm and signed paired deltas.

The default is five separately reported runs of 200 pairs after 20 warmup pairs per run. The provisional gate remains 5ms, but it is evaluated against the worst per-run paired p95 rather than the combined p95. The command reports the gate without failing by default; `-enforce` is required for a non-zero exit on latency because scheduler load and hardware are environment-dependent. Correctness failures always fail.

## Safety and interpretation review

All listeners bind literal `127.0.0.1:0`; there is no external network, active Clash access, system-proxy change, TUN change, persisted observation, or real target. The unused Proxy gateway must receive zero attempts. Every payload, Direct selection, gateway attempt, and preserved domain is counted.

This measurement does not include Mihomo's real listeners, TLS parsing/handshake, TUN capture, DNS, proxy-node latency, long-lived throughput, power use, or a real application outcome. It is an empty-load local connection-and-first-byte microbenchmark, not proof of production latency or permission for a live trial. The report therefore keeps both authority fields false.

## Alternatives

| Alternative | Reason rejected |
| --- | --- |
| Compare two unrelated runs | Scheduler drift can dominate a sub-millisecond delta |
| Use mean latency only | Hides tail latency and outliers |
| Gate on combined p95 only | Can conceal one unstable run |
| Enforce 5ms in every CI run | Shared runners are not controlled performance environments |
| Benchmark a reused connection | Does not measure the extra admission hop for a fresh connection |
| Call the one-byte echo application success | It is only synthetic transport correctness |

## Consequences

- Phase 0 gains a reproducible machine-readable local overhead surface.
- A permitted platform can distinguish correctness from an optional performance gate.
- Real Mihomo/TUN/TLS and loaded-system benchmarks remain required before product claims.
- Negative paired deltas are preserved as measurement noise rather than clamped to zero.

## Validation and migration

Tests cover options, signed nearest-rank distributions, loopback correctness, Direct-only selection, exact attempts, isolation, and authority fields. CI runs a small non-enforcing CLI smoke benchmark. No runtime config, routing, observation, SQLite, or preflight schema changes.
