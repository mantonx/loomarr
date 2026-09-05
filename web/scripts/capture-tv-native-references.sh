#!/usr/bin/env bash
# Capture full-screen React Native Storybook states at the two Kotlin TV reference layouts.
set -euo pipefail

readonly PACKAGE_ID="media.loomarr.tv.prototype"
readonly CAPTURE_PORT="${LOOMARR_TV_REFERENCE_PORT:-18778}"
WEB_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
readonly WEB_ROOT
readonly APP_DIR="${WEB_ROOT}/apps/tv"
readonly APK_PATH="${APP_DIR}/android/app/build/outputs/apk/debug/app-debug.apk"
readonly OUTPUT_ROOT="${APP_DIR}/tests/native-references"
readonly SERVER_SCRIPT="${WEB_ROOT}/scripts/tv-native-reference-server.mjs"

if ! command -v adb >/dev/null 2>&1 || ! command -v curl >/dev/null 2>&1; then
  printf 'adb and curl are required to capture TV native references\n' >&2
  exit 2
fi

if [[ -n "${LOOMARR_TV_EMULATOR_SERIAL:-}" ]]; then
  emulator_serial="${LOOMARR_TV_EMULATOR_SERIAL}"
else
  emulator_serials="$(adb devices | awk '$1 ~ /^emulator-/ && $2 == "device" { print $1 }')"
  emulator_count="$(printf '%s\n' "${emulator_serials}" | awk 'NF { count += 1 } END { print count + 0 }')"
  if [[ "${emulator_count}" -ne 1 ]]; then
    printf 'start exactly one Android TV emulator or set LOOMARR_TV_EMULATOR_SERIAL (found %s)\n' \
      "${emulator_count}" >&2
    exit 2
  fi
  emulator_serial="${emulator_serials}"
fi
readonly emulator_serial

emulator_architecture="$(adb -s "${emulator_serial}" shell getprop ro.product.cpu.abi | tr -d '\r')"
case "${emulator_architecture}" in
  arm64-v8a|x86_64) ;;
  *)
    printf 'unsupported TV emulator architecture: %s\n' "${emulator_architecture}" >&2
    exit 2
    ;;
esac
readonly emulator_architecture

physical_size="$(
  adb -s "${emulator_serial}" shell wm size | awk '/Physical size:/ { print $3 }' | tr -d '\r'
)"
physical_density="$(
  adb -s "${emulator_serial}" shell wm density | awk '/Physical density:/ { print $3 }' | tr -d '\r'
)"
case "${physical_size}:${physical_density}" in
  1920x1080:320)
    capture_layout_name="1080p"
    ;;
  3840x2160:640)
    capture_layout_name="4k"
    ;;
  *)
    printf 'TV emulator must have a native 1920x1080/320 or 3840x2160/640 framebuffer; got %s/%s\n' \
      "${physical_size}" "${physical_density}" >&2
    exit 2
    ;;
esac
readonly physical_size physical_density capture_layout_name

capture_dir="$(mktemp -d)"
readonly capture_dir
server_pid=""
cleanup() {
  adb -s "${emulator_serial}" reverse --remove "tcp:${CAPTURE_PORT}" >/dev/null 2>&1 || true
  if [[ -n "${server_pid}" ]]; then
    kill "${server_pid}" >/dev/null 2>&1 || true
    wait "${server_pid}" >/dev/null 2>&1 || true
  fi
  rm -rf -- "${capture_dir}"
}
trap cleanup EXIT INT TERM

node "${SERVER_SCRIPT}" "${CAPTURE_PORT}" >"${capture_dir}/server.log" 2>&1 &
server_pid=$!
for _ in {1..50}; do
  if curl --fail --silent "http://127.0.0.1:${CAPTURE_PORT}" >/dev/null; then
    break
  fi
  if ! kill -0 "${server_pid}" 2>/dev/null; then
    cat "${capture_dir}/server.log" >&2
    exit 1
  fi
  sleep 0.1
done
curl --fail --silent "http://127.0.0.1:${CAPTURE_PORT}" >/dev/null

printf 'tv-native-references: building embedded Storybook for %s\n' "${emulator_architecture}"
EXPO_PUBLIC_LOOMARR_STORYBOOK_DENSITY=tv \
  EXPO_PUBLIC_LOOMARR_STORYBOOK_CAPTURE_URL="http://127.0.0.1:${CAPTURE_PORT}" \
  EXPO_TV=1 \
  STORYBOOK_ENABLED=true \
  LOOMARR_ANDROID_ARCHITECTURES="${emulator_architecture}" \
  "${WEB_ROOT}/scripts/build-android-client.sh" tv

adb -s "${emulator_serial}" install -r "${APK_PATH}" >/dev/null
adb -s "${emulator_serial}" shell pm clear "${PACKAGE_ID}" >/dev/null
adb -s "${emulator_serial}" reverse "tcp:${CAPTURE_PORT}" "tcp:${CAPTURE_PORT}" >/dev/null

stories=(
  "pairing|loomarr-components-pairing-shell--tv"
  "pairing-loading|loomarr-components-pairing-shell--tv-loading"
  "pairing-error|loomarr-components-pairing-shell--tv-error"
  "watching|loomarr-components-watching-surface--number-entry"
  "watching-loading|loomarr-components-watching-surface--loading"
  "watching-empty|loomarr-components-watching-surface--empty-channel"
  "watching-error|loomarr-components-watching-surface--playback-error"
  "surf-focused|loomarr-components-surf-journey--tv"
  "surf-loading|loomarr-components-surf-journey--tv-loading"
  "surf-empty|loomarr-components-surf-journey--tv-empty"
  "surf-error|loomarr-components-surf-journey--tv-error"
  "guide-focused|loomarr-components-guide-journey--tv"
  "guide-loading|loomarr-components-guide-journey--tv-loading"
  "guide-empty|loomarr-components-guide-journey--tv-empty"
  "guide-error|loomarr-components-guide-journey--tv-error"
)

wait_for_story() {
  local description="$1"
  local story_id="$2"
  for _ in {1..30}; do
    if adb -s "${emulator_serial}" logcat -d | grep -Fq \
      "LOOMARR_NATIVE_REFERENCE_SELECTED:${story_id}"; then
      printf 'tv-native-references: observed %s\n' "${description}"
      return 0
    fi
    sleep 0.35
  done
  printf 'TV emulator never selected %s (%s)\n' "${description}" "${story_id}" >&2
  adb -s "${emulator_serial}" logcat -d -t 200 >&2
  return 1
}

capture_layout() {
  local layout="$1"
  local size="$2"
  local width="${size%x*}"
  local height="${size#*x}"
  local output_dir="${OUTPUT_ROOT}/${layout}"

  mkdir -p "${output_dir}"

  for entry in "${stories[@]}"; do
    IFS='|' read -r name story_id <<<"${entry}"
    adb -s "${emulator_serial}" shell am force-stop "${PACKAGE_ID}"
    curl --fail --silent --request PUT --data-binary "${story_id}" \
      "http://127.0.0.1:${CAPTURE_PORT}" >/dev/null
    adb -s "${emulator_serial}" logcat -c
    adb -s "${emulator_serial}" shell am start -W -n "${PACKAGE_ID}/.MainActivity" >/dev/null
    wait_for_story "${layout}/${name}" "${story_id}"
    sleep 2
    adb -s "${emulator_serial}" exec-out screencap -p >"${output_dir}/${name}.png"
    dimensions="$(
      node -e '
        const fs = require("node:fs");
        const image = fs.readFileSync(process.argv[1]);
        process.stdout.write(image.readUInt32BE(16) + "x" + image.readUInt32BE(20));
      ' "${output_dir}/${name}.png"
    )"
    if [[ "${dimensions}" != "${width}x${height}" ]]; then
      printf 'captured %s at %s, expected %sx%s\n' "${name}" "${dimensions}" "${width}" "${height}" >&2
      exit 1
    fi
  done
}

capture_layout "${capture_layout_name}" "${physical_size}"

printf 'tv-native-references: captured %d states at %s\n' \
  "${#stories[@]}" "${capture_layout_name}"
