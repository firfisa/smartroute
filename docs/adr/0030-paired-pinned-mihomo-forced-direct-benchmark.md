# ADR-0030: Add a pinned Mihomo forced-DIRECT benchmark tier

Status: Accepted
Date: 2026-08-02

## Context

ADR-0029 isolates SmartRoute's own local hop with an in-process fake SOCKS gateway. That result deliberately excludes the parser, DNS, listener, and outbound behavior of the pinned Mihomo integration boundary. A second tier is needed without touching the user's active Clash process or introducing public-network variability.

## Decision

Extend `smartroute-benchmark-lab` with an explicit `-mihomo PATH` tier. It starts only the supplied pinned Mihomo executable with a generated private home, validates its generated config, disables TUN, binds a forced-DIRECT mixed listener to loopback, and resolves `echo.test` through a dedicated synthetic DNS server to a loopback echo target.

| Arm | Measured path |
| --- | --- |
| Baseline | client → pinned Mihomo forced-DIRECT listener → echo target |
| Sidecar | client → SmartRoute → the same pinned Mihomo forced-DIRECT listener → echo target |

Both arms retain ADR-0029's fresh connection, SOCKS CONNECT, one-byte exact echo, alternating order, warmup, nearest-rank distributions, signed paired delta, worst-run p95, and opt-in latency enforcement. The report declares its tier and pinned version. Correctness requires config validation, a healthy child at measurement completion, exact payloads and sidecar selections, a domain-form target reaching the echo service, and zero attempts on the unused Proxy candidate.

Mihomo does not expose the same in-process gateway attempt counter as the fake tier. Its report therefore sets `direct_gateway_attempts_available=false`; zero is not interpreted as an observed attempt count.

## Safety and privacy review

The tier does not discover an installed Mihomo, active Clash files, controller state, system proxy, or TUN. Its explicit child, DNS server, echo target, unused Proxy gateway, and SmartRoute listener use loopback only. The generated home is removed on close. No target identity or observation history is persisted.

The target is plain TCP echo. The result is not TLS readiness, certificate or application success, TUN cost, external-network performance, throughput, energy use, or live-rollout authority. Both authority fields remain false.

## Alternatives

| Alternative | Reason rejected |
| --- | --- |
| Reuse the active Clash core | Violates isolation and makes the user's current connectivity part of the test fixture |
| Use a public website | Reachability, censorship, DNS, and server behavior would make the test non-deterministic |
| Compare fake and Mihomo as the two arms | Conflates SmartRoute overhead with two different gateway implementations |
| Claim Mihomo outbound attempts from successful payloads | Success proves the path worked, not an unavailable internal counter |
| Enable latency enforcement in shared CI | Runner load is not a controlled performance environment |

## Consequences

- The fake tier continues to isolate SmartRoute's own hop.
- The pinned Mihomo tier measures the same incremental hop across the intended listener boundary.
- Manual CI can reproduce correctness with the locked upstream binary while leaving latency report-only.
- TLS, load, throughput, system-proxy, and TUN tiers remain separate validation gates.

## Validation and migration

The generated config has unit assertions for loopback binding, forced `DIRECT`, synthetic DNS, and disabled TUN. The benchmark has an opt-in integration test using `SMARTROUTE_TEST_MIHOMO`. The manual pinned-Mihomo workflow runs a two-run smoke benchmark. Report schema 2 adds tier, Mihomo health/config fields, expanded isolation fields, and explicit direct-attempt availability. No runtime routing or user configuration changes.
