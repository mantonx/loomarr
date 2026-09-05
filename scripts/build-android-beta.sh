#!/usr/bin/env bash

set -euo pipefail

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
repo_root=$(cd -- "$script_dir/.." && pwd)
"$script_dir/check-android-release-env.sh"

exec "$repo_root/web/scripts/build-shield-play.sh" "$LOOMARR_ANDROID_VERSION_NAME"
