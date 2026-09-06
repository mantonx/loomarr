# Agent development

Loomarr's harness is agent-agnostic. Codex, Claude Code, terminal-driven agents, and humans use
the same Make targets and the same registry under Git's common directory.

## One owner, selective delegation

One task worktree owns each deliverable from first edit through merge. That owner may delegate
independent reading, competing designs, or a fresh-context review, but delegated agents return
findings to the owner by default. They do not open a second implementation branch for the same
deliverable.

Use another editing agent only when the work has a real merge seam:

| Situation | Use another agent? | Shape |
| --- | --- | --- |
| Search, research, or independent review | Yes | Read-only; report to the owning worktree |
| Two alternative interface designs | Yes | Independent proposals; the owner chooses and implements |
| Disjoint product slices | Yes | Separate worktrees, claims, tests, and PRs |
| Same DTO, generated output, migration number, or visual baseline | No | One owner; delegate read-only analysis |
| One change depends on another unmerged branch | Sequential or stacked | Record `DEPENDS_ON` and create from the dependency branch |
| One implementation split across several agents | Usually no | Coordination cost and partial ownership outweigh parallelism |

Claims prevent known collisions; they do not make overlapping implementations safe. Before
delegating edits, identify the file boundary, interface boundary, delivery owner, and merge order.
If any of those is unclear, keep one editing agent.

## Supervise a task

Use [the supervisor workflow](../../.agents/workflows/supervise.md) when one agent should coordinate
several bounded workers. It defines the task graph, worker brief, evidence report, steering loop, and
integration handoff. The delivery owner remains accountable for the combined diff, final gates, PR,
and cleanup; a worker reporting `complete` closes only its assigned outcome.

Issues track questions, bugs, and work; `PROGRESS.md` alone owns phase status and gate evidence;
claims and worktrees lock mutable seams. Every supervised assignment records its required tracking
issue and issue actions; a `PROGRESS.md` row is additional phase evidence, never a substitute or a
duplicate state. Search open and closed issues before creating one, and file only confirmed
current-`main` defects with a viewer-visible repro, evidence, and acceptance criteria. Link research
and out-of-scope confirmed defects instead of silently expanding the delivery.

Native subagents are the strongest arrangement because the parent can inspect, steer, wait for, and
collect its children directly. Independent agent sessions can still participate through the shared
registry and isolated worktrees, but their conversations are not visible across harnesses. Treat
their branches, diffs, commits, command results, and structured reports as evidence; do not imply the
supervisor can read or control an unrelated session.

### Roles, capability, and reasoning

Assign roles per task rather than making them permanent agent personas. Useful roles include a
bounded investigator, implementer, adversarial reviewer, or integrator, but each role ends with the
worker report. A worker waits for the supervisor to stop it or issue another brief; it does not grow
its own backlog. This keeps ownership with the delivery agent while still providing fresh context.

When the harness supports model and reasoning controls, select them by task shape and record the
choice in the worker brief. `AGENTS.md` owns the task-based model routing policy; the supervisor
workflow applies it at each assignment boundary. Record external sessions as `uncontrolled` when
their actual selection cannot be verified rather than assuming an equivalent model.

For straightforward evidence collection—exact SHA/status and PR/issue collection, bounded
inventories, artifact existence/hash/size checks, documented-command reproduction, and mechanical
comparison against explicit acceptance criteria—use Luna at Low reasoning by default. Use Terra at
Medium for ordinary implementation, multi-file behaviour analysis, or synthesis-heavy investigation.
Use Sol only for complex integration or high-risk or ambiguous contract reasoning with a written
need. A read-only assignment is not automatically Terra: check whether an explicit evidence checklist
makes Luna sufficient first. Record model, reasoning, rationale, authority, output, budget, cutoff,
and report reserve in every brief and verify actual settings. Escalate only after checkpointing the
specific unresolved question or failed acceptance, preserving useful evidence, and starting a fresh
bounded assignment. Budgets are limits, not targets; routing never changes authority, ownership,
gates, or safety controls.

| Task shape | Default execution |
| --- | --- |
| Small task, sequential reasoning, or shared mutable seam | One owning agent; no delegation |
| Straightforward evidence collection or mechanical comparison | Luna at Low reasoning by default |
| Ordinary implementation or synthesis-heavy analysis | Terra at Medium reasoning; justify higher-tier evidence collection |
| Complex integration or high-risk, ambiguous contract reasoning | Sol only with a written need; choose reasoning for the bounded task |
| External session without trustworthy controls | Record `uncontrolled`; verify through artifacts and evidence |

Use the task-specific defaults above. Record the selected model/capability, reasoning, and rationale;
do not switch model or reasoning during an active checkpoint. Change it only with the next bounded
assignment after the worker returns. Record worker-scoped usage as `source`, `start`, `end`, and
`delta` only when observable; use `unavailable` or `uncontrolled` otherwise, and never attribute an
aggregate goal/session total to one worker. Accepted checkpoint evidence, not token count alone,
measures progress. Repeated no-progress, duplicated work, or scope drift requires a rescope or
interrupt rather than a token-threshold reaction.

Never treat a stronger model as broader authority. Compare alternatives only when they pass the same
acceptance gate. During crash recovery, preserve the original model and thread when possible so the
checkpoint remains coherent.

### Bounded checkpoints

Every supervised implementation assignment and every review pass has a 100,000-to-200,000-token
checkpoint budget; 150,000 is the default. Record the meter source and starting value in the worker
brief. Prefer a native goal budget. If the harness exposes only worker-scoped usage, the supervisor
tracks the delta and stops early enough to preserve the limit. Reserve at least 15% for reporting
plus headroom for delayed meter updates and in-flight usage. Neither polling nor an unverified native
budget field proves a hard cap. If enforcement or worker attribution cannot be established,
that checkpoint is read-only: planning, research, diagnosis, and review are allowed, but
repository and external-state changes are not.

Follow the workflow's [launch preflight](../../.agents/workflows/supervise.md#verify-launch-before-handoff)
in the exact worker session: verify effective permissions and approved write roots, model/effort,
session id and separate native goal id, actual budget/status/usage, and the stop mechanism. A queued
instruction does not change a sandbox or acknowledge a handoff. Keep setup and authorization waits
out of active implementation loops. At stop, verify cessation and the final authoritative meter;
do not accept a cached usage count or mark unfinished work complete to silence continuations.

The limit cannot be raised or reset while a checkpoint is active. Continuing implementation,
changing scope, or running another review pass requires the worker to return a report and the
supervisor to approve a fresh checkpoint. Stop existing unbudgeted editing sessions and restart them
under a declared budget before their next mutation.

At the limit, preserve the registered worktree and claims. The worker report records the model and
reasoning, budget and actual usage, stop reason, remaining acceptance clauses, frozen HEAD and dirty
path inventory, and every required gate run or not run. A budget is a coordination boundary, never a
reason to weaken tests, grounding, authorization, migrations, safety checks, or acceptance criteria.
These fields are portable evidence: tmux panes and host-local token displays are not part of a
Linux-to-Mac handoff.

### tmux

`tmux` is a useful operator interface for independent sessions, not an orchestration protocol. Keep
the supervisor in one pane and give every editing worker its own registered worktree and pane. A pane
is not the worker's identity; its unique task name, branch, worktree, and claims are. Keep the panes
in the current supervisor window unless the maintainer explicitly asks for separate windows, and
enable mouse mode so the layout can be focused, resized, and scrolled directly.

Create worktrees through the harness before starting agent processes, then arrange panes with exact
paths and explicit targets. Verify the current supervisor's pane/window from its session context;
a default-target lookup may select an unrelated session. If the supervisor is outside tmux, ask for
the layout decision before creating a dedicated session and retain the current conversation as
delivery owner. For a supervisor already in tmux, this example adds one independent worker:

```sh
make agent-worktree TOPIC=worker-a CLAIMS=<owned-seam>
# Copy these exact ids from the verified supervisor session, not a default lookup.
supervisor_window='<session-id>:<window-id>'
tmux set-option -t "$supervisor_window" mouse on
tmux split-window -P -F '#{pane_id}' -t "$supervisor_window" -h -c /path/to/loomarr-worker-a
# Copy the returned id; start the interactive worker in that pane.
worker_pane='<returned-pane-id>'
tmux select-pane -t "$worker_pane" -T worker-a
tmux list-panes -t "$supervisor_window" -F '#{pane_id} #{pane_title} #{pane_current_command}'
```

Start the chosen agent interactively in each pane so its chat composer remains available, then
provide the workflow's worker brief. Do not use a one-shot batch command for visible supervision
unless the maintainer explicitly requests batch execution. Use
`make agent-status` plus the worker report for coordination. Do not treat `tmux capture-pane` output
as completion evidence or use blind `send-keys` automation as a substitute for an acknowledged
handoff; prompts, approval overlays, and terminal state make that brittle. Use native subagents when
live programmatic steering is required and that execution shape is authorized; do not substitute
hidden workers for a requested visible layout. Distinguish queued messages from current-turn
steering, require acknowledgement, and verify native state and outstanding tool processes after an
interrupt. When recovering a crashed session, launch or resume it from
the owning worktree and verify that both the worktree and its Git metadata are writable before it
continues an in-progress merge or edit. A linked worktree's metadata remains under the primary
checkout's `.git/worktrees/`; add that exact metadata root when a sandbox supports extra writable
directories instead of granting the recovered worker the primary checkout.

Use panes freely for genuinely independent bounded work, but not as a display of unused capacity.
After launch, show and audit both the tmux pane roster and `make agent-status`. A completed pane
remains only through capture and acknowledgement of its report. Once that report is accepted,
immediately reassign the pane to a ready independent task or close it, then show and audit both
rosters again. Repeat the audit after every accepted report, reassignment, and closure; the steady
state is the supervisor plus active workers.

## Linux-to-Mac handoff

Use the supervisor workflow's [cross-host handoff barrier](../../.agents/workflows/supervise.md#hand-off-between-linux-and-mac).
It is canonical for authorized transfer, safe topic/bundle handling, worker shutdown, fresh-Mac
registration, and issue handoff records. Keep all Linux writers stopped and the Linux branch/reports
until Mac acceptance completes; host-local registries do not provide cross-host exclusion.

## Start a task

Create, register, claim, and bootstrap a fresh sibling worktree in one command:

```sh
make agent-status
make agent-worktree TOPIC=filler-refresh CLAIMS=openapi-client
cd ../loomarr-filler-refresh
```

Registration happens before bootstrap, closing the old gap where a worktree existed and generated
files before it appeared in the roster. If the worktree already exists, register from inside it:

```sh
make agent-start TASK=filler-refresh CLAIMS=openapi-client
```

During implementation, run focused tests for the edited surface. Before publication,
`make verify BASE=origin/main` reports the changed-file scope and runs affected local evidence through
the fail-closed CI classifier. Its output distinguishes the complete CI impact, locally executed
gates, protected gates, and the local gates that actually completed; it never presents protected
Postgres, browser, native-client, or release-image evidence as a local success. The PR fast lane and
merge queue provide the protected final evidence.
Run `make verify SCOPE=all` or `make agent-baseline` only for a deliberately requested complete-repository audit,
changes to classifier/gate machinery, or boundary diagnosis—not merely because a worktree is new.

## Dependent work

Do not start two dependent branches independently from `main`; both agents will edit assumptions
the other does not contain. Stack the dependent work and make that edge visible:

```sh
make agent-worktree \
  TOPIC=channel-ui \
  BASE=channel-api \
  DEPENDS_ON=channel-api \
  CLAIMS=openapi-client
```

The harness rejects a dependency that is not active and a branch that is not based on the active
dependency branch. `make agent-status` shows the dependency and remaining lease so the merge order
is visible across harnesses.

Prefer waiting for the first PR to merge when the second task is small or rebase-sensitive. Use a
stack only when the saved wall time is worth carrying the dependency through review and rebase.

## Claims

A claim names a shared output or interface that cannot be merged safely after two agents edit it
independently.

| Claim | Covers |
| --- | --- |
| `openapi-client` | Huma definitions, `api/openapi.yaml`, orval output, shared DTOs |
| `visual-baselines` | Storybook snapshots |
| `e2e-baselines` | Full-page snapshots |
| `tokens` | Generated design tokens |
| `migrations` | The next forward-only migration number |
| `agent-contract` | `AGENTS.md`, adapters, agent workflows, and skills |
| `dev-runtime` | Make targets, local ports, Air, Compose, and the harness |

Add a domain-specific claim when two tasks would edit the same interface even if the files differ.
Keep claims narrow: claiming `*` or an entire broad domain makes safe work wait and hides the actual
seam. Duplicate active task names are rejected because they make ownership ambiguous.

Claims expire after four hours by default. Use `make agent-renew` for work that is still active and
`make agent-prune` for expired entries. A dead registry lock is reclaimed only after its owner is
gone and the lock is old enough that no live writer can be between lock creation and owner
publication.

## Worktree isolation

`make agent-worktree` branches from freshly fetched `origin/main` unless `BASE` is explicit. It runs
the pinned frontend install, code generation, Rust build, and isolated developer bootstrap.
Credentials are not copied unless `COPY_ENV=1` is explicitly supplied.

Every secondary worktree receives deterministic, distinct values for:

- backend, Vite, Storybook, and Tunarr ports;
- Compose project and volumes;
- SQLite database, filler drop, prepared-media, and artifact directories;
- the public URL used by internal Playout; and
- an isolated automatic developer login.

`make agent-env` prints those values. `make dev-be`, `make dev-fe`, `make storybook`, `make dev`, and
`make dev-gpu` consume them. Vite uses `strictPort`; a collision fails at the advertised address
instead of silently moving.

Air and its watchdog match processes by command and worktree directory. `DEV_BE_REPLACE=1` can
replace only this worktree's processes. A listener owned by another worktree is reported and left
alone.

## Baselines and gates

`agent-baseline` is an opt-in complete audit. It caches a successful `make verify SCOPE=all` by clean commit,
Go and Rust toolchains, operating system, and architecture. Worktrees at the same commit wait for one
proof and reuse it. Dirty trees
always run the gate and never populate the cache. The harness rechecks the commit and tracked-file
state after the gate and refuses to cache if implementation began while the baseline was running;
mixed-tree output is not evidence for either version.

Run small affected tests while editing, formatting and `git diff --check` before commit, then
`make verify BASE=<base>` once the diff is stable. CI owns expensive native and platform matrices.
Never run `make smoke*` from an agent session; those commands drive the maintainer's live stack.

## Finish and clean up

After the PR is merged and its required evidence is complete, release its claims and audit the
machine:

```sh
make agent-stop
make agent-gc
```

`make agent-gc` is read-only by default. It classifies every registered worktree and explains why
each one is protected or eligible. After reviewing that inventory, one explicit command retires
every eligible entry:

```sh
make agent-gc APPLY=1
```

Eligibility is deliberately strict. The worktree must be secondary, unregistered, unlocked, clean
including untracked files, free of a copied `.env`, and still at the exact head of a merged GitHub
PR whose merge commit is present on current `origin/main`. The collector matches PR head OIDs rather
than relying on `git branch --merged`, which cannot recognize squash-merged branch heads. Active,
dependent, running, dirty, credential-bearing, divergent, open, closed-unmerged, detached, and
ambiguous worktrees are reported and retained. Ignored bootstrap/runtime directories are removed
only after the worktree meets every eligibility rule and the maintainer supplies `APPLY=1`.

The audit requires an authenticated GitHub CLI because squash-merge evidence is not recoverable from
local branch ancestry. If GitHub or `origin/main` cannot be verified, the command exits without
removing anything.

Creating another worktree fails when the secondary-worktree count has reached 16. That is a backlog
tripwire, not a concurrency target: run the audit and resolve its findings first. A maintainer may
set `ALLOW_WORKTREE_BACKLOG=1` for an intentional exception.

Record a retention disposition on the owning issue: exact branch/HEAD, owner, why it remains, and
the next review trigger. Separate active delivery from superseded recovery copies; neither a closed
PR nor a stopped pane proves deletion is safe. Resolve credentials, dirty files, runtime processes,
and recovery evidence with the owner before cleanup. An exception to the limit must have its own
retirement trigger; do not turn it into a standing override. Report what was removed and which
committed work remains recoverable; do not imply discarded ignored build/runtime files were backed up.

`make doctor` reports toolchain drift, worktrees, addresses, caches, and misplaced artifacts. It
does not delete anything; `make agent-gc` owns worktree classification and retirement.

## Skills and durable workflows

The curated skill set and when to use it are documented in [Skills](skills.md). Durable audit and
review procedures live in `.agents/workflows/`; adapters may expose them as slash commands, but the
Markdown files remain the cross-harness authority.
