#!/usr/bin/env sh
# Report what heavyweight local processes are running, and refuse to be ignored quietly.
#
# ⚠ This exists because an agent session took the machine down by ACCUMULATION, not by any single
# command: Gradle and Kotlin daemons across dozens of builds, an Android emulator holding 2GB,
# scrcpy, an Air backend, a Vite dev server and a browser — each individually reasonable.
#
# Run it before starting anything heavy (`make android`, an emulator, a full gate). It only reports;
# it never kills anything, because deciding what to stop is the developer's call and a dev server
# this script does not understand may be load-bearing.

set -eu

printf '%s\n' "--- local load ---"

if [ -r /proc/loadavg ]; then
	load="$(cut -d' ' -f1 /proc/loadavg)"
	cores="$(nproc 2>/dev/null || echo 1)"
	printf 'load: %s over %s cores\n' "$load" "$cores"
fi

free -g 2>/dev/null | awk '/^Mem:/{printf "ram:  %sG used, %sG available of %sG\n", $3, $7, $2}'

printf '\n%s\n' "--- heavy processes ---"

count_of() {
	# shellcheck disable=SC2009 # pgrep -c is not portable enough for the patterns used here.
	pgrep -af "$1" 2>/dev/null | grep -vc 'dev-load-check' || true
}

report() {
	n="$(count_of "$2")"
	[ "${n:-0}" -gt 0 ] && printf '%-22s %s\n' "$1" "$n"
	return 0
}

report "gradle/kotlin daemons" "GradleDaemon|KotlinCompileDaemon|GradleWrapperMain"
report "android emulator" "qemu-system|emulator64|emulator -avd"
report "scrcpy" "scrcpy"
report "air (backend)" "tmp/loomarr-dev|air -c|air$"
report "vite (frontend)" "vite"
report "chrome/chromium" "chrome --remote-debugging|chromium --remote-debugging"

printf '\n%s\n' "--- if this looks heavy ---"
cat <<'EOF'
  gradle daemons   ./gradlew --stop          (from a generated native project)
  emulator         adb -s emulator-5560 emu kill
  scrcpy           pkill -f scrcpy
Leave dev servers alone unless you started them — see the dont-thrash-on-dev-processes note.
EOF
