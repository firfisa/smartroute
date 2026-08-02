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

if ! matches 'Scoped read-only inspection' "${project_root}/AGENTS.md" ||
  ! matches 'Until this procedure reaches step 7 with explicit confirmation' "${project_root}/docs/08-observation-and-live-trial.md"; then
  echo "read-only inspection or coordinated live-write boundary is missing" >&2
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

if ! matches '"mode": "shadow"' "${project_root}/configs/smartroute.example.json" ||
  ! matches 'ephemeral-auto' "${project_root}/docs/04-component-catalog.md" "${project_root}/docs/adr/0011-ephemeral-learning-and-preferred-racing.md" ||
  ! matches 'proxy_candidate_before_head_start' "${project_root}/docs/04-component-catalog.md"; then
  echo "ephemeral learning and preferred-race contract is missing" >&2
  exit 1
fi

if ! matches 'modernc.org/sqlite.*v1\.55\.0' "${project_root}/go.mod" "${project_root}/docs/05-upstreams.md" "${project_root}/docs/adr/0012-sqlite-strong-evidence-store.md" ||
  ! matches 'ErrCorrupt' "${project_root}/docs/04-component-catalog.md" "${project_root}/docs/adr/0012-sqlite-strong-evidence-store.md"; then
  echo "SQLite strong-evidence persistence contract is missing" >&2
  exit 1
fi

if ! matches '"persistence"' "${project_root}/configs/smartroute.example.json" ||
  ! matches 'durable_evidence_queue_full' "${project_root}/docs/04-component-catalog.md" "${project_root}/docs/adr/0013-opt-in-async-durable-evidence-writer.md" ||
  ! matches 'defaults off and is shadow-only' "${project_root}/AGENTS.md"; then
  echo "opt-in asynchronous durable writer contract is missing" >&2
  exit 1
fi

if ! matches 'learning status' "${project_root}/README.md" "${project_root}/docs/04-component-catalog.md" ||
  ! matches 'verify-backup' "${project_root}/README.md" "${project_root}/docs/adr/0014-durable-evidence-lifecycle.md" ||
  ! matches 'must never overwrite or automatically activate' "${project_root}/AGENTS.md"; then
  echo "durable evidence lifecycle contract is missing" >&2
  exit 1
fi

if ! matches 'direct_suggestion_sessions' "${project_root}/configs/smartroute.example.json" "${project_root}/docs/04-component-catalog.md" ||
  ! matches 'durable_learning_assessment' "${project_root}/docs/08-observation-and-live-trial.md" "${project_root}/docs/adr/0015-cross-session-shadow-assessment.md" ||
  ! matches 'must never feed.*PreferredPath' "${project_root}/AGENTS.md"; then
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

echo "documentation consistency checks passed"
