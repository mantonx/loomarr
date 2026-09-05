#!/usr/bin/env bash
# Build with an ephemeral key, then clean-install and launch on an Android TV emulator.
set -euo pipefail

readonly VERSION_NAME="${1:-0.1.0-beta.1}"
WEB_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
readonly WEB_ROOT
TEMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/loomarr-shield-sideload.XXXXXX")"
readonly TEMP_DIR
readonly PASSWORD='loomarr-ephemeral-sideload'
trap 'rm -rf "${TEMP_DIR}"' EXIT

export LOOMARR_ANDROID_KEYSTORE_PATH="${TEMP_DIR}/shield-sideload.p12"
export LOOMARR_ANDROID_KEYSTORE_PASSWORD="${PASSWORD}"
export LOOMARR_ANDROID_KEY_ALIAS='loomarr-shield'
export LOOMARR_ANDROID_KEY_PASSWORD="${PASSWORD}"
export LOOMARR_SHIELD_OUTPUT_DIR="${LOOMARR_SHIELD_TEST_OUTPUT_DIR:-${WEB_ROOT}/../.artifacts/shield-sideload-test}"

keytool -genkeypair -noprompt \
  -storetype PKCS12 \
  -keystore "${LOOMARR_ANDROID_KEYSTORE_PATH}" \
  -storepass "${PASSWORD}" \
  -keypass "${PASSWORD}" \
  -alias "${LOOMARR_ANDROID_KEY_ALIAS}" \
  -keyalg RSA -keysize 2048 -validity 30 \
  -dname 'CN=Loomarr Ephemeral Shield Sideload,OU=Testing,O=Loomarr,C=US' >/dev/null 2>&1

"${WEB_ROOT}/scripts/build-shield-sideload.sh" "${VERSION_NAME}"

command -v adb >/dev/null 2>&1 || {
  printf 'adb must be available on PATH\n' >&2
  exit 2
}
DEVICES="$(adb devices | awk 'NR > 1 && $2 == "device" { print $1 }')"
if [[ -n "${ANDROID_SERIAL:-}" ]]; then
  readonly SERIAL="${ANDROID_SERIAL}"
elif [[ "$(wc -w <<<"${DEVICES}" | tr -d ' ')" -eq 1 ]]; then
  readonly SERIAL="${DEVICES}"
else
  printf 'set ANDROID_SERIAL to one running Android TV emulator\n' >&2
  exit 2
fi
AVD_NAME="$(adb -s "${SERIAL}" emu avd name 2>/dev/null | tr -d '\r' | head -n 1)"
readonly AVD_NAME
case "${AVD_NAME}" in
  loomarr-tv|loomarr-tv-4k) ;;
  *)
    printf 'refusing clean install on non-Loomarr emulator %s (%s)\n' "${SERIAL}" "${AVD_NAME}" >&2
    exit 2
    ;;
esac

readonly APK_PATH="${LOOMARR_SHIELD_OUTPUT_DIR}/loomarr-shield-${VERSION_NAME}-arm64-v8a.apk"
adb -s "${SERIAL}" uninstall loomarr.media >/dev/null 2>&1 || true
adb -s "${SERIAL}" install "${APK_PATH}" >/dev/null
COMPONENT="$(adb -s "${SERIAL}" shell cmd package resolve-activity --brief \
  -a android.intent.action.MAIN -c android.intent.category.LEANBACK_LAUNCHER loomarr.media \
  | tr -d '\r' | tail -n 1)"
readonly COMPONENT
[[ "${COMPONENT}" == loomarr.media/* ]] || {
  printf 'could not resolve the Shield TV launcher activity: %s\n' "${COMPONENT}" >&2
  exit 1
}
START_RESULT="$(adb -s "${SERIAL}" shell am start -W -n "${COMPONENT}")"
readonly START_RESULT
grep -Fq 'Status: ok' <<<"${START_RESULT}" || {
  printf 'Shield TV launcher did not start successfully\n%s\n' "${START_RESULT}" >&2
  exit 1
}
adb -s "${SERIAL}" shell pidof loomarr.media >/dev/null || {
  printf 'Shield process is not running after launcher start\n' >&2
  exit 1
}

printf 'clean-installed and launched %s on %s (%s)\n' "${APK_PATH}" "${SERIAL}" "${AVD_NAME}"
