#!/usr/bin/env bash
set -euo pipefail

project_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
lock_file="${project_root}/upstreams.lock"
source_root="${SMARTRoute_MIHOMO_SOURCE:-${project_root}/.upstream/mihomo}"
tool_root="${SMARTRoute_TOOL_ROOT:-${project_root}/.cache/tools}"
go_cache="${SMARTRoute_GO_CACHE:-${project_root}/.cache/go-build}"
module_cache="${SMARTRoute_GO_MODULE_CACHE:-${project_root}/.cache/go-mod}"

mihomo_tag=""
mihomo_commit=""
while IFS=$'\t' read -r name repository tag commit license; do
  if [[ "${name}" == "mihomo" ]]; then
    mihomo_tag="${tag}"
    mihomo_commit="${commit}"
    break
  fi
done < "${lock_file}"

if [[ -z "${mihomo_tag}" || ! "${mihomo_commit}" =~ ^[0-9a-f]{40}$ ]]; then
  echo "valid Mihomo entry not found in upstreams.lock" >&2
  exit 1
fi
if [[ ! -d "${source_root}/.git" ]]; then
  echo "Mihomo source is not prepared; run bash scripts/prepare-upstreams.sh mihomo" >&2
  exit 1
fi
actual_commit="$(git -C "${source_root}" rev-parse HEAD)"
if [[ "${actual_commit}" != "${mihomo_commit}" ]]; then
  echo "Mihomo source mismatch: expected ${mihomo_commit}, got ${actual_commit}" >&2
  exit 1
fi

mkdir -p "${tool_root}" "${go_cache}" "${module_cache}"
binary="${tool_root}/mihomo-${mihomo_tag}"
(
  cd "${source_root}"
  CGO_ENABLED=0 GOCACHE="${go_cache}" GOMODCACHE="${module_cache}" \
    go build -tags with_gvisor -trimpath \
    -ldflags "-X github.com/metacubex/mihomo/constant.Version=${mihomo_tag} -X github.com/metacubex/mihomo/constant.BuildTime=SmartRoute-isolated-lab" \
    -o "${binary}" .
)

version_output="$("${binary}" -v)"
if [[ "${version_output}" != *"${mihomo_tag}"* ]]; then
  echo "built Mihomo version mismatch: ${version_output}" >&2
  exit 1
fi
echo "${binary}"
