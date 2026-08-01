#!/usr/bin/env bash
set -euo pipefail

project_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

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
  "docs/adr/README.md"
  "docs/adr/0001-sidecar-first.md"
  "configs/smartroute.example.json"
  "upstreams.lock"
)

for relative_path in "${required_files[@]}"; do
  if [[ ! -s "${project_root}/${relative_path}" ]]; then
    echo "missing or empty maintained artifact: ${relative_path}" >&2
    exit 1
  fi
done

if ! rg -q '```mermaid' "${project_root}/README.md" "${project_root}/docs/02-technical-design.md" "${project_root}/docs/04-component-catalog.md"; then
  echo "maintained architecture documents must contain Mermaid diagrams" >&2
  exit 1
fi

if ! rg -q 'v1\.19\.29' "${project_root}/upstreams.lock" "${project_root}/docs/05-upstreams.md"; then
  echo "Mihomo lock and upstream documentation are inconsistent" >&2
  exit 1
fi

if ! rg -q 'v2\.5\.2' "${project_root}/upstreams.lock" "${project_root}/docs/05-upstreams.md"; then
  echo "Clash Verge Rev lock and upstream documentation are inconsistent" >&2
  exit 1
fi

if ! rg -q '^MIT License$' "${project_root}/LICENSE" ||
  ! rg -q 'first-party standalone source is MIT-licensed' "${project_root}/AGENTS.md" ||
  ! rg -q '活文档' "${project_root}/README.md"; then
  echo "license or living-document governance is inconsistent" >&2
  exit 1
fi

echo "documentation consistency checks passed"
