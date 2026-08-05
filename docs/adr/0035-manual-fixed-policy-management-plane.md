# ADR-0035: Add a manual fixed-policy management plane without runtime activation

Status: Accepted
Date: 2026-08-03

## Context

SmartRoute needs a durable layer for targets the user has explicitly decided should always use Direct or Proxy. The current process-local preference disappears on restart, while the durable SQLite evidence store intentionally emits Shadow-only suggestions. Loading those suggestions into routing would violate the existing policy-authorization boundary.

An active fixed lock would also change semantics: the adaptive two-path racer would become a single-path decision, privacy denial could conflict with a Direct lock, and path failure would need a new machine-readable explanation. Combining storage, automatic promotion, and data-plane activation in one change would make rollback and evidence attribution ambiguous.

## Decision

Add a separate SQLite database and `smartroute policy list|lock|revoke` management commands. Store only explicit user-authored exact targets scoped by network profile, hostname/IP, port, and TCP transport. A lock may be permanent only because the user created it, or may carry an explicit TTL.

Do not load this database from `serve`, Guard, the ephemeral learner, or the durable evaluator. Do not implement automatic promotion, suffix generalization, rule export, or active Clash writes.

The database stores cleartext exact targets because user review and revocation require a reversible identity. It is separate from the HMAC-pseudonymous evidence database and observation recorder.

## Priority model reserved for activation

The intended later ordering is:

| Priority | Layer |
| ---: | --- |
| 1 | REJECT, private/admin policy, and privacy restrictions |
| 2 | Explicit user manual locks |
| 3 | Authorized expiring SmartRoute policies |
| 4 | Ephemeral preference and adaptive review |
| 5 | Original-policy Guard fallback when the engine is unavailable |

This ADR records the target ordering but does not implement it.

## Safety and privacy review

- The store is local, mode `0600`, and never opened by the runtime.
- Listing a missing store is non-creating and read-only.
- Corrupt and future-schema files are rejected without deletion or replacement.
- Replacement of the same exact scope revokes the old row and inserts the new row transactionally.
- Revoked and expired records remain visible only with `policy list -all`.
- Cleartext hostnames are an explicit consequence of the user's local lock command and are never copied from observations or durable evidence.
- No network connection, listener, payload handling, Clash access, or rule reload is added.

## Alternatives

| Alternative | Reason rejected |
| --- | --- |
| Apply durable suggestions automatically | Existing evidence is Shadow-only and lacks policy authority |
| Reuse the HMAC evidence table | Cannot list/review exact user intent and couples evidence with policy |
| Write directly into Clash rules | Loses SmartRoute scope/expiry/history and requires an active-write rollback window |
| Activate locks immediately | Missing privacy-conflict, failure, event-schema, and rollback contracts |
| Store suffix rules | Phase 0 has no safe generalization evidence or impact preview |

## Consequences

- Users and tests can exercise the durable manual-policy lifecycle without changing traffic.
- Permanent rules can originate only from explicit user action.
- A later activation ADR has a stable schema and CLI to build on.
- The project still does not claim that learned suggestions are fixed or applied.

## Validation and rollback

Tests cover missing read-only state, secure creation, exact normalization, permanent and expiring locks, transactional replacement history, revocation, invalid TCP scope, corruption, future schema, and CLI lifecycle. Rollback removes the management commands/config field and leaves the database untouched for explicit user handling; runtime behavior is unchanged either way.
