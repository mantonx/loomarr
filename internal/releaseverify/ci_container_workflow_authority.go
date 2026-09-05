package releaseverify

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type workflowStepAuthority struct {
	targets           []string
	allowsAcquisition bool
	exactName         bool
	name              string
	exactStepIndex    bool
	stepIndex         int
	environment       map[string]string
	condition         string
	shell             string
}

type workflowJobAuthority struct {
	environment map[string]string
	condition   string
	permissions map[string]string
	steps       map[string]workflowStepAuthority
}

type workflowAuthority struct {
	name             string
	pullRequestTypes []string
	environment      map[string]string
	permissions      map[string]string
	jobs             map[string]workflowJobAuthority
}

func standardWorkflowEnvironment() map[string]string {
	return map[string]string{
		"GO_VERSION":   "1.27",
		"NODE_VERSION": "22",
	}
}

func standardWorkflowPermissions() map[string]string {
	return map[string]string{"contents": "read"}
}

const cacheCleanupWorkflowCommand = "set -uo pipefail\n" +
	"# `|| true` on the list: a PR with no caches is the normal case for docs-only\n" +
	"# work, and it must not fail the job.\n" +
	"ids=$(gh api \"repos/$REPO/actions/caches\" --paginate \\\n" +
	"        -q \".actions_caches[] | select(.ref==\\\"$REF\\\" or (.ref | startswith(\\\"$QUEUE_REF_PREFIX\\\"))) | .id\" 2>/dev/null || true)\n" +
	"if [ -z \"$ids\" ]; then\n" +
	"  echo \"no caches for $REF or $QUEUE_REF_PREFIX*\"\n" +
	"  exit 0\n" +
	"fi\n" +
	"for id in $ids; do\n" +
	"  # Best-effort per cache: a 404 here means something else already deleted it\n" +
	"  # (a concurrent run, or GitHub's own expiry), which is success, not failure.\n" +
	"  if gh api -X DELETE \"repos/$REPO/actions/caches/$id\" >/dev/null 2>&1; then\n" +
	"    echo \"deleted $id\"\n" +
	"  else\n" +
	"    echo \"could not delete $id (already gone?)\"\n" +
	"  fi\n" +
	"done\n"

const appleCacheFingerprintCommand = `echo "fingerprint=$(./scripts/apple-compilation-cache.sh fingerprint)" >> "$GITHUB_OUTPUT"`

const appleCachePublisherPreflightCommand = `set -euo pipefail
gh api "repos/$REPO/actions/cache/usage" > "$RUNNER_TEMP/apple-cache-usage.json"
./scripts/apple-compilation-cache.sh admit-capacity "$RUNNER_TEMP/apple-cache-usage.json"
`

const appleCacheConsumerPrepareCommand = `set -euo pipefail
mode=cold
store="$RUNNER_TEMP/apple-compilation-cache-store"
archive="$RUNNER_TEMP/apple-compilation-cache.tar.zst"
if [[ -f "$archive" ]] && ./scripts/apple-compilation-cache.sh restore "$archive" "$store"; then
  mode=warm
else
  rm -f "$archive"
fi
echo "mode=$mode" >> "$GITHUB_OUTPUT"
echo "store=$store" >> "$GITHUB_OUTPUT"
`

const appleCachePublisherSeedCommand = `set -euo pipefail
archive="$RUNNER_TEMP/apple-compilation-cache.tar.zst"
seed="$RUNNER_TEMP/apple-compilation-cache-seed.tar.zst"
if [[ -f "$archive" ]]; then
  mv "$archive" "$seed"
  echo "archive=$seed" >> "$GITHUB_OUTPUT"
else
  echo "archive=" >> "$GITHUB_OUTPUT"
fi
`

const appleCachePublisherAdmissionCommand = `set -euo pipefail
gh api "repos/$REPO/actions/cache/usage" > "$RUNNER_TEMP/apple-cache-usage.json"
./scripts/apple-compilation-cache.sh admit-save \
  "$RUNNER_TEMP/apple-compilation-cache.tar.zst" \
  "$RUNNER_TEMP/apple-cache-usage.json"
`

const appleCachePublisherRetentionCommand = `set -euo pipefail
inventory="$RUNNER_TEMP/apple-cache-inventory.json"
gh api "repos/$REPO/actions/caches?per_page=100&ref=refs/heads/main" > "$inventory"
while read -r id; do
  [[ -n "$id" ]] || continue
  gh api -X DELETE "repos/$REPO/actions/caches/$id"
done < <(./scripts/apple-compilation-cache.sh retention-plan \
  "$inventory" "$PREFIX" refs/heads/main 1)
`

func workflowRunAuthorityEntries() map[string]workflowAuthority {
	return map[string]workflowAuthority{
		"android-beta.yml": {
			permissions: map[string]string{"actions": "read", "contents": "read"},
			jobs: map[string]workflowJobAuthority{
				"release": {
					condition: "github.ref == 'refs/heads/main'",
					environment: map[string]string{
						"ANDROID_RELEASE_OUTPUT_DIR":         "${{ github.workspace }}/.artifacts/android-release",
						"LOOMARR_ANDROID_KEYSTORE_PASSWORD":  "${{ secrets.ANDROID_UPLOAD_KEYSTORE_PASSWORD }}",
						"LOOMARR_ANDROID_KEY_ALIAS":          "${{ secrets.ANDROID_UPLOAD_KEY_ALIAS }}",
						"LOOMARR_ANDROID_KEY_PASSWORD":       "${{ secrets.ANDROID_UPLOAD_KEY_PASSWORD }}",
						"LOOMARR_ANDROID_UPLOAD_CERT_SHA256": "${{ vars.ANDROID_UPLOAD_CERT_SHA256 }}",
					},
				},
			},
		},
		"apple-compilation-cache.yml": {
			environment: standardWorkflowEnvironment(),
			permissions: map[string]string{"actions": "write", "contents": "read"},
			jobs: map[string]workflowJobAuthority{
				"publish": {
					condition: "github.ref == 'refs/heads/main'",
					steps: map[string]workflowStepAuthority{
						appleCachePublisherPreflightCommand: exactWorkflowStep(1, "Preflight repository cache headroom", workflowStepAuthority{environment: map[string]string{
							"GH_TOKEN": "${{ secrets.GITHUB_TOKEN }}", "REPO": "${{ github.repository }}",
						}}),
						"make fe-install":              exactWorkflowStep(5, "", workflowStepAuthority{targets: []string{"fe-install"}, allowsAcquisition: true}),
						appleCacheFingerprintCommand:   exactWorkflowStep(6, "Fingerprint the Apple toolchain", workflowStepAuthority{}),
						appleCachePublisherSeedCommand: exactWorkflowStep(8, "Isolate the optional seed generation", workflowStepAuthority{}),
						`./web/scripts/validate-apple-compilation-cache.sh produce "$RUNNER_TEMP/apple-compilation-cache.tar.zst"`: exactWorkflowStep(9, "Build and validate the candidate generation", workflowStepAuthority{allowsAcquisition: true, environment: map[string]string{
							"LOOMARR_APPLE_CACHE_SEED_ARCHIVE":    "${{ steps.seed.outputs.archive }}",
							"LOOMARR_APPLE_CACHE_VALIDATION_ROOT": "${{ runner.temp }}/apple-cache-publisher",
						}}),
						appleCachePublisherAdmissionCommand: exactWorkflowStep(10, "Enforce repository cache headroom", workflowStepAuthority{environment: map[string]string{
							"GH_TOKEN": "${{ secrets.GITHUB_TOKEN }}", "REPO": "${{ github.repository }}",
						}}),
						appleCachePublisherRetentionCommand: exactWorkflowStep(12, "Retain only the newest generation for this fingerprint", workflowStepAuthority{environment: map[string]string{
							"GH_TOKEN": "${{ secrets.GITHUB_TOKEN }}", "REPO": "${{ github.repository }}", "PREFIX": "${{ steps.apple-toolchain.outputs.fingerprint }}",
						}}),
					},
				},
			},
		},
		"cache-cleanup.yml": {
			name:             "Cache cleanup",
			pullRequestTypes: []string{"closed"},
			permissions:      map[string]string{"actions": "write"},
			jobs: map[string]workflowJobAuthority{
				"cleanup": {
					steps: map[string]workflowStepAuthority{
						cacheCleanupWorkflowCommand: exactWorkflowStep(0, "Delete inaccessible caches for pull request ${{ github.event.pull_request.number }}", workflowStepAuthority{
							environment: map[string]string{
								"GH_TOKEN":         "${{ secrets.GITHUB_TOKEN }}",
								"REPO":             "${{ github.repository }}",
								"REF":              "refs/pull/${{ github.event.pull_request.number }}/merge",
								"QUEUE_REF_PREFIX": "refs/heads/gh-readonly-queue/main/pr-${{ github.event.pull_request.number }}-",
							},
						}),
					},
				},
			},
		},
		"ci-agent.yml": standardRunWorkflow(map[string]workflowStepAuthority{
			"make agent-harness-test": exactWorkflowStep(2, "Worktree isolation, claims, leases, and process ownership", workflowStepAuthority{targets: []string{"agent-harness-test"}}),
		}),
		"ci-android.yml": standardRunWorkflow(map[string]workflowStepAuthority{
			"make fe-install": exactWorkflowStep(5, "", workflowStepAuthority{targets: []string{"fe-install"}, allowsAcquisition: true}),
			"make fe-codegen": exactWorkflowStep(6, "", workflowStepAuthority{targets: []string{"fe-codegen"}}),
			"echo \"gradle-cache-primary-key=android-tv-react-native-v1-${{ runner.os }}-temurin-21-node-${{ env.NODE_VERSION }}-${{ hashFiles('web/apps/tv/**', 'web/packages/**', 'web/pnpm-lock.yaml', 'web/scripts/**') }}-${{ github.sha }}-${{ github.run_id }}\"\necho \"gradle-cache-hit=${{ steps.gradle-cache.outputs.cache-hit == 'true' }}\"\necho \"gradle-cache-source-sha=${GITHUB_SHA}\"\n": exactWorkflowStep(8, "Record Gradle cache provenance", workflowStepAuthority{}),
			"make android": exactWorkflowStep(9, "", workflowStepAuthority{
				targets: []string{"android"},
				environment: map[string]string{
					"ANDROID_CI_OUTPUT_DIR": "${{ github.workspace }}/.artifacts/android-ci",
				},
			}),
		}),
		"ci-apple-mobile.yml": standardRunWorkflow(map[string]workflowStepAuthority{
			"make fe-install":                exactWorkflowStep(3, "", workflowStepAuthority{targets: []string{"fe-install"}}),
			appleCacheFingerprintCommand:     exactWorkflowStep(4, "Fingerprint the Apple toolchain", workflowStepAuthority{}),
			appleCacheConsumerPrepareCommand: exactWorkflowStep(6, "Validate the restored Apple compilation cache", workflowStepAuthority{}),
			"make client-apple-simulator CLIENT_APP=mobile": exactWorkflowStep(7, "Generate, build, install, and launch", workflowStepAuthority{
				targets: []string{"client-apple-simulator"},
				environment: map[string]string{
					"LOOMARR_APPLE_ARTIFACTS_DIR": "${{ runner.temp }}/apple-client-mobile",
					"LOOMARR_APPLE_BUILD_DIR":     "${{ runner.temp }}/apple-build-mobile",
					"LOOMARR_APPLE_CACHE_MODE":    "${{ steps.apple-compilation-cache.outputs.mode }}",
					"LOOMARR_APPLE_CACHE_STORE":   "${{ steps.apple-compilation-cache.outputs.store }}",
				},
			}),
		}),
		"ci-apple-tv.yml": standardRunWorkflow(map[string]workflowStepAuthority{
			"make fe-install":                exactWorkflowStep(3, "", workflowStepAuthority{targets: []string{"fe-install"}}),
			appleCacheFingerprintCommand:     exactWorkflowStep(4, "Fingerprint the Apple toolchain", workflowStepAuthority{}),
			appleCacheConsumerPrepareCommand: exactWorkflowStep(6, "Validate the restored Apple compilation cache", workflowStepAuthority{}),
			"make client-apple-simulator CLIENT_APP=tv": exactWorkflowStep(7, "Generate, build, install, and launch", workflowStepAuthority{
				targets: []string{"client-apple-simulator"},
				environment: map[string]string{
					"LOOMARR_APPLE_ARTIFACTS_DIR": "${{ runner.temp }}/apple-client-tv",
					"LOOMARR_APPLE_BUILD_DIR":     "${{ runner.temp }}/apple-build-tv",
					"LOOMARR_APPLE_CACHE_MODE":    "${{ steps.apple-compilation-cache.outputs.mode }}",
					"LOOMARR_APPLE_CACHE_STORE":   "${{ steps.apple-compilation-cache.outputs.store }}",
				},
			}),
		}),
		"ci-apple-cache-validation.yml": {
			environment: standardWorkflowEnvironment(),
			permissions: standardWorkflowPermissions(),
			jobs: map[string]workflowJobAuthority{
				"producer": {steps: map[string]workflowStepAuthority{
					"make fe-install": exactWorkflowStep(4, "", workflowStepAuthority{targets: []string{"fe-install"}, allowsAcquisition: true}),
					`./web/scripts/validate-apple-compilation-cache.sh produce "$RUNNER_TEMP/apple-compilation-cache.tar.zst"`: exactWorkflowStep(5, "Produce and validate the compilation cache", workflowStepAuthority{allowsAcquisition: true, environment: map[string]string{"LOOMARR_APPLE_CACHE_VALIDATION_ROOT": "${{ runner.temp }}/apple-cache-validation-producer"}}),
				}},
				"consumer": {steps: map[string]workflowStepAuthority{
					"make fe-install": exactWorkflowStep(4, "", workflowStepAuthority{targets: []string{"fe-install"}, allowsAcquisition: true}),
					`./web/scripts/validate-apple-compilation-cache.sh consume "$RUNNER_TEMP/apple-compilation-cache.tar.zst"`: exactWorkflowStep(6, "Consume and invalidate the compilation cache", workflowStepAuthority{allowsAcquisition: true, environment: map[string]string{"LOOMARR_APPLE_CACHE_VALIDATION_ROOT": "${{ runner.temp }}/apple-cache-validation-consumer"}}),
				}},
			},
		},
		"ci-clients.yml": standardRunWorkflow(map[string]workflowStepAuthority{
			"make fe-install": exactWorkflowStep(4, "", workflowStepAuthority{targets: []string{"fe-install"}}),
			"make fe-codegen": exactWorkflowStep(5, "", workflowStepAuthority{targets: []string{"fe-codegen"}}),
			"make clients": exactWorkflowStep(6, "Verify the affected client graph", workflowStepAuthority{
				targets: []string{"clients"},
				environment: map[string]string{
					"TMPDIR":                   "${{ runner.temp }}",
					"TURBO_TELEMETRY_DISABLED": "1",
				},
			}),
		}),
		"ci-docs.yml": standardRunWorkflow(map[string]workflowStepAuthority{}),
		"ci-frontend.yml": standardRunWorkflow(map[string]workflowStepAuthority{
			"make fe-install": exactWorkflowStep(4, "", workflowStepAuthority{targets: []string{"fe-install"}}),
			"make fe FE_SHARD=${{ matrix.shard }}/${{ strategy.job-total }}": exactWorkflowStep(5, "", workflowStepAuthority{targets: []string{"fe"}}),
			"make fe-tokens-verify": exactWorkflowStep(6, "Token artifacts are committed and current", workflowStepAuthority{targets: []string{"fe-tokens-verify"}, condition: "matrix.shard == 1"}),
		}),
		"ci-go-contracts.yml": standardRunWorkflow(map[string]workflowStepAuthority{
			"make fmt shellcheck privacy-verify vet tags-verify vet-tags lint agent-harness-test compose-verify release-verify go-race-verify": exactWorkflowStep(4, "Static analysis and repository contracts", workflowStepAuthority{targets: []string{"fmt", "shellcheck", "privacy-verify", "vet", "tags-verify", "vet-tags", "lint", "agent-harness-test", "compose-verify", "release-verify", "go-race-verify"}}),
			"make go-shard-verify SHARDS=3": exactWorkflowStep(5, "The test shards cover every package", workflowStepAuthority{targets: []string{"go-shard-verify"}}),
			"make openapi-verify":           exactWorkflowStep(6, "OpenAPI spec is committed and current", workflowStepAuthority{targets: []string{"openapi-verify"}}),
			"make config-docs-verify":       exactWorkflowStep(7, "Config docs are committed and current", workflowStepAuthority{targets: []string{"config-docs-verify"}}),
			"make arch-docs-verify":         exactWorkflowStep(8, "The §2 package map is committed and current", workflowStepAuthority{targets: []string{"arch-docs-verify"}}),
			"make dev-docs-verify":          exactWorkflowStep(9, "Command reference is committed and current", workflowStepAuthority{targets: []string{"dev-docs-verify"}}),
			"make retired-verify":           exactWorkflowStep(10, "Retired identifiers are gone from prose", workflowStepAuthority{targets: []string{"retired-verify"}}),
			"make ci-lint":                  exactWorkflowStep(11, "Workflows are valid", workflowStepAuthority{targets: []string{"ci-lint"}}),
			"make observability-verify":     exactWorkflowStep(12, "Observability artifacts are provisionable", workflowStepAuthority{targets: []string{"observability-verify"}, allowsAcquisition: true}),
		}),
		"ci-go.yml": standardRunWorkflow(map[string]workflowStepAuthority{
			"make test GO_SHARD=${{ matrix.shard }}/${{ strategy.job-total }}": exactWorkflowStep(10, "", workflowStepAuthority{targets: []string{"test"}}),
		}),
		"ci-image-certification.yml": standardRunWorkflow(map[string]workflowStepAuthority{
			`IMAGE_CERT_REPORT="$RUNNER_TEMP/image-certification.json" make image-cert`: exactWorkflowStep(3, "Certify the release worker against the deterministic real-codec corpus", workflowStepAuthority{targets: []string{"image-cert"}}),
		}),
		"ci-image.yml": standardRunWorkflow(map[string]workflowStepAuthority{
			packagedImageMetadataInspection: exactWorkflowStep(3, "Inspect packaged license and notice metadata", workflowStepAuthority{allowsAcquisition: true, shell: "bash"}),
		}),
		"ci-playwright.yml": standardRunWorkflow(map[string]workflowStepAuthority{
			"make fe-install":            exactWorkflowStep(4, "", workflowStepAuthority{targets: []string{"fe-install"}}),
			"make fe-codegen":            exactWorkflowStep(5, "", workflowStepAuthority{targets: []string{"fe-codegen"}}),
			playwrightVisualWorkflowStep: exactWorkflowStep(6, "Visual + a11y over storybook-static", workflowStepAuthority{targets: []string{"fe-visual"}, allowsAcquisition: true}),
			"make e2e":                   exactWorkflowStep(7, "Wizard e2e smoke vs a mocked backend", workflowStepAuthority{targets: []string{"e2e"}, allowsAcquisition: true, condition: "matrix.shard == 1"}),
		}),
		"ci-postgres.yml": standardRunWorkflow(map[string]workflowStepAuthority{
			"make test-pg": exactWorkflowStep(4, "", workflowStepAuthority{targets: []string{"test-pg"}, allowsAcquisition: true}),
		}),
		"ci-rust-contracts.yml": standardRunWorkflow(map[string]workflowStepAuthority{
			"make rust-check": exactWorkflowStep(2, "", workflowStepAuthority{targets: []string{"rust-check"}}),
		}),
		"ci-tuner.yml": standardRunWorkflow(map[string]workflowStepAuthority{
			"make fe-install":     exactWorkflowStep(3, "", workflowStepAuthority{targets: []string{"fe-install"}}),
			"make tuner-e2e-host": exactWorkflowStep(5, "100-Channel controller matrix", workflowStepAuthority{targets: []string{"tuner-e2e-host"}}),
		}),
		"ci.yml": {
			environment: standardWorkflowEnvironment(),
			permissions: standardWorkflowPermissions(),
			jobs: map[string]workflowJobAuthority{
				"changes": {steps: map[string]workflowStepAuthority{}},
				"ci-policy": {
					condition: "needs.changes.outputs.impact_policy == 'true'",
					steps: map[string]workflowStepAuthority{
						"make ci-lint release-verify":                                                exactWorkflowStep(3, "Workflow and publication policy", workflowStepAuthority{targets: []string{"ci-lint", "release-verify"}}),
						"make agent-harness-test shellcheck":                                         exactWorkflowStep(4, "Agent harness and shell policy", workflowStepAuthority{targets: []string{"agent-harness-test", "shellcheck"}}),
						"go test ./docs && make arch-docs-verify config-docs-verify dev-docs-verify": exactWorkflowStep(5, "Documentation contracts read by Go", workflowStepAuthority{targets: []string{"arch-docs-verify", "config-docs-verify", "dev-docs-verify"}}),
					},
				},
				"ci-ok": {
					condition:   "always()",
					permissions: map[string]string{"actions": "read", "contents": "read"},
					steps:       map[string]workflowStepAuthority{},
				},
			},
		},
		"image-benchmark.yml": {
			permissions: standardWorkflowPermissions(),
			jobs: map[string]workflowJobAuthority{
				"benchmark": {
					steps: map[string]workflowStepAuthority{
						"make image-bench": exactWorkflowStep(3, "Measure complete AVIF ladders", workflowStepAuthority{
							targets: []string{"image-bench"},
							environment: map[string]string{
								"IMAGE_BENCH_RUNS":   "${{ inputs.runs }}",
								"IMAGE_BENCH_REPORT": "${{ runner.temp }}/image-benchmark-${{ matrix.platform }}.json",
							},
						}),
						"make image-parallelism-bench": exactWorkflowStep(4, "Compare AVIF process and thread shapes", workflowStepAuthority{
							targets: []string{"image-parallelism-bench"},
							environment: map[string]string{
								"IMAGE_BENCH_RUNS":         "${{ inputs.runs }}",
								"IMAGE_BENCH_ROLES":        "poster",
								"IMAGE_BENCH_CPU_PROFILES": "2,4",
								"IMAGE_BENCH_REPORT_DIR":   "${{ runner.temp }}/image-parallelism-${{ matrix.platform }}",
							},
						}),
					},
				},
			},
		},
		"pages.yml": {
			permissions: map[string]string{"contents": "read", "id-token": "write", "pages": "write"},
			jobs: map[string]workflowJobAuthority{
				"build":  {steps: map[string]workflowStepAuthority{}},
				"deploy": {steps: map[string]workflowStepAuthority{}},
			},
		},
		"release-notes.yml": {
			permissions: map[string]string{"contents": "write"},
			jobs: map[string]workflowJobAuthority{
				"publish-notes": {steps: map[string]workflowStepAuthority{}},
			},
		},
		"release.yml": {
			environment: map[string]string{"IMAGE": "ghcr.io/${{ github.repository }}", "REGISTRY": "ghcr.io"},
			permissions: map[string]string{"actions": "read", "contents": "read", "id-token": "write", "packages": "write"},
			jobs: map[string]workflowJobAuthority{
				"build":   {steps: map[string]workflowStepAuthority{}},
				"publish": {steps: map[string]workflowStepAuthority{}},
			},
		},
		"rust-maintenance.yml": {
			permissions: map[string]string{"contents": "read"},
			jobs: map[string]workflowJobAuthority{
				"supply-chain": {steps: map[string]workflowStepAuthority{}},
				"fuzz": {
					environment: map[string]string{"FUZZ_SECONDS": "${{ inputs.fuzz_seconds }}"},
					steps:       map[string]workflowStepAuthority{},
				},
			},
		},
	}
}

func exactWorkflowStep(index int, name string, authority workflowStepAuthority) workflowStepAuthority {
	authority.exactName = true
	authority.name = name
	authority.exactStepIndex = true
	authority.stepIndex = index
	return authority
}

func standardRunWorkflow(steps map[string]workflowStepAuthority) workflowAuthority {
	return workflowAuthority{
		environment: standardWorkflowEnvironment(),
		permissions: standardWorkflowPermissions(),
		jobs: map[string]workflowJobAuthority{
			"run": {steps: steps},
		},
	}
}

type workflowAuthorityKey struct {
	workflow string
	job      string
	command  string
}

type workflowAuthorityLedger struct {
	required map[workflowAuthorityKey]int
}

func newWorkflowAuthorityLedger() *workflowAuthorityLedger {
	return &workflowAuthorityLedger{required: make(map[workflowAuthorityKey]int)}

}

func (ledger *workflowAuthorityLedger) expectWorkflow(workflowName string) {
	workflow, ok := workflowAuthorityCatalog().runs[workflowName]
	if !ok {
		return
	}
	for jobName, job := range workflow.jobs {
		for command := range job.steps {
			ledger.required[workflowAuthorityKey{workflow: workflowName, job: jobName, command: command}] = 0
		}
	}
}

func (ledger *workflowAuthorityLedger) authorize(workflowName, jobName string, step, run *yaml.Node, stepIndex int, protectedTargets map[string]struct{}, scripts *repositoryScriptAudit) (bool, error) {
	authority, ok := workflowAuthorityCatalog().runs[workflowName]
	if !ok {
		return false, nil
	}
	jobAuthority, ok := authority.jobs[jobName]
	if !ok {
		return false, nil
	}
	stepAuthority, ok := jobAuthority.steps[run.Value]
	if !ok {
		return false, nil
	}
	if !stepAuthority.allowsAcquisition && !targetsRemainNonAcquiring(stepAuthority.targets, protectedTargets) {
		return false, nil
	}
	if !stepAuthority.allowsAcquisition && scripts != nil && scripts.commandAcquires(run.Value) {
		return false, nil
	}
	if stepAuthority.exactStepIndex && stepIndex != stepAuthority.stepIndex {
		return false, fmt.Errorf("workflow %s job %s source-bound step %q moved from absolute step %d to %d", workflowName, jobName, strings.TrimSpace(run.Value), stepAuthority.stepIndex+1, stepIndex+1)
	}
	if _, hasActionKind := mappingValue(step, "uses"); hasActionKind {
		return false, fmt.Errorf("workflow %s job %s source-bound step %q must remain a run step", workflowName, jobName, strings.TrimSpace(run.Value))
	}
	if stepAuthority.exactName {
		gotName, present := mappingValue(step, "name")
		if stepAuthority.name == "" {
			if present {
				return false, fmt.Errorf("workflow %s job %s source-bound step %q must remain unnamed", workflowName, jobName, strings.TrimSpace(run.Value))
			}
		} else if !present || gotName.Kind != yaml.ScalarNode || gotName.Value != stepAuthority.name {
			return false, fmt.Errorf("workflow %s job %s source-bound step %q name differs from its authority", workflowName, jobName, strings.TrimSpace(run.Value))
		}
	}
	label := fmt.Sprintf("workflow %s job %s source-bound step %q", workflowName, jobName, strings.TrimSpace(run.Value))
	if err := verifyExactExecutionContext(step, label+" step", stepAuthority.environment, nil, stepAuthority.condition, stepAuthority.shell); err != nil {
		return false, err
	}
	key := workflowAuthorityKey{workflow: workflowName, job: jobName, command: run.Value}
	ledger.required[key]++
	if ledger.required[key] != 1 {
		return false, fmt.Errorf("workflow %s job %s must contain source-bound run authority %q exactly once", workflowName, jobName, strings.TrimSpace(run.Value))
	}
	return true, nil
}

func verifySourceBoundWorkflowContext(workflowName, jobName string, workflow, job *yaml.Node) error {
	authority, ok := workflowAuthorityCatalog().runs[workflowName]
	if !ok {
		return nil
	}
	jobAuthority, ok := authority.jobs[jobName]
	if !ok {
		return nil
	}
	label := fmt.Sprintf("workflow %s job %s source-bound context", workflowName, jobName)
	if err := verifyExactExecutionContext(workflow, label+" workflow", authority.environment, authority.permissions, "", ""); err != nil {
		return err
	}
	if err := verifyExactExecutionContext(job, label+" job", jobAuthority.environment, jobAuthority.permissions, jobAuthority.condition, ""); err != nil {
		return err
	}
	return verifyWorkflowJobContextAuthority(workflowName, jobName, job)
}

func verifySourceBoundWorkflowIdentity(workflowName string, workflow *yaml.Node) error {
	authority, ok := workflowAuthorityCatalog().runs[workflowName]
	if !ok || (authority.name == "" && authority.pullRequestTypes == nil) {
		return nil
	}
	name, present := mappingValue(workflow, "name")
	if authority.name == "" {
		if present {
			return fmt.Errorf("workflow %s must remain unnamed", workflowName)
		}
	} else if !present || name.Kind != yaml.ScalarNode || name.Value != authority.name {
		return fmt.Errorf("workflow %s name differs from its source-bound authority", workflowName)
	}
	trigger, present := mappingValue(workflow, "on")
	if !present || trigger.Kind != yaml.MappingNode || len(trigger.Content) != 2 || trigger.Content[0].Value != "pull_request" {
		return fmt.Errorf("workflow %s trigger must remain exactly pull_request", workflowName)
	}
	pullRequest := trigger.Content[1]
	if pullRequest.Kind != yaml.MappingNode || len(pullRequest.Content) != 2 || pullRequest.Content[0].Value != "types" {
		return fmt.Errorf("workflow %s pull_request trigger differs from its source-bound authority", workflowName)
	}
	types := pullRequest.Content[1]
	if types.Kind != yaml.SequenceNode || len(types.Content) != len(authority.pullRequestTypes) {
		return fmt.Errorf("workflow %s pull_request types differ from its source-bound authority", workflowName)
	}
	for index, want := range authority.pullRequestTypes {
		if types.Content[index].Kind != yaml.ScalarNode || types.Content[index].Value != want {
			return fmt.Errorf("workflow %s pull_request types differ from its source-bound authority", workflowName)
		}
	}
	return nil
}

func (ledger *workflowAuthorityLedger) verifyComplete() error {
	keys := make([]workflowAuthorityKey, 0, len(ledger.required))
	for key := range ledger.required {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(left, right int) bool {
		if keys[left].workflow != keys[right].workflow {
			return keys[left].workflow < keys[right].workflow
		}
		if keys[left].job != keys[right].job {
			return keys[left].job < keys[right].job
		}
		return keys[left].command < keys[right].command
	})
	for _, key := range keys {
		count := ledger.required[key]
		if count != 1 {
			return fmt.Errorf("workflow %s job %s must contain source-bound run authority %q exactly once (found %d)", key.workflow, key.job, strings.TrimSpace(key.command), count)
		}
	}
	return nil
}

func verifyExactExecutionContext(scope *yaml.Node, label string, wantEnvironment, wantPermissions map[string]string, wantCondition, wantShell string) error {
	gotEnvironment, err := scalarEnvironment(scope)
	if err != nil {
		return fmt.Errorf("%s: %w", label, err)
	}
	if !equalStringMaps(gotEnvironment, wantEnvironment) {
		return fmt.Errorf("%s environment differs from its source-bound authority", label)
	}
	gotPermissions, err := scalarMapping(scope, "permissions", "workflow permissions")
	if err != nil {
		return fmt.Errorf("%s: %w", label, err)
	}
	if !equalStringMaps(gotPermissions, wantPermissions) {
		return fmt.Errorf("%s permissions differ from its source-bound authority", label)
	}
	if _, ok := mappingValue(scope, "defaults"); ok {
		return fmt.Errorf("%s must not override run defaults", label)
	}
	for key, want := range map[string]string{
		"if":                wantCondition,
		"shell":             wantShell,
		"working-directory": "",
		"continue-on-error": "",
	} {
		got, present := mappingValue(scope, key)
		if want == "" {
			if present {
				return fmt.Errorf("%s must not set %s", label, key)
			}
			continue
		}
		if !present || got.Kind != yaml.ScalarNode || got.Value != want {
			return fmt.Errorf("%s %s differs from its source-bound authority", label, key)
		}
	}
	return nil
}

func equalStringMaps(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}
