# ADR-0022: Record bounded post-commit relay telemetry without claiming application success

Status: Accepted
Date: 2026-08-02

## Context

Readiness and route-selection counts cannot show how much adaptive traffic actually used Direct or Proxy after commitment. They also cannot expose whether an accepted connection ever delivered bytes from the remote side. These measurements are needed to evaluate proxy usage and shutdown behavior, but traffic volume is privacy-sensitive and a byte in either direction does not prove an HTTP request, certificate validation, login, page load, or user-visible success.

ADR-0021 made connection completion ordering deterministic, so the runtime can now emit one bounded outcome after relay copy goroutines finish without racing recorder shutdown.

## Decision

`netrelay.Bidirectional` returns directional copied-byte counts and whether its context-cancellation branch closed the relay. Sidecar emits one `relay_outcome` after each committed adaptive relay with only:

| Field | Meaning | Explicit limitation |
| --- | --- | --- |
| Target scope | Existing network profile, hostname, port and TCP identity | HMAC-pseudonymized by the recorder unless cleartext hostname was separately enabled |
| Selected path | Direct or Proxy | Does not show the static rule lane or counterfactual byte volume |
| Client-to-remote bytes | Bytes copied after path commitment | TLS ClientHello consumed and sent during readiness is excluded |
| Remote-to-client bytes | Bytes copied/replayed after commitment | May include prefetched TLS ServerHello; non-zero is not application success |
| Relay duration | Time from relay start until both copy directions end | Not end-to-end request/page latency |
| Termination | `ended` or explicit context `canceled` | `ended` does not mean success; cancellation is not routing evidence |

Payload contents, packet samples, headers, URLs, credentials, TLS secrets, per-read timing, and raw I/O errors are never recorded.

Observation JSONL advances to schema 2. The report reader continues to accept schema-1 decision/diagnostic rows, but `relay_outcome` is valid only in schema 2 with every counter present and non-negative. The identity-free observation report advances to version 2 and aggregates:

- outcomes, ended/canceled counts, and fraction with any remote-to-client bytes;
- total and Direct/Proxy directional post-commit bytes;
- relay-duration p50/p95/p99;
- explicit interpretation flags that remote bytes are not application success and byte volume covers adaptive post-commit relays only.

Aggregate addition fails on signed-64-bit overflow rather than wrapping. Relay observations never enter the learning engine or durable evidence store.

## Privacy review

Per-target byte volume can reveal browsing intensity even without payloads. Therefore it inherits the recorder's default-off switch, local-only storage, HMAC target treatment, file/retention bounds, pause/clear/export controls, mode-0600 files, and no-cloud/no-Git policy. Identity-free reports expose only path aggregates and omit target/profile/session identifiers. A pseudonymized JSONL export remains sensitive and is not anonymous.

## Alternatives

- Record no bytes: rejected because proxy-usage impact would remain unmeasurable even in a controlled trial.
- Treat any remote byte as client-visible success: rejected because TLS readiness replay alone can satisfy that condition.
- Parse HTTP/TLS application content: rejected because it crosses the payload/privacy boundary and still fails for opaque protocols.
- Record raw relay errors: rejected because platform strings are unbounded and can leak endpoint details; Phase 0 needs only ended versus explicit lifecycle cancellation.
- Add byte totals to durable learning evidence: rejected because traffic volume is not route-validity evidence.

## Consequences

- Controlled trials can quantify adaptive Direct/Proxy post-commit traffic and explicit shutdown cancellation.
- The data still cannot calculate `avoidable_proxy_ratio`, application success, total system proxy savings, or static-baseline regret without additional baseline/client-outcome instrumentation.
- TLS direction totals are asymmetric around the readiness boundary by design and must not be presented as wire-level usage.

## Validation and migration

`net.Pipe` tests verify exact directional payload counts and cancellation. Recorder tests scan persisted JSON for identity/payload leakage. Report tests cover both paths, zero remote bytes, cancellation, schema-1 compatibility, schema-2 enforcement, and overflow refusal. Existing schema-1 files remain readable; new recorders write schema 2 only. No active Clash or SQLite migration is involved.
