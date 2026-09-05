#!/usr/bin/env bash

set -euo pipefail

if (($# != 2)); then
	echo 'usage: publish-android-beta.sh <signed.aab> <release-manifest.json>' >&2
	exit 2
fi

aab=$1
manifest=$2
: "${GOOGLE_PLAY_SERVICE_ACCOUNT_JSON_PATH:?GOOGLE_PLAY_SERVICE_ACCOUNT_JSON_PATH is required}"
: "${ANDROID_RELEASE_TRACK:?ANDROID_RELEASE_TRACK is required}"

if [[ ! -f "$aab" || ! -f "$manifest" || ! -f "$GOOGLE_PLAY_SERVICE_ACCOUNT_JSON_PATH" ]]; then
	echo 'android publish: bundle, release manifest, and service-account JSON must be files' >&2
	exit 1
fi
if [[ "$ANDROID_RELEASE_TRACK" != internal ]]; then
	echo 'android publish: only the internal track is allowed' >&2
	exit 1
fi

package=$(jq -er '.package' "$manifest")
version_name=$(jq -er '.versionName' "$manifest")
version_code=$(jq -er '.versionCode' "$manifest")
if [[ "$package" != loomarr.media ]]; then
	echo 'android publish: release manifest has the wrong package identity' >&2
	exit 1
fi
manifest_sha=$(jq -er '.aabSha256' "$manifest")
actual_sha=$(sha256sum "$aab" | awk '{print $1}')
if [[ "$manifest_sha" != "$actual_sha" ]]; then
	echo 'android publish: bundle digest does not match the release manifest' >&2
	exit 1
fi

client_email=$(jq -er '.client_email' "$GOOGLE_PLAY_SERVICE_ACCOUNT_JSON_PATH")
token_uri=$(jq -er '.token_uri' "$GOOGLE_PLAY_SERVICE_ACCOUNT_JSON_PATH")
if [[ "$token_uri" != https://oauth2.googleapis.com/token ]]; then
	echo 'android publish: service-account token endpoint is not the Google OAuth endpoint' >&2
	exit 1
fi

temp_dir=$(mktemp -d)
trap 'rm -r -- "$temp_dir"' EXIT
private_key="$temp_dir/service-account.pem"
jq -er '.private_key' "$GOOGLE_PLAY_SERVICE_ACCOUNT_JSON_PATH" >"$private_key"
chmod 0600 "$private_key"

base64url() {
	openssl base64 -A | tr '+/' '-_' | tr -d '='
}

issued_at=$(date +%s)
expires_at=$((issued_at + 3600))
header=$(printf '%s' '{"alg":"RS256","typ":"JWT"}' | base64url)
claims=$(
	jq -cn \
		--arg iss "$client_email" \
		--arg scope 'https://www.googleapis.com/auth/androidpublisher' \
		--arg aud "$token_uri" \
		--argjson iat "$issued_at" \
		--argjson exp "$expires_at" \
		'{iss:$iss,scope:$scope,aud:$aud,iat:$iat,exp:$exp}' |
		base64url
)
unsigned="$header.$claims"
signature=$(printf '%s' "$unsigned" | openssl dgst -sha256 -sign "$private_key" | base64url)
assertion="$unsigned.$signature"

token_response=$(
	curl --fail-with-body --silent --show-error \
		-X POST "$token_uri" \
		-H 'Content-Type: application/x-www-form-urlencoded' \
		--data-urlencode 'grant_type=urn:ietf:params:oauth:grant-type:jwt-bearer' \
		--data-urlencode "assertion=$assertion"
)
access_token=$(jq -er '.access_token' <<<"$token_response")
authorization="Authorization: Bearer $access_token"
api_root="https://androidpublisher.googleapis.com/androidpublisher/v3/applications/$package/edits"
upload_root="https://androidpublisher.googleapis.com/upload/androidpublisher/v3/applications/$package/edits"

edit_response=$(
	curl --fail-with-body --silent --show-error \
		-X POST "$api_root" \
		-H "$authorization" \
		-H 'Content-Type: application/json' \
		-d '{}'
)
edit_id=$(jq -er '.id' <<<"$edit_response")

bundle_response=$(
	curl --fail-with-body --silent --show-error \
		-X POST "$upload_root/$edit_id/bundles?uploadType=media" \
		-H "$authorization" \
		-H 'Content-Type: application/octet-stream' \
		--data-binary "@$aab"
)
uploaded_code=$(jq -er '.versionCode' <<<"$bundle_response")
if [[ "$uploaded_code" != "$version_code" ]]; then
	echo "android publish: Play accepted version code $uploaded_code, expected $version_code" >&2
	exit 1
fi

track_body=$(
	jq -cn \
		--arg track "$ANDROID_RELEASE_TRACK" \
		--arg name "$version_name" \
		--arg code "$version_code" \
		'{track:$track,releases:[{name:$name,status:"completed",versionCodes:[$code]}]}'
)
curl --fail-with-body --silent --show-error \
	-X PUT "$api_root/$edit_id/tracks/$ANDROID_RELEASE_TRACK" \
	-H "$authorization" \
	-H 'Content-Type: application/json' \
	-d "$track_body" >/dev/null
commit_response=$(
	curl --fail-with-body --silent --show-error \
		-X POST "$api_root/$edit_id:commit" \
		-H "$authorization" \
		-H 'Content-Type: application/json' \
		-d '{}'
)
committed_id=$(jq -er '.id' <<<"$commit_response")

echo "android publish: committed edit $committed_id for $package $version_name ($version_code) to $ANDROID_RELEASE_TRACK"
