# ADR-0023: Correlate adaptive terminal events with random per-connection scopes

Status: Accepted
Date: 2026-08-02

## Context

ADR-0022 records one `relay_outcome` after a committed adaptive relay ends, while the earlier `decision` is emitted when a path commits. Target scope and selected path cannot safely join those rows: concurrent connections to the same target can interleave, and pairing by time is ambiguous. Without an exact join, a report cannot distinguish a complete committed connection from an outcome whose decision fell outside the report window, or a decision whose relay was still active when recording paused.

The join must not become a stable user, process, network, target, or learning identity. Entropy failure also must not enter the routing critical path.

## Decision

For each accepted Sidecar request with a parsed target, generate a fresh 128-bit random value formatted as `conn-` followed by 32 lowercase hexadecimal characters. The same value is carried by that connection's terminal `decision` or `diagnostic` and, after commitment, its `relay_outcome`.

The identifier has exactly one purpose: correlate bounded observation events from the same adaptive connection. It is not a route key, target identity, trial-session identifier, durable-evidence key, process identity, or learning signal. Guard events remain outside this Sidecar-local pairing contract.

Generation or validation failure yields an explicitly unscoped event and does not reject, delay, replay, or reroute the connection. New observation rows use JSONL schema 3. The report reader remains compatible with schema 1 and 2; those rows, and schema-3 rows created during entropy failure, are counted as unscoped.

The identity-free observation report advances to version 3 and exposes counts only:

| Count | Meaning | Must not be inferred |
| --- | --- | --- |
| Scoped/unscoped terminal and relay rows | Whether exact pairing metadata is available | Unscoped does not mean failed |
| Paired relay outcomes | A committed decision and relay outcome agree on exact target scope and selected path | The relay or application succeeded |
| Unmatched relay outcomes | The terminal row is absent from the selected report window | The decision was never emitted |
| Committed decisions without outcome | The outcome is absent from the selected report window | The relay failed; it may still have been active or truncated by pause/cutoff/crash |

Duplicate terminal/outcome rows for one scope, a relay paired with a diagnostic or uncommitted decision, and target/path contradictions make report construction fail. Error text and report JSON never expose the identifier.

## Privacy review

A random connection scope is pseudonymous correlation metadata, not anonymous data. Raw local debug events when persistence is disabled, JSONL and exports retain it so an authorized analysis can join the two bounded rows; repeated captures can therefore reveal row linkage. Persistent copies inherit the recorder's default-off switch, local-only storage, retention/capacity bounds, file permissions, pause/clear/export controls, and no-cloud/no-Git boundary. Aggregate reports omit every connection identifier and emit only counts.

The identifier contains no timestamp, hostname, network profile, process, account, sequence number, or machine-derived material. It is never sent to Mihomo, Direct, Proxy, or the destination.

## Alternatives

| Alternative | Reason rejected |
| --- | --- |
| Pair by target, path and nearest timestamp | Ambiguous for concurrent same-target connections and fragile across file rotation |
| Use a monotonically increasing counter | Leaks process ordering and collides across restarts unless combined with another identity |
| Reuse the trial-session identifier | Groups many connections and cannot provide an exact join |
| Make identifier generation mandatory for routing | Observability entropy failure must not become a connectivity failure |
| Omit identifiers from raw storage | Prevents exact local correlation and makes completeness claims unverifiable |

## Consequences

- Reports can measure event-pair completeness without guessing from target or time.
- Window truncation and crash/pause boundaries remain visible as missing/unmatched counts rather than fabricated failures.
- Raw observation exports gain sensitive linkage metadata and must remain locally controlled.
- Guard-to-Sidecar end-to-end connection correlation is still unavailable and requires a separate safety/privacy decision if later needed.

## Validation and migration

Generation tests verify format, entropy-backed uniqueness, and rejection of semantic/malformed values. Sidecar lifecycle tests verify that a decision and its relay outcome share one scope and that generation failure leaves events unscoped without altering routing. Recorder/report tests verify schema-3 persistence, schema-1/2 compatibility, exact pairing, window-truncation counts, contradiction rejection, and absence of identifiers from report JSON and safe errors.

No data migration is required. Existing schema-1/2 JSONL remains readable and is reported as connection-unscoped. No Clash configuration or SQLite schema changes are involved.
