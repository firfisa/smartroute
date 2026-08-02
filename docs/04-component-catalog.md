# SmartRoute Component and Interface Catalog

Version: v0.6
Last updated: 2026-08-02

This file is the maintained registry for components, interfaces, commands, configuration fields, and decision reason codes. Status is explicit: `implemented`, `experimental`, or `planned`.

## 1. Component map

```mermaid
flowchart TB
    CLI["cmd/smartroute\nCLI and future daemon"]
    Config["internal/config\nSchema and validation"]
    Model["internal/model\nDomain types"]
    Decision["internal/decision\nPaired observation evaluator"]
    Learning["internal/learning\nEphemeral preference state"]
    SOCKS["internal/socks5\nSOCKS5 wire protocol"]
    Transport["internal/transport\nSOCKS dialers and racer"]
    TLSInspect["internal/tlsinspect\nbounded TLS first-flight parser"]
    Sidecar["internal/sidecar\nInbound relay"]
    Guard["internal/guard\nPre-payload availability"]
    NetRelay["internal/netrelay\nBidirectional relay"]
    Privacy["internal/privacy\nDirect-probe policy"]
    Supervisor["internal/supervisor\nChild lifecycle"]
    Observe["internal/observe\nBounded local records"]
    TestLab["internal/testlab\nIsolated fault lab"]
    LabCLI["cmd/smartroute-testlab\nJSON lab runner"]
    MihomoLab["internal/mihomolab\nPinned child-process lab"]
    MihomoLabCLI["cmd/smartroute-mihomo-lab\nIntegration runner"]
    Mihomo["internal/upstream\nFuture active adapter"]
    Store["internal/store\nSQLite strong evidence"]

    CLI --> Config
    CLI --> Decision
    CLI --> Learning
    CLI --> Sidecar
    CLI --> Guard
    CLI --> Privacy
    CLI --> Supervisor
    CLI --> Observe
    CLI --> Store
    Config --> Privacy
    Config --> Learning
    Decision --> Model
    Learning --> Model
    Store --> Learning
    Store --> Model
    Transport --> Model
    Transport --> SOCKS
    Transport --> TLSInspect
    Sidecar --> TLSInspect
    Sidecar --> SOCKS
    Sidecar --> Transport
    Sidecar --> NetRelay
    Sidecar --> Privacy
    Sidecar --> Learning
    Guard --> SOCKS
    Guard --> NetRelay
    TestLab --> Sidecar
    TestLab --> SOCKS
    LabCLI --> TestLab
    MihomoLabCLI --> MihomoLab
    MihomoLab --> Sidecar
    MihomoLab --> Guard
    MihomoLab --> TestLab
    Transport -. "planned" .-> Mihomo
    Store -. "future durable policy" .-> Learning
```

Solid edges are implemented imports. Dashed edges are planned Phase 0–2 relationships.

## 2. Component registry

| Component | Owner | Status | Responsibility | Explicit non-responsibility | Primary tests |
| --- | --- | --- | --- | --- | --- |
| `cmd/smartroute` | CLI | experimental | Command parsing, config validation, synthetic decision trace, Guard/engine lifecycles, observation controls, durable shadow-writer lifecycle, and evidence status/backup/verify/restore | Durable policy application, destructive evidence cleanup, and active Clash configuration | `cmd/smartroute/main_test.go`; data plane exercised by Test Labs |
| `cmd/smartroute-testlab` | Test | implemented | Run deterministic loopback scenarios and print JSON report | Real destinations or Clash integration | `internal/testlab/lab_test.go` plus CI named step |
| `cmd/smartroute-mihomo-lab` | Test | implemented | Run the exact pinned Mihomo binary in an isolated topology and print JSON | Discovering or operating the active Clash instance | `internal/mihomolab/lab_test.go`; `make mihomo-lab` |
| `internal/config` | Core | implemented | Strict JSON schema, safe loopback defaults, validation | Mihomo YAML generation | `internal/config/config_test.go` |
| `internal/model` | Core | implemented | Path, target, readiness, observation, decision types and readable JSON evidence | State persistence | `internal/model/model_test.go` plus consumer tests |
| `internal/decision` | Core | experimental | Evaluate one paired Direct/Proxy observation | Multi-session promotion and decay | `internal/decision/evaluator_test.go` |
| `internal/learning` | Policy | experimental | Consume strong completed pairs; maintain scoped consecutive counters, TTL, unstable state, and optional ephemeral preference | Cross-session evidence, durable policy, user locks, or health-control persistence | `internal/learning/engine_test.go`; Sidecar learning-loop tests |
| `internal/socks5` | Data plane | experimental | No-auth SOCKS5 CONNECT parsing/client handshake and domain preservation | Authentication, UDP ASSOCIATE, BIND | `internal/socks5/protocol_test.go`; Test Lab |
| `internal/transport` | Data plane | experimental | SOCKS5 candidate dialing, TCP and TLS racing, ServerHello gate, cancellation, exact prefetched-byte replay | Certificate/Finished/application validation | Racer and TLS readiness tests; Mihomo Lab |
| `internal/tlsinspect` | Data plane | experimental | Bounded TLS record reassembly, ClientHello/ServerHello structure checks, early-data rejection | TLS decryption, certificate validation, payload logging | `internal/tlsinspect/tlsinspect_test.go` |
| `internal/sidecar` | Data plane | experimental | SOCKS admission, TLS first-flight orchestration, stage enforcement, relay, decision and diagnostic events | SQLite I/O, durable policy evaluation, or active Clash modification | Sidecar net.Pipe tests, Test Lab and Mihomo Lab |
| `internal/guard` | Data plane | experimental | Before forwarding payload to either lane, select the adaptive engine or fall back to the original-policy SOCKS listener | Direct/Proxy evidence, learning, post-commit replay, Guard-process supervision | `internal/guard/server_test.go`; Mihomo Lab fault scenarios |
| `internal/netrelay` | Data plane | experimental | Shared bidirectional TCP copying and half-close handling | Routing decisions or payload inspection | Consumer tests in Guard, Sidecar and Test Lab |
| `internal/privacy` | Policy | experimental | Validate/normalize exact and suffix deny rules; decide whether a target may open Direct | Network I/O, hostname persistence, or route learning | `internal/privacy/policy_test.go`; Sidecar privacy tests |
| `internal/supervisor` | Runtime | experimental | Independently monitor Guard/engine child processes, cap restart backoff, and emit lifecycle events | Supervising Mihomo, replaying failed connections, or host-level service installation | `internal/supervisor/supervisor_test.go`; CLI service-spec tests |
| `internal/observe` | Persistence | experimental | HMAC-pseudonymize typed events, write bounded per-source JSONL, and implement pause/resume/clear/export | Learned policy, payload capture, process identity, cloud upload, or active Clash access | `internal/observe/recorder_test.go`; CLI sink/control tests |
| `internal/testlab` | Test | implemented | Ephemeral loopback echo target, fake Direct/Proxy gateways, deterministic faults | External network and active Clash access | `internal/testlab/lab_test.go` |
| `internal/mihomolab` | Test | implemented | Temporary config/home, child lifecycle, synthetic DNS, forced-listener and readiness assertions | Active Clash discovery, external traffic, TUN, system proxy | `internal/mihomolab/lab_test.go`; explicit runtime command |
| `internal/upstream` | Integration | planned | Mihomo config/API adapter and topology validation | Shipping Mihomo source | Planned integration tests |
| `internal/store` | Persistence | experimental | HMAC target keys, sessions, evidence/status, migrations, integrity/retention, bounded async writing, and verified snapshot lifecycle | Runtime policy application, overwrite restore, cleartext targets, raw analytics, or JSONL recording | SQLite, writer and lifecycle test suites |

## 3. Implemented function and interface registry

| Symbol | Owner | Inputs | Output | Error behavior | Stability | Tests |
| --- | --- | --- | --- | --- | --- | --- |
| `config.Default()` | `internal/config` | None | Safe Phase 0 `Config` | None | experimental | `TestDefaultIsValid` |
| `config.Load(path)` | `internal/config` | JSON file path | Validated `Config` | Wraps read/decode errors; rejects unknown fields | experimental | `TestLoadRejectsUnknownFields` |
| `Config.Validate()` | `internal/config` | Config value | `error` | Joins all detectable validation errors | experimental | Config test table |
| `Observation.Validate()` | `internal/model` | Observation value | `error` | Rejects invalid path/stage, negative latency, weak success, unclassified failure | experimental | Decision tests |
| `Observation.MarshalJSON/UnmarshalJSON()` | `internal/model` | Observation value or readable JSON | Symmetric stage names and millisecond latency | Decode rejects unknown stage and any invalid observation | experimental | Readable-units and round-trip tests |
| `model.ParseStage(value)` | `internal/model` | Stage name | `Stage` | Rejects unknown stage | experimental | CLI parser tests |
| `PairEvaluator.Evaluate(direct, proxy)` | `internal/decision` | Ordered paired observations | Explainable `Decision` | Rejects invalid observations/config | experimental | Outcome-matrix table |
| `socks5.ReadRequest(rw)` | `internal/socks5` | SOCKS byte stream | Domain/IP target and port | Rejects auth, unsupported commands/address types, malformed input | experimental | Protocol and Test Lab tests |
| `socks5.DialContext(ctx, endpoint, target)` | `internal/socks5` | Context, SOCKS endpoint, target | Connected tunnel | Closes on cancellation; returns handshake/reply errors | experimental | Test Lab |
| `CandidateDialer.Dial(ctx, target)` | `internal/transport` | Context and target | Connection and observation | Implementer classifies cancellation/path error | experimental contract | Racer tests |
| `SOCKS5Dialer.Dial(ctx, target)` | `internal/transport` | TCP target, fixed endpoint, declared `ReadinessStage` | SOCKS tunnel plus observation at the declared stage; default L1 | Classifies timeout, cancellation, SOCKS failure; rejects invalid stages | experimental | Test Lab and Mihomo Lab |
| `Racer.Race(ctx, target)` | `internal/transport` | Two dialers, preferred path, head-start, timeout, target | Winner plus optional already-completed `OtherObservation` | Defaults Direct-first; preferred failure starts opposite immediately; never waits for loser | experimental | Both preference orders, fallback, prior-failure, cancellation and dual-failure tests |
| `tlsinspect.ReadClientHello(reader, max)` | `internal/tlsinspect` | TLS byte stream and byte limit | Opaque validated ClientHello with exact record bytes | Rejects malformed, oversized, trailing first-flight data and `early_data` | experimental | Fragmentation/rejection table |
| `tlsinspect.ReadServerHello(reader, max)` | `internal/tlsinspect` | TLS byte stream and byte limit | Structurally valid ServerHello plus exact consumed records | Classifies alerts, truncation, malformed and unexpected messages | experimental | Fragmentation/alert tests |
| `ReadinessGate.Await(ctx, conn, target)` | `internal/transport` | Context, candidate connection, target | Stage, failure class and prefetched bytes | Must not lose or unsafely replay consumed bytes | experimental contract | TLS gate tests |
| `TLSRacer.Race(ctx, target, hello)` | `internal/transport` | Two candidates, validated no-early-data ClientHello, gate and timing | L3 winner connection with replay prefix | Cancels loser; returns paired classified failures | experimental | `internal/transport/tls_readiness_test.go` |
| `TLSRacer.RacePreferred(ctx, target, hello, path)` | `internal/transport` | Same TLS contract plus Direct/Proxy first preference | L3 winner with preferred launch order | Invalid preference fails before dialing; opposite path remains available | experimental | Proxy-first and fallback tests |
| `TLSRacer.ConnectPath(ctx, target, hello, path)` | `internal/transport` | One policy-selected candidate and validated no-early-data ClientHello | L3 connection with replay prefix | Rejects invalid/missing path; returns `TLSPathError` with classified observation | experimental | Proxy-only success/replay and failure tests |
| `privacy.New(mode, patterns)` | `internal/privacy` | Mode and exact/suffix deny patterns | Immutable compiled local policy | Rejects unknown mode, whitespace, URL-like/invalid host syntax and suffix IP rules | experimental | Policy validation table |
| `Policy.Evaluate(target)` | `internal/privacy` | Target hostname/IP | Allow/deny plus stable reason code | Missing policy and invalid target fail closed to Proxy-only | experimental | Exact/suffix/boundary/privacy-first tests |
| `learning.New(config)` | `internal/learning` | Mode, Direct/Proxy thresholds, TTL, optional clock | Concurrent process-local engine | Rejects unknown mode, low thresholds, and non-positive TTL | experimental | Config validation and engine tests |
| `Engine.Observe(target, winner, other)` | `internal/learning` | Scoped target, ready winner, optional completed opposite observation | Explainable update and ephemeral policy | Rejects invalid pairs; incomplete/canceled/pre-outbound evidence returns non-applied reason | experimental | Promotion, contradiction and weak-evidence tests |
| `Engine.PreferredPath(target)` | `internal/learning` | Scoped target | Live Direct/Proxy preference or empty | Returns empty in shadow/unknown/unstable/expired/invalid cases | experimental | Shadow, scope and TTL tests |
| `Supervisor.Run(ctx)` | `internal/supervisor` | Context, service specs, starter, restart policy | Runs independent monitors until cancellation | Rejects invalid/duplicate services; runtime failures trigger capped restart rather than stopping siblings | experimental | Restart, start-error, independence, cancellation tests |
| `CommandStarter.Start(ctx, service)` | `internal/supervisor` | Executable/args and synchronized stdout/stderr | Started child implementing `Wait()` | Parent cancellation interrupts child; `WaitDelay` kills after grace period | experimental | Supervisor consumers; platform process integration pending |
| `observe.New(options)` | `internal/observe` | Local directory, source and capacity/privacy limits | Source-specific recorder | Rejects unsafe paths/limits and invalid salt; initialization errors are explicit | experimental | Recorder validation/privacy tests |
| `Recorder.Record(event)` | `internal/observe` | Typed bounded event with optional raw target | Pseudonymous JSONL record or paused no-op | Oversized/write errors return to caller; runtime caller warns once and continues routing | experimental | Hashing, pause, rotation and oversized-event tests |
| `observe.Inspect/Pause/Resume/Clear/Export` | `internal/observe` | Observation directory and explicit destination/paused state | Lifecycle status or local file operation | Only manages engine/Guard/supervisor subdirectories; clear requires pause; export refuses nesting/existing destination and omits salt/symlinks | experimental | Control and export tests |
| `store.Open(ctx, config)` | `internal/store` | Context, DB path, busy timeout | Migrated/integrity-checked SQLite store with local HMAC key | Rejects unsafe path, missing/invalid key, `store.ErrCorrupt`, and future schema; never replaces data | experimental | Open/reopen, corruption, permissions and future-schema tests |
| `store.OpenReadOnly(ctx, config)` | `internal/store` | Existing DB path and timeout | Integrity-checked exact-current-schema store | Never creates key/DB or migrates; rejects missing key, corruption and any schema mismatch | experimental | Missing/current/old-schema tests; CLI status tests |
| `Store.StartSession(ctx, id, time)` | `internal/store` | Safe local session ID and timestamp | Durable independent-session row | Rejects invalid/duplicate sessions and cancellation | experimental | Session and foreign-key tests |
| `Store.AppendStrongEvidence(...)` | `internal/store` | Target, known session, winner/opposite pair, timestamp | Whether a schema-v1 row was written | Shared learning gate skips weak/incomplete pairs; unsafe failure tokens and DB errors are explicit | experimental | Strong/weak/privacy/concurrent-write tests |
| `Store.ListEvidence/Summarize` | `internal/store` | Target and lower timestamp bound | Ordered rows or wins/distinct-session counts | Invalid stored stages/directions fail visibly | experimental | Scope, summary and corrupt-row tests |
| `Store.PruneEvidence/Checkpoint` | `internal/store` | Retention cutoff or context | Deleted evidence count or compact WAL boundary | Transactionally removes empty sessions; errors never imply automatic replacement | experimental | Evidence/session pruning and privacy-file tests |
| `store.NewAsyncWriter(...)` | `internal/store` | Store, session, queue capacity, optional error callback | Background strong-evidence writer | Rejects unsafe construction; individual append errors are counted and later writes continue | experimental | Queue bounds, errors, skips and close tests |
| `AsyncWriter.Enqueue(request)` | `internal/store` | Target, completed pair and timestamp | Immediate accepted flag plus safe durable reason | Never blocks; full/closed queues drop only durable evidence | experimental | Backpressure and close/enqueue race tests |
| `AsyncWriter.Close(ctx)` | `internal/store` | Shutdown context | Drained completion or context error | Stops admission, drains accepted work, never force-closes its store | experimental | Drain and timeout tests |
| `runtimeLearningEngine.Observe(...)` | `cmd/smartroute` | Sidecar target and completed candidate evidence | Ephemeral update plus optional durable enqueue reason | Weak pairs do not enqueue; writer result never becomes a route error | experimental | Runtime learner and Sidecar metadata tests |
| `Store.Status(ctx)` | `internal/store` | Current-schema store | Schema and aggregate session/evidence/time counts | Contains no target/session identity or failure details; query errors are explicit | experimental | Aggregate/privacy and CLI tests |
| `Store.Backup(ctx, destination)` | `internal/store` | Open source store and new directory | Online SQLite snapshot, key, verified manifest | Refuses existing/unsafe destination; failures retain `INCOMPLETE` | experimental | Snapshot consistency, modes, reopen, marker and overwrite tests |
| `store.VerifyBackup(ctx, source)` | `internal/store` | Completed snapshot directory | Validated manifest and aggregate status | Rejects incomplete/tampered/unknown/non-regular/corrupt artifacts; verifies a private copy | experimental | Source-immutability and tamper tests |
| `store.RestoreBackup(ctx, source, destination)` | `internal/store` | Verified snapshot and new DB path | Restored DB/key plus aggregate status | Never overwrites or activates; failures retain `.INCOMPLETE` | experimental | New-path restore and existing-path refusal tests |
| `sidecar.Server.Serve(ctx, listener)` | `internal/sidecar` | Context, listener, plain Racer or TLSRacer | Serves until cancellation/error; TLS mode requires L3 | Rejects unsafe ClientHello before dialing; never reads Clash config | experimental | Sidecar, Test Lab and Mihomo Lab |
| `guard.Server.Serve(ctx, listener)` | `internal/guard` | Context, listener, adaptive/original dialers and bounded timeouts | Serves SOCKS targets; commits one availability lane before payload | Falls back on adaptive handshake failure; refuses when both lanes fail; never replays post-commit data | experimental | Adaptive, unavailable, wedged and dual-failure unit tests; Mihomo Lab scenarios |
| `netrelay.Bidirectional(left, right)` | `internal/netrelay` | Two owned TCP-like connections | Relays both directions until completion | Best-effort half-close; relay errors are not routing evidence | experimental | Guard/Sidecar end-to-end tests |
| `testlab.RunAll(ctx)` | `internal/testlab` | Context | Isolation and scenario JSON model | Fails if any scenario invariant fails | implemented | `internal/testlab/lab_test.go` |
| `mihomolab.Run(ctx, binaryPath)` | `internal/mihomolab` | Context and explicit pinned binary path | Isolation, topology, readiness and scenario report | Rejects wrong version/config; owns and stops only its child | implemented | `internal/mihomolab/lab_test.go`; `make mihomo-lab` |

## 4. CLI contract

| Command | Status | Purpose | Network effects | Persistence |
| --- | --- | --- | --- | --- |
| `smartroute version` | implemented | Print version, commit, build date | None | None |
| `smartroute validate -config PATH` | implemented | Strictly parse and validate local JSON config | None | None |
| `smartroute trace -direct SPEC -proxy SPEC` | implemented | Evaluate one synthetic paired observation and print JSON | None | None |
| `smartroute serve [-acknowledge-direct-probes]` | experimental | Run TLS-over-SOCKS sidecar with runtime privacy policy | Privacy-first/deny targets open Proxy only; acknowledged explicit-opt-in targets may race Direct/Proxy | Optional bounded engine JSONL and separately opt-in SQLite strong evidence; SQLite never supplies routes |
| `smartroute guard` | experimental | Run the separate availability boundary in front of the adaptive engine | Configured loopback Guard, engine and original-policy SOCKS endpoints | Optional bounded Guard JSONL; otherwise debug stdout |
| `smartroute supervise` | experimental | Run Guard and adaptive engine as independently restartable children | Same loopback effects as the two child commands; does not operate Mihomo | Optional bounded per-source JSONL; otherwise debug stdout |
| `smartroute observations status\|pause\|resume\|clear\|export` | experimental | Operate the configured local recorder directory | None | Status/control, confirmed deletion, or redacted file export |
| `smartroute learning status` | experimental | Inspect configured durable evidence health and aggregates | None | Missing state is not created; existing current schema is opened read-only |
| `smartroute learning backup` | experimental | Snapshot configured evidence into a new private directory | None beyond local SQLite locks | Includes DB/key/manifest; never a redacted export |
| `smartroute learning verify-backup` | experimental | Validate checksums and SQLite contents on a temporary copy | None | Leaves source snapshot unchanged |
| `smartroute learning restore` | experimental | Restore a snapshot to a new database path | None | Refuses every existing DB/key/marker; never updates config or activates policy |
| `smartroute policy` | planned | Inspect, lock, revoke, or export policy | None by default | Policy store |
| `smartroute-testlab` | implemented | Run isolated deterministic data-plane scenarios | Ephemeral loopback sockets only | None |
| `smartroute-mihomo-lab -mihomo PATH` | implemented | Run isolated pinned-Mihomo contract scenarios | Child process, temporary home, local synthetic DNS and ephemeral loopback sockets only | Temporary files removed; JSON report only |

Trace observation syntax:

```text
success:<stage>:<latency_ms>
failure:<stage>:<latency_ms>:<failure_class>
```

Example:

```bash
go run ./cmd/smartroute trace \
  -direct failure:tcp:250:tls_reset \
  -proxy success:tls:120
```

## 5. Configuration reference

| JSON field | Type | Default | Validation | Behavior |
| --- | --- | --- | --- | --- |
| `version` | integer | `1` | Must equal current schema version | Enables explicit migrations |
| `listen_address` | host:port | `127.0.0.1:17890` | Literal loopback, non-zero, unique | Experimental `serve` SOCKS listener |
| `direct_endpoint` | host:port | `127.0.0.1:17891` | Literal loopback, non-zero, unique | Experimental Direct SOCKS candidate endpoint |
| `proxy_endpoint` | host:port | `127.0.0.1:17892` | Literal loopback, non-zero, unique | Experimental Proxy SOCKS candidate endpoint |
| `guard_listen_address` | host:port | `127.0.0.1:17893` | Literal loopback, non-zero, unique | Experimental `guard` SOCKS listener |
| `original_endpoint` | host:port | `127.0.0.1:17894` | Literal loopback, non-zero, unique | Mihomo listener forced to the user's original catch-all policy |
| `guard_adaptive_timeout_ms` | integer | `250` | 10–2000 | Maximum local adaptive-engine SOCKS handshake time before same-connection fallback |
| `original_fallback` | enum | `proxy` | `direct` or `proxy` | Used by synthetic paired evaluator; Guard runtime uses `original_endpoint` instead |
| `decision.direct_head_start_ms` | integer | `200` | 10–2000 | Delay before starting Proxy for an unknown target |
| `decision.max_direct_penalty_ms` | integer | `150` | 0–5000 | Direct may be this much slower and still win when both succeed |
| `decision.candidate_timeout_ms` | integer | `5000` | Must exceed head start | Overall candidate deadline |
| `learning.mode` | enum | `shadow` | `shadow` or `ephemeral-auto`; missing legacy value becomes shadow | Shadow computes only; auto applies live process-local preference to launch order |
| `learning.max_entries` | integer | `10000` | 1–1000000; missing legacy value becomes default | Bounds process memory; full table rejects new learning after expired-entry pruning |
| `learning.proxy_promotion_wins` | integer | `3` | At least 2 | Consecutive strong Proxy pairs for ephemeral promotion |
| `learning.direct_promotion_wins` | integer | `5` | At least 2 | Consecutive strong Direct pairs for ephemeral promotion |
| `learning.policy_ttl_hours` | integer | `72` | Positive | Ephemeral preference expiry; restart clears earlier |
| `learning.persistence.enabled` | boolean | `false` | Boolean | Explicitly opens the SQLite strong-evidence shadow writer; false creates no DB/key |
| `learning.persistence.database_path` | path | `data/learning.db` | Non-empty file path; not `.` or filesystem root | SQLite file; sibling `.key` and any WAL/SHM files share its lifecycle |
| `learning.persistence.queue_size` | integer | `256` | 1–65536 | Bounds pending non-blocking durable writes; full queues drop evidence only |
| `learning.persistence.retention_hours` | integer | `720` | 1–87600 | Evidence older than this is pruned at enabled runtime startup |
| `learning.persistence.shutdown_timeout_ms` | integer | `2000` | 100–30000 | Bounds writer drain plus WAL checkpoint during shutdown |
| `privacy.mode` | enum | `explicit-opt-in` | `explicit-opt-in` or `privacy-first` | `privacy-first` opens zero Direct candidates; explicit mode also requires CLI acknowledgment |
| `privacy.never_direct_probe` | string list | empty | Plain exact; leading `.` or `*.` suffix; normalized ASCII hostname/IP; invalid entries reject config | Matching entries override acknowledgment and use Proxy-only L3 |
| `observation.enabled` | boolean | `false` | Boolean | Enables local persistence and suppresses duplicate raw runtime stdout events |
| `observation.directory` | path | `data/observations` | Non-empty; not `.` or filesystem root | Git-ignored local salt, pause marker and source subdirectories |
| `observation.max_file_bytes` | integer | `8388608` | 1024–1073741824 | Hard per-file bound; oversized single events are rejected |
| `observation.max_files_per_source` | integer | `4` | 1–100 | Oldest files pruned independently for engine, Guard and supervisor |
| `observation.retention_hours` | integer | `168` | 1–8760 | Age pruning occurs during source-file rotation |
| `observation.include_cleartext_hostname` | boolean | `false` | Boolean | Explicitly adds hostname to persisted rows; network profile remains HMAC-only |

Fields marked planned in behavior are validated now but must not be described as active runtime features.

## 6. Decision reason-code catalog

| Reason code | Selected route | Evidence meaning | Promotion meaning |
| --- | --- | --- | --- |
| `direct_only_success` | Direct | Direct succeeded; Proxy failed | Strong Direct evidence, not an automatic permanent lock |
| `proxy_only_success` | Proxy | Direct failed; Proxy succeeded | Strong Proxy evidence |
| `both_success_direct_within_budget` | Direct | Both succeeded and Direct met configured latency budget | Medium Direct evidence |
| `both_success_proxy_materially_faster` | Proxy | Both succeeded and Direct exceeded latency budget | Medium Proxy evidence |
| `both_failed_use_original` | Original fallback | Neither path succeeded | No route promotion |

Runtime race/commit reasons:

| Reason code | Meaning | Commit behavior |
| --- | --- | --- |
| `direct_candidate_before_head_start` | Direct candidate was admitted before Proxy launch | Commit only if observation reaches configured minimum stage |
| `direct_candidate_won` | Direct candidate won after both candidates could run | Same stage gate applies |
| `proxy_candidate_won` | Proxy candidate won | Same stage gate applies |
| `proxy_candidate_before_head_start` | Learned Proxy-first candidate became ready before Direct launch | Commit at the same stage gate; absence of Direct is not failure evidence |
| `candidate_below_commit_stage` | Winning transport candidate did not prove target readiness | Close candidate, return SOCKS failure, `committed=false`, never learn |
| `client_hello_rejected` | Client first flight was unsafe or invalid | Close after local SOCKS admission; open zero candidates |
| `tls_candidates_failed` | Neither candidate returned a valid ServerHello | Close connection; report both classified path failures; never learn success |
| `privacy_proxy_path_failed` | Direct was forbidden and the only Proxy path failed L3 | Close; emit policy reason, Direct skipped marker and classified Proxy failure |

Direct-probe privacy reasons:

| Reason code | Direct candidate | Meaning |
| --- | ---: | --- |
| `direct_probe_allowed_explicit_opt_in` | Allowed | Process acknowledgment is present and no deny rule matches |
| `privacy_first_proxy_only` | Forbidden | Global privacy-first mode |
| `never_direct_probe_exact` | Forbidden | Exact hostname/IP deny entry matched |
| `never_direct_probe_suffix` | Forbidden | Apex or label-boundary suffix entry matched |
| `invalid_target_proxy_only` | Forbidden | Runtime target cannot be safely normalized |
| `missing_privacy_policy_proxy_only` | Forbidden | Sidecar was constructed without a compiled policy; fail closed |

Ephemeral learning reasons:

| Reason code | Counter update | Effect |
| --- | ---: | --- |
| `incomplete_paired_evidence_no_update` | No | Winner had no completed opposite observation |
| `weak_path_failure_no_update` | No | Opposite path was canceled/not-started or failed below outbound admission |
| `learning_capacity_reached_no_update` | No | Bounded table remained full after expired-entry pruning; routing continues |
| `strong_direct_evidence_recorded` | Direct +1 | Below threshold; no preference yet |
| `strong_proxy_evidence_recorded` | Proxy +1 | Below threshold; no preference yet |
| `ephemeral_direct_preference_promoted` | Direct threshold | Direct-first ephemeral preference becomes live only in auto mode |
| `ephemeral_proxy_preference_promoted` | Proxy threshold | Proxy-first ephemeral preference becomes live only in auto mode |
| `ephemeral_preference_refreshed` | Same direction +1 | Matching strong pair refreshes TTL |
| `ephemeral_preference_contradicted` | Opposite starts at 1 | Remove live preference and enter unstable |
| `learning_skipped_by_policy` | No | Privacy policy allowed only a single path |
| `learning_update_error` | No | Bounded metadata only; selected connection still commits |

Durable evidence reasons:

| Reason code | Durable effect | Route effect |
| --- | --- | --- |
| `durable_evidence_queued` | Strong pair accepted into the bounded writer queue | None |
| `durable_evidence_queue_full` | Strong pair dropped because the queue is full | None; current connection continues |
| `durable_evidence_writer_closed` | Strong pair arrived after shutdown admission closed | None; current connection continues |

Guard availability reasons:

| Reason code | Selected lane | Meaning |
| --- | --- | --- |
| `adaptive_available` | Adaptive | Adaptive engine completed its local SOCKS handshake before the bounded timeout |
| `adaptive_unavailable_use_original` | Original | Adaptive engine refused/timed out before payload; the same client connection uses the original-policy listener |
| `adaptive_and_original_unavailable` | None | Neither local lane accepted the target; return SOCKS failure |

## 7. Event catalog

| Event | Producer | Minimum fields | Consumer | Status |
| --- | --- | --- | --- | --- |
| `candidate.started` | Racer | target ID, path, timestamp | Metrics/debug | planned; not emitted yet |
| `candidate.ready` | Readiness gate | path, stage, latency | Decision engine | planned |
| `candidate.failed` | Dialer/gate | path, stage, failure class | Decision engine | planned |
| `candidate.canceled` | Racer | path, cancellation reason | Metrics | planned |
| `decision` | Sidecar/decision engine | `event_type`, target, selected path, reason, optional privacy/learning/durable reasons and ephemeral policy state, winner `observation`, optional completed `other_observation`, `committed` | CLI/recorder/UI | experimental `DecisionEvent`; JSONL schema v1 additive optional metadata |
| `diagnostic` | Sidecar | `event_type`, target, reason, failure class, optional Direct/Proxy failures and `policy_reason` | CLI/debug | experimental `DiagnosticEvent`; no payload bytes |
| `guard_decision` | Guard | `event_type`, target, selected lane, reason, bounded failure classes, `committed` | CLI/recorder/UI | experimental; JSONL schema v1 implemented, no payload bytes |
| `supervisor` | Supervisor | `event_type`, service, state, attempt, bounded failure class, optional `backoff_ms` | CLI/recorder/operator | experimental; states include `started`, `start_failed`, `exited`, `restart_scheduled`, `stopped` |
| `policy.promoted` | Learning engine | old/new state, evidence, expiry | Store/UI/export | planned |
| `learning.frozen` | Health guard | failing control, profile ID | UI/metrics | planned |

Events must never contain HTTP bodies, credentials, cookies, subscription URLs, or raw TLS secrets.

## 8. Maintenance checklist

When a symbol, field, event, or component changes:

1. Update its row in this file.
2. Update the relevant Mermaid diagram if dependencies changed.
3. Add an ADR if responsibility or safety semantics changed.
4. Add/update tests and name them in the registry.
5. Record user-visible changes in `CHANGELOG.md`.
