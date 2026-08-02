# ADR-0021: Cancel and drain accepted runtime connections before server exit

Status: Accepted
Date: 2026-08-02

## Context

Sidecar and Guard previously closed their listener when the parent context ended, but `Serve` returned as soon as `Accept` failed. Already accepted handlers were not tracked. A handler could still be blocked in SOCKS parsing or bidirectional relay while the supervisor proceeded to close observation and durable-evidence resources or start a replacement process. The relay itself had no cancellation input, so an unresponsive peer could retain a handler indefinitely.

This is not only a resource leak. It weakens the meaning of a completed service lifecycle event and makes shutdown-time observation/evidence ordering nondeterministic.

## Decision

Sidecar and Guard now create a per-`Serve` context and account for every accepted handler with a wait group. When the parent context is canceled or `Accept` ends:

1. Stop admission by closing/canceling the listener boundary.
2. Cancel the per-server context.
3. Close each accepted inbound connection, including connections still in SOCKS/TLS parsing.
4. Pass that context into bidirectional relay; cancellation closes both owned relay endpoints.
5. Wait for both relay copy directions and every accepted handler before `Serve` returns.

An externally closed listener remains a clean stop. An unexpected accept failure is returned only after active handlers have been interrupted and joined. Cancellation is never routing evidence and does not retry, replay, or create learning evidence; a committed connection is simply terminated at the explicit process-lifecycle boundary.

## Alternatives

- Return immediately and rely on process exit: rejected because tests, embedded use, and orderly recorder/SQLite shutdown need deterministic ownership.
- Let existing connections finish without interruption: rejected for Phase 0 because a silent peer can prevent supervisor recovery indefinitely.
- Add a global mutable connection registry: rejected because per-`Serve` ownership plus handler-scoped cancellation is smaller and avoids package globals.
- Replay interrupted connections through the original lane: rejected by the post-commit no-replay invariant.

## Consequences

- `Serve` now means listener admission and all accepted connection handlers have ended.
- Supervisor cancellation can interrupt an otherwise healthy active connection; this is explicit shutdown behavior, not transparent failover.
- `netrelay.Bidirectional` now requires `context.Context` and owns closure of both supplied connections on cancellation.
- Shutdown ordering can safely close recorder and durable-writer resources after `Serve` returns.

## Validation

Deterministic `net.Pipe` tests prove payload transparency before cancellation, closure of both relay endpoints after cancellation, and Sidecar/Guard return only after a pending handshake has been closed and joined. Race testing covers the cancellation/handler accounting paths without binding a local port.

## Migration and rollback

Internal callers must pass their handler context to `netrelay.Bidirectional`. Rolling back restores immediate server return and is therefore unsafe unless an equivalent accepted-connection ownership mechanism replaces it.
