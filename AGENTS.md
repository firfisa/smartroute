# SmartRoute Agent Guide

This file is the operating contract for humans and coding agents working in this repository.

## 1. Project mission

SmartRoute is a local-first adaptive routing layer for the Mihomo/Clash ecosystem. It preserves trusted static rules and applies explainable Direct/Proxy decisions only to traffic explicitly placed in an adaptive lane.

The project succeeds only if it improves measured connection experience or proxy usage without reducing reliability, privacy, or rollback safety.

## 2. Current phase

The repository is in Phase 0: architecture and feasibility spike.

Current scope:

- Go-based process supervisor, TCP/SOCKS5 availability Guard, adaptive sidecar, first-readiness candidate decision, bounded automatic last-known-good index, and local observation recorder.
- Deterministic loopback Test Lab plus a pinned, isolated Mihomo child-process contract lab before any active Clash integration.
- macOS-first development while keeping platform boundaries explicit.
- TCP and TLS observability first.
- Direct-probe privacy policy is enforced before candidate creation; denied TLS targets use Proxy-only L3 validation.
- DNS as a separate diagnostic path.
- UDP/QUIC use static or historical policy until protocol-specific validators exist.
- Canonical `auto` mode and local persistence are the defaults. The first TCP/TLS-ready path is immediately remembered in an HMAC-keyed bounded index without per-target approval, promotion counters, temporary tiers, or TTL; `durable-auto` is a compatibility spelling.
- A full isolated Runtime Lab now exercises the real Clash transform, pinned Mihomo, actual `smartroute supervise` children, SQLite restart reuse, and opposite overwrite before any active installation.
- A macOS user LaunchAgent may own the exact pinned trial supervisor command from a private Application Support runtime so an active transform never depends on a terminal or agent session for top-level liveness; cross-platform service installation remains future work.
- Durable evidence status and verified backup/restore-to-new-path remain legacy diagnostic lifecycle tools. Evidence deletion remains out of scope; automatic policy rows may be cleared independently.
- Automatic last-known-good mappings have no promotion counter, provisional/confirmed tier, or TTL. A later successful opposite fallback overwrites the exact mapping immediately; cross-session thresholds remain analytical only.
- Aggregate Shadow reports omit target keys and identities and use only targets with retained strong paired evidence as their explicit denominator.
- Paused observation reports aggregate readiness, path selection, Guard fallback, and timing without returning target/profile hashes; readiness must not be labeled application success.
- Enabled runtime observation uses a random trial session shared by supervisor children and restarts; human-readable session labels are rejected and aggregate reports omit concrete IDs.
- Shadow, ephemeral promotion, cross-session suggestion, health-freeze, and user-authored fixed-policy facilities are retained only as legacy diagnostics or advanced management surfaces. They are not the MVP runtime path and receive no new feature work without measured trial evidence.

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
6. An automatic last-known-good mapping is not a manual lock. It may be overwritten by a later successful opposite fallback, removed by bounded capacity eviction or explicit clear, and is isolated by network profile.
7. Once potentially side-effecting application data has been committed to a remote path, SmartRoute must not transparently replay it on another path.
8. TLS 1.3 early data must never be duplicated.
9. Unknown UDP has no generic success signal and must not be treated as if it were TCP.
10. The runtime must fail back to the user's original routing policy when SmartRoute is unavailable.
11. Domain observations, process information, and network fingerprints remain local by default.
12. Every automatic decision must expose a machine-readable reason and evidence summary.
13. Mihomo SOCKS CONNECT success is only `StageOutbound`; it must not be committed or learned as target TCP success without a stronger readiness gate.
14. TLS first-flight racing may duplicate only a fully parsed ClientHello without `early_data`; every consumed winner byte must be replayed exactly, and a valid ServerHello is L3 evidence rather than full-handshake success.
15. The availability Guard must choose the original-policy lane before forwarding client payload to either lane when the adaptive engine is unavailable; post-commit failures are never transparently replayed, and Guard-process failure remains a separate protection boundary.
16. Trial readiness must be derived from fresh, content-validated isolated-lab evidence and explicit privacy/risk acknowledgments. A successful preflight is read-only and never authorizes an active Clash write, reload, or trial start.
17. Sidecar and Guard shutdown must cancel every accepted handshake/relay, close both owned relay connections, and wait for all connection handlers before `Serve` returns; shutdown interruption is never routing evidence or a replay trigger.
18. Relay telemetry may persist only post-commit directional byte counts, duration, selected path, and bounded termination. Remote bytes include protocol readiness data and are never application-success evidence; zero remote bytes alone are never a learned failure.
19. `privacy-first`, matching `never_direct_probe` entries, invalid targets, and missing runtime policy must open zero Direct candidates; privacy denial still requires the Proxy path to meet its protocol readiness gate.
20. Guard and adaptive engine are supervised independently; restart shortens future failure windows but never justifies replaying the connection that observed a process failure, and supervisor failure requires an external service manager.
21. Persistent observation is opt-in and capacity bounded; target/profile identity is pseudonymous by default, raw events are not duplicated to stdout while recording, and recorder failure never changes a routing outcome.
22. A successful race may expose the other path only when that path completed before selection; canceled, still-running, and unstarted candidates are never learning evidence, and the winner is never delayed to manufacture a pair.
23. Runtime learning defaults to `auto`: the first ready path becomes the exact last-known-good mapping immediately. A mapping hit opens one path and must attempt the opposite path once after a pre-commit selected-path failure; opposite readiness overwrites immediately.
24. Automatic mappings have no approval, win/session threshold, provisional state, confidence tier, or TTL. Legacy ephemeral evidence rules must never gate or alter the automatic path.
25. The in-memory automatic index is capacity bounded; eviction may remove an old mapping but must never reject, delay, or reroute the current connection.
26. Durable target identity is HMAC-pseudonymous with a separate local key; a missing key, corrupt database, corrupt record, or future schema must be reported without automatic deletion, replacement, or routing impact.
27. Automatic application and local persistence default on. Enqueue or write failure must never delay, reject, replay, or alter the current connection; `auto` updates its bounded memory index immediately after readiness and persists the change asynchronously for later processes.
28. Durable backups include the target-HMAC key and are as sensitive as the live store; status/backup must not create or migrate the source, incomplete artifacts must be rejected, and restore must never overwrite or automatically activate a database.
29. Raw `direct_suggested` and `proxy_suggested` assessments remain legacy diagnostics and never feed runtime selection. In `auto`, only an actual TCP/TLS-ready selected path creates or overwrites an exact HMAC-keyed mapping; no automatic suffix/generalized rule is permitted.
30. Aggregate reports must never expose target keys or imply that targets with strong evidence represent all traffic; suggestion coverage alone is not proof of latency, reliability, or proxy-usage improvement.
31. Legacy systemic-health freezing must not affect `auto` lookup or updates. Both-path failure leaves the existing mapping unchanged; no health state may change an in-flight route, replay application data, delete policies, or bypass the original-policy Guard.
32. Observation reports must omit target/profile identifiers, reject corrupt or incompatible rows, and label TCP/TLS readiness separately from application or client-visible success.
33. Trial-session identifiers must be random non-semantic scopes, never account/network/user labels; supervisor children share one ID across restarts, while aggregate reports expose only counts.
34. Connection identifiers must be random non-semantic correlation scopes, never learning or identity keys. Missing or invalid generation must not affect routing; aggregate reports omit identifiers, count incomplete pairs explicitly, and never reinterpret them as path failure.
35. `original_fallback` is a declared category for the original-policy listener, not an observed counterfactual. Baseline reports must label it as declared, require controlled-trial acknowledgment, and never call changed winner bytes or selections saved traffic or application success.
36. Relay direction endings may persist only fixed EOF/timeout/reset/closed/I/O-error/canceled tokens; raw errors are forbidden. These tokens are diagnostic transport metadata, never application success, learned path failure, retry authority, or replay authority.
37. A post-trial data-quality pass authorizes descriptive analysis only. It must never be presented as verified baseline improvement, client-visible success, statistical significance, policy-change authority, or permission to modify/reload Clash.
38. Controlled-trial assessment session, configuration fingerprint, time window, and thresholds must be fixed by a successful preflight before activation. Assessment must reject plan/config drift and any missing, unscoped, or unexpected session without exposing concrete session identifiers in aggregate output.
39. Synthetic Trial Lab output is plumbing evidence only. It must be visibly synthetic, identity-free, temporary, network-free, unacceptable to preflight, and incapable of authorizing a live trial or policy change.
40. Loopback benchmark correctness is mandatory, but its latency gate is environment-dependent and opt-in. Results must identify the fake-gateway or pinned-Mihomo tier and never be generalized to unmeasured TUN/TLS, real application success, product benefit, or live-trial authority.
41. TLS benchmark input must be a fully parsed ClientHello without `early_data`; success requires an exact structurally valid ServerHello and must remain labeled L3 readiness rather than full handshake, certificate, HTTP, or application success.
42. Concurrent relay load is a separate chunked-echo experiment, not a latency or maximum-throughput benchmark. Byte correctness is mandatory; its environment-dependent ratio gate is opt-in, and a missed gate must remain visible rather than be moved after observing results.
43. The fixed-policy database contains only explicit user-authored exact targets and is management-plane-only in Phase 0. Durable suggestions, observations, and ephemeral preferences must never populate it; `serve`, Guard, and candidate ordering must not read it until a later ADR authorizes activation.
44. Automatic durable policy lookup performs no SQLite I/O on the connection path; startup loads at most `learning.max_entries` pseudonymous keys into a bounded index.
45. Automatic mappings use the last known ready path immediately, without win thresholds, independent-session promotion, provisional/confirmed tiers, or TTL. Any future lifecycle mechanism requires measured trial evidence and a new ADR.
46. A live candidate package is sensitive, private, and checksum-gated. Install/rollback may atomically replace only the verified active script, require explicit confirmation, and must not reload Clash; binding or content drift must fail closed.
47. Live strong evidence in legacy diagnostic modes is bounded by both retention time and `learning.persistence.max_evidence_rows`; runtime trimming may exceed the configured row ceiling by at most 255 queued writes and must return to the exact ceiling at clean startup/shutdown.
48. A healthy automatic policy opens one candidate. Its opposite candidate may open only after selected-path pre-commit failure; committed application data is never replayed.
49. Automatic mode persists only the exact last-ready mapping through a bounded asynchronous writer. It must not create evidence/session rows, run cross-session assessment, or validate unused promotion, TTL, health-freeze, retention, or suggestion settings.
50. A selected automatic TLS path receives at most half the total readiness timeout so one sequential opposite attempt retains a real time budget. A healthy mapping still opens one candidate, and this split must not become an unmeasured per-target tuning system.
51. A live transform may be reloaded only after Engine and Guard pass the local `armed` topology check. Rollback restores and reloads the original Clash script before stopping the runtime; topology checks never send a destination CONNECT.
52. Observation aggregates must recognize every emitted automatic route, learning, and writer reason through bounded catalogs and expose only identity-free counts. Unknown reasons fail closed without echoing the rejected value.
53. While an active Clash transform routes traffic to the Guard, the top-level supervisor must be owned by an external service boundary independent of a terminal or coding-agent session. That boundary must use the verified pinned runtime command, keep logs local/private, and preserve restore-and-reload-before-stop rollback order.

## 4. Repository map

Keep this map current whenever a top-level component is added, removed, or renamed.

| Path | Responsibility |
| --- | --- |
| `cmd/smartroute/` | Main CLI/daemon entry point |
| `cmd/smartroute-testlab/` | Standalone deterministic integration-test executable |
| `cmd/smartroute-trial-lab/` | Network-free synthetic observation/assessment rehearsal executable |
| `cmd/smartroute-benchmark-lab/` | Paired loopback sidecar overhead benchmark executable |
| `cmd/smartroute-load-lab/` | Concurrent chunked-echo relay load executable |
| `cmd/smartroute-load-sweep/` | Fixed concurrency/payload matrix and runtime-allocation diagnostic executable |
| `cmd/smartroute-capacity-lab/` | Fixed client-paced offered-load capacity executable |
| `cmd/smartroute-mihomo-lab/` | Isolated pinned-Mihomo topology and contract probe |
| `cmd/smartroute-runtime-lab/` | Full process-level Clash transform, supervisor, persistence, restart, and overwrite lab |
| `internal/config/` | Configuration schema, defaults, validation |
| `internal/decision/` | Policy state machine and route decisions |
| `internal/learning/` | Automatic target-key helpers plus legacy ephemeral/Shadow diagnostics |
| `internal/fixedpolicy/` | User-authored exact-target lock/list/revoke SQLite management plane; no runtime activation |
| `internal/health/` | Systemic-failure learning freeze and deterministic recovery gate |
| `internal/model/` | Stable domain types shared by internal components |
| `internal/connectionid/` | Random non-semantic per-connection observation scopes |
| `internal/transport/` | Candidate dialers and protocol-aware readiness gates |
| `internal/socks5/` | Minimal no-authentication SOCKS5 client/server protocol |
| `internal/sidecar/` | Inbound SOCKS server, path commitment, and TCP relay |
| `internal/guard/` | Separate pre-payload availability selection between adaptive engine and original policy |
| `internal/netrelay/` | Shared bidirectional TCP relay primitive |
| `internal/privacy/` | Local Direct-probe policy compilation, normalization, and reason codes |
| `internal/observe/` | Bounded local JSONL recording, pseudonymization, rotation, and lifecycle controls |
| `internal/store/` | Pseudonymous SQLite evidence diagnostics plus a bounded last-known-good mapping/index and dedicated asynchronous policy writer |
| `internal/supervisor/` | Independent child lifecycle, bounded restart backoff, and structured service events |
| `internal/testlab/` | Loopback echo/TLS targets, exact handshake fixtures, mutable fake gateways, base faults, and the four-step last-known-good learning contract |
| `internal/triallab/` | Temporary synthetic recorder/report/assessment integration scenarios |
| `internal/runtimecheck/` | Local-only baseline/armed/running SOCKS topology doctor for live sequencing |
| `internal/benchlab/` | Alternating baseline/sidecar TCP echo and TLS ServerHello benchmark |
| `internal/loadlab/` | Alternating concurrent baseline/sidecar chunked relay load |
| `internal/mihomolab/` | Temporary Mihomo config, child lifecycle, synthetic DNS, and topology assertions |
| `internal/trial/` | Read-only controlled-trial prerequisites, pre-registered assessment plans, and descriptive data-quality gates |
| `internal/tlsinspect/` | Bounded ClientHello/ServerHello record parsing and early-data rejection |
| `internal/upstream/` | Mihomo integration boundaries and adapters |
| `docs/` | Maintained product, architecture, interface, and validation documentation |
| `docs/adr/` | Architecture Decision Records |
| `configs/` | Safe examples; never store real subscriptions or secrets |
| `integrations/clash-verge/` | Idempotent post-script transform for the final MATCH and loopback SmartRoute objects |
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
| Automatic routing | First readiness, exact scope, same-path reuse, pre-commit fallback, immediate overwrite, no expiry, capacity, and persistence failure independence |
| Legacy ephemeral learning | Strong-pair gate, scope isolation, thresholds, TTL, contradiction, capacity, and proof that it is absent from `auto` |
| Transport | Cancellation, stagger timing, loser cleanup, no unsafe replay |
| TLS readiness | Fragmented ClientHello, malformed input, TLS 1.3 early-data rejection |
| Mihomo adapter | Loop prevention, forced outbound mapping, unavailable sidecar fallback |
| Persistence | Schema migration, crash recovery, corrupted-record behavior |
| Fixed policy | Exact scope, replacement history, expiry, revoke, read-only missing state, corruption/future schema, and no runtime activation |
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
- `smartroute-trial-lab` uses only a removed temporary workspace and synthetic metadata; its report is never accepted as preflight evidence.
- `smartroute-runtime-lab` requires explicit Mihomo, SmartRoute, Node, composer, and apply-script inputs; creates only a removed private workspace; binds literal loopback ephemeral ports; starts only its owned children; and must report zero external network, active Clash, TUN, or system-proxy access.
- `smartroute-benchmark-lab` uses literal loopback and ephemeral ports only. Its optional pinned-Mihomo tier accepts only an explicit lab binary and owns a temporary child/home; it never discovers the active installation. Its TLS protocol uses only the parsed no-early-data synthetic first flight and reports ServerHello readiness, not full-handshake success. CI checks correctness without enforcing latency; performance enforcement is an explicit platform run.
- `smartroute-load-lab`, `smartroute-load-sweep`, and `smartroute-capacity-lab` have the same isolation and explicit-pinned-child boundary. They report only aggregate connection/byte/throughput/runtime distributions, never fixture payloads or raw per-connection rows. Runtime metrics cover the current Go process only, exclude kernel and Mihomo-child work, and short-window CPU deltas are diagnostic rather than a performance gate. Capacity pacing is a client-side offered-load schedule, not bandwidth/RTT/loss emulation. CI enforces correctness but not environment-dependent performance gates.
- `make macos-launch-agent-test` creates a private synthetic runtime and parses a generated plist in a removed temporary directory. `make relocate-live-runtime-test` verifies stopped-runtime path rebasing using only temporary files and loopback port allocation. Neither calls `launchctl`, installs a LaunchAgent, or reads active Clash. Live bootstrap, bootout, or restart testing is manual and coordinated like any other active-environment operation.

### Active-environment inspection and live rollout

- Scoped read-only inspection of the user's active Clash Verge Rev files is permitted for compatibility analysis.
- Read-only results must be redacted: do not print or copy subscription URLs, credentials, controller secrets, node secrets, cookies, full rule contents, or unrelated browsing logs.
- Report which paths and structural fields were inspected; do not imply that read permission authorizes a write, reload, system-proxy change, or TUN change.
- Active configuration writes happen only in a user-coordinated live-trial window after isolated syntax/topology tests pass.
- Before a write, prepare a fresh recoverable backup, redacted diff, exact target list, verified rollback action, smoke test, and stop conditions.
- Prefer changing the durable active profile merge/script layer over editing a generated output, but determine the real binding read-only each time.
- Live observation recording is local and off by default. It requires bounded retention and explicit controls for cleartext hostname, process identity, export, pause, and deletion.
- An active SmartRoute transform must not rely on a foreground shell, PTY, or coding-agent execution cell to keep the Supervisor alive. Use the verified OS-service boundary or roll back the transform before ending that owner.
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
