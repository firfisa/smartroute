# Changelog

All notable project changes are recorded here. The project has not published a release.

## Unreleased

### Added

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

### Known limitations

- TLS readiness is structural and experimental; certificate/Finished validation, learned-policy persistence, OS-level supervisor service integration, active configuration integration, and broad real-site compatibility are not implemented yet.
- No public release artifacts have been published yet.
