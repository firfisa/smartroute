#!/usr/bin/env bash
set -euo pipefail

project_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
lock_file="${project_root}/upstreams.lock"
upstream_root="${SMARTRoute_UPSTREAM_ROOT:-${project_root}/.upstream}"

mkdir -p "${upstream_root}"

is_requested() {
  local candidate="$1"
  shift
  if [[ "$#" -eq 0 ]]; then
    return 0
  fi
  local requested
  for requested in "$@"; do
    if [[ "${requested}" == "${candidate}" ]]; then
      return 0
    fi
  done
  return 1
}

prepared=0

while IFS=$'\t' read -r name repository tag commit license; do
  if [[ -z "${name}" || "${name}" == \#* ]]; then
    continue
  fi
  if ! is_requested "${name}" "$@"; then
    continue
  fi
  if [[ ! "${name}" =~ ^[a-z0-9-]+$ || ! "${commit}" =~ ^[0-9a-f]{40}$ ]]; then
    echo "invalid upstream lock entry: ${name}" >&2
    exit 1
  fi

  target="${upstream_root}/${name}"
  if [[ ! -d "${target}/.git" ]]; then
    git clone --filter=blob:none --no-checkout "${repository}" "${target}"
  fi

  git -C "${target}" fetch --depth 1 origin "${commit}"
  git -C "${target}" checkout --detach "${commit}"
  actual="$(git -C "${target}" rev-parse HEAD)"
  if [[ "${actual}" != "${commit}" ]]; then
    echo "upstream verification failed for ${name}: expected ${commit}, got ${actual}" >&2
    exit 1
  fi
  echo "prepared ${name} ${tag} ${commit} (${license})"
  prepared=$((prepared + 1))
done < "${lock_file}"

if [[ "$#" -gt 0 && "${prepared}" -ne "$#" ]]; then
  echo "one or more requested upstream names were not found in upstreams.lock" >&2
  exit 1
fi
