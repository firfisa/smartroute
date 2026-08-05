# ADR-0025: Record bounded directional relay end reasons without raw I/O errors

Status: Accepted
Date: 2026-08-02

## Context

ADR-0022 deliberately stored only global `ended` or lifecycle `canceled` termination. That protects privacy but collapses materially different post-commit behavior: EOF, timeout, reset/broken pipe, local closed connection, and an unclassified I/O error all appear as `ended`. Byte counts alone cannot distinguish an orderly stream end from a transport disruption, and zero remote bytes still must not be promoted into a learned failure.

Raw Go/network error strings are unsuitable observation data. They are platform-dependent, unbounded, and may contain hostnames, IP addresses, ports, filesystem details, or wrapped implementation text. Direction end signals also remain transport observations, not application outcomes.

## Decision

`netrelay.Bidirectional` retains each `io.Copy` result and maps its error to exactly one bounded token:

| Token | Classification | Explicit limitation |
| --- | --- | --- |
| `eof` | `io.Copy` ended without error or with EOF | Does not prove an application completed successfully |
| `timeout` | Error implements `net.Error` and reports timeout | Does not identify whether read, write, peer, or local policy caused it |
| `reset` | Wrapped connection reset or broken pipe | Does not assign blame to Direct, Proxy, client, or server |
| `closed` | Known closed-network/closed-pipe sentinel | May follow ordinary local shutdown behavior |
| `io_error` | Any other copy error | Intentionally discards the raw error and endpoint detail |
| `canceled` | Explicit relay context cancellation | Applied to both directions because the runtime lifecycle ended the relay |

The Sidecar emits `client_to_remote_end` and `remote_to_client_end` with every new `relay_outcome`. The old global `termination` remains because explicit lifecycle cancellation is an important process-level boundary. A canceled outcome requires both directional tokens to be `canceled`; an ended outcome cannot contain `canceled`.

Observation JSONL advances to schema 5 and requires both tokens on new relay outcomes. The report reader remains compatible with schemas 1–4; older relay rows contribute to an `unclassified` bucket in both directions. Identity-free report version 5 emits fixed per-direction counts for all six tokens plus `unclassified`.

Directional end reasons never enter ephemeral learning, durable evidence, route selection, retry, or replay. They are diagnostic aggregates only.

## Privacy and safety review

Only the fixed tokens above may be persisted. Recorder and report validation reject arbitrary strings and never include rejected values in error text. No wrapped error, address, hostname, syscall text, payload, packet sample, or per-read timing is recorded.

The report includes a machine-readable `direction_ends_not_application_success` interpretation flag. EOF is not labeled clean success; reset/timeout/I/O error is not automatically labeled a route failure. Correlating these signals with client-visible outcomes or using them for learning requires a later ADR and stronger validation.

## Alternatives

| Alternative | Reason rejected |
| --- | --- |
| Keep only `ended`/`canceled` | Cannot characterize post-commit transport reliability |
| Persist raw errors | Unbounded, unstable, and may leak endpoint identity |
| Parse error strings | Platform fragile and encourages accidental identity capture |
| Treat EOF as application success | EOF says nothing about HTTP status, TLS completion, page load, or user intent |
| Learn from reset/timeout immediately | Post-commit direction errors lack the counterfactual and application context required by learning invariants |
| Record only the first direction to finish | Loses asymmetric shutdown evidence and can misrepresent half-close behavior |

## Consequences

- Controlled trials can compare bounded post-commit transport endings by direction without retaining raw error detail.
- Historical schema-2/3/4 relay rows remain usable but are visibly unclassified for this dimension.
- Explicit runtime cancellation remains separate from network-path evidence.
- The classification is intentionally coarse and cannot establish end-to-end experience on its own.

## Validation and migration

Unit tests cover EOF, timeout, reset, closed, unknown error, and cancellation override without exposing the supplied error text. Sidecar lifecycle tests verify bounded reasons reach the outcome. Recorder/report tests reject unsafe or inconsistent tokens, aggregate asymmetric reasons, preserve legacy rows as unclassified, and keep report JSON free of raw event fields.

No stored data is rewritten. No Clash configuration, SQLite schema, payload handling, or retry behavior changes.
