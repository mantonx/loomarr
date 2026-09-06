# Loomarr agent contract

This file is the canonical contract for every coding agent and human-operated harness. Agent-specific
files may add interface conveniences, but they do not override this file.

## Authority

- `docs/design.md` is the source of truth for behaviour. Amend it in the same PR before code that
  deviates from it.
- Companion design documents in `docs/` own their named domains; `CONTEXT.md` owns vocabulary.
- `PROGRESS.md` records active and shipped work. Read its **Active work** table first; do not load the
  historical tables unless the task needs them.
- Generated artifacts are changed through their generators, never by hand.
- Frontend packages are deep modules — read [`web/packages/README.md`](web/packages/README.md) before
  adding one or importing from one.

## Session lifecycle

From the worktree that will own the change:

```sh
make agent-status
make agent-worktree TOPIC=<short-name> CLAIMS=<comma-separated-shared-outputs>
# cd to the printed worktree; it is already registered
```

`agent-status` is the cross-harness roster. A product's own agent-list command is supplementary; it
cannot see agents running in other products. Before starting, resolve any overlapping task or claim.

Supervised workers receive bounded, rotating task roles and return control when the brief is done;
they do not self-assign follow-up work. Record model and reasoning choices when the harness exposes
them. Change those choices only at assignment boundaries, and never let them expand the worker's
authority, scope, claims, tools, or acceptance criteria.

For multi-PR delivery, run independent reviews and disjoint repairs concurrently, including early
review of later stack members against fixed parent commits. Preserve dependency order for merges,
one writer per overlapping seam, and final integrated-tree evidence. Follow the
[concurrent backlog procedure](.agents/workflows/supervise.md#review-a-pr-backlog-concurrently).

### Task-based model routing

Choose the least expensive model clearly capable of meeting the assignment's acceptance criteria.
Use Luna at Low reasoning by default for straightforward evidence collection: exact SHA/status and
PR/issue collection, bounded inventories, artifact existence/hash/size checks, documented-command
reproduction, and mechanical comparison against explicit acceptance criteria. Use Terra at Medium
for ordinary implementation, multi-file behaviour analysis, and investigations requiring synthesis.
Use Sol only for complex integration or high-risk or ambiguous contract reasoning, with a written
reason. Use Astra for the hardest cross-system architectural decisions, difficult competing evidence,
or a specific capability gap beyond Sol; record the expected benefit and risk justification. A
supervisor role, review label, or available pane does not justify Astra. A clearly demanding task may
start at the justified model without wasteful trial runs through every smaller model.

Select reasoning independently of model capability: Low for mechanical checklists, Medium for
ordinary implementation or analysis, and High for difficult ambiguity or demanding integration with
a written quality need. Use xhigh or max only for exceptional bounded problems with an explicit
expected benefit and budget justification. Use only levels supported by the selected model and
actual harness; verify effective settings and record `uncontrolled` when they cannot be verified.

A read-only assignment is not automatically a Terra assignment: first determine whether an
explicit evidence checklist makes Luna sufficient. Every brief records the selected model/reasoning,
rationale, authority, output, native budget, cutoff, and report reserve; verify the launched settings.
Higher-tier evidence collection needs a concrete synthesis, ambiguity, or risk justification.
Repair inadequate briefs, missing evidence, and sandbox or permission failures as workflow problems,
not automatic model or reasoning upgrades. Escalate only a specific unresolved quality or capability
gap, rather than using higher effort to compensate for missing authority or inputs.
Escalation checkpoints the specific unresolved question or failed acceptance, preserves useful
evidence, and starts a fresh bounded assignment; never silently switches an active worker or repeats
the whole scan. Budget ceilings are limits, not targets, and higher capability never broadens
authority; preserve one-writer ownership, visible panes, gates, and safety controls.

For genuinely independent bounded work, use visible worker panes freely when the maintainer asks
for supervised coordination. After accepting a worker report, immediately reassign that pane to a
ready independent task or close it; retain it only through report capture and acknowledgement.
Show and audit the pane and cross-harness rosters after launch, accepted report, reassignment, and
closure. The steady state is the supervisor plus active workers, never accumulated idle panes. The
supervisor workflow preserves the interactive-session and tmux details.

Every supervised implementation or review assignment is one token-bounded checkpoint. Declare a
limit from 100,000 through 200,000 tokens before the checkpoint starts; use 150,000 by default. Use
the harness's native goal budget when it has one. Otherwise, when worker-scoped usage is observable,
record the meter source and starting value and stop early enough to preserve the limit. Reserve at
least 15% for the final report plus headroom for delayed usage updates and in-flight work. Polling a
counter is not proof of a hard cap. If enforcement or worker attribution cannot be established,
permit read-only planning, research, or review only;
do not begin or resume edits. Follow-up scope and another review pass require a fresh checkpoint and
budget rather than an increase to the active limit.

Before editing handoff, verify the exact worker session's effective permissions, model, reasoning,
native goal identity, budget, status, and usage. A queued instruction does not change a sandbox or
prove acknowledgement. Keep initialization and authorization waits out of active implementation
loops. Verify cessation and the final meter before accepting a report; preserve incomplete work
without falsely marking the goal complete. Follow the supervisor workflow's launch and stop checks.

Apply the task-based model and reasoning policy above. Hitting a budget stops the checkpoint; it never authorizes weaker
gates, reduced grounding, narrower acceptance, or skipped safety checks. Preserve the worktree and
claims and report usage, remaining work, the stop reason, frozen tree identity, and gates run or not
run as defined in [the supervisor workflow](.agents/workflows/supervise.md).

Use claims for scarce outputs whose conflicts are expensive:

- `openapi-client` — `api/openapi.yaml` and the generated orval client
- `visual-baselines` and `e2e-baselines`
- `tokens`
- `migrations`
- `agent-contract` and `dev-runtime`

Add a domain-specific claim when two changes would edit the same interface or DTO. A worktree isolates
files; the claim identifies the real seam where concurrent work would collide.

During implementation, run the smallest focused test that covers the edit. Before pushing, run
`make verify BASE=<base>`; the classifier selects affected local evidence through the same fail-closed
impact policy as CI, including lint and tests over the affected Go package closure. The PR fast lane
and merge queue provide the protected final evidence. Reserve
`make verify SCOPE=all` and `make agent-baseline` for an explicitly requested complete audit, changes to the
gate/classifier machinery, or diagnosis of a classifier boundary—not merely because a task started.
Renew a long-running claim with `make agent-renew`; clean abandoned expired entries with
`make agent-prune`. When finished, run `make agent-stop`; after merge, audit and retire completed
worktrees with `make agent-gc` and an explicitly reviewed `make agent-gc APPLY=1`.

## Delivery

Completed, validated implementation work is published as a pull request and set to auto-merge by
default. Leave it as a draft only while required gates or requested work remain. Do not publish or
enable auto-merge when the task explicitly asks for local-only changes, a review checkpoint, or a
different delivery path.

## Prime directives

1. Gates are hard. Never stub, skip, delete, or weaken a test to make a gate green.
2. Never weaken grounding, the approval gate, authorization, or forward-only migrations, including in
   tests and seed data.
3. New dependencies require a design §14 amendment with a one-line rationale in the same PR.
4. Application code is Go except for the frontend build, the vendored `yt-dlp` executable, and the
   required `loomarr-image` Rust worker documented in design §14 and §22. Do not introduce another
   application runtime.
5. Unit tests never touch the network. Extend `internal/testkit`; do not create private service mocks.
6. Store conformance remains one suite over SQLite and Postgres.
7. Never run `make smoke*` from an agent session. It drives the maintainer's live stack.
8. Do not preserve legacy formats, schemas, adapters, or code paths speculatively. Keep compatibility
   only for verified live data, active users, or an external contract that cannot be migrated in the
   same change; otherwise migrate or remove the obsolete path.
9. Pin every installed third-party dependency to one exact reviewed version. Installed-dependency
   manifest sections must not use semver ranges or floating tags; container images and remote CI
   actions must use immutable digests or commit SHAs. Peer ranges remain compatibility contracts,
   not selected installations. Workspace links and operator-supplied Loomarr release variables are
   the other exceptions. Compatibility authorities such as Expo may require an older exact version;
   record the hold beside the pin instead of widening its range.

## Commands

`make verify` is the single local verification interface. Its default is affected evidence;
`SCOPE=all` is the explicit complete-repository audit. Use the comprehensive scope only when the task
requests it, changes the gate machinery/classifier, or needs to diagnose a boundary. One focused test:

```sh
go test -race -run TestName ./internal/<pkg>/
```

The complete, generated target reference is `docs/dev/commands.md`. Fix a Makefile `##` description
and run `make dev-docs`; never copy target lists into prose.

Useful local interfaces:

```sh
make doctor                 # toolchain, worktrees, ports, caches, artifacts
make bootstrap              # pnpm install + codegen + local directories
make verify BASE=<base>     # affected local evidence before push
make agent-env              # this worktree's runtime addresses
make dev-be                 # isolated backend with Air
make dev-fe                 # isolated Vite frontend pointed at that backend
```

Go 1.27+, the Rust toolchain pinned by `rust-toolchain.toml`, Node 22.x (22.5 minimum), pnpm 11.13.1,
GNU Make 4.x, and Docker are required. ffmpeg/ffprobe are required for playout tests. Lint tools and
Air run at pinned versions from the harness.

## Generated files

- `api/openapi.yaml` → `make openapi`; verify with `make openapi-verify`
- `docs/configuration.md` → `make config-docs`
- `docs/dev/commands.md` → `make dev-docs`
- `docs/design.md` §2 map → `make arch-docs`
- `web/packages/tokens/generated/` → `make fe-tokens`
- `web/packages/api/generated/` and `web/apps/web/src/routeTree.gen.ts` → `make fe-codegen`; both are
  gitignored and absent from a fresh worktree
- applied migrations in `internal/store/migrations/` are immutable; add the next migration

## Repository map

- `cmd/loomarr` is the entrypoint; `internal/app` is the composition root; `internal/api` owns Huma
  routes.
- Domain packages under `internal/` map to the ports documented in design §2.
- `internal/fillerstructure` is the provider-neutral complete-timeline reducer shared by filler
  certification and production; challenge truth and provider clients do not belong there.
- `internal/testkit` is the shared mock and pinned-fixture module.
- `web/` is a pnpm workspace: `apps/web` plus `packages/{api,core,tokens,fixtures}`. Frontend request
  types come from orval and are never handwritten.

## Local runtime and worktrees

Create a sibling worktree through the harness:

```sh
make agent-worktree TOPIC=<branch>
```

It branches off a freshly fetched `origin/main` — not whatever branch the primary worktree is parked
on — so a new worktree always starts from current main. To stack deliberately on the current branch (or
any other base), pass `BASE=HEAD` (or `BASE=<ref>`). It installs frontend dependencies and runs codegen. Credentials are not copied by default; use
`COPY_ENV=1` only when the task genuinely needs the maintainer's configured integrations. Secondary
worktrees receive deterministic, distinct backend/frontend/Storybook/Tunarr ports, a Compose project,
an SQLite database, a prepared-publication library, a filler drop folder, and
`.artifacts/<instance>/`.

Do not park a secondary worktree on `main`. Never remove a worktree containing uncommitted or untracked
work. `make agent-gc` is the canonical cross-worktree audit; its explicit `APPLY=1` mode may remove
only worktrees whose exact head belongs to a merged PR on current `origin/main`. Active, dependent,
dirty, credential-bearing, divergent, open, and ambiguous worktrees fail closed.
For retained worktrees, record an owner, reason, and next review trigger on the tracking issue.
Preservation is not a retirement plan; closing a pane or completing a goal does not clean a worktree.

Develop against the URL printed by `make dev-fe`; the backend URL serves the last embedded SPA build and
can look stale by design. A bare `go run ./cmd/loomarr` can orphan a stale child; use `make dev-be`.

## Hard rules

- Auth changes include the design §19 negatives: member 403s and sessions dying on disable.
- Retiring a capability adds its identifier to `scripts/check-retired.sh` in the same PR.
- Adding a setting changes design §15 first.
- Adding a build tag changes the guarded `TAGS` list in the Makefile.
- Adding a CI build input changes the per-job filter in `.github/workflows/ci.yml`; never add a
  workflow-level `paths:` filter.
- Pull requests own fast affected feedback; the merge queue owns complete affected integration
  evidence. Normal queue-produced pushes to `main` publish only and must not restore a third product
  validation run. Keep `CI` required and strict for both PR and `merge_group` commits.
- Frontend work uses the Vite server, not the stale SPA embedded on the backend port.

## Stop points

Stop and ask the maintainer for a Phase-0 contract deviation, an authorization/safety change, a gate
that appears to require weakening, or a new authority beyond the requested task. Preserve unrelated
dirty files and worktrees.

## Agent adapters and skills

Durable workflows live in `.agents/workflows/`; installed skill bodies live in `.agents/skills/`.
Agent-specific directories such as `.claude/` contain adapters or symlinks only. A required workflow
must be usable without a proprietary slash command, home-directory plan, or product-specific worktree
feature.

One worktree owns implementation and delivery. Use subagents for independent research, competing
designs, and fresh-context review; they report to the owner unless a separate editing worktree has a
clear file seam, interface seam, claim set, and merge order. For dependent branches, record
`DEPENDS_ON` and stack with `BASE=<dependency-branch>`. See `docs/dev/agents.md` and the curated
catalog in `docs/dev/skills.md`. When asked to supervise or coordinate multiple agents, follow
`.agents/workflows/supervise.md`.
