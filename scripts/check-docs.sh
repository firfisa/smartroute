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

echo "documentation consistency checks passed"
