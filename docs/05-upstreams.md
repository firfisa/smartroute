# Upstream Inventory

Last verified: 2026-08-04.

SmartRoute keeps upstream sources outside the tracked repository in `.upstream/`. The exact references are recorded in `upstreams.lock` and prepared by `scripts/prepare-upstreams.sh`.

## 1. Locked upstreams

| Project | Repository | Locked release | Commit | License | Role | Integration status |
| --- | --- | --- | --- | --- | --- | --- |
| Mihomo | `MetaCubeX/mihomo` | `v1.19.29` | `e26714a181ac0e2fa803453c0a8e9a9ce94e31cb` | GPL-3.0 | TUN, DNS, static routing, Direct and proxy outbounds | macOS arm64 and Linux amd64 isolated TLS topology verified; ADR-0004/0005 apply |
| Clash Verge Rev | `clash-verge-rev/clash-verge-rev` | `v2.5.2` | `28f2efc504059b1dc75c793618b775c8e1b2a5f1` | GPL-3.0 | Desktop profile/script lifecycle reference | No fork; first-party post-script transform passes synthetic tests and pinned-Mihomo syntax validation |

SmartRoute's first-party standalone source is released under MIT. The upstream projects in this table remain under GPL-3.0 and are not relicensed by SmartRoute. The current sidecar-first boundary keeps their source outside this repository; any future copying, linking, fork distribution, or bundled release requires a fresh license-boundary review and the notices/source obligations applicable to that distribution model.

## 2. Runtime dependency inventory

| Module | Locked version | License | Purpose | Compatibility/maintenance status |
| --- | --- | --- | --- | --- |
| `modernc.org/sqlite` | `v1.55.0` | BSD-3-Clause; embedded SQLite is public domain | CGo-free `database/sql` driver for local strong-evidence persistence | Supports darwin/arm64, linux/amd64 and Windows; experimental store tests pass |
| `modernc.org/libc` | `v1.74.1` (indirect) | BSD-3-Clause | Exact libc dependency selected by the SQLite module | Keep aligned through `go.mod/go.sum`; never override independently without SQLite tests |

The SQLite driver is a linked runtime dependency, not source copied into this repository. Its transitive modules are pinned by `go.sum`. Upgrade steps require checking the canonical release notes, license files, supported targets, SQLite/libc pairing, migration/recovery tests, and clean-checkout build size. ADR-0012 records the selection and privacy boundary.

Primary references: [modernc SQLite package](https://pkg.go.dev/modernc.org/sqlite), [canonical repository](https://gitlab.com/cznic/sqlite).

## 2.1 Development tool inventory

| Tool | Version policy | Purpose | Runtime impact |
| --- | --- | --- | --- |
| Node.js | 24 in CI | Execute the dependency-free Clash transform/composer tests and isolated full Runtime Lab | Development only; SmartRoute daemon remains Go |
| `actions/setup-node` | `v6` | Provision Node for the transform test job | CI only |
| macOS system Ruby | 2.6-compatible standard library surface | Private active-candidate packaging and checksum-gated file replacement | Local integration tooling only; no gem dependency |

The transform and composer use only Node built-ins and add no npm dependency or shipped JavaScript package tree.

## 3. Mihomo source-contract evidence

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
| `fallback` does not retry the current connection | `.upstream/mihomo/adapter/outboundgroup/fallback.go` | `DialContext` selects one member with `findAliveProxy`, returns that member's dial error, and only calls `onDialFailed`; it does not dial the next member for the same connection |

Isolated runtime results from `make mihomo-lab` on macOS arm64 and the Ubuntu amd64 GitHub runner:

| Contract | Result |
| --- | --- |
| Exact binary | v1.19.29 at locked commit, version injected by the reproducible build script |
| Forced Direct listener | Local loopback payload passed |
| Forced Proxy listener | Local fake SOCKS proxy received the domain-form `echo.test` target and relayed bytes |
| Loop prevention | Only the front adaptive connection entered SmartRoute; forced listeners did not recurse |
| Readiness semantics | Forced Direct still proves only L1 when no ServerHello returns |
| TLS adaptive recovery | Front path copied a parsed no-early-data ClientHello; Proxy returned ServerHello and was committed at `StageTLS` |
| Full transformed process runtime | Actual composer output, pinned Mihomo, `smartroute supervise` children and policy-only SQLite passed Direct persistence, two restart reloads, silent-Direct timeout fallback and Proxy overwrite |
| Isolation | Temporary home/config, synthetic local DNS, random loopback ports, no TUN/system proxy/external network/active Clash reads or writes |
| Paired forced-DIRECT benchmark | macOS arm64 5×200 pairs: aggregate paired p95 200µs, worst-run p95 231µs, 2000/2000 exact echoes, 1100/1100 sidecar Direct selections, zero Proxy attempts |
| Paired TLS ServerHello benchmark | macOS arm64 5×200 pairs: aggregate paired p95 230µs, worst-run p95 254µs, 2000/2000 exact ServerHello responses, 2200/2200 target ClientHellos including warmups, zero Proxy attempts |
| Concurrent relay load | macOS arm64 3×16×1MiB: 48/48 measured connections and exact bytes per arm, median sidecar 939.24 MiB/s, worst sidecar/baseline ratio 0.677, provisional 0.70 gate missed, zero Proxy attempts |

Remaining runtime checks:

- Confirm Fake-IP/TUN combinations preserve `metadata.Host` before the adapter boundary.
- Confirm both forced listeners work through config reloads on macOS; startup behavior is verified.
- Confirm the proxy listener cannot resolve to `DIRECT` through a user selector when collecting a Proxy counterfactual.
- The pinned Mihomo fault lab passed engine stop, original-policy fallback, rebind, and adaptive recovery on macOS arm64/v1.19.29 on 2026-08-02. Actual OS host-service integration and any outer Mihomo health fallback remain pending; neither can retry the first connection that observed Guard failure.

## 4. Preparation

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

## 5. Update procedure

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
