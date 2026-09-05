#!/usr/bin/env bash

set -euo pipefail

readonly UNSIGNED_AAB="${1:-}"
readonly PRODUCER_MANIFEST="${2:-}"
readonly EMULATOR_SERIAL="${LOOMARR_TV_EMULATOR_SERIAL:-}"
readonly PACKAGE_ID="loomarr.media"
readonly JOURNEY_PORT="${LOOMARR_TV_JOURNEY_PORT:-18777}"
readonly DISCOVERY_NAME="Loomarr Emulator Acceptance"
readonly BUNDLETOOL_VERSION="1.18.1"
readonly BUNDLETOOL_SHA256="675786493983787ffa11550bdb7c0715679a44e1643f3ff980a529e9c822595c"

if [[ -z "${UNSIGNED_AAB}" || -z "${PRODUCER_MANIFEST}" ]]; then
	printf 'usage: LOOMARR_TV_EMULATOR_SERIAL=emulator-NNNN %s <unsigned.aab> <producer-manifest.json>\n' "$0" >&2
	exit 2
fi
if [[ ! "${EMULATOR_SERIAL}" =~ ^emulator-[0-9]+$ ]]; then
	printf 'LOOMARR_TV_EMULATOR_SERIAL must name one explicit emulator; physical devices are refused\n' >&2
	exit 2
fi

for command in adb curl dns-sd ffmpeg java jq keytool node sha256sum unzip; do
	command -v "${command}" >/dev/null 2>&1 || {
		printf '%s is required for Android release emulator acceptance\n' "${command}" >&2
		exit 2
	}
done
[[ -f "${UNSIGNED_AAB}" && -f "${PRODUCER_MANIFEST}" ]] || {
	printf 'the unsigned AAB and producer manifest must both exist\n' >&2
	exit 2
}
[[ "$(adb -s "${EMULATOR_SERIAL}" get-state)" == device ]] || {
	printf 'emulator %s is not ready\n' "${EMULATOR_SERIAL}" >&2
	exit 1
}

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
repo_root=$(cd -- "${script_dir}/.." && pwd)
evidence_dir="${ANDROID_EMULATOR_EVIDENCE_DIR:-${repo_root}/.artifacts/emulator-proof}"
temp_dir=$(mktemp -d)
fixture_pid=""
discovery_pid=""
cleanup() {
	adb -s "${EMULATOR_SERIAL}" reverse --remove "tcp:${JOURNEY_PORT}" >/dev/null 2>&1 || true
	if [[ -n "${discovery_pid}" ]]; then
		kill "${discovery_pid}" >/dev/null 2>&1 || true
		wait "${discovery_pid}" >/dev/null 2>&1 || true
	fi
	if [[ -n "${fixture_pid}" ]]; then
		kill "${fixture_pid}" >/dev/null 2>&1 || true
		wait "${fixture_pid}" >/dev/null 2>&1 || true
	fi
	rm -rf -- "${temp_dir}"
}
trap cleanup EXIT INT TERM

version_name=$(jq -er '.versionName' "${PRODUCER_MANIFEST}")
version_code=$(jq -er '.versionCode' "${PRODUCER_MANIFEST}")
expected_digest=$(jq -er '.aabSha256' "${PRODUCER_MANIFEST}")
actual_digest=$(sha256sum "${UNSIGNED_AAB}" | awk '{print $1}')
jq -e --arg name "${version_name}" --argjson code "${version_code}" \
	'.package == "loomarr.media" and .versionName == $name and .versionCode == $code and .signed == false' \
	"${PRODUCER_MANIFEST}" >/dev/null
[[ "${actual_digest}" == "${expected_digest}" ]] || {
	printf 'Android release emulator acceptance refuses an AAB that differs from producer evidence\n' >&2
	exit 1
}
"${repo_root}/web/scripts/verify-shield-aab.sh" \
	"${UNSIGNED_AAB}" "${version_name}" "${version_code}" "${temp_dir}/verified.json" unsigned >/dev/null
jq -e --slurpfile verified "${temp_dir}/verified.json" \
	'.package == $verified[0].package and .versionName == $verified[0].versionName and
	 .versionCode == $verified[0].versionCode and .aabSha256 == $verified[0].aabSha256' \
	"${PRODUCER_MANIFEST}" >/dev/null

bundletool="${LOOMARR_BUNDLETOOL_JAR:-${temp_dir}/bundletool.jar}"
if [[ -z "${LOOMARR_BUNDLETOOL_JAR:-}" ]]; then
	curl --fail --location --silent --show-error \
		"https://github.com/google/bundletool/releases/download/${BUNDLETOOL_VERSION}/bundletool-all-${BUNDLETOOL_VERSION}.jar" \
		--output "${bundletool}"
fi
[[ "$(sha256sum "${bundletool}" | awk '{print $1}')" == "${BUNDLETOOL_SHA256}" ]] || {
	printf 'bundletool %s failed its pinned SHA-256 check\n' "${BUNDLETOOL_VERSION}" >&2
	exit 1
}
[[ "$(java -jar "${bundletool}" version)" == "${BUNDLETOOL_VERSION}" ]] || {
	printf 'bundletool version mismatch\n' >&2
	exit 1
}

password=loomarr-emulator-acceptance
keystore="${temp_dir}/emulator.p12"
keytool -genkeypair -noprompt -storetype PKCS12 -keystore "${keystore}" \
	-storepass "${password}" -keypass "${password}" -alias loomarr-emulator \
	-keyalg RSA -keysize 2048 -validity 1 \
	-dname 'CN=Loomarr Emulator Acceptance,O=Loomarr Test,C=US' >/dev/null 2>&1
java -jar "${bundletool}" get-device-spec \
	--device-id="${EMULATOR_SERIAL}" --output="${temp_dir}/device.json" >/dev/null
java -jar "${bundletool}" build-apks \
	--bundle="${UNSIGNED_AAB}" --output="${temp_dir}/release.apks" \
	--device-spec="${temp_dir}/device.json" --ks="${keystore}" \
	--ks-key-alias=loomarr-emulator --ks-pass="pass:${password}" --key-pass="pass:${password}" >/dev/null

# A clean install is intentional acceptance state and applies only to the named emulator.
adb -s "${EMULATOR_SERIAL}" uninstall "${PACKAGE_ID}" >/dev/null 2>&1 || true
java -jar "${bundletool}" install-apks \
	--apks="${temp_dir}/release.apks" --device-id="${EMULATOR_SERIAL}" >/dev/null
installed_version=$(adb -s "${EMULATOR_SERIAL}" shell dumpsys package "${PACKAGE_ID}" |
	awk -F= '/versionName=/{gsub(/\r/, "", $2); print $2; exit}')
installed_code=$(adb -s "${EMULATOR_SERIAL}" shell dumpsys package "${PACKAGE_ID}" |
	awk '/versionCode=/{sub(/^.*versionCode=/, ""); sub(/ .*/, ""); print; exit}')
[[ "${installed_version}" == "${version_name}" && "${installed_code}" == "${version_code}" ]] || {
	printf 'installed package version does not match the accepted AAB\n' >&2
	exit 1
}

mkdir -p "${evidence_dir}"
ffmpeg -hide_banner -loglevel error -y \
	-f lavfi -i 'testsrc2=size=640x360:rate=24' \
	-f lavfi -i 'sine=frequency=440:sample_rate=48000' \
	-t 120 -c:v libx264 -preset ultrafast -pix_fmt yuv420p -g 48 \
	-c:a aac -b:a 96k -f hls -hls_time 2 -hls_playlist_type vod \
	"${temp_dir}/media.m3u8"

start_fixture() {
	node "${repo_root}/web/scripts/tv-emulator-fixture-server.mjs" \
		"${JOURNEY_PORT}" "${temp_dir}" 0.0.0.0 >"${temp_dir}/fixture.log" 2>&1 &
	fixture_pid=$!
	for _ in {1..50}; do
		if curl --fail --silent "http://127.0.0.1:${JOURNEY_PORT}/__journey" >/dev/null; then return 0; fi
		kill -0 "${fixture_pid}" 2>/dev/null || {
			cat "${temp_dir}/fixture.log" >&2
			return 1
		}
		sleep 0.1
	done
	printf 'fixture server did not become ready\n' >&2
	return 1
}

stop_fixture() {
	if [[ -n "${fixture_pid}" ]]; then
		kill "${fixture_pid}" >/dev/null 2>&1 || true
		wait "${fixture_pid}" >/dev/null 2>&1 || true
		fixture_pid=""
	fi
}

dump_ui() {
	adb -s "${EMULATOR_SERIAL}" shell uiautomator dump /sdcard/loomarr-release.xml >/dev/null
	adb -s "${EMULATOR_SERIAL}" exec-out cat /sdcard/loomarr-release.xml >"${temp_dir}/window.xml"
}

wait_for_ui() {
	local description=$1
	local expected=$2
	local attempts=${3:-40}
	for ((attempt = 1; attempt <= attempts; attempt += 1)); do
		dump_ui
		if grep -Fq "${expected}" "${temp_dir}/window.xml"; then
			printf 'android-emulator: observed %s\n' "${description}"
			return 0
		fi
		sleep 0.25
	done
	printf 'emulator never showed %s (%s)\n' "${description}" "${expected}" >&2
	cat "${temp_dir}/window.xml" >&2
	return 1
}

wait_for_state() {
	local description=$1
	local expression=$2
	local attempts=${3:-60}
	for ((attempt = 1; attempt <= attempts; attempt += 1)); do
		if curl --fail --silent "http://127.0.0.1:${JOURNEY_PORT}/__journey" | jq -e "${expression}" >/dev/null; then
			printf 'android-emulator: observed %s\n' "${description}"
			return 0
		fi
		sleep 0.25
	done
	printf 'emulator never reached %s\n' "${description}" >&2
	return 1
}

launch() {
	adb -s "${EMULATOR_SERIAL}" shell am force-stop "${PACKAGE_ID}"
	adb -s "${EMULATOR_SERIAL}" shell am start -n "${PACKAGE_ID}/.MainActivity" >/dev/null
}

open_launcher_surface() {
	adb -s "${EMULATOR_SERIAL}" shell am start \
		-a android.intent.action.ALL_APPS \
		-p com.google.android.apps.tv.launcherx >/dev/null
}

wait_for_launcher_identity() {
	local focused_window
	for _ in {1..40}; do
		dump_ui
		launcher_node=$(grep -oE '<node[^>]*package="com.google.android.apps.tv.launcherx"[^>]*content-desc="Loomarr"[^>]*>' "${temp_dir}/window.xml" | head -1 || true)
		if [[ -n "${launcher_node}" ]]; then
			focused_window=$(adb -s "${EMULATOR_SERIAL}" shell dumpsys window |
				awk '/mCurrentFocus=/{print; exit}')
			if [[ "${focused_window}" != *com.google.android.apps.tv.launcherx* ]]; then
				sleep 0.25
				continue
			fi
			printf 'android-emulator: observed exact Loomarr tile on launcher surface\n'
			return 0
		fi
		sleep 0.25
	done
	printf 'emulator never showed the exact Loomarr tile on the Android TV apps launcher\n' >&2
	cat "${temp_dir}/window.xml" >&2
	return 1
}

measure_launcher_identity() {
	local bounds x1 y1 x2 y2 width height
	bounds=$(printf '%s\n' "${launcher_node}" |
		sed -nE 's/.*bounds="\[([0-9]+),([0-9]+)\]\[([0-9]+),([0-9]+)\]".*/\1 \2 \3 \4/p')
	read -r x1 y1 x2 y2 <<<"${bounds}"
	width=$((x2 - x1))
	height=$((y2 - y1))
	[[ "${width}" -gt 0 && "${height}" -gt 0 ]] || {
		printf 'launcher Loomarr tile did not expose usable visible bounds\n' >&2
		return 1
	}
	ffmpeg -hide_banner -loglevel error -i "${evidence_dir}/launcher-surface.png" \
		-vf "crop=${width}:${height}:${x1}:${y1},signalstats,metadata=print:file=-" \
		-frames:v 1 -f null - 2>/dev/null >"${temp_dir}/launcher-stats.txt"
	launcher_min_luma=$(awk -F= '/lavfi.signalstats.YMIN=/{print $2 + 0; exit}' "${temp_dir}/launcher-stats.txt")
	launcher_max_luma=$(awk -F= '/lavfi.signalstats.YMAX=/{print $2 + 0; exit}' "${temp_dir}/launcher-stats.txt")
	launcher_saturation=$(awk -F= '/lavfi.signalstats.SATAVG=/{print $2 + 0; exit}' "${temp_dir}/launcher-stats.txt")
	awk -v minimum="${launcher_min_luma}" -v maximum="${launcher_max_luma}" -v saturation="${launcher_saturation}" \
		'BEGIN {exit !(minimum < 60 && maximum > 120 && saturation > 10)}' || {
		printf 'launcher Loomarr tile lacked the visible contrast and colour expected from an app identity\n' >&2
		return 1
	}
}

key() {
	adb -s "${EMULATOR_SERIAL}" shell input keyevent "$1"
	sleep 0.25
}

start_fixture
dns-sd -R "${DISCOVERY_NAME}" _loomarr._tcp local "${JOURNEY_PORT}" \
	protocol=1 scheme=http >"${temp_dir}/dns-sd.log" 2>&1 &
discovery_pid=$!
adb -s "${EMULATOR_SERIAL}" shell pm clear "${PACKAGE_ID}" >/dev/null
open_launcher_surface
wait_for_launcher_identity
adb -s "${EMULATOR_SERIAL}" exec-out screencap -p >"${evidence_dir}/launcher-surface.png"
measure_launcher_identity
launch
wait_for_ui "automatic LAN discovery" "Connect to ${DISCOVERY_NAME}"
dump_ui
grep -Eq "content-desc=\"Connect to ${DISCOVERY_NAME}[^\"]*\"[^>]*focused=\"true\"" "${temp_dir}/window.xml" || {
	printf 'discovered server was not the preferred TV focus target\n' >&2
	exit 1
}
adb -s "${EMULATOR_SERIAL}" exec-out screencap -p >"${evidence_dir}/lan-discovery.png"
key KEYCODE_DPAD_CENTER
wait_for_state "pairing through the discovered address" '.pairStarts == 1 and .pairPolls >= 1'
wait_for_state "playback after LAN discovery pairing" '.playUrlChannels[-1] == "classic-animation"'
printf 'android-emulator: LAN discovery selection, pairing, and playback passed\n'

kill "${discovery_pid}" >/dev/null 2>&1 || true
wait "${discovery_pid}" >/dev/null 2>&1 || true
discovery_pid=""
stop_fixture
sleep 1
start_fixture
adb -s "${EMULATOR_SERIAL}" reverse "tcp:${JOURNEY_PORT}" "tcp:${JOURNEY_PORT}" >/dev/null
adb -s "${EMULATOR_SERIAL}" shell pm clear "${PACKAGE_ID}" >/dev/null
launch
wait_for_ui "manual-entry fallback" "Enter address manually"
key KEYCODE_DPAD_CENTER
wait_for_ui "TV URL field" "Loomarr server address"
key KEYCODE_DPAD_UP
key KEYCODE_DPAD_CENTER
adb -s "${EMULATOR_SERIAL}" shell input text "http://127.0.0.1:${JOURNEY_PORT}"
key KEYCODE_ENTER
wait_for_ui "pairing code after manual entry" "Pairing code 2468"
wait_for_state "manual pair start and approval polling" '.pairStarts == 1 and .pairPolls >= 1'
wait_for_state "playback after manual pairing" '.playUrlChannels[-1] == "classic-animation"'

# Any D-pad activity makes Watching chrome visible; five seconds without input must hide it.
key KEYCODE_DPAD_DOWN
adb -s "${EMULATOR_SERIAL}" exec-out screencap -p >"${evidence_dir}/playbar-visible.png"
sleep 6
dump_ui
if grep -Eq 'Classic Animation|Science Fiction|Up next|tune.*channels' "${temp_dir}/window.xml"; then
	printf 'playback controls remained visible after the inactivity deadline\n' >&2
	exit 1
fi
adb -s "${EMULATOR_SERIAL}" exec-out screencap -p >"${evidence_dir}/playbar-hidden.png"
printf 'android-emulator: playback controls disappeared after remote inactivity\n'

# Screen recording is the durable visual proof for the process-dead launch identity. Recording
# begins over active video before the process is killed; the luminance transition therefore proves
# that the paired native player is covered while the animation runs, rather than merely recording a
# dark launcher before startup.
adb -s "${EMULATOR_SERIAL}" shell rm -f /sdcard/loomarr-paired-launch.mp4
adb -s "${EMULATOR_SERIAL}" shell screenrecord --time-limit 5 \
	/sdcard/loomarr-paired-launch.mp4 >/dev/null 2>&1 &
recording_pid=$!
sleep 0.25
adb -s "${EMULATOR_SERIAL}" shell am force-stop "${PACKAGE_ID}"
adb -s "${EMULATOR_SERIAL}" shell am start -n "${PACKAGE_ID}/.MainActivity" >/dev/null
wait "${recording_pid}" || true
adb -s "${EMULATOR_SERIAL}" pull /sdcard/loomarr-paired-launch.mp4 \
	"${evidence_dir}/paired-launch.mp4" >/dev/null
ffmpeg -hide_banner -loglevel error -y -i "${evidence_dir}/paired-launch.mp4" \
	-vf 'fps=10,scale=480:-1,tile=5x10' -frames:v 1 \
	"${evidence_dir}/paired-launch-contact-sheet.png"
ffmpeg -hide_banner -loglevel error -i "${evidence_dir}/paired-launch.mp4" \
	-vf 'fps=10,signalstats,metadata=print:file=-' -f null - 2>/dev/null \
	>"${temp_dir}/paired-launch-stats.txt"
read -r launch_min_luma launch_max_luma < <(
	awk -F= '/lavfi.signalstats.YAVG=/{
		value=$2 + 0
		if (count == 0 || value < minimum) minimum=value
		if (count == 0 || value > maximum) maximum=value
		count += 1
	} END {if (count > 0) print minimum, maximum}' "${temp_dir}/paired-launch-stats.txt"
)
awk -v minimum="${launch_min_luma}" -v maximum="${launch_max_luma}" \
	'BEGIN {exit !(minimum < 60 && maximum > 90)}' || {
	printf 'paired cold start did not visibly cover active video with the dark launch identity\n' >&2
	exit 1
}
wait_for_state "paired restart into playback" '.playUrlChannels | length >= 2'

avd_name=$(adb -s "${EMULATOR_SERIAL}" emu avd name | tr -d '\r' | head -1)
jq -n \
	--arg aabSha256 "${actual_digest}" \
	--arg avd "${avd_name}" \
	--arg emulatorSerial "${EMULATOR_SERIAL}" \
	--argjson launcherMaxLuma "${launcher_max_luma}" \
	--argjson launcherMinLuma "${launcher_min_luma}" \
	--argjson launcherSaturation "${launcher_saturation}" \
	--arg producerCommit "$(jq -r '.commit // ""' "${PRODUCER_MANIFEST}")" \
	--arg producerRunId "$(jq -r '.producerRunId // ""' "${PRODUCER_MANIFEST}")" \
	--arg versionName "${version_name}" \
	--argjson pairedLaunchMaxLuma "${launch_max_luma}" \
	--argjson pairedLaunchMinLuma "${launch_min_luma}" \
	--argjson versionCode "${version_code}" \
	'{aabSha256: $aabSha256, avd: $avd, emulatorSerial: $emulatorSerial,
	 producerCommit: $producerCommit, producerRunId: $producerRunId,
	 versionCode: $versionCode, versionName: $versionName,
	 automaticLanDiscovery: true, discoveredAddressPairing: true, manualAddressFallback: true,
	 launcherSurfaceObserved: true, launcherSurfaceScreenshot: "launcher-surface.png",
	 launcherTileAccessibleName: "Loomarr", launcherTileMaxLuma: $launcherMaxLuma,
	 launcherTileMinLuma: $launcherMinLuma, launcherTileSaturation: $launcherSaturation,
	 pairedColdLaunchMaxLuma: $pairedLaunchMaxLuma, pairedColdLaunchMinLuma: $pairedLaunchMinLuma,
	 pairedColdLaunchRecording: "paired-launch.mp4", pairedColdLaunchVideoCovered: true,
	 playbarAutoHide: true}' \
	>"${evidence_dir}/acceptance.json"

printf 'android-emulator: exact AAB %s passed automatic discovery, manual fallback, pairing, playback, playbar, and paired-restart acceptance\n' \
	"${actual_digest}"
