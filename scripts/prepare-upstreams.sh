#!/usr/bin/env bash
set -euo pipefail

project_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
lock_file="${project_root}/upstreams.lock"
upstream_root="${SMARTRoute_UPSTREAM_ROOT:-${project_root}/.upstream}"

mkdir -p "${upstream_root}"

while IFS=$'\t' read -r name repository tag commit license; do
  if [[ -z "${name}" || "${name}" == \#* ]]; then
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
done < "${lock_file}"
