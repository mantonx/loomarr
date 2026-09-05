#!/usr/bin/env bash
# Inspect a Shield sideload APK and emit machine-readable evidence next to it.
set -euo pipefail

readonly APK_PATH="${1:-}"
readonly EXPECTED_VERSION="${2:-}"
readonly EXPECTED_CODE="${3:-}"
readonly EVIDENCE_PATH="${4:-}"

if [[ -z "${APK_PATH}" || -z "${EXPECTED_VERSION}" || -z "${EXPECTED_CODE}" || -z "${EVIDENCE_PATH}" ]]; then
  printf 'usage: %s <apk> <version-name> <version-code> <evidence-json>\n' "$0" >&2
  exit 2
fi
if [[ ! -f "${APK_PATH}" ]]; then
  printf 'Shield APK does not exist: %s\n' "${APK_PATH}" >&2
  exit 2
fi
if [[ -z "${ANDROID_HOME:-}" ]]; then
  printf 'ANDROID_HOME must point to the Android SDK\n' >&2
  exit 2
fi

find_build_tool() {
  local name="$1" tool
  tool="$(find "${ANDROID_HOME}/build-tools" -mindepth 2 -maxdepth 2 -type f -name "${name}" | sort | tail -n 1)"
  if [[ -z "${tool}" || ! -x "${tool}" ]]; then
    printf 'Android build tool not found: %s\n' "${name}" >&2
    exit 2
  fi
  printf '%s\n' "${tool}"
}

APKSIGNER="$(find_build_tool apksigner)"
readonly APKSIGNER
AAPT2="$(find_build_tool aapt2)"
readonly AAPT2
command -v apkanalyzer >/dev/null 2>&1 || {
  printf 'apkanalyzer must be available on PATH\n' >&2
  exit 2
}

APP_ID="$(apkanalyzer manifest application-id "${APK_PATH}")"
readonly APP_ID
VERSION_NAME="$(apkanalyzer manifest version-name "${APK_PATH}")"
readonly VERSION_NAME
VERSION_CODE="$(apkanalyzer manifest version-code "${APK_PATH}")"
readonly VERSION_CODE
DEBUGGABLE="$(apkanalyzer manifest debuggable "${APK_PATH}")"
readonly DEBUGGABLE
MANIFEST="$(apkanalyzer manifest print "${APK_PATH}")"
readonly MANIFEST
FILES="$(apkanalyzer files list --files-only "${APK_PATH}")"
readonly FILES
BADGING="$("${AAPT2}" dump badging "${APK_PATH}")"
readonly BADGING
RESOURCES="$("${AAPT2}" dump resources "${APK_PATH}")"
readonly RESOURCES
SIGNATURE="$("${APKSIGNER}" verify --verbose --print-certs "${APK_PATH}")"
readonly SIGNATURE

[[ "${APP_ID}" == "loomarr.media" ]] || {
  printf 'unexpected Shield package: %s\n' "${APP_ID}" >&2
  exit 1
}
[[ "${VERSION_NAME}" == "${EXPECTED_VERSION}" ]] || {
  printf 'unexpected Shield version name: %s\n' "${VERSION_NAME}" >&2
  exit 1
}
[[ "${VERSION_CODE}" == "${EXPECTED_CODE}" ]] || {
  printf 'unexpected Shield version code: %s\n' "${VERSION_CODE}" >&2
  exit 1
}
[[ "${DEBUGGABLE}" == "false" ]] || {
  printf 'Shield release APK must not be debuggable\n' >&2
  exit 1
}
grep -Fq 'android.intent.category.LEANBACK_LAUNCHER' <<<"${MANIFEST}" || {
  printf 'Shield APK has no Leanback launcher activity\n' >&2
  exit 1
}
grep -Fq 'android.intent.action.MAIN' <<<"${MANIFEST}" || {
  printf 'Shield APK has no main launcher action\n' >&2
  exit 1
}
grep -Fq 'android.software.leanback' <<<"${MANIFEST}" || {
  printf 'Shield APK does not declare Android TV support\n' >&2
  exit 1
}
grep -Fq 'android:banner=' <<<"${MANIFEST}" || {
  printf 'Shield APK has no application banner resource\n' >&2
  exit 1
}
grep -Fq 'android:icon=' <<<"${MANIFEST}" || {
  printf 'Shield APK has no application icon resource\n' >&2
  exit 1
}
grep -Fq "application-label:'Loomarr'" <<<"${BADGING}" || {
  printf 'Shield APK does not resolve the Loomarr application label\n' >&2
  exit 1
}
LAUNCHER_ACTIVITY="$(sed -nE "s/^leanback-launchable-activity: name='([^']+)'.*/\1/p" <<<"${BADGING}" | head -n 1)"
readonly LAUNCHER_ACTIVITY
[[ "${LAUNCHER_ACTIVITY}" == "loomarr.media.MainActivity" ]] || {
  printf 'unexpected Shield TV launcher activity: %s\n' "${LAUNCHER_ACTIVITY}" >&2
  exit 1
}
grep -Eq '(^|/)assets/index\.android\.bundle$' <<<"${FILES}" || {
  printf 'Shield APK has no embedded React Native bundle\n' >&2
  exit 1
}
grep -Fq 'drawable/tv_banner' <<<"${RESOURCES}" || {
  printf 'Shield APK has no packaged TV banner\n' >&2
  exit 1
}
grep -Fq 'mipmap/ic_launcher' <<<"${RESOURCES}" || {
  printf 'Shield APK has no packaged launcher icon\n' >&2
  exit 1
}
grep -Eq "application: .*icon='[^']+'.*banner='[^']+'" <<<"${BADGING}" || {
  printf 'Shield APK does not resolve both launcher icon and TV banner resources\n' >&2
  exit 1
}

BUNDLE_BYTES="$(apkanalyzer files cat --file assets/index.android.bundle "${APK_PATH}" | wc -c | tr -d ' ')"
readonly BUNDLE_BYTES
[[ "${BUNDLE_BYTES}" -gt 0 ]] || {
  printf 'Shield React Native bundle is empty\n' >&2
  exit 1
}
NATIVE_LIBRARIES="$(grep -Ec '(^|/)lib/arm64-v8a/[^/]+\.so$' <<<"${FILES}" || true)"
readonly NATIVE_LIBRARIES
[[ "${NATIVE_LIBRARIES}" -gt 0 ]] || {
  printf 'Shield APK has no arm64-v8a native libraries\n' >&2
  exit 1
}
NATIVE_ABIS="$(sed -nE 's#^/?lib/([^/]+)/.*#\1#p' <<<"${FILES}" | sort -u | paste -sd, -)"
readonly NATIVE_ABIS
[[ "${NATIVE_ABIS}" == "arm64-v8a" ]] || {
  printf 'unexpected Shield native ABIs: %s\n' "${NATIVE_ABIS}" >&2
  exit 1
}

CERT_SHA256="$(sed -nE 's/^Signer #1 certificate SHA-256 digest: //p' <<<"${SIGNATURE}" | head -n 1)"
readonly CERT_SHA256
[[ -n "${CERT_SHA256}" ]] || {
  printf 'Shield APK signature certificate digest was not reported\n' >&2
  exit 1
}
APK_SHA256="$(shasum -a 256 "${APK_PATH}" | awk '{print $1}')"
readonly APK_SHA256

jq -n \
  --arg apk "${APK_PATH}" \
  --arg apkSha256 "${APK_SHA256}" \
  --arg package "${APP_ID}" \
  --arg versionName "${VERSION_NAME}" \
  --argjson versionCode "${VERSION_CODE}" \
  --arg certificateSha256 "${CERT_SHA256}" \
  --arg launcherActivity "${LAUNCHER_ACTIVITY}" \
  --arg nativeAbis "${NATIVE_ABIS}" \
  --argjson nativeLibraryCount "${NATIVE_LIBRARIES}" \
  --argjson bundleBytes "${BUNDLE_BYTES}" \
  '{apk: $apk, apkSha256: $apkSha256, package: $package, versionName: $versionName,
    versionCode: $versionCode, certificateSha256: $certificateSha256,
    launcherActivity: $launcherActivity, leanbackLauncher: true,
    applicationLabel: "Loomarr", icon: true, banner: true,
    sourceEntry: "web/apps/tv/index.ts", javascriptBundle: "assets/index.android.bundle", bundleBytes: $bundleBytes,
    nativeAbis: ($nativeAbis | split(",")), nativeLibraryCount: $nativeLibraryCount}' \
  >"${EVIDENCE_PATH}"

printf 'verified Shield sideload: %s\n' "${APK_PATH}"
cat "${EVIDENCE_PATH}"
