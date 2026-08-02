# SmartRoute Agent Guide

This file is the operating contract for humans and coding agents working in this repository.

## 1. Project mission

SmartRoute is a local-first adaptive routing layer for the Mihomo/Clash ecosystem. It preserves trusted static rules and applies explainable Direct/Proxy decisions only to traffic explicitly placed in an adaptive lane.

The project succeeds only if it improves measured connection experience or proxy usage without reducing reliability, privacy, or rollback safety.

## 2. Current phase

The repository is in Phase 0: architecture and feasibility spike.

Current scope:

- Go-based process supervisor, TCP/SOCKS5 availability Guard, adaptive sidecar, preferred-order candidate racer, ephemeral learning engine, decision engine, and bounded local observation recorder.
- Deterministic loopback Test Lab plus a pinned, isolated Mihomo child-process contract lab before any active Clash integration.
- macOS-first development while keeping platform boundaries explicit.
- TCP and TLS observability first.
- Direct-probe privacy policy is enforced before candidate creation; denied TLS targets use Proxy-only L3 validation.
- DNS as a separate diagnostic path.
- UDP/QUIC use static or historical policy until protocol-specific validators exist.
- Opt-in SQLite strong-evidence collection uses a bounded asynchronous writer; durable policy application waits for trial evidence, policy authorization, and user controls. The runtime health gate suppresses new learning but does not rewrite earlier durable rows.
- Durable evidence status and verified backup/restore-to-new-path are local lifecycle tools; destructive clear and automatic activation remain out of scope.
- Cross-session durable assessment is shadow-only and requires both strong-win and independent-session thresholds; any retained opposite-direction evidence is conflicting.
- Aggregate Shadow reports omit target keys and identities and use only targets with retained strong paired evidence as their explicit denominator.

Out of scope until an ADR changes it:

- Reimplementing TUN, DNS interception, subscriptions, or proxy protocols already provided by Mihomo.
- A full Clash Verge Rev fork.
- Generic replay of application payloads.
- Generic first-packet UDP/QUIC racing.
- Cloud collection, shared browsing history, or automatic domain uploads.
- Black-box ML routing.

## 3. Architectural invariants

These rules must not be weakened silently:

1. `REJECT`, private-network rules, administrative policy, and manual locks outrank learned policy.
2. SmartRoute replaces or intercepts selected low-confidence rules and the final catch-all; it does not wait for traffic to fall past `MATCH`.
3. `Direct failed + Proxy succeeded` is strong proxy evidence. A Direct failure alone is not.
4. A failure on both paths does not promote either route.
5. A learned policy is scoped by network profile, target identity, port, and transport.
6. Automatically learned policy expires and decays. Only users or administrators create permanent locks.
7. Once potentially side-effecting application data has been committed to a remote path, SmartRoute must not transparently replay it on another path.
8. TLS 1.3 early data must never be duplicated.
9. Unknown UDP has no generic success signal and must not be treated as if it were TCP.
10. The runtime must fail back to the user's original routing policy when SmartRoute is unavailable.
11. Domain observations, process information, and network fingerprints remain local by default.
12. Every automatic decision must expose a machine-readable reason and evidence summary.
13. Mihomo SOCKS CONNECT success is only `StageOutbound`; it must not be committed or learned as target TCP success without a stronger readiness gate.
14. TLS first-flight racing may duplicate only a fully parsed ClientHello without `early_data`; every consumed winner byte must be replayed exactly, and a valid ServerHello is L3 evidence rather than full-handshake success.
15. The availability Guard must choose the original-policy lane before forwarding client payload to either lane when the adaptive engine is unavailable; post-commit failures are never transparently replayed, and Guard-process failure remains a separate protection boundary.
16. `privacy-first`, matching `never_direct_probe` entries, invalid targets, and missing runtime policy must open zero Direct candidates; privacy denial still requires the Proxy path to meet its protocol readiness gate.
17. Guard and adaptive engine are supervised independently; restart shortens future failure windows but never justifies replaying the connection that observed a process failure, and supervisor failure requires an external service manager.
18. Persistent observation is opt-in and capacity bounded; target/profile identity is pseudonymous by default, raw events are not duplicated to stdout while recording, and recorder failure never changes a routing outcome.
19. A successful race may expose the other path only when that path completed before selection; canceled, still-running, and unstarted candidates are never learning evidence, and the winner is never delayed to manufacture a pair.
20. Runtime learning defaults to `shadow`; `ephemeral-auto` changes launch order only, never removes the opposite candidate, and every in-memory preference expires or disappears on restart.
21. Ephemeral learning accepts only a ready winner paired with an opposite failure that reached outbound admission; incomplete, canceled, not-started, privacy-forced, and pre-outbound failures never update preference counters.
22. The in-memory learning table is capacity bounded; reaching the bound may suppress new learning but must never reject, delay, or reroute the current connection.
23. Durable target identity is HMAC-pseudonymous with a separate local key; a missing key, corrupt database, corrupt record, or future schema must be reported without automatic deletion, replacement, or routing impact.
24. Durable evidence collection defaults off and is shadow-only; enqueue and runtime write failure must never delay, reject, replay, or reroute the current connection, and stored evidence cannot select a route until a later ADR authorizes it.
25. Durable backups include the target-HMAC key and are as sensitive as the live store; status/backup must not create or migrate the source, incomplete artifacts must be rejected, and restore must never overwrite or automatically activate a database.
26. A durable `direct_suggested` or `proxy_suggested` assessment is diagnostic only and must never feed `PreferredPath`, candidate order, generated rules, or an applied-policy row; retained evidence in both directions produces no suggestion.
27. Aggregate reports must never expose target keys or imply that targets with strong evidence represent all traffic; suggestion coverage alone is not proof of latency, reliability, or proxy-usage improvement.
28. Systemic-health freezing affects learning inputs and process-local preferences only. It must not change an in-flight route, replay application data, delete durable evidence, or bypass the original-policy Guard.

## 4. Repository map

Keep this map current whenever a top-level component is added, removed, or renamed.

| Path | Responsibility |
| --- | --- |
| `cmd/smartroute/` | Main CLI/daemon entry point |
| `cmd/smartroute-testlab/` | Standalone deterministic integration-test executable |
| `cmd/smartroute-mihomo-lab/` | Isolated pinned-Mihomo topology and contract probe |
| `internal/config/` | Configuration schema, defaults, validation |
| `internal/decision/` | Policy state machine and route decisions |
| `internal/learning/` | Process-local ephemeral preferences plus pure cross-session shadow assessment |
| `internal/health/` | Systemic-failure learning freeze and deterministic recovery gate |
| `internal/model/` | Stable domain types shared by internal components |
| `internal/transport/` | Candidate dialers and protocol-aware readiness gates |
| `internal/socks5/` | Minimal no-authentication SOCKS5 client/server protocol |
| `internal/sidecar/` | Inbound SOCKS server, path commitment, and TCP relay |
| `internal/guard/` | Separate pre-payload availability selection between adaptive engine and original policy |
| `internal/netrelay/` | Shared bidirectional TCP relay primitive |
| `internal/privacy/` | Local Direct-probe policy compilation, normalization, and reason codes |
| `internal/observe/` | Bounded local JSONL recording, pseudonymization, rotation, and lifecycle controls |
| `internal/store/` | Pseudonymous SQLite evidence, read-only status, async writing, retention, and verified backup/restore lifecycle |
| `internal/supervisor/` | Independent child lifecycle, bounded restart backoff, and structured service events |
| `internal/testlab/` | Loopback targets, fake gateways, and fault scenarios |
| `internal/mihomolab/` | Temporary Mihomo config, child lifecycle, synthetic DNS, and topology assertions |
| `internal/tlsinspect/` | Bounded ClientHello/ServerHello record parsing and early-data rejection |
| `internal/upstream/` | Mihomo integration boundaries and adapters |
| `docs/` | Maintained product, architecture, interface, and validation documentation |
| `docs/adr/` | Architecture Decision Records |
| `configs/` | Safe examples; never store real subscriptions or secrets |
| `scripts/` | Reproducible development and upstream-preparation commands |
| `.github/workflows/` | CI checks required before merge |

## 5. Documentation governance

Documentation is part of the implementation, not an afterthought.

For any material change, update the relevant artifacts in the same commit:

| Change | Required documentation |
| --- | --- |
| Component added/removed or responsibility moved | `docs/04-component-catalog.md` and architecture diagram |
| Public/internal interface signature changes | Interface table in `docs/04-component-catalog.md` |
| Routing semantics or safety invariant changes | New ADR in `docs/adr/` and this file when invariant changes |
| Config field added/changed | Config reference table and example config |
| Observation/event schema changes | Event catalog and migration notes |
| Large refactor | Before/after Mermaid diagram, affected-component table, and ADR |
| User-visible behavior changes | README summary and validation plan when metrics change |
| Dependency or upstream version changes | `docs/05-upstreams.md`, license note, and compatibility status |

Documentation requirements:

- Prefer Mermaid diagrams, state diagrams, sequence diagrams, and tables for relationships and lifecycle behavior.
- Keep prose for rationale and constraints, not as the only representation of architecture.
- Every interface entry must record owner, inputs, outputs, error behavior, stability, and tests.
- Every major decision uses an ADR with status, context, decision, alternatives, and consequences.
- Never document a planned interface as implemented. Mark it `planned`, `experimental`, or `stable`.
- Use exact commands and paths. Do not claim verification without recording the command and result.

### Living architecture and operating rules

Architecture diagrams, interface tables, plans, and this `AGENTS.md` describe the best verified current state; they are maintained constraints, not immutable historical snapshots.

- Change them whenever implementation evidence, experiments, upstream behavior, or project phase makes an assumption stale.
- Update affected diagrams, status labels, tables, and operating rules in the same commit as the code or decision that changed them.
- Do not preserve a stale diagram merely for visual or narrative consistency. Git history and ADRs preserve the previous design.
- Mark capabilities as `planned`, `experimental`, or `stable`; never let a future design appear to be current behavior.
- Record architectural, safety, privacy, or compatibility changes in an ADR with evidence, migration/rollback impact, and superseded decisions.
- `AGENTS.md` may evolve with the project phase, but weakening an invariant requires an explicit ADR and prominent review note.

## 6. Change workflow

Before editing:

1. Read `README.md`, the relevant design document, and all applicable ADRs.
2. Inspect the current code path and tests; do not infer runtime behavior from a diagram alone.
3. Define the smallest reversible change and its verification surface.

During editing:

1. Keep one responsibility per package.
2. Add or update tests with behavior changes.
3. Preserve backward-compatible config behavior unless an ADR explicitly approves a break.
4. Do not mix upstream source snapshots with first-party code.
5. Do not add telemetry or network calls without an explicit privacy review and configuration switch.

Before handoff or commit:

1. Run formatting, unit tests, static checks, and config validation.
2. Update diagrams, interface/config tables, ADRs, and upstream inventory when applicable.
3. Report what was verified and what remains experimental.
4. Check that no subscription URLs, tokens, domain histories, or user network identifiers are staged.

## 7. Go conventions

- Use the Go version declared in `go.mod`.
- Keep executable wiring in `cmd/`; business rules belong in `internal/` packages.
- Accept `context.Context` for blocking or cancellable operations.
- Use explicit typed errors or error wrapping; callers must be able to distinguish timeout, cancellation, validation, and path failure.
- Inject clocks and dialers into decision code to keep tests deterministic.
- Avoid package globals for mutable state.
- Keep interfaces small and consumer-owned.
- Use structured events; do not parse human log strings to drive decisions.
- Use `gofmt` and `go test ./...` before commit.
- Add race testing when concurrency enters the data path: `go test -race ./...`.

## 8. Test policy

Minimum tests by layer:

| Layer | Required coverage |
| --- | --- |
| Config | Defaults, invalid combinations, safe fallback |
| Decision engine | All outcome-matrix transitions, TTL, decay, manual precedence |
| Ephemeral learning | Strong-pair gate, shadow/auto behavior, scope isolation, thresholds, TTL, contradiction, capacity, and no route impact on rejection |
| Transport | Cancellation, stagger timing, loser cleanup, no unsafe replay |
| TLS readiness | Fragmented ClientHello, malformed input, TLS 1.3 early-data rejection |
| Mihomo adapter | Loop prevention, forced outbound mapping, unavailable sidecar fallback |
| Persistence | Schema migration, crash recovery, corrupted-record behavior |
| Observation recorder | Default pseudonymization, explicit cleartext switch, pause/resume, bounded rotation/retention, confirmed clear, safe export, and routing-independent failure |
| End-to-end | Direct-only, proxy-only, both fail, DNS fault, network-profile change |

Tests must not depend on public censorship behavior or a third-party website remaining reachable. Use deterministic local fault injection.

### Test-environment isolation

Automated tests must not inspect, change, reload, or share listeners with the user's active Clash/Mihomo environment.

- Unit tests use in-memory fixtures or `net.Pipe` where practical.
- The default integration Test Lab binds only literal loopback addresses with port `0`; the operating system allocates every port.
- The Test Lab makes no external connection and never reads or writes Clash Verge Rev profiles, generated configs, controller state, system proxy settings, or TUN state.
- An isolated Mihomo test must launch its own pinned child process with a generated temporary config, separate data directory, dedicated ports, and no system proxy/TUN changes.
- Tests involving the active Clash environment, system proxy, TUN, or real destinations are manually invoked, opt-in, and require explicit user authorization for the exact scope.
- `smartroute serve` is never started by a default automated test; automation uses `smartroute-testlab`.

### Active-environment inspection and live rollout

- Scoped read-only inspection of the user's active Clash Verge Rev files is permitted for compatibility analysis.
- Read-only results must be redacted: do not print or copy subscription URLs, credentials, controller secrets, node secrets, cookies, full rule contents, or unrelated browsing logs.
- Report which paths and structural fields were inspected; do not imply that read permission authorizes a write, reload, system-proxy change, or TUN change.
- Active configuration writes happen only in a user-coordinated live-trial window after isolated syntax/topology tests pass.
- Before a write, prepare a fresh recoverable backup, redacted diff, exact target list, verified rollback action, smoke test, and stop conditions.
- Prefer changing the durable active profile merge/script layer over editing a generated output, but determine the real binding read-only each time.
- Live observation recording is local and off by default. It requires bounded retention and explicit controls for cleartext hostname, process identity, export, pause, and deletion.
- Never record payloads, URL paths/queries, headers, cookies, credentials, subscription contents, or TLS secrets.

## 9. Upstream and dependency policy

- Track Mihomo and Clash Verge Rev as upstream references, not copied source trees, until an ADR approves a fork.
- Pin every prepared upstream to a commit or release in `docs/05-upstreams.md`.
- Record license, purpose, integration boundary, and update procedure.
- Prefer scripts that clone into ignored `.upstream/` paths over committing upstream source.
- Do not modify files inside `.upstream/`; patches belong in this repository or an explicitly managed fork.
- New runtime dependencies require a reason, license check, maintenance assessment, and tests.
- SmartRoute's first-party standalone source is MIT-licensed. GPL-licensed upstream source remains under its own license and must not be copied, linked, or redistributed as part of SmartRoute without an explicit license-boundary review.

## 10. Git and GitHub maintenance

- The canonical repository is the public `firfisa/smartroute` repository and the project license is MIT.
- The main branch must remain buildable and documented.
- Use focused commits with imperative subjects, for example `feat(decision): add unknown target state`.
- Do not commit generated binaries, real databases, logs, subscriptions, tokens, or `.upstream/` sources.
- Pull requests must include: problem, design impact, tests, risk/rollback, and documentation impact.
- CI must pass before merge.
- Tag experimental releases clearly; do not call the sidecar production-ready until the validation gates in `docs/03-mvp-validation-plan.md` pass.
- Before changing repository ownership, visibility, license, or branch protection, record the intended change and confirm it explicitly with the owner.

## 11. Security and privacy review triggers

Stop and add an explicit review section or ADR when a change:

- Sends domain, process, network, or observation data off-device.
- Exposes a controller or management API.
- Opens a listener beyond loopback.
- Changes replay, retry, TLS, DNS, or credential-handling behavior.
- Adds automatic suffix generalization.
- Probes private, link-local, metadata, or user-denied destinations.
- Changes fail-open/fail-closed behavior.
- Persists target, process, network-profile, or client-outcome observations, even when local-only.

## 12. Definition of done for Phase 0

Phase 0 is complete only when:

- The repository builds and tests from a clean checkout.
- A minimal CLI exposes version/config validation and an experimental decision trace.
- Core model and decision interfaces are documented in a maintained catalog.
- At least one ADR records the sidecar architecture.
- Example configuration contains no secrets and validates locally.
- Upstream repositories are pinned and can be prepared reproducibly.
- CI runs formatting, tests, vet, and documentation consistency checks.
- Known limitations and the next validation gate are visible from the README.
