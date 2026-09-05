#!/usr/bin/env bash

set -euo pipefail

root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
script="${root}/scripts/test-android-release-emulator.sh"

launcher_open=$(grep -nFx 'open_launcher_surface' "${script}" | head -1 | cut -d: -f1)
launcher_observed=$(grep -nFx 'wait_for_launcher_identity' "${script}" | head -1 | cut -d: -f1)
launcher_capture=$(grep -nF ">\"\${evidence_dir}/launcher-surface.png\"" "${script}" | head -1 | cut -d: -f1)
launcher_measured=$(grep -nFx 'measure_launcher_identity' "${script}" | head -1 | cut -d: -f1)
activity_launch=$(grep -nFx 'launch' "${script}" | head -1 | cut -d: -f1)

[[ -n "${launcher_open}" && -n "${launcher_observed}" && -n "${launcher_capture}" && -n "${launcher_measured}" && -n "${activity_launch}" ]] || {
	printf 'release emulator harness must open, observe, capture, and measure the apps launcher before MainActivity\n' >&2
	exit 1
}
if grep -Fq 'android.intent.category.LEANBACK_LAUNCHER' "${script}"; then
	printf 'release emulator harness must not use LEANBACK_LAUNCHER to open the apps launcher\n' >&2
	exit 1
fi
if grep -Fq 'KEYCODE_HOME' "${script}"; then
	printf 'release emulator harness must open the observable apps grid, not an indeterminate HOME surface\n' >&2
	exit 1
fi
grep -Fq -- '-a android.intent.action.ALL_APPS' "${script}" || {
	printf 'release emulator harness must open the Android TV apps launcher\n' >&2
	exit 1
}
grep -Fq -- '-p com.google.android.apps.tv.launcherx' "${script}" || {
	printf 'release emulator harness must address the pinned Android TV apps launcher explicitly\n' >&2
	exit 1
}
grep -Fq 'content-desc="Loomarr"' "${script}" || {
	printf 'release emulator harness must require the exact accessible Loomarr tile name\n' >&2
	exit 1
}
grep -Fq 'mCurrentFocus=' "${script}" || {
	printf 'release emulator harness must prove the launcher, not Loomarr, owns the visible surface\n' >&2
	exit 1
}
[[ "${launcher_open}" -lt "${launcher_observed}" && "${launcher_observed}" -lt "${launcher_capture}" && "${launcher_capture}" -lt "${launcher_measured}" && "${launcher_measured}" -lt "${activity_launch}" ]] || {
  printf 'launcher identity observation, capture, and pixel measurement must precede MainActivity\n' >&2
  exit 1
}
grep -Fq "launcherTileSaturation: \$launcherSaturation" "${script}" || {
	printf 'release emulator evidence manifest must retain launcher screenshot and pixel measurements\n' >&2
	exit 1
}

echo 'test-android-release-emulator-contract-test: ok'
