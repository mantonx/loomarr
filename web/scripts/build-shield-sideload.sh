#!/usr/bin/env bash
# Build and verify the permanent-identity React Native Android TV sideload.
set -euo pipefail

readonly VERSION_NAME="${1:-}"
WEB_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
readonly WEB_ROOT
REPO_ROOT="$(cd "${WEB_ROOT}/.." && pwd)"
readonly REPO_ROOT
readonly APP_DIR="${WEB_ROOT}/apps/tv"
readonly OUTPUT_DIR="${LOOMARR_SHIELD_OUTPUT_DIR:-${REPO_ROOT}/.artifacts/shield-sideload}"
readonly GRADLE_HEAP="${LOOMARR_ANDROID_GRADLE_HEAP:-1280m}"
readonly ARCHITECTURES="${LOOMARR_ANDROID_ARCHITECTURES:-arm64-v8a}"
readonly NATIVE_JOBS="${LOOMARR_ANDROID_NATIVE_JOBS:-1}"

if [[ -z "${VERSION_NAME}" ]]; then
  printf 'usage: %s <major.minor.patch[-beta.N|-rc.N]>\n' "$0" >&2
  exit 2
fi
if [[ -z "${ANDROID_HOME:-}" ]]; then
  printf 'ANDROID_HOME must point to the Android SDK\n' >&2
  exit 2
fi
if [[ "${ARCHITECTURES}" != "arm64-v8a" ]]; then
  printf 'Shield sideload supports only the Shield arm64-v8a ABI\n' >&2
  exit 2
fi
for name in LOOMARR_ANDROID_KEYSTORE_PATH LOOMARR_ANDROID_KEYSTORE_PASSWORD \
  LOOMARR_ANDROID_KEY_ALIAS LOOMARR_ANDROID_KEY_PASSWORD; do
  if [[ -z "${!name:-}" ]]; then
    printf 'Shield sideload requires %s\n' "${name}" >&2
    exit 2
  fi
done
if [[ ! -f "${LOOMARR_ANDROID_KEYSTORE_PATH}" ]]; then
  printf 'Shield signing keystore does not exist: %s\n' "${LOOMARR_ANDROID_KEYSTORE_PATH}" >&2
  exit 2
fi
KEYSTORE_PATH="$(cd "$(dirname "${LOOMARR_ANDROID_KEYSTORE_PATH}")" && pwd)/$(basename "${LOOMARR_ANDROID_KEYSTORE_PATH}")"
readonly KEYSTORE_PATH
VERSION_CODE="$("${REPO_ROOT}/scripts/android-version-code.sh" "${VERSION_NAME}")"
readonly VERSION_CODE

[[ "$(node -p "require('${APP_DIR}/package.json').main")" == "index.ts" ]] || {
  printf 'Shield production entry must remain apps/tv/index.ts\n' >&2
  exit 1
}
grep -Fq 'from "./src/app"' "${APP_DIR}/index.ts" || {
  printf 'Shield production entry must register src/app\n' >&2
  exit 1
}

keytool -list \
  -keystore "${KEYSTORE_PATH}" \
  -storepass:env LOOMARR_ANDROID_KEYSTORE_PASSWORD \
  -alias "${LOOMARR_ANDROID_KEY_ALIAS}" >/dev/null

EXPO_PACKAGE_JSON="$(cd "${APP_DIR}" && node -p "require.resolve('expo/package.json')")"
readonly EXPO_PACKAGE_JSON
readonly EXPO_TEMPLATE="${EXPO_PACKAGE_JSON%/package.json}/template.tgz"
[[ -f "${EXPO_TEMPLATE}" ]] || {
  printf 'the pinned Expo package does not contain its native template: %s\n' "${EXPO_TEMPLATE}" >&2
  exit 2
}

export LOOMARR_SHIELD_RELEASE_CHANNEL=sideload
export LOOMARR_ANDROID_VERSION_NAME="${VERSION_NAME}"
export LOOMARR_ANDROID_VERSION_CODE="${VERSION_CODE}"
export LOOMARR_ANDROID_KEYSTORE_PATH="${KEYSTORE_PATH}"
# The TV footer and diagnostic identity must describe the artifact actually installed, not the
# isolated prototype version from app.json. Expo inlines EXPO_PUBLIC values into the production
# JavaScript bundle, so derive this from the same validated name that owns Android versionName.
export EXPO_PUBLIC_LOOMARR_CLIENT_VERSION="${VERSION_NAME}"

(
  cd "${WEB_ROOT}"
  CI=1 EXPO_TV=1 pnpm --filter @loomarr/tv exec expo prebuild --clean --platform android \
    --no-install --template "${EXPO_TEMPLATE}"
)

(
  cd "${APP_DIR}/android"
  CMAKE_BUILD_PARALLEL_LEVEL="${NATIVE_JOBS}" NODE_ENV=production EXPO_TV=1 \
    ./gradlew assembleRelease \
      --no-daemon \
      --max-workers=1 \
      "-Dorg.gradle.jvmargs=-Xmx${GRADLE_HEAP}" \
      -Pkotlin.compiler.execution.strategy=in-process \
      "-PreactNativeArchitectures=${ARCHITECTURES}"
)

readonly GENERATED_APK="${APP_DIR}/android/app/build/outputs/apk/release/app-release.apk"
[[ -f "${GENERATED_APK}" ]] || {
  printf 'Gradle did not produce the expected Shield APK: %s\n' "${GENERATED_APK}" >&2
  exit 1
}
mkdir -p "${OUTPUT_DIR}"
readonly OUTPUT_APK="${OUTPUT_DIR}/loomarr-shield-${VERSION_NAME}-arm64-v8a.apk"
readonly EVIDENCE_PATH="${OUTPUT_APK%.apk}.json"
cp "${GENERATED_APK}" "${OUTPUT_APK}"
"${WEB_ROOT}/scripts/verify-shield-apk.sh" \
  "${OUTPUT_APK}" "${VERSION_NAME}" "${VERSION_CODE}" "${EVIDENCE_PATH}"
