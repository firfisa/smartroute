# ADR-0034: Add a client-paced offered-load capacity lab

Status: Accepted
Date: 2026-08-02

## Context

ADR-0032 and ADR-0033 show a stable sidecar/baseline ratio near 0.665 for a high-speed loopback request/echo workload. That is a useful ceiling diagnostic but is easy to misread as a 33% slowdown at ordinary network rates. Artificially sleeping in a target or gateway and then quoting a near-one throughput ratio would hide the mechanism and resemble network emulation without implementing queues, congestion control, RTT, or loss.

## Decision

Add `smartroute-capacity-lab` with fixed aggregate one-way offered loads of 100, 500, 1000, 5000, and 8000 Mbps. Measured clients follow an absolute cumulative-byte schedule after each exact echo. Falling behind removes future waits rather than accumulating additional delay, and final completion is compared with the schedule deadline.

Advance the underlying Load Lab report from schema 1 to schema 2. Schema 2 adds an optional measurement offered-load value and a per-batch pacing object; unpaced runs mark pacing disabled. The reports are transient test artifacts rather than production persistence, so no data migration is required.

Use a report-only allowance of the larger of 3% of target duration or 1 ms. Baseline must meet the cell before a sidecar result is attributable. Correctness always fails closed; deadline misses never change exit status, policy, or rollout authority.

The report explicitly states that pacing represents application demand, not network emulation, RTT, or loss.

## Evidence

On the initial 2026-08-02 macOS arm64 runs, both fake and pinned-Mihomo tiers met every 100–5000 Mbps deadline. At 8000 Mbps, both baselines met with worst overruns below 0.2 ms while sidecar worst overruns were 1.937 ms and 1.410 ms against a 1 ms allowance. Every correctness check passed.

This independently locates the current capacity boundary in the multi-gigabit range and reconciles it with the unpaced load ceiling. It does not prove WAN, TUN, Wi-Fi, or application performance.

## Safety and privacy review

All listeners and destinations are ephemeral loopback. Payload is a deterministic local fixture and is never exported. The Mihomo tier accepts an explicit pinned lab binary and owns its child and temporary home. The lab does not inspect active Clash, change system proxy/TUN, persist observations, or contact external destinations.

## Alternatives

| Alternative | Reason rejected |
| --- | --- |
| Call target-side sleeps a bandwidth emulator | No queue, TCP congestion, RTT, or loss semantics |
| Report only paced throughput ratios | Ratios approach one by construction below capacity and conceal deadline margin |
| Attribute every sidecar miss | Invalid when the baseline cannot satisfy the same offered load |
| Enforce the capacity boundary in CI | Shared-host scheduling makes the performance outcome environment-dependent |
| Use real destinations now | Non-deterministic and violates isolated-test policy |

## Consequences

- The project can distinguish unused maximum-throughput headroom from an offered-load capacity miss.
- The current local sidecar meets this fixed workload through 5 Gbps and misses the 8 Gbps cell on the measured host.
- The matrix remains a report-only diagnostic and cannot authorize activation.
- Real network emulation and coordinated live-entry tests remain separate gates.

## Validation and rollback

Tests cover offered-load duration math, option bounds, duplicate cells, loopback scheduling, deadline summaries, exact correctness, authority denial, and cancellation through the existing Load Lab path. Removing the command affects only isolated validation tooling.
