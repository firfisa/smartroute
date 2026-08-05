# ADR-0041: Report automatic-policy effect without target identity

Status: Accepted
Date: 2026-08-04

## Context

The first coordinated live trial produced valid automatic decisions using `durable_policy_selected`, but observation report v6 rejected that reason because its bounded reason catalog predated the automatic last-known-good runtime. Routing, recording, and SQLite policy persistence continued correctly, yet the standard aggregate could not answer the product question: did SmartRoute receive catch-all traffic, create mappings, and reuse them?

The raw rows already carried `learning_reason` and `durable_reason`, but v6 neither validated nor aggregated them. A one-off local aggregation confirmed the behavior without exposing target hashes, but a maintained product report must provide the same answer directly.

## Decision

Identity-free observation report v7:

- accepts both automatic transport reasons: `durable_policy_selected` and `durable_policy_fallback`;
- retains fail-closed bounded catalogs for routing, learning, and durable-writer reasons;
- emits `learning_reason_counts` and `durable_reason_counts` alongside existing route reasons;
- never emits the rejected value for an unknown reason, preventing a corrupt token from becoming an output channel;
- keeps JSONL schema 5 unchanged and continues to read schemas 1 through 5.

The useful automatic counters are:

| Counter | Meaning |
| --- | --- |
| `automatic_direct_path_remembered` | A new or changed target mapping was set to Direct |
| `automatic_proxy_path_remembered` | A new or changed target mapping was set to Proxy |
| `automatic_path_unchanged` | The ready result agreed with the in-memory mapping by observation time |
| `durable_policy_queued` | A changed mapping entered the bounded asynchronous SQLite writer |
| `durable_policy_selected` | The connection began on a mapping already present at lookup time |

These are routing/readiness facts, not application success. Concurrent cold-start connections may all begin before the first mapping is visible; later observations can therefore be `automatic_path_unchanged` without having started as `durable_policy_selected`.

## Alternatives

| Alternative | Decision |
| --- | --- |
| Keep using ad-hoc raw JSONL scripts | Rejected: not a maintained or independently testable interface |
| Accept every safe-looking reason token | Rejected: weakens corruption and privacy boundaries |
| Include target hashes in the report | Rejected: unnecessary for aggregate product evidence |
| Treat durable selection as application success | Rejected: it proves route reuse only |

## Consequences

- Normal reports directly show first learning, repeated agreement, durable writes, and fixed-path reuse.
- Report consumers must accept version 7.
- Existing observation files remain readable; no migration or runtime restart is required.
- Trial assessment safety semantics remain unchanged.

## Validation

Tests record automatic selected/fallback decisions, learning results, and durable queue results, then require the exact aggregate counters. Corrupt unknown reason values continue to fail without being echoed. The repaired reader successfully aggregated the ongoing live trial while the pinned routing process continued unchanged.
