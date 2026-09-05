#!/usr/bin/env bash

set -euo pipefail

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
temp_dir=$(mktemp -d)
trap 'rm -r -- "$temp_dir"' EXIT

repo_root=$(cd -- "$script_dir/.." && pwd)
release_identity="$repo_root/web/apps/tv/android-release.json"
version_name=$(jq -er '.versionName' "$release_identity")
version_code=$("$script_dir/android-version-code.sh" "$version_name")
output_dir=${ANDROID_CI_OUTPUT_DIR:-"$repo_root/.artifacts/android-ci"}

password=loomarr-ephemeral-release-test
keystore="$temp_dir/upload.p12"
keytool \
	-genkeypair -noprompt \
	-storetype PKCS12 \
	-keystore "$keystore" \
	-storepass "$password" \
	-keypass "$password" \
	-alias loomarr-upload \
	-keyalg RSA \
	-keysize 4096 \
	-validity 1 \
	-dname 'CN=Loomarr Ephemeral Release Test,O=Loomarr Test,C=US' >/dev/null 2>&1
fingerprint=$(
	LC_ALL=C keytool -list -v -keystore "$keystore" -storepass "$password" -alias loomarr-upload |
		awk -F': ' '/SHA256:/{print $2; exit}'
)
LOOMARR_ANDROID_VERSION_NAME=$version_name \
	LOOMARR_ANDROID_VERSION_CODE=$version_code \
	LOOMARR_ANDROID_KEYSTORE_PATH=$keystore \
	LOOMARR_ANDROID_KEYSTORE_PASSWORD=$password \
	LOOMARR_ANDROID_KEY_ALIAS=loomarr-upload \
	LOOMARR_ANDROID_KEY_PASSWORD=$password \
	LOOMARR_ANDROID_UPLOAD_CERT_SHA256=$fingerprint \
	ANDROID_RELEASE_OUTPUT_DIR="$temp_dir/signed" \
	"$script_dir/build-android-beta.sh"

jq -e --arg name "$version_name" --argjson code "$version_code" \
	'.versionName == $name and .versionCode == $code and .signed == true' \
	"$temp_dir/signed/loomarr-tv-$version_name-$version_code.json" >/dev/null

mkdir -p "$output_dir"
unsigned="$output_dir/loomarr-tv-$version_name-$version_code-unsigned.aab"
evidence="${unsigned%.aab}.json"
cp "$temp_dir/signed/loomarr-tv-$version_name-$version_code.aab" "$unsigned"
zip -q -d "$unsigned" 'META-INF/MANIFEST.MF' 'META-INF/*.SF' 'META-INF/*.RSA' 'META-INF/*.DSA' 'META-INF/*.EC'
"$repo_root/web/scripts/verify-shield-aab.sh" \
	"$unsigned" "$version_name" "$version_code" "$evidence" unsigned

jq -e \
	--argjson code "$version_code" \
	--arg name "$version_name" \
	'.package == "loomarr.media" and .versionName == $name and .signed == false and
	 .versionCode == $code and .nativeLibraries > 0 and
	 .abis == ["arm64-v8a", "armeabi-v7a", "x86", "x86_64"] and
	 .elfLoadAlignmentBytes == 16384 and
	 .elfLoadAlignmentAbis == ["arm64-v8a", "x86_64"] and
	 .sourceEntry == "web/apps/tv/index.ts"' \
	"$evidence" >/dev/null

producer_evidence="$temp_dir/producer.json"
jq \
	--arg producerRunId "${GITHUB_RUN_ID:-1}" \
	--arg producerEvent "${GITHUB_EVENT_NAME:-merge_group}" \
	--arg producerWorkflowRef "${GITHUB_WORKFLOW_REF:-loomarr/loomarr/.github/workflows/ci.yml@refs/heads/main}" \
	'. + {producerRunId: $producerRunId, producerEvent: $producerEvent,
	 producerWorkflowRef: $producerWorkflowRef}' \
	"$evidence" >"$producer_evidence"
LOOMARR_ANDROID_KEYSTORE_PATH=$keystore \
	LOOMARR_ANDROID_KEYSTORE_PASSWORD=$password \
	LOOMARR_ANDROID_KEY_ALIAS=loomarr-upload \
	LOOMARR_ANDROID_KEY_PASSWORD=$password \
	LOOMARR_ANDROID_UPLOAD_CERT_SHA256=$fingerprint \
	"$script_dir/sign-android-ci-artifact.sh" \
	"$unsigned" "$producer_evidence" "$temp_dir/promoted"
jq -e --arg unsignedSha "$(jq -er '.aabSha256' "$evidence")" \
	'.signed == true and .unsignedAabSha256 == $unsignedSha and .producerRunId != null' \
	"$temp_dir/promoted/loomarr-tv-$version_name-$version_code.json" >/dev/null

echo "android release: retained verified unsigned React Native AAB at $unsigned"
