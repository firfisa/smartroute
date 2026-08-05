#!/usr/bin/env bash
set -euo pipefail

project_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

matches() {
  local pattern="$1"
  shift

  if command -v rg >/dev/null 2>&1; then
    rg -q "${pattern}" "$@"
  else
    grep -Eq "${pattern}" "$@"
  fi
}

required_files=(
  "go.mod"
  "go.sum"
  "LICENSE"
  "AGENTS.md"
  "README.md"
  "CHANGELOG.md"
  "docs/01-project-assessment.md"
  "docs/02-technical-design.md"
  "docs/03-mvp-validation-plan.md"
  "docs/04-component-catalog.md"
  "docs/05-upstreams.md"
  "docs/06-protocol-capability-matrix.md"
  "docs/07-isolated-test-lab.md"
  "docs/08-observation-and-live-trial.md"
  "docs/09-sidecar-overhead-benchmark.md"
  "docs/10-concurrent-relay-load-lab.md"
  "docs/11-offered-load-capacity-lab.md"
  "docs/12-fixed-policy-management-plane.md"
  "docs/13-active-clash-readonly-compatibility.md"
  "docs/14-live-trial-candidate-package.md"
  "docs/15-macos-launch-agent.md"
  "docs/adr/README.md"
  "docs/adr/0001-sidecar-first.md"
  "docs/adr/0002-isolated-test-lab.md"
  "docs/adr/0003-read-only-clash-inspection-and-live-rollout.md"
  "docs/adr/0004-mihomo-socks-ack-is-not-target-readiness.md"
  "docs/adr/0005-safe-tls-first-flight-racing.md"
  "docs/adr/0006-separate-availability-guard.md"
  "docs/adr/0007-enforce-direct-probe-privacy.md"
  "docs/adr/0008-supervise-guard-and-engine.md"
  "docs/adr/0009-bounded-local-observation-recorder.md"
  "docs/adr/0010-preserve-only-completed-counterfactual-evidence.md"
  "docs/adr/0011-ephemeral-learning-and-preferred-racing.md"
  "docs/adr/0012-sqlite-strong-evidence-store.md"
  "docs/adr/0013-opt-in-async-durable-evidence-writer.md"
  "docs/adr/0014-durable-evidence-lifecycle.md"
  "docs/adr/0015-cross-session-shadow-assessment.md"
  "docs/adr/0016-privacy-safe-shadow-report.md"
  "docs/adr/0017-freeze-learning-on-systemic-failure.md"
  "docs/adr/0018-identity-free-observation-readiness-report.md"
  "docs/adr/0019-random-shared-trial-session-scope.md"
  "docs/adr/0020-read-only-evidence-based-trial-preflight.md"
  "docs/adr/0021-context-cancel-and-drain-runtime-connections.md"
  "docs/adr/0022-post-commit-relay-telemetry.md"
  "docs/adr/0023-random-connection-observation-scope.md"
  "docs/adr/0024-declared-original-policy-baseline.md"
  "docs/adr/0025-bounded-relay-direction-end-reasons.md"
  "docs/adr/0026-post-trial-descriptive-data-quality-gate.md"
  "docs/adr/0027-pre-register-trial-assessment-plan.md"
  "docs/adr/0028-synthetic-trial-control-plane-lab.md"
  "docs/adr/0029-paired-loopback-sidecar-overhead-benchmark.md"
  "docs/adr/0030-paired-pinned-mihomo-forced-direct-benchmark.md"
  "docs/adr/0031-paired-tls-serverhello-readiness-benchmark.md"
  "docs/adr/0032-separate-concurrent-chunked-relay-load-lab.md"
  "docs/adr/0033-retain-standard-library-tcp-copy-after-load-sweep.md"
  "docs/adr/0034-client-paced-offered-load-capacity-lab.md"
  "docs/adr/0035-manual-fixed-policy-management-plane.md"
  "docs/adr/0036-opt-in-automatic-durable-policy-layer.md"
  "docs/adr/0037-make-automatic-last-known-good-the-product-default.md"
  "docs/adr/0038-private-checksum-gated-live-candidate.md"
  "docs/adr/0039-reserve-readiness-time-for-automatic-fallback.md"
  "docs/adr/0040-arm-runtime-before-active-reload.md"
  "docs/adr/0041-report-automatic-policy-effect.md"
  "docs/adr/0042-verify-semantic-active-script-binding.md"
  "docs/adr/0043-own-live-supervisor-with-macos-launchagent.md"
  "cmd/smartroute-runtime-lab/main.go"
  "internal/mihomolab/runtime.go"
  "internal/runtimecheck/check.go"
  "integrations/clash-verge/smartroute-transform.js"
  "scripts/compose-clash-script.mjs"
  "scripts/test-clash-transform.mjs"
  "scripts/test-clash-transform-mihomo.mjs"
  "scripts/apply-composed-clash-script.mjs"
  "scripts/prepare-active-clash-candidate.rb"
  "scripts/manage-active-clash-candidate.rb"
  "scripts/test-prepare-active-clash-candidate.rb"
  "scripts/prepare-live-trial-runtime.rb"
  "scripts/prepare-macos-launch-agent.rb"
  "scripts/test-prepare-macos-launch-agent.rb"
  "scripts/relocate-live-runtime.rb"
  "scripts/test-relocate-live-runtime.rb"
  "scripts/build-release.sh"
  ".github/workflows/release.yml"
  "docs/releases/v0.1.0.md"
  "configs/smartroute.example.json"
  "upstreams.lock"
)

for relative_path in "${required_files[@]}"; do
  if [[ ! -s "${project_root}/${relative_path}" ]]; then
    echo "missing or empty maintained artifact: ${relative_path}" >&2
    exit 1
  fi
done

if ! matches '```mermaid' "${project_root}/README.md" "${project_root}/docs/02-technical-design.md" "${project_root}/docs/04-component-catalog.md"; then
  echo "maintained architecture documents must contain Mermaid diagrams" >&2
  exit 1
fi

if ! matches 'v1\.19\.29' "${project_root}/upstreams.lock" "${project_root}/docs/05-upstreams.md"; then
  echo "Mihomo lock and upstream documentation are inconsistent" >&2
  exit 1
fi

if ! matches 'v2\.5\.2' "${project_root}/upstreams.lock" "${project_root}/docs/05-upstreams.md"; then
  echo "Clash Verge Rev lock and upstream documentation are inconsistent" >&2
  exit 1
fi

if ! matches '^MIT License$' "${project_root}/LICENSE" ||
  ! matches 'first-party standalone source is MIT-licensed' "${project_root}/AGENTS.md" ||
  ! matches '活文档' "${project_root}/README.md"; then
  echo "license or living-document governance is inconsistent" >&2
  exit 1
fi

if ! matches '127\.0\.0\.1:0' "${project_root}/docs/07-isolated-test-lab.md" ||
  ! matches 'Automated tests must not inspect, change, reload, or share listeners' "${project_root}/AGENTS.md"; then
  echo "isolated test-lab boundary is missing or inconsistent" >&2
  exit 1
fi

if ! matches 'CurrentReportVersion = 2' "${project_root}/internal/testlab/lab.go" ||
  ! matches 'auto_first_ready_remembered' "${project_root}/internal/testlab/automatic_learning.go" "${project_root}/docs/07-isolated-test-lab.md" ||
  ! matches 'auto_fallback_overwrites_proxy' "${project_root}/internal/testlab/automatic_learning.go" "${project_root}/docs/07-isolated-test-lab.md" ||
  ! matches 'testlab\.ScenariosComplete' "${project_root}/internal/trial/preflight.go" "${project_root}/docs/04-component-catalog.md"; then
  echo "standalone last-known-good Test Lab contract is missing" >&2
  exit 1
fi

if ! matches 'Scoped read-only inspection' "${project_root}/AGENTS.md" ||
  ! matches 'Until this procedure reaches step 9 with explicit confirmation' "${project_root}/docs/08-observation-and-live-trial.md"; then
  echo "read-only inspection or coordinated live-write boundary is missing" >&2
  exit 1
fi

if ! matches '1,286 total; exactly one `MATCH`' "${project_root}/docs/13-active-clash-readonly-compatibility.md" ||
  ! matches 'Catch-all branch 0.*Proxy' "${project_root}/docs/13-active-clash-readonly-compatibility.md" ||
  ! matches 'authorizes none of those writes' "${project_root}/docs/13-active-clash-readonly-compatibility.md"; then
  echo "active Clash read-only compatibility snapshot is missing or unsafe" >&2
  exit 1
fi

if ! matches 'candidate_below_commit_stage' "${project_root}/docs/02-technical-design.md" "${project_root}/docs/04-component-catalog.md" ||
  ! matches 'make mihomo-lab' "${project_root}/README.md" "${project_root}/docs/07-isolated-test-lab.md"; then
  echo "Mihomo readiness contract or isolated lab documentation is inconsistent" >&2
  exit 1
fi

if ! matches 'early_data' "${project_root}/AGENTS.md" "${project_root}/docs/adr/0005-safe-tls-first-flight-racing.md" ||
  ! matches 'tls_proxy_recovers_unreachable_direct' "${project_root}/docs/07-isolated-test-lab.md"; then
  echo "TLS first-flight safety contract or runtime evidence is missing" >&2
  exit 1
fi

if ! matches 'guard_falls_back_when_engine_unavailable' "${project_root}/docs/07-isolated-test-lab.md" ||
  ! matches 'does not dial the next member for the same connection' "${project_root}/docs/adr/0006-separate-availability-guard.md"; then
  echo "availability guard contract or Mihomo fallback evidence is missing" >&2
  exit 1
fi

if ! matches 'privacy_first_proxy_only' "${project_root}/docs/04-component-catalog.md" ||
  ! matches 'never_direct_probe' "${project_root}/docs/adr/0007-enforce-direct-probe-privacy.md"; then
  echo "runtime Direct-probe privacy contract is missing" >&2
  exit 1
fi

if ! matches 'smartroute supervise' "${project_root}/README.md" "${project_root}/docs/adr/0008-supervise-guard-and-engine.md" ||
  ! matches 'restart_scheduled' "${project_root}/docs/04-component-catalog.md"; then
  echo "supervisor lifecycle contract is missing" >&2
  exit 1
fi

if ! matches 'smartroute observations' "${project_root}/README.md" "${project_root}/docs/adr/0009-bounded-local-observation-recorder.md" ||
  ! matches 'include_cleartext_hostname' "${project_root}/configs/smartroute.example.json" "${project_root}/docs/04-component-catalog.md"; then
  echo "bounded local observation contract is missing" >&2
  exit 1
fi

if ! matches 'other_observation' "${project_root}/docs/04-component-catalog.md" "${project_root}/docs/adr/0010-preserve-only-completed-counterfactual-evidence.md" ||
  ! matches 'canceled.*not.*evidence' "${project_root}/docs/adr/0010-preserve-only-completed-counterfactual-evidence.md"; then
  echo "completed counterfactual evidence contract is missing" >&2
  exit 1
fi

if ! matches '"mode": "auto"' "${project_root}/configs/smartroute.example.json" ||
  ! matches 'old Shadow/ephemeral engine is not instantiated' "${project_root}/docs/08-observation-and-live-trial.md" ||
  ! matches 'ModeAuto' "${project_root}/internal/learning/engine.go"; then
  echo "default automatic routing contract is missing" >&2
  exit 1
fi

if ! matches 'modernc.org/sqlite.*v1\.55\.0' "${project_root}/go.mod" "${project_root}/docs/05-upstreams.md" "${project_root}/docs/adr/0012-sqlite-strong-evidence-store.md" ||
  ! matches 'ErrCorrupt' "${project_root}/docs/04-component-catalog.md" "${project_root}/docs/adr/0012-sqlite-strong-evidence-store.md"; then
  echo "SQLite strong-evidence persistence contract is missing" >&2
  exit 1
fi

if ! matches '"persistence"' "${project_root}/configs/smartroute.example.json" ||
  ! matches 'durable_evidence_queue_full' "${project_root}/docs/04-component-catalog.md" "${project_root}/docs/adr/0013-opt-in-async-durable-evidence-writer.md" ||
  ! matches 'Automatic application and local persistence default on' "${project_root}/AGENTS.md"; then
  echo "default asynchronous automatic-policy writer contract is missing" >&2
  exit 1
fi

if ! matches 'learning status' "${project_root}/README.md" "${project_root}/docs/04-component-catalog.md" ||
  ! matches 'verify-backup' "${project_root}/README.md" "${project_root}/docs/adr/0014-durable-evidence-lifecycle.md" ||
  ! matches 'must never overwrite or automatically activate' "${project_root}/AGENTS.md"; then
  echo "durable evidence lifecycle contract is missing" >&2
  exit 1
fi

if ! matches 'direct_suggestion_sessions' "${project_root}/docs/04-component-catalog.md" ||
  ! matches 'durable_learning_assessment' "${project_root}/docs/08-observation-and-live-trial.md" "${project_root}/docs/adr/0015-cross-session-shadow-assessment.md" ||
  ! matches 'assessments remain legacy diagnostics and never feed runtime selection' "${project_root}/AGENTS.md"; then
  echo "cross-session shadow assessment contract is missing" >&2
  exit 1
fi

if ! matches 'learning report' "${project_root}/README.md" "${project_root}/docs/04-component-catalog.md" ||
  ! matches 'target keys' "${project_root}/docs/adr/0016-privacy-safe-shadow-report.md" ||
  ! matches 'must never expose target keys' "${project_root}/AGENTS.md"; then
  echo "privacy-safe Shadow report contract is missing" >&2
  exit 1
fi

if ! matches 'trial preflight' "${project_root}/README.md" "${project_root}/docs/04-component-catalog.md" ||
  ! matches 'AuthorizesLiveActivation' "${project_root}/internal/trial/preflight.go" ||
  ! matches 'never authorizes' "${project_root}/docs/adr/0020-read-only-evidence-based-trial-preflight.md"; then
  echo "read-only trial preflight contract is missing" >&2
  exit 1
fi

if ! matches 'netrelay\.Bidirectional\(ctx' "${project_root}/docs/04-component-catalog.md" ||
  ! matches 'wait for all connection handlers' "${project_root}/AGENTS.md" ||
  ! matches 'never routing evidence' "${project_root}/docs/adr/0021-context-cancel-and-drain-runtime-connections.md"; then
  echo "runtime connection cancellation and drain contract is missing" >&2
  exit 1
fi

if ! matches 'relay_outcome' "${project_root}/docs/04-component-catalog.md" "${project_root}/docs/08-observation-and-live-trial.md" ||
  ! matches 'Remote bytes include protocol readiness data' "${project_root}/AGENTS.md" ||
  ! matches 'not application success' "${project_root}/docs/adr/0022-post-commit-relay-telemetry.md"; then
  echo "post-commit relay telemetry privacy contract is missing" >&2
  exit 1
fi

if ! matches 'connection_id' "${project_root}/docs/04-component-catalog.md" "${project_root}/docs/08-observation-and-live-trial.md" ||
  ! matches 'Connection identifiers must be random non-semantic correlation scopes' "${project_root}/AGENTS.md" ||
  ! matches 'rather than fabricated failures' "${project_root}/docs/adr/0023-random-connection-observation-scope.md"; then
  echo "random connection observation scope contract is missing" >&2
  exit 1
fi

if ! matches 'declared_baseline_path' "${project_root}/docs/04-component-catalog.md" "${project_root}/docs/08-observation-and-live-trial.md" ||
  ! matches 'original_fallback.*declared category' "${project_root}/AGENTS.md" ||
  ! matches 'not a live Mihomo rule trace' "${project_root}/docs/adr/0024-declared-original-policy-baseline.md"; then
  echo "declared original-policy baseline contract is missing" >&2
  exit 1
fi

if ! matches 'client_to_remote_end' "${project_root}/docs/04-component-catalog.md" "${project_root}/docs/08-observation-and-live-trial.md" ||
  ! matches 'raw errors are forbidden' "${project_root}/AGENTS.md" ||
  ! matches 'never include rejected values in error text' "${project_root}/docs/adr/0025-bounded-relay-direction-end-reasons.md"; then
  echo "bounded relay direction-end privacy contract is missing" >&2
  exit 1
fi

if ! matches 'trial assess' "${project_root}/README.md" "${project_root}/docs/04-component-catalog.md" "${project_root}/docs/08-observation-and-live-trial.md" ||
  ! matches 'descriptive analysis only' "${project_root}/AGENTS.md" ||
  ! matches 'authorizes_policy_change=false' "${project_root}/docs/adr/0026-post-trial-descriptive-data-quality-gate.md"; then
  echo "post-trial descriptive data-quality gate contract is missing" >&2
  exit 1
fi

if ! matches 'preflight-report' "${project_root}/README.md" "${project_root}/docs/04-component-catalog.md" "${project_root}/docs/08-observation-and-live-trial.md" ||
  ! matches 'ExpectedTrialSessionID' "${project_root}/internal/observe/report.go" ||
  ! matches 'must be fixed by a successful preflight' "${project_root}/AGENTS.md" ||
  ! matches 'no longer accepts replacement window or threshold flags' "${project_root}/docs/adr/0027-pre-register-trial-assessment-plan.md"; then
  echo "pre-registered trial assessment plan contract is missing" >&2
  exit 1
fi

if ! matches 'smartroute-trial-lab' "${project_root}/AGENTS.md" "${project_root}/docs/04-component-catalog.md" "${project_root}/docs/07-isolated-test-lab.md" ||
  ! matches 'smartroute-trial-lab' "${project_root}/.github/workflows/ci.yml" ||
  ! matches 'preflight_evidence=false' "${project_root}/README.md" "${project_root}/docs/adr/0028-synthetic-trial-control-plane-lab.md" ||
  ! matches 'Synthetic Trial Lab output is plumbing evidence only' "${project_root}/AGENTS.md"; then
  echo "synthetic trial control-plane lab contract is missing" >&2
  exit 1
fi

if ! matches 'smartroute-benchmark-lab' "${project_root}/AGENTS.md" "${project_root}/docs/04-component-catalog.md" "${project_root}/docs/07-isolated-test-lab.md" "${project_root}/.github/workflows/ci.yml" ||
  ! matches 'WorstRunP95OverheadUS' "${project_root}/internal/benchlab/lab.go" ||
  ! matches 'latency gate is environment-dependent and opt-in' "${project_root}/AGENTS.md" ||
  ! matches 'fake-gateway or pinned-Mihomo tier' "${project_root}/AGENTS.md" ||
  ! matches 'worst per-run paired p95' "${project_root}/docs/adr/0029-paired-loopback-sidecar-overhead-benchmark.md"; then
  echo "paired sidecar overhead benchmark contract is missing" >&2
  exit 1
fi

if ! matches 'pinned_mihomo_forced_direct' "${project_root}/internal/benchlab/lab.go" "${project_root}/docs/09-sidecar-overhead-benchmark.md" ||
  ! matches 'direct_gateway_attempts_available=false' "${project_root}/docs/adr/0030-paired-pinned-mihomo-forced-direct-benchmark.md" ||
  ! matches 'make benchmark-mihomo' "${project_root}/README.md" "${project_root}/docs/04-component-catalog.md" "${project_root}/docs/07-isolated-test-lab.md" ||
  ! matches 'pinned Mihomo sidecar benchmark smoke' "${project_root}/.github/workflows/mihomo-lab.yml"; then
  echo "pinned Mihomo benchmark tier contract is missing" >&2
  exit 1
fi

if ! matches 'tls_server_hello' "${project_root}/internal/benchlab/lab.go" "${project_root}/docs/09-sidecar-overhead-benchmark.md" ||
  ! matches 'TLSClientHellosAccepted' "${project_root}/internal/benchlab/lab.go" ||
  ! matches 'contains no `early_data`' "${project_root}/docs/adr/0031-paired-tls-serverhello-readiness-benchmark.md" ||
  ! matches 'make benchmark-mihomo-tls' "${project_root}/README.md" "${project_root}/docs/04-component-catalog.md" "${project_root}/docs/07-isolated-test-lab.md" ||
  ! matches 'Loopback TLS readiness benchmark smoke' "${project_root}/.github/workflows/ci.yml" ||
  ! matches 'pinned Mihomo TLS readiness benchmark smoke' "${project_root}/.github/workflows/mihomo-lab.yml"; then
  echo "TLS ServerHello benchmark protocol contract is missing" >&2
  exit 1
fi

if ! matches 'smartroute-load-lab' "${project_root}/AGENTS.md" "${project_root}/docs/04-component-catalog.md" "${project_root}/docs/07-isolated-test-lab.md" "${project_root}/.github/workflows/ci.yml" ||
  ! matches 'bidirectional_relay_bytes' "${project_root}/internal/loadlab/lab.go" "${project_root}/docs/10-concurrent-relay-load-lab.md" ||
  ! matches 'missed gate must remain visible' "${project_root}/AGENTS.md" ||
  ! matches '0\.70 gate missed' "${project_root}/docs/adr/0032-separate-concurrent-chunked-relay-load-lab.md" ||
  ! matches 'make load-mihomo' "${project_root}/README.md" "${project_root}/docs/04-component-catalog.md" "${project_root}/docs/07-isolated-test-lab.md" ||
  ! matches 'pinned Mihomo concurrent relay load smoke' "${project_root}/.github/workflows/mihomo-lab.yml"; then
  echo "concurrent relay Load Lab contract is missing" >&2
  exit 1
fi

if ! matches 'smartroute-load-sweep' "${project_root}/AGENTS.md" "${project_root}/Makefile" "${project_root}/docs/04-component-catalog.md" "${project_root}/docs/10-concurrent-relay-load-lab.md" ||
  ! matches 'runtime_metrics_scope' "${project_root}/internal/loadlab/lab.go" ||
  ! matches 'short-window CPU deltas are diagnostic' "${project_root}/docs/adr/0033-retain-standard-library-tcp-copy-after-load-sweep.md" ||
  ! matches 'Retain standard-library TCP copying' "${project_root}/docs/adr/README.md" ||
  ! matches 'make load-sweep-mihomo' "${project_root}/README.md" "${project_root}/docs/04-component-catalog.md" "${project_root}/docs/07-isolated-test-lab.md" ||
  ! matches 'Fixed relay load sweep smoke' "${project_root}/.github/workflows/ci.yml" ||
  ! matches 'pinned Mihomo fixed relay load sweep smoke' "${project_root}/.github/workflows/mihomo-lab.yml"; then
  echo "fixed load sweep and relay-copy decision contract is missing" >&2
  exit 1
fi

if ! matches 'smartroute-capacity-lab' "${project_root}/AGENTS.md" "${project_root}/Makefile" "${project_root}/docs/04-component-catalog.md" "${project_root}/docs/11-offered-load-capacity-lab.md" ||
  ! matches 'represents_network_emulation' "${project_root}/internal/loadlab/capacity.go" ||
  ! matches 'Baseline must meet' "${project_root}/docs/adr/0034-client-paced-offered-load-capacity-lab.md" ||
  ! matches 'make capacity-mihomo' "${project_root}/README.md" "${project_root}/docs/04-component-catalog.md" "${project_root}/docs/07-isolated-test-lab.md" ||
  ! matches 'Offered-load capacity lab' "${project_root}/.github/workflows/ci.yml" ||
  ! matches 'pinned Mihomo offered-load capacity lab' "${project_root}/.github/workflows/mihomo-lab.yml"; then
  echo "client-paced offered-load capacity contract is missing" >&2
  exit 1
fi

if ! matches 'smartroute policy list\|lock\|revoke' "${project_root}/docs/04-component-catalog.md" "${project_root}/docs/02-technical-design.md" ||
  ! matches 'fixed_policy.database_path' "${project_root}/docs/04-component-catalog.md" ||
  ! matches 'fixed-policies.db' "${project_root}/README.md" "${project_root}/docs/12-fixed-policy-management-plane.md" ||
  ! matches 'runtime activation is not implemented' "${project_root}/cmd/smartroute/main.go" "${project_root}/docs/12-fixed-policy-management-plane.md" ||
  ! matches 'management-plane-only' "${project_root}/AGENTS.md" ||
  ! matches 'without runtime activation' "${project_root}/docs/adr/README.md"; then
  echo "manual fixed-policy management-plane contract is missing" >&2
  exit 1
fi

if matches 'fixedpolicy' "${project_root}/internal/sidecar/server.go" "${project_root}/internal/guard/server.go" "${project_root}/internal/learning/engine.go"; then
  echo "fixed-policy store leaked into the Phase 0 runtime path" >&2
  exit 1
fi

if ! matches '"mode": "auto"' "${project_root}/configs/smartroute.example.json" "${project_root}/README.md" ||
  ! matches 'no approval.*promotion counter.*TTL' "${project_root}/docs/adr/0037-make-automatic-last-known-good-the-product-default.md" ||
  ! matches 'RememberDurablePath' "${project_root}/internal/store/sqlite.go" "${project_root}/docs/04-component-catalog.md" ||
  ! matches 'NewAsyncPolicyWriter' "${project_root}/internal/store/writer.go" "${project_root}/docs/04-component-catalog.md" ||
  ! matches 'ConnectPreferredWithFallback' "${project_root}/internal/transport/tls_readiness.go" "${project_root}/docs/04-component-catalog.md" ||
  ! matches 'dedicated bounded asynchronous policy writer' "${project_root}/docs/adr/0037-make-automatic-last-known-good-the-product-default.md"; then
  echo "automatic last-known-good routing contract is missing" >&2
  exit 1
fi

if matches 'promotion_wins|policy_ttl_hours|max_evidence_rows|suggestion_sessions' "${project_root}/configs/smartroute.example.json"; then
  echo "legacy learning controls leaked into the default automatic config" >&2
  exit 1
fi

if ! matches 'runtime-lab' "${project_root}/Makefile" "${project_root}/README.md" "${project_root}/docs/04-component-catalog.md" "${project_root}/docs/07-isolated-test-lab.md" ||
  ! matches 'Run full transformed SmartRoute runtime lab' "${project_root}/.github/workflows/mihomo-lab.yml" ||
  ! matches 'r\.Timeout/2' "${project_root}/internal/transport/tls_readiness.go" ||
  ! matches 'half the total readiness' "${project_root}/docs/adr/0039-reserve-readiness-time-for-automatic-fallback.md" ||
  ! matches 'attachDurableLearning' "${project_root}/cmd/smartroute/main.go" "${project_root}/cmd/smartroute/main_test.go"; then
  echo "full runtime lab and reserved fallback budget contract is missing" >&2
  exit 1
fi

if ! matches 'applySmartRoute' "${project_root}/integrations/clash-verge/smartroute-transform.js" "${project_root}/docs/04-component-catalog.md" ||
  ! matches 'clash-transform-mihomo' "${project_root}/Makefile" "${project_root}/README.md" "${project_root}/docs/13-active-clash-readonly-compatibility.md" ||
  ! matches 'Clash Verge transform tests' "${project_root}/.github/workflows/ci.yml" ||
  ! matches 'Validate transformed Clash config with pinned Mihomo' "${project_root}/.github/workflows/mihomo-lab.yml"; then
  echo "Clash Verge final-MATCH transform contract is missing" >&2
  exit 1
fi

if ! matches 'active-candidate-test' "${project_root}/Makefile" "${project_root}/README.md" "${project_root}/docs/04-component-catalog.md" ||
  ! matches 'Validate private active-candidate packaging' "${project_root}/.github/workflows/mihomo-lab.yml" ||
  ! matches 'atomic.*install.*rollback' "${project_root}/docs/14-live-trial-candidate-package.md" "${project_root}/docs/adr/0038-private-checksum-gated-live-candidate.md" ||
  ! matches 'must not reload Clash' "${project_root}/AGENTS.md"; then
  echo "private checksum-gated live-candidate contract is missing" >&2
  exit 1
fi

if ! matches 'smartroute doctor' "${project_root}/README.md" "${project_root}/docs/04-component-catalog.md" "${project_root}/docs/adr/0040-arm-runtime-before-active-reload.md" ||
  ! matches 'PhaseArmed' "${project_root}/internal/runtimecheck/check.go" ||
  ! matches 'prepare-live-trial-runtime' "${project_root}/scripts/test-prepare-active-clash-candidate.rb" "${project_root}/docs/14-live-trial-candidate-package.md" ||
  ! matches 'ReportVersion = 7' "${project_root}/internal/observe/report.go" ||
  ! matches 'learning_reason_counts' "${project_root}/internal/observe/report.go" "${project_root}/docs/adr/0041-report-automatic-policy-effect.md"; then
  echo "armed live sequencing or automatic-effect report contract is missing" >&2
  exit 1
fi

if ! matches 'macos-launch-agent-test' "${project_root}/Makefile" "${project_root}/README.md" "${project_root}/docs/15-macos-launch-agent.md" ||
  ! matches 'never calls `launchctl`' "${project_root}/docs/adr/0043-own-live-supervisor-with-macos-launchagent.md" ||
  ! matches 'metadata changes' "${project_root}/docs/adr/0042-verify-semantic-active-script-binding.md"; then
  echo "macOS external-service or semantic-binding contract is missing" >&2
  exit 1
fi

if matches 'provisional_policy_ttl_hours' "${project_root}/configs/smartroute.example.json" "${project_root}/internal/config" "${project_root}/README.md" "${project_root}/docs/04-component-catalog.md"; then
  echo "removed automatic TTL tier remains in the maintained contract" >&2
  exit 1
fi

if ! matches 'make release VERSION=v0\.1\.0' "${project_root}/README.md" ||
  ! matches 'build-release\.sh VERSION' "${project_root}/docs/04-component-catalog.md" ||
  ! matches 'smartroute-\$\{version\}-\$\{goos\}-\$\{goarch\}' "${project_root}/scripts/build-release.sh" ||
  ! matches 'docs/releases/\$\{GITHUB_REF_NAME\}\.md' "${project_root}/.github/workflows/release.yml" ||
  ! matches 'first numbered public release' "${project_root}/docs/releases/v0.1.0.md"; then
  echo "versioned release contract is missing" >&2
  exit 1
fi

echo "documentation consistency checks passed"
