# Proportional local and CI feedback

## Goal

Reduce Loomarr's local-development and CI feedback time without weakening the assurance required
to merge or release. Changed paths are classified once, unfamiliar inputs fail closed, fast checks
exercise the affected dependency closure, and a protected final boundary retains the complete
required gates.

## Measured baseline

On 2026-08-22, recent successful pull requests completed in roughly 10-12 minutes. A change limited
to `internal/suggest` and `docs/programming-design.md` still ran three `make check` shards, Postgres
conformance, Windows playout verification, and native amd64 and arm64 release-image builds. In one
representative run the three `make check` steps took 456, 537, and 648 seconds. Each shard repeated
Rust, formatting, vet, lint, tagged compilation, Windows compilation, harness, and release-contract
work; only the Go test package list was partitioned.

The repository requires one aggregate `CI` status. On 2026-08-23 its branch protection was changed
to strict mode while preserving the GitHub Actions app binding. On 2026-08-27 the organization-owned
repository activated a required, one-build merge queue for `main`; pull requests now receive fast
impact-scoped feedback, while scarce macOS evidence runs on the generated current-base merge group.

On 2026-08-24, the next 20 successful pull-request runs were measured from GitHub's run and job
records. Wall time includes runner queueing; occupied time is the sum of non-skipped job execution;
critical path is the longest single job execution.

| Family | Runs | Wall p95 | Occupied runner p95 | Longest-job p95 |
| --- | ---: | ---: | ---: | ---: |
| Native client selected | 9 | 68.8 min | 146.9 min | 53.3 min |
| Backend / repository | 10 | 13.2 min | 37.8 min | 7.3 min |
| Web | 1 | 18.1 min | 51.3 min | 4.8 min |
| All sampled runs | 20 | 54.5 min | 124.4 min | 40.6 min |

This sample is the activation baseline, not a claim that the families have equal statistical
confidence. It establishes the actual first bottleneck: the broad `clients` decision wakes both
macOS native builds, and native queue plus execution dominates the end-to-end tail. The initial
target is a native-client p95 at or below 20 minutes and at least 40% fewer occupied runner-minutes
for this representative mix. Existing 5-minute leaf and 12-minute merge-group budgets remain; a
budget miss creates optimization evidence and never skips a gate.

## Assurance tiers

| Tier | Scope | Budget |
| --- | --- | --- |
| Edit | Direct package or frontend test in watch mode | seconds |
| Pre-push | `make verify`: affected dependency closure and relevant static/policy checks | 90 seconds p95 |
| Pull request | Fail-closed, impact-scoped gates running in parallel | 5 minutes p95 for leaf changes |
| Merge group | Full affected-domain gate against current `main` | 12 minutes p95 |
| Main, nightly, release | Publication on admitted main; explicit complete audits and release certification | comprehensive |

These are feedback budgets, not test timeouts. Exceeding one produces evidence for the next
optimization; it never skips or kills a correctness gate.

## Policy

One deep module owns path classification. Its interface accepts changed repository paths and
returns stable gate decisions. Local tooling and CI are adapters at that seam; neither carries a
second set of path regular expressions. Unknown paths, missing bases, classifier errors, and new
source families select every gate.

For Go changes, the fast tier runs race tests for changed packages plus their reverse-dependent
closure. Repository-wide compilation and contract checks remain cheap, parallel checks. These
seams always force the complete Go suite: composition root, shared testkit, store interfaces and
migrations, module files, build tags, and generators.

The final protected tier retains all applicable assertions from `make check`, Postgres conformance,
the three-browser tuner suite, visual and accessibility coverage, native release architectures,
and Android. The work changes when assurance runs, not whether it exists.

## Delivery

1. Add the classifier and exhaustive table/known-path tests without changing CI behavior.
2. Make affected local verification consume it and calculate reverse Go dependencies.
3. Split global Go contracts from sharded race tests so global work runs once.
4. Add specialized Postgres, Windows, Rust, visual, e2e, tuner, image, and Android decisions in
   shadow mode while the old jobs still run.
5. Compare shadow selections with full outcomes and add a regression fixture for every mismatch.
6. Enable strict merge protection. After the repository transfer, enable and prove the
   already-supported `merge_group` path through an organization repository ruleset.
7. Activate proportional pull-request gates, make merge groups authoritative for integration, and
   retain explicit complete manual/nightly/release audits without repeating them on admitted main.
8. Publish selected gates, setup/cache/test timings, critical path, and runner-minutes in summaries;
   then profile genuinely slow packages after orchestration waste is gone.
9. Split the broad client family into shared JavaScript, iOS, tvOS, Expo Android mobile, and Expo
   Android TV decisions. App-local paths select one app on two platforms; shared-package and API
   contract paths select every transitive native consumer. Keep each decision observational until
   its required native job and protected final boundary are explicit. Expo Android mobile is now
   active as a separate required gate; Expo Android TV remains observational.
10. Before any new release, run and record release-relevant certification on the maintainer's local
    machine. Protected CI remains required for host-incompatible native evidence and current-main
    provenance; neither local nor remote evidence substitutes for the other.

Each activation is a separate reversible pull request. `docs/design.md` section 19 and the developer
gate documentation are amended before the first change that alters required behavior.

## Evidence

- PR #462 merged the fail-closed classifier with the full required CI matrix green. Its critical
  path was 11m18s; the other Go shards took 11m06s and 9m37s.
- The reverse-dependency selector resolves the repository graph in about 0.3 seconds on the
  development host. A representative `internal/suggest` leaf selects 10 of 59 packages, including
  its command, API, composition, integration, and workflow consumers while excluding the unrelated
  store package. Cross-cutting and unknown paths select all 59.
- With the selector wired into `agent-verify`, that representative leaf completed its 10-package
  race-policy-aware local check in 34.2 seconds on a warm development host. The command still states
  that it is focused evidence and that the complete gate remains required before publication.
- PR #464 merged that local activation with the full matrix green. Its legacy CI critical path was
  still 11m15s: every specialized job finished within 3m31s, while the three Go shards took 11m15s,
  9m14s, and 9m28s because each repeated the same repository contracts before its test partition.
- The next slice preserves `make check` as `check-static` plus `test`, runs the static/repository
  contracts once beside three test-only shards, verifies the shard partition independently, and
  requires both halves in the `CI` aggregate.
- PR #465 merged that split. Its clean-run test shards completed in 3m48s, 4m36s, and 5m56s,
  versus 11m15s, 9m14s, and 9m28s before. The one-time contract job took 8m50s because a 1m59s
  runtime certification still followed 6m26s of independent static contracts; the next slice runs
  those two required results in parallel.
- PR #466 merged the certification split. On its clean run, repository contracts became the
  5m39s critical path while release-worker certification completed in 2m50s and the cached Go
  shards completed in 2m21s, 3m09s, and 2m24s. This establishes the pre-activation baseline for
  observing specialized decisions without allowing them to skip any current job.
- PR #799's authoritative merge-group run exposed a new cold-run imbalance after later suite growth:
  the three Go test steps took 11m57s, 6m30s, and 5m40s. Reported package durations totaled
  1058/683/523 seconds under straight round-robin because `app`, `channels`, and `store` aligned on
  shard 1. Alternating each N-wide package row models 766/683/816 seconds from the same evidence,
  with no maintained cost table and no change to the three-runner count or exact-partition guard.
- That run also confirmed all rolling Go caches missed and their queue saves correctly skipped.
  GitHub's default-branch cache policy rules out `merge_group`/`workflow_run` promotion. A trusted
  `push` warmer would repeat substantial compiler/linter work after the authoritative queue, so the
  bounded next optimization is shard assignment rather than adding a third validation lane.
- The specialized classifier now publishes shadow `impact_*` decisions and a comparison table in
  the `changes` job summary. Legacy broad outputs remain the only selectors until shadow evidence
  and regression fixtures establish that every affected gate is retained.
- PR #468 merged the first live shadow report with every legacy-selected job green and a 3m32s
  critical path. Auditing those full outcomes exposed that the native Windows job was absent from
  `ci-ok.needs`; its failure could therefore have been invisible to the protected `CI` result. The
  next regression fixture makes aggregate completeness structural rather than hand-maintained.
- PR #469 restored Windows to the required aggregate and added a parser-backed completeness guard;
  its full matrix passed with the now-load-bearing Windows result green in 1m32s. Exact path-set
  fixtures now pin both selected and intentionally unselected specialized gates across every gate
  family, turning subsequent shadow mismatches into explicit regression cases.
- PR #470 merged those exact fixtures. Its script-and-docs change proposed only contracts and docs,
  while the authoritative legacy family also ran three Go shards (up to 3m21s), Postgres (2m04s),
  Windows (1m59s), and image certification (2m30s), all green. This is measured safe
  over-selection rather than a false negative.
- Live branch-protection verification on 2026-08-23 reports `strict: true` for the sole required
  `CI` check, still bound to the GitHub Actions app (`app_id: 15368`). Merge-queue activation is
  blocked by GitHub's organization-ownership requirement, not by workflow readiness:
  `merge_group` and its merge-base handling are already present.
- The pre-activation input audit found non-code files read by Go tests that neither the legacy nor
  shadow maps fully represented: design/configuration/generated-command docs, install docs and
  README, committed OpenAPI, and production Compose. It also found release-contract consumers for
  Dockerfile and packaged notices. Each now selects its consuming gate and has an exact fixture;
  PR #472 passed the complete legacy matrix and merged.
- The repository transferred from the personal namespace to the lowercase organization namespace
  `loomarr/loomarr` on 2026-08-23. Public visibility, the `main` default branch, Actions, the release
  and tag, open work, and strict app-bound `CI` protection survived the transfer. Source, module,
  image, documentation, and local Git identities move together in the repository-identity slice.
- A 20-run post-client sample measured a 68.8-minute native-client wall p95 and 146.9 occupied
  runner-minute p95, compared with 13.2 and 37.8 minutes for backend/repository runs. Per-run CI
  summaries now expose queueing separately from execution so runner scarcity is not mistaken for a
  compilation-cache problem.
- The shadow classifier now separates shared-client, iOS, tvOS, Expo Android mobile, and Expo
  Android TV decisions. Exact fixtures cover app-local, shared-package, native-script, workspace,
  and OpenAPI inputs; unknown paths still select every gate.
- PR #560 merged that split with the complete 24-result matrix and strict aggregate green. Its live
  timing report measured 42.8 minutes end to end and 114.2 occupied runner-minutes: mobile Apple
  alone consumed 41.5 minutes, tvOS 18.3 minutes, and the shared-client job 0.7 minutes.
- The first activation audit found that `make test-pg` directly runs `internal/backendtransition`
  while the shadow Postgres map did not select that package. The activation therefore broadens the
  initial Postgres decision to every Go source, pins the missed package in the exact fixture, and
  adds a structural verifier before replacing the legacy selector. This still removes non-Go
  over-selection and leaves dependency-aware Go narrowing for a separately observed slice.
- PR #561 merged that Postgres activation after the complete required matrix passed against current
  `main`. Its authoritative run completed in 29.1 minutes and 104.2 occupied runner-minutes; tvOS
  was the 26.8-minute critical path and mobile took 23.6 minutes. The activation review also caught
  and prevented an accidental expansion of the proportional release-candidate scope.
- The Playwright activation audit found shadow false negatives for extensionless runtime helpers,
  committed PNG baselines, Vite configuration, shared API/core/fixture packages, and OpenAPI. The
  conservative first selector treats every shipping Web runtime source as visual-sensitive, keeps
  unit-test-only sources outside the browser job, and structurally requires the visual/e2e union
  before replacing the broad Web selector. Tuner remains shadow-only for its own reversible slice.
- PR #563 merged the Playwright activation after a complete current-main matrix and strict aggregate
  passed. Its authoritative run completed in 38.8 minutes and 119.8 occupied runner-minutes; mobile
  Apple took 37.2 minutes and tvOS 30.9 minutes while every non-native job completed within 5.0
  minutes. The tuner activation therefore remains a separate change, and the native split/cache
  work retains this per-platform baseline rather than hiding it inside a reshaped matrix.
- The tuner activation audit keeps every shipping Web runtime source tuner-sensitive because the
  three-browser performance matrix loads the real SPA and HLS controller. Exact fixtures prove that
  unit, spec, and story-only source changes may skip tuner, while tuner e2e inputs, browser build
  configuration, shared API/core/fixtures, runtime tokens, and OpenAPI retain it. A structural
  verifier rejects the legacy broad Web selector, detached classifier output, and
  release-candidate broadening.
- PR #567 merged the tuner activation with its dedicated macOS matrix green in 3.1 minutes after a
  20.8-minute runner queue. The complete run took 43.2 minutes and 104.5 occupied runner-minutes;
  mobile Apple executed for 33.4 minutes after 9.5 queued, while tvOS executed for 20.3 minutes
  after 10.4 queued. Queue and execution therefore remain separate native budget inputs.
- The first native activation replaces the combined Apple matrix with independently required iOS
  and tvOS jobs, hard-codes each app command, and preserves the evaluated cache-key strings. Only
  iOS consumes its specialized decision in this slice; tvOS retains the broad client selector for
  a separate activation. The structural verifier rejects app swaps, a restored matrix, a detached
  mobile output, or fallback to the legacy selector.
- Repeated fresh-worktree bootstrap failures exposed that Cargo and pnpm changed into the selected
  worktree but `go run ./cmd/dev-bootstrap` inherited the caller's checkout. A harness regression
  now records the Go command's working directory, and bootstrap roots all three toolchains in the
  selected worktree before native CI work proceeds.
- PR #569 merged the Apple split and mobile activation after a refreshed current-main matrix. Its
  authoritative run took 31.6 minutes end to end and 109.4 occupied runner-minutes; mobile executed
  for 30.6 minutes and tvOS for 24.3 minutes with negligible queueing. The tvOS follow-up consumes
  the already-shadowed `apple_tv` decision and adds fail-closed verifier mutations for legacy
  fallback, detached output, and release-scope broadening without changing manual release scope.
- PR #571 merged the Apple TV activation after a refreshed current-main matrix. Its authoritative
  run took 45.9 minutes end to end and 125.9 occupied runner-minutes; mobile executed for 42.2
  minutes and tvOS for 29.7 minutes. This is above the 20-minute native feedback target and below
  the 53.3-minute pre-activation longest-job p95, so further cache/build work remains required.
- Local Expo Android activation exposed a second false safety assumption before a new CI job could
  consume it: AGP executes generated Ninja commands directly, so
  `CMAKE_BUILD_PARALLEL_LEVEL=1` did not constrain Reanimated and six compiler children stalled a
  4 GB scope for more than 30 minutes. The corrective generator registers depth-one CMake compile
  and link pools on every Android application and library subproject at plugin-application time.
  A dependency-cold local mobile build completed in 6m07s with one live Clang child. After the
  generated pool was parameterized and both app projects were regenerated, the final mobile and TV
  targets completed in 4m43s and 2m57s. PR #573 merged the correction after a refreshed
  current-main matrix; Expo Android jobs remain unactivated until their exact app/platform
  selectors are introduced in reversible slices.
- The supported release artifact is Linux on amd64 and arm64; Windows has no supported native
  distribution. The dedicated Windows runner, its shadow decision, and the local Windows
  cross-compile therefore spent assurance time on a platform outside the product contract. They
  are retired together while the structural `ci-ok` verifier continues to require every remaining
  top-level job.
- A 2026-08-27 burst exposed the next orchestration bottleneck: one client branch launched and
  superseded roughly a dozen CI runs in under three hours while selected macOS jobs across unrelated
  PRs queued for tens of minutes to more than two hours. Strict up-to-date protection then made
  successful long-running PR evidence stale whenever another branch merged. Ruleset `21647889`
  activates a squash, `ALLGREEN`, one-build/one-merge queue for `main`. Ordinary PR runs no longer
  admit Apple mobile, Apple TV, tuner, or macOS harness jobs; those remain required on
  `merge_group` and manual runs, guarded by `VerifyCINativeAdmission`. A later lane-ownership
  correction makes merge groups authoritative for all expensive affected integration gates and
  removes the redundant product-validation run from queue-produced main pushes.
- A 2026-09-02 seven-entry burst exposed the cost of keeping that queue permanently serialized. One
  native-client merge group took 22.9 minutes and left the fifth entry with GitHub's 2,577-second
  estimate even though ordinary queue runs measured 7.6–9.1 minutes. Build concurrency is bounded at
  two: merge limits do not combine CI builds, and a larger speculative window would multiply the
  cumulative rebuilds invalidated by an earlier failure. The live policy checker preserves squash,
  `ALLGREEN`, single-PR merges, no bypass actors, and the three-hour response timeout while changing
  only this throughput control.
- The first admission PR exposed a separate over-selection defect before merge: changing the CI
  workflow itself selected every product family, so unchanged Rust, Android, Apple, frontend,
  browser, image, Postgres, and Go code rebuilt. The live run was cancelled. CI orchestration now
  selects a dedicated policy job, every product job consumes its own exact `impact_*` decision, and
  the whole admission branch is pinned to `docs,agent,policy` by a regression fixture.
