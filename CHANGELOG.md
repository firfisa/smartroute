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

### Known limitations

- No SOCKS server, real network dialing, learning persistence, TLS parser, or Mihomo adapter is implemented yet.
- The repository license and GitHub visibility are pending owner confirmation.
- GitHub authentication for the local `gh` CLI is currently invalid and must be refreshed before the first push.
