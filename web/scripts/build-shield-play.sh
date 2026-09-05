#!/usr/bin/env bash
# Build and inspect the permanent-identity React Native Android TV App Bundle.
set -euo pipefail

readonly VERSION_NAME="${1:-}"
WEB_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
readonly WEB_ROOT
REPO_ROOT="$(cd "${WEB_ROOT}/.." && pwd)"
readonly REPO_ROOT
readonly APP_DIR="${WEB_ROOT}/apps/tv"
readonly OUTPUT_DIR="${ANDROID_RELEASE_OUTPUT_DIR:-${REPO_ROOT}/.artifacts/android-release}"
readonly GRADLE_HEAP="${LOOMARR_ANDROID_GRADLE_HEAP:-1280m}"
readonly ARCHITECTURES="armeabi-v7a,arm64-v8a,x86,x86_64"
readonly NATIVE_JOBS="${LOOMARR_ANDROID_NATIVE_JOBS:-1}"

if [[ -z "${VERSION_NAME}" ]]; then
  printf 'usage: %s <major.minor.patch[-beta.N|-rc.N]>\n' "$0" >&2
  exit 2
fi
if [[ -z "${ANDROID_HOME:-}" ]]; then
  printf 'ANDROID_HOME must point to the Android SDK\n' >&2
  exit 2
fi

"${REPO_ROOT}/scripts/check-android-release-env.sh"

[[ "$(node -p "require('${APP_DIR}/package.json').main")" == "index.ts" ]] || {
  printf 'Shield production entry must remain apps/tv/index.ts\n' >&2
  exit 1
}
grep -Fq 'from "./src/app"' "${APP_DIR}/index.ts" || {
  printf 'Shield production entry must register src/app\n' >&2
  exit 1
}

KEYSTORE_PATH="$(cd "$(dirname "${LOOMARR_ANDROID_KEYSTORE_PATH}")" && pwd)/$(basename "${LOOMARR_ANDROID_KEYSTORE_PATH}")"
readonly KEYSTORE_PATH
EXPO_PACKAGE_JSON="$(cd "${APP_DIR}" && node -p "require.resolve('expo/package.json')")"
readonly EXPO_PACKAGE_JSON
readonly EXPO_TEMPLATE="${EXPO_PACKAGE_JSON%/package.json}/template.tgz"
[[ -f "${EXPO_TEMPLATE}" ]] || {
  printf 'the pinned Expo package does not contain its native template: %s\n' "${EXPO_TEMPLATE}" >&2
  exit 2
}

export LOOMARR_SHIELD_RELEASE_CHANNEL=play
export LOOMARR_ANDROID_KEYSTORE_PATH="${KEYSTORE_PATH}"
export EXPO_PUBLIC_LOOMARR_CLIENT_VERSION="${VERSION_NAME}"

(
  cd "${WEB_ROOT}"
  CI=1 EXPO_TV=1 pnpm --filter @loomarr/tv exec expo prebuild --clean --platform android \
    --no-install --template "${EXPO_TEMPLATE}"
)

(
  cd "${APP_DIR}/android"
  CMAKE_BUILD_PARALLEL_LEVEL="${NATIVE_JOBS}" NODE_ENV=production EXPO_TV=1 \
    ./gradlew bundleRelease \
      --no-daemon \
      --build-cache \
      --max-workers=1 \
      "-Dorg.gradle.jvmargs=-Xmx${GRADLE_HEAP}" \
      -Pkotlin.compiler.execution.strategy=in-process \
      "-PreactNativeArchitectures=${ARCHITECTURES}"
)

readonly GENERATED_AAB="${APP_DIR}/android/app/build/outputs/bundle/release/app-release.aab"
[[ -f "${GENERATED_AAB}" ]] || {
  printf 'Gradle did not produce the expected Shield bundle: %s\n' "${GENERATED_AAB}" >&2
  exit 1
}
mkdir -p "${OUTPUT_DIR}"
readonly ARTIFACT_STEM="loomarr-tv-${VERSION_NAME}-${LOOMARR_ANDROID_VERSION_CODE}"
readonly OUTPUT_AAB="${OUTPUT_DIR}/${ARTIFACT_STEM}.aab"
readonly EVIDENCE_PATH="${OUTPUT_DIR}/${ARTIFACT_STEM}.json"
cp "${GENERATED_AAB}" "${OUTPUT_AAB}"
"${WEB_ROOT}/scripts/verify-shield-aab.sh" \
  "${OUTPUT_AAB}" "${VERSION_NAME}" "${LOOMARR_ANDROID_VERSION_CODE}" "${EVIDENCE_PATH}"
