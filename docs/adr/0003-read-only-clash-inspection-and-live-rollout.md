# ADR-0003: Read-only Clash inspection and coordinated live rollout

- Status: Accepted
- Date: 2026-08-02
- Owners: SmartRoute maintainers

## Context

ADR-0002 keeps automated network tests independent from the user's active Clash/Mihomo environment. That isolation remains necessary, but a total prohibition on reading the active configuration would hide the actual profile hierarchy, generated files, listener settings, and integration constraints that SmartRoute must eventually support.

The owner has authorized read-only inspection of the active Clash Verge Rev environment. Configuration writes, reloads, system-proxy changes, and TUN changes remain prohibited until the isolated implementation is mature enough for a jointly supervised live trial.

## Decision

SmartRoute uses three distinct operational lanes:

| Lane | Active Clash reads | Active Clash writes/reloads | Authorization |
| --- | ---: | ---: | --- |
| Automated Test Lab and CI | No | No | Default |
| Read-only compatibility inspection | Yes, scoped and redacted | No | Permitted by owner; report exact paths inspected |
| Coordinated live trial | Yes | Yes, only approved files/actions | Explicit maintenance window and owner confirmation |

Read-only inspection may examine file names, profile relationships, structural keys, sanitized logs, and runtime topology. It must not print, copy into Git, or export subscription URLs, credentials, controller secrets, cookies, node secrets, or full browsing history.

A live configuration replacement is not a routine coding step. Before any write or reload, SmartRoute must provide:

1. The exact source files and generated files affected.
2. A fresh recoverable backup and its location.
3. An isolated syntax/topology validation result against the pinned Mihomo version.
4. A redacted before/after diff.
5. A rollback command or action tested against a disposable copy.
6. Clear stop conditions and a short maintenance window agreed with the owner.

## Observation policy for a live trial

Live-trial observations are local, opt-in, bounded, and structured. The default record may include timestamps, a local session ID, a pseudonymous network-profile ID, target identity according to the selected privacy mode, port/transport, candidate stages and latency, failure classes, selected route, reason code, and client-visible outcome.

It must not include payload bytes, URL paths or queries, HTTP headers, cookies, credentials, subscription contents, raw TLS secrets, or packet captures. Cleartext hostnames and process identity require separate switches. Retention, export, pause, and deletion controls must exist before the recorder is used for a real trial.

## Consequences

- SmartRoute can adapt to the real Clash Verge profile hierarchy without risking accidental edits during development.
- Automated Test Lab claims remain strong because it still performs zero active-environment reads.
- Live rollout requires more ceremony, but failures have a defined recovery path.
- Observation design becomes an explicit privacy surface rather than an incidental log stream.

## Relationship to ADR-0002

ADR-0002 remains accepted for automated tests and isolated Mihomo processes. This ADR adds a separate read-only compatibility lane and defines the future, owner-coordinated write boundary.
