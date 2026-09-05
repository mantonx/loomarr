#!/usr/bin/env bash

set -euo pipefail

readonly UNSIGNED_AAB="${1:-}"
readonly PRODUCER_MANIFEST="${2:-}"
readonly OUTPUT_DIR="${3:-}"
if [[ -z "${UNSIGNED_AAB}" || -z "${PRODUCER_MANIFEST}" || -z "${OUTPUT_DIR}" ]]; then
  printf 'usage: %s <unsigned.aab> <producer-manifest.json> <output-dir>\n' "$0" >&2
  exit 2
fi

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
repo_root=$(cd -- "${script_dir}/.." && pwd)
version_name=$(jq -er '.versionName' "${PRODUCER_MANIFEST}")
version_code=$(jq -er '.versionCode' "${PRODUCER_MANIFEST}")
LOOMARR_ANDROID_VERSION_NAME="${version_name}" \
  LOOMARR_ANDROID_VERSION_CODE="${version_code}" \
  "${script_dir}/check-android-release-env.sh"
[[ "$(jq -er '.signed' "${PRODUCER_MANIFEST}")" == false ]] || {
  printf 'android release: producer manifest must describe an unsigned bundle\n' >&2
  exit 1
}
expected_digest=$(jq -er '.aabSha256' "${PRODUCER_MANIFEST}")
actual_digest=$(sha256sum "${UNSIGNED_AAB}" | awk '{print $1}')
[[ "${actual_digest}" == "${expected_digest}" ]] || {
  printf 'android release: unsigned bundle digest changed before signing\n' >&2
  exit 1
}
if unzip -Z1 "${UNSIGNED_AAB}" | grep -Eq '^META-INF/'; then
  printf 'android release: refusing an unsigned input with META-INF signing material\n' >&2
  exit 1
fi

temp_dir=$(mktemp -d)
trap 'rm -rf -- "${temp_dir}"' EXIT
before_entries="${temp_dir}/before.tsv"
after_entries="${temp_dir}/after.tsv"
hash_entries() {
  local archive=$1
  local output=$2
  while IFS= read -r entry; do
    [[ "${entry}" == */ ]] && continue
    [[ "${entry}" == META-INF/* ]] && continue
    printf '%s\t%s\n' "$(unzip -p "${archive}" "${entry}" | sha256sum | awk '{print $1}')" "${entry}"
  done < <(unzip -Z1 "${archive}" | LC_ALL=C sort) >"${output}"
}
hash_entries "${UNSIGNED_AAB}" "${before_entries}"

mkdir -p "${OUTPUT_DIR}"
signed_aab="${OUTPUT_DIR}/loomarr-tv-${version_name}-${version_code}.aab"
signed_manifest="${signed_aab%.aab}.json"
LC_ALL=C jarsigner \
  -keystore "${LOOMARR_ANDROID_KEYSTORE_PATH}" \
  -storepass:env LOOMARR_ANDROID_KEYSTORE_PASSWORD \
  -keypass:env LOOMARR_ANDROID_KEY_PASSWORD \
  -signedjar "${signed_aab}" \
  "${UNSIGNED_AAB}" "${LOOMARR_ANDROID_KEY_ALIAS}" >/dev/null

hash_entries "${signed_aab}" "${after_entries}"
cmp -s "${before_entries}" "${after_entries}" || {
  printf 'android release: signing changed a non-signature bundle entry\n' >&2
  diff -u "${before_entries}" "${after_entries}" >&2 || true
  exit 1
}
if unzip -Z1 "${signed_aab}" | grep '^META-INF/' | grep -Ev \
  '^META-INF/(MANIFEST\.MF|[A-Z0-9_-]{1,8}\.(SF|RSA|DSA|EC))$' >/dev/null; then
  printf 'android release: signer introduced unexpected META-INF material\n' >&2
  exit 1
fi

"${repo_root}/web/scripts/verify-shield-aab.sh" \
  "${signed_aab}" "${version_name}" "${version_code}" "${signed_manifest}" signed >/dev/null
jq --arg unsignedAabSha256 "${expected_digest}" \
  --arg producerCommit "$(jq -er '.commit' "${PRODUCER_MANIFEST}")" \
  --arg producerRunId "$(jq -er '.producerRunId' "${PRODUCER_MANIFEST}")" \
  '. + {unsignedAabSha256: $unsignedAabSha256, producerCommit: $producerCommit,
    producerRunId: $producerRunId}' "${signed_manifest}" >"${temp_dir}/signed.json"
mv "${temp_dir}/signed.json" "${signed_manifest}"

printf 'android release: signed and verified %s without recompilation\n' "${signed_aab}"
