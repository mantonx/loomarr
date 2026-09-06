---
description: Coordinate bounded agents under one delivery owner and integrate their evidence
---

# Supervise agents

Coordinate independent agents without splitting ownership of one deliverable. The supervisor owns
the goal, acceptance criteria, integration, gates, and delivery. Workers own bounded investigations
or clearly separated implementation seams and return evidence to the supervisor.

**Input:** one goal with acceptance criteria. Optional: named workstreams or a maximum worker count.

Do not use this workflow merely because multiple agents are available. Use it when independent work
can reduce elapsed time, isolate noisy context, or provide a genuinely fresh review. Keep sequential
reasoning and edits to the same mutable seam with one agent.

## Establish the owner

One registered task worktree is the delivery owner. From that worktree:

1. Read `PROGRESS.md`'s **Active work** table and the governing design text.
2. Run `make agent-status`; reconcile active tasks, claims, dependencies, and worktrees.
3. Confirm that the owner holds every scarce-output claim needed for integration.
4. State the acceptance evidence and stop points before delegating.

A product's native agent panel shows only its own agent tree. `make agent-status` is the
cross-harness roster, but it does not expose another session's conversation or reasoning. Report
that visibility limit instead of implying direct control over an independent session.

## Track work in GitHub

GitHub issues track questions, bugs, and work; `PROGRESS.md` alone owns phase status and gate
evidence; claims and worktrees lock mutable seams. An issue is required for every supervised
research, work, implementation, or review assignment. A `PROGRESS.md` row may add a phase-evidence
pointer, but never substitutes for the issue or duplicates its phase state.

Every worker brief and report carries its required issue URL/number and issue actions, including an
open and closed-issue search, a linked issue, a created issue, a comment, or `none` with its reason.
Search open and closed issues before creating one.

File only a confirmed current-`main` bug: include its viewer-visible symptom, reproduction,
evidence, and acceptance criteria. Do not file speculation. Link research findings and conclusions to
the tracking issue and, when the result needs to outlive the report, a durable Markdown record. File
or link an out-of-scope confirmed defect instead of silently fixing it. Implementation, PR, and
cross-host handoff reports retain their tracking issue pointers.

## Choose the execution shape

Match concurrency to independent seams, not to the number of available sessions. Keep one agent for
a small task, a sequential reasoning chain, or edits to shared mutable state. Use a supervisor and
bounded workers when investigation, review, or implementation can proceed independently and the
owner can verify each returned result before integration.

Treat roles as temporary missions or review lenses, not permanent agent identities. A worker returns
control when its brief is complete and does not choose its own next task. The supervisor may then
stop it or issue a new brief with a different role. This rotation keeps specialized context bounded
without creating long-lived ownership silos.

At each assignment boundary, apply the canonical
[task-based model routing](../../AGENTS.md#task-based-model-routing) policy. Keep Luna-first mechanical
evidence collection; do not infer Astra from a supervisor role, review label, or available pane.
Select model and reasoning independently, record the expected benefit and required justification,
and verify support and effective settings in the actual harness. Repair brief, evidence, or
permission defects as workflow issues before considering a capability escalation.

Record model, reasoning, rationale, authority, output, native budget, cutoff, and report reserve in
the brief. Escalation checkpoints the specific unresolved question or failed acceptance, preserves
useful evidence, and starts a fresh bounded assignment; never silently upgrade an active worker or
repeat the whole scan. Compare speed or cost only among runs that satisfy the same acceptance
criteria. Budget ceilings are limits, not targets. Routing never changes ownership, safety controls,
or acceptance gates.

Record the selection and rationale in the brief. Model choice never changes scope, authority, claims,
tools, stop points, or acceptance. Do not switch a model or reasoning setting during an active
checkpoint; change it only with the next bounded assignment after the worker has returned control.
For an external session whose model cannot be controlled or verified, record `uncontrolled` instead
of guessing.

## Review a PR backlog concurrently

When the goal spans multiple PRs, default to parallel bounded work wherever the evidence or edits
are independent. Do not serialize the entire backlog behind one PR's review, repair, or hosted CI.
Before assigning workers, record each PR's fixed base/head, dependencies, required evidence,
mutable seams, and next action on the tracking issue.

- Review independent PRs concurrently. For a stack, review later PRs against their exact parent
  commits while earlier PRs are repaired or await CI; merge in dependency order.
- Keep each review frozen. A worker reads the recorded commits or saved diff, not another worker's
  changing checkout. After integration, review the delta against the accepted evidence and rerun
  affected gates; an early review does not certify a different final tree.
- Parallelize repairs only across disjoint files and claimed interfaces, with isolated worktrees
  and explicit write authority. Keep one writer for overlapping files or generated outputs and one
  delivery owner per PR. The supervisor coordinates integration and delivery.
- Use waiting time for another ready review, bounded evidence check, or independent repair. Do
  not launch duplicate reviews or invent work merely to fill panes. Close completed panes promptly.
- Report the active PRs, each worker's scope, dependencies that still serialize delivery, and the
  next integration decision. If useful parallel work is unavailable, state the concrete dependency
  or constraint instead of silently processing the whole backlog serially.

Visible panes, task-based model routing, token limits, report reserves, claims, independent review,
and final gates apply to every concurrent assignment. More PRs in flight never weakens acceptance
or authorizes closing a PR without evidence that its intended outcome is delivered or superseded.

## Bound every checkpoint

Treat one implementation assignment or one review pass as a checkpoint with a declared token limit.
Choose a limit from 100,000 through 200,000 tokens before the checkpoint starts; use 150,000 unless
the brief gives a concrete reason for another value in that range. The limit covers the worker's
complete checkpoint, including tool results and corrections as counted by the chosen meter. It does
not estimate product effort or redefine acceptance.

Use the strongest enforcement the harness actually provides:

1. When it has a native goal or task budget, create the checkpoint with that budget before any edit
   or review begins and record the native meter as the source.
2. Otherwise, when worker-scoped usage is visible, record the source and starting value. The
   supervisor owns the arithmetic and an early stop threshold, not just a prompt reminder.
3. When usage is unavailable, cannot be attributed to this worker, or cannot be bounded by the
   available controls, fail closed for mutation. The
   worker may perform read-only planning, research, diagnosis, or review, but it may not create,
   change, delete, commit, push, or publish repository or external state.

Do not infer a worker's usage from an aggregate parent, session, host, billing, or goal total. Do not
silently raise or reset a live checkpoint's limit. A changed scope, implementation continuation, or
second review pass is a new checkpoint: collect the current report, decide whether its evidence is
worth continuing, and issue a fresh brief and budget. Existing unbudgeted editing sessions must stop,
freeze, and restart under this rule before their next mutation.

Reserve at least 15% of the limit for reporting. Set the working cutoff lower still to cover meter
latency and in-flight usage; record the reserve, headroom, and rationale in the brief. For example,
a 150,000 limit with a 22,500 report reserve and 22,500 headroom has a 105,000 working cutoff.
Observed counter jumps are evidence for headroom, not a guaranteed upper bound. Verify when native
enforcement takes effect (within a turn or only between turns); a native budget field alone does
not prove a hard cap. Polling alone cannot guarantee one either. If the largest possible in-flight
increment cannot be bounded, keep the worker read-only until suitable enforcement is available.

Monitor through report completion and confirmed cessation. A missing/stale meter, changed worker or
goal identity, or failed monitor must stop editing. Re-read the final authoritative meter rather
than accepting the worker's cached count. Record any overshoot honestly; do not reset the goal,
subtract setup usage, or change attribution to make the checkpoint fit.

Reaching the limit is a mandatory return boundary, not failure and not permission to cut the work
down until it appears complete. The worker preserves its registered worktree and claims, freezes the
tree identity, reports accepted evidence and unfinished acceptance clauses, and returns control. The
supervisor chooses whether to accept the checkpoint, create a new bounded checkpoint, reassign the
remaining work, or stop the initiative. Never weaken a test, gate, grounding, authorization,
migration, safety check, or acceptance clause to fit a budget.

## Build the task graph

Split the goal only at real seams. For every worker, record:

- a unique task id and one-sentence outcome;
- read-only or editing mode;
- exact scope and explicit exclusions;
- inputs and governing acceptance clauses;
- required evidence and return format;
- dependencies, claims, file/interface seam, and merge order when it edits; and
- the condition for stopping and returning control.

Prefer read-only workers for codebase exploration, diagnosis, research, test analysis, competing
designs, and fresh-context review. They may share the owner's checkout because they do not mutate
it. An editing worker needs its own registered worktree only when its file seam, interface seam,
claims, and merge order are all explicit. If any are unclear, keep one editing owner.

Do not ask two workers to edit the same DTO, migration sequence, generated output, visual baseline,
or application interface. Claims reveal known collisions; they do not make overlapping edits safe.

## Brief each worker

Give each worker the smallest complete context, not the supervisor's accumulated reasoning:

```text
WORKER BRIEF
task: <unique id>
role: <temporary mission or review lens>
outcome: <one sentence>
mode: <read-only | editing>
execution: <model/capability and reasoning effort, inherited, or uncontrolled; rationale>
budget: <meter source; limit; start; enforcement: native | supervisor | read-only>
identity: <session/thread id; native goal/task id separately, or unavailable>
preflight: <effective permissions; permitted write roots; model/effort and budget verification>
cutoff: <working cutoff; report reserve >=15%; in-flight headroom; verified stop mechanism>
usage: <source; start; end; delta, or unavailable/uncontrolled>
tracking: <required issue URL/#>
phase-evidence: <PROGRESS.md row or none>
issue-actions: <searched open/closed; linked/created/commented, or none with reason>
owner: <task and worktree>
base: <commit or branch>
scope: <paths, subsystem, or question>
do-not-touch: <explicit exclusions>
acceptance: <verbatim clauses or exact commands>
return: <required evidence and format>
stop: <completion, budget limit, or escalation condition>
```

Record usage only when the harness exposes a worker-scoped measurement. `source` says where the
number came from; `start`, `end`, and `delta` are that source's values at the assignment boundaries.
Use `unavailable` when it cannot be observed and `uncontrolled` when an external session cannot be
attributed reliably; either value makes the checkpoint read-only. Never assign an aggregate goal or
session total to an individual worker.

Use the current harness's delegation facility. When it supports agent trees, spawn workers under the
supervisor so it can inspect, steer, wait, and collect their results. For an independent external
session, provide the brief through the available channel and treat its registry, branch, worktree,
commits, and reports as observable evidence rather than assuming live steering.

## Verify launch before handoff

1. Resolve the owning worktree, frozen HEAD/dirty inventory, claims, and exact worker session.
   Finish layout and permission decisions before activating an implementation goal. If initialization
   needs a goal, meter it as its own bounded assignment; do not leave an active implementation goal
   repeatedly continuing while it waits for authorization.
2. Launch with the least permissions required by the brief. In that worker's actual sandbox, verify
   the effective policy and approved worktree/artifact roots. For editing, use a disposable sentinel
   through the intended editing tool in an approved scratch path, then remove only that sentinel.
   A supervisor-side filesystem check does not test the worker sandbox. Read-only workers must not
   run a write probe. Before authorized Git mutations, resolve the linked Git directory and common
   directory and verify necessary metadata access without changing refs or the index as a probe.
   Do not grant the primary checkout merely because metadata lives beneath it.
3. Treat an instruction or queued command as a request, not a permission-policy change. If effective
   permissions are wrong, stop and relaunch/resume using the harness's supported configuration path;
   verify again in the resulting session. Do not retry product edits to diagnose a permission denial.
4. Create the bounded work goal before task edits or review begin. Independently verify session id,
   goal id, actual model/effort, budget, goal status, meter scope, and current usage. Session and goal
   ids are distinct. Count initialization within that goal; never assume its starting usage is zero.
5. Arm and verify the stop mechanism before handing over editing authority. Record the worker's
   acknowledgement of the exact brief. A queued message is not acknowledged until the worker reads
   and accepts it. If readiness cannot be established, stop the checkpoint instead of letting an
   automatic goal loop consume the budget waiting for the supervisor.

Pausing or awaiting authorization must not be reported as completion. Use the harness's pause/stop
control for automatic continuation; if none is available, collect evidence and end the session.
Keep the unfinished acceptance and claims intact. Do not mark an unfinished goal complete just to
silence it, or repeatedly resume an already blocked goal without a resolved dependency.

## Present interactive tmux supervision

When the maintainer asks for visible tmux supervision, use clearly titled worker panes freely for
genuinely independent bounded work, while keeping the supervisor and all workers in the current
tmux window unless the maintainer explicitly requests separate windows. Enable tmux mouse mode so
the maintainer can focus panes, resize them, and inspect scrollback. Launch each worker with an
interactive agent session so its chat composer remains available for follow-up messages; do not
substitute a one-shot batch command merely because its output is visible in a pane.

Resolve the supervisor's actual pane and window from its session context, then use explicit tmux
targets. A bare default-target lookup can select an unrelated session. If the supervisor is outside
tmux, ask for the layout decision before creating a dedicated session; keep the current conversation
as delivery owner and record the chosen session/window. Do not substitute hidden agents for a
requested visible layout.

After launch, show and audit the pane roster and `make agent-status` roster. A completed pane stays
only until its report has been captured and acknowledged. After accepting that report, immediately
reassign the pane to a ready, independent bounded task or close it; do not retain idle panes to fill
capacity. Show and audit both rosters again after every accepted report, reassignment, and closure.
The steady-state layout is the supervisor plus active workers.

## Supervision loop

Keep the main context on decisions and evidence. Do not copy raw exploration logs into it.

1. Inspect native agent state and `make agent-status` before assigning follow-up work. In visible
   tmux supervision, show the pane roster too.
2. At a meaningful evidence checkpoint, collect the worker report below. Interpret progress by
   accepted evidence for that checkpoint, not by tokens alone. The declared token limit is still a
   mandatory stop boundary even when progress is good.
3. Check cited files, diffs, commands, exit status, and artifacts. A worker's conclusion is a claim,
   not integration evidence.
4. When the report is accepted in visible tmux supervision, immediately reassign the pane only to a
   ready independent bounded task, or close it. Retain it only through capture and acknowledgement
   of that report; never leave it idle. Show and audit the pane roster and `make agent-status` after
   the accepted report and again after the reassignment or closure.
5. Send a bounded correction when evidence is missing or scope drifted. Below the working cutoff,
   repeated no-progress, duplicated work, or scope drift warrants interruption; the reserved reporting
   and in-flight allowance also requires an early stop. At the cutoff, end implementation and collect
   the report within the remaining budget. Reassign only the unfinished portion; do not
   restart accepted work.
6. Escalate a blocked dependency, contract deviation, authorization change, or overlapping claim.
7. Wait when no supervisor decision is needed; avoid polling agents merely to produce activity.

Use the verified harness steering/interrupt control. Distinguish queued follow-up from current-turn
steering, and require acknowledgement before treating a changed brief as active. Do not blindly send
keys into an unknown composer or approval overlay. After an interrupt, verify the native task is
stopped/paused and inventory any still-running tool process; an idle pane alone proves neither.
Preserve unrelated processes and ownership. Reconcile the report against the stopped tree and final
meter before accepting it; if reporting capacity is exhausted, the supervisor records missing fields
and unfinished acceptance rather than restarting the worker beyond its cap.

Workers return this schema:

```text
WORKER REPORT
task: <id>
role: <temporary mission or review lens>
state: <complete | blocked | needs-review>
execution: <actual model/capability and reasoning effort, inherited, or uncontrolled>
budget: <meter source; limit; enforcement: native | supervisor | read-only>
identity: <verified session/thread id; native goal/task id separately>
stop-verification: <actual goal status; cessation evidence; outstanding tool processes or none>
usage: <source; start; end; delta, or unavailable/uncontrolled>
tracking: <required issue URL/#>
phase-evidence: <PROGRESS.md row or none>
issue-actions: <searched open/closed; linked/created/commented, or none with reason>
branch/worktree: <branch> @ <absolute worktree, or read-only>
base/head: <commit> / <commit>
claims: <comma-separated or none>
changed: <paths or none>
freeze: <head OID; dirty-path inventory and digest, or clean>
gates: <commands run with pass/fail; required gates not run with reason>
evidence: <accepted artifacts and file:line citations>
findings: <distilled conclusions with file:line citations>
blockers: <specific dependency or none>
stop-reason: <brief complete | budget limit | escalation | blocked>
remaining: <unfinished acceptance clauses or none>
next: <recommended supervisor action>
retention: <worktree owner; retain/remove eligibility; reason; next review trigger>
```

`complete` means the assigned outcome and evidence are complete, not that the initiative is merged,
released, or accepted. Only the supervisor may make the initiative-level completion claim.

## Rehearse launch and stop failures

Before relying on a new harness or changed launch procedure, record these cases against disposable
fixtures, never the maintainer's live stack. These are operator acceptance checks, not claims that
documentation lint tests runtime enforcement.

| Case | Required result |
| --- | --- |
| Editing brief in a read-only worker | Preflight blocks handoff; no product edit is attempted |
| Requested policy differs from effective session policy | Stop and reconfigure; recheck in the actual worker |
| Thread id presented as goal id, wrong budget, or stale count | Reject the report/launch until independently reconciled |
| Usage jumps across the working cutoff | Stop work, account for in-flight usage, and preserve the report reserve; never claim polling guarantees the cap |
| Meter/monitor disappears or identity changes | Editing stops; no silent unmetered continuation |
| Worker awaits permission or receives queued steering | No idle implementation loop; changed authority requires acknowledgement |
| Interrupt with a tool still running | Record and resolve the owned process before declaring cessation |
| Supervisor is outside tmux | Obtain a layout decision; never target an unrelated default window |

## Hand off between Linux and Mac

A cross-host handoff is a stop-the-world barrier, not another orchestration channel. Linux and Mac
must never write the same deliverable simultaneously. Before Linux releases the work, stop or wait
for every worker, collect its final `WORKER REPORT`, reconcile `make agent-status`, and add the
handoff record to the tracking issue. Do not hand off merely because a pane is idle. Local registries
cannot enforce cross-host exclusion; the barrier and durable issue handoff record do.

The tracking issue contains this required record. Keep it current through the Linux stop and Mac
acceptance; `issue-actions` preserves searched, linked, created, and commented outcomes.

```text
HANDOFF RECORD
tracking: <required issue URL/#>
issue-actions: <searched open/closed; linked/created/commented, or none with reason>
topic: <exact branch name>
task: <exact task name>
claims: <exact comma-separated values, or empty explicitly>
linux-head: <full commit OID>
transfer: <authorized push/PR URL, or bundle filename + SHA-256 digest>
dirty-paths: <resolved/retained path-by-path disposition, or none>
stopped-linux-writers: <worker ids and owner after make agent-stop>
mac-verified-head: <full commit OID>
mac-owner: <task and worktree>
```

In every fresh Linux or Mac shell, copy the exact `topic`, `task`, and `claims` values from this
record; never assume inherited environment values. `claims` may be empty, but it must be set
explicitly. Before any ref, push, fetch, or worktree command, validate the required values:

```sh
topic='<exact topic from handoff record>'
task='<exact task from handoff record>'
claims='<exact claims from handoff record; empty is explicit>'
[ -n "$topic" ] && [ -n "$task" ] || exit 1
git check-ref-format --branch "$topic" >/dev/null || exit 1
```

On Linux, from the delivery worktree, verify the intended transfer and make every intended tracked
change a commit. Resolve or explicitly retain dirty and untracked paths; neither is silently handed
to Mac. Validate the topic before using it as a ref, and derive transfer filenames only from a safe
HEAD-derived handoff id. Use one authorized transfer route:

```sh
make agent-status || exit 1
git status --short || exit 1
git diff --check || exit 1
git log --oneline origin/main..HEAD || exit 1
git add <intended-paths> || exit 1
git commit -m "<handoff-ready change>" || exit 1
git check-ref-format --branch "$topic" >/dev/null || exit 1
test "$(git branch --show-current)" = "$topic" || exit 1
handoff_id=$(git rev-parse --verify --short=12 HEAD) || exit 1

# Only with authorization to publish this branch or PR:
git push origin "refs/heads/$topic:refs/heads/$topic" || exit 1

# Or, when a push/PR is not authorized, create a named bundle for secure copying:
bundle="loomarr-handoff-$handoff_id.bundle"
git bundle create "$bundle" "$topic" ^origin/main || exit 1
sha256sum "$bundle" > "$bundle.sha256" || exit 1
```

Transfer the bundle and its checksum through an authorized secure channel, then verify the checksum
on Mac before fetching it. Name and checksum build/test artifacts separately; transfer only the
artifacts explicitly needed for acceptance. Private evidence, `.env` files, and credentials are
never copied by default. tmux panes, local registries, claims, caches, ports, and ignored generated
outputs are host-local and must be recreated or re-established on Mac. Once the transfer is verified,
Mac tells Linux to stop. Every Linux writer, including the owner, then runs `make agent-stop` before
Mac edits begin.

On Mac, start from a fresh clone and re-establish the task before any edit. For a pushed branch:

```sh
git clone <authorized-repository-url> loomarr || exit 1
cd loomarr || exit 1
git check-ref-format --branch "$topic" >/dev/null || exit 1
git fetch origin "refs/heads/$topic:refs/remotes/origin/$topic" || exit 1
make doctor || exit 1
make agent-worktree TOPIC="$topic" BASE="origin/$topic" TASK="$task" CLAIMS="$claims" || exit 1
# cd to the worktree path printed by the harness; do not derive a path from $topic.
cd <harness-printed-worktree> || exit 1
make agent-status || exit 1
test "$(git rev-parse HEAD)" = "$(git rev-parse "origin/$topic")" || exit 1
git diff --exit-code "origin/$topic...HEAD" || exit 1
make agent-baseline || exit 1
```

For a securely copied bundle, verify and fetch it before the same `make doctor` sequence:

```sh
bundle=<securely-copied-bundle-path>
git check-ref-format --branch "$topic" >/dev/null || exit 1
expected=$(awk '{print $1}' "$bundle.sha256") || exit 1
actual=$(shasum -a 256 "$bundle" | awk '{print $1}') || exit 1
test "$actual" = "$expected" || exit 1
git bundle verify "$bundle" || exit 1
git fetch "$bundle" "refs/heads/$topic:refs/remotes/origin/$topic" || exit 1
```

`make agent-worktree` registers `TASK` and the same claims before bootstrap; do not add a second
`make agent-start`. Confirm the registration and claims with `make agent-status`, resolve any
conflict, and update the tracking issue with Mac's verified head before editing. Preserve the Linux
branch, bundle (if used), and all worker reports until Mac has completed acceptance. The Mac owner
alone may resume writing after this barrier.

## Integrate and deliver

1. Preserve read-only findings separately from the supervisor's judgment.
2. Review every editing worker's diff against its brief before integrating it in the recorded order.
3. Re-run affected checks after integration; worker-local green checks do not prove the combined tree.
4. Use `gate-review.md` for a fresh-context acceptance review when the change has a written gate.
5. Run the complete required gates for the touched areas, publish the owning PR, and follow its CI.
6. After merge, release claims with `make agent-stop`; audit retirement with `make agent-gc` before
   any explicit `APPLY=1` cleanup.

Record a cleanup disposition on the tracking issue even when removal is unsafe: exact branch/HEAD,
owner, reason retained, and a next review trigger (merge, recovery acceptance, handoff, or a date).
Separate active work from superseded recovery copies. Closing a PR without merging or accepting a
worker report is not proof that a local checkout is disposable. Preserve dirty files, credentials,
evidence, and unrelated processes; resolve their disposition with the owner instead of overriding
the collector. A worktree-limit exception needs a maintainer decision and a retirement trigger,
not a permanently raised limit. Do not create new work merely to justify retained panes/worktrees.

## Supervisor output

```text
SUPERVISION: <goal>
owner: <task>  branch: <branch>  worktree: <path>

WORKERS
<task>  <state>  <role>  <mode>  <execution>  <outcome>  <evidence or blocker>

INTEGRATION
accepted: <worker outputs incorporated>
pending: <remaining work or evidence>
conflicts: <claims, dependencies, or none>
next: <single next action>
```
