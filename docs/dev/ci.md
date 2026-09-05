# CI

![The CI classifier selects specialized gates which converge on one required aggregate](../diagrams/generated/ci.svg)

*[D2 source](../diagrams/ci.d2)*

Each family contains independently filtered jobs. The required aggregate always runs and checks
every top-level result, including jobs that correctly skipped.

## Fast PR feedback, authoritative queued integration

Ordinary pull-request pushes run affected policy, repository-contract, static-analysis,
compile/type, unit, documentation, shared-client, and Android feedback and return the required `CI`
result quickly. They do not run race-policy shards, Postgres conformance, Playwright, release-image
builds, runtime image certification, Apple mobile, Apple TV, the tuner matrix, or the macOS harness.
The required `main` merge queue then builds at most two cumulative candidates concurrently against
the current base. That
`merge_group` run is the authoritative integration lane: the same fail-closed classifier selects
the complete affected gate set, and every selected result remains a dependency of `CI`. Explicit
manual runs retain release-candidate and full recovery scopes plus an isolated Apple compilation
cache portability scope. A normal queue-produced push to
`main` runs publication workflows only rather than validating the admitted commit again.

The root workflow owns triggering, classification, admission, manual scopes, and the required
aggregate. Product job implementations live in family-named reusable workflows, so an edit to one
family selects only its owning product gate plus the lightweight **CI policy** job. Workflow
adapters, the release-policy verifier, agent harness, and design-contract prose likewise select
policy plus only the family they actually affect. They do not select unrelated Rust, Android,
Apple, image, browser, frontend, Postgres, or application-Go builds. The root workflow selects
policy, where structural verification covers admission, aggregation, and family wiring without
rebuilding unchanged products. The root Make interface and unknown paths still select everything.

The Go repository-contract job keeps the stable `make privacy-verify` interface, but its captured
fixture guard is one in-process tracked-tree scan in `releaseverify`. The previous shell loop
started hashing processes for every unique candidate and measured 54 seconds locally by itself;
the same case-folded SHA-256 policy now measures about two seconds, reports only labels and digests,
and keeps household-name/profile-PIN candidates restricted to the four audited response fixtures.

Make uses the same ownership boundary. The root `Makefile` is the stable interface for shared
variables, help, and ordered `mk/*.mk` includes; each module owns one command family. The generated
command reference follows those includes and rejects missing, cyclic, escaping, or duplicate target
definitions, so splitting the physical files does not split the public command catalog.

This is admission control, not weaker assurance. A pull request cannot merge directly after its fast
result: it must enter the queue and pass the generated current-base commit. The queue uses squash
merges, `ALLGREEN`, two concurrent builds, one PR per merge, and a three-hour check-response timeout.
The concurrency cap is deliberately two: the serialized policy turned one 22.9-minute Apple build
plus six ordinary queued changes into a 43-minute estimate, while unbounded parallelism would
multiply cumulative builds and their invalidation cost when an earlier entry fails. Two removes the
single-build head-of-line bottleneck without admitting a burst onto every Apple runner. The queue
still gives accepted work a stable place in line instead of repeatedly invalidating successful
strict-mode runs. `scripts/ci-merge-queue-policy.sh` checks the live ruleset; `APPLY=1` updates only
its build-concurrency field and then re-checks every admission parameter.

The policy has three coupled halves:

- GitHub ruleset **Main merge queue admission** targets `refs/heads/main`.
- Branch protection applies the strict, Actions-owned `CI` requirement to administrators as well as
  ordinary contributors, so removing post-merge validation does not create a bypass path.
- `.github/workflows/ci.yml` triggers on `pull_request`, `merge_group`, and explicit manual dispatch;
  it does not trigger product validation for a normal `main` push.

`releaseverify.VerifyCINativeAdmission` rejects loss of the queue trigger, renewed PR admission, a
queue-only condition that drops manual evidence, a restored post-merge product trigger, or a missing
scarce-capacity job. The live ruleset is verified through `scripts/ci-merge-queue-policy.sh` and
GitHub's branch-protection API when changing repository protection.

## Jobs run only when their inputs changed

A `changes` job diffs against the merge base and each job gates on its output. It fails safe: no
usable merge base — first push, force-push, new branch — runs everything.

**Adding a new build input means adding it to the filter in the same PR.**

Two non-obvious entries: `docs/help/` is in the Go filter because those pages are embedded and
the doc-claims test reads them, and `scripts/` is there because the job executes them.

### Specialized gate classifier activates one job at a time

The `changes` job also runs `scripts/ci-impact.sh` and publishes `impact_*` outputs for the
specialized contract, Go, full-Go, Rust, Postgres, web, shared-client, iOS, tvOS, Expo
Android mobile, Expo Android TV, visual, e2e, tuner, image, docs, agent, and legacy Android TV
gates. Its run summary places those proposed decisions beside the current broad families.

Postgres was the first active specialized output. `store-postgres` consumes `impact_postgres`
directly while remaining in the required `CI` aggregate. The explicit release-candidate scope
continues to exclude database conformance. This first activation intentionally treats every `.go`
file plus `go.mod`, `go.sum`, store migrations, and unknown paths as Postgres-sensitive. That
conservative boundary skips proven
non-Go over-selection without guessing which transitive Go dependency can change a real-Postgres
assertion. Dependency-aware narrowing is a later shadow change.

### Container acquisition is bounded before product tests

Container acquisition has one retry boundary: `scripts/ensure-container-image.sh`. A locally
inspected image returns without a pull. Otherwise the helper makes at most five pull attempts with
exactly two seconds between attempts, fails immediately if a wait cannot run, and propagates the
fifth pull failure status. The retry ends before `docker run` or testcontainers starts product tests;
product execution is never retried.

The Playwright runner `scripts/run-playwright-container.sh` owns its fixed image identity. Each Make
target first invokes its bounded ensure mode, then one product mode. Its reviewed source builds
Docker arguments as a shell array, places the image at a fixed boundary, and accepts only a mode plus
the validated `LOOMARR_PLAYWRIGHT_SHARD=--shard=N/M` visual-suite input. The Postgres pin has one
Make-readable and Go-embedded authority at
`internal/testkit/postgresimage/image.txt`; direct Postgres-module `Run` calls must use
`postgresimage.Name()`. Release verification structurally pins that owner to one private
`//go:embed image.txt` string and the single `strings.TrimSpace(image)` return; missing, transformed,
environment-derived, or alternate implementations fail closed. Both owner and data-file changes
select the contracts, Go, full-Go, Postgres, image, and policy gates. The package must contain
exactly one untagged non-test production Go file, `image.go`; build constraints, another production
file, or a second embed/`Name` owner fail closed, while `_test.go` files do not participate in
production ownership. The verifier scans non-generated Go throughout the repository, including
`cmd/`, tooling, and future top-level packages while excluding only vendor and named generated/cache
trees. Deprecated `postgres.RunContainer` is forbidden because its image cannot use the repository
authority. `postgres.Run` admits only the repository's direct database, username, password, and wait
customizers; opaque, variadic, nested, image-replacing, pull-policy, or shadowed-package customizers
are rejected. Generic testcontainers `Run` admits no customizers. `GenericContainer`, request
aliases, pointer aliases, request builders, and construction-time or later `ContainerRequest.Image`
assignments accept an explicit unrelated image literal, but Postgres-bearing or opaque values must
use that authority. `AlwaysPullImage` and `ImageSubstitutors` are forbidden at construction or later
mutation so request objects cannot restore unbounded acquisition.
The direct AST audit classifies ordinary, multi-result, compound, and range-key/value writes to
whole requests or `Image`, including nested selectors, dereferences, and parentheses. A pointer to
either target may not cross an opaque call boundary, directly or through a tracked alias; this also
rejects direct reflection and unsafe mutation paths. Request or Image pointers may remain direct,
tracked local aliases, but cannot enter slices, arrays, maps, structs, interfaces, channels, returns,
or opaque aggregate calls; aggregate indexing or selection therefore cannot become an unaudited
mutation path. No pointer-taking authority seam is approved:
the Postgres authority is the value-returning `postgresimage.Name()`. This is a direct structural
contract, not a claim to infer arbitrary reflection, unsafe, return-value, or interprocedural
dataflow. Dereferencing a tracked pointer retains whether it targets a whole request or only `Image`,
so ordinary, compound, multi-result, and range writes cannot erase that authority distinction.
Taking acquisition functions as values is rejected because their arguments can no longer be audited.
The alternate exported acquisition surfaces are closed too: `ParallelContainers` and the
`DockerProvider.CreateContainer`, `ReuseOrCreateContainer`, and `RunContainer` methods are rejected
rather than accepting opaque requests or pull policies outside the audited `Run` and
`GenericContainer` seams.
Postgres image literals are also rejected outside count-bounded exact verifier-fixture values and the
one exact unrelated Authentik direct-Docker fixture value; no whole Go file is exempt.

Testcontainers' cleanup image is separately pinned in
`internal/testkit/ryukimage/image.txt`, acquired through the same bounded helper, and exported to
testcontainers only by the exact `test-pg` recipe while Ryuk remains enabled. The Ryuk pin is a
fully-qualified `docker.io` name; the Postgres pin is its implicit `library/...` form because the
product recipe sets the exact non-empty
`TESTCONTAINERS_HUB_IMAGE_NAME_PREFIX=docker.io`, which takes precedence over an operator's
`~/.testcontainers.properties`. Testcontainers-go v0.44 prepends that runtime authority exactly
once to the implicit Postgres pin, while Docker resolves the helper's same implicit form to Docker
Hub; an inherited prefix therefore cannot transform either product image into a different pull.
`TESTCONTAINERS_RYUK_DISABLED=false` likewise overrides a user property that would disable cleanup.
The Postgres and Ryuk recipes read their repository-owned pin files directly; no environment or
command-line Make variable can replace either authority. A
focused contract binds the qualified Ryuk file to the installed testcontainers module's exported
default plus the exact Hub prefix, so a dependency update cannot silently introduce a second image.
Each product Make target depends directly on one shared ensure target.
Release verification requires the ensure and protected product targets to remain phony, checks each
protected target's own exact recipe, and closes the full prerequisite graph over an exact
target/recipe allowlist: the five Playwright targets may
reach only their shared ensure target and their existing Storybook or frontend build prerequisite,
while `test-pg` may reach only its shared ensure target and the Rust worker build. A wrapper or
changed prerequisite therefore cannot hide another runtime, helper, service, pull, or alternate
container acquisition. The five Playwright recipes and their ensure target invoke only fixed runner
modes; removed `PW_DOCKER_USER`, `PW_CI`, `PW_REAL_CI`, `PW_IMAGE`, and `PW_SHARD` variables are
unexported and forbidden as Make assignments. Command-line, environment, or recursively expanded
values therefore have no pre-image argument seam. Make's dependency graph executes the shared phony
ensure target once even when the Playwright targets are selected together with `-j`.
Every Make recipe that invokes the bounded helper, reviewed runner, a fixed
Docker/Podman/nerdctl/Buildah/docker-compose executable, or a variable engine with container grammar seeds
the workflow-protected target set, regardless of global options, Compose options, or the eventual
verb. GNU Make's `target: prerequisites ; recipe` form is parsed as an inline recipe rather than as
prerequisite text. Backslash-newline recipe continuations are lexically spliced into one logical
command before that classification, including commands reached through reverse prerequisite closure.
Exact active literal Make assignments are resolved only for command classification. Outside recipes,
GNU Make's first unescaped `#` begins a comment even when it is adjacent to a value or prerequisite;
an escaped `\#` remains data. Recipe and inline-recipe text uses the shell's distinct comment rule,
where an adjacent hash remains part of its token. Audited non-recipe Make source rejects logical
continuations, `define`, and parse-time `eval` rather than partially interpreting those contexts. An
unresolved Make command variable followed by container grammar fails closed.
An unresolved variable in recipe command position also seeds the protected set, so a helper or
runner supplied through a Make command-line or environment override cannot hide behind
`$(RUNNER)`. The root's exact `GO` and `CARGO` assignments are resolved before that check, and
recursive `$(MAKE)` recipes are followed through the protected-target graph, so known benign tools
do not become container routes. Literal shell command substitutions inside recipes are traversed
through the same recursive-Make closure. Recursive Make admits only a positive numeric `-jN` or
`--jobs=N` parallelism option and no variable assignments; even a non-graph assignment can retarget
a command-position variable in a reachable recipe. Leading command-prefix assignments and
assignments consumed by nested `env` wrappers remain attached to the effective recursive Make
invocation, so wrappers cannot erase that fact. Alternate Makefiles, directories, includes,
evaluation, execution controls, dynamic option values, and every unknown option fail closed because
they can select a different graph or executable. The shared
command walker consumes the closed option grammar for `env`, `command`, `exec`, `timeout`, `nice`,
and `xargs` before classifying the effective executable; a wrapper operand cannot be mistaken for
the command. Shell `eval` bodies are recursively classified, with only the exact checked-in
`dev-env.sh export` import admitted for the image tooling recipes. Release verification never launches a host Make executable. A checked-in minimal
Make-policy fixture and golden normalization/closure snapshot preserve the security-relevant
adjacent/escaped-hash boundary, prerequisite graph edges, inline and continued recipes, shell-recipe
hash tokens, recursive protected-target reachability, and untrusted alternate-Makefile rejection.
The policy parser canonicalizes harmless surrounding whitespace, including the trailing space GNU
Make retains before a whitespace-delimited comment; the fixture is not a claim of byte-for-byte GNU
Make evaluation. Independent public mutations own continued assignment and `define`/`eval` rejection,
parse-time side effects, and untrusted recursive graphs. This keeps the required policy hermetic
across Linux, Homebrew, and the macOS system Make handoff.
This includes the existing local `dev` and `dev-gpu` Compose targets. Reverse prerequisites and
recursive Make recipes close that set transitively. Classification grants no new authority: a
workflow route to an arbitrary engine-bearing target is rejected; only the exact audited Postgres
and Playwright commands below may reach their separately verified image authorities.
Repository shell scripts invoked by a reachable Make or workflow route are indexed and traversed
recursively. Literal paths, transparent wrappers, prefix assignments, shell interpreters, and unique
repository-script suffixes share one bounded 32-edge, cycle-safe traversal. Docker, Podman, nerdctl,
Buildah, Compose, variable-engine acquisition grammar, `if`/`then` bodies, and input or output
process substitutions are classified without executing the script. A cycle is not acquisition by
itself, but any acquisition-bearing member poisons the complete reachable cycle.

The reusable Postgres workflow retains one exact `make test-pg` step. Across every job and step,
release verification admits only the current workflow, `run` job, and exact step schemas. It rejects
job containers, services, unaudited reusable-workflow delegation, container-acquiring actions, unknown keys,
metadata-only or malformed steps, multiple execution mechanisms, and unapproved environment,
defaults, condition, error-tolerance, shell, working-directory, or `MAKEFLAGS` controls. The root
tool-version environment and the cache-save condition are admitted only at their exact current
values. Its only permitted shell steps are the exact cache-epoch command and `make test-pg`; this
closes dry-run, variable, wrapper, and quoting indirection without parsing arbitrary shell. Every
repository workflow is also parsed structurally. Any service or job container or `docker://` action
is rejected. Workflow shell is not interpreted: route-shaped steps are classified from the raw
scalar and statically declared workflow, job, and step environment, then compared with exact
workflow-name-and-whole-scalar authorities. The classifier recognizes Make by executable basename,
including absolute paths and wrappers, the bounded helper and Playwright runner wherever they occur,
and fixed Docker, Podman, nerdctl, Buildah, and Compose acquisition grammar across quoting and
command-substitution text. It applies the shell's backslash-newline splice before classification, so
an engine, helper, runner, or Make executable cannot be split across YAML scalar lines. Command
and process substitutions are inspected recursively without evaluation; shell control words cannot
hide an engine command, and ambiguous acquisition-bearing syntax fails closed. Ambiguous nesting and a variable
executable inside any nested `env`, `command`, or interpreter command fail closed. Any variable in
executable position is rejected generically, including adjacent or braced construction and
invocation through `exec`, `source`, `.`, quotes, or wrappers. Legacy backtick substitution is not
evaluated or partially parsed: balanced, unbalanced, escaped, nested, wrapped, and CRLF forms all
fail closed unless the entire checked-in step matched an earlier source-bound authority. A
candidate step containing assignments, variable executables or targets, command substitution,
control operators, or any other added shell context is not evaluated and cannot match an authority;
it fails closed.

The only bounded test-image download routes are
the exact `make test-pg` step
in `ci-postgres.yml`, the exact visual shard invocation, and the exact `make e2e` step in
`ci-playwright.yml`; `ci-image.yml` retains one source-exact local packaged-image metadata inspection
that cannot pull an image. Release publication's two existing manifest/signing scripts are also
source-bound exact authorities because their audited bodies use Docker buildx manifest commands;
they do not become test-image acquisition authorities and cannot be replaced by an arbitrary script.
Each bounded test-image authority is required exactly once at its declared
workflow, job, absolute `steps[]` index, step kind and name, whole command, target set, and inherited
execution context. Pinned action steps count in that absolute index, so inserting, removing, or
moving either a shell or action step cannot preserve an authority accidentally; removal,
duplication, movement, relabelling, swapping, or an extra Playwright mode fails repository verification.
Every registered run authority, including container-free harness and documentation commands, is
also required exactly once. Its job name, runner, dependencies, timeout, and typed strategy/matrix
shape are source-bound alongside the existing workflow/job/step environment, permissions,
condition, shell, and working-directory controls; absence, addition, or replacement changes the
authority rather than silently changing where it executes. Every pinned action occurrence in every
checked-in workflow is a complete exact authority at its workflow, job, and absolute step index.
Its step kind, name and id, full-SHA `uses` scalar, complete `with` inputs, condition, and environment
must match the checked-in authority; the repository inventory proves both that no action occurrence
is unregistered and that no registered workflow or occurrence disappeared. Action-only workflows
receive the same exact workflow/job environment, defaults, permissions, runner, dependency,
timeout, deployment-environment, and typed strategy/matrix audit. Integer authorities must remain
YAML integers rather than numerically equivalent quoted strings. An action-to-action or action-to-run
same-slot replacement is therefore rejected without resolving the action over the network.
Overrides or additional shell syntax make those routes non-exact and are rejected. Existing
unrelated Make steps are separately source-bound by workflow name, exact whole
scalar, and their explicit target list. They remain admitted only while the independent Make graph
proves every named target is outside the acquisition-bearing reverse closure; changing an Android,
cache-cleanup, Go, or other admitted target into a container route therefore invalidates the workflow
authority. This is a closed checked-in command grammar, not arbitrary shell data-flow interpretation.
Every protected and currently unrelated Make authority binds the workflow name, job name, exact run
scalar, absolute `steps[]` index, run-step kind and exact name, explicit target list, and complete
inherited workflow/job/step execution context together.
Workflow and job conditions, step conditions, legitimate step environments, and the packaged-image
inspection's `bash` shell are admitted only at their exact current values. Any additional or changed
environment, defaults, condition, error tolerance, shell, or working directory rejects the authority.
Thus `MAKEFLAGS`, `GNUMAKEFLAGS`, `MFLAGS`, `MAKEFILES`, `MAKEOVERRIDES`, `MAKELEVEL`, `MAKE`, `SHELL`,
`PATH`, `BASH_ENV`, `ENV`, `GOENV`, `GOFLAGS`, and equivalent controls cannot redirect or suppress an
exact-looking command. An exact command is authorized only at the scalar node inside a real
`jobs.<job>.steps[]` entry whose complete inherited context passed this audit; matching text in a
malformed or metadata-only YAML shape receives no exemption.
Job-level `uses:` is rejected except for the root CI workflow's exact local family calls. Those
calls admit exactly `name`, `needs`, `if`, and `uses` at their source-bound values and are closed by
`VerifyCIFamilyWorkflows`: each job must name its registered local workflow and the target must
expose only `workflow_call`. Product families contain exactly one implementation job. The isolated
Apple cache portability workflow is the sole registered exception: its exact producer and dependent
consumer jobs prove transfer across distinct runners. Every implementation job is scanned
independently by this container policy. Remote,
renamed, additional, or otherwise unresolved reusable workflows fail closed.

The workflow topology is itself an authority: the checked-in workflow filenames, job names, step
counts, and run-versus-action kind at each slot must match exactly. An extra workflow, job, or step
therefore fails before command classification; pinned actions and registered run authorities retain
their deeper exact content and context checks.
Topology, run commands, pinned actions, job contexts, reusable callers, and family-workflow paths
are constructed together through one typed catalog. Each verification receives fresh maps rather
than package-mutable authority state, and a drift contract rejects missing workflows, jobs, contexts,
actions, commands, or family registrations and colliding absolute step identities.
The cache-cleanup workflow is one such run authority: its complete deletion command, step name and
environment, exact `Cache cleanup` workflow name, closed `pull_request` trigger with only the
`closed` type, workflow permissions, job name and runner, and absence of alternate execution context
are source-bound rather than inferred from its one-step topology.

Make execution is closed over the checked-in root `Makefile` and every `mk/*.mk`: each module must be
included exactly once, and non-recipe syntax is limited to the current plain assignments, targets,
and exact root includes. Conditional directives are rejected rather than interpreted; define/endef,
dynamic evaluation, modifiers, alternate includes, and non-recipe logical continuations are also
forbidden. Recipe continuations are instead normalized as shell lexical splices and audited as one
logical command.
Every parenthesized or braced Make reference in assignments, targets, and every logical recipe is
structurally balanced. Escaped `$$` recipe expansions remain shell-owned, while Make-owned references
are inspected before shell command classification. Ordinary variable references and the current pure
`if`, `or`, `sort`, `subst`, and `wildcard` functions are allowed;
`shell`, `eval`, `file`, Guile, `!=` shell assignments, `call`, dynamically constructed function
names, `value`, `foreach`, and any other function are rejected even when nested or continued inside
an otherwise allowed assignment, recipe, or pure function. Playwright host-user discovery now occurs inside the exact reviewed runtime script,
so Make has no parse-time shell exception. The Postgres image pin remains one Go-embedded and
Make-readable file, and the Ryuk pin is a second repository-owned testkit file. Make reads both by
literal path with recipe-time `cat` immediately before the bounded helper rather than exposing an
overridable variable or exempting a parse-time `file` function.
`GO ?= go` and `CARGO ?= cargo` are fixed, and global shell, executable, flag, recipe-prefix, and
special-target controls that could suppress or redirect a protected recipe are forbidden.
The `ci-policy` job has a source-bound bootstrap chain: exact pinned checkout and setup-go actions,
then `go run ./cmd/releaseverify -root .` as the first shell step, followed by the three exact Make/Go
policy commands. The workflow root admits only exact Go and Node version environment values and no
defaults; the job and all six steps admit no inherited environment, alternate shell, working
directory, tolerance, or unknown execution keys; the job's existing needs, condition, and runner are
source-bound, and no step may add a condition. Setup-go's exact inputs are also bound, so a preceding
action cannot redefine the Go executable or path. `BASH_ENV`, `ENV`, `PATH`,
`GOENV`, `GOFLAGS`, constructed values, or an alternate Make/runtime context therefore fail before
the checked-in policy can claim protection. The exported direct-bootstrap verifier enforces this
independently and `VerifyCIContainerDownloads` composes it into repository policy. A Make-only
mutation cannot suppress this audit. This bootstrap still has an unavoidable
self-verification boundary: the required-check configuration, GitHub Actions runner, and review of a
change that removes or poisons the direct step must cause the verifier to run before the repository
can inspect itself. Release verification therefore proves the checked-in structure once invoked; it
does not claim authority over the hosting platform or an execution that never starts.

The public verifier admits only the exact reviewed helper and Playwright runner sources,
so no test-only package links into `cmd/releaseverify`. Tests execute that source through the shared
testkit process boundary with fake local Docker and sleep executables to prove exact image arguments,
cached zero-pull behavior, the five-attempt/four-wait bound, wait failure, and final-status
propagation without a registry or network.

The helper's classifier consumers are repository contracts, Postgres, visual, e2e, tuner, and CI
policy. The Playwright runner selects repository contracts, visual, e2e, tuner, and CI policy. The
broad Web unit gate is not selected: its `make fe` path never invokes either script, while each
container-backed Playwright target and `test-pg` owns an explicit prerequisite.
Any production or test change under `internal/releaseverify` or `cmd/releaseverify` selects both the
policy job and the sharded Go job, because only the latter executes the package's RED-capable tests.

Playwright is the second active job. Its four shards consume the union of `impact_visual` and
`impact_e2e`; the combined job still runs both suites exactly as before. Shipping Web runtime
sources are conservatively visual-sensitive because Storybook alias imports make filename-only
transitive narrowing unsafe. Shared API/core/fixture inputs and OpenAPI select both suites, while
visual/e2e tests and committed baselines select their owner. Only proven unit-test-only Web sources
skip Playwright in this first slice.

Tuner is the third active job. Its macOS browser matrix consumes `impact_tuner` directly. Every
shipping Web runtime source remains tuner-sensitive because the matrix loads the real SPA and HLS
controller; unit, spec, and story-only modules may skip it. Tuner e2e inputs, browser build
configuration, shared API/core/fixture/player packages, runtime tokens, and OpenAPI select it
explicitly.

Apple mobile is the fourth active decision and Apple TV is the fifth. iOS and tvOS are separate
top-level jobs with hard-coded app commands, dedicated impact selectors, and independent required
results. Existing cache-key strings are preserved so splitting job identity does not discard
compatible pnpm, CocoaPods, or ExpoModulesJSI entries.

Every job consumes its dedicated `impact_*` output. A missing base, classifier failure, or unknown
path selects every specialized gate. The manual release-candidate scope remains unchanged and
excludes Postgres, Playwright, tuner, and application-client builds.

`scripts/testdata/ci-impact.tsv` records the exact ordered gate set for representative paths and
multi-path changes across every specialized gate. The classifier contract test compares complete
sets, so both a missed gate and an unexplained extra gate require an explicit fixture decision.

Non-code files consumed by Go tests are Go inputs too. In particular, design/configuration/command
docs, install docs and README, the committed OpenAPI document, production Compose, and embedded
help select the complete Go test set. Dockerfile and packaged licence/notices select repository
contracts as well as the image build. These mappings are fixtures because file extensions cannot
reveal those dependencies.

Client decisions follow the actual consumer graph. An app-local mobile change selects shared-client,
iOS, and Expo Android mobile evidence; a TV change selects shared-client, tvOS, and Expo Android TV.
Changes to `api`, `core`, `fixtures`, `player`, `design-system`, or `ui` select both apps on both
native platforms because those packages are transitive inputs to both. The shared player package
also selects Web, Tuner, Image, and Android TV because its browser and native adapters share one
transport contract. Browser-only client-proof and Turborepo contract changes select the shared
JavaScript gate without spending a native runner.
Apple mobile and Apple TV are active. Expo Android mobile and Expo Android TV remain classifier
decisions until each has an independently required job and current-main evidence.

## Per-run measurements

The required `CI` aggregate appends a timing table after it has evaluated every required result.
`scripts/ci-run-metrics.sh` reads GitHub's run and job records and reports queue delay, execution
time, end-to-end time, the longest job, and total occupied runner time. The report is observational:
an API or checkout failure emits a warning but cannot turn verified code red or hide a failed gate.
Its formatter is tested against a pinned API fixture without touching the network.

The distinction between queue and execution is load-bearing for native work. A macOS job can be the
critical path because it waited for capacity, because Xcode compiled slowly, or both; changing cache
policy cannot solve the first case, while adding shards can make it worse.

## The image job is the exception

The image filter follows every source family copied by the Dockerfile: Docker metadata, packaged
LICENSE/notices, Cargo and Rust sources, Go sources/modules/embedded migrations, embedded help, the frontend,
OpenAPI, and the bundle guard. `Makefile` and workflow-only changes do not change image bytes and
therefore do not trigger it.

It builds each release platform on a native runner, loads the resulting image without pushing it,
and inspects the packaged LICENSE/notices and OCI labels. The Dockerfile's build-time commands prove
the bundled tools; the post-build inspection proves the final runtime filesystem rather than a
comment or an intermediate stage.

The image job has a `timeout-minutes` because its release builds are independently bounded; GitHub's
default is six hours.

The reusable Apple mobile and Apple TV jobs have explicit job limits of 75 and 60 minutes. These
bounds are evidence-derived from recent successful merge-group runs (mobile: 29:14–50:46, TV:
16:38–34:49) and cover prebuild, dependency installation, Xcode build, simulator readiness,
installation, and launch. Review them after at least 20 successful native runs or at the first
native timeout. A hard timeout can prevent the screenshot step from running; the screenshot remains
useful for ordinary failures but is not guaranteed after timeout.

It exists because a Dockerfile that could never build for arm64 sat undetected. Build both
platforms or it can't catch that.

## Manual scopes are explicit

Manual CI defaults to `release-candidate`. That scope is for certifying an exact `main` commit before
tagging: it runs repository contracts, the real-codec image-worker certification, and both native
release-image builds. It does not rerun Android, PostgreSQL, Go race, frontend, Playwright,
tuner, docs, or the macOS harness; their normal push and pull-request impact coverage is unchanged.

Select `full` explicitly when an investigation genuinely needs every matrix. Both modes publish a
scope-marker job, but `scripts/validate-release-source.sh` accepts only the release-candidate marker
for Docker publication. It rejects normal push CI and full manual runs even when green, so mobile,
client, and unrelated platform matrices cannot become release prerequisites. The release-candidate
marker also makes the contract and certification jobs mandatory evidence rather than relying only on
the workflow's overall conclusion.

Select `apple-cache-validation` only while proving the Apple compilation-cache protocol. It runs no
ordinary product family: one `xcode-27` job performs the complete mobile and TV Release
install-launch-liveness gates while populating an LLVM CAS, validates and packs that store, and
transfers it as a one-day workflow artifact. A dependent `xcode-27` job restores and validates the
archive before proving mobile and TV hits, a real Swift source-change hit-and-miss, corrupt-archive
rejection, fingerprint invalidation, and cold mobile/TV fallback. The runner boundary is deliberate;
a cache that works only within one machine is not eligible for later CI consumption. This manual
scope is included in `ci-ok` when selected and is skipped for pull requests, merge groups, release
certification, and `full` dispatches.

After that portability proof is green, `Apple compilation cache` is the only workflow authorized to
publish compiler results. It is manual-only, refuses every ref except `refs/heads/main`, and builds
the complete mobile and TV Release install-launch-liveness gates before saving. A restored seed is
hash-validated before use; the candidate CAS and compressed archive are validated again, and a save
is refused if its size plus a 512 MiB reserve would exceed the repository's 10 GiB cache budget.
Before installing dependencies or starting either native build, the writer also requires enough
live headroom for the maximum permitted archive plus that reserve; this conservative preflight
avoids spending the full cold-build window on a candidate that cannot possibly be published. The
post-build admission still uses the archive's exact compressed size.
After a successful save, cleanup is limited to the exact fingerprint prefix on `refs/heads/main` and
retains one generation. Only this workflow has `actions: write` for the compiler cache.

The ordinary Apple mobile and Apple TV jobs are compiler-cache consumers only. Each computes the
toolchain/SDK/native-input fingerprint, uses the restore-only cache action, and exposes a store only
after zstd and LLVM CAS hash validation. Missing or rejected archives select the unchanged cold
gate. A valid store that fails during the warm build is quarantined and receives one clean cold
retry; every architecture, install, launch, screenshot, and liveness assertion still runs. These
jobs never save the compiler archive, and the obsolete pnpm/CocoaPods and ExpoModulesJSI lookup
steps have no writer and are omitted. Pull-request and merge-queue refs therefore cannot create
sibling-scoped Apple generations that later groups cannot read.

The Apple verifier starts the selected simulator before Expo's required clean prebuild, then runs
`pod install` explicitly while the simulator boots. `simctl bootstatus` remains a hard barrier
before Expo builds, installs, or launches, and `expo run:ios --no-install` skips only the duplicate
CocoaPods invocation. The uploaded `phase-timings.tsv` records clean prebuild, CocoaPods, simulator
readiness, native build/install, and artifact/runtime assertions. Simulator readiness contains the
overlapped prebuild and CocoaPods intervals, so those rows are deliberately not additive.

Do not add a generated `ios`/Pods cache without re-proving both integrity and repository capacity.
Measured 2026-08-31, one clean mobile native workspace was 981 MiB and compressed to 339 MiB while
the repository already held 9.11 GiB of its 10 GiB Actions-cache budget. Restoring Pods alone still
pays CocoaPods project generation; restoring the integrated workspace overwrites clean-prebuild
authority. Neither seam justified consuming the remaining cache headroom.

## `ci-ok` is the only required check

It always runs and inspects `needs.*.result` explicitly. That has to be explicit: a skipped job
doesn't fail an aggregate by default, and neither does a failed one under `if: always()`.

The `main` branch requires the GitHub Actions-owned `CI` check in strict mode: a pull request must
be tested against the current base before it can merge. Preserve both the check name and its app
binding when editing branch protection.

`make release-verify` parses the workflow and requires every top-level job to appear in
`ci-ok.needs`. This prevents a newly added or accidentally removed dependency from producing a
green required check while its real job is red.

Never add a workflow-level `paths:`. A run that doesn't trigger reports no checks, so a required
check sits "expected" forever and the PR can't merge. Filter per job.

The workflow handles `merge_group`, and the organization-owned repository requires the merge queue
through its `main` ruleset. The strict, GitHub-Actions-owned `CI` check remains the protected
current-base boundary inside the queue. Never remove the queue rule merely to bypass a delayed or
failing native result.

## Sharding

Go tests, frontend and Playwright split across runners for wall-clock only. Repository-wide Go and
Rust contracts run once in `go-contracts`, in parallel with three test-only Go shards and the
independent release-worker certification. Their union is the same assurance as `make verify SCOPE=all`
plus the existing CI-only certification. The `ci-ok` aggregate requires every job, so moving a
contract out of the test shards cannot make it optional.

`make go-shard-verify` runs in `go-contracts` and asserts the Go shards are a true partition of
`go list ./...` — a split that drops a package would otherwise pass by not running it.

The Go partition is serpentine over the alphabetic package stream: an N-package row assigns
left-to-right, the next right-to-left, and so on. This keeps assignment derived from the current tree
without a package-cost table, but breaks the every-N phase alignment straight round-robin can create.
The 2026-09-01 merge-group run placed `internal/app`, `internal/channels`, and `internal/store` on
shard 1: its test step took 11m57s versus 6m30s and 5m40s. Replaying the reported package durations
through the serpentine assignment models 766/683/816 package-seconds instead of 1058/683/523. The
partition guard proves coverage, while `TestGoShardUsesSerpentineRows` independently pins the
alternating assignment.

Within the SQLite store package, conformance assertions clone one closed, fully migrated template
database rather than replaying the complete migration history for every assertion. The 2026-09-01
profile measured 169.3s for the package and 134.3s for SQLite conformance: 131 fresh stores each
replayed 86 migrations, more than 11,000 migration applications in one run. Every clone remains a
private file opened through the production SQLite adapter. Migration ordering, boot seeding,
downgrade behavior, historical data, and restart behavior retain dedicated fresh-migration tests;
Postgres conformance remains unchanged. Re-running the exact race profile with the cloned fixture
reduced the package to 40.3s and SQLite conformance to 6.2s (−76.2% and −95.4%, respectively). Five
consecutive race-enabled conformance/factory runs remained green, followed by the unchanged real
Postgres integration gate over store, backend transition, and app. In the authoritative queue, the
race steps moved from 7m42s / 6m38s / 11m30s to 9m13s / 6m35s / 5m33s: the critical path fell 2m17s
(19.9%) and aggregate race-step time fell 4m29s (17.4%), despite normal per-runner variation moving
shard 1 upward.

Domain tests that need current persistence state, rather than migration behavior, use
`testkit.MigratedSQLiteStore` for the same reason. A race-enabled 2026-09-01 profile found
`internal/channels` spending 74.46s while its helpers replayed every migration for each private test
database. Routing those helpers through the existing isolated migrated fixture reduced the exact
package profile to 4.19s (94.4%). Tests that exercise startup, migration, downgrade, historical data,
or restart behavior must continue to open and migrate fresh databases.

PostgreSQL conformance also uses an isolated template, but through PostgreSQL's own database-clone
interface rather than file copying. Seven successful merge-queue samples put the real-Postgres step
between 8m20s and 14m18s; `internal/store` dominated every sample at 366–722 package-seconds while
backend transition and app ran concurrently. The local race profile measured 124.43s for the full
integration-tagged store package and 46.23s for Postgres conformance. Its factory had dropped and
recreated `public`, then replayed all 86 migrations for every assertion. Migrating and boot-seeding
one closed template, creating a private database from it per assertion, and opening each clone
through the production Postgres adapter reduced the exact profile to 85.46s / 6.85s (−31.3% and
−85.2%). Clone cleanup closes the Store before a maintenance connection force-drops only the named
disposable database. Migration, preflight, and cross-backend data-migration tests still create fresh
databases; queue evidence remains the proportional gate.

Sharding is free on a public repo. Check the bill before copying it into a private one.

## Caching

- **`actions/cache` never overwrites an existing key.** A cache whose contents track something
  its key doesn't gets written once and frozen. Use a rolling key with `restore-keys`.
- **The 10GB cap evicts LRU across all refs**, so closed PRs' caches push out live ones.
  `cache-cleanup.yml` deletes both `refs/pull/N/merge` and matching merge-queue caches on close.
- **Default-branch cache writes require a trusted trigger.** GitHub permits a default-branch writer
  from `push` or `workflow_dispatch`, but `merge_group` and `workflow_run` cannot promote their
  outputs into that scope. The Go cache-save conditions therefore remain useful only for a deliberate
  manual run on `main`; ordinary PR and queue runs are restore-only. Do not add a post-queue artifact
  promotion workflow: GitHub makes that cache scope read-only, and a `push` warmer that repeats the
  expensive compiler/linter work would violate the publication-only main lane. Reconsider a warmer
  only with measured evidence that a bounded preparation step saves more work than it adds.

Apple compilation caching is a validated artifact protocol, not an unchecked DerivedData restore.
The fingerprint binds the runner OS and architecture, exact Xcode and Swift identities, both
simulator SDKs, Release build settings, and the pnpm lockfile. Restores are decompressed into a
temporary path and accepted only after the installed `llvm-cas` verifies hashes; failed warm builds
quarantine that exact store and retry the complete gate once in cold mode. Archive size, repository
cache usage, unrelated-cache headroom, and main-ref retention are checked before any trusted writer
may save. PR and merge-queue consumers remain restore-only, and a warm-required validation cannot
turn its cold fallback into a passing cache claim.

## Hand-maintained lists, and what guards them

Three lists in this repo are written by hand and would rot silently. Each has an executable guard
that fails when it drifts, and all three run in CI:

| List | Guard | Runs via |
| --- | --- | --- |
| `TAGS` in the Makefile | `scripts/check-tags.sh` | `make tags-verify`, part of comprehensive verification |
| Retired identifiers | `scripts/check-retired.sh` | `make retired-verify`, its own CI step |
| Release-image source-family probes | `releaseverify.VerifyCIImageInputs` | `make release-verify`, part of comprehensive verification |

`tags-verify` compares the tags in `//go:build` lines against `TAGS` and fails **both ways**:

- **In the tree, not in `TAGS`** — those files are invisible to `vet-tags` and `lint`. Nothing
  compiles them, which is how a live ffmpeg test sat uncompiled for months.
- **In `TAGS`, not in the tree** — the list claims coverage it doesn't have. Drop the tag in the
  PR that removed its last file.

Downgrading the second direction to a warning is the obvious-looking fix when it's inconvenient.
Don't — a warning printed by a job that exits 0 is one nobody reads.

The CI path filter includes `scripts/`, so a PR editing only a guard still runs the job that
executes it.

## Scheduled Rust maintenance

`rust-maintenance.yml` is intentionally outside the required PR gate. Every Monday, and on manual
dispatch, it installs pinned cargo-deny and cargo-fuzz versions, checks both Cargo lockfiles for
RustSec advisories, approved SPDX licences, and untrusted sources, then fuzzes the worker's bounded
JSON-to-decoder boundary under nightly libFuzzer. A crash retains its reproducer for 30 days.

This job is allowed to be expensive and network-sensitive. The fast deterministic protections stay
in comprehensive verification: Cargo lock enforcement, clippy/tests, and `#![forbid(unsafe_code)]` on Loomarr-owned
shipping crates.
