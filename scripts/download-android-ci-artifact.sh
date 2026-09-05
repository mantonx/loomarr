#!/usr/bin/env bash

set -euo pipefail

readonly CI_RUN_ID="${1:-}"
readonly OUTPUT_DIR="${2:-}"
: "${GH_TOKEN:?GH_TOKEN is required}"
: "${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required}"
: "${GITHUB_SHA:?GITHUB_SHA is required}"

if [[ ! "${CI_RUN_ID}" =~ ^[1-9][0-9]*$ || -z "${OUTPUT_DIR}" ]]; then
  printf 'usage: %s <ci-run-id> <output-dir>\n' "$0" >&2
  exit 2
fi

repo_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
release_identity="${repo_root}/web/apps/tv/android-release.json"
version_name=$(jq -er '.versionName' "${release_identity}")
version_code=$("${repo_root}/scripts/android-version-code.sh" "${version_name}")
artifact_name="loomarr-android-unsigned-${GITHUB_SHA}"

run=$(gh api "repos/${GITHUB_REPOSITORY}/actions/runs/${CI_RUN_ID}")
jq -e \
  --arg sha "${GITHUB_SHA}" \
  '.head_sha == $sha and .event == "merge_group" and .status == "completed" and
   .conclusion == "success" and .path == ".github/workflows/ci.yml"' \
  <<<"${run}" >/dev/null || {
  printf 'android release: CI run is not successful merge-queue evidence for exact current main\n' >&2
  exit 1
}

jobs=$(gh api --paginate "repos/${GITHUB_REPOSITORY}/actions/runs/${CI_RUN_ID}/jobs?per_page=100")
jq -e \
  '[.jobs[] | select((.name == "Android TV — React Native Play bundle" or
    .name == "Android TV — React Native Play bundle / Android TV — React Native Play bundle") and
    .conclusion == "success")] | length == 1' <<<"${jobs}" >/dev/null || {
  printf 'android release: CI run did not execute exactly one successful Android bundle gate\n' >&2
  exit 1
}

artifacts=$(gh api "repos/${GITHUB_REPOSITORY}/actions/runs/${CI_RUN_ID}/artifacts?per_page=100")
artifact=$(jq -cer --arg name "${artifact_name}" \
  '[.artifacts[] | select(.name == $name and .expired == false)] |
   if length == 1 then .[0] else error("expected exactly one unexpired exact-name artifact") end' \
  <<<"${artifacts}") || {
  printf 'android release: CI run has no unique unexpired Android artifact for current main\n' >&2
  exit 1
}
artifact_id=$(jq -er '.id' <<<"${artifact}")
artifact_digest=$(jq -er '.digest | sub("^sha256:"; "")' <<<"${artifact}")

temp_dir=$(mktemp -d)
trap 'rm -rf -- "${temp_dir}"' EXIT
archive="${temp_dir}/artifact.zip"
gh api "repos/${GITHUB_REPOSITORY}/actions/artifacts/${artifact_id}/zip" >"${archive}"
actual_archive_digest=$(sha256sum "${archive}" | awk '{print $1}')
[[ "${actual_archive_digest}" == "${artifact_digest}" ]] || {
  printf 'android release: downloaded artifact archive digest does not match GitHub metadata\n' >&2
  exit 1
}

mkdir -p "${OUTPUT_DIR}"
unzip -q "${archive}" -d "${OUTPUT_DIR}"
aab="${OUTPUT_DIR}/loomarr-tv-${version_name}-${version_code}-unsigned.aab"
manifest="${aab%.aab}.json"
[[ -f "${aab}" && -f "${manifest}" ]] || {
  printf 'android release: artifact does not contain the source-controlled bundle identity\n' >&2
  exit 1
}
[[ "$(find "${OUTPUT_DIR}" -type f | wc -l | tr -d ' ')" == 2 ]] || {
  printf 'android release: artifact contains unexpected files\n' >&2
  exit 1
}

expected_aab_digest=$(jq -er '.aabSha256' "${manifest}")
actual_aab_digest=$(sha256sum "${aab}" | awk '{print $1}')
[[ "${actual_aab_digest}" == "${expected_aab_digest}" ]] || {
  printf 'android release: bundle digest does not match producer evidence\n' >&2
  exit 1
}
jq -e \
  --arg sha "${GITHUB_SHA}" \
  --arg run "${CI_RUN_ID}" \
  --arg name "${version_name}" \
  --argjson code "${version_code}" \
  '.commit == $sha and .producerRunId == $run and .producerEvent == "merge_group" and
   (.producerWorkflowRef | startswith($ENV.GITHUB_REPOSITORY + "/.github/workflows/ci.yml@")) and
   .versionName == $name and .versionCode == $code and .package == "loomarr.media" and .signed == false' \
  "${manifest}" >/dev/null || {
  printf 'android release: producer evidence does not bind exact source, run, workflow, and version\n' >&2
  exit 1
}

verification="${temp_dir}/verified.json"
"${repo_root}/web/scripts/verify-shield-aab.sh" \
  "${aab}" "${version_name}" "${version_code}" "${verification}" unsigned >/dev/null
jq -e --slurpfile verified "${verification}" \
  '.package == $verified[0].package and .versionName == $verified[0].versionName and
   .versionCode == $verified[0].versionCode and .aabSha256 == $verified[0].aabSha256 and
   .abis == $verified[0].abis and .nativeLibraries == $verified[0].nativeLibraries' \
  "${manifest}" >/dev/null || {
  printf 'android release: downloaded bundle inspection differs from producer evidence\n' >&2
  exit 1
}

printf 'android release: accepted artifact %s from merge-queue run %s for %s\n' \
  "${artifact_id}" "${CI_RUN_ID}" "${GITHUB_SHA}"
