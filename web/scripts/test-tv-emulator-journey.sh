#!/usr/bin/env bash
# Build, install, and drive the production TV root through its replacement journey on one emulator.
set -euo pipefail

readonly PACKAGE_ID="media.loomarr.tv.prototype"
readonly JOURNEY_PORT="${LOOMARR_TV_JOURNEY_PORT:-18777}"
WEB_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
readonly WEB_ROOT
readonly APP_DIR="${WEB_ROOT}/apps/tv"
readonly APK_PATH="${APP_DIR}/android/app/build/outputs/apk/debug/app-debug.apk"
readonly SERVER_SCRIPT="${WEB_ROOT}/scripts/tv-emulator-fixture-server.mjs"

if ! command -v adb >/dev/null 2>&1; then
  printf 'adb is required for the TV emulator journey\n' >&2
  exit 2
fi
if ! command -v ffmpeg >/dev/null 2>&1; then
  printf 'ffmpeg is required to produce the deterministic journey stream\n' >&2
  exit 2
fi
if ! command -v curl >/dev/null 2>&1 || ! command -v jq >/dev/null 2>&1; then
  printf 'curl and jq are required for the TV emulator journey\n' >&2
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

journey_dir="$(mktemp -d)"
readonly journey_dir
fixture_pid=""
cleanup() {
  adb -s "${emulator_serial}" reverse --remove "tcp:${JOURNEY_PORT}" >/dev/null 2>&1 || true
  if [[ -n "${fixture_pid}" ]]; then
    kill "${fixture_pid}" >/dev/null 2>&1 || true
    wait "${fixture_pid}" >/dev/null 2>&1 || true
  fi
  rm -rf -- "${journey_dir}"
}
trap cleanup EXIT INT TERM

printf 'tv-emulator-journey: generating deterministic HLS fixture\n'
ffmpeg -hide_banner -loglevel error -y \
  -f lavfi -i 'testsrc2=size=640x360:rate=24' \
  -f lavfi -i 'sine=frequency=440:sample_rate=48000' \
  -t 60 -c:v libx264 -preset ultrafast -pix_fmt yuv420p -g 48 \
  -c:a aac -b:a 96k -f hls -hls_time 2 -hls_playlist_type vod \
  "${journey_dir}/media.m3u8"

node "${SERVER_SCRIPT}" "${JOURNEY_PORT}" "${journey_dir}" >"${journey_dir}/server.log" 2>&1 &
fixture_pid=$!
for _ in {1..50}; do
  if curl --fail --silent "http://127.0.0.1:${JOURNEY_PORT}/__journey" >/dev/null; then
    break
  fi
  if ! kill -0 "${fixture_pid}" 2>/dev/null; then
    cat "${journey_dir}/server.log" >&2
    exit 1
  fi
  sleep 0.1
done
curl --fail --silent "http://127.0.0.1:${JOURNEY_PORT}/__journey" >/dev/null

printf 'tv-emulator-journey: building embedded %s TV application\n' "${emulator_architecture}"
EXPO_PUBLIC_LOOMARR_URL="http://127.0.0.1:${JOURNEY_PORT}" \
  LOOMARR_ANDROID_ARCHITECTURES="${emulator_architecture}" \
  "${WEB_ROOT}/scripts/build-android-client.sh" tv

adb -s "${emulator_serial}" reverse "tcp:${JOURNEY_PORT}" "tcp:${JOURNEY_PORT}" >/dev/null
adb -s "${emulator_serial}" install -r "${APK_PATH}" >/dev/null
adb -s "${emulator_serial}" shell pm clear "${PACKAGE_ID}" >/dev/null
adb -s "${emulator_serial}" logcat -c
adb -s "${emulator_serial}" shell am start -W -n "${PACKAGE_ID}/.MainActivity" >/dev/null

dump_ui() {
  adb -s "${emulator_serial}" shell uiautomator dump /sdcard/loomarr-journey.xml >/dev/null
  adb -s "${emulator_serial}" exec-out cat /sdcard/loomarr-journey.xml >"${journey_dir}/window.xml"
}

wait_for_ui() {
  local description="$1"
  local expected="$2"
  local attempts="${3:-30}"
  for ((attempt = 1; attempt <= attempts; attempt += 1)); do
    dump_ui
    if grep -Fq "${expected}" "${journey_dir}/window.xml"; then
      printf 'tv-emulator-journey: observed %s\n' "${description}"
      return 0
    fi
    sleep 0.5
  done
  printf 'TV emulator never showed %s (%s)\n' "${description}" "${expected}" >&2
  cat "${journey_dir}/window.xml" >&2
  adb -s "${emulator_serial}" logcat -d -t 200 >&2
  return 1
}

assert_ui_absent() {
  local description="$1"
  local unexpected="$2"
  dump_ui
  if grep -Fq "${unexpected}" "${journey_dir}/window.xml"; then
    printf 'TV emulator still showed %s (%s)\n' "${description}" "${unexpected}" >&2
    cat "${journey_dir}/window.xml" >&2
    return 1
  fi
  printf 'tv-emulator-journey: observed %s\n' "${description}"
}

journey_state() {
  curl --fail --silent "http://127.0.0.1:${JOURNEY_PORT}/__journey"
}

wait_for_state() {
  local description="$1"
  local expression="$2"
  local attempts="${3:-40}"
  for ((attempt = 1; attempt <= attempts; attempt += 1)); do
    if journey_state | jq -e "${expression}" >/dev/null; then
      printf 'tv-emulator-journey: observed %s\n' "${description}"
      return 0
    fi
    sleep 0.25
  done
  printf 'TV emulator never reached %s (%s)\n' "${description}" "${expression}" >&2
  journey_state >&2
  return 1
}

key() {
  adb -s "${emulator_serial}" shell input keyevent "$1"
  sleep 0.35
}

# Fresh storage must exercise the real pair-start/poll/store transition before Watching appears.
wait_for_ui "fresh pairing code" "Pairing code 2468"
wait_for_state "pair start and approval polling" '.pairStarts == 1 and .pairPolls >= 1'
wait_for_ui "Watching home" "Open programme guide" 40
wait_for_state "initial Channel tune" '.playUrlChannels[-1] == "classic-animation"'

# A handled remote event reveals both pieces of Watching chrome. Neither may remain after the
# five-second inactivity window, so the journey's persistent home sentinel lives outside them.
key KEYCODE_DPAD_UP
wait_for_state "handled remote tune" '.playUrlChannels[-1] == "science-fiction"'
wait_for_ui "Watching identity after handled remote activity" "SCIENCE FICTION"
wait_for_ui "Watching playbar after handled remote activity" "The Neutral Zone"
sleep 6
assert_ui_absent "Watching identity dismissed after inactivity" "SCIENCE FICTION"
assert_ui_absent "Watching playbar dismissed after inactivity" "The Neutral Zone"
key KEYCODE_DPAD_RIGHT
sleep 1
assert_ui_absent "Watching identity remains dismissed after unhandled input" "SCIENCE FICTION"
assert_ui_absent "Watching playbar remains dismissed after unhandled input" "The Neutral Zone"
key KEYCODE_DPAD_DOWN
wait_for_state "return to initial Channel" '.playUrlChannels[-1] == "classic-animation"'
sleep 6

# Guide tune activity starts the same bounded chrome window. Reopen Guide afterward so hardware
# Back still proves it returns to the mounted Watching composition without inventing activity.
key KEYCODE_DPAD_CENTER
wait_for_ui "Guide" "Programme guide"
wait_for_ui "authoritative Guide programme" "Radioactive Man"
key KEYCODE_DPAD_DOWN
key KEYCODE_DPAD_CENTER
wait_for_state "Guide tune" '.playUrlChannels[-1] == "science-fiction"'
wait_for_ui "Watching identity after Guide tune activity" "SCIENCE FICTION"
wait_for_ui "Watching playbar after Guide tune activity" "The Neutral Zone"
sleep 6
assert_ui_absent "Watching identity dismissed after Guide tune inactivity" "SCIENCE FICTION"
assert_ui_absent "Watching playbar dismissed after Guide tune inactivity" "The Neutral Zone"
key KEYCODE_DPAD_DOWN
wait_for_state "return to initial Channel after Guide tune" '.playUrlChannels[-1] == "classic-animation"'
sleep 6
key KEYCODE_DPAD_CENTER
wait_for_ui "Guide before Back" "Programme guide"
key KEYCODE_BACK
wait_for_ui "Watching after Back" "Open programme guide"

# Left opens Surf; moving down and OK tunes the focused second Channel back into Watching.
key KEYCODE_DPAD_LEFT
wait_for_ui "Surf" "Channel surfer"
key KEYCODE_DPAD_DOWN
key KEYCODE_DPAD_CENTER
wait_for_state "Surf tune" '.playUrlChannels[-1] == "science-fiction"'
wait_for_ui "Watching identity after Surf tune activity" "SCIENCE FICTION"
wait_for_ui "Watching playbar after Surf tune activity" "The Neutral Zone"
sleep 6
assert_ui_absent "Watching identity dismissed after Surf tune inactivity" "SCIENCE FICTION"
assert_ui_absent "Watching playbar dismissed after Surf tune inactivity" "The Neutral Zone"

# Android numeric keys must build and commit the exact server-authored Channel number.
# Keep OK inside the production 1.2-second entry window; item 34 owns visual capture evidence.
key KEYCODE_7
key KEYCODE_7
printf 'tv-emulator-journey: observed number entry\n'
key KEYCODE_DPAD_CENTER
wait_for_state "number tune" '.playUrlChannels[-1] == "classic-animation"'
wait_for_ui "Watching after number tune" "Open programme guide"

# Backgrounding closes the event stream; returning refreshes and retunes the current Channel.
channel_loads_before="$(journey_state | jq -r '.channelLoads')"
play_urls_before="$(journey_state | jq -r '.playUrlChannels | length')"
event_disconnects_before="$(journey_state | jq -r '.eventDisconnects')"
key KEYCODE_HOME
wait_for_state "background event-stream release" ".eventDisconnects > ${event_disconnects_before}"
adb -s "${emulator_serial}" shell am start -W -n "${PACKAGE_ID}/.MainActivity" >/dev/null
wait_for_state "foreground catalog refresh" ".channelLoads > ${channel_loads_before}"
wait_for_state "foreground retune" ".playUrlChannels | length > ${play_urls_before} and .[-1] == \"classic-animation\""
wait_for_ui "Watching after foreground restoration" "Open programme guide"

# Surf's final remote-reachable action owns a confirmation step before revocation and local reset.
key KEYCODE_DPAD_LEFT
wait_for_ui "Surf before disconnect" "Channel surfer"
for _ in {1..8}; do key KEYCODE_DPAD_DOWN; done
key KEYCODE_DPAD_CENTER
wait_for_ui "disconnect confirmation" "Disconnect this device?"
key KEYCODE_DPAD_RIGHT
key KEYCODE_DPAD_CENTER
wait_for_state "device revocation" '.revocations == 1'
wait_for_ui "fresh-pair recovery after disconnect" "This device was disconnected"

printf 'tv-emulator-journey: pair, Watching, Guide, Back, Surf, tune, number entry, disconnect, and foreground restoration passed\n'
