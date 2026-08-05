# Changelog

All notable project changes are recorded here.

## Unreleased

## [0.1.0] - 2026-08-05

### Added

- macOS live-trial LaunchAgent generator with exact pinned-runbook validation, private logs, `KeepAlive`/`RunAtLoad`, synthetic no-`launchctl` tests, ADR-0043, and a verified first live handoff.
- Stopped-runtime relocation into private Application Support storage with absolute config/runbook rebasing, learning-store validation, synthetic testing, and a live forced-Supervisor restart that recovered all five endpoints under `launchd`.
- Semantic active-script binding verification that tolerates unrelated Clash Verge registry metadata rewrites while still rejecting a different current profile/script path or content, under ADR-0042.
- Local-only `smartroute doctor` with `baseline`, `armed`, and `running` phases; SOCKS checks stop after method negotiation and never send a destination CONNECT.
- Private live-runtime workspace preparation with a pinned binary, exact candidate topology, absolute state paths, paused bounded observations, random trial scope, and a reverse-order rollback runbook.
- Identity-free observation report v7 counters for automatic learning and durable-writer outcomes, including fixed-path selection and fallback reasons.
- ADR-0040 requiring Engine/Guard to be armed before active reload and ADR-0041 making automatic-policy effect visible without target identity.
- First coordinated live-trial snapshot: 34 adaptive Guard decisions across 20 target scopes, 22 Direct and 12 Proxy selections, 20 newly persisted mappings, 14 same-path observations, and zero readiness failure in the initial window.

- Project operating contract in `AGENTS.md`.
- Product assessment, technical design, MVP validation plan, component catalog, protocol matrix, and ADR process.
- Go module with strict local configuration validation.
- Explainable paired Direct/Proxy observation evaluator.
- Human-readable observation JSON using readiness stage names and millisecond latency.
- Experimental `version`, `validate`, and synthetic `trace` CLI commands.
- Reproducible upstream locks for Mihomo v1.19.29 and Clash Verge Rev v2.5.2.
- Source-level verification of Mihomo forced-listener routing and SOCKS domain preservation at the locked commit.
- Documentation checks, tests, vet, formatting, and GitHub Actions workflow.
- MIT license for SmartRoute's first-party standalone source.
- Living-architecture governance: diagrams, catalogs, ADRs, and `AGENTS.md` evolve with verified implementation evidence.
- Portable documentation checks with a standard `grep` fallback, plus current GitHub Actions runtimes.
- Minimal no-authentication SOCKS5 client/server protocol with domain-form target preservation.
- Staggered Direct/Proxy TCP candidate racing with loser cancellation and structured reason codes.
- Experimental loopback sidecar relay and `smartroute serve` command.
- Standalone `smartroute-testlab` with ephemeral loopback ports, fake gateways, deterministic faults, and JSON reports.
- ADR and enforced rules that keep automated tests separate from the active Clash/Mihomo environment.
- Read-only, redacted active Clash inspection policy and a coordinated configuration replacement/rollback boundary.
- Local observation schema, privacy controls, retention requirements, and live-trial procedure.
- Isolated pinned-Mihomo child-process lab with temporary home, synthetic DNS, random loopback ports, and no TUN/system-proxy changes.
- Runtime verification of forced Direct, forced Proxy, SOCKS domain preservation, and loop prevention on Mihomo v1.19.29.
- Conservative readiness correction: Mihomo SOCKS success is `StageOutbound`, while the sidecar requires `StageTCP` to commit.
- `candidate_below_commit_stage` rejection so plain transport candidates fail safely below L2.
- Manual GitHub Actions workflow for the isolated pinned-Mihomo contract lab.
- Bounded TLS record inspector with fragmented ClientHello/ServerHello support and stable failure classes.
- Pre-dial TLS `early_data` rejection, staggered L3 candidate racing, loser cancellation, and exact ServerHello replay.
- Experimental `serve` TLS mode and `event_type`-discriminated diagnostics for rejected ClientHello or failed TLS candidates.
- End-to-end Mihomo recovery from an unreachable Direct target to a Proxy `StageTLS` winner.
- In-memory Go `crypto/tls` 1.3 handshake and encrypted echo through the selected sidecar path.
- Separate `smartroute guard` process that selects the adaptive engine or the user's original-policy listener before accepting client payload.
- Bounded fallback from a refused or wedged adaptive-engine SOCKS handshake, with structured `guard_decision` reason codes.
- Isolated Mihomo engine stop, original-policy fallback, restart, and adaptive-return scenarios; platform runtime evidence is pending because the current sandbox denied loopback bind.
- ADR-0006 documenting why Mihomo's stock `fallback` group cannot retry the same failed connection.
- Runtime `privacy-first` and `never_direct_probe` enforcement before Direct candidate creation, with exact/suffix matching and fail-closed target normalization.
- Single-path TLS L3 connector used for privacy-forced Proxy traffic, preserving ServerHello readiness and prefetched-byte replay without opening Direct.
- Structured privacy decision/failure reasons and ADR-0007 covering matching, precedence, migration and rollback semantics.
- `smartroute supervise` with independently monitored Guard/engine child processes, structured lifecycle events, stable-window reset and capped exponential restart backoff.
- Graceful child interruption with a bounded kill delay, synchronized child event writers, and ADR-0008 documenting residual Guard-down and supervisor-down windows.
- Opt-in local JSONL observations with HMAC-pseudonymous targets/profiles, per-source rotation, count/age caps, and mode-0600 state.
- `smartroute observations` status, pause, resume, clear-confirmation, and redacted export controls, plus ADR-0009.
- Raw runtime event suppression while persistent recording is enabled; later recorder write failures warn once without interrupting routing.
- Optional `other_observation` evidence when the opposite candidate completed before route selection; canceled, running, and unstarted losers remain excluded.
- Symmetric validated JSON marshal/unmarshal for observation stage and millisecond latency fields, plus ADR-0010.
- Process-local strong-pair learning keyed by network profile, normalized target, port, and transport, with consecutive thresholds, TTL, and immediate contradiction handling.
- Safe-default `shadow` and explicit `ephemeral-auto` learning modes, including backward-compatible shadow defaults for legacy configs.
- Preferred-order Direct/Proxy racing where an early preferred-path failure starts the opposite path immediately; learned policy never becomes a single-path lock.
- Capacity-bounded ephemeral policy table with expired-entry reclamation and non-disruptive refusal of new learning when full.
- Decision/JSONL learning reason and ephemeral policy-state metadata, plus ADR-0011.
- CGo-free SQLite strong-evidence store using `modernc.org/sqlite v1.55.0`, with schema-v1 migrations, WAL, integrity checks, explicit pruning and checkpointing.
- HMAC-pseudonymous durable target keys backed by a separate mode-0600 key; cleartext hostname/profile never enter SQLite.
- Durable independent-session summaries, safe failure tokens, corruption/future-schema refusal, and ADR-0012.
- Opt-in `serve` integration for strong evidence using a non-blocking bounded writer, startup retention pruning, random sessions, bounded drain/checkpoint shutdown, and safe queue/write metadata.
- Learning persistence configuration with backward-compatible defaults; disabled mode creates no database or key, and stored evidence remains shadow-only under ADR-0013.
- `smartroute learning status|backup|verify-backup|restore` with no-create read-only inspection, SQLite online snapshots, SHA-256 manifests, private-copy verification, new-path-only restore, and explicit incomplete markers under ADR-0014.
- Transactional retention now removes empty historical sessions, with runtime startup reordered to prune before creating the current session.
- Deterministic cross-session Shadow assessment requiring both strong-win and distinct-session thresholds, with conservative any-opposite-direction conflict handling under ADR-0015.
- Asynchronous `durable_learning_assessment` events after successful writes plus read-only exact-target `smartroute learning evaluate`; neither feeds route selection.
- Identity-free `smartroute learning report` with in-database exact-target grouping, shared evaluator semantics, explicit retention/thresholds, and insufficient/conflicting/Direct/Proxy aggregate counts under ADR-0016.
- Concurrent systemic-health gate with distinct-target failure windows, Proxy-specific recovery, expiry, immediate network/portal signals, ephemeral-policy clearing, durable-write suppression, and structured `learning_health` events under ADR-0017.
- Paused, identity-free `observations report` with strict JSONL validation, readiness/path/Guard aggregates, p50/p95/p99 decision and candidate latency, explicit interpretation limits, and no target/profile hashes under ADR-0018.
- Random `trial_session_id` scoping shared by supervisor, Guard, engine and child restarts, with legacy unscoped accounting and identity-free session counts under ADR-0019.
- Versioned, UTC-timestamped Test Lab and Mihomo Lab evidence plus `smartroute trial preflight`, which fails closed on stale/incomplete isolation evidence, missing privacy/Auto acknowledgments, unpaused recording, or an unmatched durable backup without inspecting or authorizing active Clash, under ADR-0020.
- Context-aware bidirectional relay and deterministic Sidecar/Guard shutdown: cancellation closes pending handshakes and both relay endpoints, joins copy goroutines, and waits for every accepted handler before `Serve` returns, under ADR-0021.
- Observation JSONL schema 2 `relay_outcome` events and identity-free report v2 aggregates for post-commit Direct/Proxy directional bytes, duration, remote-byte coverage, and lifecycle cancellation, with schema-1 read compatibility and explicit non-application-success limits under ADR-0022.
- Observation JSONL schema 3 random per-connection scopes and identity-free report v3 pair-completeness counts, with schema-1/2 read compatibility, contradiction refusal, non-disruptive entropy failure, and no identifier output under ADR-0023.
- Observation JSONL schema 4 declared-original-policy baselines and identity-free report v4 same/changed selection aggregates, changed-winner relay volume, strict pair consistency, and dedicated preflight acknowledgment without counterfactual-savings claims under ADR-0024.
- Observation JSONL schema 5 bounded per-direction relay end reasons and identity-free report v5 EOF/timeout/reset/closed/I/O-error/canceled aggregates, with legacy unclassified accounting, raw-error rejection, and no application-success or learning semantics under ADR-0025.
- Read-only `trial assess` post-trial data-quality gate with explicit sample/scope/pair/cancellation thresholds, single-session enforcement, invariant checks, machine-readable descriptive-analysis readiness, and unconditional no-policy/no-baseline/no-client-outcome claims under ADR-0026.
- Pre-registered assessment plans binding the trial session, decoded-config fingerprint, window, and thresholds before activation; `trial assess` now requires the successful preflight report and report v6 verifies the expected session without exposing its identifier under ADR-0027.
- Network-free `smartroute-trial-lab` rehearsal of the real recorder → report-v6 → assessment chain, including a positive complete-session window and a negative unexpected-session gate, with temporary identity cleanup and explicit non-evidence/non-authority fields under ADR-0028.
- Paired multi-run `smartroute-benchmark-lab` comparing a Direct SOCKS baseline with the same path behind SmartRoute, reporting nearest-rank latency and signed overhead distributions, worst-run p95, exact correctness, optional latency enforcement, and strict loopback/non-authority limits under ADR-0029.
- Pinned-Mihomo forced-DIRECT benchmark tier with an explicit lab binary, generated temporary home/config, synthetic DNS, domain-form loopback target, report schema 2, CI smoke coverage, and separate non-authoritative measurements under ADR-0030.
- TLS ServerHello benchmark protocol across fake and pinned-Mihomo gateway tiers, using exact parsed no-early-data ClientHello fixtures, target-side acceptance counts, exact prefetched ServerHello replay, report schema 3, and non-enforcing CI smoke coverage under ADR-0031.
- Separate `smartroute-load-lab` for alternating concurrent chunked-echo baseline/sidecar batches, exact bidirectional byte accounting, connection and throughput distributions, opt-in provisional ratio enforcement, fake/pinned-Mihomo tiers, and explicitly retained initial 0.70 gate misses under ADR-0032.
- Fixed `smartroute-load-sweep` concurrency/payload matrix with process-scoped allocation/CPU diagnostics; fake and pinned-Mihomo long cells converge near a 0.665 ratio, so ADR-0033 retains `io.Copy`, the original 0.70 report-only gate, and the current payload-memory boundary.
- Client-paced `smartroute-capacity-lab` with baseline-attributable deadline reporting: both fake and pinned-Mihomo tiers meet 100–5000 Mbps demand and expose the current sidecar boundary at 8000 Mbps under ADR-0034, without claiming network emulation.
- Load Lab report schema 2 adds explicit measured-arm pacing state and aggregate offered load; unpaced behavior is unchanged and reports pacing as disabled.
- Manual fixed-policy management plane with a separate cleartext SQLite schema, exact TCP scope, permanent/TTL locks, transactional replacement history, read-only listing and revocation; this manual database remains outside runtime under ADR-0035.
- Opt-in `durable-auto` mode under ADR-0036: no per-target approval; first TCP/TLS-ready result immediately becomes the exact HMAC-keyed last-known-good mapping; bounded startup index, unchanged-path write suppression, one-candidate healthy path, sequential pre-commit fallback, opposite-success overwrite, identity-free counts, and evidence-retaining clear.
- Canonical `auto` mode is now the default under ADR-0037, with local persistence enabled. Its runtime skips the ephemeral promotion/TTL engine and health-freeze path; `durable-auto` remains a compatibility spelling, while Shadow/ephemeral/manual-policy facilities are diagnostic or advanced rather than the MVP product path.
- Automatic persistence now uses a dedicated bounded policy-only writer: it stores no evidence/session rows, runs no cross-session assessment, and ignores legacy promotion, TTL, health, retention, and suggestion settings.
- Added an idempotent Clash Verge transform and non-overwriting script composer that preserve the existing script, replace only the final `MATCH`, create loopback Guard/Direct/Proxy/original-policy objects, and fail closed on ambiguous group graphs or reserved-name collisions.
- Added Node-only transform tests plus a synthetic pinned-Mihomo `-t` validation command; neither reads or writes the active Clash environment.
- Added `smartroute-runtime-lab`, which executes the real Clash composer, transformed pinned-Mihomo topology, actual `smartroute supervise` children, policy-only SQLite, restart reuse, silent-path fallback, opposite overwrite, and Direct tripwire in a removed loopback-only workspace.
- Fixed automatic selected-path timeout handling so the remembered path can consume at most half the total readiness deadline, preserving a real budget for one sequential opposite fallback under ADR-0039.
- Fixed a process-only `auto` panic caused by assigning a typed-nil legacy evidence writer to the runtime interface; automatic assembly now attaches only the dedicated non-nil policy writer.
- Added ADR-0038 and a private candidate-package workflow. It resolves the current script binding read-only, retains only original/composed scripts plus checksums and rollback metadata, validates the current transformed config with temporary geodata and pinned Mihomo, and refuses overwrite or drift.
- Added checksum-gated `verify`, atomic `install`, and atomic `rollback` actions. Writes require explicit confirmation and never reload Clash; the full lifecycle passes against a synthetic app directory. The current active package was prepared and verified read-only only.
- Test Lab report v2 adds a four-connection last-known-good contract: first Direct readiness is remembered, the next hit opens no Proxy, Direct failure falls back and overwrites to Proxy, and the final hit opens no Direct. Preflight now validates every scenario field instead of accepting names plus top-level pass flags.
- Hard resource controls for automatic learning: `max_entries` bounds policy rows/index memory, `max_evidence_rows` defaults to 100,000, runtime evidence may exceed by at most 255 queued writes, and the hot path performs no SQLite I/O. Added allocation gate and repeatable lookup benchmark.
- Fresh macOS arm64/v1.19.29 isolated Mihomo evidence for Guard adaptive selection, original-policy fallback while the engine is stopped, and return to adaptive after engine rebind, with all isolation assertions true.

### Known limitations

- TLS readiness is structural and experimental; automatic mappings remain exact and have no speculative TTL. Automatic network-change/captive-portal detection, a desktop UI, UDP/QUIC adaptive learning, one-click Clash integration, and broad cross-platform real-site compatibility are not implemented yet.
- The macOS LaunchAgent and active Clash integration are operator-oriented preview surfaces rather than a signed application installer.
