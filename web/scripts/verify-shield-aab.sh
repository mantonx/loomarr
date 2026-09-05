#!/usr/bin/env bash
# Inspect a React Native Shield App Bundle and emit machine-readable release evidence.
set -euo pipefail

readonly AAB_PATH="${1:-}"
readonly EXPECTED_VERSION="${2:-}"
readonly EXPECTED_CODE="${3:-}"
readonly EVIDENCE_PATH="${4:-}"
readonly SIGNING_MODE="${5:-signed}"

if [[ -z "${AAB_PATH}" || -z "${EXPECTED_VERSION}" || -z "${EXPECTED_CODE}" || -z "${EVIDENCE_PATH}" ]]; then
  printf 'usage: %s <aab> <version-name> <version-code> <evidence-json> [signed|unsigned]\n' "$0" >&2
  exit 2
fi
if [[ ! -f "${AAB_PATH}" ]]; then
  printf 'Shield App Bundle does not exist: %s\n' "${AAB_PATH}" >&2
  exit 2
fi
if [[ -z "${ANDROID_HOME:-}" ]]; then
  printf 'ANDROID_HOME must point to the Android SDK\n' >&2
  exit 2
fi
if [[ "${SIGNING_MODE}" != signed && "${SIGNING_MODE}" != unsigned ]]; then
  printf 'Shield App Bundle signing mode must be signed or unsigned\n' >&2
  exit 2
fi
if [[ "${SIGNING_MODE}" == signed ]]; then
  : "${LOOMARR_ANDROID_UPLOAD_CERT_SHA256:?LOOMARR_ANDROID_UPLOAD_CERT_SHA256 is required}"
fi

for command in jq unzip keytool jarsigner strings; do
  command -v "${command}" >/dev/null 2>&1 || {
    printf 'Shield App Bundle inspection requires %s\n' "${command}" >&2
    exit 2
  }
done
if command -v readelf >/dev/null 2>&1; then
  readonly READELF=readelf
elif command -v llvm-readelf >/dev/null 2>&1; then
  readonly READELF=llvm-readelf
elif [[ -x "$(find "${ANDROID_HOME}/ndk" -type f -o -type l 2>/dev/null | grep '/llvm-readelf$' | sort | tail -n 1)" ]]; then
  READELF="$(find "${ANDROID_HOME}/ndk" \( -type f -o -type l \) -name llvm-readelf | sort | tail -n 1)"
  readonly READELF
else
  printf 'Shield App Bundle inspection requires readelf or llvm-readelf\n' >&2
  exit 2
fi

normalize_fingerprint() {
  tr -d ':[:space:]' | tr '[:lower:]' '[:upper:]'
}

INSPECTION_DIR="$(mktemp -d)"
readonly INSPECTION_DIR
trap 'rm -rf "${INSPECTION_DIR}"' EXIT

BUNDLE_FINGERPRINT=""
if [[ "${SIGNING_MODE}" == signed ]]; then
  EXPECTED_FINGERPRINT="$(printf '%s' "${LOOMARR_ANDROID_UPLOAD_CERT_SHA256}" | normalize_fingerprint)"
  readonly EXPECTED_FINGERPRINT
  [[ "${EXPECTED_FINGERPRINT}" =~ ^[0-9A-F]{64}$ ]] || {
    printf 'LOOMARR_ANDROID_UPLOAD_CERT_SHA256 must be a SHA-256 certificate fingerprint\n' >&2
    exit 2
  }

  # Upload keys are intentionally self-signed. Trust the embedded certificate only for this
  # cryptographic verification, then bind that exact certificate to the protected fingerprint.
  readonly SIGNER_CERTIFICATE="${INSPECTION_DIR}/signer.pem"
  readonly SIGNER_TRUSTSTORE="${INSPECTION_DIR}/signer-trust.p12"
  readonly SIGNER_TRUSTSTORE_PASSWORD="loomarr-aab-verifier"
  keytool -printcert -rfc -jarfile "${AAB_PATH}" >"${SIGNER_CERTIFICATE}"
  keytool -importcert -noprompt -alias loomarr-upload \
    -file "${SIGNER_CERTIFICATE}" -keystore "${SIGNER_TRUSTSTORE}" -storetype PKCS12 \
    -storepass "${SIGNER_TRUSTSTORE_PASSWORD}" >/dev/null 2>&1
  LC_ALL=C jarsigner -verify -strict -keystore "${SIGNER_TRUSTSTORE}" \
    -storepass "${SIGNER_TRUSTSTORE_PASSWORD}" "${AAB_PATH}" >/dev/null 2>&1
  BUNDLE_FINGERPRINT="$(
    LC_ALL=C keytool -printcert -file "${SIGNER_CERTIFICATE}" |
      awk -F': ' '/SHA256:/{print $2; exit}' |
      normalize_fingerprint
  )"
  [[ "${BUNDLE_FINGERPRINT}" == "${EXPECTED_FINGERPRINT}" ]] || {
    printf 'Shield App Bundle certificate does not match the protected upload fingerprint\n' >&2
    exit 1
  }
else
  if unzip -Z1 "${AAB_PATH}" | grep -Eq '^META-INF/'; then
    printf 'unsigned Shield App Bundle contains forbidden META-INF signing material\n' >&2
    exit 1
  fi
fi
readonly BUNDLE_FINGERPRINT
unzip -q "${AAB_PATH}" 'base/manifest/AndroidManifest.xml' 'base/resources.pb' 'base/lib/*/*.so' 'base/assets/*' -d "${INSPECTION_DIR}"

readonly MANIFEST_PATH="${INSPECTION_DIR}/base/manifest/AndroidManifest.xml"
[[ -s "${MANIFEST_PATH}" ]] || {
  printf 'Shield App Bundle has no base Android manifest\n' >&2
  exit 1
}
MANIFEST_TEXT="$(strings "${MANIFEST_PATH}")"
readonly MANIFEST_TEXT
for value in loomarr.media "${EXPECTED_VERSION}" "${EXPECTED_CODE}" loomarr.media.MainActivity \
  android.intent.action.MAIN android.intent.category.LEANBACK_LAUNCHER android.software.leanback \
  drawable/tv_banner; do
  grep -Fq "${value}" <<<"${MANIFEST_TEXT}" || {
    printf 'Shield App Bundle manifest is missing %s\n' "${value}" >&2
    exit 1
  }
done
RESOURCES_TEXT="$(strings "${INSPECTION_DIR}/base/resources.pb")"
readonly RESOURCES_TEXT
for value in app_name Loomarr tv_banner ic_launcher; do
  grep -Fq "${value}" <<<"${RESOURCES_TEXT}" || {
    printf 'Shield App Bundle resources are missing %s\n' "${value}" >&2
    exit 1
  }
done

readonly BUNDLE_PATH="${INSPECTION_DIR}/base/assets/index.android.bundle"
[[ -s "${BUNDLE_PATH}" ]] || {
  printf 'Shield App Bundle has no embedded React Native bundle\n' >&2
  exit 1
}
grep -aFq "${EXPECTED_VERSION}" "${BUNDLE_PATH}" || {
  printf 'Shield React Native bundle does not contain artifact version %s\n' "${EXPECTED_VERSION}" >&2
  exit 1
}

LIBRARIES=()
while IFS= read -r library; do
  LIBRARIES+=("${library}")
done < <(find "${INSPECTION_DIR}/base/lib" -type f -name '*.so' -print | sort)
if ((${#LIBRARIES[@]} == 0)); then
  printf 'Shield App Bundle has no native libraries\n' >&2
  exit 1
fi
ABIS=()
while IFS= read -r abi; do
  ABIS+=("${abi}")
done < <(find "${INSPECTION_DIR}/base/lib" -mindepth 1 -maxdepth 1 -type d -exec basename {} \; | sort)
readonly EXPECTED_ABIS='arm64-v8a,armeabi-v7a,x86,x86_64'
ACTUAL_ABIS="$(IFS=,; printf '%s' "${ABIS[*]}")"
readonly ACTUAL_ABIS
[[ "${ACTUAL_ABIS}" == "${EXPECTED_ABIS}" ]] || {
  printf 'unexpected Shield App Bundle ABIs: %s\n' "${ACTUAL_ABIS}" >&2
  exit 1
}
for library in "${LIBRARIES[@]}"; do
  case "${library}" in
    */arm64-v8a/*|*/x86_64/*) ;;
    *) continue ;;
  esac
  ALIGNMENTS=()
  while IFS= read -r alignment; do
    ALIGNMENTS+=("${alignment}")
  done < <("${READELF}" -lW "${library}" | awk '$1 == "LOAD" {print $NF}')
  if ((${#ALIGNMENTS[@]} == 0)); then
    printf 'no ELF LOAD segments found in %s\n' "${library#"${INSPECTION_DIR}/"}" >&2
    exit 1
  fi
  for alignment in "${ALIGNMENTS[@]}"; do
    if ((alignment < 0x4000)); then
      printf '%s has LOAD alignment %s below 16 KiB\n' "${library#"${INSPECTION_DIR}/"}" "${alignment}" >&2
      exit 1
    fi
  done
done

if command -v sha256sum >/dev/null 2>&1; then
  AAB_SHA256="$(sha256sum "${AAB_PATH}" | awk '{print $1}')"
else
  AAB_SHA256="$(shasum -a 256 "${AAB_PATH}" | awk '{print $1}')"
fi
readonly AAB_SHA256
COMMIT="$(git -C "$(dirname "${BASH_SOURCE[0]}")/../.." rev-parse HEAD)"
readonly COMMIT
PRODUCER_RUN_ID="${GITHUB_RUN_ID:-}"
PRODUCER_EVENT="${GITHUB_EVENT_NAME:-}"
PRODUCER_WORKFLOW_REF="${GITHUB_WORKFLOW_REF:-}"
readonly PRODUCER_RUN_ID PRODUCER_EVENT PRODUCER_WORKFLOW_REF

jq -n \
  --arg aab "${AAB_PATH}" \
  --arg aabSha256 "${AAB_SHA256}" \
  --arg package loomarr.media \
  --arg versionName "${EXPECTED_VERSION}" \
  --argjson versionCode "${EXPECTED_CODE}" \
  --arg commit "${COMMIT}" \
  --arg producerRunId "${PRODUCER_RUN_ID}" \
  --arg producerEvent "${PRODUCER_EVENT}" \
  --arg producerWorkflowRef "${PRODUCER_WORKFLOW_REF}" \
  --arg uploadCertificateSha256 "${BUNDLE_FINGERPRINT}" \
  --argjson signed "$([[ "${SIGNING_MODE}" == signed ]] && printf true || printf false)" \
  --argjson nativeLibraries "${#LIBRARIES[@]}" \
  --argjson bundleBytes "$(wc -c <"${BUNDLE_PATH}" | tr -d ' ')" \
  '{aab: $aab, aabSha256: $aabSha256, package: $package, versionName: $versionName,
    versionCode: $versionCode, commit: $commit, signed: $signed,
    producerRunId: (if $producerRunId == "" then null else $producerRunId end),
    producerEvent: (if $producerEvent == "" then null else $producerEvent end),
    producerWorkflowRef: (if $producerWorkflowRef == "" then null else $producerWorkflowRef end),
    uploadCertificateSha256: (if $uploadCertificateSha256 == "" then null else $uploadCertificateSha256 end),
    launcherActivity: "loomarr.media.MainActivity", leanbackLauncher: true,
    applicationLabel: "Loomarr", icon: true, banner: true,
    sourceEntry: "web/apps/tv/index.ts", javascriptBundle: "base/assets/index.android.bundle",
    bundleBytes: $bundleBytes, abis: ["arm64-v8a", "armeabi-v7a", "x86", "x86_64"],
    nativeLibraries: $nativeLibraries, elfLoadAlignmentBytes: 16384,
    elfLoadAlignmentAbis: ["arm64-v8a", "x86_64"]}' >"${EVIDENCE_PATH}"

printf 'verified Shield App Bundle: %s\n' "${AAB_PATH}"
cat "${EVIDENCE_PATH}"
