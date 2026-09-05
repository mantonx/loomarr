#!/usr/bin/env bash

set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

: "${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required}"
: "${GITHUB_SHA:?GITHUB_SHA is required}"
: "${GITHUB_REF:?GITHUB_REF is required}"

if [[ "$GITHUB_REF" != refs/heads/main ]]; then
	echo 'android release: workflow must run from the main branch' >&2
	exit 1
fi

head=$(git rev-parse HEAD)
if [[ "$head" != "$GITHUB_SHA" ]]; then
	echo 'android release: checked-out source does not match the workflow commit' >&2
	exit 1
fi
if [[ "$(git rev-parse --is-shallow-repository)" == true ]]; then
	git fetch --no-tags --unshallow origin main
else
	git fetch --no-tags origin main
fi
if [[ "$head" != "$(git rev-parse origin/main)" ]]; then
	echo 'android release: source must be the current main commit' >&2
	exit 1
fi

runs=$(
	gh api \
		"/repos/${GITHUB_REPOSITORY}/actions/workflows/ci.yml/runs?per_page=100" \
		--jq '.workflow_runs[] | [.id, .conclusion, .event, .head_branch, .head_sha] | @tsv'
)

android_job='Android TV — React Native Play bundle'
evidence_run_id=
evidence_sha=
while IFS=$'\t' read -r run_id conclusion event branch run_sha; do
	[[ -n "$run_id" ]] || continue
	[[ "$branch" == main && ("$event" == push || "$event" == workflow_dispatch) ]] || continue

	jobs=$(
		gh api --paginate \
			"/repos/${GITHUB_REPOSITORY}/actions/runs/${run_id}/jobs?per_page=100" \
			--jq '.jobs[] | [.name, .conclusion] | @tsv'
	)
	android_conclusion=$(
		awk -F $'\t' -v expected="$android_job" '
			$1 == expected || index($1, expected " / ") == 1 { print $2; exit }
		' <<<"$jobs"
	)
	# A release-candidate run deliberately skips Android. It is not contrary evidence, so keep
	# looking for the newest run that actually executed the gate.
	[[ -n "$android_conclusion" && "$android_conclusion" != skipped ]] || continue
	if [[ "$conclusion" != success || "$android_conclusion" != success ]]; then
		echo "android release: newest Android CI evidence is not successful (run ${run_id}: ${conclusion:-none}/${android_conclusion:-none})" >&2
		exit 1
	fi
	[[ -n "$run_sha" ]] || continue

	if [[ "$run_sha" != "$head" ]]; then
		git merge-base --is-ancestor "$run_sha" "$head" || continue
		changed=()
		while IFS= read -r path; do
			[[ -n "$path" ]] && changed+=("$path")
		done < <(git diff --name-only "$run_sha" "$head")
		if ((${#changed[@]})); then
			impact=$("$script_dir/ci-impact.sh" "${changed[@]}")
			if grep -Fqx 'android=true' <<<"$impact"; then
				echo "android release: Android inputs changed after CI evidence run ${run_id}" >&2
				exit 1
			fi
		fi
	fi

	evidence_run_id=$run_id
	evidence_sha=$run_sha
	break
done <<<"$runs"

if [[ -z "$evidence_run_id" ]]; then
	echo 'android release: main ancestry has no successful React Native Play bundle gate' >&2
	exit 1
fi

echo "android release: source commit $GITHUB_SHA reuses successful React Native Play bundle gate ${evidence_run_id} at ${evidence_sha}"
