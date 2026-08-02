# ADR-0020: Gate controlled trials with read-only evidence-based preflight

Status: Accepted
Date: 2026-08-02

## Context

A controlled trial depends on several independent safety conditions: valid configuration, explicit Direct-probe and cleartext/Auto acknowledgments, a paused bounded recorder, a healthy durable store with a matching backup, and recent isolated Test Lab and Mihomo Lab results. A prose checklist alone is easy to execute partially. A bare lab `passed` boolean is also insufficient because it does not prove report freshness, pinned-version identity, or every isolation field.

## Decision

Add `internal/trial` and `smartroute trial preflight`. The command is strictly read-only with respect to repository/runtime state and the active Clash environment. It validates:

| Gate | Required evidence |
| --- | --- |
| Configuration and privacy | Strict config validation; Direct acknowledgment for `explicit-opt-in`; separate acknowledgment for cleartext hostname recording |
| Learning | `shadow` by default; `ephemeral-auto` requires explicit acknowledgment and remains a warning |
| Observation | Recording enabled and paused before the coordinated window; earlier managed files are reported as a warning |
| Durable evidence | Existing DB/key must pass read-only validation and match a verified backup; a fresh or disabled store is handled explicitly |
| Test Lab | Current report schema, fresh timestamp, all scenarios passed, loopback/ephemeral-only, no external network or Clash access |
| Mihomo Lab | Same freshness/schema requirements plus exact pinned build marker, full readiness/Guard gates, no TUN/system-proxy/active-Clash effects |

Lab reports carry a version and UTC generation timestamp. The default maximum evidence age is 24 hours, with a five-minute future-clock tolerance. Unknown JSON fields, trailing documents, a duplicated/missing/extra required scenario name, stale reports, or contradictory isolation fields fail closed. Changing the required scenario contract requires a report-schema version change.

The JSON result uses stable check IDs and `pass`/`warn`/`fail`. Warnings do not block readiness; any failure does. It always states that persistent state was not changed and active Clash was not inspected; it never authorizes live activation.

## Alternatives

- Keep a manual checklist: rejected because it cannot be reliably automated or audited.
- Trust report file modification time and `passed`: rejected because either can disagree with report contents.
- Have preflight automatically pause recording, create backups, or run labs: rejected because that would mix read-only evaluation with state changes and network/process execution.
- Inspect or patch active Clash during preflight: rejected because readiness evidence and live authorization are separate boundaries.

## Consequences

- Operators must deliberately pause observations and supply fresh report files.
- Existing durable state blocks readiness until a verified status-matching snapshot is supplied.
- The command can be rerun safely and produces machine-readable failure evidence.
- A `ready: true` report remains only a prerequisite artifact. The owner must still approve the exact live-trial window and active configuration changes under ADR-0003.

## Migration and rollback

Reports created before schema version 1 lack freshness metadata and are rejected; rerun the isolated lab to migrate evidence. Rollback is removal of the CLI/package and report metadata fields. No user state or active configuration requires migration.
