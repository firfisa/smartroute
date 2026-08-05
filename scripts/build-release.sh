#!/usr/bin/env bash
set -euo pipefail

version="${1:-}"
if [[ ! "${version}" =~ ^v[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$ ]]; then
  echo "usage: scripts/build-release.sh vMAJOR.MINOR.PATCH" >&2
  exit 1
fi

project_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
if [[ "${ALLOW_DIRTY_RELEASE:-0}" != "1" ]] && [[ -n "$(git -C "${project_root}" status --porcelain)" ]]; then
  echo "release build requires a clean worktree (set ALLOW_DIRTY_RELEASE=1 only for a local smoke build)" >&2
  exit 1
fi

export GOCACHE="${GOCACHE:-${project_root}/.cache/go-build}"
export GOMODCACHE="${GOMODCACHE:-${project_root}/.cache/go-mod}"
output="${project_root}/dist/${version}"
if [[ -e "${output}" ]]; then
  echo "release output already exists: ${output}" >&2
  exit 1
fi

commit="$(git -C "${project_root}" rev-parse HEAD)"
build_date="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
temporary="$(mktemp -d "${TMPDIR:-/tmp}/smartroute-release.XXXXXX")"
trap 'rm -rf "${temporary}"' EXIT
mkdir -p "${output}"

targets=(
  "darwin arm64"
  "darwin amd64"
  "linux arm64"
  "linux amd64"
)

for target in "${targets[@]}"; do
  read -r goos goarch <<<"${target}"
  name="smartroute-${version}-${goos}-${goarch}"
  staging="${temporary}/${name}"
  mkdir -p "${staging}"
  (
    cd "${project_root}"
    CGO_ENABLED=0 GOOS="${goos}" GOARCH="${goarch}" \
      go build -trimpath \
      -ldflags "-s -w -X main.version=${version} -X main.commit=${commit} -X main.date=${build_date}" \
      -o "${staging}/smartroute" ./cmd/smartroute
  )
  cp "${project_root}/LICENSE" "${project_root}/README.md" "${staging}/"
  tar -C "${temporary}" -czf "${output}/${name}.tar.gz" "${name}"
done

(
  cd "${output}"
  shasum -a 256 ./*.tar.gz > checksums.txt
  shasum -a 256 -c checksums.txt
)

native="${temporary}/smartroute-${version}-$(go env GOOS)-$(go env GOARCH)/smartroute"
if [[ -x "${native}" ]]; then
  "${native}" version
fi
printf 'release=%s\ncommit=%s\nbuilt=%s\nartifacts=%s\n' "${version}" "${commit}" "${build_date}" "${output}"
