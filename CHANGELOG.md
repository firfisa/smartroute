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
- `candidate_below_commit_stage` rejection so experimental routing fails safely until TLS readiness exists.
- Manual GitHub Actions workflow for the isolated pinned-Mihomo contract lab.

### Known limitations

- TCP-level SOCKS relay is experimental; TLS readiness, learning persistence, and the Mihomo adapter are not implemented yet.
- No public release artifacts have been published yet.
