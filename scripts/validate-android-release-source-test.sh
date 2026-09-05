#!/usr/bin/env bash

set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
test_root=$(mktemp -d /tmp/android-release-source-test.XXXXXX)
trap 'find "$test_root" -mindepth 1 -depth -delete; rmdir "$test_root"' EXIT
mkdir -p "$test_root/bin"

head_sha=1111111111111111111111111111111111111111
evidence_sha=0000000000000000000000000000000000000000

cat > "$test_root/bin/git" <<'STUB'
#!/usr/bin/env bash
set -euo pipefail
case "$*" in
  'rev-parse HEAD'|'rev-parse origin/main') printf '%s\n' "$RELEASE_TEST_HEAD" ;;
  'rev-parse --is-shallow-repository') printf '%s\n' "$RELEASE_TEST_SHALLOW" ;;
  'fetch --no-tags origin main') ;;
  'fetch --no-tags --unshallow origin main') : > "$RELEASE_TEST_UNSHALLOW_MARKER" ;;
  "merge-base --is-ancestor $RELEASE_TEST_EVIDENCE $RELEASE_TEST_HEAD")
    if [[ "$RELEASE_TEST_SHALLOW" == true && ! -f "$RELEASE_TEST_UNSHALLOW_MARKER" ]]; then
      echo "fatal: Not a valid commit name $RELEASE_TEST_EVIDENCE" >&2
      exit 128
    fi
    [[ "$RELEASE_TEST_ANCESTOR" == true ]]
    ;;
  "diff --name-only $RELEASE_TEST_EVIDENCE $RELEASE_TEST_HEAD")
    printf '%b' "$RELEASE_TEST_CHANGED"
    ;;
  *) echo "unexpected git invocation: $*" >&2; exit 64 ;;
esac
STUB

cat > "$test_root/bin/gh" <<'STUB'
#!/usr/bin/env bash
set -euo pipefail
if [[ "$*" == *'/jobs?per_page=100'* ]]; then
  printf '%b\n' "$RELEASE_TEST_JOBS"
else
  printf '%b\n' "$RELEASE_TEST_RUNS"
fi
STUB
chmod +x "$test_root/bin/git" "$test_root/bin/gh"

qualified_job=$'Android TV — React Native Play bundle / Android TV — React Native Play bundle\tsuccess'
plain_job=$'Android TV — React Native Play bundle\tsuccess'

run_case() {
  local name=$1 runs=$2 jobs=$3 changed=$4 ancestor=$5 expected=$6 shallow=${7:-false}
  local output status
  rm -f "$test_root/unshallowed"
  set +e
  output=$(cd "$root" && \
    PATH="$test_root/bin:$PATH" \
    RELEASE_TEST_HEAD="$head_sha" \
    RELEASE_TEST_EVIDENCE="$evidence_sha" \
    RELEASE_TEST_RUNS="$runs" \
    RELEASE_TEST_JOBS="$jobs" \
    RELEASE_TEST_CHANGED="$changed" \
    RELEASE_TEST_ANCESTOR="$ancestor" \
    RELEASE_TEST_SHALLOW="$shallow" \
    RELEASE_TEST_UNSHALLOW_MARKER="$test_root/unshallowed" \
    GITHUB_REF=refs/heads/main \
    GITHUB_REPOSITORY=loomarr/loomarr \
    GITHUB_SHA="$head_sha" \
    ./scripts/validate-android-release-source.sh 2>&1)
  status=$?
  set -e
  if [[ "$expected" == pass && $status -ne 0 ]]; then
    printf 'validate-android-release-source-test: %s unexpectedly failed:\n%s\n' "$name" "$output" >&2
    exit 1
  fi
  if [[ "$expected" == fail && $status -eq 0 ]]; then
    printf 'validate-android-release-source-test: %s unexpectedly passed\n' "$name" >&2
    exit 1
  fi
}

current_run=$'42\tsuccess\tworkflow_dispatch\tmain\t'"$head_sha"
ancestor_run=$'41\tsuccess\tworkflow_dispatch\tmain\t'"$evidence_sha"

run_case qualified-current "$current_run" "$qualified_job" '' true pass
run_case plain-current "$current_run" "$plain_job" '' true pass
run_case unchanged-android-ancestor "$ancestor_run" "$qualified_job" \
  $'web/apps/web/src/main.tsx\nweb/apps/web/vite.config.ts\n' true pass
run_case shallow-android-ancestor "$ancestor_run" "$qualified_job" \
  $'web/apps/web/src/main.tsx\n' true pass true
run_case changed-android-ancestor "$ancestor_run" "$qualified_job" \
  $'web/apps/tv/src/app.tsx\n' true fail
run_case changed-impact-authority "$ancestor_run" "$qualified_job" \
  $'scripts/ci-impact.sh\n' true fail
run_case changed-source-validator "$ancestor_run" "$qualified_job" \
  $'scripts/validate-android-release-source.sh\n' true fail
run_case unrelated-history "$ancestor_run" "$qualified_job" '' false fail
run_case failed-android-job "$current_run" \
  $'Android TV — React Native Play bundle / Android TV — React Native Play bundle\tfailure' '' true fail
run_case missing-android-job "$current_run" $'CI\tsuccess' '' true fail

echo 'validate-android-release-source-test: ok'
