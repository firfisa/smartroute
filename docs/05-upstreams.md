# Upstream Inventory

Last verified: 2026-08-02 via `git ls-remote --tags --refs`.

SmartRoute keeps upstream sources outside the tracked repository in `.upstream/`. The exact references are recorded in `upstreams.lock` and prepared by `scripts/prepare-upstreams.sh`.

## 1. Locked upstreams

| Project | Repository | Locked release | Commit | License | Role | Integration status |
| --- | --- | --- | --- | --- | --- | --- |
| Mihomo | `MetaCubeX/mihomo` | `v1.19.29` | `e26714a181ac0e2fa803453c0a8e9a9ce94e31cb` | GPL-3.0 | TUN, DNS, static routing, Direct and proxy outbounds | macOS arm64 and Linux amd64 isolated TLS topology verified; ADR-0004/0005 apply |
| Clash Verge Rev | `clash-verge-rev/clash-verge-rev` | `v2.5.2` | `28f2efc504059b1dc75c793618b775c8e1b2a5f1` | GPL-3.0 | Possible future desktop UI and lifecycle integration | Reference only; no fork |

SmartRoute's first-party standalone source is released under MIT. The upstream projects in this table remain under GPL-3.0 and are not relicensed by SmartRoute. The current sidecar-first boundary keeps their source outside this repository; any future copying, linking, fork distribution, or bundled release requires a fresh license-boundary review and the notices/source obligations applicable to that distribution model.

## 2. Mihomo source-contract evidence

The following evidence was inspected at the locked v1.19.29 commit and compared with the isolated runtime lab.

| Required assumption | Verified source path | Finding |
| --- | --- | --- |
| A listener can force a named proxy | `.upstream/mihomo/listener/inbound/base.go` | `BaseOption.SpecialProxy` decodes the `proxy` field and adds it through `WithSpecialProxy` |
| A mixed listener inherits the forced proxy | `.upstream/mihomo/listener/inbound/mixed.go` | `MixedOption` embeds `BaseOption` and passes `m.Additions()` to HTTP/SOCKS listeners |
| Forced proxy bypasses normal rule matching | `.upstream/mihomo/tunnel/tunnel.go` | `resolveMetadata` resolves `metadata.SpecialProxy` directly before regular rule matching |
| SOCKS inbound preserves a domain target | `.upstream/mihomo/adapter/inbound/util.go` | SOCKS domain address populates `metadata.Host` and destination port |
| SOCKS outbound forwards a domain target | `.upstream/mihomo/adapter/outbound/util.go` | `serializesSocksAddr` emits SOCKS domain form when `metadata.Host` is present |
| SOCKS outbound performs CONNECT to the sidecar | `.upstream/mihomo/adapter/outbound/socks5.go` | `DialContext` connects to the local SOCKS server and performs a CONNECT handshake with serialized target metadata |
| Inbound SOCKS success precedes target dial | `.upstream/mihomo/transport/socks5/socks5.go` and tunnel handoff | `ServerHandshake` writes success before `tunnel.HandleTCPConn` resolves/dials; classify the ACK as L1 `StageOutbound` |

Isolated runtime results from `make mihomo-lab` on macOS arm64 and the Ubuntu amd64 GitHub runner:

| Contract | Result |
| --- | --- |
| Exact binary | v1.19.29 at locked commit, version injected by the reproducible build script |
| Forced Direct listener | Local loopback payload passed |
| Forced Proxy listener | Local fake SOCKS proxy received the domain-form `echo.test` target and relayed bytes |
| Loop prevention | Only the front adaptive connection entered SmartRoute; forced listeners did not recurse |
| Readiness semantics | Forced Direct still proves only L1 when no ServerHello returns |
| TLS adaptive recovery | Front path copied a parsed no-early-data ClientHello; Proxy returned ServerHello and was committed at `StageTLS` |
| Isolation | Temporary home/config, synthetic local DNS, random loopback ports, no TUN/system proxy/external network/active Clash reads or writes |

Remaining runtime checks:

- Confirm Fake-IP/TUN combinations preserve `metadata.Host` before the adapter boundary.
- Confirm both forced listeners work through config reloads on macOS; startup behavior is verified.
- Confirm the proxy listener cannot resolve to `DIRECT` through a user selector when collecting a Proxy counterfactual.
- Implement and fault-test an automatic fallback to the user's original `MATCH` policy when the sidecar is unavailable.

## 3. Preparation

```bash
bash scripts/prepare-upstreams.sh
bash scripts/prepare-upstreams.sh mihomo
```

The script:

1. Reads `upstreams.lock`; optional positional names select a subset.
2. Clones with blob filtering into ignored `.upstream/<name>` directories.
3. Fetches and checks out the exact commit in detached mode.
4. Verifies `HEAD` matches the lock.

Override the local destination without using a broad system directory:

```bash
SMARTRoute_UPSTREAM_ROOT=/private/tmp/smartroute-upstreams \
  bash scripts/prepare-upstreams.sh
```

## 4. Update procedure

| Step | Required evidence |
| --- | --- |
| Read release notes and security changes | Link in PR description |
| Resolve the exact tag and commit | `git ls-remote` output |
| Update `upstreams.lock` | Tag and 40-character commit |
| Update this table | Version, commit, compatibility status |
| Run integration topology tests | System proxy and TUN results per OS |
| Review license/notice changes | PR checklist |
| Record behavior changes | `CHANGELOG.md` and ADR if architecture changes |

Never edit `.upstream/` and then rely on the untracked change. First-party patches belong here; a maintained upstream modification requires an explicitly approved fork and separate remote.
