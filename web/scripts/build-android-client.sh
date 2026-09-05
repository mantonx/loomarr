#!/usr/bin/env bash
# Build one prototype Android client without letting Gradle's native process
# tree exhaust a Linux development workstation.
set -euo pipefail

readonly APP_NAME="${1:-mobile}"
readonly SCOPE_MARKER="${2:-}"
WEB_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
readonly WEB_ROOT
readonly SCRIPT_PATH="${WEB_ROOT}/scripts/build-android-client.sh"
readonly APP_DIR="${WEB_ROOT}/apps/${APP_NAME}"
readonly MEMORY_HIGH="${LOOMARR_ANDROID_MEMORY_HIGH:-3750M}"
readonly MEMORY_MAX="${LOOMARR_ANDROID_MEMORY_MAX:-4G}"
readonly GRADLE_HEAP="${LOOMARR_ANDROID_GRADLE_HEAP:-1280m}"
readonly ARCHITECTURES="${LOOMARR_ANDROID_ARCHITECTURES:-arm64-v8a}"
readonly CPUSET="${LOOMARR_ANDROID_CPUSET:-0-3}"
readonly NATIVE_JOBS="${LOOMARR_ANDROID_NATIVE_JOBS:-1}"

if [[ "${APP_NAME}" != "mobile" && "${APP_NAME}" != "tv" ]]; then
  printf 'usage: %s [mobile|tv]\n' "$0" >&2
  exit 2
fi
if [[ "${APP_NAME}" == "tv" ]]; then
  readonly ENTRY_FILE="index.ts"
else
  readonly ENTRY_FILE="node_modules/expo-router/entry.js"
fi
if [[ -z "${ANDROID_HOME:-}" ]]; then
  printf 'ANDROID_HOME must point to the Android SDK\n' >&2
  exit 2
fi

EXPO_PACKAGE_JSON="$(
  cd "${APP_DIR}"
  node -p "require.resolve('expo/package.json')"
)"
readonly EXPO_PACKAGE_JSON
readonly EXPO_TEMPLATE="${EXPO_PACKAGE_JSON%/package.json}/template.tgz"
if [[ ! -f "${EXPO_TEMPLATE}" ]]; then
  printf 'the pinned Expo package does not contain its native template: %s\n' "${EXPO_TEMPLATE}" >&2
  exit 2
fi

if [[ ! -x "${APP_DIR}/android/gradlew" ]]; then
  (
    cd "${WEB_ROOT}"
    pnpm --filter "@loomarr/${APP_NAME}" exec expo prebuild --platform android --no-install \
      --template "${EXPO_TEMPLATE}"
  )
fi

if [[ "${SCOPE_MARKER}" != "--inside-memory-scope" ]] \
  && command -v systemd-run >/dev/null 2>&1 \
  && systemctl --user show-environment >/dev/null 2>&1; then
  exec systemd-run --user --scope --quiet \
    -p MemoryAccounting=yes \
    -p "MemoryHigh=${MEMORY_HIGH}" \
    -p "MemoryMax=${MEMORY_MAX}" \
    /usr/bin/env \
    LOOMARR_ANDROID_GRADLE_HEAP="${GRADLE_HEAP}" \
    LOOMARR_ANDROID_ARCHITECTURES="${ARCHITECTURES}" \
    LOOMARR_ANDROID_CPUSET="${CPUSET}" \
    LOOMARR_ANDROID_NATIVE_JOBS="${NATIVE_JOBS}" \
    ANDROID_HOME="${ANDROID_HOME}" \
    "${SCRIPT_PATH}" "${APP_NAME}" --inside-memory-scope
fi

if [[ "${SCOPE_MARKER}" != "--inside-memory-scope" ]]; then
  printf 'warning: user systemd unavailable; native build has worker limits but no memory ceiling\n' >&2
fi

# `assembleDebug` normally expects Metro and therefore produces an APK that opens to React Native's
# red "Unable to load script" screen when installed on a remote Shield. This target is the physical
# device proof, so embed the production JS/assets explicitly while retaining debug-native signing
# and the faster incremental native build. Keep this below the scope handoff: otherwise the outer
# and inner script processes both bundle, and the first Metro process escapes the memory limit.
mkdir -p "${APP_DIR}/android/app/src/main/assets" "${APP_DIR}/android/app/src/main/res"
(
  cd "${APP_DIR}"
  NODE_ENV=production pnpm exec expo export:embed \
    --platform android \
    --dev false \
    --entry-file "${ENTRY_FILE}" \
    --bundle-output android/app/src/main/assets/index.android.bundle \
    --assets-dest android/app/src/main/res \
    --reset-cache \
    --max-workers 1
)

cd "${APP_DIR}/android"
gradle_command=(./gradlew assembleDebug
  --no-daemon \
  --max-workers=1 \
  "-Dorg.gradle.jvmargs=-Xmx${GRADLE_HEAP}" \
  -Pkotlin.compiler.execution.strategy=in-process \
  "-PreactNativeArchitectures=${ARCHITECTURES}")
if command -v taskset >/dev/null 2>&1; then
  CMAKE_BUILD_PARALLEL_LEVEL="${NATIVE_JOBS}" NODE_ENV=development \
    taskset --cpu-list "${CPUSET}" "${gradle_command[@]}"
else
  CMAKE_BUILD_PARALLEL_LEVEL="${NATIVE_JOBS}" NODE_ENV=development "${gradle_command[@]}"
fi
