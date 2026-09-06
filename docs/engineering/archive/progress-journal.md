# PROGRESS journal — archived narrative (through 2026-08)

## 2026-07-13/14 — first live end-to-end smoke

The first run against the maintainer's Emby, Sonarr/Radarr/Seerr, Tunarr, and Ollama stack exposed
composition seams that package-level gates had missed. The fixes bound approved intents to channel
lineups, retained real durations, represented in-library and requested titles consistently, fed
terminal Provisioner events to the Scheduler and SSE bus, resolved media-server items to Tunarr
program identifiers, expanded series into episodes, and corrected container-volume ownership.

The lasting lesson was to test the Loomarr path end to end rather than prove a dependency with a
side script. Phase 12.5 in `PROGRESS.md` carries the shipped gate evidence; this journal keeps the
historical reason that integration phase exists.

> **ARCHIVED — a record, not an instruction.** This is the long-form build journal that used to
> sit at the top of `PROGRESS.md`, split out on 2026-08-10 because it had grown to roughly 3,500
> lines and buried the phase table underneath it.
>
> **`PROGRESS.md` remains the phase record** (CLAUDE.md prime directive #1) and still carries the
> table, the v2 rows, the environment notes and the project facts. This file carries only the
> narrative: what was found, what it cost, and why each decision went the way it did.
>
> Statements here were true when written and many are now stale by design — that is what a
> journal is. For current behaviour see [`docs/design.md`](../../design.md); for current phase
> status see [`PROGRESS.md`](../../../PROGRESS.md).

## V54 — filler refresh 2 (active at the time of the docs overhaul, 2026-08-10)

**A channel whose FIRST reconcile failed was stranded forever (2026-08-10, branch
`fix/stuck-building-channel`).** Not a phase — a live bug found by driving the app: an approved
channel sat at `building` with a fully-built 19-airing schedule that had never reached Tunarr.

⚠ **A three-part deadlock, and the middle part is the lesson.** `ClaimDueChannels` carried
`AND reconcile_deadline > 0`; the deadline's ONLY writer is the *last* step of a *successful*
reconcile (`reconcile.go:177`); the initial reconcile is best-effort. So a first reconcile that
failed left `deadline=0` → invisible to the sweep → never retried → never pushed. **The binder's
own comment said "best-effort, the sweep retries."** The sweep retried every channel except the
one case the comment invoked it for. Confirmed on the maintainer's install: `building`,
`tunarr_id=''`, `reconcile_deadline=0`, beside a healthy channel with both set.

Fix: **remove the guard** — `0` means DUE NOW, which also heals already-stranded rows with no
migration — and stamp new channels due-now in both creation paths (binder + `POST /v1/channels`).
The guard removal is the load-bearing half; the stamp is belt-and-braces so a future writer
re-adding `> 0` breaks one defence, not both.

⚠ **The `> 0` guard was never a contract.** Nothing tested a zero deadline: the conformance suite
covered past/future/detached and nothing else, and the guard lived in two hand-written dialect
statements no test compared. The new conformance case asserts a zero-deadline channel IS claimed —
sabotage-checked by re-adding the guard (red: "1 row, want ch-due AND ch-never-reconciled").

⚠ **Logging was the reason this took source-reading to diagnose.** The failure was a `log.Warn`
that had scrolled out of the terminal, so *why* the first reconcile failed is permanently
unknowable for this incident. Now ERROR, naming the consequence, plus a durable `activity` row
(the mechanism that already survives a restart and surfaces on the Dashboard) — via a nil-safe
`Binder.WithActivity`, matching the existing `rec`/`codec` idiom.

⚠ **Still open, filed separately:** nothing in the frontend calls `useReconcileChannel`, so
`POST /v1/channels/{id}/reconcile` has no UI caller — an operator has no manual way out of this
state. The actions menu offers only Watch / Edit / Pause / Delete. A surface-audit finding.


**A2/A3 — the decisions that did not stick, and a control that confirmed nothing (2026-08-10).**
Gate: `make check` exit 0 + `make fe` exit 0 + `make retired-verify` exit 0 (41 identifiers) +
`make openapi-verify` exit 0 after commit. Each run unpiped, exit code read directly.

⚠ **`review → terminal` had NO operator-side writer.** `filed`/`rejected` were only ever written
by `filler.Pipeline`, so every operator verb moved `clips` and left the pipeline row alone: file
cleared `held`, dismiss wrote `removed_at`, and "Looks right" PATCHed an era. The row kept saying
`review`, so `ConveyorOnly` kept returning it and `needsDecision` stayed true — **a filed clip came
back on the next refetch and `total` never reached zero.** Fixed with `settlePipeline`, guarded on
the row's current disposition (a `running` clip finishes its ladder; an operator verb never settles
it early) and best-effort like its sibling `clearPipelineRejects`.

**`dismissed` is a fourth disposition** (maintainer's call, via AskUserQuestion). A person saying no
and the quality gate refusing are different facts: `rejected` carries a stable reason code, measured
detail, and a per-reason `Soft()` undo rule, none of which an operator dismissal has. It is off the
conveyor and off the refusals list — that list is what Loomarr decided WITHOUT the operator.

⚠ **A defect the plan did not have, found by re-grepping rather than by looking for it:**
`asSuggested` looked each clip up with `GetClip(ctx, path)`, and `GetClip` is `WHERE hash = ?`
(V38c split identity from location and added `GetClipByPath` for exactly this). The miss hit a
`continue`, so **"File all as suggested" filed the clips and confirmed NOTHING** — a fifth control
in the #236/#240/#241 class. It was invisible because `putClip` defaults `Hash = Path`, so every
test in the package equates identity and location. The regression test gives the clip a
content-hash-shaped id that is not its path, and was RED on unmodified code before the fix.

⚠ **Every fix was sabotage-checked, and one round proved the check itself was vacuous.** Neutering
`settlePipeline` reddened three tests but left the running-guard test green — it asserts a row
STAYS `running`, which a no-op also satisfies — so the guard needed its own round (widen the
allowed origins → red). Restore-widening and both FE fixes reddened on their own rounds.

⚠ A2 was TWO independent causes for one silence: `ClipTagDialog` was mounted only under the
catalog branch, *and* the identifier handed up was the clip's path where the shell resolves by
hash. Either alone was sufficient. The tab's own test asserted `toHaveBeenCalledWith(ASK.path)` and
was green the whole time the button did nothing — only a route-level test that renders the page and
looks for the dialog can tell the difference, so that is what was added.

⚠ **Known gap, recorded rather than described as a feature:** the restore endpoint accepts a
`dismissed` row and returns it to `review`, but **no surface lists dismissed clips**, so that undo
is unreachable from the UI. §10 says so in those words.


**V54 — filler refresh 2: phase A ACTIVE (branch `v54-filler-refresh`).** Plan:
`~/.claude/plans/v54-filler-refresh-2.md` (phases A–H; A severity · B sources twirl-down ·
C split preview · D catalog artwork/hierarchy/pagination · E break length · F dHash · G a11y ·
H §10 docs). Origin: the maintainer's ten reported filler problems, 2026-08-10. A 12-area audit
(browser + source, each area re-verified by a second agent) confirmed all ten and found ~25
unreported defects, five of them the "control does less than it says" class of #236/#240/#241.

⚠ **The audit's error class, recorded because it recurred in 7 of 12 areas: a FALSE NEGATIVE
claiming something was not built when it was.** A pod duration budget, cross-catalog dHash dedup,
a frame-at-timestamp capability, and the missing-bytes operator warning were all reported absent
and all exist. Treat "X does not exist" in that plan as a claim to re-grep, never a fact.

**A1 — the open redirect, and a restart that read as a logout (2026-08-10).** Gate: `make fe`
exit 0 (**1348** app + 49 core + 19 api + 5 tokens, biome clean on 972 files) + `make check`
exit 0 + `make openapi-verify` exit 0 + `make retired-verify` exit 0. Each run without a pipe —
see the pipe-masking warning below, which this session re-triggered by piping `make check` into
`tail` and reading `tail`'s exit code.

`/login?redirect=https://evil.example` on an already-signed-in browser navigated **off-site**:
the guard's `throw redirect({ href })` is force-committed by the router with `replace: true`, so
it became a `window.location.replace()`, gated only by an http/https scheme check. `?redirect=`
was never validated — and no test covered the round trip at all (`reachability.test.tsx:328`
excludes `/login`; `wizard.spec.ts:115` asserts a bare `/\/login/` regex that does not constrain
the param). Fixed with `safeRedirectPath`, the frontend mirror of `safeReturnPath`
(`ssoroutes.go:296-309`), applied in `validateSearch` and again at both navigations.

⚠ **A second defect in the same guard: `_authed`'s bare `catch {}` treated ANY failure as a
logout.** With `meQueryOptions` setting `retry:false`, a 500, a proxy blip or the operator
restarting the server from the Dashboard — which this very layout mounts `RestartOverlay` for —
bounced a valid session to `/login`. Now only an `ApiError` with status 401 means signed out;
everything else is rethrown. Also fixed the back-button trap (`history.push` → `replace`).

⚠ **A router-level backslash test was WRITTEN AND DELETED because sabotage left it green** —
jsdom/memory-history does not commit that `href` the way a browser does, so it asserted nothing.
The rule is covered where it bites (`safe-redirect-path.test.ts`) and was verified in a real
browser instead: `?redirect=/\evil.example` → `/guide`. Every fix here was sabotage-checked; a
first-try pass on a security test is the case this file's own warnings exist for.

Live-verified in the browser, not only green: hostile → `/guide`, `/\`-hostile → `/guide`,
legitimate `?redirect=/filler/sources` → `/filler/sources`.



**V52 — the image service: phases 0–7 MERGED, phase 8 is PR #226.** `ca15ba1d` (#199, phases 0–4),
`309c5dfc` (#209, phase 5), `db241852` (#217, phase 6), `2db769d9` (#221, phase 7). A follow-up,
**PR #230**, closes the two items V52 itself left open and carries the migrator fix below.

⚠ **THE SQLite→POSTGRES MIGRATOR WAS BROKEN ON MAIN, AND SIX INTEGRATION TESTS HAD NEVER RUN.**
Found while adding coverage for the binary-coercion branch V52 phase 8 left unexercised — i.e. by
the act of justifying two lines of code rather than by looking for a bug.

`MigrateData` died partway through with `duplicate key value violates unique constraint
"filler_sources_pkey"`. Preflight refuses a populated target, so the destination *was* empty when
the operator chose it — but `Open(dsn, true)` then runs goose, and some migrations INSERT rows. The
source carries those same seeded rows, so a plain insert collides. Every operator who tried to move
an established SQLite install to Postgres hit it. Reproduced on unmodified `main` (`a40fa721`) on
tests nobody had touched, before any fix was written.

⚠ **The reason it was invisible is a THIRD variant of "green that proves nothing".** `make test-pg`
was `-run TestPostgresConformance`, so every other integration test in the package compiled and was
never selected — including `TestMigrateSQLiteToPostgres`, which its own file header calls "the V11
gate", plus `TestMigrateCoversEveryTable` and three `TestPreflight*`. **A filter is invisible in the
output**: the target printed an honest pass and said nothing about the six tests it skipped. The
earlier two variants were a pipe masking an exit code, and a missing `-tags=integration` printing
`ok … [no tests to run]`. The durable lesson: a test EXISTING, COMPILING and EXECUTING are three
separate facts, and CI output routinely evidences only the first two.

The fix clears the destination's seeded rows before copying, in REVERSE topological order — the
same graph the insert order uses, backwards, so a child's rows always go before its parent's.
`DELETE` rather than `ON CONFLICT DO NOTHING` because the SOURCE is authoritative: it holds the
operator's live data, which began from those same seeds and may since have been edited, and
DO NOTHING would keep the pristine seed and discard the edit while looking like success.

⚠ **§22's TMDB attribution is now COMPLETE** — the notice shipped in phase 7, and the mark
(`blue_short`, vendored from TMDB's attribution page, checked for `<script>`/`on*=`, `<title>` added
for the a11y lint and the artwork otherwise byte-identical) ships here. The SVG ban in §22 applies
to UPLOADS, whose serve route is public; a vendored asset served by Vite from `public/` is the same
footing as `favicon.svg`.

⚠ **BOTH of V52's open items are now CLOSED by #230, and this paragraph used to say otherwise.**
It read "two things V52 does NOT close, and both need the maintainer" — true when written, false
within the day, and left standing across two merges. That is the fourth time this header has
outlived its work, which is the failure the warning below already describes twice. (1) §22's TMDB
attribution is complete: the notice shipped in phase 7 and the LOGO ships in #230, vendored from
TMDB's attribution page. (2) `channel_icons.bytes` was the schema's ONLY BLOB/BYTEA column, so
dropping the table left `copyTable`'s binary-coercion branch reachable by nothing —
`TestBinaryColumnsSurviveMigration` now covers it against a probe table the TEST creates, so no
production column exists for a test's benefit and the branch is exercised rather than merely kept.

**Phase 8 — the retirements (2026-08-09, branch `v52-phase8-retirements`).** `/v1/filler/thumb` and
`/v1/filler/hover` (routes, handlers, `safeThumbPath`, three test files), `ClipDTO.thumbnail`/
`.preview` and `IncomingAskDTO.thumbnail` on the wire, `clipThumbURL`/`clipHoverURL` on the
frontend, and `channel_icons` entire — route, handler, both store methods, the interface entries
and the table (`00049`, both dialects). Nine identifiers added to `scripts/check-retired.sh`.
Gate: `make check` exit 0 + `make fe` exit 0 + `make retired-verify` clean (34 identifiers) +
`make openapi` regenerated (**116 lines removed**).

⚠ **No backfill, and that is a decision about THIS project rather than about migrations.** Loomarr
has no production installs, so nothing has accumulated icons predating phase 5. A backfill job
would have been code written to migrate data that does not exist — needing its own gate, its own
tests, and a reader in five years working out whether it still mattered. The maintainer's standing
direction is that leaving debt behind is the worse outcome; if that changes before release, the
honest fix is to restore the window, not to keep a dead job.

⚠ **The clip artwork COLUMNS survive while the routes and DTO fields go, and phase 6's plan was
wrong about this.** Its migration says `thumbnail`/`preview` "retire in phase 8, once nothing reads
them" — but the adoption job reads them as its permanent work list: the render pipeline writes
files under `FILLER_DIR` and the job converts them, because `internal/filler` importing
`internal/images` breaks the layering. A rolling background job is never "done" from a migration's
point of view, so those columns are the current render→adopt seam, not a migration window.

⚠ **A visible gap was ACCEPTED here, deliberately, and it is the maintainer's call rather than an
oversight.** With the legacy routes gone, a clip whose artwork the adoption job has not reached yet
renders no image until it does — hours on a large catalog, since the job runs every five minutes in
batches. It is self-healing and loses nothing, and it degrades into the card's designed *no-frame*
layout (the one that shipped before extracted frames existed) rather than into a broken image,
which is what makes it tolerable.

⚠ **This phase FOUND A SECOND LEAK, in phase 5's code, the same way phase 7 found phase 3b's.**
`DeleteChannel` still ran `DELETE FROM channel_icons` after phase 5 moved icon bytes to the image
service — so a deleted channel's `image_refs` row survived, and a ref is exactly what tells the GC
an image is still in use (§22). The icon was never orphaned and never collected: bytes on disk owned
by a channel that no longer exists, for the life of the install. `DeleteImageRefs` had **no caller
anywhere in the codebase**. The replacement is covered by a new `ChannelDeleteDropsImageRefs`
conformance subtest that also asserts the IMAGE ROW SURVIVES — two channels sharing one icon is
ordinary when identity is a content hash, so deleting refs must never delete bytes. Verified by
sabotage: removing the call makes it go red.

⚠ **`ALLOW_PATH` gained `internal/store/migrations/`, and that exemption is forced rather than
chosen.** A migration that CREATES a table names it, and §16 makes applied migrations immutable —
so `00012_channel_icons.sql` will say `channel_icons` for the life of the repository and cannot be
annotated out of the way. The migration that DROPS it must name it too. Neither is an instruction to
an operator, which is what the ban protects.

⚠ **`docs/help/` needed no sweep, which is worth recording as evidence rather than as luck.** The
retired identifiers appear nowhere in it, and neither does prose describing the old behaviour — so
the failure that motivated `check-retired.sh` (the deleted `/hooks/arr` webhook still documented as
a setup step) did not recur here. The grep is what proves that, not a reading.

⚠ **The header said "Phase 6 is PR #217" for the whole time that PR was merged**, which is the
third time this paragraph has outlived its work — the two warnings below already say so, and both
were written by sessions that then left a fresh stale pointer behind them. The durable reading is
that "the current phase is PR #N" is a form that CANNOT stay true: a PR is a state, not a record.
A merged phase is named by its SHA, and only the unmerged one may name a branch.

**Phase 7 — TMDB onto the service (2026-08-09, branch `v52-phase7-tmdb`).** The icon picker, the
Watch timeline and the guide loaded posters and stills straight from `image.tmdb.org` in the
operator's browser — the third of the three defects §22 exists to remove, and the one §10's clip
rule had already named ("a beacon that leaks who is browsing the catalog and when") without ever
being applied to TMDB. Both producers now adopt; `imageBase` moves `w500` → `original`, because
these URLs are no longer fetched by any client and §22 builds the ladder locally. Gate: `make
check` exit 0 (34 packages `ok`, zero `FAIL` / `no tests to run`) + `make openapi` regenerated +
`make fe` exit 0 (1249 app + 19 api; the four `TmdbAttribution` tests verified BY NAME under
`--reporter=verbose`, not by exit code).

⚠ **A suggestion or thumb is emitted only once its bytes exist, and this is a correctness rule.**
`Adopt` keys a pending row on a hash of the SOURCE URL and the fetch re-keys it onto the content
hash, deleting the placeholder. A URL built from a placeholder hash therefore resolves for under a
minute and 404s forever — and for the icon picker that URL becomes the channel's stored `logo`, so
it would rot permanently. A cold picker returning fewer than twelve posters is the strictly better
failure, and `images-fetch` fills it in within the minute.

⚠ **This phase FIXED A LATENT DEFECT IN PHASE 3b, found only because phase 7 was its first
caller.** The same re-key means `GetImage(hashOfURL(src))` misses forever afterwards, so a second
`Adopt` of one URL minted a fresh placeholder and the fetch job re-downloaded bytes already on
disk. Invisible while `Adopt` had no production caller; on an interactive surface the steady state
would have been twelve TMDB downloads per picker open, against an origin that caps a client at 20
simultaneous connections. Proved first (`origin hits = 2, want 1`), then fixed with migration
`00048` (index on `source_url`, both dialects) and a lookup asking only for FETCHED rows — two
rows share a source URL for the width of a re-key BY DESIGN, and the placeholder is precisely the
one a caller must not be handed. The new 15th `Images` conformance subtest was verified by
sabotage: dropping `origin_fetched_at > 0` makes it go red.

⚠ **`make fe-visual` baselines are NOT regenerated on this branch.** `Channels/ChannelIconField`
now renders its suggestion grid through `<Image>` rather than a plain `<img>`, which changes those
snapshots; the sanctioned path is `make fe-visual-update`, reviewed in the PR. It was not run here
because Playwright is not run locally in this project — CI owns that gate.

⚠ **The TMDB LOGO is still outstanding — the notice ships, the mark does not.** §22 requires both:
the notice verbatim (done, `TmdbAttribution` on Settings → Connections, asserted literally so a
copy-edit cannot silently break compliance) and TMDB's logo shown less prominently than Loomarr's
own branding. The logo is TMDB's trademark and this repository carries no brand assets; the
component takes it as a prop and renders the notice alone without one. **Supplying that asset is
the remaining half of the §22 attribution obligation** and is not something phase 8 retires.

**Phase 6 — clip artwork onto the service (2026-08-09, PR #217).** Stills and hover loops lived
only as files under `FILLER_DIR`; they now carry image-service identities, so they get srcset, a
modern format on the STILL (the only WebP in the product was the animated hover), content
addressing and honest caching. Gate: `make check` + `make openapi-verify` + `make fe` (1243 app +
19 api) + `make fe-visual` (782), all exit 0.

⚠ **Adoption is a JOB (`images-adopt-artwork`, every 5 min), and choosing that over an inline call
is what removes work from phase 8.** Its work list is "artwork on disk with no image identity", so
EXISTING and newly-rendered artwork adopt through one path — there is no separate clip backfill to
write, and therefore no second implementation to drift. Inline would also have coupled
`internal/filler` to `internal/images`, which the layering does not allow.

⚠ **`thumb_image_hash`/`hover_image_hash` are OMITTED from `UpsertClip`'s DO UPDATE.** The folder
scan calls the same upsert knowing nothing about image identities; including them would blank every
clip's artwork on re-sync. That block now documents SIX columns sharing this rule — V51d's
`created_at` joined it in the same merge, for the same class of reason.

⚠ **`<Image>` gained a THIRD fallback state, and a real caller forced it.** The clip card's hover
loop stacks ON its still and must render NOTHING on failure to reveal it; a colour block there is a
visible fault where the honest state is "no preview". `fallback ?? default` cannot express that —
it collapses "unspecified" and "explicitly nothing". Now `!== undefined`, so **`null` means
nothing**. ⚠ Biome rejecting the `<></>` workaround is what surfaced it: the lint was pointing at an
API gap, not a style nit.

⚠ **The SECOND migration collision of this arc.** V51d took `00046` while phase 6 was open; mine
renumbered to `00047`. Migration numbers are a hand-allocated global namespace with **no reservation
step**, and git merges two same-numbered files without complaint — different names, no textual
conflict. `ls internal/store/migrations/sqlite | tail` after EVERY merge, not only when writing one.
Both sides also added columns to one INSERT: verify column count against placeholder count
programmatically, because an arity mismatch is a runtime error no compiler catches.

⚠ **Main was ALREADY RED when phase 6 merged it in** (`6f7269aa`), and the mechanism is one this
file has recorded before under a different name. #214 added
`getListFillerMockHandler({ clips: [] })`; #203 made `total` REQUIRED on `ListFillerOutputBody`.
Each was green against the main it branched from; together they do not typecheck, and #203 merged
because its CI ran against a base without #214. **The generated client is the coupling, and neither
diff mentions the other's file** — exactly the `playoutApi` barrel story. Fixed in this PR (one
line), so merging phase 6 returns main to green.

Gate for what has landed: `make check` **exit 0, zero failures** (0 lint, `-race`) +
`make config-docs` + `make openapi` regenerated with no drift + `make test-pg` green with the
**14** `Images` conformance subtests **verified to actually execute under Postgres** +
`make fe` (**15** `Image` unit tests, verified by NAME under `--reporter=verbose`, not by exit
code) + `make fe-visual` (**780** passed, against a **freshly rebuilt** `storybook-static`) +
phase 5 **verified live in a browser** against an isolated store.

⚠ **This header named a branch that no longer exists and a phase count two behind, within an hour
of the merge that made both wrong.** It is the same failure this file already warns about for a
"Next up" that outlives its work — a stale pointer reads as current, and the session-start ritual
reads THIS line first. When a V52 phase merges, this paragraph is part of the phase, not paperwork
after it.

⚠ **One open issue blocks the NEXT phase's evidence, not its code: #210.** The `UI/Image` stories'
`srcset` uses base64 data URIs, which always contain a comma — `srcset`'s candidate separator — so
those images never load and the baselines captured a ThumbHash placeholder rather than an image.
Phase 6 puts `<Image>` into a clip GRID, which is precisely what those baselines would need to
prove. Fixing #210 first (a Storybook `staticDirs` asset, so candidates are same-origin and
comma-free) is the difference between phase 6 having a real visual gate and inheriting a blind one.

⚠ **"Exit 0" and "the assertions ran" are two different claims, and this branch has now been
bitten by both halves.** The pipe version is already recorded below (`make … | tail` reports the
pipe's status over a red suite). The second: `go test -run TestPostgresConformance ./internal/store/`
prints **`ok … [no tests to run]` and exits 0** without `-tags=integration`, because the file is
not compiled at all — so the filter matches nothing and the run proves nothing. The only sufficient
evidence is seeing the subtest NAMES in `-v` output. `make test-pg` passes the tag; a hand-rolled
`go test` does not.

Commits, rebased onto main `d7868a9`: `7f80749` (§22 doc), `fd18f29` (codec), `511cb97`
(service + store + migration), `1049395` (routes), `872872b` (renumber to 00045), `c68ec2d`
(the 10.4× test-harness fix), `c9a05d7` (phase 3a — wiring + `IMAGES_*` settings),
`05e1aee` (gofmt fix, see below), `cc754e6` (phase 3b — the four jobs).

⚠ **Phase 3a's recorded gate evidence was WRONG and this is the correction.** `c9a05d7` says
`make check` exit 0; it did not. Adding `Images ImageService` to `api.Server` landed inside an
aligned run of struct fields, so gofmt wanted to re-align its five neighbours and the tree failed
at the FIRST step of `make check` — `fmt`, before vet, lint or a single test. CI on PR #199 would
have been red the whole time. Fixed in `05e1aee`.

The mechanism is worth keeping because it is not a typo class: **gofmt aligns a whole run of
adjacent fields, so inserting one line re-writes lines you did not touch** — a hand-added field in
an aligned block is unformatted by default rather than by accident. And the reason it was reported
green is the same masking this file already warns about one paragraph up.

Loomarr shows images from four sources and handled each differently — icons as **database
blobs**, clip stills and hover loops on disk under `FILLER_DIR`, TMDB posters **hot-linked from
the operator's browser**. `internal/images` (§22) is one pipeline: sha256 content addressing,
disk storage sharded 2/2 under `images.dir`, AVIF+WebP+JPEG, ThumbHash placeholders, immutable
cache headers. **Nothing is wired yet** — phases 2–8 add the routes, jobs, FE primitive, and
migrate the four existing paths onto it.

⚠ **Three measurements contradicted the research the plan was built on, and the doc was corrected
rather than left to be discovered.** (1) `-tags nodynamic` costs **5.3×**, not ~3× — 12.5ms →
66.9ms per 500px WebP encode, and the fast path is fast *because* this box has a system libwebp
the library `dlopen`s, which is exactly the non-reproducibility the tag prevents. (2) **AVIF is
NOT an order of magnitude past WebP**: with `libaom -still-picture -cpu-used 6` it is **86ms vs
67ms**, about 1.3×. The scary 300–1200ms figures come from running a *video* encoder at video
defaults — asked for ONE frame, SVT-AV1 allocated **2.34 GB**, spawned **82 threads**, and
produced a file **78% larger** than libaom. The AVIF-is-a-job decision survives on a reason
measurement supports — **concurrency, not latency**: each encode forks a multithreaded process,
so a cold grid of 50 posters forks 50 at once. (3) A benchmark caught the plan's own rule being
broken in its own implementation (`Resize` per rung re-walks the halving chain: 231ms → 100ms).

⚠ **Two pre-existing defects found by adding one test group, both worth knowing:**

1. **`make test-pg | tail` reports exit 0 while `make` exited 1.** The pipe masks the status; the
   suite was RED and looked green. Capture the exit code directly, always.
2. **The Postgres conformance `TRUNCATE` list was a hand-written literal** covering ~8 of 20
   tables, under a comment asking the next person to keep it in step. Rows leaked between
   sub-tests, so an assertion over a GLOBAL query passed on SQLite (fresh file per sub-test) and
   failed only on Postgres. ⚠ **Completing the list is WRONG** — proven by two Filler tests going
   red: `filler_sources` and the taxonomy carry **migration-seeded** rows, and nothing in the
   literal distinguished "omitted by accident" from "omitted on purpose". Replaced with
   `DROP SCHEMA` + `CREATE SCHEMA` so `Open` re-runs every migration: seeded rows return by
   construction and new tables are covered the day they exist. Integration suite 9s → 21s.

**Phase 2 is also done** (`1049395`): three Huma operations — `rawOp` byte serve, typed record,
multipart upload — registered in both register lists, spec regenerated, `openapi-verify` green.

**MERGED as `ca15ba1d` (PR #199).** It was held at one hand-off because CI had not reported, and
merging past an unreported check is not a thing this project does — that judgement was right and is
kept here, but the branch is gone; do not go looking for `v52-image-service`.
⚠ A fresh worktree needs `npx pnpm@11.13.1 install --frozen-lockfile && npx pnpm@11.13.1 codegen`
before any FE work — `packages/api/generated/` is gitignored, so a skipped codegen typechecks red
*after* a successful install.

✅ **BLOCKER 1 — the migration collision, resolved.** This branch defined `00044_images.sql`;
**V51b merged `00044_filler_clip_pipeline.sql` to main** while it was open. Rebased onto main
(`d7868a9`) and renumbered both dialects to **`00045_images.sql`**; the postgres file's
"mirror of the sqlite 00044" cross-reference moved with it.

⚠ Keep the reasoning, because the next branch that sits open across a merge hits it again.
This was worse than a rename: goose records applied migrations **by version parsed from the
filename prefix**, so a database that already ran one `00044` **silently skips** the other — no
error, no failure, until something queries a table that was never created. Forward-only (§16)
decides which one moves: the **unmerged** migration renumbers, never the merged one. Check
`goose_db_version` on a dev DB for a stale `44`; if it is there, rebuild rather than hand-edit.
This is the concrete form of the conflict CLAUDE.md's worktree table warns about, and 00043
carries a comment about the same trap. The renumber was cheap only because
`internal/store/embed.go` globs `migrations/*/*.sql` — there is no hand-maintained list to
drift.

✅ **BLOCKER 2 — the CI timeout, resolved, and the diagnosis in this entry was WRONG.** It read
`panic: test timed out after 10m0s` → `FAIL internal/api 600.060s` as "the suite is big; raise
`-timeout` or split the package". Both readings were wrong, and measuring before changing anything
is what showed it: **462 top-level tests, 258.8s of a 259.7s package, slowest single test 5.3s,
median ~0.6s.** There is no slow test and nothing to split out.

Benchmarking the shared harness under `-race` found it — `store.Open` on a fresh file **503ms**
(45 goose migrations), `api.Router` **17ms**, and 462 tests, so **~232s of the 259s was the same
45 migrations re-run once per test.** Fixed by migrating ONCE per package into a template SQLite
file and copying it per test (**26ms**): measured **259.7s → 25.0s**, a **10.4×** drop, `make
check` exit 0 with zero failures (`c68ec2d`).

⚠ **Not a weakened gate** (prime directive 2). Every test still runs against a real, fully-migrated
database built by the real `store.Open`, on its own private copy — nothing shared, no assertion
changed. `autoMigrate` stays true on the per-test open, which is what makes both failure modes
safe: an empty or truncated copy is a version-0 database goose migrates the old way (slow, still
correct), and a corrupt one fails `store.Open` and calls `t.Fatal`. There is no path where a test
runs against a schema it did not ask for.

⚠ **The reason a split would have "worked" is worth keeping**: Go's `-timeout` is per test binary,
so N packages each get their own 10 minutes. The panic goes away while all 232s of wasted work
stays, and every test added later still pays 503ms. It buys headroom, not speed — and this branch
tipped the package over precisely by adding `00045`, one more migration on the 503ms path.

⚠ **`newServer` covered only 15 of the 462 tests** — converting it alone measured 247s, i.e. almost
nothing. The other 45 `store.Open` call sites are file-local helpers serving ~10 tests each, and
all of them moved to the shared `openTestStore`. **This is a per-package fix and 56 test files
across the repo open a store the same way**; `internal/store` (33s), `internal/integration` (25s)
and `internal/recurate` (15s) are the next candidates if the Go job ever needs more.

**Phase 3a done** (`c9a05d7`): the service is WIRED and proven end-to-end.

⚠ **`Server.images` was nil, so all three phase-2 routes 404'd in a running instance** — the
"a built component nobody imported" pattern this file already records against V1, V17a and V23.
No unit test can catch it, because the defect IS the absence of a caller, so the guard drives the
real composition root: `TestImageRoutesAreWired` goes through `BuildHandler`, uploads a real PNG,
reads the record and fetches a rendition over HTTP. **Sabotage-verified** — returning nil from
`imageService` makes it fail with the 501 its message names.

The adapter (`internal/app/imageadapter.go`) is the whole cost of "no domain package imports
internal/store": a type-for-type translation across a typing boundary. ⚠ Boxing goes through
`imageService()` because a nil `*images.Service` assigned straight into an interface field is a
**non-nil interface holding a nil pointer**, so `if s.images == nil` would silently stop working —
app.go already carries that warning for `*tmdb.Client`.

Settings: the seven `images.*` keys from §15 under a new `GroupImages`, plus the four job schedule
keys (an undeclared `ScheduleKey` **panics the settings service at startup**, so they land before
the jobs do). `Groups()` is consumed only by the docs generator, so a group with no Settings page
adds a docs section and nothing else — the keys are reachable via Settings → All until a later
phase gives them a form.

⚠ **Two defects in phase-1 code, both fixed in 3a:**

1. **`images.formats` was a DEAD KNOB.** `Config.Formats` existed, `New` defaulted it, and nothing
   read it — an operator dropping `avif` to save CPU or `jpeg` to save storage would have changed
   nothing while `docs/configuration.md` promised both. `Produces` is now its single reader, with a
   test that names the setting, because a setting with no reader cannot be caught by a test that
   does not name it.
2. **`Config` promised hot-apply and could not deliver it.** Its doc comment claimed the values
   were "resolved live from settings by the caller", but they were plain fields captured at
   construction. `MaxUploadBytes`/`PublicBaseURL`/`Formats` are funcs now; `Dir` stays a value
   because the blob store is built from it and re-pointing it at runtime would orphan every file
   already written. **The shape now encodes which knobs hot-apply.** `server.public_url` is the one
   that matters — Tunarr fetches stored icon URLs machine-to-machine, so setting it in the wizard
   must not need a restart.

**Phase 3b done** (`cc754e6`): the four jobs — `images-fetch`, `images-avif`, `images-rehydrate`,
`images-gc` — registered through `registerImageJobs` and pinned by `TestJobSet`, which went red the
moment they appeared and is the only test that can see a job constructed and never registered.

⚠ **The re-key is the part to understand before touching the fetcher.** `Adopt` keys a
not-yet-fetched row on `sha256("url:"+srcURL)` — a namespaced placeholder, because the content hash
cannot be known before the bytes arrive. When the bytes land, identity MUST become the real content
hash or `Cache-Control: immutable` stops being true: a URL would keep its name while its content
changed, which is the one thing content addressing exists to prevent. So `fetchOne` writes a new
row, moves the refs, and deletes the placeholder — **in that order**, because `image_refs` cascades
on delete and the obvious ordering drops the association silently. Sabotage-verified: swapping them
loses the ref and the test says so.

The same machinery covers a case that is not a placeholder at all — a TTL refresh or rehydrate of
artwork upstream has REPLACED. Hashes differ, the row re-keys, every derivative URL changes. That
is the honest outcome; the bytes really are different.

⚠ **The store needed FOUR new methods, not the two this file predicted.** The two known ones were
the GC's (`TotalImageDerivativeBytes`, `ListColdestDerivatives`). The two that were missed:
`RepointImageRefs` for the re-key above, and `ListImagesByOrigin` because rehydrate's work list —
"every remote row, whatever its fetch state" — is expressible by neither `ListImagesAwaitingFetch`
(scoped to the never-fetched sentinel) nor `ListImagesExpiredBefore` (scoped to a cutoff). A file
can go missing under a row in either state. The prediction "the other three jobs need no store
change" was an estimate, not a gate.

⚠ `RepointImageRefs` is **insert-then-delete, never `UPDATE image_refs SET image_hash`**. Two
distinct source URLs can hold identical bytes, so both placeholders re-key onto ONE content hash and
the second update violates the primary key. `DO NOTHING` makes the collision the no-op it should be.

**The TTL decision (maintainer's call): purge-then-requeue.** The GC deletes the original and every
derivative, clears `origin_fetched_at`, and `images-fetch` re-downloads within the minute. Rejected:
re-fetch in place and delete only on failure, which reads as strictly nicer and puts the compliance
question **inside an error branch** — TMDB unreachable for a day would silently keep serving expired
bytes, and the ceiling would be enforced by nothing. A ceiling that holds only while the network is
up is not a ceiling. Recorded doc-first in §22.

⚠ **The GC collects orphans BEFORE it expires, and a test found the interaction.** Both sweeps can
select the same row (past its TTL *and* unreferenced). Expiring first purges the bytes and queues a
fresh download moments before the orphan sweep deletes it — and if that delete fails, a download
instruction for an image no surface will ever show is what survives.

⚠ **Eviction is image-level LRU**, as decided: order by `images.last_used_at`, drop the coldest
derivatives, stop once under `images.cache_budget_mb`. Per-derivative LRU stays rejected —
`image_derivatives` has no `last_used_at` and adding one would make every image request a row WRITE
(a 50-poster grid = 50 writes per page load, on SQLite with `SetMaxOpenConns(1)`). The conformance
test pins the ordering by making `created_at` and `last_used_at` **disagree**: the coldest image's
derivative is the NEWEST file, so a `created_at` ordering returns exactly the wrong answer and still
looks plausible. Sabotage-verified.

**SSRF** (`images-fetch` is the second job to reach the internet unattended, after `filler-fetch`,
and the first driven by a URL in a row rather than a source an operator added): https only, host
allowlist, redirects capped at 3 and refused into private/loopback/link-local ranges, body capped on
the READ. ⚠ **The private-range rule deliberately does NOT apply to the first hop.** A self-hosted
media server is normally ON a private address — that is what self-hosted means — so a blanket ban
would refuse exactly the artwork the install owns. What makes hop one safe is that its host came
from the install's own configuration; hop two is the one an allowlisted host controls.

⚠ The allowlist is **derived** (`tmdb.org` + the `library.url` host), not a settings key. An
allowlist is only a control if something other than the attacker decides what is on it, and a knob
whose only correct values are these two is a third place to get it wrong. ⚠ Matching is exact-or-
dot-anchored-suffix, never a bare `HasSuffix` — sabotage-verified that the bare version accepts
`eviltmdb.org` against an allowlist containing `tmdb.org`.

⚠ **`Derivative.Path` was documented as "relative to the images dir" and never was.** Nothing read
the field before, so the lie was free; the GC hands it straight to a remove, where believing the
comment would have deleted nothing, reported success, and left the budget permanently over.

**The known Dockerfile gap is CLOSED** — `/data/images` is pre-created alongside `/data/filler`.
Phase 3b is what made it bite rather than merely untidy: `images-fetch` runs every minute, so on a
zero-env first run the first thing to touch that path is a background job failing into a root-owned
volume once a minute forever, with nothing connecting it to a directory nobody created. ⚠ This edits
`Dockerfile`, so the CI **Image job runs** (~30 min, both platforms under QEMU) — expected, not a
misfire.

**Phase 4 — the FE `<Image>` primitive (2026-08-09).** One Layer-1 component, seven Storybook
stories, 15 unit tests, 14 visual baselines, both hand-maintained barrels (`ui/index.ts` and
`packages/api/src/index.ts`'s `imagesApi`), and the `thumbhash` §14 row. §22's *Frontend contract*
was already written at phase 0 and the implementation matches it as specified — explicit
`width`/`height`, a `priority` mode flipping loading/fetchPriority/decoding **together**, a built-in
error fallback, and explicit `sizes` (never `sizes="auto"`; Safari supports it in no version).

⚠ **The failure flag was a bare `useState(false)`, and that is a bug a full green suite could not
see.** React reconciles by POSITION, so a grid that paginates, filters or sorts hands a *different*
`image` to the same instance — and the flag survived the swap, rendering a perfectly good image as a
colour block permanently. It would not have looked broken either: the block reads the NEW image's
`dominantHex`, so it reads as a deliberate empty state. Every existing test rendered one image and
never swapped it, which is exactly why nothing caught it; the probe that found it was a `rerender`.

Fixed by giving the failure an identity — `const failed = failedHash === image.hash` — which needs
no effect and no reset, because the comparison goes false in the same render the hash changes.
⚠ **Sticky per image, deliberately:** a `logo` is an operator-pasted arbitrary URL (§22), so a
failure is usually permanent and retrying known-bad bytes on every re-render is the worse default.

⚠ **Both halves are sabotage-verified, and the second one mattered.** The recovery test was proven
red before the fix. The stickiness test passed on the FIRST try, which this file already treats as
suspect — so the plausible-wrong implementation (reset-on-hash-change via a during-render
`setState`) was written and run: it passes recovery and **fails** stickiness. The two tests together
pin the semantics rather than merely the bug.

⚠ **A comment that contradicted its own code, corrected rather than left.** The placeholder memo
was documented as keyed on the hash while the dep array read `image.placeholder`. The code was
right and the comment was wrong, and the reason is specific to this service: phase 3b's fetch
**re-keys** a row from `url:`-hash to content-hash and back-fills `placeholder`, so a hash-keyed
memo is the classic stale-closure shape — it would hold the previous row's blur.

⚠ **Nothing in the app renders `<Image>` yet, so there is no browser verification to have.** The
Storybook gallery is the only real surface until phase 5 wires the first consumer; this is stated
rather than glossed, because "green tests" and "seen working" are different claims and only the
first one is available here.

⚠ **Merged `origin/main` in (V51c + V53a/b/d/e landed while this branch was open), and the merge
found a drift this file has a standing warning about.** `make check`, `openapi-verify` and
`retired-verify` stayed green; `make fe` went **red on two `barrel.test.ts` cases**. The cause is
the interesting part: when phase 4 was written there was **one** hand-maintained API barrel, and
V53d/e added **two more** (`src/zod/index.ts`, `src/msw/index.ts`) plus the guard test that catches
an unexported tag. So `images` was correctly exported from the barrel that existed and missing from
two that did not. **A hand-maintained list can drift because the list MULTIPLIED, not only because
someone forgot an entry** — and no amount of care on the branch could have anticipated it. The
guard main added is what turned a silent gap into a red gate; a fourth barrel would behave the same.

⚠ **`rebase` was the wrong tool and `merge` was the right one, for a reason worth reusing.** All 16
commits touch `PROGRESS.md`, so the rebase demanded the same resolution up to 16 times; one merge
resolved it once. That is only safe because this repo **squash-merges** — the branch's internal
shape is discarded at merge, so linear history buys nothing here. ⚠ `pnpm-lock.yaml` auto-merged
**textually**, which is precisely how a corrupt lockfile enters a tree; `pnpm install
--frozen-lockfile` is the cheap proof that the merged lockfile and merged `package.json` agree, and
`pnpm codegen` had to re-run because main changed `orval.config.ts` (it now emits `zod` and `msw`).

**Phase 5 — channel icons onto the service (2026-08-09, branch `v52-phase5-channel-icons`).**
The upload ingests through the image service (role=icon, visibility=public, origin=upload) with the
owner Ref that stops the GC collecting a live icon as an orphan, and the channel's logo becomes the
content-addressed URL. `ChannelDTO` gains `logoImage`, and the icon field's preview renders through
`<Image>`. Gate: `make check` + `make fe` (1239) + `make openapi-verify`, 9 new Go tests.

⚠ **The `?v=` cache-bust is DELETED rather than reimplemented, and that is the shape of the win.**
The old URL addressed a CHANNEL, so replacing an icon reused the URL and needed a query param to
defeat Tunarr's and Emby's caches. The new URL addresses the BYTES, so a different icon is
structurally a different URL. `TestUploadChannelIcon_URLFollowsTheBytesNotTheChannel` pins both
halves — same bytes ⇒ same URL, different bytes ⇒ different URL — because a regression to a
channel-addressed URL would serve a stale logo indefinitely and nothing else would notice.

⚠ **JPEG at w500, and this is the one place §22's compatibility floor earns its keep.** The floor
exists for old iOS and legacy Android WebViews; this URL is consumed by exactly that chain — Tunarr
hands it to Emby, which hands it to a television. WebP's ~97% support is a BROWSER number, and
browsers are not the population fetching this.

⚠ **`logoImage` ENRICHES `logo`, it does not replace it**, because an operator-pasted URL is a
supported way to set a channel icon rather than a legacy state. `imageHashFromLogo` therefore
VALIDATES (64 lowercase hex) rather than merely extracting: `PATCH /v1/channels/{id}` accepts any
logo string, so its output is attacker-influenced by construction and is handed straight to the
image store as a lookup key. A bare "take the segment after `/v1/images/`" would forward
`../../etc/passwd`. Pinned in `channellogo_internal_test.go`, traversal case included.

⚠ **The list handler pre-resolves BEFORE the loop, deduped by hash.** `channelToDTO` runs once per
channel, so a lookup inside it is an N+1 — the shape a profile here has already caught once, and the
reason `LineupEntryDTO.State` is documented as list-omitted. Twenty channels sharing an icon cost
one lookup. `imageToDTO` was extracted at the same time so there is ONE construction of that
projection; two hand-written copies is the drift class where the second one forgets `srcSetAvif`
when the AVIF job lands and quietly serves WebP forever on one surface.

⚠ **Two operation descriptions were corrected in the same pass** — the serve route still advertised
"the ?v= cache-bust changes on re-upload" and the upload still said "points the channel's logo at
the serve URL". Both became false the moment the handler changed. The serve route is now marked
LEGACY: it is the migration window for pre-V52 icons and retires with `channel_icons` in phase 8.

⚠ **A sabotage check nearly recorded a false verification, and the mechanism generalises.** The
first attempt to challenge the `logoImage` wiring test used a LINE-NUMBERED `sed` that silently
no-opped — earlier edits in the same file had shifted the target line by four — so it reported
success while changing nothing, and the test "passed" having never been challenged. Same shape as
`go test` printing `[no tests to run]` and exiting 0. **Make a sabotage PRINT what it changed before
running the test**; a verification you cannot see happen is not one. Redone correctly: red, then
green on restore.

⚠ **gofmt's aligned-block trap bit again, exactly as phase 3a recorded it.** Adding `LogoImage` to
`ChannelDTO` re-aligned nine neighbouring fields, and `make check` failed at `fmt` — its FIRST step,
before vet or a single test. This is now the second occurrence on this branch; the durable reading
is that a hand-added field in an aligned struct block is unformatted BY DEFAULT, so run `gofmt -l`
after any struct edit rather than waiting for the gate.

**Backfill deferred to phase 8, deliberately.** Existing `channel_icons` rows still serve through
the legacy route, so nothing breaks; a backfill job built now would sit idle through phases 6 and 7
and only matter the moment the table is dropped. It belongs next to the retirement it enables.

⚠ **One seam is open and named rather than hidden:** a logo that is an external URL gets no
`logoImage`, so the preview falls back to a plain `<img>`. That is correct today — the instance does
not own those bytes and knows no dimensions for them. **Phase 7 is where adopting remote logos would
close it**; if that is wanted, it is a decision to make BEFORE phase 7, not after.

**Next up: 6–7** clip artwork and TMDB onto the service; **8** retirements +
`scripts/check-retired.sh` + the `docs/help/` sweep. ⚠ Phases 5–7 each regenerate the orval client,
so per CLAUDE.md's worktree rule they are **not** parallelisable. ⚠ A fresh worktree needs
`npx pnpm@11.13.1 install --frozen-lockfile && npx pnpm@11.13.1 codegen` first —
`packages/api/generated/` is gitignored, so a skipped codegen typechecks red *after* a successful
install.

⚠ **One known gap remains**: the WebP `-tags nodynamic` gap, unchanged and still open.

**V53d — one mock layer instead of 31, and the first migrated file found two defects in the stub
it replaced (2026-08-09, branch `feat/msw-fixtures`).** Gate: `make fe` (**1222** app + **19** api +
51 core + 5 tokens, biome clean on 922 files) + `make retired-verify` (25). Zero Go files touched.

31 test files each hand-rolled a local `stubFetch`, so **31 places independently encoded what the
wire looks like** — the frontend doing exactly what the Go side bans (*"Phases do not invent private
mocks; extend the testkit"*). This is that shared layer: `msw` + orval-generated handlers behind
`@loomarr/api/msw`, with `src/test/msw/server.ts` owning the lifecycle.

⚠ **V53c was PLANNED AND DISSOLVED, on evidence.** It was to be runtime response validation through
the mutator. Orval cannot do it here: `runtimeValidation` does not exist in 7.21.0 (zero occurrences
in any `@orval/*` package), and in 8.24.0 the **only** `.parse()` injection is the Angular
`.pipe(map(...))` path — the fetch client's mutator branch builds its request function and returns
before reaching it. Loomarr's transport IS a custom mutator (CSRF, cookie auth, RFC 7807), and orval
PR #3226 — *"pass zod schema to custom fetch response implementation"* — is still open. **But the
value survived the design dying:** what it was actually for was catching FIXTURE drift, not backend
drift (`openapi-verify` already covers that), and a test knows which endpoint it stubs, so
`validated(schema, fixture)` needs no URL→schema map and no transport change.

**What is generated is the WIRING, not the data.** The URL, method and status come from the spec, so
a renamed route is fixed by a regenerate where a hand-written path silently stops matching and its
test keeps passing against nothing. ⚠ **The generated DATA is never trusted:** optional fields emit
as `arrayElement([value, undefined])`, so presence varies per CALL and nothing is seeded — flaky
rather than merely arbitrary. `useExamples` stays unset (it reads singular `example`; Huma emits 3.1
plural `examples:`).

⚠ **`onUnhandledRequest: "error"` is NOT used, because it does not fail a test.** MSW's docs define
it as *"print an error and halt request execution"*, and the maintainer confirms in mswjs/msw#946
that the interceptor handles the exception as the native class would, so *"from MSW's perspective no
exception has happened"* and the runner never sees it. The server records unhandled requests and
throws in `afterEach` instead. **Verified by direct probe** — a fetch to an unrouted path fails the
test by name — after a first sabotage attempt passed and looked like a broken guard. It was not: the
sabotage was invalid, because the component never made the request I removed the handler for.

⚠ **THE FIRST MIGRATED FILE FOUND TWO REAL DEFECTS IN THE STUB IT REPLACED, and this is the argument
for the sweep.** Neither was findable by reading:

- The old stub answered every non-PATCH call with `{ entries: [] }`, which read as *"the modal loads
  the settings list on mount."* It does not. **A catch-all stub structurally cannot distinguish
  "handled" from "never asked for"** — MSW can, because an unmatched request is now an error.
- It returned `{ results: {} }` where the wire says `results: SettingResult[]`. **The test asserted
  against a shape the API never produces**, and passed for as long as it existed, because a
  hand-rolled stub is untyped by construction. The generated handler is typed, so it is now a
  compile error.

Also: the old stub matched on `init?.method === "PATCH"` alone, so it would have accepted a PATCH to
ANY url — including one the component should never call.

**Migration is incremental by construction.** MSW installs globally with NO handlers, and an
unmigrated test's `vi.stubGlobal("fetch", …)` replaces global fetch outright and never reaches the
interceptor — verified: all 158 files still pass with the server installed. The two mechanisms
coexist until the last stub is gone.

⚠ **`retired-verify` caught this entry's own prose**: the §14 row and `server.ts` both cite
`/v1/suggestions` → `/v1/proposals` as the historical rename example, which is a banned identifier.
Both carry the explicit `retired-ok` opt-out rather than a reworded dodge — the guard is supposed to
fire on that string, and a mention that is deliberate should say so.

**V53e — the migration, in batches (COMPLETE, 2026-08-09). 31 of 31 migrated.** Gate: `make fe`
(**1243** app + 19 api + 51 core + 5 tokens, biome clean on 931 files) + `make retired-verify`
(26 identifiers). The old mechanism is now BANNED by `scripts/check-retired.sh`, sabotage-verified:
re-adding a `vi.stubGlobal("fetch"` line turns it red and reverting turns it clean.
Batch 1 (`#206`): `use-auth`, `users-step`, `first-channel-step`, `sources-tab`. Batch 2 (`#207`):
`wizard-ai-block`, `channel-row-menu`, `use-channel-refine`, plus the shared `channel()` fixture.
Batch 3 (`#213`): `incoming-tab`, `split-review-page`. Batch 4: `channel-watch`, `app-router`, plus
the shared `appHandlers()` baseline. **Batch 5: the whole ROUTE-LEVEL set** — `help`, `users`,
`guide-page`, `tasks-page`, `settings`, `wizard-router`, `filler`, `reachability` — plus the shared
`me()`/`user()`/`setting()` fixtures.

⚠ **`packages/api/src/mutator/mutator.test.ts` is a PERMANENT exception, not a to-do.** It tests
`customFetch` itself and asserts on `credentials: "include"` and the `X-Loomarr-Csrf` header —
neither of which an MSW resolver can observe, because MSW intercepts BELOW the layer under test.
Stubbing `fetch` is the correct tool for testing the fetch wrapper. **When
`vi.stubGlobal("fetch"` goes into `scripts/check-retired.sh` in the final batch, this file needs an
explicit carve-out** or the guard fails on the one file that is right.

⚠ **`appHandlers()` MUST BE SPREAD LAST**, and this is the batch that found out why. MSW resolves
the FIRST matching handler and `server.use()` PREPENDS, so "the most recent registration wins" is
true across `use()` CALLS and exactly backwards WITHIN one. `test/help` overrode `/v1/docs` with two
pages, got the baseline's `{ docs: [] }` instead, and failed as five 5-second TIMEOUTS with **no
unhandled-request error** — the guard cannot help, because the request WAS handled, just by the
wrong handler. Batch 4 never hit it only because everything `app-router` adds is absent from the
baseline. The rule is now in `handlers.ts`'s header.

⚠ **`main` WAS RED at `6f7269aa` and nobody knew.** `#214` added `handlers.ts` with
`getListFillerMockHandler({ clips: [] })`; `#203` made `total` required on `ListFillerOutputBody`.
Each was green against the main it branched from; together they did not typecheck, so main went red
on the second merge and stayed red until `#217` fixed it in passing. **The coupling is the
generated client, and neither diff mentions the other's file** — which is precisely the "two phases
touching the same generated output" hazard CLAUDE.md's worktree section describes, in its
harder-to-see form: not a merge conflict, but a clean merge that does not compile.

⚠ **The count is COUNTED, never tallied** — a running total in a commit message is exactly the sort
of number that drifts, and it drifted twice before this rule:

```
grep -rl 'const stubFetch\|vi.stubGlobal("fetch"' --include=*.test.tsx --include=*.test.ts .   # remaining
grep -rl '@/test/msw/server' --include=*.test.tsx --include=*.test.ts .                        # migrated
```

### How to migrate one (the playbook, so the next session does not re-derive it)

1. **Read the old stub for its catch-all.** Nearly every one ends in `return json({})` or similar.
   That branch is answering real requests — `channel-watch`'s hid THREE, `app-router`'s hid NINE.
2. **Use the generated handler** from `@loomarr/api/msw`; add `appHandlers()` first for anything
   that mounts the real route tree. Run the test and let the **unhandled-request guard enumerate**
   what the catch-all was covering — it reports the full list per test, by name.
3. **Expect the types to reject the old fixtures.** They are usually missing required fields;
   `MeBody.local` alone has been absent in FOUR files. Fix the fixture, never cast — `as ChannelDTO`
   in `channel-watch` was silencing eleven fields.
4. **Error cases stay hand-written** (`http.get(...)` + `HttpResponse.json(..., { status })`): the
   spec declares errors via `default:` (RFC 7807) on 132 of 134 operations, with ZERO explicit
   4xx/5xx, so orval has no status to generate from. Safe against renames anyway — a stale path
   stops matching and the real request goes unhandled, failing the test by name.
5. **Replace `mock.calls` assertions with resolver-recorded values.** A url-substring filter only
   proves the test's own spelling; reaching a route-bound resolver proves the route.

### What is left

**Route-level: DONE (batch 5).** The prediction that they would go faster than `app-router` held —
`appHandlers()` covered most of the surface — but the catch-alls were hiding far more than expected:
`test/reachability` alone had **13** (see below), against `app-router`'s nine.

**Component-level: DONE (batch 6).** `channel-filler`, `channel-lineup-editor`, `tune-panel`,
`use-channel-rules-draft`, `use-channel-filler-draft`, `refine-panel`, `filler-page`,
`channel-suggest-panel`, `sources-panel`, `pin-clip-dialog`, `use-channel-lineup`. None mounts the
route tree, so none needed `appHandlers()` — but the yield did not drop for being smaller files.

⚠ **`vi.stubGlobal("fetch"` is now IN `scripts/check-retired.sh`** (batch 6, the final one). The
carve-out for `mutator.test.ts` is the SEARCH PATH, not an allow-rule: the script searches
`web/apps/web/src` and not `web/packages`. **Anyone widening `SEARCH` to `web/` must add an explicit
exemption for that file in the same edit**, or the guard fails on the one file that is right.

### Batch 5's findings — the yield went UP, not down

**`test/reachability` was rendering 13 screens against `{}`.** It mounts every route in the
generated tree, so it touches more of the API than any other file, and its entire purpose is to
prove a screen shows REAL CONTENT. The catch-all was answering `/v1/playout/status`,
`/v1/system/services`, `/v1/system/database`, `/v1/system/backups`, `/v1/system/restart`,
`/v1/programming/vocabulary`, `/v1/library/collections`, `/v1/taxonomy`, `/v1/filler/pulls`,
`/v1/channels/:id/{cycle,upcoming,tracks}`, `/v1/channels/:id/filler/coverage` and
`POST …/pods/preview` — every one of which is a required array the page maps over. **A suite named
"reachability" was asserting reachability against empty objects.**

⚠ **Its own stub documented two of its failures in comments and they were never fixed** — both
begin "Before the /v1/filler catalog match", describing how the sources tab and the incoming queue
fell through to the CLIPS payload and rendered a wrong-shaped page that still passed. Its own words:
*"a stub that answers the wrong shape is indistinguishable from a working page until something
depends on the shape."* That is the argument for this whole migration, written by the thing it
indicts. It also registered `/v1/proposals` **twice**, the second branch unreachable.

**Eight more required fields no stub ever sent**, caught by `tsc` the moment the fixtures became
typed: `UserBody.effectiveQuota` + `.pendingAcquisitions` (the users list was built by SPREADING
the signed-in `MeBody`, a different type), `SettingEntry` × 4 (`advanced`/`doc`/`group`/`kind` —
tests wrote four of its eight), `ClipDTO.playCount` + `.playsCounted`, `IncomingAskDTO.hash` (the
clip's content identity, which every row action keys on), `SecretRevealOutputBody.displayable`,
`LLMModelView.tools`, `ListFillerOutputBody.total`. Plus one response shape the API cannot
produce: `TunarrConnectOutputBody` is `{ librariesEnabled, sourceId }`, and the test served
`{ ok: true }`.

**Three more substring collapses**, all in files that looked fine: `u.includes("/sessions")`
matched both `GET /v1/users/:id/sessions` and `DELETE /v1/sessions/:hash`; `u.endsWith("/split")`
was one character from `/v1/filler/splits/:id`, the read that immediately follows it; and
`u.includes("/v1/filler/") && method === "PATCH"` matched both the single-clip tag write and
`PATCH /v1/filler/sources/:id` — three assertions in `test/filler` then searched for "a PATCH, to
anything" and read its body.

### Batch 6's findings — smaller files, same rate

⚠ **`init?.method === "PATCH"` with NO url check at all, in FIVE files** (`use-channel-lineup`,
`channel-lineup-editor`, `use-channel-rules-draft`, `use-channel-filler-draft`, `pin-clip-dialog`,
`tune-panel`). Every one recorded "a PATCH happened" and then asserted on its body. In
`use-channel-rules-draft` that assertion carries the hook's CENTRAL claim — *editing previews and
does not save* — so the one property the file exists to prove was resting on a predicate that a
PATCH to any endpoint in the app would satisfy.

⚠ **The strongest form of the wrong-shape trap, in `sources-panel`:** its catch-all answered every
non-`me` request with `{ sources: [], total: 0, results: [] }` — a UNION of three endpoints' shapes,
merged so whichever one asked would find its field. A stub like that cannot fail; it is pre-satisfied
for every caller.

⚠ **A second duplicated `/v1/proposals` branch** (`channel-suggest-panel`:
`u.includes("/v1/proposals") || u.includes("/v1/proposals")`), matching the one in
`test/reachability`. Dead code in a stub produces no symptom at all, which is why both survived.

**Nine more required fields and two impossible shapes**, all caught by `tsc` the moment the fixtures
became typed: `ClipDTO.playCount`/`.playsCounted`, `Proposal.alternates`/`.scores`,
`ApproveOutputBody.status`, `SettingsListOutputBody.features`, plus `RemoteSourceDTO` (the add
returns `{ id, label, uri, enabled }`, not a bare id) and `SetFillerSourceEnabledOutputBody` (a body,
not 204). The two impossible ones: `filler-page` served `/v1/filler/pool` as
`{ total, untagged, channels }` — `total` is not a field of `FillerPoolOutputBody` at all, and
`clips`/`commercials`/`eligible` are all required — and `test/settings` served
`TunarrConnectOutputBody` as `{ ok: true }` when the wire says `{ librariesEnabled, sourceId }`.

⚠ **`vi.restoreAllMocks()` does not undo `vi.stubGlobal`.** Three files installed a mock
`EventSource` that way and cleaned up with `restoreAllMocks`, so the capture leaked into whatever
ran next. `unstubAllGlobals` is the matching call.

⚠ **Nine defects in eight files — the yield is not tapering, and every one is the same root cause
wearing a different face: a hand-rolled stub is UNTYPED and UNBOUND.**

- **Required fields no stub ever supplied.** `MeBody` requires `local`; `SystemLLMStatus` requires
  `local` AND `reachable`; `ChannelDTO` requires **eleven** fields where tests invented
  `{ id, name, status }`. A component reading `pendingCount` off one of those would see `undefined`
  where the server always sends a number.
- **A response shape the API never produces.** `{ results: {} }` where the wire says
  `results: SettingResult[]` — the test asserted against a fiction and passed for as long as it
  existed.
- **Catch-all branches answering requests that are never made** (three of them), and one hiding a
  request that IS made: `WizardAiBlock` calls `/v1/system/llm/discover`, which `json({})` answered
  silently, so that path ran against an empty object.
- **Assertions matching a url SUBSTRING the test wrote itself** — `calls.find(c => c.method ===
  "PATCH")` would match a PATCH to any endpoint at all. Reaching a route-bound resolver is the
  stronger claim.

⚠ **Error cases stay hand-written, and it is the SPEC's shape, not convenience:** this API declares
errors with OpenAPI `default:` (RFC 7807) on 132 of 134 operations — **zero** explicit 4xx/5xx codes
— so orval has no status to generate an error handler from. Verified safe against a rename: with the
path deliberately broken, the component's real request goes unhandled and the guard fails the test
BY NAME. The failure mode is "no handler" (loud), never "wrong data" (silent).

⚠ **`make fe` does NOT run `fe-visual`**, so a green local gate never covers the Playwright job.
Batch 1's first CI run went red on `mcr.microsoft.com` TLS handshake timeout — a Docker image pull,
`Error 125`, no browser ever started. **The tell was shard asymmetry**: 1/2 passed in 8m49s on the
identical commit while 2/2 died in 57s, and a real snapshot diff cannot be shard-asymmetric on the
same code. Re-run, not a code change.

**V53e is CLOSED.** 31 files migrated across six batches, ~60 defects, and the guard that stops a
thirty-second one. Nothing here is "next up".

⚠ **The number worth carrying forward is the RATE, not the total: it never tapered.** Batch 1
averaged a defect a file and so did batch 6, across files a third the size. That is the argument
against the intuition this migration kept inviting — "the remaining ones are smaller, they will be
clean". They were not, because size was never the variable. **A hand-rolled stub is UNTYPED and
UNBOUND, and both properties fail silently**: the type lets a fixture omit a required field forever,
and the substring lets an assertion match a request it was not about. A short file has fewer places
to hide one, not a lower chance per place.

⚠ **The two mechanisms that actually caught things are worth reusing anywhere mocks are involved.**
The unhandled-request guard turned "answered with `{}`" into a named failure and found 13 endpoints
in one file. The generated types turned "fixture omits a required field" into a compile error and
found nineteen. Neither is a test anyone wrote; both are a *shape* that makes the defect
unrepresentable. `appHandlers()` is the counterexample that proves it — a hand-maintained list,
guarded only by the first mechanism, which is exactly why its header says so.

**V50d(a) — the collapsed-body focus gap V50c left behind (2026-08-09).** Gate: `make fe`
(**1223** app + 19 api + 51 core + 5 tokens, biome clean on 923 files).

`connection-block` closed with `grid-template-rows: 0fr` + `overflow:hidden` — zero height but
**NOT `display:none`** — so its body stayed in the accessibility tree and stayed **focusable**. That
body holds a "Test connection" button and, when a check fails, a "Fix" link into the Help centre:
**a keyboard user tabbing the wizard reached both while the section was visibly shut.**

⚠ **V50c fixed exactly this in `CollapsibleSection` and left its lookalike behind**, so the two
disagreed for three phases. That is the cost of the duplication the component's own header comment
flags — it borrows `.reveal` from CollapsibleSection but is a separate implementation, and a fix to
one is not a fix to the other. `Collapsible.Panel hiddenUntilFound` now renders the closed body as
`content-visibility: hidden`: out of the a11y tree, still mounted (a half-filled connection form
keeps its state), still reachable by find-in-page.

⚠ **It has no story, so axe never sees this component** — which is why the regression is a UNIT test
asserting `hidden="until-found"` plus the absence of the inner control, not a visual one.
Sabotage-verified: swapping `hiddenUntilFound` for `keepMounted` (mounted but reachable — precisely
the old bug) fails it while the other nine pass.

**Also in this PR: `make fe-visual`/`e2e` now run Docker as the host user.** The Playwright image
runs as root, so everything it wrote into the bind mount was root-owned — and the symptom surfaced
far from the cause: `git worktree remove` half-failed on `test-results/`, git **deregistered the
worktree anyway**, and ~550MB per worktree was stranded with no git record it existed. Three
worktrees had accumulated **1.7GB** that way. `--user $(id -u):$(id -g)` fixes it; `-e HOME=/tmp`
rides along because a non-root uid's default `/root` is unwritable and fails in a way that reads
like a Playwright bug. ⚠ **Unverifiable locally by policy** (Playwright is not run on this machine)
— CI is the verifier, and a broken job shows up red on this PR rather than silently.

**Next up: the rest of V50d** — house-style conformance across `components/ui`, which is what the
phase was originally about; this PR only took its one accessibility defect.

**V53b — arrays are not nullable; `null` stops being a second empty list (2026-08-09, branch
`feat/non-nullable-arrays`).** Gate: `make check` (0 lint, `-race`) + `make openapi-verify` +
`make retired-verify` (25) + `make fe` (**1222** app + 18 api + 51 core + 5 tokens, biome clean on
919 files).

Every list field in this API was typed `T[] | null` — **109 nullable type-unions against 4 plain
arrays** — so every client handled two representations of "nothing", forever. The generated zod
carried `.nullish()` on every array and the FE coalesced `?? []` at each use.

The cause was `huma.DefaultArrayNullable`, which defaults to true *correctly*: a Go nil slice really
does marshal to `null`. It is now false, set in `humaConfig` — the single constructor behind both
the served API and the spec export, so runtime and document cannot disagree.

⚠ **The flag alone would have made the spec LIE**, and Huma says so in its own doc comment: *"any
`nil` slice will still encode as `null` in JSON."* Flipping it changes what the document CLAIMS
without changing what the wire SENDS. It ships with a guard or not at all.

**The guard.** `TestResponses_ContainNoJSONNull` drives every parameterless GET — **derived from the
exported spec**, not a hand-kept roster, because a new list endpoint nobody added to a list is
exactly the one that would regress — against an **EMPTY STORE**, and fails on any JSON null in a
success body. ⚠ The empty store is the point, not an accident: nil slices are what a repository
returns when it finds no rows, so a fresh database is precisely the state that produces them, and a
seeded fixture would hide the entire class by never taking the empty branch. Since the spec now
declares nothing nullable, *"no null anywhere in the body"* is the exact invariant.

**It found one leak in 46 paths, and it was the one that matters most.** `/v1/setup/status`
returned `checks: null`: `runConnectionChecks` used `var checks []SetupCheck` and deliberately
contributes no check for unwired services — so an **unconfigured install, the wizard's entire reason
to exist**, was the case that produced it. One leak in 46 is also the shape of the change: 51
`make([]T, 0)` guards already existed across the handlers, so this finishes an inconsistency rather
than inventing a convention.

**Frontend fallout, all benign and all worth naming:**

- The generated zod lost `.nullish()` **entirely** (count: 0) — the ambiguity is gone from the
  schemas, not merely handled at each call site.
- ⚠ `VocabularyWhen`/`VocabularyWhat`/`VocabularyHow` **stopped being generated, and nothing was
  renamed.** Orval only emitted those aliases because `X[] | null` needed a name; a non-nullable
  array inlines to `WhenVocab[]`, so they had no reason to exist. First read was "orval renamed
  them" — worth checking rather than assuming, because the fix differs.
- `presets.ts` carried a comment stating the served arrays *"are nullable on the wire"*. True when
  written, and about to become a lie. The coalescing it justified **stays**, for a different and
  still-real reason: the vocabulary is `undefined` until its query resolves. **Null is gone;
  unloaded is not.**
- Three fixtures passed `models: null`; the component already does `hp.models ?? []` and branches on
  `length === 0`, so `[]` is the identical branch rather than a changed one — checked before
  swapping, since a fixture edit that silently moves a branch is how a test stops testing.

⚠ **This also unblocks orval's MOCK generator, which V53a recorded as rejected** — it degraded
`type: ["array","null"]` to `arrayElement([[], null])` without descending into `items`. Re-measured
on the new spec: **137 never-populated list mocks → 0**, and **247** populated `Array.from(...)`
generators. That is a consequence, not the justification: `null` vs `[]` was an API defect on its
own terms, and if codegen had been the only argument the right answer would have been to leave it
alone.

**V51f shipped the phase this line used to point at** (see its own entry below). **The V51g loop it
also held open is CLOSED — and the answer was not the one it predicted** (fix 4 above): WAGA-5 was
not starved after #225, it had finished in **three seconds** and been waiting on a person ever
since. Nothing could show that, which is why "never been seen to finish" was the honest observation
and the wrong conclusion.

---

**V51f — honest filler controls: the era range becomes real, and four settings stop lying
(2026-08-09, branch `v51f-policy-fields`).** Gate: `make check` (0 lint, `-race`) +
`make openapi-verify` + `make retired-verify` (32) + `make fe` (**1320** app + 19 api + 51 core +
5 tokens, biome clean on 961 files). The last phase of the V51 plan.

⚠ **Every defect here had shipped code that READ convincingly, and three had green tests
asserting them.** That is the through-line of the phase, not a coincidence: each was a control an
operator could see and change, wired to something that ignored it.

1. **The era range was half a range.** `filler.Selection.Era` and `filler.Window.Era` were one
   `int`, so the "To year" the UI renders, types, canonicalises and *inverted-range-validates* was
   discarded by every consumer: **1990–1999 behaved identically to 1990–2035**. Both bounds are
   honoured now, via `filler.EraRange`.
2. **"Any era" was unreachable.** The scope default keyed off `Era == 0` — what a cleared field and
   a deliberate "any" both look like — so clearing re-inherited on the next derivation. Presence is
   the opt-in now: absent inherits, PRESENT is the operator's answer even when empty.
3. **Three `filler.Policy` fields were set in tests and nowhere else.** `EraStrict` is deleted (a
   narrow range gives strictness through a control that exists); `MinClipMs`/`MaxClipMs` get real
   settings. Until now `durationEligible` always returned true, so `PoolReport.Eligible` —
   headlined as *"the number that surprises operators"* — was arithmetically identical to
   `Commercials` **on every install ever run**.
4. **Picking an Audience emptied the whole ladder on an untagged catalog.** `filterAudience`
   admits `aud` or `general`; an unclassified clip is `""` and matched neither. Those clips now
   fill the BOTTOM rung only — never above a grounded match.
5. **A PATCH omitting `policy.filler` wiped the channel's pins, exclusions and criteria.** Same
   silent-loss shape `policy_merge.go` already records for `scope`, in the half `OperatorSet`
   cannot reach — and worse, because nothing re-proposes an operator's filler selection the way a
   refine re-suggests a scope.
6. **Unticking a pin BLOCKED the clip.** On a component whose own header ⚠ warns that collapsing
   the third state "silently blocks an operator's catalog".

⚠ **The safety asymmetry in (4) is tightened beyond the plan and is not negotiable.** The plan
said "not `kids`"; family channels are watched by children, so the rule is an **allowlist** —
`general` and `late_night` only. A denylist would hand every audience added later the permissive
default, which is the wrong direction for the one rule here that is about safety. A kids channel
with an untagged catalog correctly falls to its bumper card: a visible, fixable state rather than
unclassified adverts in front of children.

⚠ **Three defects were defended by green tests, and two of those tests said the same wrong thing.**
`TestFitFor_StrictEraSkipsTheWidenedRung` proved a branch no operator could reach.
`"unticking an overridden channel blocks it rather than clearing the override"` (the picker) and
`"writes an exclusion, not just a missing pin"` (the dialog) both pinned the untick bug as the
contract. **Stated twice, a trap reads as a decision** — anyone who noticed would assume they were
the one who was wrong. Every replacement asserts the property rather than the symptom.

⚠ **The per-criterion breakdown is NOT derived from `FitFor`, which the plan proposed.** `FitFor`
short-circuits on the first failing predicate, so counting by its reason attributes a clip failing
both category and audience to whichever check runs first — an answer that would change if someone
reordered that function, with nothing to catch it. Each criterion is counted independently, from
the predicate the ladder itself uses.

⚠ **One rule had THREE implementations that disagreed, and the disagreement was invisible.**
`SelectionForChannel` applied the scope era, `api.fillerSelectionToDomain` did not, and
`podPreviewAdapter.PreviewDraft` applied it again — so the API's omission was silently rescued one
layer down. Two copies cancelling out is the worst kind of agreement: nothing looks wrong, and it
stops working the instant a third state exists. `channels.SelectionFrom` is the single writer.

⚠ **Deviations from the plan, both deliberate.** The settings are `filler.min_clip_duration` /
`max_clip_duration` (`KindDuration`), not the plan's `*_seconds` — the neighbouring
`filler.min_duration` is a duration, and two unit conventions in one settings group is a trap of
its own; the old names are in `scripts/check-retired.sh`. And the plan's "delete the three
frontend workarounds" (H) did not apply: the two sites that spread `{...policy, filler}` are
*editing* filler and must carry the rest of the policy. The real hazard was
`use-channel-rules-draft`, safe only by accident — its draft is seeded `policy ?? {}`, so applying
before the channel resolved sent a policy with no filler at all.

**The three G items V51f left out shipped immediately after it**, as #237/#238/#239 → PR #240: the
`PodMax` clamp warning with its count, a removed pinned clip rendering as "no longer in your
catalog" instead of a bare hash, and the member-readable `GET /v1/channels/{id}/pods` finally
wired into the channel Info tab.

⚠ **This paragraph said they were "deliberately left for a follow-up" for about a day after the
follow-up merged** — a pointer outliving its work, which is the failure the top of this file
describes and then committed here. Recorded rather than quietly reworded: the two PRs were split
on purpose (one moved `openapi.yaml`, the other could not), and a split like that is exactly when
the doc half goes stale, because the branch that finishes the work is not the branch holding the
sentence about it.

**Two things the V51 line does NOT close, both known:**

- ⚠ **A clip that cannot fit a single pass still never completes.** #225 made a deferral yield, so
  one unfinishable reel stops starving the other 84 — but it never finishes either. WAGA-5 was
  never evidence against this: it is 16m47s and completed in three seconds once the belt stopped
  lying about it (#229). The 2.4-hour recording that prompted V51g is still unsplittable.
- ⚠ **No orphaned-baseline guard.** Deleting a story leaves its PNGs behind — `--update-snapshots`
  only ever writes — referenced by nothing and green forever. Found by hand when V51f's `EraStrict`
  story went; the same hand-maintained-list class as `scripts/check-retired.sh`.

**Next up: nothing in the V51 plan** — V51f was its last phase. The items above are issues, not
phases; see CLAUDE.md's issue-tracker note before writing a row for them here.

⚠ **None of V51f has been verified on the live stack.** It is green in CI and unseen in a browser:
the era range, the untagged-audience admission, the per-criterion meter and the per-channel break
density have never run against the maintainer's Emby/Tunarr. Green is not the same as works.

**V51g — a rung may not spend per SEGMENT what the budget allows per CLIP (2026-08-09,
PRs #223, #225 and #229).** Gate: `make check` (0 lint, `-race`) + `make retired-verify` (28).

⚠ **Four fixes, and only the first was the one that was planned.** Each was correct and exposed
the next, one layer up — and **none of the last three were reachable from any test**; all four were
found by reading output that looked fine.

1. **The planned fix** (#223): delete `classify`, defer instead of failing, detach the writes.
2. **The scheduler had the identical bug** — `UpsertScheduledJob` recorded a job's outcome through
   the context whose expiry caused it, so ANY job killed by its deadline never persisted
   `last_result`/`last_error`/`next_run`. Not filler-specific; every job in the registry. Found in
   the log within a minute of running the first fix.
3. **The polite deferral then STARVED the queue** (#225). Oldest-first work list, so the 2.4-hour
   recording that could not fit a pass was handed the whole budget again on the next one — the
   other **84 clips were never reached**. A deferral now yields (`NextRun` one pass ahead): not the
   backoff a failure earns, a turn-taking rule.
4. **And then the belt could not show that any of it had worked** (#229 — #225 merged while this
   was being written, so it is a follow-up PR, not a commit on that branch). `Status` is the
   CURRENT rung's state, `Disposition` is the clip's. The `VerdictReview`/`VerdictReject` paths,
   and the fatal branch of `onFailure` one function away, set the disposition and recorded the
   rung but left `Status` at the `running` written on entry. A clip handed to a person persisted
   as `split/running, 0%` while its own ladder entry said `done`: **one row disagreeing with
   itself.** `Record` is now the single writer of `Status`, so the next verdict path cannot
   reintroduce it by forgetting a line.

⚠ **Nothing FUNCTIONAL broke, which is exactly why the suite was green over it.** Every store
predicate in `clippipeline.go` keys on `disposition`; not one selects on `status`. What broke was
the picture — `ClipPipeline.resolve` prefers `row.stage`/`row.status` over the visited ladder
(correctly: a rung mid-run has no entry yet), so the pip pulsed *"in progress"* forever and the
rung's note was never rendered. **Nine reels were in that state on the live catalog, not one.**
The three new assertions check the ROW AGAINST ITS OWN LADDER rather than a literal `done` — the
value differs per verdict and is not the point; the disagreement is. Verified by sabotage: all
three go red without the fix, each naming the mismatch.

⚠ **The pre-fix rows do NOT self-heal, and no backfill is shipping for them.** `review` is terminal,
so those clips are never re-run and keep their stale `running`. Live evidence of both halves, taken
either side of the rebuild: `Jacobs Ladder Commercial blocks` reached review at 17:36:05Z and
reports `split/done`; `WAGA-5` reached it at 16:46:06Z and still reports `split/running` against a
ladder that says `done`. They clear when an operator reviews them, which is what they are waiting
for. Same reasoning as V52 phase 8: no production installs, so a migration would be code written to
fix data that exists only on this dev box.

⚠ **`advanced=0 completed=0 rejected=0 failed=0` is what total starvation looks like**, and it is
also what a healthy idle pass looks like. The field that distinguishes them — `deferred` — was the
one added to the struct and forgotten in the log line. The old code at least said "failed". **A
number removed from a log is a number an operator stops being able to act on.**
Diagnosed from a live catalog, and the measurements are in §10 (V51g) because they are what
corrected the diagnosis twice.

**The symptom.** `WAGA-5/Fox Commercial Breaks(2/5/1995)`, a 16m47s reel, sat at *"Finding the ads
inside"* through **twelve** passes — `attempts: 12` against a `MaxAttempts` of 3 — failing every
two minutes with `context deadline exceeded` and starting over. ~25 minutes of GPU re-doing the
same first third while the row animated as though it were progressing.

**Measured on the real file, which is what found the cause** (the first two theories were wrong —
"the detection scan is too slow" and "cutting is too slow" are both off by two orders of magnitude):

| Step of `split` | Cost |
| --- | --- |
| blackdetect + silencedetect | **4s** (319× realtime) |
| dedup — `GrayFrames` × 51 | **33s** |
| cut — stream copy × 51 | **3s** |
| **`classify` — one LLM turn × 51** | **≈377s**, against a 120s pass |

⚠ **`classify` was strictly-worse duplicate work, not merely expensive.** It called the same
`Classify` the `tag` rung calls, but with `SplitSegment.Transcript` — EMPTY unless `rescue` ran, and
rescue only transcribes segments over ~120s (none qualified). So it classified 51 adverts from a
generated name, `"… part 7"`, identical bar the number — then every spawned segment ran
`transcribe` → `tag` and called the same function again with a real transcript. **Deleted, not
unwired**: an orphaned method reads as a capability someone can switch back on.

⚠ **The severe half is not about split at all.** `onFailure` computed the failure record, the
backoff and the `MaxAttempts` resolution and wrote them through **the context whose expiry caused
the failure** — so every one was discarded, and only the pre-work write (`status=running`,
`attempts++`) survived. **Any rung that ever times out loops forever**; split was just the first
slow enough to prove it. All decision-writes now go through a detached context.

⚠ **Running out of time is not failing.** A cancelled context is a DEFERRAL: back to `queued`, the
attempt rolled back (it was counted before the work began), no backoff, resume next pass — which is
exactly how budget exhaustion already behaved. `ErrDeferred` and `PipelineResult.Deferred` keep it
out of the failure count, because a reel too slow for one pass was emitting an identical WARN every
two minutes and the summary blamed the clip.

⚠ **The fake store had to start honouring cancellation before any of this was testable.**
`pipeMemStore.UpsertClipPipeline` ignored `ctx`, so the code path that wrote through a dead context
looked perfect in tests and failed only against a real store. Sabotage-verified: restoring the
attached save reproduces the original bug precisely — `Advance` returns `context canceled`, the row
keeps its burnt attempt, and the run counts it as a clip failure.

**Three grounding assertions MOVED rather than being dropped** (§8 is not negotiable). They
exercised `Classify` through `Propose`'s removed call; the rule is tested at its own seam —
`TestClassify_EraGroundedBySourceText` and `TestClassify_UngroundedEraBecomesSuggestion`. What
`Propose` owes now is the CUT, and the tests assert segments arrive with **no** tags at all.

⚠ **Known gap, deliberately not built.** A ~3-hour capture is ~500 segments, so the fingerprint pass
alone would be ~5 minutes and exceed a pass again. The fix then is a per-pass SEGMENT budget with
resume by `(ParentHash, index)` — the lineage column exists (§10 V45, migration 00039). Not built
because the measured corpus does not reach it, and resume interacts with proposal editing in ways
that need their own design.

⚠ **This entry describes the FIRST half of #208. The second half — the conveyor merge — was found
by looking at the rendered result and is recorded in §10 (V51e).** Briefly: `asks` and `pipeline`
shipped as two arrays over overlapping populations, and on 85 real clips **84 appeared in both** —
a row demanding a decision above a row captioned "nothing here needs you". The state that fixed it
already existed and was never consulted: V51b's `review` disposition means exactly "the machine is
finished and a human is needed", which is the population `asks` was inferring from tag-shape. One
array (`clips`), `needsDecision` per row. `total` counted `len(asks)` — the same number by accident
and the wrong rule by construction — and now counts `NeedsDecision`, so the badge and the list
finally agree. Verified live: 85 rows, 85 unique hashes, badge 0.

⚠ Also found by looking, not testing: `resolve` collapsed `queued` into `running`, so all 85
enrolled clips rendered as actively being worked on when one was. And `role="alert"` on an `<li>`
replaced its implicit `listitem` role, breaking the ladder's list semantics — an axe `serious` that
was **hidden behind a colour-contrast failure on the same element** until that one cleared.

**V51e — the pipeline becomes visible; V51b's API finally has a renderer (2026-08-08, branch
`v51e-incoming-pipeline`, stacked on V51d).** Gate: `make check` (0 lint, `-race`) + `make fe`
(biome + tsc + unit + SPA + storybook build) + `make openapi-verify`. ⚠ **`make fe-visual` and
`make e2e` were NOT run locally** — the maintainer's machine cannot carry Playwright, so both are
**CI-verified only**, and three new stories mean the visual job may legitimately need baselines.
Said plainly rather than implied green.

**V51b built an ordered, watchable pipeline and shipped it to an audience of zero.**
`GET /v1/filler/incoming` carried `pipeline` and `rejected`, `eventTypeMap` carried a
`filler_clip` frame, `FillerClipEvent` was a typed DTO reaching orval — and `grep onFillerClip
web/apps/web/src` returned nothing. The operator-visible symptom V51b existed to remove ("I
downloaded forty commercials and nothing is happening") **survived V51b unchanged**, because
every fact needed to fix it was being served to a frontend that never asked. That is the shape
worth remembering: a phase can be complete on its own terms and deliver none of its purpose.

⚠ **Two contract gaps between the V51 plan and what V51b actually built, both found by reading
the Go rather than the plan.**

**1. The plan's reason for "stages come from the server" was wrong; its conclusion was right for a
different reason.** It argued installs vary in ladder LENGTH (vision off → 7 rungs). They do not:
`filler.StageOrder` is a fixed eight-element compile-time constant and a disabled rung is recorded
as `skipped`, which the plan separately insists must be rendered *with its reason*. The real
constraint is that `IncomingPipelineDTO.stages` is the **visited** ladder — a clip at `split`
sends three records — so a strip drawn from it would GROW as the clip advanced instead of filling.
The response now carries `stageOrder`, derived from `StageOrder` itself. ⚠ Its guard compares
against `filler.StageOrder` rather than a literal list, because a literal here would be the second
copy of the sequence the field exists to prevent, and editing it is exactly how a real drift gets
buried.

**2. The plan's out-of-order guard had nothing to key on.** It specified "merge only ever advances,
using the BE's monotonic `seq`" — `FillerClipEvent` has no `seq` and no timestamp. Ordering is
derived from the ladder instead, and the rule is deliberately narrow: **a stage or status change
is always applied; only the percentage within one rung is guarded.** Strictly advance-only was
rejected on the maintainer's call — `Rewind` moves a clip backward on purpose, and a strict guard
would blank the entire re-run until something forced a refetch. ⚠ **The status half is
load-bearing and was not in the original choice**: `pipeline.go`'s retry path re-runs a failed rung
with `Progress` reset to 0, so guarding on progress alone would pin the row at "failed at 80%"
while the transcode had genuinely restarted. It has its own test.

**Three defects found while building, none of which any existing test could see.**

⚠ **A false green, and the mechanism is worth recording.** The Bash cwd silently reverted from the
worktree to the primary repo mid-session (the trap `loomarr-bash-cwd-resets-use-git-c` already
documents for *commits*). Edits landed correctly by absolute path; `go test ./internal/api/` ran
against **main's** copy and printed `ok`. A brand-new test was reported verified having never been
compiled — and once run in the right tree it did not even BUILD (`int` vs `int64`,
`filler.KindCommercial` undefined). The tell was `[no tests to run]` on a `-run` regex naming a
test that certainly existed: **a `-run` filter matching nothing exits 0**, so a typo and a
wrong-directory run are indistinguishable from a pass. Use `-v` on a new test's first run; `--- PASS:
<name>` proves it existed, `ok` does not.

⚠ **The pipeline half of `/v1/filler/incoming` shipped in V51b with no API test of its contents at
all** — the same shape as V51a's `clips.confidence`, where a column round-tripped perfectly and had
no producer. A row is drawn as a clip card, so `durationMs` and `thumbnail` were added from the
lookup `pipelineDTO` was already performing, and all three fields are now asserted against a clip
whose values are deliberately distinct from each other and from its hash.

⚠ **The strip and the expanded ladder both claimed the accessible name `Progress for <clip>`**,
putting two identically-named lists in the tree for one row. It surfaced only because
`hidden="until-found"` keeps a collapsed panel in the DOM — so `queryByText` found detail that was
never exposed, and the first draft of the "stays collapsed" test **would have passed against a
panel that never collapsed**. Names are now variant-specific and the test queries by ROLE, which
asserts exposure rather than presence.

**What was deliberately not built, so it reads as sequenced rather than forgotten.** Preview-then-
accept on a pipeline row (a clip mid-`transcode` is being rewritten by ffmpeg; previewing it is a
question this phase does not answer); the `CollapsibleSection` refactor onto the new `Disclosure`;
migrating the app's three other progress bars onto `Progress`; and V51d's two parked capabilities
(the composite container row, the sort control). The catalog decomposition of `filler-page.tsx`
also stays open — this slice took the Incoming half only.

**Sabotage-verified, each confirmed red then reverted:** the `stageOrder` drift guard (dropped one
rung), the pipeline-row DTO (dropped `DurationMs`), the SSE merge guard (always-advance), the merge
itself (wholesale row replacement), the ladder source (drew from `stages` — took 3 tests red), the
events provider drift guard (removed one of the two wirings), and the collapse rule (`defaultOpen`).

**V51d — the catalog is paged, sorted, and searched wider (2026-08-09).** Gate: `make check`
(0 lint, `-race`) + `make test-pg` (both dialects, **4 new conformance suites × 2 backends**) +
`make fe` (biome clean on 918 files + tsc + unit + SPA + storybook build) + `make openapi-verify`
+ `make retired-verify` (25 identifiers) + `make config-docs`.

`GET /v1/filler` returned **every clip in the install** on every call, and four clients depended on
it. `limit` now defaults to 100 and caps at 500, `total` rides every response, and the listing
sorts by `name|duration|added|plays|confidence` in either direction. This also removes a latent
hard failure: `attachTags` binds one parameter per clip in a single `IN (…)` and Postgres caps a
statement at 65535, so the unpaginated read stopped working north of ~65k clips.

⚠ **`ClipFilter.Limit == 0` means NO limit, and the default lives in the API.** Pod assembly loads
the catalog through the zero filter, so a store-side default of 100 would silently cut every
channel's break pool to a hundred clips — no error, no log line. `TestListFiller_DefaultsToOnePage…`
pins the number at the HTTP edge for exactly that reason, and `minimum:"1"` on the parameter stops
a client reaching the store's unbounded sentinel with `limit=0`.

⚠ **The sabotage pass found the property I expected to be load-bearing was not.** Deleting the
`hash` tie-break left `PagesConcatenateToTheWholeList` **green on SQLite** — its plan happens to be
stable across `LIMIT/OFFSET` re-executions, so the property that exists to catch a missing total
order could not catch it there. What does catch it, deterministically and on both backends, is
`DescendingIsTheExactReverse`. Removing `LOWER(name)` fails loudly (SQLite's BINARY collation puts
`'Z' < 'a'`), and removing the frontend's `page: undefined` reset turns its own test red. A
first-try green on a new constraint stays suspect.

**Four unbounded consumers, four different fixes** — the point is that paging *deleted* three of
them rather than paginating them: the dashboard's clip count reads `/v1/filler/watch`'s SQL-counted
total (it was fetching every column of every clip for one `.length`); the channel pin/exclude
resolver asks for **the hashes it holds** (`ClipFilter.Hashes`) — the old catalog-and-map shape was
not merely wasteful under paging but WRONG, resolving whichever pins landed on page one; the ⌘K
palette asks for the six rows it renders, through one `CLIP_RESULTS` constant shared with the
slice; and the Filler page wires the pager.

⚠ **The highest-risk line is the frontend's, not the store's**: every filter change must reset
`page`. `setFilters` merges blindly, so without it, typing in the search box on page 7 lands on an
empty page 7 of a two-page result and renders "No clips match" over a catalog that matches plenty.
The rule lives in the one function every filter control calls, and is sabotage-verified.

**Search widens** to `name | brand | visible_text | tags` (an `EXISTS` over `clip_tags`, never a
JOIN — a clip with three matching tags must be one row, or `CountClips` counts it three times and
the total contradicts the rows). `transcript` stays behind `QueryTranscript`: kilobytes per clip
and "ford" matches "afford". **No FTS** — FTS5 and `tsvector` are different engines with different
tokenizers, which would force `ListClips` to branch on dialect and the suite to assert
equivalent-but-not-identical results per backend (§5 forbids it).

**Migration `00046`.** V51b took `00044`; `00045` went to V52's images table, which merged first — so this was renumbered from `00045` before landing, while it was still unapplied anywhere. `clips.created_at`, because
`updated_at` is bumped by every re-sync and an "added" sort backed by it would reshuffle the whole
catalog after a routine scan. Existing rows backfill from `updated_at` as a stated estimate. It is
the **fourth** column omitted from `UpsertClip`'s `DO UPDATE`, and the only one with no `Set…`
writer at all: nothing may ever change when a clip arrived.

**Two capabilities land in the store and API but are deliberately NOT switched on in the UI yet**,
both sequenced to V51e rather than left as accidents:

- **Composites-as-containers.** `TopLevelOnly` + `IncludeComposites` work and are conformance-
  tested, but the catalog listing does not pass them, because **nothing in the frontend renders
  `isComposite`** (`grep` finds zero uses outside the generated client). Turning them on today
  would draw a 16-minute recorded break as an ordinary playable clip card with no "NOT AIRABLE"
  marker — worse than the current inverse, where segments show as flat rows. V51e owns the
  container row (the `NN CUTS` badge, the expand chevron, the segment grid).
- **Sort.** The five keys and both directions are live on the wire and pinned by the concatenation
  property; the catalog sends none of them, because the control is V51e's `Select` with the
  direction baked into each option. Recorded here so it is a sequenced gap, not a surface-audit
  orphan.

⚠ **`hashes` is comma-separated on the wire, not repeated.** Huma emits `explode: false`, so
`?hashes=a&hashes=b` parses as one value and silently resolves one clip; orval's generated URL
builder calls `value.toString()` on the array, which produces the right thing. An API test asserts
the comma form so the two halves cannot drift.

**V53a — the form schemas stop mirroring the wire and start deriving from it (2026-08-09, branch
`feat/zod-from-spec`).** Gate: `make fe` (**1222** app + **18** api + 51 core + 5 tokens, biome clean
on 919 files) + `make retired-verify` (25 identifiers). Zero Go files touched.

`packages/core`'s three zod schemas mirrored wire field names **by hand**, and that shipped a bug:
`intentSchema` said `maxAcquire` where the wire says `maxAcquisitions` and `runtimeTarget` where it
says `runtimeTargetMin`. Both parsed, both serialized into JSON the server ignored, and a user's
acquisition cap and runtime target silently vanished. A contract test was written afterwards to
catch it — and covered **one of the three**. `bootstrapSchema` and `loginSchema` were unguarded
(checked: neither had drifted, yet).

Orval now emits zod schemas from the same spec (`@loomarr/api/zod`), and each form schema is
`.pick()`ed off its wire schema, then `.extend()`ed with the rules. **A lookalike name is now a
compile error at the schema definition**, for all three and every future one.

⚠ **The error message is cryptic and worth recognising**: picking a key that is not on the wire
fails as `Type 'true' is not assignable to type 'never'`. It means exactly one thing — *that field
does not exist on the wire*. Verified by sabotage: restoring `maxAcquire`/`runtimeTarget` fails
`tsc` at both lines.

⚠ **Generation carries NAMES and TYPES, not RULES — and the split is deliberate, not a shortcut.**
This spec declares 5 `minimum`, 3 `maximum` and 7 `minLength` across ~9k lines, `maxAcquisitions`
has no bounds at all, and OpenAPI has nowhere to put a user-facing message. So the trims, the 0–200
cap, the 8-character password floor and every message stay hand-authored in `.extend()` — and
`confirm` (form-only, never sent) is added there too. That is why the pattern is pick-then-extend
rather than a straight derive: **the wire decides which names are real; the form is free to add its
own on top.**

⚠ **The contract test was kept, not deleted, and the reason is a real distinction.** `.pick()`
guarantees the NAMES exist on the wire; `intent-contract.test.ts`'s assignability check guarantees
the parsed OUTPUT still satisfies `Intent` after `.extend()` rewrites the value types. Proven
complementary rather than assumed: changing `maxAcquisitions` to `z.string()` in the extend produced
**0 errors** in core and **failed** the assignability check. Two claims, two guards.

**The zod barrel is hand-written and therefore guarded** (`barrel.test.ts`), same as the endpoint
barrel — a generated module nobody can import is indistinguishable from one that was never built.
⚠ It checks the ZOD output directory, not the endpoint one: `events` is SSE and has no schemas to
generate, so comparing against endpoints would fail forever on a tag that is correct to be absent.
Sabotage-verified (dropping `users` reports it by name). It is FLAT where the endpoint barrel is
namespaced — namespacing exists there only because orval repeats helper enums per tag file, and zod
names are operation-scoped and collision-free.

⚠ **Only the zod half of orval's generation was adopted; the mock generator was measured and
rejected for its DATA.** It targets OpenAPI 3.0 idioms and this spec is 3.1: **109 nullable
type-unions vs 4 plain arrays, 0 `nullable: true`**. Meeting `type: ["array","null"]` it emits
`arrayElement([[], null])` without descending into `items` — **137 list fields that are never
populated** — and `useExamples` reads singular `.example` where Huma emits plural `examples:`, so
**0 of 53** example tags are used, across **1304 unseeded faker calls**. Not configurable away. The
zod generator handled the same 3.1 spec correctly, which is what isolates the gap to the mock half.


**V51c — sources roll up by provider, with no column, no table and no migration (2026-08-09).**
Gate: `make check` (0 lint, `-race`) + `make test-pg` (both dialects) + `make openapi-verify` +
`make retired-verify` + `make config-docs`.

Three archive.org collections sat as three sibling rows with no indication they are one service.
The Sources tab now shows one **Archive.org** row and one **YouTube** row, each twirling down to
the targets beneath it — and the whole thing is **derived from `kind` at read time**.

⚠ **The argument for deriving it is a correctness argument, not a shortcut: the grouping being
asked for is already a column.** Every `archive` row belongs under Archive.org and there is no
representable case where it belongs elsewhere, so a stored `parent_id` would be a second encoding
of a fact `kind` already carries — and second encodings make illegal states representable
(`kind='archive', parent_id='provider:youtube'`). Three concrete costs it would have added, each
measured against code that exists: a **duplicate** blank-URI YouTube row beside migration 00034's
seeded one (both invisible to `idx_filler_sources_uri`, whose `WHERE uri <> ''` excludes them —
"one source appears twice", which 00023 and 00029 both exist to prevent); a **four-state** inherit
problem for the nil/0/N fetch overrides whose own ⚠ says callers "must not re-derive this
three-state logic separately"; and a 409 on any pending pull, because `filler_pulls.plan_json`
stores `SourceID` strings looked up at approve time.

The escape hatch is recorded in §10 so it is not re-litigated: a provider that ever gains state of
its own gets a `filler_providers` table keyed on the existing `kind` vocabulary — **not**
`parent_id`, because that state is per-provider, not per-node.

⚠ **The phase found a guard that had been protecting nothing for two phases.**
`TestSetFillerSourceEnabled_RefusesRowsWithNothingToStop` asserted a 409 for `PATCH
/v1/filler/sources/remote`. V37 retired that container, so from that point no read model could
produce the id and no client could send it — the test stayed green by asserting a refusal for a
row that did not exist, and the handler kept a `case "remote"` that read as protection. The DELETE
handler had already removed its twin for exactly this reason, in a comment saying so; the PATCH
side was simply missed. The guard is now a **prefix test over the derived provider ids**, which
the read model emits on every request, so it covers a case an operator can actually reach.

**Three things deliberately do not inherit**, each an opinionated call: `enabled` (no group switch
— cascade-on-write destroys each child's own choice, which the store forbids in as many words, and
a computed `effective = parent && child` fails in the direction of *fetching from a provider the
operator switched off*); fetch overrides (leaf only); and `lastFetchedAt`, which becomes a
read-only `MAX` over children computed in the API so no column can disagree.

⚠ **Both new behaviours were sabotage-verified**: splitting the pre-order into "all groups, then
all children" turns the ordering test red, and disabling the prefix guard turns the 409 test red
(404, not 409 — the id resolves to nothing). A first-try green on a new constraint is suspect.

**Honest gap, exposed rather than created:** `sync.go` writes `Source = "filler-dir"` for every
clip the folder scan finds and nothing records which SOURCE a downloaded clip came from, so
`bySource["archive"]` is 0 on essentially every install. A group reports the **sum of its
children's counts** — honest arithmetic over whatever they claim, never an invented number.
Per-source attribution is an intake change, filed separately.

**V51b — ingest becomes one watchable pipeline; seven sweeps become two jobs (2026-08-08).**
Gate: `make check` (0 lint, `-race`) + `make test-pg` (both dialects) + `make openapi-verify` +
`make retired-verify` (**25 identifiers**, up from 9) + `make config-docs`.

Filler had grown **one cron sweep per capability** — sync, fetch, language, split, transcribe,
vision, reindex — each scanning the whole catalog for its own kind of work, on its own schedule,
knowing nothing about what the others had done to a clip. The operator-visible consequence was
that a download of forty commercials said *"waiting to be checked"* for up to an hour while three
jobs worked on it at :15, :30 and :50. **The system was working and looked broken.**

Eight rungs now run in order per clip — `probe → transcode → split → language → transcribe → tag →
vision → score` — behind one `filler-pipeline` job every two minutes. Each rung answers two
questions separately: *does this apply to this clip, in this install?* (no exec, re-evaluated
every pass, so flipping `filler.vision.enabled` on picks up clips that already went past it) and
*do the work*. State lives in `filler_clip_pipeline`, a **sibling of `clips`** — the cache has
been DROP-TABLE-recreated twice, and these rows record that ~341s of Whisper and a paid vision
call have already been spent.

⚠ **Three findings, all live on `main` before this phase, none of which any test could have
caught.**

**1. `filler.autofile.normalize_loudness` has been inert since V42 shipped.** `NormalizeInPlace`
had **no production caller at all** — only tests. The settings comment asserted the opposite,
citing §15's own rule that a setting nothing reads does not exist: *"this one lands with its
consumer (`filler.NormalizeInPlace`, called from the auto-file step)"*. A comment claiming a
consumer exists is not a consumer existing, and nothing failed when it stopped being true. The
transcode rung applies the loudness filter in the pass that is already re-encoding, which finally
wires the toggle.

**2. The score rung would have silently disabled half of `Score`.** `Score(modelConfidence)` lets
the model LOWER the grounded ceiling and never raise it — so *"unsure about a clip whose tags all
verify"* still reaches a human. That self-report exists only inside the tag rung; recomputing from
the persisted row with 0 would score the clip a full 100 and auto-file it. `ScoreClip` passes the
row's confidence as the model layer, so lowering survives and raising stays impossible.

**3. A composite reaching the score rung would have destroyed the reel its segments come from.**
Composite detection moved to `probe` (so a 16-minute recording stops being airable when it is
MEASURED, not when someone finally splits it — §10 V45's bug). But a compilation has no coherent
grounding, so `filler.reject.unidentified` — **ON by default** — would tombstone it. The skip is
**one rule in `advance`**, not six `Applies` checks: six copies is six chances for a new rung to
forget it, and forgetting is silent.

**What was retired, and the one reversal.** Four jobs and their schedule keys, `filler.split.every`
(splitting is a rung every long recording reaches, so "how often do we go looking" stopped being a
question with an answer), and `NormalizeInPlace`. ⚠ **`filler.autosplit.enabled` flips to ON**,
reversing a default whose ⚠ argued for OFF because a mis-cut clip plays half an advert and the
source is consumed either way. That risk is unchanged; the evidence moved — the gate's measured
failure mode is refusing GOOD reels, and off-by-default meant every compilation waited for a click
the design says should be unnecessary.

⚠ **Both drift guards fired during the work, which is the best evidence they work.**
`TestEveryPublishedEventIsInTheEventTypeMap` reads `internal/app` as SOURCE for
`Type: "…", Payload: api.X{` — hoisting the new frame into a local variable hid it, and the guard
reported `filler_clip` as declared-but-never-published. `retired-verify` then caught eleven stale
references to the retired identifiers, including two settings comments that were now simply false.

**Tests: the four retired jobs' suites were PORTED, not deleted** — `stage_language_test.go`,
`stage_transcribe_test.go`, `stage_vision_test.go`. Three cases went with the sweeps rather than
being rewritten (`BoundsOnePass` × 3): the bound is now the runner's budget, and asserting it
against a stage would be testing something that file no longer owns. The grounding tests — the
ones that matter — survive unchanged, because `groundVisionTags` did not change.

**V51a — the filler pre-flight: four dead code paths, one blind fixture class (2026-08-08, `7119d92`).**
Gate: `make check` (0 lint, `-race`) + `make test-pg` (both dialects) + `make openapi-verify` +
`make retired-verify` (9 identifiers) + `make fe` (**1196 tests**, up from 1190).

⚠ **This is the V41 lesson recurring, in the one place V41 did not look.** V41's entry below says
the identity/location fixture blind spot "was written into a comment and the fixture was never
fixed" — it fixed `clipAt`, `sampleClip`, `untaggedClip` and `tagMemStore`, and left the SPLIT
fixtures alone. All four defects here lived behind exactly those.

**1. Split confirm had never worked since V38c.** The persisted proposal stored `clip.Path` in a
field named `clipPath`; `Confirm` fed it to `GetClip`, which is `WHERE hash = ?`. Every confirm
returned *"compilation … no longer in the catalog"* for a clip that was in the catalog — an
operator could detect, open a 41-segment reel, edit it, and never commit. Reproduced first against
a real store, then fixed: the proposal carries `clipHash`, the file location is derived from the
row exactly as `Propose` already derives it.

**2. Every segment upserted with an empty hash.** `UpsertClip` is `ON CONFLICT(hash)`, so segment
N overwrote segment N−1 and a whole reel became **one** row. Cuts are now hashed the moment they
are written and filed at their own shard path, which retires `uniqueClipPath`/`sanitizeClipName`
(content addressing leaves no name to sanitise and no collision to break). **Sabotage-verified**:
removing the hash assignment collapses the test to one row.

**3. `dedup`'s self-exclusion never fired.** Parameter named `clipPath`, tested against `c.Path`,
called with the hash — so every segment was compared against the compilation it was cut from and
came back flagged a duplicate. Noise in the review, and enough for `AutoConfirmable` to reject a
sound reel.

**4. `clips.confidence` had no writer, in any catalog, ever.** `Score` computed the
grounding-capped number and `Tagger.Run` used it for the filing decision and dropped it. Auto-file
worked; the number an operator judges it by was permanently 0, so the Incoming meter never
rendered. `SetClipConfidence` is the writer now. ⚠ The store's conformance case passed the whole
time because it seeded the value through `UpsertClip`'s INSERT — **a column can round-trip
perfectly and still have no producer**.

**Plus a live FE defect found on the way.** `useLoomarrEventListener` never re-dispatched
`onPlayout` or `onDatabase`, so `settings/system/database.tsx`'s migration progress was dead. The
drift guard was green because it regexed BOTH handler objects in one pass and asserted their
**union** — a guard that ORs its two sources cannot see drift between them. It now slices and
asserts each independently, and throws rather than degrading if its anchors move.

**The durable half.** Both split fixtures are hash-keyed with identity and location deliberately
unequal; `seedCompilation` returns the hash so no test can re-derive it; the fake `Cut` writes
span-derived bytes (identical bytes would make segments legitimately collapse, masking the bug);
and `Confirm` is exercised end to end against a real store for the first time.

⚠ **The register is still behind.** V42, V44, V45a and V46–V49 shipped without rows; this entry
sits above V41 because that is the last recorded phase, not because nothing happened between.
Back-filling them is scheduled with the V51 doc pass — see the plan.

**V50c — CollapsibleSection onto Base UI, and a collapsed section stops being focusable
(2026-08-08, branch `feat/base-ui-v50c`).** Gate: `make fe` (**1222** app + 17 api + 51 core + 5
tokens, biome clean on 918 files) + `make fe-visual` (**764 passed, 0 failed, 0 flaky, 0 axe**, no
baseline changed) + `make check` + `retired-verify` (14 identifiers).

⚠ **Scope is ONE component, deliberately.** V50c was sketched as "disclosure/form primitives" and
the form half is dropped: `checkbox` and `switch` are native `<input>`s carrying explicit decisions
AGAINST a primitive, and both record that the reasoning **predates V50a and survives the vendor
change**. Porting them would re-implement semantics the platform already gets right. `search-command`'s
hand-written `role="combobox"` was audited in the same pass and honours its contract in full. **The
sketch was written before anyone read the files; the files had already decided.**

**What the port buys.** The old version's header a11y was already correct (a real
`<button aria-expanded aria-controls>`), so the win is `hiddenUntilFound`: the browser's find-in-page
reaches text inside a CLOSED section and opens it, where `overflow:hidden` left it findable by
nothing.

⚠ **And it removes a defect two tests were resting on.** The old `.reveal` closed with
`grid-template-rows: 0fr` + `overflow:hidden` — zero height but **NOT `display:none`** — so collapsed
controls stayed in the accessibility tree and stayed **focusable**. A keyboard user could Tab into a
section they could not see. Two `channel-filler` tests reached a control inside a closed body with
`findByRole` and passed; they open the section first now, as a user must. **The bug surfaced as a
test that could only pass while it existed.** ⚠ Its failure mode is deliberately unhelpful and worth
recognising: `asyncUtilTimeout` and `testTimeout` are both 5000ms, so findBy's own "Unable to find
role" never surfaces — the test times out first and reports only `Test timed out in 5000ms`.

⚠ **`hiddenUntilFound` is load-bearing, not a bonus.** Sabotaging it to prove the new test could fail
showed both tests failing with *"Unable to find an element with the text"* — Base UI's Panel
**unmounts its children when closed** by default, where the hand-rolled version always kept the body
mounted and merely clipped it. A port that swapped the elements and adjusted assertions to match
would have shipped a silent change to a **mounting** contract, breaking anything holding form state,
scroll position or a ref in a collapsed section. Nothing in the gate reads a mounting contract.

⚠ **The motion stayed hand-rolled, verified rather than assumed.** Base UI measures the panel
(`scrollHeight` → `--collapsible-panel-height`) so an author can transition `height`; the `.reveal`
grid-rows 0fr→1fr trick is height-agnostic, so nothing is measured and a body that changes size
mid-open cannot desync from a stale measurement. The primitive owns state and semantics; the
stylesheet still owns motion. Reduced-motion needed no work — it is a global `*` rule inside a media
query, independent of implementation.

⚠ **The CSS rule is two selectors on purpose.** `.reveal` has a second consumer: `connection-block`
borrows its mechanics and passes `data-open={open}`, a React boolean, while Base UI emits `data-open`
VALUELESS when open. Loosening the rule to `.reveal[data-open]` would match the **string `"false"`**
that React renders for `data-open={false}` — React stringifies booleans for `data-*` rather than
omitting them — and pin connection-block permanently open. **Caught by nothing:** jsdom does not
apply the stylesheet, and connection-block has no story for the visual suite to snapshot.

**Doc-first (§14):** no dependency conversation needed, because the row already anticipated this
("the primitives the app still hand-rolls … stop needing a §14 conversation each"). ⚠ But its list
was stale in **two** places — `menu` left it in V50b without the doc catching up, and `collapsible`
leaves it here. Both removed, with `combobox` recorded as deliberately kept rather than merely
omitted.

⚠ **KNOWN GAP, not fixed here.** `connection-block` still closes with the bare `.reveal`, so its
collapsed body — a Test action and a Fix link — **remains focusable and announced**. Same defect,
left alone because scope is one component; it has no story, so axe never sees it. This is the
strongest candidate for the next slice.


**V50b — the hand-rolled overlays fold onto the primitives (2026-08-08, branch
`feat/base-ui-v50b`).** Gate: `make fe` (**1221** app + 17 api + 51 core + 5 tokens, biome clean on
911 files + tsc + SPA build + storybook build) + `make fe-visual` (**764 passed, 0 failed, 0 flaky,
0 axe**, and **no baseline changed** — V50b touches no story or spec) + `make check` +
`retired-verify` (14 identifiers).

Five components, and **two of them were asserting accessibility they did not implement**. That is
the through-line: a hand-rolled overlay can spell `role="dialog" aria-modal="true"` into the DOM
without owning a single behaviour the role promises, and nothing in this repo's gate reads a role
and checks whether it is TRUE. Not types, not lint, and — the surprising one — not axe, which
validates that attributes are well-formed, not that they are honest.

- **`channel-row-menu` → Menu.** Deletes the app's only `createPortal`, a `getBoundingClientRect`
  layout effect, three hardcoded pixel constants, both-axis viewport clamping, flip-up-on-overflow,
  `invisible`-until-measured, a full-bleed `<button>` backdrop, capture-phase scroll/resize
  listeners, and hand-written menu roles. ⚠ Two were latent BUGS, not verbosity: `PANEL_MAX_H = 210`
  went stale the moment the menu's content changed (nothing enforced it, and the armed-confirm state
  is the tallest), and **dismiss-on-scroll was a workaround for an anchor the portal could not
  track** — the popup follows its trigger now instead of closing. Gains arrow-key nav, typeahead, a
  focus trap, focus restore and Escape, none of which it had.
- **`command-palette` → Dialog.** It claimed `aria-modal="true"` on a `fixed inset-0` div with no
  portal, no focus trap, no focus restore, no scroll lock and no inert background: a screen-reader
  user was told the page behind was inert while Tab walked straight into it, and closing dropped
  focus at the top of the document. ⚠ Escape moved WITH it — `useCommandShortcut` bound Escape at
  the window only because the palette had no dismiss of its own; keeping both would be two closers
  racing.
- **`restart-overlay` — ⚠ DELIBERATELY NOT AlertDialog, against the plan.** It made the same false
  claim, and the planned fix was to make the claim true. Reading the spec says otherwise:
  `alertdialog` is defined for interrupting with a REQUIRED RESPONSE and needs a focusable element,
  and this overlay has no interactive content in any of its three states. Worse, its "came back"
  state is deliberately `pointer-events-none` so a lingering confirmation cannot swallow the
  operator's next click — which a modal forbids. The false attributes are dropped instead: `status`
  (polite) normally, `alert` (assertive) on failure. **A real modal would have satisfied the audit
  and broken the interaction.**
- **`timeline-scrubber` → Tooltip positioner with a virtual anchor.** Deletes `CARD_WIDTH = 256`,
  which duplicated the `w-64` class and would decouple the moment either changed; `shift()` now
  keeps the card on screen against the VIEWPORT, measured rather than assumed, and the portal
  removes the `overflow:hidden` clipping risk.
- **`search-command` — ⚠ RE-EXAMINED AND DELIBERATELY KEPT**, with the reasoning written into the
  file so it is not re-litigated from scratch. The standing objection ("cmdk would need a §14
  change") dissolved when Base UI landed, but Autocomplete still does not fit on SHAPE, not effort:
  it is Portal→Positioner→Popup with no inline mode, and this is an always-visible panel embedded in
  six layouts that each position it themselves. The v2 mock decides it — the ⌘K block draws a modal
  overlay with the list in a centred card, which is what the Dialog port produces; a floating
  combobox would be moving AWAY from the mock.

⚠ **A hooks-order violation shipped in the first cut of the scrubber and is worth recording**,
because the test that should have caught it existed and could not. The anchor's `useMemo` sat BELOW
the empty-airings `return null`, so an empty render runs one fewer hook than a populated one and
React — which matches hooks by CALL ORDER — throws on a channel whose guide data empties out. The
existing "renders nothing when there are no airings" test mounts once with `[]`, so it never sees a
change in hook count and passes either way. **A test can cover a state and still not cover the
TRANSITION into it.** The added regression test rerenders one instance both ways; confirmed red
against the pre-fix component with the other three tests passing beside it. `trackW` left hover
state in the same pass — the clamp was its only reader, and a width still riding in state would read
as though the card bounds itself to the strip, which is exactly what stopped being true.

⚠ **The scrubber's visible behaviour change is NOT covered by the visual suite, and the reason
generalises**: near the track's ends the hover card now stops at the viewport edge rather than the
strip edge. No baseline moved — because the card only exists while hovering and the gallery never
hovers. Those stories cover the STRIP, not the card, and they would not have moved if the change
had broken it either.

**Test harness:** `asyncUtilTimeout` raised to 5s. Turning `getBy` into `findBy` across the
migration took the suite from 2 intermittent failures to 6 — every one passing in isolation, all CPU
contention across parallel specs rather than anything in the code. ⚠ A longer wait cannot make a
broken assertion pass; it only stops a red build that means nothing. Net effect: the two
PRE-EXISTING baseline flakes (help, filler) are green too.

**V50a — Radix → Base UI, the vendor consolidation (2026-08-08, branch `feat/base-ui-v50a`).**
Gate: `make fe` (**1219** app + 17 api + 51 core + 5 tokens, biome + tsc + SPA build + storybook
build) + `make fe-visual` (**760 passed, 0 failed, 0 flaky, 0 axe** on a CLEAN verify run, after a
reviewed 2-baseline update) + `make e2e` (7) + `make check` + `retired-verify` (14 identifiers).

Six `@radix-ui/*` packages → one `@base-ui/react`; **823 lines of transitive lockfile closure
deleted**. Doc-first per directive 1: §14's stack row keeps every primitive's ORIGINAL rationale
(Select-over-native, tooltip-over-`title=`, slider-owns-the-WAI-ARIA-contract, menu-is-not-a-select)
because only the vendor moved.

⚠ **Two user-visible regressions that a compile-clean port would have shipped.** Neither is caught
by types, lint, or axe:

1. **Every Select trigger would have shown its RAW VALUE.** Base UI resolves `<SelectValue>` to the
   item's label only when Root is given an `items` map; all 23 selects here pass options as inline
   `<SelectItem>` JSX. Triggers would read "240" where the list says "4 hours". Hand-writing
   `items={[…]}` per site would duplicate every label expression (several computed) — the
   two-lists-that-must-agree pattern this repo has been bitten by — so the wrapper DERIVES them
   from the items. Found only because the assertion was written before the port was believed.
2. **Tooltips lost their accessible description.** Base UI's tooltip is visual-only BY DESIGN — no
   `role="tooltip"`, no `aria-describedby`. Harmless for 36 of 37 consumers, whose trigger
   `aria-label` restates the content. Not for `FieldHelp`, which renders each setting's `doc`
   prose: a screen-reader user got "About Ordering, button" and nothing else.

⚠ **The durable half is the COMMENTS.** Three explained why something was the way it was, each was
true under Radix, and each would have silently become a lie: `field-help` claimed the SR user
"hears the same guidance"; `volume-control` put `aria-label` on the Thumb "because Radix puts
role=slider there" (still the right placement — Base UI nests an `<input type="range">` — but a
different reason); `setup.ts` credited five jsdom shims to Radix. Removing the shims one at a time
proved the pointer-capture trio was **dead weight** and that `scrollIntoView`, the one that matters,
was never Radix's need — it is our own `search-command`, and removing it fails 31 tests.

Other findings: Base UI mounts portalled popups **asynchronously**, so six tests needed `findBy`
over `getBy` (waiting, not weakening); `closeOnClick` defaults false where Radix closed on select
(preserved for the track picker); Menu has no standalone Label, so the heading now NAMES its group;
`asChild` renamed to `render` at all 11 sites, with both `@radix-ui` and `asChild` added to
`check-retired.sh` so neither vocabulary creeps back. **The single visual delta in 760 snapshots**
was the volume thumb at value 0 — Radix clamped it inside the track at the extremes, Base UI centres
it on the value; 54px, reviewed, baseline updated. Debt register 43 → 40 (tooltip, select, dialog).

**V41 — the audit pass: three live defects, six cleanups (2026-08-05, `aba3b22`, PRs #169–#175).**
Gate, per PR: `make check` (0 lint, `-race`) + `test-pg` (both dialects) + `openapi-verify` +
`retired-verify`; the web PRs additionally `make fe` (**1116 tests**, up from 1108) + `fe-visual`
(**716 passed, 0 axe**, on CLEAN verify runs, never `--update-snapshots`) + `make e2e` (7).

Started as "what needs refactoring?" and found working code was the smaller half of the answer.

⚠ **THE AI TAGGER HAD NEVER TAGGED A CLIP, and play counters had never moved** (#169). V38c split
clip identity (`Hash`) from location (`Path`); three callers kept passing the path into hash-keyed
store methods. `UpdateClipTags` returns `ErrNotFound` and the tagger treats that as **fatal**, so
every run aborted on its first taggable clip. Playout's `RecordClipPlay` swallowed the same miss as
telemetry. `PATCH /v1/filler/{id}` 404'd from the UI. `PodEntry` had no `Hash` field at all — which
is *why* playout had only a path to pass.

⚠ **The reason it survived two releases is the durable half.** `clipAt`/`sampleClip` (store) and
`untaggedClip` (filler) all set hash and path to the SAME STRING, so a hash-keyed call and a
path-keyed one were indistinguishable to every test. **This is the identical blind spot that already
shipped the `DeleteClipsNotIn` catastrophe** — the lesson was written into a comment and the fixture
was never fixed. All three now derive one field from the other and are never equal; `tagMemStore`
mirrors the real store's split (hash for tags, path for held) instead of accepting either. Making
them honest surfaced **four tagger tests passing for the wrong reason**, including one whose comment
documented the bug as the design, and a V28 play-count test still pinning the pre-V38c contract.

Also #169: the **visual gate was asserting an error page** — all four `ChecklistItem` stories × both
viewports were ONE byte-identical 1160-byte PNG of the words "Not Found", because `/wizard` was
missing from `RouterHarness`'s `NAV_PATHS`. The error page satisfies every wait the spec does and is
perfectly stable, so it passed forever. `RouterHarness` now **throws** on an unregistered path.
And five endpoints loaded the whole catalog to produce an integer (`CountClips`,
`CountClipsBySource`, `AutoFiledOnly`; `Pool` went from one full-table read *per channel* to one).

**The cleanups.** `recurate` was a **second lineup writer** — it trimmed `ch.Lineup` and persisted
the channel moments before the binder's additive union ran over the same field, the two ordered
against each other by a code comment (#171). Retirements now ride the proposal (`Proposal.Retired`)
and the binder applies them through the one `schedule.ApplyLineup` primitive; `UpsertChannel` is
**gone from `CuratorStore`**, so the two-writer arrangement is unexpressible rather than merely
discouraged. Incoming and Sources own their own queries (#170, #172). `/v1/suggestions` →
**`/v1/proposals`** (#172), the glossary's own rule finally followed — with three deliberate
survivors recorded in `CONTEXT.md` (the persisted job kind `"suggest"`, the SSE phase frame, the
`FeatureSuggestions` capability: all the verb, never the artifact). Dead `schedule/backfill.go`
deleted, two indexed lookups replacing full-table scans (migration `00037`), and `conformance.go`
split 2,650 lines → an 89-line runner + five domain files, **verified by diffing the subtest names
the suite actually runs: 39 before, 40 after, zero dropped** (#173).

⚠ **Every fix was sabotage-verified** — reintroduce the bug, confirm the tests go red. That is what
caught the near-miss: the first fixture fix left the suite GREEN, which proved the tests had never
exercised the distinction at all. It also stopped a wrong deletion (the shell's `confirmEra` looks
redundant but serves the Catalog's tag-cycling) and it is how the ffmpeg helper's **total absence of
coverage** was found (#174): the two language backends carried byte-identical copies of one ffmpeg
invocation, and changing `-ar 16000` to `-ar 8000` failed **nothing**.

**CI now installs ffmpeg** (#175), measured rather than assumed — a probe reported `ffmpeg: MISSING`
with three tests reporting SKIP while the job stayed green. Cached debs: ~38s → **~14s** on a hit.
⚠ Cache apt's **delta** (87 debs / 61MB), never the `--recurse` closure (298 packages / **244MB** —
it includes base packages the runner already has, and would cost more of the 10GB budget than it
saves). `--no-download` makes a broken cache FAIL rather than silently re-fetch.

**Deliberately NOT done, and each argued rather than skipped:** `app.go`'s 1,187 lines and
`api.Server`'s 44 fields (§14.1 examined and kept both; `architecture_test.go` encodes it), the 75
in-body `requireAdmin` calls (defence-in-depth on an authorization boundary — the wrong direction to
tidy), and extracting the Catalog tab (its query feeds the shell's own badge and it is what renders
when no other tab does, so extraction buys no runtime saving and costs a ~10-prop interface).

**V40 — language detection, two backends behind one seam (2026-08-05, `b7ef76a`, PR #167).** The
detection §10 designed and the V40 quality-gate entry below records as *unbuilt*. A scheduled job
over a ~10s span (never an inline scan pass — `whisper-cli` is ~3s natively but **~341s under
QEMU**), with two interchangeable backends: local `whisper-cli`, or an audio-capable hosted model
through the §8.1 provider. ⚠ The silence guard is the load-bearing part: a 978s recorded ad break
whose first 10s measure −70 LUFS was answered `ar` and **tombstoned**. Asked what language silence
is in, a model does not decline — it guesses, and re-asked it answered `en`. Two fixes: `LanguageSpan`
samples long recordings from the MIDDLE (where we look), and `spanIsSilent` refuses to ask below the
floor (holds wherever we land). Parser pinned to REAL whisper output, not remembered field names.

**V40 — the quality gate: reject the broken, normalise the quiet (2026-08-03, PR #166).** Gate:
`make check` (0 lint, `-race`) + the settings suite green **with ffmpeg hidden from PATH** + the
filler/playout/api suites. Automatic by maintainer decision — no badges, no review step, no
operator choice.

Two rejects at the scan boundary, so nothing downstream has to know: **shorter than 10s**
(`filler.min_duration`) and **no audio stream**. The floor exists because `DurationMs > 0` was the
only guard, and a **2.9KB / 33-millisecond truncated download passed it** — then sat `filed` and
airable in the dev catalog. Loudness is normalised **at playout** to −23 LUFS
(`filler.target_lufs`), verified end-to-end on real clips: −26.8 → **−23.4**, and −32.6 → **−23.1**
(a 9.5 dB correction landing within 0.1 dB of target).

⚠ **Normalisation never rewrites the file.** The clip folder holds files a person put there;
in-place is destructive and unrepeatable — the original is unrecoverable, and a re-scan cannot tell
it already happened, so a second pass would normalise an already-normalised file. ⚠ **Filler
only**: `Airing.Source` is the discriminator (set for a clip under `FILLER_DIR`, empty for a
library title), because normalising a feature film to advert loudness flattens its dynamic range.
⚠ **Single-pass**, despite ffmpeg preferring two — two-pass reads the whole file before emitting a
frame, which is fatal for a live stream.

⚠ **It also fixed a CI failure that had been red since V38c.**
`TestFeatures_UnsetToolPathsFallBackToPathLookup` faked `execProbe` but left the real
`exec.LookPath` running, so it asserted the **host's** PATH: green on any machine with ffmpeg
installed, red on every runner without it. Reproduced locally by hiding ffmpeg from PATH.

⚠ **`Probed.HasAudio` was the wrong polarity, and the lesson generalises.** `false` meaning
"reject" retroactively changed every `Probed{...}` literal written before the field existed — nine
test doubles that were correct when written began rejecting every clip, and the suite panicked on
an empty catalog. Renamed to `Silent` so the zero value is permissive. **A gate whose zero value
denies is a gate that breaks its own callers.**

**Deliberately not done: brightness.** Measured YAVG 64–127 against a mid-grey of 128, and the dim
end is what an eighties VHS transfer looks like — auto-brightening invents a picture the source
never had, and unlike loudness it is not recoverable at playout. **Language detection** is designed
in §10 but unbuilt: `whisper-cli` is vendored, but ~3s natively and **~341s under QEMU**, so it must
be a job over a ~10s span rather than an inline scan pass.

**V39 — hover previews and a clip player (2026-08-03, `6fd0147`, PR #164).** Animated WebP previews
generated **in the same ffmpeg pass as the still** (a `split` filter, one decode, ~0.47s/clip), a
`VideoPlayer` core primitive that knows nothing about clips, and a Radix Slider scrubber (§14)
replacing a hand-rolled `role="slider"`. ⚠ It surfaced that **`/v1/filler/media` had 404'd for every
clip since V38c** — the handler used `GetClip` (`WHERE hash = ?`) while its URL carries the sharded
*path*, and nothing noticed because no client called it until the player shipped.

**V38c — one pipeline in, content-addressed clips, path-based navigation (2026-08-02, `f058075`,
PR #162).** Gate: `make check` (0 lint, `-race`) + `test-pg` (both dialects) + `openapi-verify` +
`make fe` (biome + typecheck + **1076 tests**) + `fe-visual` (**706 passed, 0 axe**, on a CLEAN
verify run, not `--update-snapshots`) + `make e2e` (7). ⚠ ~~**Still open: V38c.8–9**~~ **UPDATED
2026-08-03 — V38c.8 shipped (`837d38b`, the seeded default sources) and loudness normalisation
shipped in V40.** Genuinely still open: **V38c.9** (the Add-a-source polish against the mock), plus
V38b's remaining carried list — the first-run starter pull, reel name/thumbnail, the Tune panel and
filmstrip. Amended rather than deleted for the reason the superseded "Next up" at the bottom of this
file records: a list of open items that outlives the work it names still reads as current.

Every source now takes **one route** into the catalog — arrive in the watch folder, hash, move
into the clip folder as `<hash>.<ext>`, sidecar, catalogue. Identity is a **sparse content hash**
(head 64KB + tail 64KB + size), the third identity change and the one that finally keys on
something Loomarr owns. Many watched folders made the path unusable: two folders each holding
`ads/coke.mp4` produced one identity and silently overwrote each other.

**Library sources are SCANNED again** (maintainer, reverses V35). §10 and §9.1 amended doc-first,
carrying the guardrails the reversal must not break — a library is one source among several and
never the catalog's only route, identity stays the hash rather than a media-server item id, clips
are copied into the clip folder rather than played in place, and a filler library is never offered
to the suggester as programme material.

⚠ **THREE BUGS THAT EVERY GREEN GATE MISSED, all found by running the real binary.** This is the
phase's most transferable lesson:

1. **`DeleteClipsNotIn` deleted the entire catalog on every sync.** It matched `WHERE path NOT IN
   (…)` while the sync passes HASHES; a hash never equals a path, so every clip counted as "not in
   the keep set". The route reported `1 added, 1 pruned` forever and held nothing — filler
   silently never worked. **The conformance suite passed throughout because its fixtures set
   `path` and `hash` to the same string.**
2. **A file dropped straight into `FILLER_DIR` was catalogued and then pruned in the same pass** —
   the documented drop-folder, and what every pre-V38c install does. `ClipPath`'s new allow-list
   accepts only hash paths, so a human-readable name was not a valid id. Every intake test missed
   it because they all dropped fixtures into the WATCH folder.
3. **`Sync` still keyed on `rc.Path`** after identity moved to `rc.ID`. Nothing caught it because
   `DirSource` fills both AND the in-memory double keyed on the path too — a double that models
   identity differently from the real thing tests the double.

⚠ **An unlayered CSS rule had disabled every accent border in the app.** `styles.css` set
`* { border-color: … }` outside any layer; Tailwind v4 sorts by LAYER before specificity, so it
beat `.border-signal` and every sibling across **26 files**. The active tab's amber underline
rendered grey. No gate could see it — the class was in the JSX, in the compiled CSS and on the
element, simply always losing — and **the visual baselines had been captured while it was broken,
so they encoded the grey as correct**. Found by diffing `getComputedStyle()` on the running app
against the same element in the rendered mock; ~330 regenerated baselines are that correction.

**Navigation is path-based app-wide** (maintainer): `/filler/sources`, `/queue/approval`,
`/channels/$id/<section>` join `/settings/<page>`; old `?tab=`/`?section=` links redirect. ⚠ Only
the TAB became a path segment — Filler's `q`/`kind`/`audience`/`view` stay in the query, because
they narrow the clip grid and nothing on the other tabs is a clip. `NavTabs` replaced `CountTabs`
and `ChannelNav`; tabs are real `<Link>`s with `aria-current`, **not** `role="tab"`, because these
navigate and the tab role would promise arrow-key behaviour navigation does not have.

**`fe-visual` earned its place twice.** It caught two real a11y bugs in components written this
phase — an invalid `aria-controls` at CRITICAL impact (copied from `CountTabs`, where tabs
genuinely controlled panels) and a 4.11:1 count pill (`signal` on a `signal` tint composites
amber-on-amber). ⚠ It also surfaced that the **e2e wizard baseline was long stale**, showing a
six-step flow with *Webhooks* and *Library* — both retired phases ago. The border diff only made a
dead snapshot fail loudly.

⚠ **Two deliberate departures from the mock**, recorded in the code so the next diff does not
"correct" them back: the switched-off source row keeps its text readable (the mock's
`opacity:.45` measures **2.95:1** against a required 4.5:1) and its `off` stat is not dimmed to
`#5A6170` (**3.14:1**). The mock is authoritative for look; an axe failure is a hard gate.

**A process note worth keeping:** `docker run -e CI` passes NOTHING when `CI` is unset on the
host, so `playwright.config.ts`'s `workers: cpus().length` never applies locally and the visual
suite runs at half the cores. Unfixed here deliberately — a one-line Makefile change that deserves
its own commit and a single timed run.

**V38b — clips arrive from SOURCES, not from a URL box (2026-08-02).** Registered sources are now
polled on a schedule and new clips download unattended, bounded by four configurable limits. The
paste-a-URL panel is **deleted**. ⚠ **Still open: the first-run starter pull, loudness
normalisation, reel name/thumbnail, the Tune panel and filmstrip.**

**This phase started from a user question, not a plan.** The Incoming tab said *"Everything
downloaded so far has been filed"* directly above a panel saying *"Downloading isn't available in
this install"* — two claims that cannot both be true. Pulling that thread found a real defect
underneath.

⚠ **ONE `ingest` flag gated TWO downloaders with different needs.** archive.org is fetched over
plain HTTP and needs only ffmpeg; yt-dlp is for YouTube. Requiring both meant a box with ffmpeg
and no yt-dlp reported "downloading unavailable" **while being perfectly able to fetch** — and the
starter pull is an archive.org collection, so first-run acquisition was blocked by a binary it
never invokes. Same shape as V37's `Fetchable()`: an invariant true when written ("every ingest
needs yt-dlp") that quietly stopped holding when a second downloader landed beside it.

⚠ **The test defended the bug.** `TestFeatures_IngestNeedsBothToolsPresent` asserted *both*
directions. One is a real dependency and is documented — yt-dlp shells out to ffmpeg to merge
streams. The mirror case had **no comment and no argument**: it was asserted by symmetry. Now
split, and sabotage-verified — re-coupling them fails naming the defect.

**Also found while checking:** §15 has always said the tool paths "default to the vendored
binaries"; the registry defaults were `""` and only the Docker image set them, so **every source
build had ingest off even with the tools installed**. Unset now falls back to a PATH lookup, which
makes the documented behaviour true.

**Auto-fetch, and what bounds it (maintainer, 2026-08-02).** §15's *"there is no unattended
crawler"* is **superseded**: a registered, enabled source is polled and new items download without
anyone asking. ⚠ **The superseded rule's concern was legitimate, so it survives as LIMITS rather
than as a prohibition** — `filler.fetch.every` (6h, `0` = off), `max_per_run` (10),
`max_catalog_clips` (2000), `max_disk_gb` (20). Every one fails toward doing less.

**Three properties, each sabotage-verified:** only **enabled** sources are polled (the Sources
switch already promises it); everything fetched arrives **held**, so the unattended step is
*acquisition* and never *admission*; and a limit that stops a pass is **reported**, because a
crawler that quietly does nothing is indistinguishable from one that is broken.

⚠ **Dedupe has no cursor, deliberately.** archive.org discovery is a search, not a feed, so "new"
can only mean "not already here" — the catalog is the high-water mark. It compares **basenames
without extension**, because a downloaded clip lands as `<id>.mp4` and comparing raw strings
against bare archive ids would match nothing and re-download everything on every pass. And the
catalog read must include **held** clips or the job re-downloads its own review queue forever.

**The URL box is retired**, not moved. Clips arrive because you added a source — registration,
per-row search, an approved pull, or auto-fetch — and a box that made you find the URL yourself
was the odd one out. Its two end-to-end tests were **deleted rather than relocated**: the
behaviour is gone, and a test kept alive against a deleted surface passes by rendering something
nobody can reach. `check-retired.sh` now guards the name.

⚠ **A bug I introduced and the maintainer's question surfaced.** Asked whether the Sources panel
was being redesigned too (it was not — V37 did that), checking found the fetcher polled sources
and **never stamped them**: `MarkFillerSourceFetched` shipped in V33, is in the store interface,
and had **no production caller**. Every row would have read *"never fetched"* forever while
auto-fetch downloaded from it every six hours. Now stamped — ⚠ only after a successful queue AND
only when something was actually queued, because "last fetched" must mean "last brought something
in" or a source polled fruitlessly for a week reads as freshly productive. Sabotage-verified.

**Two guards that fired usefully:** `Resolve` **panics** on an undeclared `ScheduleKey`, so
registering a job without its settings row takes the app down at boot — fail-fast, caught by
`make check`. And `TestJobSet` pins the whole job roster, so **the first job that reaches out to
the internet unattended could not be added quietly**; its row is now the record that §15's old
claim no longer holds.

Gate: `make check` GREEN (`-race`) · `make fe` GREEN (1050 tests) · **`fe-visual` 690 passed, 4
modified baselines** (the corrected empty state), verified without `--update-snapshots` ·
`make e2e` 7/7 · `retired-verify` clean (6 identifiers) · `config-docs` regenerated.

**V38 — the confidence spine (2026-08-02).** Clips gain a **lifecycle**: an ingested clip is
**held** — recorded but not in the playable catalog, not matched into a pod, not counted as
coverage — until it is filed, by a human or by clearing a confidence threshold. Incoming reads
held clips, files them (singly or **all-as-suggested**), and shows **what was filed without
asking** with a one-click undo. ⚠ **Still open: loudness normalisation, reel name/thumbnail, the
Tune panel and filmstrip.**

**Maintainer calls:** grounding-gated score with the model refining **within the cap**;
auto-filing **ON at 85**; and — after I found the shipped pipeline had no filing step at all —
**build the real hold-then-file pipeline** rather than repurposing a heuristic.

⚠ **I mis-scoped this twice, and both corrections matter more than the code.** First I planned to
gate "filing" without checking one existed: the scan catalogued a clip and the tagger tagged it
**in place**, so nothing was ever held. Then I warned that auto-filing ON meant clips reaching
channels unreviewed — **backwards**: before V38 an ingested clip was catalogued and playable
*immediately*, no score, no gate. Auto-filing at 85 is the **first** thing ever to stand between a
download and a channel. §10 records both.

**Six safety properties, each sabotage-verified:**

1. ⚠ **An ungrounded era cannot be auto-filed at ANY settable threshold.** Grounding facts set a
   **ceiling**; the model may only lower it. Verified twice — as arithmetic, and end-to-end
   through the filing decision, because **a correct cap that nothing consults protects nothing**.
2. ⚠ **Held clips never reach a pod** — excluded at the one `ListClips` chokepoint, the shape
   V35's tombstone established, because pod assembly reads it with a zero filter.
3. ⚠ **A re-scan cannot file a held clip** — omitted from `UpsertClip`'s `DO UPDATE` *and*
   preserved in the sync merge. One scan pass would otherwise empty the queue into live channels.
4. ⚠ **The policy fails CLOSED** — `boolv`, not `boolOn`. They differ only when settings cannot
   answer, and there `boolOn` would publish unreviewed clips exactly when the install is degraded.
5. ⚠ **Hand-filing CLEARS `auto_filed`** — a human looked, and leaving the marker set makes a
   reviewed clip indistinguishable from an unreviewed one.
6. ⚠ **Confirm-as-suggested carries all three tags** — `UpdateClipTags` writes era, audience and
   category unconditionally, so an era-only write blanks the other two. Second call site to hit
   this; the first is commented in `filler-page.tsx`, which is now a strong argument that the
   store method's all-columns behaviour is the real smell.

**The lifecycle fork needed a maintainer decision.** Ingest downloads into the *same folder the
scan watches*, so at catalogue time a fetched clip and a hand-copied one are both just files. The
`.info.json` sidecar is the signal: sidecar ⇒ Loomarr fetched it ⇒ hold; none ⇒ a person put it
there ⇒ file on sight. Holding a hand-copied clip is the ceremony §7 warns teaches people to click
through gates.

**Five defects the tooling caught — three of them mine:**

- ⚠ **A test that passed under sabotage.** `TestSync_ReScanDoesNotReHoldAFiledClip` went green with
  the preserve replaced by a recompute: `serverFieldsUnchanged` skips an unchanged clip *before any
  write*, so the second sync never exercised the merge. It now forces a write. **The protection was
  real but came from the idempotent skip, not the line the test claimed to cover** — false
  assurance that would have shipped.
- ⚠ **A comment that disagreed with its own code** — I wrote that `confidence` was omitted from
  `DO UPDATE` while the SQL still updated it. Only executing it found that.
- ⚠ **`boolToInt` into a Postgres BOOLEAN — the third dialect trap this session.** `ai_tagged` uses
  that helper safely because its column is INTEGER in *both*. **The split is per COLUMN.**
- ⚠ **`filler.autofile.normalize_loudness` shipped declared-but-unconsumed** and I caught it in the
  final gate. `make config-docs` published a key nothing reads — the exact defect that got these
  keys removed in V35's review, **re-committed inside the phase that documents the lesson**.
  Removed; it lands with its ffmpeg pass.
- ⚠ **Every filler duration in Incoming rendered "0m"** — `formatDuration` is minute-granular
  (programme runtimes); clips are 5–60 seconds. `formatClipDuration` already existed and is what
  the card and row use. No test asserted on it, and "0m" is a plausible-looking string; only the
  baseline image showed it.

⚠ **And one thing I got wrong twice about the confidence bar**: I read three scores as rendering
identically and "fixed" it twice before measuring the pixels — 17px, 30px, 38px for 40/72/92. It
was correct all along; a 40×1px bar simply reads as a band. What the measurement *did* find is
that a native `<meter>` cannot be themed (it drew the browser's green, not `lock`), which is why
it is a `role="meter"` div. **Reading an image is evidence; guessing from one is not.**

Also: `ListUntaggedCommercials` needs `IncludeHeld` — held clips are precisely the ones needing
tags, and without it the tagger skips every fresh download and the threshold appears inert. And
the reachability stub had **no `/v1/filler/incoming` branch**, so the tab tested the catch-all —
the second time that stub has answered the wrong shape (V35b found the first).

Gate: `make check` GREEN (`-race`) · `test-pg` GREEN (00030/00031 both dialects) · `make fe` GREEN
(**1053** tests) · **`fe-visual` 690 passed, 6 new baselines**, verified without
`--update-snapshots` · `make e2e` 7/7 · `config-docs` regenerated · `openapi-verify` clean once
committed.

**V37 — sources as one flat list, and YouTube becomes real (2026-08-02).** Every source is one
row in one list: the drop-folder, the media-server library, each archive collection, each YouTube
playlist. The `remote` CONTAINER row is retired, `POST /v1/filler/sources` takes a **kind**, and a
registered source carries its own switch, fetch time and Remove.

⚠ **This SUPERSEDES a rule that was load-bearing, so the doc records what survives it.** §10 said
the folder is derived-from-config and remotes are rows, *"and that asymmetry is deliberate and is
why one source never appears twice."* Flattening does not dissolve that concern — it moves it, so
§10's new *"Sources are one flat list"* names the two properties the flat model must carry itself:
**"not configured" stays expressible** (the config kinds are rows even when unset — §10's own
answer to *"why is my catalog empty?"*, which a table of things-that-exist cannot give), and **no
source appears twice** (those kinds are singletons). Doc-first, before any code.

**Five findings, three of them defects the tooling caught:**

1. ⚠ **A pull would have enqueued a fetch of an empty URL.** Both pull paths filtered on
   `Enabled` alone, which was correct while the table held *only* fetchable remotes — my seeded
   singletons are enabled, so a plan gained a folder row whose `uri` is `""`, and approval handed
   `Ingest` a blank string. **The invariant "every row here is fetchable" was implicit and became
   false the moment the table flattened.** Now explicit as `FillerSource.Fetchable()`, used by
   propose *and* the approve-time re-check so the two cannot disagree.
2. ⚠ **`enabled` is BOOLEAN on Postgres and INTEGER on SQLite** — a literal `1` copied across is
   a hard 42804 at migrate time. Invisible reading the two files side by side; `test-pg` caught
   it. The good failure mode: it cannot reach an install half-applied.
3. ⚠ **The singleton guard lives in the DATABASE, not in Go.** A partial unique index on
   `kind IN ('folder','library')`. Sabotage-verified — dropping `UNIQUE` fails the conformance
   suite with *"a SECOND folder row was accepted"*. A Go-only guard is one the next caller
   forgets, and the failure mode is two drop-folder rows with different switches: exactly the
   precedence question the superseded model refused to have.
4. ⚠ **Registration is deliberately STRICTER than ingest.** `clipfetch.KindForURL` defaults an
   unknown host to YouTube because yt-dlp handles the widest set of sites — right for an admin
   pasting one URL and watching it work, wrong for a row that persists and fetches unattended.
   The host check is **exact**, not `Contains`: sabotage proved a substring check registers both
   `youtube.com.evil.test` and `notyoutube.com`.
5. ⚠ **The on/off switch had never been in a visual baseline.** No story passed
   `onToggleEnabled`, so the tab's most consequential control — whose copy is a behaviour claim —
   was unrendered in every snapshot. Pre-existing, not a V37 regression; closed by the new
   `WithRegisteredSources` story, and found by *looking at the picture*.

Also: the row id is namespaced (`archive:…` / `youtube:…`) because one table now holds two
vocabularies and a bare identifier lets one kind silently UPSERT over another's; `searchable` is
**server-sent** rather than re-derived client-side (V35b hard-coded `kind === "remote"` and
predicted this); and the page's Add-a-source form closes a route that had been **API-only for two
phases** — `POST /v1/filler/sources` shipped in V35 with nothing calling it.

**Deferred, deliberately: community packs.** There is no pack index — no URL, no manifest, no
fetcher — so a `PACKS` row would be the "control that dims and changes nothing" §10 forbids two
paragraphs above. **Licence stays off the wire** (maintainer, 2026-08-02): §6.3 measured ~92% of
archive items declaring none, and a badge absent nine rows in ten teaches an operator to read
absence as "fine" rather than "unknown".

Gate: `make check` GREEN (`-race`) · `test-pg` GREEN (00029 conformant on **both** dialects) ·
`make fe` GREEN (1049 tests) · **`fe-visual` 686 passed, 2 new baselines and 10 modified**,
verified by re-running **without** `--update-snapshots` · `make e2e` 7/7 · `config-docs` no drift ·
`openapi-verify` clean once committed (spec verified stable across regenerations).

⚠ **`retired-verify` fails on an UNTRACKED local `docker/.env`** holding a generated
`WEBHOOK_SECRET` from an old dev run. Not V37 and not something CI sees — the check greps the
working tree, not the index. Verified clean with that file moved aside (5 identifiers checked).

**V35b — the honest frontend delta (2026-08-02).** The part of the redesigned Filler page that
needed no backend: the catalog's **list ⇄ grid** toggle (`?view=list`, new `ClipRow`), the card's
**thumbnail overlays** (duration / quality / select move onto the frame), **Select all**, and the
per-source search **re-scoped into the archive row** behind a *Search it* toggle — finding clips is
something you do TO a source, so it no longer sits in a detached card.

**Four things the tooling caught that no assertion would have:**

1. ⚠ **`/v1/filler/sources` was never stubbed in the reachability suite** — it fell through to the
   clips payload, so the Sources tab rendered **zero source rows** in every run, and the old
   *"Find clips"* assertion passed anyway because that heading sat OUTSIDE the list. Moving the
   search INTO the row is what made the test depend on the shape. **A stub answering the wrong
   shape is indistinguishable from a working page until something depends on it.**
2. ⚠ **The list row crushed the clip name.** A grid item's default `min-width:auto` refuses to
   shrink below its content, so a long name pushed the tag columns out instead of ellipsising —
   the one column that must never be squeezed. Every test passed; **only reading the baseline
   image showed it.** Fixed with `min-w-0` and pinned by a `LongName` story.
3. ⚠ **`aria-label` on a bare `<span>` is ignored.** The quality overlay carried one to stop
   "1080P" being announced as letter-spaced shouting; ARIA only permits naming on an element with
   a role, and the chip-row version got its role from `Badge`. Biome caught it — a real a11y bug,
   not a lint nit. `role="img"` now carries the name.
4. ⚠ **A `widthFrame(360)` story does NOT test a mobile layout.** Tailwind's `md:`/`lg:` resolve
   against the VIEWPORT, so the decorator narrows the container and changes which columns render
   not at all — the story drew a squashed desktop row that *looked* like proof. Deleted (with its
   two orphaned baselines, which `--update-snapshots` writes but never prunes); the visual suite's
   mobile viewport is what actually exercises the collapse.

⚠ **The mock's `searchable: id === 'archive'` could not be followed literally** — archive.org has
no peer row today; it lives nested under `remote`. The expander is a **render prop keyed by row**,
so V37's flattening changes which row matches and not this shape.

**Not adopted from the mock, deliberately:** list rows carry **no per-row actions**. List view is
for scanning and bulk-selecting; acting on one clip is what the card is for, and rebuilding it
badly in 46px of column would be worse than the split the mock draws.

Gate: `make fe` GREEN (**1047** tests, up from 1039) · `fe-visual` **684 passed, 14 new baselines
and 2 modified** (both the intended ClipCard overlay change), verified by re-running **without**
`--update-snapshots` so the axe half ran · `make check` GREEN.

**The maintainer export landed, and it split the Filler track (2026-08-02).**
`design/loomarr-prototype-desktop-v2.dc.html` is now the **632,110-byte** export — the one the
V35 row below says was needed. It carries the redesigned Filler screen *and* the ~270 KB of JS
every previous fetch truncated. **Next up: V35b, then V37 and V38** (plan §6.5).

⚠ **Reading the markup alone had understated one screen.** The confidence meter looked like
layout; the JS shows `conf >= autoConf` is the **routing decision** for every incoming segment —
what gets filed silently vs. surfaced to a human. So the Incoming redesign is **not frontend
work**: the score is a backend capability the UI only reports. It became its own phase (V38)
rather than a step inside a page phase.

⚠ **Two verification lessons, both about the truncated fetch rather than the design.** (1)
`support.js` was recorded as byte-identical in three places; it was **not** (64,222 → 69,150
bytes, 155 lines). The check was real but ran against the capped read — **a hash taken from a
truncated fetch describes the cap, not the file.** (2) The "do not invent `poolStats`/`asks`/
`reels`/`services`" instruction was correct while the JS was unreachable and is now **retired**,
because those values are readable at source. Both corrected in `design/README.md`,
`design/FILLER-DELTA-2026-08-01.md` and plan §6.5.

⚠ **1.7 was recorded as NOT DONE and that was wrong** — see the corrected row below. The work sat
uncommitted in the tree, so the record and the code disagreed in the direction that costs most:
the plan would have re-specified a built feature.

**Three ratified decisions (maintainer, 2026-08-02)** now in plan §6.5: **follow the mock's
design**; **YouTube + community packs become real registrable sources** (V37 — a schema and
contract change, not a restyle); and **the tagger records a confidence score** (V38 — the
dependency the whole Incoming redesign rests on).

**V35 — the Filler page as redesigned (2026-08-01).**
Branch `feat/filler-clip-card-detail`, 14 commits. The design project's prototype was re-fetched
and the Filler screen had been **rewritten** — 4 tabs → 3, Discover deleted into Sources, a new
Incoming tab, and filler acquisition behind an **approval gate**. Plan §6.5 specs it; the
structure is in `design/FILLER-DELTA-2026-08-01.md`.

⚠ **It invalidated an audit written the same day** (the "Filler UI is behind its own backend"
block below): **five items retired, three reshaped, two survive**. That block is now annotated
rather than deleted, because the lesson is the durable part — an audit is a claim about a moving
reference, and this one had a shelf life of one day.

~~⚠ **`design/loomarr-prototype-desktop-v2.dc.html` is STALE for this screen and could not be
updated.**~~ ✅ **RESOLVED 2026-08-02 — the export landed** (see the top entry). The cap that
caused it was real and is worth keeping: `DesignSync.get_file` stops at 262,144 bytes, the file is
~310 KB of markup before its JS, and a markup-only splice renders nothing. ⚠ The last sentence of
this block — `support.js` "byte-identical, so no runtime change accompanies it" — was **wrong**,
because it was verified against the truncated fetch.

**Shipped (backend + contract):**

| | |
| --- | --- |
| **1.1 Pool health** | `GET /v1/filler/pool`. ⚠ Per-channel numbers **ARE** `Coverage`, called once per live channel — no aggregate ladder. Sabotage: giving it its own opinion fails on all 3 channels |
| **1.2 Clip media** | `GET /v1/filler/media/{path...}`. Two gates the mock cannot show: a **catalog** check (the drop-folder is not a public share) and an **extension allowlist** (serving `mime.TypeByExtension` from an operator-writable folder is stored XSS from our own origin) |
| **1.3 Source switch** | Migration `00026` + `filler.source.folder.enabled`. Gate lives in the **Syncer**, not its three callers; `ErrSourceDisabled` → a 409 naming the switch |
| **1.5 Pull proposals** | Migration `00027`. **The approval gate for filler acquisition** — §10 stated "the machine proposes, a human commits" with nothing to hang it on; a pull is that object |
| **Step 2** | `make openapi` + orval regen; the typecheck caught every hand-built `FillerSourceDTO` fixture, which is the contract-1:1 principle paying out |

⚠ **Three findings that changed the design rather than the code.** (1) §15 already specified
`FILLER_SOURCE_LIBRARY_ENABLED`; implementing it found **there is no scan to gate** — §10 took the
media server out of the filler path — so the key was removed doc-first rather than shipped as a
control that dims a row and changes nothing. (2) The first draft of `00026` put `enabled` in the
upsert's `DO UPDATE` list: a Go bool zero-values to **false**, so any existing caller re-registering
a source would have silently switched it **off**. (3) `resolved.boolv` answers false when settings
cannot answer, which would have stopped the catalog scan on a degraded boot — `boolOn` fails **open**
for keys whose declared default is true.

**Every safety property is sabotage-verified**, not assumed: proposing that downloads, a
double-approve, an aggregate with its own ladder, a gate placed below the scan, an upsert that
flips the switch, and an allowlist replaced by a mime lookup each fail a named test.

**Also shipped (frontend + the two remaining backend items):**

| | |
| --- | --- |
| **1.4 Incoming** | `GET /v1/filler/incoming` + the tab. ⚠ **No confidence score** — the mock draws a bar per row; the tagger records neither a score nor a rationale, so each row carries the REASON it is waiting, derived from real state. `filler.autofile.*` is the knob that would need one and is not built |
| **1.6 Bulk ops** | The blocked decision, answered: **a tombstone**. Migration `00028`. Not a row delete (`clips` is a synced cache, so the next scan puts it back) and not a file delete (nothing here deletes an operator's media). The UI says "Remove from catalog" and the files stay |
| **Step 3** | Three tabs under the pool strip; Discover **retired**; bulk selection; the `FILLER PULL` card on the Queue; source switches |

⚠ **Discover is gone as a tab**, and its query state was deleted rather than left wired to
nothing. The ingest panel moved to Incoming, the tab about how clips *arrive*. An old
`?tab=discover` bookmark falls back to Catalog.

⚠ **The reachability suite earned its keep again**: retiring a tab broke four assertions pointing
at `?tab=discover` — exactly the "a tab nobody can navigate to passes as reachable" failure it
exists for. It now also asserts the health strip is present, since a strip has no nav entry.

**Three defects the tooling caught, each worth the note:**

1. `pluralize()` already emits the count, so the first draft printed numbers twice ("of 90 90
   commercials") — and I repeated it in a second spot before noticing.
2. `onConfirmEra` sent a bare `{era}`. The comment **directly above the mutation** warns that
   `UpdateClipTags` writes all three tag columns unconditionally, so that wipes audience and
   category.
3. Two comboboxes named "Audience" on one page (the filter bar already owned the name) — a real
   ambiguity for anyone on a keyboard, surfaced as "found multiple elements". They are "Set
   era/audience/category" now, which also separates the two jobs.

Plus an a11y finding: `role="switch"` **replaces** the native checkbox semantics, so `checked`
alone leaves the control announcing no state; `aria-checked` is explicit.

**NOT DONE, and deliberately so:**

- ~~**1.7 per-channel include-set override + `fitNote`**~~ **DONE — this row was stale, not
  accurate (corrected 2026-08-02).** `components/loomarr/filler/channel-override-picker/`
  implements exactly the mock's shape: one checkbox per channel, a `fitNote` per row, and **Back
  to automatic** as the route to the third state. It is reachable through the rewritten
  `pin-clip-dialog` (no longer pin-only — it can exclude and un-pin), which `filler-page` renders,
  and `reachability.test.tsx` asserts the entry point. ⚠ It sat **uncommitted** while the record
  said NOT DONE, which is the direction of drift that costs most: the next phase would have
  re-specified a feature that was already built. Commit `8af15e4` carries the backend half
  (`GET /v1/filler/fit`, `ChannelFitDTO` with a reason **code** the frontend words).
- ~~**The card's cycle → "Edit tags" swap.**~~ **DECIDED: not doing it** (maintainer,
  2026-08-01). Click-to-cycle stays. It is faster for the common case (one wrong tag on one
  clip), and the select editor's real advantage — several fields across several clips — is what
  V35's **bulk bar** now covers from a selection, which a per-card editor never could. Recorded
  in `design/FILLER-DELTA-2026-08-01.md` so nobody "fixes" the code back to the drawing. ⚠ The
  general rule: **a mock is authoritative for what a screen SHOWS, not for deleting a working
  interaction it happens not to draw.**
- **Sources: per-source inline search and "Add a source" UI.** The routes exist
  (`POST` / `DELETE /v1/filler/sources`, `GET /v1/filler/discover`); nothing on the tab calls
  them, so all three are **API-only** for now. ⚠ `DiscoverPanel` was **deleted** rather than
  left orphaned — the redesigned per-source search is a different shape (a result row carries
  `date · duration · quality` and a *Queue download*), so it was not reusable.
- **The pull composer is a stub, and says so.** One plan row per enabled source, a constant
  `why`, `estimateClips: 0`, and the operator's `note` is **stored but never reaches `Ingest`**.
  A real forecast needs an upstream search per row at propose time. The gate around it is
  complete; what it proposes is not yet clever.
- **`filler.starter_collection` is declared and read by nothing** — the declared-but-unconsumed
  defect V12's row says blocked that phase. It predates V35 (`03116f0`) but is now §10's
  business, since §10 says it seeds a pull.
- **Live verification** on the maintainer's stack.

### Review findings, fixed (2026-08-01)

`/code-review` over the branch, two axes in parallel. **Every safety property verified good** —
no second ladder, a pending pull writes only, the tombstone holds end to end, no test neutered.
Six real defects, all fixed here:

1. ⚠ **A dead control, found INDEPENDENTLY BY BOTH AXES** — the empty catalog's "Find clips"
   navigated to `tab: "discover"`, a tab this phase retired. `validateSearch` drops the unknown
   value, so the button landed back on the empty catalog it was offered from. **Deleting a
   destination is not done until every route TO it is gone, and a nav target is not
   type-checked.** It is now *Propose a pull*, calling the same mutation the strip does.
2. **§15 declared `FILLER_AUTOFILE_MIN_CONFIDENCE` / `_DROP_BELOW`; neither is in the registry** —
   against §15's own rule that a setting not in the registry does not exist, and against §10 four
   paragraphs earlier saying autofile is not built. Removed from the doc.
3. **§10 over-claimed the source switch** ("not scanned, not searched, and not downloaded from").
   Only the scan and the pull path are gated, and the other two routes are *not source-scoped* —
   keyword discovery searches the Archive globally, and ingest takes a URL the operator typed.
   Both are the ratified single-item-direct path. The claim is narrowed rather than the routes
   gated.
4. **Migration `00026`'s comment named `filler.source.library.enabled`**, a key dropped later in
   the same phase once implementing it found no scan to gate — a permanent contradiction in a
   forward-only file. Corrected in both dialects (comment only; no schema change).
5. **`DiscoverPanel` was orphaned** — barrel-exported, storied, baselined, rendered nowhere: the
   exact "built and imported by nothing" state the reachability suite exists to catch. Deleted
   with its 10 baselines.
6. **Three stories hand-rolled DTOs**, against `frontend-design.md` §5.1b. Moved to
   `@loomarr/fixtures`; the migration was content-preserving, which the visual suite proved by
   moving **zero** of their baselines.

Plus a defensive nil-store guard in `fillermedia.go`. ⚠ Not a live panic — `liveConfig` is nil
exactly when the store is, so the handler already 404s — but that coupling was true by
construction and stated nowhere, and every sibling guards the store directly.

⚠ **One reviewer finding was MISATTRIBUTED and is recorded as such**: the "scope creep" of the
starter pack and the `filler.dir` default flip both come from `03116f0`, which predates V35. The
Spec axis reviewed `main...HEAD`, which includes six earlier commits on this branch.

Gate at `8b87703`: `make check` GREEN (`-race`) · `test-pg` GREEN (`00026`–`00028` conformant on
both backends) · `make fe` GREEN (1018 tests) · **`fe-visual` 658 passed, 26 NEW baselines and
zero modified**, verified by re-running *without* `--update-snapshots` · `make e2e` 7/7 ·
`openapi-verify` / `config-docs` / `retired-verify` no drift.

⚠ **A pre-existing flake, seen once:** `TestGuide_GapsArePreservedNotFiltered` failed with a 1ms
timeline hole (`block 1 starts at …240 but 0 stopped at …239`) and passed on five consecutive
re-runs. Unrelated to this phase — no guide code changed — but recorded so the next person to see
it knows it is not new.

**V12 — System → Backup + About (2026-07-29).** The two sub-tabs V9 deliberately left out,
plus the scheduled backup job behind them. The phase could not start until a doc conflict was
resolved: the gate said *"retention honored"* while `design.md:1397` deferred scheduled
backups to §20 and recorded `backup.schedule`/`backup.retain` as declared-but-unconsumed —
which they had been since V4, read by nothing. Shipping the page over dead settings would have
made its own footer ("nightly at 03:30 · keeps 7") a false claim, so the keys are consumed and
§16/§18.1/§7/§20 were amended first. **Three things that are safety properties, not features:**
retention filters on the writer's own filename pattern (`backup.dir` may hold the operator's
files — without the filter the prune takes their photos *and* the live database); prune runs
*after* a successful write (pruning first leaves fewer backups than it started with when the
snapshot fails); and the download's client-supplied filename is validated before it reaches the
filesystem (without it, `../loomarr.db` serves the live database). Each verified by sabotage,
the traversal one against the real service rather than a fake. Full row below.

**V31/V32 — the Dashboard's last two panels (2026-07-29).** Services (what is broken) and
Recent activity (what happened). Two maintainer questions shaped both. First: where do feed
rows come from? Answer — each domain **transition**, not the event bus, which `bus.go` itself
documents as lossy ("a dropped event is a latency bug"); a feed built there loses rows exactly
when the install is busiest. Second: why does Services **poll** rather than use SSE? Answer —
a probe result is not an event anyone observes (the server only learns it by making six
outbound calls, **730ms measured**), so pushing it would mean probing forever on an idle box.
But asking flipped the *other* panel: the feed IS event-shaped, so it now takes an `activity`
frame and polls not at all. ⚠ A wrapped "14m ago" made rows uneven and **every test stayed
green** — caught only by reading the baseline image. Full row below.

**V13 — restart in place, and Windows decided the mechanism (2026-07-29).** Loomarr restarts
by rebuilding itself in the same process: `for { app := Build(); app.Run(); app.Shutdown() }`.
Same PID, no supervisor, **identical on Windows** — which is the whole reason it is not
`syscall.Exec`. That was the settled choice until the maintainer asked how Windows would work:
`exec_windows.go` returns `EWINDOWS`, so it **compiles cleanly and fails only at runtime**, and
the button would have shipped broken. Jellyfin takes the same in-process approach for the same
reason. ⚠ The live test then caught a package-level `startedAt` **I had added in V12** that
survived the rebuild — About would have claimed days of uptime on an instance restarted seconds
ago, silently. Three maintainer corrections each *removed* something: Reload (settings already
hot-apply on save), the five-row consequence list (one line now), and full interactivity during
a restart (the app dims and blocks input app-wide). Full row below.

✅ **CI is running again** (2026-07-29). The GitHub Actions billing block recorded against
Phases 4–5 and several v2 rows below is **resolved** — runs on `main` are green, so a PR goes
through real CI rather than the merge-on-green-local exception those older rows describe.
Treat every "billing-blocked" note below as history, not as the current state.

**Three follow-ups the same day, two of them maintainer corrections to my judgment.** (1) I
argued the backup job should stay *unregistered* on Postgres since three other surfaces
explain `pg_dump`; the maintainer overruled it, correctly — **an omitted row is also a
claim**, indistinguishable on the Tasks page from a job that runs fine and has never failed.
`DisabledReason` now states it, enforced at four points in the scheduler rather than by UI
convention. (2) I shipped About *without* the mock's Go-runtime/uptime/schema rows because
the endpoint could not fill them; the maintainer asked for the fields instead, so
`/v1/system/version` now carries them — ⚠ `startedAt` as an **instant**, never a
pre-computed uptime. (3) Chasing "why don't I see this at `:5173`" found the backend was an
**orphaned `go run` binary serving pre-change code for hours**, and that `.air.toml` had been
committed since July with Air never installed *and* pointed at a database that does not
exist. All three rows below.

**V14 remainder + the Guide's 17× latency fix (2026-07-26).** Branch
`feat/guide-fold-and-dev-login`, 11 commits. Two threads: finish the IA fold V14 left open,
then make the resulting surface fast enough to use.

**The fold (V14's remaining gate, C8 answered).** `/channels` and `/suggest` are DELETED;
`/guide` is the channels surface, headed "Channels", owning origination via `✦ Add a channel`
in its header. Admin nav 9 → 7, matching the v2 mock's `navDefs` exactly; member nav 4 → 3
(`Request a channel` was a nav entry only while `/suggest` was its own page — a second link to
`/guide` would be a duplicate React key, not an IA choice). Members get the header affordance,
labelled for them, since it is now the only origination door in the app.

This had been blocked for several phases on one stated reason, recorded in four places: "the
grid has no origination affordance yet". **The mock always had one** — its Guide screen is
headed "Channels" and carries the button. Nothing had ported it; the blocker was a reading
error, not a missing decision. §12 amended doc-first (the ⚠ note said "this line comes out with
it"). **C8** is now recorded as API-ONLY BY DECISION rather than left orphaned. Also fixed in
passing: `router.history.push("/channels")` after login — a raw string nothing type-checked,
which would have 404'd every sign-in post-fold.

**The Guide rebuilt against the RENDERED mock.** The first pass was built by reading
`.dc.html` source and inferring, which produced ten defects the maintainer found by looking at
it. Now the mock renders locally (`python -m http.server` over `design/`) so comparison is
repeatable. Two were real bugs, not styling: `border-l-signal` never rendered (Tailwind emits
`border-<color>` and `border-l-<color>` in a build-time-fixed order, so the all-sides utility
won regardless of `cn()` ordering — every accent was #2A2E37, code reading correctly and
rendering wrong), and clicking a block did nothing (a `<button>` with no handler). Plus: the
now-line was 1px amber running through amber blocks (now 2px `onair` red + time chip), the row
menu opened clipped and offered no Edit, the header had no toolbar, and a horizontal scrollbar
sat under a grid that had room (`min-w` was a hardcoded 880px, so zooming out could never make
it fit).

**Perf: 1910ms → 103ms click-to-rows, measured in the browser.** Five layers, each found by
measurement after the previous hypothesis was disproved:

| | click→rows | what it was |
| --- | --- | --- |
| start | 1910ms | |
| `5fcd031` | 610ms | N+1: availability re-resolved per layout pass (72 library calls/request) |
| `97523e9` | 421ms | channels resolved sequentially |
| `43fd680` | 218ms | `pickOrder` fully sorted n candidates at each of n positions to use one |
| `b939576` | **103ms** | the relaxation ladder re-ran the whole placement per step |

**Hypotheses disproved by measurement, recorded so nobody re-tries them:** Emby is slow (one
`/Items` call is 1.5–10ms; an 803-episode enumeration is 40ms); the connection pool is starved
(`MaxIdleConnsPerHost` 4 vs 64 — no difference, 29 concurrent calls in 4–7ms either way); JSON
decode of large payloads (629KB in 5ms); and `ComputeDesiredAt` is inherently expensive (45ms
for **200** channels). The warm split is now 4ms client + 87ms API + 12ms render — the client
is not the cost.

**A 17× REGRESSION, reverted (`eb55c7e`).** The "obvious" `pickOrder` fix — stop building the
tier-2 candidates the caller discards — took the guide from 250ms to 4.3s with every test
green. Those candidates are load-bearing: the caller skips them but only after `budget--`, so
they are what makes a hard pool exhaust `backtrackBudget` and fall back to greedy. The
wasteful-looking code was the circuit-breaker. A ⚠ comment records it with the measurement.

**New in §5/§7/§14/§15/§18.1, all doc-first:** `series_episodes` (migration `00018`, both
dialects) with the `series-episode-refresh` job — deliberately NOT a `library-scan` hook, which
only correlates in-flight acquisitions and would never revisit an already-`available` show;
`@tanstack/react-virtual` for row windowing (200 channels → 19 rows / 793 nodes, verified);
`/debug/pprof/*` behind `LOOMARR_PPROF` (default off, not-registered-is-the-gate, boot WARN);
and `POST /v1/auth/dev-login` behind `LOOMARR_DEV_LOGIN` (same posture; excluded from
`api/openapi.yaml` on purpose — a dev bypass is not part of the product contract).

**Three defects that only sabotage-testing caught**, each a test that looked right and guarded
nothing: an expiry test written as `memoTTL + 1s` (so raising the TTL to 100h still passed); a
§19 member negative using `getByRole("tab", {name})` where CountTabs puts the count inside the
button, so the matcher matched nothing and passed whether or not the tab rendered; and a data
race in `fakeXMLTVGuide` that `-race` missed — my own concurrency change turned a sequential
interface concurrent, and every implementation including test doubles inherited a
thread-safety obligation. Also: `/debug/` was missing from `apiPrefixes`, so with pprof OFF the
profiler endpoint returned the SPA's index.html with a 200 (`go tool pprof` reports that as
"unrecognized profile format", which reads as a broken profiler rather than a disabled one).

Gate: `make check` GREEN (-race); `make test-pg` GREEN (migration `00018` conformant on both
backends); `make fe` GREEN (biome 663 files, typecheck, 766 tests); **fe-visual 504 passed, 0
flaky** (15 baselines regenerated — exactly GuideGrid + AppShell, verified by reading the diffs
and re-running WITHOUT `--update-snapshots`); **e2e 7/7**; `openapi-verify` + `config-docs` no
drift. Guide verified live in the browser throughout, per the maintainer's standing rule that a
curl timing is not what a user experiences.

**Still open:** click-to-rows is 103ms against a ≤50ms target — the remaining cost is the first
placement (~6ms/channel, genuine work) plus store reads and JSON, so further gains need a
different approach rather than more of this one. The V18 gate text says 375px while the
harness renders 390px, and V22's gate cites `design.md:940`, which has shifted; both need a
doc-first correction (found by `/register-check`, 2026-07-26).

**Curation-rule-engine arc — self-updating channels / re-curation (Phase B, 2026-07-24).** The
second half of the rotation/re-curation plan (`.claude/plans/curation-rotation-and-recuration.md`):
a channel built from an intent no longer freezes — an **opted-in** channel periodically
re-evaluates its intent against the current library and evolves its lineup, **preferring
in-library matches, weighting net-new acquisitions by quality + intent, and never bypassing the
approval gate** (programming-design §8.2). Composed from existing parts: **B0** extracted a
`ChannelBinder` (`internal/binder`) — the "materialize approved proposal → channel" logic — out of
the API behind an interface the composition root wires ONCE, so manual-approve AND every
auto-approve path bind identically (also **closed a latent gap**: a per-user auto-approved refine
enqueued acquisitions but never rebound its channel). **Per-channel opt-in** = `policy.autoCurate`
(rides `policy_json`, NO migration — like rules/filler/window) with optional MinScorePct/MaxTitles
overrides; **global knobs** `job.recurate.schedule` (weekly), `recurate.min_score_pct` (60),
`recurate.max_titles` (40). **`recurate.Curator`** = the channel-scoped auto-curate grant: filters a
re-curation proposal's net-new acquisitions to those clearing the quality bar within the cap
(in-library adds free), then approves through the ONE `suggest.Approve` gate (audit "auto-curate")
— **never a raw `wanted` write**, fails closed. **`recurate.Runner`** + the `channel-recurate` job
iterate eligible channels (live+intent-backed+auto-curate; skips paused/detached/hand-made/
not-opted-in) → trigger a refresh refine → the worker's new `ChannelAutoCurator` considers it.
Tests: quality bar, in-library-no-acquisition, **approval-gate negative** (not-opted-in never
requests), title cap, per-channel override, runner eligibility, + the B0 fix. **Live-verified** on
the homelab: opted the 1980s Action Heroes channel in, ran the job → refine → Curator approved
(audit "auto-curate") → bound; **enqueued=0** (library already complete — the correct idempotent,
conservative-spend outcome), **zero stray wanted titles** (gate honored). Doc-first §8.2 +
config-design + design §8. Gate: `make check` (`-race`) + `test-pg` + `openapi-verify` +
`config-docs` + FE `tsc` all GREEN. **The rotation/re-curation plan is now COMPLETE** (Phase A
rotation + Phase B re-curation both shipped). Merged on green-local.

**Curation-rule-engine arc — audience ceiling is a kids/teen guardrail (2026-07-24).** The
"1980s Action Heroes" channel came out capped at **TV-14**, silently dropping its 9 R-rated
films (Die Hard, Predator, Terminator…) — a small model reflexively caps genre channels, and
`groundPolicy` kept any proposed ceiling unconditionally. **Rule (maintainer):** the ceiling
exists ONLY so a channel a user asked to be *for kids/teens* can never show adult content; an
unqualified channel is **adult-default**. **Fix:** `groundPolicy(…, intent)` now keeps a
proposed ceiling ONLY when `intentSignalsKids` matches the intent text (kids/family/cartoons/
Bluey/Saturday-morning/teen/… across description/tone/era/refine/must-include); **no signal →
the ceiling is dropped** (everything admitted). Safety asymmetry absolute: dropping an
unjustified ceiling only *loosens*; when a kids signal IS present the ceiling stays + is
enforced fail-closed, and the raise-to-admit-picks (generalized from TV-G→TV-PG to the whole
kids band) **never crosses the kids→adult line** (`KidsCeilingRank`) — a stray R pick on a kids
channel is dropped by §4, never admitted. Prompt reinforced to omit the ceiling for non-kids
channels. Tests: no-kids→drop, 6 kids phrasings→keep, raise-never-crosses-kids-line, existing
raise/never-lower + grounding fuzz still green. Doc-first §4/§8. **Live-verified:** "90s action
heroes movies" → ceiling None; "cartoons for little kids" → TV-Y7. Gate: `make check` GREEN
(`-race`); `openapi-verify` + `config-docs` no drift. Merged on green-local.

**Curation-rule-engine arc — window rotation fix (2026-07-24).** A movie channel whose
library exceeds its window (the maintainer's "1980s Action Heroes", 15 films ≈30h, 24h window)
**repeated the same subset daily and never aired the tail**. Root cause: `truncateToWindow` kept
the deck HEAD (`slots[:kept]`); the window index advanced the shuffle SEED (order) but not the
slice OFFSET, so `sequential`/`syndication` channels (stable order) looped the same prefix.
**Fix:** `windowSlice` — a ROTATING ~window-of-runtime slice whose start advances with the window
index and WRAPS the catalog (window 1 continues where window 0 left off — TILES, not slides), so
over a full cycle every program airs (coverage invariant). Deck order is now seed-stable per
channel; rotation lives in the offset. Runs on the COLLAPSED deck (franchise/two-parter never
split by the seam); idempotent within a window (no re-push). New tests: rotation coverage (the
exact defect reproduction — 15/15 films air over a cycle), tiling-vs-sliding, idempotency,
franchise-never-split-by-seam across all offsets. **Live-verified** on `ch_2986070a483d5cb0`:
with an eligible pool > window, the aired set rotates and all 15 films air. **Also surfaced (not
a bug):** the channel's **TV-14 ceiling excluded its 9 R-rated films** (Die Hard, Predator,
Terminator, …) — the §4 audience filter working as designed — which is why it looked worse (only
6 PG/PG-13 films, < one window). Maintainer chose to raise this channel to R. Gate: `make check`
GREEN (`-race`); `openapi-verify` + `config-docs` no drift. This is the **catalog-rotation half of
the rotation/re-curation plan** (`.claude/plans/curation-rotation-and-recuration.md`); **next: the
scheduled re-curation job (Phase B, per-channel auto-curate opt-in decided)**. Merged on
green-local (CI still billing-blocked).

**Curation-rule-engine arc — Phase 4: "Programming rules" editor + time-travel preview (2026-07-24).**
The authoring + legibility layer for the wall-clock curation engine (Phases 1–3 already
shipped the deterministic core, seasonal-as-a-rule, and LLM preset authoring — commits
`2f39787`/`f0d01a9`/`22ec659`). Two halves, doc-first (`docs/programming-design.md` §8.1 written
before code): **(BE)** a read-only **`GET /v1/channels/{id}/cycle?at=<rfc3339>`** cycle preview
that runs the SAME pure `ComputeDesiredAt` reconcile runs (one code path — preview can't drift)
at a chosen wall-clock, returning the resolved slots + **which curation rule is active then** +
the resolved rolling-window horizon; makes first-match-by-priority legible ("at Saturday 9am the
Weekend marathon rule wins"). New `schedule.ActiveRuleAt`/`SchedulingRule.Label`/`Describe`
(attribution derived from the SAME `pickRule` the engine uses; suggester now names LLM-authored
rules), `schedule.ResolveWindow`, `channels.Engine.CyclePreview` (read-only — heals/pushes
nothing), and the `ChannelService.CyclePreview` interface method (returns `schedule` primitives, so
`api` stays decoupled from `channels`). **(FE)** a `ChannelRulesEditor` (token-based WHEN/WHAT/HOW
picker mirroring `internal/schedule/presets.go`, `@dnd-kit` drag-to-priority where **list order IS
priority**, computed labels) + a `ChannelCyclePreview` (datetime-local + quick presets → the
attribution callout + program/pending/break slot list), both wired into the channel "rules" tab.
**Bug caught in FE review + fixed:** a hand-authored **marathon** wrote `window:"0s"` (Duration 0 =
"inherit the window" ⇒ WOULD truncate the binge) instead of `"-1ns"` (the `schedule.WindowFull`
sentinel = "whole run") — the opposite of intent; fixed + regression-guarded so a hand-authored
marathon is byte-identical to an LLM-authored one (§8.1). Gate: `make check` GREEN (`-race`);
`make openapi` regenerated + committed (`openapi-verify` clean post-commit); `config-docs` no drift;
FE `biome + typecheck + 402 vitest + web build + storybook build` GREEN (new component stories +
tests, `story-coverage` guard). **Next (Phase 5):** custom holiday calendars (the last §9 logged
item). **CI note:** GitHub Actions still billing-blocked — validated locally + merged on green-local
per the maintainer's standing call.

**Scheduler arc — Phase 5: retire the inbound webhook subsystem (2026-07-23).** The final
phase of the poll-availability arc. Availability + download progress now come entirely from
polling (library scan §4, arr queue poll §18.1), so the inbound `POST /hooks/arr` webhook has
no remaining job — deleted. Removed: `internal/ingest` (the whole package), the `/hooks/arr`
mount + `api.Options.Ingest`, the app wiring, `SecretWebhook`/`WEBHOOK_SECRET` + its reveal/
regenerate enums, the `webhook` setup check + `SetupCheck.LastReceived` + `webhook_last_received:`
settings, the `loomarr_webhook_events_total` metric + `WebhookEvent()`, the four `{sonarr,radarr}/
*_webhook.json` fixtures + `FINDINGS-arr-webhooks.md`, the FE wizard **Webhooks** step (+ its
steps/routes/shell registrations + e2e handshake block), and the secrets-panel `webhook_secret`
row. **Deliberately kept** (look-alikes, verified before deleting): filler-clip ingest
(`clipfetch`/`filler.Ingest`/`FeatureIngest`/`INGEST_*`), the outbound `event.webhook_url`
notifier, and **`provision.KeyFromWebhook` + `radarr/import_webhook.json`** (still used by the
channel-lineup key-parity path). The retirement was authorized by the Phase-2 safety proof
`TestScanAvailability_NoWebhook` (green — a `requested` title reaches `available` with no
webhook), never by weakening a gate. Gate: `make check` GREEN (`-race`); `openapi-verify` +
`config-docs` no drift; FE biome + typecheck + **382 vitest** GREEN; visual + e2e baselines
regenerated in the Playwright Docker image (WizardShell + wizard-flow snapshots; webhook-step
baselines removed). **NOTE (CI):** GitHub Actions is billing-blocked on the account (jobs fail
in ~1s with a payments notice) — Phases 4–5 validated locally and merged on green-local per the
maintainer's call; restore billing to re-enable CI.

**Scheduler arc — Phase 1: cron scheduler + 4-loop migration (2026-07-23).** First phase of
the direct-Sonarr/Radarr + poll-availability plan (retiring inbound webhooks, since verified
research shows Overseerr/Seerr is entirely poll-driven). A real job scheduler like Sonarr's
System → Tasks: `internal/scheduler` (code-defined registry + `scheduled_jobs` state table +
a leased due-claim reusing the `ClaimDueTitles` idiom — SQLite guarded UPDATE / Postgres
`FOR UPDATE SKIP LOCKED`). Schedules are **full cron** (6-field, seconds-leading, matching
Overseerr) via a new `KindCron` setting validated by `github.com/adhocore/gronx` (pure-Go,
zero transitive deps — added to design §14 + a new §18.1, doc-first). **All 4 existing loops
(reconcile, channel-sweep, filler-sync, session-sweep) migrated to scheduler jobs** — their
standalone `Run`/`WithInterval`/ticker plumbing deleted (`reconcile/runner.go`, `janitor.go`,
the `channels.Runner` loop, `go runFillerSync`); each is now Run-now-triggerable with an
editable cron from day one. `GET /v1/jobs` + `POST /v1/jobs/{name}/run` (admin-only); timing
is BE-authored and pushed over a new `job` SSE frame (the FE never computes countdowns).
FE: Settings → **Tasks** page (Sonarr-style table + "Run now") and a reusable **Modify Job**
modal — human-readable cron presets by default, an **Advanced** toggle revealing the raw
cron field. Also removed the redundant Live TV wizard step + `POST /v1/setup/livetv-connect`
(auto-wires on Connections save; config-design §6). Gate: `make check` GREEN (`-race`); FE
`biome + typecheck + vitest` GREEN (381 tests, 11 new). **Next (Phase 2):** poll-based
availability — `RecentlyAdded`/`AllItems` library scan jobs driving `LibraryConfirmed`.

**Phase 14 — domain metrics, tranche 3: §17 closed (2026-07-20).** The last non-latency,
non-state counters, each hooked at its subsystem's natural point:
`loomarr_llm_tokens_total{kind}` (prompt/completion, parsed from the provider usage block —
Ollama `prompt_eval_count`/`eval_count`, OpenAI `usage.*`; zero → no-op so a provider that
omits usage adds no phantom sample); `loomarr_filler_pods_total{match_level}` (fallback-ladder
rung, recorded in `BuildFillerList` — the attach path, NOT `Preview`, so UI previews don't
inflate it); `loomarr_channel_slot_substitutions_total` (`programWentStale` → `staleProgramCount`,
now returns the count, recorded in reconcile). **Cost is deliberately NOT a metric** — it's
tokens × a per-model posted rate that drifts and is hosted-specific, so it belongs in a
dashboard recording rule over the token series, not baked into the request path. That closes
the §17 metric list end to end (RED + runtime + state gauges + event counters + latency +
these). Gate: `make check` GREEN.

**Phase 14 — domain metrics, tranche 2: latency (2026-07-20).** Client-side RED for every
outbound dependency via ONE instrumented transport in `httpx.NewNamed` — the six RPC
adapters (library/tunarr/seerr/tmdb/ollama+openai) now build named clients, so
`loomarr_outbound_request_duration_seconds{target}` + `loomarr_outbound_requests_total{target,code}`
cover the §17 library-lookup / Tunarr-API-latency-and-errors / LLM-latency in one series
filtered by target (a transport failure → `code="error"`). Health probes stay on plain
`New` (unlabelled) to keep the metric to the operational RPC path. Plus reconcile timing:
`Engine.Reconcile` (named-return + defer) emits `loomarr_channel_reconcile_duration_seconds`
and `loomarr_channel_reconciles_total{result}`; the injected clock keeps it deterministic (0)
under fixed-time tests. `httpx → metrics` is a new edge but acyclic (metrics imports only
prometheus + provision). **Still deferred** (§17): LLM token/cost (needs provider usage),
filler pod-ladder depth, slot-drift — domain counters, not latencies. Gate: `make check` GREEN.

**Phase 14 — domain metrics, tranche 1 (2026-07-20).** The §17 *domain* series, first
tranche: the state gauges via a pull-based collector (`loomarr_titles{state}`,
`loomarr_jobs{status}`, `loomarr_active_sessions`) + the two cleanest event counters
(`loomarr_auth_logins_total{result}`, `loomarr_webhook_events_total{type}`). The collector
reads three new store count-by-state methods on *scrape* (not on the write path), so no
mutation path is instrumented; `RegisterStoreCollector` wires it once at boot from
`BuildHandler`. The store methods are dialect-neutral (GROUP BY / COUNT) — one impl on
`sqlStore`, covered by a new `ObservabilityCounts` case in the one-suite-two-backends
conformance suite. Webhook + login labels are bounded (unknown eventType → "other"; login
result ∈ success/failure, rate-limits excluded — they're the 429 signal). Scrape-time store
errors degrade to `loomarr_metrics_scrape_errors_total`, never a panic or a stale zero.
**Still deferred** (§17, honest): the latency histograms (reconcile / Tunarr-API / LLM /
library-lookup), LLM token/cost, filler pod-ladder depth, slot-drift — each needs its own
timing wrapper around an external call, a different pattern; a later tranche. Gate: `make
check` GREEN; conformance passes on sqlite (`make test-pg` for Postgres on CI/Docker).

**Phase 14 — docs set, compose audit, metrics foundation (2026-07-20).** The user-facing
help set (Quickstart, Integrations, Concepts, Member, Filler + Troubleshooting keyed to
checklist items), rewritten lean on maintainer feedback, embedded and served at `/v1/docs`
(`a50c57b`, deep-link routing fixed `eb09813`). Seed docs folded into `docs/` (`62d9369`);
README got Documentation + Operations sections (`f46ddf9`).

*Compose-profile audit* (`61e1f9c`): topology matched §16; three satellite docs had drifted
— README + compose header never showed `--profile ai` (yet the default LLM points at the
ollama service it gates), and `.env.example` called filler "a profile" when §16 is explicit
it's the `loomarr:filler` image tag. Fixed the docs; design.md was already correct.

*Metrics* — `internal/metrics` + `GET /metrics` (unauthenticated, §7), `prometheus/client_golang`
(already sanctioned in §14 line 633, so no new-dep conversation). **Scope, honestly (no silent
caps):** wired the RED basics — `loomarr_http_requests_total` / `_request_duration_seconds` /
`_requests_in_flight`, labelled by method + *matched route pattern* (bounded cardinality, not
raw path) — plus the free Go/process runtime collectors. **Deferred:** the §18 *domain* series
(records-by-state, reconcile + Tunarr-API latency, LLM tokens, filler pod-ladder depth, logins,
active sessions, job-queue depth, janitor purges). The pull-based gauges among them need store
count-by-state methods, which touch the one-suite-two-backends conformance gate — a separate
follow-up, not smuggled in here.

**Maintainer smoke — §21's DoD closed end to end (2026-07-20, cont.).** The walkthrough
now runs intent → grounded proposal (real Ollama) → approve → a channel PLAYING in Tunarr,
proven live: an 80-program kids channel from "90s Saturday morning cartoons", built by
Loomarr's own approve→reconcile against the real Emby. Eight steps; `make smoke-livetv`
wires+destroys a disposable Jellyfin for the one media-server-writing action. **Bugs the
smoke found, every one CI-green beforehand — the seams BETWEEN gate-green subsystems:**

1. `GET /v1/users` panic — int setting read through a string-only seam (`0dc957e`).
2. Empty env var destroyed the operator's saved value — `.env.example` ships `LLM_MODEL=`;
   the resolver read present-but-empty as a pin (`be860bc`).
3. FINDING 1 — a fresh install had no way in (`/`→`/login`, no account); added the
   unauthenticated `GET /v1/setup/state` the router guards branch on (`38f8215`).
4. Model selection didn't hot-apply — `persist` bypassed the settings service, so the
   choice vanished on restart (`2128db5`).
5. `make smoke` could never exit — `go run` supervises rather than execs (`e8a956b`).
6. Live TV wiring broken on ALL of Jellyfin — the enumerate GETs are write-only there
   (405); moved to `GET /System/Configuration/livetv`, works on both flavors (`57209f8`).
7. Wizard stranded operators behind un-skippable wiring steps; both wiring actions also
   surfaced in Settings (`3f3082e`).
8. FINDING 4 — approving never created the channel (§7 said it should); `channelForIntent`
   (`be9cb35`).
9. FINDING 6 — a kids channel went live with ZERO programs: discovery backfill set
   InLibrary but dropped the rating, so an audience ceiling excluded every entry (dead
   air, §9). Backfill now carries the rating (`bad5814`).

**Open (surfaced, not taken):** FINDING 5 — the `tunarr` setup check only probes
reachability, so an unset/foreign `transcode_config_id` reads green while every channel
create 400s; recommend auto-selecting Tunarr's Default + validating it. Part 2 — the
acquisition-rating gap (a not-yet-owned title has no library rating) is entangled with
§389's stamp-once-at-create-time invariant; the clean fix is one design question: should
an entry's rating refresh at reconcile when library presence resolves? (Also fixes the
upgrade case where a pre-fix cached proposal, §8 24h TTL, carries empty ratings forward.)

**Maintainer smoke — the §21 second half, mechanised (2026-07-20).** Branch
`fix/quota-panic-live-smoke`. `make smoke` stands up a THROWAWAY install (own database,
own Tunarr container) and drives it with Playwright against the real media server, TMDB
and Ollama. Not in CI and never in `make check` — those mock every external service by
rule (§19), and this exists because the seams *between* gate-green subsystems are where
the bugs have been. Seerr is deliberately omitted so no approval can start a real
download. The stack stays up between runs, so the suite is stateful by design: every step
asserts something real in both a fresh and a re-run state, because a suite that is
normally red teaches you to ignore it.

**Three real bugs in its first four steps, none of which any gate could have caught:**

1. **`GET /v1/users` panicked on every load** (shipped in #36) — an int setting read
   through a string-only seam. Unit tests all leave the config seam nil, so the typed
   accessor was never reached. Fixed with a `LiveConfigInt` seam + a regression test that
   wires it (`0dc957e`).
2. **An empty env var silently destroyed the operator's saved value** (`be860bc`).
   `.env.example` ships `LLM_MODEL=`, and the resolver read a present-but-empty var as a
   pin. The §8.1 picker persisted `llm.model` and hot-swapped the live suggester — UI said
   "In use", suggestions genuinely worked — while every *read* still resolved to the empty
   pin, so the checklist said "no model selected" right after one was, and the choice
   vanished on the next restart (verified live: `"model":""` → `"model":"qwen3.5:9b"`).
   config-design §3 now states empty-is-unset and boot WARNs each pin it ignores. Every
   settings unit test missed it because none had ever set a key to `""`.
3. **FINDING 1 — a fresh install had no way in.** `/` redirected to `/login`, which no
   credential could pass because no account existed, and nothing on the page said the
   install was unclaimed; only an operator who guessed `/wizard` escaped. §16 has always
   told operators to "open the UI at `/` and create the owning admin", so the code
   contradicted the doc. Fixed with an unauthenticated `GET /v1/setup/state` (§7, doc-first)
   that the `_authed` and `/login` guards branch on; `needsBootstrap` **fails closed**, so a
   blip on that probe cannot drop a healthy install's users into a first-run wizard.
   Covered by a Go handler test (unauthenticated + flips on bootstrap), FE unit tests for
   the failure directions, and two e2e cases (unclaimed → wizard from both entry points;
   claimed + signed out → login).

Gate evidence: `make check`, `make e2e` (7/7), `make fe` unit (223/223), and `make smoke`
against the live stack.

**Phase 13.4e — the 13.4 gate (2026-07-19).** Branch `feat/fe-gate-13.4e`. Phase 13.4 is
complete: Channels, Board, Suggest, Settings, Users, Filler, Help, and the ⌘K palette.

**The reachability guard is the gate this phase earned.** SEVEN times in 13.4 something
was built, unit-tested, and unreachable — two settings panels never mounted; a formatter
never called (so "·til 8:00 PM" was dead UI on every channel card); a 323-line settings
form rendered by nothing; a clip's tag action gated so the one clip needing correction
couldn't be; a search scope that always returned empty; a Search button wired to a
discarded setState. Component tests passed in every case, because a component test cannot
see whether anything mounts it.

`reachability.test.tsx` asserts every route in the generated tree renders real content and
every feature-gated panel appears when its flag is on. The route list is DERIVED from
`router.routesById` — a hand-maintained list is the same mistake one level up, which
`structure.test.ts` already learned. **Verified against the real regressions:** removing
the secrets panel's mount and removing the palette's mount each fail it with a precise
message.

**The approve-flow e2e is §7's gate under test.** An admin approving enqueues the
acquisition; a member is not offered approval and nothing is enqueued (§19's negative).
Assertions land on the mock's recorded state, not the screen — "the button looked like it
worked" is exactly the failure a gate exists to catch. Verified by deleting the `isAdmin`
condition on the approval queue: the member test fails.

**Contract discipline held.** The mock backend's proposal shape was wrong twice (guessed
field names), and both times the fix was to read the generated DTO rather than the
remembered shape — the same rule the fixtures carry.

**Gate:** `make check` GREEN; `make fe` GREEN (217 app / 24 core / 9 api); `make fe-visual`
GREEN (188, zero flaky); `make e2e` GREEN (5); `make openapi-verify` GREEN.
**Next:** Phase 14 — the user-facing docs set, runbook, README, and folding in the seed
docs (checking each against the design doc first: §2 and §7.2 were both stale this phase).


**Phase 13.4d — Users and Filler shipped; Help + ⌘K remain (2026-07-19).** PRs #26–#33,
all merged. The session ran backend-prep-then-screens, and the prep was worth it: the
gap sweep before writing any UI found surfaces §12/§13 assume but nobody had built.

**Backend prep (#26, #27, #28, #29, #30).** Sessions list/revoke; the `user_sync` gate
that fixed a live `useSyncUsers` 404; the credential-path flag; clip search moved off the
federated scope; the sidecar deleted and ingest folded into the core; pod preview; the
Help docs embed, version endpoint, and a storeless-boot panic fix.

**Screens (#32, #33).** Users (allowlist, sessions, explicit import) and Filler (catalog,
tagging, ingest, pod preview). Both admin-gated in the UI as a courtesy; the server
enforces (§11, §19).

**The recurring failure of this phase, worth carrying into 13.4e.** SIX things were built,
unit-tested, and unreachable — `AiModelSettings` and `SecretsSettings` never mounted;
`formatEpgTime` never called, so "·til 8:00 PM" was dead UI on every channel card;
`SettingsGroupForm` (323 lines + tests + 12 visual baselines) rendered by nothing;
`ClipCard`'s tag action gated so the one clip that needs correcting couldn't be;
`scope=clips` advertising a corpus that always returned empty. Every component test
passed in every case. Page-level tests were added for Users and Filler specifically to
assert WIRING rather than behavior — **13.4e should make reachability part of the gate**:
every route renders, every feature-gated panel appears when its flag is on.

**Gates got two real repairs.** The a11y check rejected a disabled row whose `opacity-60`
fell under the WCAG AA contrast floor. And the visual suite's "1 flaky" — present on every
run, a different story each time — was axe-core's module-global running flag re-entering
through `runPartialRecursive`; Playwright's retry kept it green so it never blocked, but a
gate that cries wolf stops being read. Retried only on that exact message; zero flaky on
every run since.

**Shared-code cleanup (#31).** Four core formatters had NO call sites (the inverse of the
duplication we went looking for), one live grammar bug at n=1, and copy-to-clipboard
hand-rolled twice with the same two defects in both copies.

**Gate:** `make check`, `make fe` (191 app tests), `make fe-visual` (188, zero flaky),
`make e2e`, `make openapi-verify` — all GREEN on main.
**Next:** 13.4d-3 (Help page + ⌘K palette — transport and `troubleshooting.md` already
exist from #30, so it is mostly rendering + client-side search), then 13.4e.


**Phase 13.4d-0c — Help transport, version, and a startup panic (2026-07-19).** Branch
`feat/be-help-13.4d0c`. The last backend slice before the 13.4d screens.

**The docHref anchors had nothing to land on.** The API has emitted deep-links like
`troubleshooting#tunarr` since phase 8, and §13 promises "every red check deep-links to
its section" — but nothing embedded `docs/`, and the troubleshooting page did not exist.
Every red check pointed at a blank page, at exactly the moment an operator is stuck.
Now: `docs/help/` embedded via `docs/embed.go`, `GET /v1/docs` + `GET /v1/docs/{slug}`,
and a **test that every anchor the API emits resolves to a real heading** — renaming a
heading fails the build instead of silently breaking a link. Only `help/` ships; the
design docs beside it are internal and deliberately excluded.

Wrote `troubleshooting.md` with all 8 sections. This is not phase 14's docs set (that
still owns Quickstart/Concepts/Member guide/etc.) — it is the one page the *existing*
backend contract already requires. Also gave `tunarr_library` its own anchor rather than
sharing `#tunarr`: it fails for a specific silent reason (unscanned media source ⇒ dead
air while everything else reads healthy) and deserves that explanation, not generic
connectivity advice.

**Version.** Nothing exposed one — §16 discusses upgrades and the runbook assumes the
operator knows what they run. `internal/buildinfo` stamps via ldflags, falls back to Go's
embedded VCS stamps, then to "dev". Surfaced on `GET /v1/system/version` together with
readiness.

**Deliberately did NOT move /healthz + /readyz into huma.** The sweep suggested it so
orval could type them, but their consumers are Docker HEALTHCHECK and orchestrators,
which hold no session — registering them in the authenticated /v1 API would put auth in
front of a container health probe. The UI gets the typed twin instead. Pinned by a test,
because "type the health endpoints" is a tempting future change.

> ⚠ **REVERSED LATER, and the pin did its job.** The probes are Huma operations under
> `/v1/healthz` and `/v1/readyz` now. What changed is not the concern above but its
> premise: "registering them in huma" no longer implies "in the authenticated API",
> because `RolePublic` makes non-authentication an explicit, greppable property of an
> operation. The two things this entry actually cared about are both preserved and still
> tested — the probes answer with **no credential**, and a **storeless boot** still gets
> 200 from healthz and 503-with-a-reason from readyz (the 503 keeps carrying `ready` and
> `detail` rather than becoming an RFC 7807 problem; huma additionally emits its standard
> `$schema` link, as on every other typed response here). Their bare paths stay as permanent
> aliases, because a healthcheck lives in someone's compose file and cannot be migrated
> by editing this repo. `TestOpsProbesStayUnauthenticated` survives, now covering both
> paths, plus `TestOpsProbeAliasesAgreeWithTheCanonicalPaths`.

**Found and fixed a startup panic (pre-existing, from the 2026-07-18 guide-reader work).**
Starting with no DATABASE_URL is a SUPPORTED mode — main logs "running without a store
(not ready)" and expects /readyz to explain itself. Instead the process panicked in
BuildHandler: no store ⇒ no settings service, and `resolved.str` dereferenced nil. A
container missing DATABASE_URL crash-looped instead of answering the probe that would
have told the operator why. Guarded every `resolved` getter; regression test verified to
reproduce the exact panic without the fix.

**Gate:** `make check` GREEN; `make fe` GREEN (169 app tests); `make openapi-verify` GREEN;
manually verified a storeless boot serves /healthz 200 and /readyz 503 "no store configured".
**Next:** 13.4d proper — the Filler, Users, Help, and ⌘K screens.


**Phase 13.4d-0b pt2 — the sidecar is actually gone (2026-07-19).** Branch
`feat/ingest-in-core-13.4d0b2`. PR #25 changed the DESIGN; this closes the code. Between
the two, the docs said the sidecar was removed while the repo still shipped it — real
drift, caught by the maintainer noticing `Dockerfile.ingest` in the tree.

**The move was smaller than feared.** The download logic was already `internal/ingestkit`
(a core package, fully tested with fake downloaders); only a 71-line driver in
`cmd/loomarr-ingest/` was sidecar-specific. Renamed to **`internal/clipfetch`** because
`internal/ingest` is the Sonarr/Radarr WEBHOOK handler (§6's Ingest port) — two unrelated
concepts one autocomplete apart, now that both live in the core.

**Two real bugs found by actually building and running the image:**
1. **The tooling was x86_64-only.** Inherited from `Dockerfile.ingest`, which hardcoded
   amd64 URLs for yt-dlp/deno/ffmpeg. On arm64 the image BUILT FINE and then died at run
   time with `rosetta error: failed to open elf at /lib64/ld-linux-x86-64.so.2`. Now
   fetched per `TARGETARCH`, with a build-time `--version` check on all three so a broken
   image can never ship looking healthy.
2. **Appending the stage made the fat image the DEFAULT.** Docker builds the last stage,
   so `docker build .` produced 704MB instead of the distroless core. Reordered: filler
   before core, core last. Measured after: **loomarr:latest 31.1MB, loomarr:filler 549MB.**

**The documented size was wrong.** §10/§14/§16 claimed "~170MB" (inherited from the
sidecar's own comment). Measured reality is ~518MB more. Corrected everywhere rather than
left as a comfortable number. Also dropped **ffprobe** (~99MB): Loomarr never probes media
— Tunarr assigns duration during its `local`-source scan — so bundling it was pure weight.

**`ingest` is the first environment-derived feature gate.** It probes for runnable
yt-dlp + ffmpeg rather than reading settings completeness, exactly as config-design §7's
new exception describes. Both tools are required (yt-dlp cannot merge streams without
ffmpeg, so a half-present image would accept the job and fail mid-download on the
high-res sources most worth fetching). The 409 names the remedy — "run the loomarr:filler
image" — and deliberately is NOT `feature_not_configured`, because no setting can open it.

**Ingest returns a job id, not a result.** Downloads run minutes to hours; progress rides
the SSE bus as `filler_ingest` frames, the same shape §8.1's model pull uses. The
background context is deliberately NOT the request context, or every ingest would die the
moment a browser tab closed.

**Gate:** `make check` GREEN; `make fe` GREEN (169 app tests); `make openapi-verify` GREEN;
`docker build` of BOTH targets verified, and yt-dlp/deno/ffmpeg each executed natively on
arm64 (not inferred from a successful build).
**Next:** 13.4d-0b pt3 (pod preview — §12's explicit Filler requirement), then 13.4d-0c.


**Design change — filler ingest moves into the core; the `loomarr-ingest` sidecar is removed (2026-07-19).**
Branch `design/ingest-in-core`. Doc-only; no code, no spec drift. Maintainer call, taken before building
13.4d's Filler page so the page isn't designed against a seam we were about to delete.

**What changed and why.** The sidecar's *only* stated justification was keeping ~170MB of yt-dlp+ffmpeg out of
the core image (§14). It was already Go, so §14's language policy never required it. What that slimness cost:
a second image to build/version/publish, a `filler` compose profile, a proxy endpoint in the core just to reach
the sidecar, ingest progress that could not ride the existing SSE job bus, and a Filler page whose primary
action was a hop to an optional service that might not be running. Ingest is now a normal in-core job, and the
tooling ships in an opt-in image *tag* (`loomarr:filler`) rather than an opt-in *service* — same binary, same
config, same endpoints, so operators move between tags with a restart, not a topology change.

**The one genuinely new mechanism:** `ingest` is the first feature gate that is NOT derived from settings
completeness — no amount of configuring opens it on `loomarr:latest`, because it depends on which image is
running. config-design §7's heading previously claimed `RequiredFor` computed availability and "nothing else
does"; that is now stated as an explicit, reasoned exception rather than left as a quiet contradiction. The
invariant that survives: one function computes the set, every consumer reads it — only the inputs differ.
ffmpeg is bundled (not skipped) so yt-dlp can merge separate video/audio streams; without it, high-res sources
fail or silently downgrade.

**Sections touched:** §2 (filler flow + FillerSource port row), §7 (`/v1/filler/ingest`), §10 (ingestion path,
config, probing), §13 (Filler guide), §14 (Sidecar & CI → Ingest tooling & CI), §15 (four `INGEST_*` keys),
§16 (two tags one binary; compose profiles now sqlite/postgres/ai), §21 (repo layout drops
`cmd/loomarr-ingest/`; phase-12 text); config-design §5 (Filler settings row), §7 (the exception), §6 (wizard
step). The §15 additions are the human mirror only — the Go registry entries land with the implementation, so
`make config-docs` stays drift-free until then.

**Stale docs fixed in passing (a real contradiction, not cosmetics):** §2 lines 85/98 still described filler as
"the media server scans its dedicated filler library" and "media-server filler-library sync" — but §10 was
revised long ago to Tunarr-`local` with the media server *out* of the filler path, and §2 was never updated.
Same for `/v1/filler/sync`'s table description and config-design's Filler settings row (which also referenced
`/v1/filler/sources`, an endpoint that does not exist).

**Gate:** `make check` GREEN; `make openapi-verify` GREEN (no spec drift — doc-only).
**Next:** 13.4d-0a/0b/0c (the backend surfaces 13.4d needs), then 13.4d itself.


**Phase 13.4c-2 — the model picker and the secrets panel (2026-07-19).** Branch `feat/fe-settings-13.4c2`. The two
sub-features deliberately deferred out of 13.4c, each substantial enough to deserve the room.

**The §8.1 model picker renders a judgement it does not make.** Picking a local model is a real onboarding hurdle —
a household admin should not have to know which Ollama tag fits their GPU or supports tool-calling — so the BE
probes the machine and returns a catalog already annotated with `fit` (fits/tight/wont_fit), `approxVramGiB`,
`runtimeOk`, `pulled`, `recommended`, and a `why`. The UI's whole job is to show that honestly. Two calls worth
recording: an unrunnable model is shown **disabled with its reason** rather than hidden, because "why isn't X
listed?" is a worse question than seeing X greyed out; and an unpulled tag offers **only Download**, never "use
this", because `select` 409s on a model that is not local (§8.1). Pull progress exists only on the `llm_pull` SSE
frames, so it arrives through the shared event fan-out built in 13.4a, and a terminal frame refreshes the catalog
so the row flips from Download to In-use without a reload.

**The secrets panel states consequences before the click, not after.** §4's display policy is differentiated by
PURPOSE, so the panel is a closed set of three rather than a generic list: `API_TOKEN` and `WEBHOOK_SECRET` are
values you must paste elsewhere (viewable on demand, eye toggle + copy), while `SESSION_SECRET` has nothing to
paste anywhere and is **never displayed** — Regenerate is its only affordance. Regeneration requires a second
confirm carrying the specific consequence, because they differ sharply: rotating the webhook secret silently
breaks every Sonarr/Radarr hook already configured, and rotating the session secret signs everyone out *including
the person clicking*. An operator should learn that from the button, not the aftermath. Revealing is fetched
**imperatively on click** rather than mounted as a live query — a secret should cross the wire when asked for, not
sit in the cache of every page that renders the panel.

Gates: `make check` (30 packages) + `openapi-verify` GREEN; `make fe` GREEN (**189 tests**); Docker visual GREEN
(172, **14 new baselines**, a11y clean); `make e2e` GREEN. **Next: 13.4d** (Filler · Users · Help · ⌘K) then
**13.4e** (the 13.4 gate).

**Phase 13.4c — Settings (2026-07-19).** Branch `feat/fe-settings-13.4c`. The troubleshooting console (§13) and
the first real test of whether `SettingsGroupForm` held up outside the wizard. **It did not**, and that was the
useful finding.

**The save UNIT differs by context, so form ownership had to move out.** config-design §5 specifies ONE sticky save
bar per page — Sonarr's model, chosen because connection settings change *together* (a URL and its token) and a
half-saved pair caught mid-test looks like a broken integration. But Connections alone has four blocks (media
server, requester, Tunarr, TMDB), and `SettingsGroupForm` owned both its state and its own Save button: four
blocks would have meant four save buttons and no aggregate dirty state. Extracted **`SettingsFields`** — a
*controlled* group of fields, no `<form>`, no save — which the wizard (one group per step, own save) and a
Settings page (many blocks, one bar) both compose. `SettingsGroupForm` now wraps it; **all 29 wizard tests passed
unchanged**, which is what made the refactor safe to do.

**Also corrected:** the build plan says 5 Settings pages; config-design §5 lists **6** — it omits **Filler**. §5 is
authoritative on Settings IA (a companion wins on its own domain), so six were built. And `updatedBy`/`updatedAt`
existed on every entry but were never rendered; §5's field anatomy asks for "changed by … · when", now shown —
only where a *person* set the value, since an env pin or built-in default has no author.

**Built:** the six pages behind a nav, each composing blocks with one save bar that appears only when dirty and
sends **only changed keys** (an untouched secret reads back empty (§4) and an empty-string PATCH would clear it
(§9) — the hazard fixed in #14, now guarded at the page level too). Per-block **Test** runs the same named check
the wizard uses. The re-runnable **connection checklist** sits at the top of Connections, making Settings the
troubleshooting console for the life of the install rather than a first-run-only screen.

**Both conventions guards fired on my own work** — `structure.test.ts` caught a hook filed inside another module's
folder, and `story-coverage.test.ts` caught two new Layer-2 components shipped without stories. Biome's a11y rule
caught `role="region"` where a `<section>` belongs. Three convention failures I did not have to notice myself.

**A silent no-op in my own tooling, worth recording:** this entry was missing from the 13.4c PR entirely. The
insert anchored on a marker that did not exist on that branch (13.4b was still unmerged), and `str.replace`
returns the string unchanged rather than failing — the same class of quiet failure as an ignored `maxAcquire` or a
guessed guide field. Entries now anchor on the file's stable preamble and the result is verified, not assumed.

**Deferred, flagged:** the §8.1 **AI model picker** (probe, fit-ranked catalog, hot-swap, `llm_pull` progress over
SSE) and the **generated-secrets panel** (view/copy/regenerate, §4).

Gates: `make check` (30 packages) GREEN; `make fe` GREEN (**167 tests**); Docker visual GREEN (157, **14 new
baselines**, a11y clean); `make e2e` GREEN. **Next: 13.4c-2** (model picker + secrets panel) then **13.4d**
(Filler · Users · Help · ⌘K).

**Phase 13.4b — Channels, Channel detail, Board (2026-07-19).** Branch `feat/fe-channels-13.4b`. The
"where is my stuff" surfaces, on top of the guide read landed as this slice's prerequisite (#19).

**Two derivations that belong in one place, not inline in a page.** `channelHealth` maps the API's lifecycle
(`building/live/drifted/detached`) plus slot fill onto the card's presentational health — deliberately different
vocabularies (channel-card.type.ts says a page must derive this). The interesting case is **live with unfilled
slots**: the channel IS airing (Tunarr plays flex, never dead air, §9) but is not yet what was asked for, so it
reads `pending-slots` rather than `healthy`, which would hide the backfill the operator is waiting on.
`channelOnAir` answers a *different* question — a **drifted** channel still broadcasts, it just no longer matches
intent, so it is `live` there and `drift` in health. Both are pure and tested.

**The Board leads with the journey, not the states.** §13 asks for member framing — "1 of 3 titles have landed" —
so the five provisioning states (§4) collapse into waiting · acquiring · ready. `unavailable` deliberately does NOT
become a fourth stage: a title that gave up after its TTL belongs to the "waiting" conversation (something to
retry), not a verdict on the channel — and it stays in the progress **denominator**, since dropping it would
silently shrink what was requested and read as better progress than reality.

**Built:** Channels (cards with now/next from the one-call guide endpoint, health rollup, reconcile-now, and the
§6 empty state whose single next action is "suggest a channel" — the only way to make one). Channel detail moved
the route into a directory (`channels/index.tsx` + `channels/$id.tsx`) and shows slot fill, the Tunarr link, and
the **relaxation ladder**: each applied relaxation is structured (`kind/from/to`), so a chip reads
"audience: TV-Y → TV-Y7" instead of an opaque label — the honest account of what Loomarr loosened to fill the
channel (programming-design §9). Board groups titles by stage with per-title `StateBadge` for the operator who
wants detail, and offers retry only where a human can act.

**Caught by the typechecker, not by me:** the retry initially posted `{key}`, but the enqueue contract takes the
title's real identity (`mediaType/tmdbId/tvdbId/name/year`) — the 1:1 generated client refusing a hand-guessed
body, which is exactly what it is for.

Gates: `make check` (30 packages) + `openapi-verify` GREEN; `make fe` GREEN (**172 tests**); Docker visual GREEN
(144, 0 diffs); `make e2e` GREEN. **Next: 13.4c** — Settings (5 groups, secrets lifecycle, the §8.1 model picker,
and the re-runnable checklist as the troubleshooting console), which reuses `SettingsGroupForm` from 13.3b.

**Phase 13.4b (prerequisite) — reading Tunarr's guide for now/next (2026-07-19).** Branch
`feat/fe-channels-13.4b`. The BE half of Channels: `NowNextStrip` was built in 13.2 and listed under Channels in
the build plan, but had **no data source**. `LineupEntry` carries a duration and no start time — Loomarr owns the
lineup (what should play), Tunarr owns playout (when it actually plays, §6) — so airtimes are Tunarr's truth and
cannot be recomputed here without duplicating its scheduling math.

**How the contract was established, after two wrong turns worth recording.** First I claimed this needed
maintainer-supervised live contact and handed over curl commands. That was invented: the repo vendors Tunarr's
OpenAPI (`api/vendor/tunarr-openapi.json`), a version-pinned Tunarr runs in the dev stack, `TUNARR_URL` is in
`.env`, and Tunarr is open source. The rule I cited forbids the *test suite* touching the network, not
investigating. Second — prompted by "the code is open source, why not look at it?" — reading Tunarr's own zod
schemas at tag `v1.3.8` **invalidated the capture I had just taken**: I had sampled the SINGULAR
`/api/guide/channels/{id}`, whose shape (`{index, lineupItem, startTimeMs}`) carries **no title and no stop time**.
Had that been pinned, Channels would have shipped a title-less strip plus a second lookup just to name a program.
The **plural** `/api/guide/channels` carries `start`/`stop`, a real `title`, and tmdb `identifiers[]` — and is keyed
by channel id, so a Channels LIST costs **one** upstream call, not one per card. That also dissolved the N+1
objection I had raised against doing this at all. Capture + both traps recorded in
`fixtures/tunarr/GUIDE-FINDINGS.md`; the vendored spec does not type the guide response, so the source and the
capture are the only authorities.

**Built:** `programmer.Guide` (parses the guide; tmdb id lifted from `identifiers[]` so an entry joins to a
provisioning key with no second lookup; an ungenerated guide is empty, not an error — the tolerance `GetLineup`
already gives an unprogrammed channel). `guideAdapter` reduces a timeline to the pair a card shows, using a
**containment** test rather than "the first entry", so an out-of-order or already-finished head cannot mislabel
what is on. `GET /v1/channels/now-next` serves every channel from that one call, keyed by the **Loomarr** id (the
FE never sees Tunarr ids), and a Tunarr hiccup returns empty rather than blanking the page.

**Self-review caught two more:** the new route shares a prefix with `/v1/channels/{id}` and could plausibly have
resolved to `get-channel` with `id="now-next"` — Go 1.22 prefers the literal segment, but that was assumed rather
than proven, and is now pinned; and a test carried a hand-rolled `itoa` reinventing `strconv`. Parsing is tested
against the pinned capture and **proven to fail** (read the title-less singular shape → red).

Gates: `make check` (30 packages) + `openapi-verify` GREEN; `make fe` GREEN; `make e2e` GREEN; orval generated
`useChannelsNowNext`. **Next: 13.4b proper** — the Channels page (cards, health rollup, now/next, reconcile-now),
Channel detail, and the Board.

**Phase 13.4a — the Suggest workspace + approval queue (2026-07-18).** Branch `feat/fe-suggest-13.4a`. The
product's core loop: a sentence becomes a grounded lineup. 13.4 is 8 surfaces, so it is sliced — **a** Suggest +
approval, **b** Channels + Board, **c** Settings, **d** Filler/Users/Help/⌘K, **e** the gate. Suggest went first
because the wizard already hands off to `/suggest` and it was landing on a placeholder — a broken promise in
shipped code.

**A three-way contract mismatch, fixed at the root (first commit).** `submitInput.Body` was a hand-mirrored copy
of `suggest.Intent` — the exact pattern PR #9 removed from ProposalDTO, whose "zero hand-written mirror on either
side" comment sits *twelve lines below* the mirror that had drifted. It omitted `RuntimeTgt`, so `runtimeTargetMin`
was **unreachable by any client** even though the suggester feeds it to the LLM prompt and the scorer, and §13
lists a runtime target among the constraints a user may set. Meanwhile the FE's `intentSchema` said `maxAcquire`
where the wire says `maxAcquisitions` — parses fine, serializes fine, server ignores it: **a user's acquisition cap
silently vanished**. Fixed by typing the body from the domain rather than patching one field, so `SubmitInputBody`
disappears from the spec entirely and the request body now refs the SAME `Intent` schema `Proposal.intent` already
used (spec net −29 lines). Two now-redundant `submitAdapter` copies deleted — the composition root's and a second
one duplicated inside `internal/integration`. **Both halves are guarded, both proven to fail:**
`TestSubmit_CarriesTheWholeIntent` (drop RuntimeTgt's json tag → red) and a **compile-time** `intent-contract.test.ts`
asserting the form's output *is* a valid API `Intent` (rename a field → `tsc` breaks).

**One SSE stream, many listeners.** The live phases (`searching · reasoning · scoring`) exist ONLY on the wire — no
GET returns them — but the layout already opens an EventSource for cache invalidation, so a screen calling
`useLoomarrEvents` again would open a second. `LoomarrEventsProvider` owns the single subscription and fans frames
out; `useLoomarrEventListener` is a no-op outside it, so a component still renders in a story or unit test, just
without live frames.

**Built:** `IntentForm` (the hero + templates from `packages/core`, plus §13's "intent-writing hints" — era, tone,
runtime target, acquisition cap — behind a disclosure, because the blank page is solved by one good sentence, not a
form) → `useSuggestionRun` (submit → job → live phase → the proposal, matched on `jobId` client-side since
`GET /v1/proposals` filters by status but not job, and the DTO already carries it) → `GenerationProgress` →
`ProposalReview`, with approve/deny **admin-only** (§7/§11 — a member reviews without the controls). The
`ApprovalQueue` lists everything still `submitted`: approving is the only path from a proposal to `/v1/titles`, so
that list is the audit surface for what is about to spend real resources. Per §8 the stream stays a latency
optimisation — a dropped `done` frame costs a spinner, not a proposal, because the list refetches on the same event.

Gates: `make check` (30 packages) + `openapi-verify` GREEN; `make fe` GREEN (**170 tests**); Docker visual GREEN
(144, 0 diffs); `make e2e` GREEN. **Next: 13.4b** — Channels + Channel detail + Board.

**Phase 13.3d — the phase gate: wizard flow suite + page snapshots (2026-07-18).** Branch `feat/fe-wizard-13.3d`.
Closes Phase 13.3 (frontend-build-plan §5 gate: "wizard e2e smoke vs mocked BE green; page-level snapshot per
wizard step"). `make e2e` was a stub that exited 1; it is now real.

**What it drives.** The REAL embedded SPA build (`internal/web/dist`, served with history fallback so `/wizard`
resolves exactly as it does from the Go binary's embed), against a **mocked `/v1` via Playwright route
interception** — no MSW dependency and no stub server, so there is no second API implementation to drift from
`openapi.yaml`. The mock is deliberately **stateful**: signing in flips `me`, each one-click wiring turns its own
check green, importing marks the candidate imported. A first-run flow that can't progress proves nothing.

**The smoke walks a fresh install end to end** — bootstrap → auto-login → checklist → Live TV → webhook handshake
(asserting the URL is built from the *revealed* secret and that both apps read as listening, neither as failed) →
skip → Tunarr library → import users → guided first channel → lands on `/suggest?intent=…` with `setup.completed`
flipped. Plus both halves of first-run routing: a completed setup opens Channels, an unfinished one is sent back.
**Seven page-level snapshots**, one per step, with a `mask()` for the relative-timestamp region (real behaviour we
want rendering, just not diffing).

**The gate immediately earned its keep — a bug no unit test had caught.** On the optional **users** step, Continue
was permanently disabled: `isStepDone` returns false for any step without a server check, so an operator who
*did* import users was stranded behind a button that could never enable — only Skip worked. Optional steps must
never block; they now gate on skippability rather than completion. Only walking the whole flow as a user surfaces
that. A page snapshot also caught duplicated copy (the shell's step description and the step's own paragraph said
the same sentence), now deduped to the §11 point that actually surprises people.

**Two Playwright configs, one determinism kit.** A maintainer question ("why two configs?") exposed real
duplication: the diff ratio, launch flags, reduced-motion and retries were copy-pasted, so tuning one gate would
silently diverge from the other. They now share `playwright.shared.ts`. The configs stay separate for a concrete
reason recorded there: Playwright boots **every** configured `webServer` regardless of project filter, and the
suites have different build prerequisites (`storybook-static` vs `internal/web/dist`) — merging would force both
builds to run either gate. `make e2e` mounts the repo ROOT (not `web/`) because the SPA build lands outside `web/`,
and serves via pure-JS `http-server` for the same reason the visual suite does: the bind-mounted `node_modules`
holds the HOST's native binaries, so vite/rollup cannot run inside the Linux image.

**Proven to fail:** renaming the Live TV CTA breaks the flow and the suite goes red on all three attempts, then
green again on revert. Gates: `make check` (30 packages) + `openapi-verify` GREEN; `make fe` GREEN (165 tests);
Docker visual GREEN (144, 0 baseline diffs); **`make e2e` GREEN (3 specs, 7 committed page baselines)**. CLAUDE.md's
command contract updated (`make e2e` / `make e2e-update`). **Phase 13.3 COMPLETE — next: 13.4, the core product
surfaces**, which inherits the Suggest workspace the wizard now hands off to.

**Phase 13.3c — wizard steps 3–7 + the conventions guard (2026-07-18).** Branch `feat/fe-wizard-13.3c`.
Completes the operator first-run flow, and makes the FE conventions self-enforcing after they were broken twice.

**Two more doc↔code gaps closed (BE prerequisite, doc-first).** Scoping the steps surfaced two read surfaces the
docs promise but nothing implemented — the same class as `setup.completed` in 13.3b. (1) §4 says `API_TOKEN` and
`WEBHOOK_SECRET` are "viewable on demand by admins (eye toggle + copy button)", and `secrets.go` even comments that
webhook_secret is "viewable (as URL)" — but the only route returning a generated secret's value was
`POST …/regenerate`, so an operator could see the webhook secret **only by rotating it**, breaking every webhook
already configured. Added **`GET /v1/settings/secrets/{name}`** (`{value, displayable}`; SESSION_SECRET withheld),
with a test asserting **reading never rotates**. (2) §11/§13 say the admin "picks which Emby/Jellyfin accounts get
in", but `POST /v1/users/import` takes raw media-server ids and nothing listed candidates — an admin would have
needed GUIDs. Added **`GET /v1/users/candidates`** (accounts + `imported` flag), tested through the REAL provisioner
against the testkit media server, asserting the flag flips after an import.

**Steps built.** 3 **Live TV** + 5 **Tunarr library** share a `ConnectStep`: both are idempotent one-click wirings
where **the BE check, not the click, reports success** (§6 "never silent"), so the button stays available once green.
4 **Webhook handshake** shows the paste-able `/hooks/arr?token=…` URL built from the revealed secret and **polls
`setup/status` per app**, so Sonarr and Radarr flip independently while the operator is in the other tab. 6 **Import
users** renders the candidates picker — already-imported accounts stay **visible but locked** ("where did they go?"
is worse than a disabled row), and a missing media server reads as a reason to skip, not a wall. 7 **First channel**
**hands off** rather than duplicating 13.4: it flips `setup.completed` through the ordinary PATCH and drops the
operator into Suggest with the intent prefilled. Channel templates moved from `packages/fixtures` (story data) to
**`packages/core` as product data** — §13 says they ship in the bundle — with a test asserting every template is a
valid `intentSchema` intent.

**A maintainer catch worth more than the feature work.** `src/wizard/` (15 loose files) and `src/auth/` (4) both
violated the standing folder-per-module rule — which was *already written down in memory* and broken anyway, twice.
Both are now folder-per-module with co-located `.type.ts` + `index.ts`, and the shared `wiring-step.type.ts` was
dissolved so each step owns its props. The durable fix isn't better recall, it's **`src/test/structure.test.ts`**:
a conformance test that fails on a loose file, a missing barrel, or a misnamed implementation file — the same move
`story-coverage.test.ts` makes for stories and the GritQL plugins make for arrow-functions/exports. **Proven to
fail** (drop a stray `.ts` in `src/wizard/` → red). The conventions memory now records which check enforces which
rule, so a future rule change ships with its check.

Also fixed en route: `ChecklistItem` renders `hint` only on `fail`, so the webhook step's "Listening — press Test"
silently vanished; the per-app status line now lives in the step rather than bending a shared component (and its
baselines) for one caller. Gates: `make check` (30 packages) + `openapi-verify` GREEN; `make fe` GREEN (**165
tests**, +31); Docker visual GREEN (144 passing, **0 baseline diffs**). **Next: 13.3d** — the phase gate (wizard e2e
smoke vs a mocked BE + a page-level snapshot per step).

**Fix: an empty-string PATCH could silently wipe every stored secret (2026-07-18).** Branch
`fix/settings-secret-clear` (BE-only; independent of the 13.3b/form PRs). Found while hardening the FE settings form:
the FE guard I'd added only protected *our* client, so the hole was still open in the API itself.

**The hazard.** `GET /v1/settings` deliberately returns **no value** for a secret (`internal/settings/list.go` — only
`set`/`preview`, per §4 "never echoed"), while `PATCH /v1/settings` treated `""` as **delete** for *any* key
(`write.go`). So **a GET → PATCH round-trip destroyed every stored secret** — Emby token, Seerr/TMDB/LLM keys. Any
script, the break-glass `API_TOKEN` path, or a future client (mobile, a second FE) would hit it; nothing in the API
prevented it. Non-secrets were never at risk (GET returns their real values, so their round-trip is idempotent).

**The fix (doc-first, maintainer-chosen "reject + explicit DELETE").** An empty-string PATCH on a `secret` key is now
**`invalid`** ("replace-only; send a new value, or DELETE the key to clear it") — making the round-trip *loud and
harmless* rather than quietly destructive, and matching §4's own replace-only language. Clearing keeps working through
a new explicit verb: **`DELETE /v1/settings/{key}`** (admin) drops the stored override so the key reverts to
env/default — `204` cleared · `404` unknown · `409` env-pinned (the environment wins; unset the variable to manage it
in-app). Hot-applies like any write. Docs first: config-design §9 carve-out + §8 route, design §7 endpoint table.
Nothing reachable was lost — the FE already couldn't clear a secret (a secret's baseline is `""`, so the changed-only
PATCH never included it).

**Tests are the point here:** a direct regression test reproduces the naive round-trip (`library.token: ""` alongside a
real edit) and asserts the stored secret **survives**, plus the rejection carries actionable text; `Clear` is covered
for success/unknown/env-pinned, and the route test asserts the three HTTP mappings and that DELETE is admin-only (§19).
`make check` + `make openapi-verify` GREEN; `make fe` GREEN (orval picked up the route — `useSettingsClear` is ready
for 13.4 Settings' "remove this integration").

**Forms: react-hook-form → TanStack Form (2026-07-18).** Branch `refactor/fe-tanstack-form` (stacked on 13.3b).
Maintainer call, done doc-first — the third and final leg of the deliberate TanStack consolidation (Query 13.1,
Router 13.3a, Form now).

**Doc-first (§14 Forms row + frontend-design §4.3/§6 + frontend-build-plan):** the old row justified RHF as *"the
shadcn form convention"* — a justification that was already moot, since Loomarr never adopted shadcn's RHF-bound
`<Form>` wrapper (every form hand-composes `Label`+`Input`). New rationale: `zod@^3.24` implements **Standard Schema**,
which TanStack Form consumes natively, so the `packages/core` schemas pass straight in and **`@hookform/resolvers`
disappears entirely** — two deps collapse to one; field types infer from `defaultValues` (the same end-to-end typing
as orval DTOs + typed router links); `@tanstack/form-core` is framework-agnostic so mobile shares form *logic*, not
just the schemas.

**Behavior-neutral by construction:** validators run `onSubmit` (as RHF did), not `onChange` — errors stay out of the
operator's way while typing. **`LoginForm` and the bootstrap-step tests passed completely unchanged**, which was the
intended signal that neither DOM nor UX drifted; the Docker visual suite agrees (**143 passing, 0 baseline diffs**).
The open question from the plan resolved cleanly: `bootstrapSchema`'s cross-field `.refine(…, {path:["confirm"]})`
does surface on the `confirm` field through Standard Schema.

**`SettingsGroupForm` converted from controlled-props to owning its state** (maintainer chose the wider scope), which
surfaced **two real findings**. (1) **TanStack Form reads a dot in a field name as a nested path** — and every registry
key is dotted, so `name="library.url"` wrote `{library:{url}}` instead of the flat key the API expects. Values are now
carried **positionally** (aligned with `entries`), keeping the form agnostic to key shape; the mapping back to dotted
keys lives in one tested helper. (2) Because the form now produces the PATCH body, it must submit **only changed**
fields: a stored secret always reads back as `""` (§4 never echoes it) and an empty-string PATCH **clears** an optional
key (§9) — so submitting every field would have silently wiped every stored secret on save. `changedEntries` enforces
that, with a dedicated test asserting an untouched secret is absent from the body. The form deliberately does **not**
re-seed on `entries` change (a background refetch must never overwrite typing); a caller wanting a fresh baseline
remounts with a `key`.

Gates: `make fe` GREEN (**134 tests**), Docker visual GREEN (143, **0 diffs**), `make check` GREEN (no Go changes).

**Phase 13.3b — wizard foundation + steps 1–2 (2026-07-18).** Branch `feat/fe-wizard-13.3b`. The settings-driven
wizard machinery, built on config-design §6's rule that **the wizard IS the settings system** — no parallel form
system; each step renders a registry group's form through the same `PATCH /v1/settings` path Settings (13.4) will use.

**Doc↔code gap closed (doc-first):** config-design §6 specified *"a `setup_completed` flag in the registry"* but no
such key existed (checked all 47). Added **`setup.completed`** (bool, `SETUP_COMPLETED`, Advanced, GroupAdvanced) to
`internal/settings/declared.go`, and amended §6 to name the key exactly + note it's written through the ordinary PATCH.
`api/openapi.yaml` unchanged (registry keys are data, not schema — `openapi-verify` clean); `docs/configuration.md`
regenerated (+1 row).

**Built — the reusable half (all Layer-2, story + test each):** `SettingField` — ONE `SettingEntry` as a control,
everything from contract data: `kind` picks the widget (bool→Checkbox, enum→Select, int/url/string→Input,
secret→password), `enum` fills options, `doc` is help text, **`provenance:"env"` locks the field** with a "set via
environment" chip (§3 visible provenance), a stored secret shows its masked `preview` tail with **replace-only**
editing (§4 — never echoed), `caution` explains a self-healed value, and a `SettingResult` renders `invalid(problem)`
inline / `pinned` as a badge. `SettingsGroupForm` — a group's fields + the per-group **Show advanced (n)** toggle (§5)
+ inline live-test result + Save. Two new **dependency-free ui primitives** (`Checkbox`, `Select`) as native elements —
deliberately not Radix, so no §14 dep conversation for a checkbox and a 2-option list. `humanizeSettingKey` added to
`packages/core` (`library.url` → "Library URL") because the settings API ships `doc` but **no display label**; derived
once so wizard + Settings can't drift, and it doubles as the checklist's check-name humanizer.

**Built — the wizard:** `WizardShell` (rail + card + nav; step states `done|current|pending|skipped` — a **skipped
optional step renders neutral, never red**, §6). `src/wizard/steps.ts` holds the **resume-safe derivation as pure,
tested functions**: completion comes from server truth (`GET /v1/setup/status` + whether a session exists), never from
client progress, so a refresh — or finishing from another browser — lands correctly. Required = **media_server +
tunarr** only (§6 "shortest honest path"); Seerr/AI/TMDB/filler report but never block. The three wiring checks
(`livetv`/`webhook`/`tunarr_library`) each belong to their own later step, so Connections doesn't double-count them.
**Step 1 Bootstrap** — `POST /v1/setup/bootstrap` with `bootstrapSchema` (already in core), then **auto-signs in with
the same credentials** (bootstrap issues no session) so the operator types the password once; a 409 isn't an error to
explain away, it's "this instance is past bootstrap → sign in". **Step 2 Checklist** — reuses the 13.2 `ChecklistItem`,
driven by `setup/status`, failures shown as the BE's plain-language hint + doc deep-link (no stack traces, ever).
**First-run routing:** `/` reads `setup.completed` and routes to `/wizard` until set — and **fails open** (a member's
403, a 500, a missing key ⇒ "completed"), so a non-admin is never trapped in operator-only setup.

**A real bug the tests caught:** the wizard computed its resume step before `me`/`setup-status` settled, so it painted
the wrong step and then yanked the operator forward (checklist → TV guide). Now it holds the paint until both settle
and lands right the first time. Gates: `make check` + `openapi-verify` GREEN; `make fe` GREEN (**129 tests**, +27 this
phase); Docker visual GREEN — **143 passing, 32 new baselines** (16 stories × 2 viewports), a11y clean on every new
component. **Next: 13.3c** (steps 3–7: livetv-connect, webhook handshake, tunarr-connect, user import, first channel)
then **13.3d** (gate: wizard e2e smoke vs mocked BE + a page snapshot per step).

**Phase 13.3a — auth foundation on TanStack Router (2026-07-18).** Branch `feat/fe-auth-13.3a`. The FE identity
layer the wizard + product surfaces sit behind (§11, §12) — built, then **the router was swapped react-router →
TanStack Router** mid-branch (maintainer call, doc-first) before more surfaces land, so 13.3a arrives already on
the typed router.

**Router swap (doc-first §14/§12, frontend-design, frontend-build-plan):** `react-router` v6 → **`@tanstack/react-router`
(file-based)**. Rationale in §14: end-to-end type-safe routing (typed params/search/links) matching the orval-contract
ethos; shares the TanStack Query client via router `context`; loader-based auth guards (`beforeLoad` → `redirect`, no
guard-flash). Web-only — routing was always the per-platform seam (mobile keeps Expo Router). `@tanstack/router-plugin`
(Vite) + `-cli` (`tsr generate`) generate `src/routeTree.gen.ts` — **gitignored + Biome-ignored + regenerated by
`pnpm codegen`**, exactly like orval output (never hand-edited). Route tree is `src/routes/`: `__root` (carries the
queryClient context), public `login` + `wizard`, and a pathless **`_authed`** guard layout whose `beforeLoad`
`ensureQueryData(meQueryOptions)` throws `redirect({to:"/login", search:{redirect: location.href}})` on 401 — the app
shell + all stub screens hang off it. `main.tsx` builds `createRouter({routeTree, context:{queryClient}})` + the
`Register` module augmentation (global type-safe Links).

**Auth pieces:** `useAuth` — the one interpreter of `GET /v1/auth/me` via shared `meQueryOptions` (`retry:false`; a 401
is a definite answer), narrowing the success|error union by status → `{user, isAuthenticated, isAdmin, isLoading, error}`
(feeds AppShell name/role). `RequireAuth` **deleted** — the guard is now `_authed`'s `beforeLoad`. `LoginForm` —
presentational (RHF + `zodResolver(loginSchema)` from `packages/core`, shared verbatim with mobile), block-level failure
via `ErrorState` (RFC 7807 → words), field errors inline, no user enumeration. `login` route wires `authApi.useLogin`
(invalidate me + `history.push` to the typed `redirect` search param on success; `beforeLoad` bounces an already-authed
visitor). AppShell `NavLink` → TanStack **`Link`** (active state via `data-status="active"`, so it stays a pure-className
component) + a footer sign-out; `_authed` layout feeds the real user + logout (`authApi.useLogout` → `queryClient.clear()`
→ `/login`). **Kept 1:1:** `LoginForm.onSubmit` takes generated `LoginInputBody`, identity reads `MeBody`. `Placeholder`
moved out of `src/routes/` (file-based = every file is a route) → `components/loomarr` (+ story; its "Dead air" label was
`text-static-500` at 2.94:1 — bumped to `static-400` so the newly-covered a11y gate passes, per the 13.2 precedent).

**Test/story router harness** (the swap's one real cost): TanStack `Link`/route hooks need a RouterProvider even in
isolation — added `RouterHarness`/`withRouter` in `test/story-utils` (a minimal in-memory router over the nav paths).
Coverage: a **router-level `app-router.test`** drives the REAL generated tree — signed-out → login form, authed → screen,
sign-in → Channels (replaces the old login + require-auth component tests, preserves §19 negative-auth). `make fe` GREEN
(codegen now generates the route tree before typecheck; 77 units); Docker visual GREEN — 6 `loginform` + 2 `placeholder`
baselines added, 4 `appshell` regenerated (Link renders pixel-equivalent; sub-pixel delta only). **Next: 13.3b/c** (the
7-step wizard) then **13.3d** (wizard e2e smoke vs mocked BE + a page snapshot per step).

**Contract 1:1 hardening — BE proposal typing + FE de-duplication (2026-07-18).** Branch
`feat/fe-contract-1to1`. A maintainer catch ("is anything hand-written that shouldn't be?") — audited the
FE against the generated client and found several hand-mirrored types (design §12 = no hand-written glue).
**BE:** `ProposalDTO.proposal` was `json.RawMessage` (→ OpenAPI `unknown`); now typed as `suggest.Proposal`
directly (imported the domain struct, dropping the old local `proposalBody`/`lineupItem`/`acqItem` mirrors),
so orval generates the full `Proposal`/`ProposalItem`/`Scores`/`ChannelPolicy` schema — true 1:1. **FE:**
deleted the mirrors — `Clip`/`ClipKind`/`ClipAudience` → `ClipDTO*`, `ProposalView`/`ProposalItemView` →
generated `Proposal`/`ProposalItem`, `ProblemDetail` → `ErrorModel` (deleted `mutator.type.ts`),
`ProvisioningState` → `TitleDTOState | "drift"` (5 states from the generated union + the one FE-only state).
Kept the genuine FE view models — the ⌘K `PaletteScope` (renamed from the colliding `SearchScope`) and the
derived `ChannelHealth` rollup — now documented as such. Forced a real honesty fix: generated
`ProposalItem[]` is `| null` (nil Go slice), so ProposalReview now null-guards. `make check` (BE incl. the
policy round-trip integration test) + `make fe` + the Docker visual suite (104/104, renders unchanged) all
GREEN; `api/openapi.yaml` regenerated + committed. Docs: frontend-build-plan §3 corrected.

**Phase 13.2b/c/d — remaining components + Storybook adoption (2026-07-17).** Branch
`feat/fe-gallery-visual-13.2`. **13.2b DONE:** the remaining Layer-2 components (IntentInput,
GenerationProgress, ProposalReview, PodTimeline, ClipCard, ApprovalQueueItem, SearchCommand) + a shared
`Badge` primitive + `formatClipDuration` (sub-minute-aware) — all with CVA/tokens, states, a11y, tests;
`make fe` + `make check` were GREEN before the Storybook pivot.
**DECISION (maintainer, doc-first): adopt Storybook 10 for the component gallery/workshop AND
visual/a11y testing, replacing the hand-rolled `/__gallery` registry.** This reverses frontend-design §5's
original mechanism, so the docs were updated FIRST (per prime directive #1): frontend-design §3/§4.1/§4.2/
§5/§7, design §14 (new dep row + rationale; **Chromatic rejected** — hosted SaaS breaks the offline rule),
frontend-build-plan §4/§9/§10, CLAUDE.md command contract. **Rationale:** CSF is the industry-standard
component contract + a real dev workshop (controls/autodocs/a11y panel), and it carries to the future
mobile app via `@storybook/react-native` (Expo, on-device). **Every §5 guarantee preserved:** offline
(`storybook-static`), deterministic (Playwright Docker, `document.fonts.ready`, `prefers-reduced-motion`
+ `animations:'disabled'`, frozen clock), committed baselines (`toHaveScreenshot` `maxDiffPixelRatio 0.001`),
and 100%-coverage-enforced (a test maps the component barrel → stories). a11y: `@storybook/addon-a11y` in
the workshop + `@axe-core/playwright` in the *same* Playwright pass over `storybook-static` (one browser
layer for pixels + axe; `addon-vitest` deferred as an optional future for play/interaction tests).
**Mobile-ready:** deterministic fixtures move to
a shared `packages/fixtures`, data contracts to `packages/core`, so web + future RN stories share args.
**Built:** Storybook 10 + addon-a11y; the hand-rolled registry replaced by 52 co-located CSF stories across
15 components (exports-at-end — the maintainer rejected exempting stories from the `no-inline-export` plugin,
and Storybook 10 indexes `export { … }` + `export default meta` fine); `packages/fixtures` + core contracts;
the story-coverage test. **The a11y gate immediately caught + fixed three real WCAG bugs** the earlier
components shipped: informational `text-static-500` (2.94:1, §2.1 says decorative-only) → `static-400`;
`opacity-70` on a denied approval card compositing all its text below AA → removed; a `<li role="alert">`
breaking the `<ol>` list structure → moved to a sibling `<p role="alert">`. **Visual determinism (the hard
part):** `make fe-visual`/`-update` now run Playwright **inside the pinned `mcr.microsoft.com/playwright`
image** (the reference rasterizer, §5.2) — Chromatic still rejected — reusing the host's JS-only node_modules
+ the image's browsers (no in-container install, host binaries untouched). Getting a stable green took three
fixes beyond the doc's kit: `animation: none` (reduced-motion only fast-forwards, leaving infinite spinners
on a random frame), **element-scoped** snapshots (`#storybook-root`, not the centered page whose fractional
margins shift text AA), and **retries** for residual sub-pixel jitter (a real diff still fails every attempt).
**104 `*-linux.png` baselines committed** (non-Linux suffixes gitignored). `make fe` + `make check` GREEN;
the Docker visual suite passes (flaky-on-retry ≤2/104). frontend-design §5.1/§5.2 updated to match.

**Phase 13.2a — design-system foundation: components, conventions, Biome (2026-07-17).** Branch
`feat/fe-design-system-13.2`. The Layer-2 component vocabulary + the codebase conventions + linting that
everything downstream (wizard, surfaces) builds on. **Self-hosted Geist** (@fontsource-variable, bundled
by Vite — visual-test determinism, §2.2). **shadcn primitives** (Button/Card/Input/Label, restyled via
tokens only). **Layer-2 components** (frontend-design §3): StateBadge, OnAirIndicator, NowNextStrip,
ChannelCard, EmptyState, ErrorState (RFC7807 renderer), ChecklistItem, AppShell — each with CVA/tokens,
all states, a11y (sr-only status, sentence-case DOM text so SR reads words not letter-spaced shouting).
**Maintainer conventions established mid-build (saved to auto-memory [[fe-code-conventions]])** and applied
across the WHOLE tree (app + packages, orval-generated excepted): (1) arrow-function expressions, (2)
exports in a single block at end of file, (3) folder-per-component/module with `name.tsx` + `name.type.ts`
+ `name.test.tsx` + `index.ts` barrel, (4) types isolated in `*.type.ts`, (5) barrel imports, (6) a
Vitest+Testing-Library test per module. **Biome** (maintainer's call over ESLint) for lint+format with
sensible settings, wired into `make fe` + new `fe-lint`/`fe-lint-fix` targets; Tailwind-v4 CSS excluded
(Biome's CSS parser can't read @theme/@custom-variant), `noLabelWithoutControl` off (false-positive on the
Label primitive), `useSortedClasses` on (Tailwind class sort in cn/cva/clsx — kills visual-diff churn).
**Two custom Biome GritQL plugins** (maintainer chose the Biome-native path over ESLint-hybrid/ts-morph;
current API verified via web docs since training predates the stable release) enforce the rules Biome
lacks built-ins for: `no-function-declaration.grit` (arrow expressions only) and `no-inline-export.grit`
(declare-then-export-block — catches `export const|let|var` + `export type X =`; `export function` falls
to the first plugin). Known GritQL gap (documented in-plugin): Biome 2.5 doesn't match TS
`export interface|class|enum` nodes, but the `*.type.ts` convention already routes types into export
blocks. A `no-raw-hex-color` plugin (enforce the §2 token layer) was prototyped but dropped — GritQL
regex doesn't reliably scan string-literal contents; noted as a follow-up. **Toolchain fix:**
`vitest@2→3` to dedupe `vite` to v6 (the v5/v6 skew broke the config types). Tests: **28 app + package
tests, all green** (StateBadge/OnAir/ChannelCard/EmptyState/ErrorState/ChecklistItem/AppShell + button/
card/input/label + Layout; core events/format/schemas; api mutator; tokens palette/contrast). `make fe`
GREEN (biome + codegen + typecheck 4 pkgs + tests + embedded build), `make check` GREEN. **Deferred to the
next PR (13.2b–d):** the remaining Layer-2 (IntentInput, GenerationProgress, ProposalReview, PodTimeline,
ClipCard, ApprovalQueueItem, SearchCommand), the `/__gallery` registry, and the Playwright visual + axe
harness.

**Phase 13.1 — FE workspace skeleton + token pipeline (2026-07-17).** Branch `feat/fe-scaffold-13.1`.
The greenfield frontend foundation (`frontend-design.md` §2.5/§4, §7 "Phase 1"): a pnpm monorepo under
`web/` with the shared packages the future Expo app bolts onto. **packages/tokens** — the Test Card
palette as the TS source of truth + a deterministic generator emitting `theme.css` (Tailwind v4 @theme),
a NativeWind-ready `tailwind-preset.cjs`, and `tokens.json`; its **contrast gate reproduces the design's
published WCAG ratios to the decimal** (onair base 4.01→-300 4.53, suggest 3.86→4.65) and fails the build
on any on-tint regression (§2.1). **packages/api** — orval generates typed TanStack Query hooks from
`api/openapi.yaml` (incl. the 6 routes 13.0 rescued — `useLogin`/`useMe`/`useBootstrap` now exist),
namespaced per tag, over a shared fetch mutator (same-origin, cookie creds, `X-Loomarr-Csrf`, RFC7807 →
`ApiError`). **packages/core** — the SSE invalidation bus (maps the BE's `title`/`channel`/`suggestion`/
`llm_pull` frames → coarse query invalidation, the §8 "GET is truth on reconnect" contract), zod schemas
(intent/bootstrap/login — reused by RN later), formatters. **apps/web** — Vite + React 18 + Tailwind v4 +
shadcn (new-york) with the AppShell nav rail, react-router skeleton, QueryClient + SSE providers, sonner.
The build embeds into the Go binary: **internal/web/embed.go** (`//go:embed all:dist`) serves the SPA at
`/` (SPA fallback for client routes; API prefixes guarded to 404, never HTML) with a committed
`.gitkeep` so `go build` works Go-only (serves a "run make fe" notice). Makefile gained
`fe`/`fe-tokens`/`fe-tokens-verify`/`fe-codegen`/`fe-install`. **Verified:** `make check` GREEN (added
SPA-served + guard assertions to `TestWiring_FreshInstall`; the absent-route contract shifted 405→**404**
uniformly via the SPA guard — invariant "absent ≠ 501" preserved), `make fe` GREEN (codegen + typecheck
4 pkgs + tokens/core unit tests + build), `fe-tokens-verify` GREEN. Toolchain decision recorded: **Node
20+/pnpm/Vite** (not bun/deno) — §14-decided, keeps the Expo bridge + CI determinism. Self-hosted Geist
deferred to 13.2 (visual-determinism concern; token font-stack falls back meanwhile). orval output is
gitignored (regenerated from the spec by `make fe`); token artifacts are committed (drift-checked).

**Phase 13.0 — BE contract closed for the FE (2026-07-17).** Branch `feat/be-contract-13.0`. The
prerequisite before any FE code (see `docs/frontend-build-plan.md`): make every route the FE calls
present + typed and every wizard/workspace surface fully BE-backed. Doc-first §7/§8/§13 updated, then:
**(1) OpenAPI coverage** — the 6 routes orval couldn't type (`auth/login|logout|me`, `setup/bootstrap`,
`users/import`, `users/sync`) were absent from the exported spec because `ExportOpenAPI` builds a bare
`Server{}` and the `register*` funcs nil-guard; added a `schemaOnly` flag (set only by export) so their
SCHEMAS emit into `api/openapi.yaml` while runtime nil-guarding is untouched (+296 lines, all additive).
**(2) setup/status completed** — was only `livetv` + `tunarr_library` (its own handler admitted "added by
their phases"); now aggregates the connection probes (`media_server`, `requester`, `tunarr`, `llm` incl.
local reachable+pulled / hosted key-present, `tmdb` via a cheap known-id lookup, `filler` when
configured) reusing the §8 `setup/test` registry, plus the `webhook` handshake check carrying per-app
`sonarr`/`radarr` `lastReceived` (read from the KV the ingest handler writes), each with a `docHref`
Troubleshooting deep-link. Added `llm`+`tmdb` probes to `connectionTests`. **(3) Suggester progress
events** (maintainer chose real per-step over indeterminate) — the worker published nothing intermediate;
now `Suggest` reports `searching`→`reasoning`→`scoring` via a **context-threaded** `ProgressFunc` (zero
signature churn across 13 call sites; a bare ctx = no-op), and the worker emits `done`/`failed` around it
through a narrow `ProgressEmitter` wired in the composition root to publish SSE `type=suggestion` frames
`{jobId, phase}` (parallel to `llm_pull`; the `eventEmitter` gained `SuggestionPhase`). Tests:
`TestProgress_ReportsOrderedPhases` (unit — clean run emits exactly searching→reasoning→scoring),
`TestSetupStatus_FullChecklist` (integration — connection checks carry docHref, webhook check waits with
empty lastReceived). `make check` GREEN; `make openapi` regenerated (commit makes `openapi-verify` green).
Closes findings 1–4 of the FE↔BE audit; finding 5 (coarse drop-tolerant SSE) is a documented property.

**Phase-13 FE plan reviewed vs the live BE — 5 seams found, plan written (2026-07-17).** Before writing any
FE, audited `frontend-design.md` + `design/` prototypes against the real BE contract (32 typed ops + 8
raw routes + the 3-type `title`/`channel`/`job` SSE bus). Found **5 FE↔BE seams**: (1) 6 core routes —
`auth/login|logout|me`, `setup/bootstrap`, `users/import`, `users/sync` — are **not in the exported
`openapi.yaml`** so orval can't type them; (2) `GET /v1/setup/status` returns only `livetv` +
`tunarr_library`, **not the full §13 checklist** (its own handler comment admits "added by their
phases" — they weren't); (3) **no read surface for webhook handshake timestamps** though the store
tracks them (`store.go:102,112`); (4) the **suggester emits no per-step progress** (worker publishes
nothing intermediate) so the mock's `GenerationProgress` steps are unbacked; (5) SSE is coarse +
drop-tolerant (a design property, not a bug — FE must invalidate-and-refetch). Also confirmed the design
is **already mobile/Expo-aware** (`frontend-design.md` §2.5/§4.2: shared `packages/{tokens,api,core}` +
token→NativeWind preset bridge; Expo+NativeWind+RN-Reusables pre-decided). **Maintainer decisions:**
(a) close the contract **first** as a **13.0 PR**; (b) mobile **ready now, build web only** (Expo app is
a future phase + a §14 update); (c) **add real per-step suggester progress events**
(`searching/reasoning/scoring/done/failed`). Full sequenced plan (13.0 close-contract → 13.1 monorepo +
tokens → 13.2 gallery + visual harness → 13.3 wizard → 13.4 surfaces → 13.5 gate), the page→endpoint→SSE
coverage map, and the mobile-auth flag (**native app needs CORS + per-user bearer tokens — a §11/§14
conversation, out of scope for 13**) live in **`docs/frontend-build-plan.md`**. No code yet; next action
is the 13.0 BE PR (doc-first §13/§8).

**Tunarr media-source auto-wiring (tunarr-connect) — onboarding gap closed (2026-07-16).** Branch
`feat/tunarr-autoconnect`. A live-smoke question ("is the Tunarr↔Emby wiring in onboarding?") found
a real gap: the design *required* Tunarr to have the media server as *its* source, enabled+scanned
(§6, Phase-0 #12), but nothing in the wizard did it — an operator could finish all-green yet get
**dead-air channels** (empty Tunarr program table). Maintainer chose **full automation**. **Design
win proven live:** Tunarr accepts Loomarr's **admin API key** as the source access token (verified
vs Tunarr 1.3.8 + Emby 4.10) — so **no Emby user login, no new credential, no §15 expansion**.
Delivered (doc-first §6/§7/§13): `programmer` gains `EnsureEmbySource`/`ConnectLibraries`/
`MediaLibrariesReady` (media-source CRUD + enable/scan of the movie+show libraries, idempotent);
`setup.MediaSourceConnector` orchestrates (resolves the admin userId via `library.ListUsers`, live
flavor/url/token); `POST /v1/setup/tunarr-connect` (admin, idempotent) + a `tunarr_library` check in
`/v1/setup/status`; wired in `internal/app` (the connector uses the same programmer the tests inject,
via a media-source interface type-assert); testkit Tunarr double implements the interface. Tests:
programmer unit (create-then-reuse idempotent, movies+shows-only enable, ready-gate), integration
`TestJourney_TunarrConnect` (real connector → double: connect → 2 libs → `tunarr_library` flips →
idempotent re-run), member-403 matrix + fresh-install 501 extended. **Dogfooded LIVE:** POST
tunarr-connect against real Tunarr returned the existing source **idempotently** (librariesEnabled=2)
and `/v1/setup/status` flipped `tunarr_library: ok=true`. `make check` green.

**Live BE smoke on the Mac — 2 findings (2026-07-16).** Branch `fix/picker-prober-live`. First
real drive of THIS session's changes against the homelab: native Ollama (qwen3.5:9b) + Emby/Seerr
over Tailscale, a FRESH app boot configured through the settings API (the wizard path, not env
pins). **Proven live:** the live-enable fix (fresh install `POST /v1/suggestions` → 501; PATCH
connections → **200 with no restart**); the real connection probes (`media_server` ListUsers +
the NEW `requester` `Seerr.Reachable`, both ok over Tailscale); features flip live. **Finding 1
(FIXED):** the model-picker's Prober base URL was **frozen at boot** (`llm.NewProber(set.str("llm.url"))`),
so configuring `llm.url` via the wizard left the picker reporting `reachable:false` / `pulled:false`
until a restart — the same class as the live-enable gap, and the integration harness missed it
because it seeds `llm.url` BEFORE build. Fix: `systemLLMService` builds the Prober per-call from a
live `ollamaBase()` resolver (like the suggester's Swappable hot-swap); regression added to
`TestWiring_ConfigEnablesLive` (picker reachable after PATCH); **verified live** (reachable=true,
qwen3.5:9b pulled=true after the PATCH). **Finding 2 (model-quality, documented — NOT a code/seam
bug):** qwen3.5:9b (the catalog's top-recommended local model) emits conversational prose instead
of the final JSON proposal (`invalid character 'C'`), so a real grounded job fails the JSON-repair
loop. `Think:false` is already applied on tool turns and the job failed **cleanly** (the graceful
"no valid proposal" path, which is unit-tested) — the SYSTEM operated as designed; the MODEL is the
weak link. Actionable: prefer a known-good local model (qwen3:8b / qwen3:14b Q6_K / llama3.1:8b) or
a hosted model; the catalog's qwen3.5:9b recommendation warrants review; a possible follow-up is a
final-turn `format:json` (tools dropped) to coerce weak models (doc-first, §8 grounding — separate).
**Phase 2 (a real Tunarr channel) not yet run** — needs the dev Tunarr wired to Emby (maintainer creds).

**E2E integration seams + composition-root testability + live-enable fix (2026-07-16).**
On branch `feat/e2e-integration-seams`. Pre-FE hardening: drive the WHOLE app (real composition,
not a hand-wired subset) through every FE-facing flow so the frontend meets a seam-free backend.
**Composition seam:** extracted `run()`'s 260-line wiring body into an importable `internal/app`
package — `app.BuildHandler(ctx, st, log, Overrides) (http.Handler, error)` — that both `run()`
(production) and the tests call, so tests exercise the REAL `api.Options` wiring. `cmd/loomarr/main.go`
shrank 710→133 lines (thin entrypoint); the package is split by concern (`app`/`systemllm`/
`settingsadapter`/`settingsboot`/`filler`/`adapters`/`emitter`/`ids`). `Overrides` injects the two
in-process boundaries (Tunarr `programmer.Programmer`, scripted `llm.Provider`) + a TMDB base override;
library/seerr are real adapters over testkit HTTP doubles via seeded settings. **New testkit
`Ollama`** HTTP double (`/api/version`,`/api/tags`,`/api/pull` stream) so the §8.1 picker
(probe→select→pull + SSE) runs through the real `systemLLMService`+`Prober`. **New E2E suite**
(`internal/integration`, real `app.BuildHandler`, testkit-only, in `make check`): a **new-admin
journey** (bootstrap→409-on-2nd→**local bcrypt login**→settings/feature-gates→`/setup/test` real
probe→picker probe/select(409-unpulled)/pull→intent→approve→channel-with-policy-enforcement→reconcile-
idempotent), a **member journey** (real import→media-server login→allowed set→**403 across the FULL
admin matrix** incl. settings/system-llm/setup/filler/backup that the old §19 test omitted→disable-
kills-session), and a **wiring** file (fresh-install 501/405 nil-dep matrix + the hot-apply proof), plus an **SSE
E2E** test (authenticated subscribe → pull → assert an `llm_pull` frame arrives — the FE's
live-update channel, previously only 401-tested). **Two pre-FE gaps a self-audit found, closed:**
(a) the wizard's "Test Seerr" button had NO backend — added `Seerr.Reachable` (validates URL+key,
no side effects) + the `requester` check in `connectionTests` + a testkit `/settings/main`
endpoint; the admin journey now drives all three probes (media_server/tunarr/requester); (b) the
SSE delivery test above.
**Live-enable fix (honors config-design §3 / §8.1 "no restart"):** the audit-flagged gap — a saved
connection flipped the `features` map but its route stayed **501 until restart** (services were
nil-wired at boot). Fixed by **always-constructing** the feature services (reconciler/channels/
suggester/filler, given a store) with the existing dynamic per-call providers, and moving each
handler gate from `s.X == nil` to a live check — `featureOff(ctx, feature)` (Features() snapshot) for
suggestions/filler, `unconfigured(key)` (live `set.str`) for search/channels/livetv, picker always-on.
The gate is **additive** (`nil OR live-off`), so the api-package unit tests (which wire deps directly,
no live source) are untouched. `TestWiring_ConfigEnablesLive` PROVES a PATCH to `/v1/settings` enables
`/v1/suggestions`+`/v1/search`+reconcile **with no restart**. **Known caveat:** the library *flavor*
is fixed at construction (defaults to Emby), so switching to Jellyfin still needs a restart — url/token
hot-apply; follow-up is a live flavor closure (~15 auth call sites). Gates: `make check` (`-race`, lint
0, config-docs) + `make test-pg` + boot smoke (fresh-install bootstrap 200, `/readyz` ready, clean
shutdown) all green. NOT a phase — pre-Phase-13 hardening; unblocks the FE build on a proven backend.

**LLM provider surface + pull-path fixes + Mac/Linux dev portability (2026-07-16).** Live dev
bring-up on an Apple-Silicon Mac surfaced two §8.1 pull bugs and drove a provider-surface decision
(all `make check` green). **Fixed:** (1) a model **pull aborted at 120s** — `Prober.Pull` used a
whole-request `http.Client.Timeout` (`TimeoutLLM`) that kills a multi-GB stream mid-body; added
`httpx.NewStreaming()` (no whole-request budget, ctx-governed; connect/TLS/header stages still
bounded) + regression test. (2) **pull progress now surfaces raw bytes** — exported
`llm.PullProgress{Status,Completed,Total}`; the `/v1/events` `llm_pull` SSE frame carries
`completed`/`total` so the FE renders "X of Y GB" + derives ETA (was percent-only). **Design
decision (doc-first, §8/§8.1):** the hosted LLM surface narrows to **OpenRouter** (the blessed
aggregator — one key → every frontier family) + **Custom** (a user-supplied OpenAI-compatible base
URL, gated by live validation, not an allowlist); the curated openai/anthropic/groq/gemini entries
are dropped (reachable via OpenRouter or Custom). Family-tier ranking unchanged. **Dev:**
`compose.dev.yaml` is host-agnostic now (`platform: linux/amd64`, `MEDIA_SERVER_IP` override); NVIDIA
transcode is an opt-in `compose.dev.gpu.yaml` overlay (`make dev-gpu`). Verified live: app native vs
Emby+Seerr+TMDB (Matrix grounding), Ollama on Metal, the §8.1 picker (probe→pull→select). A
cross-cutting fix/refinement, **not a phase** — Phase 13 (Web UI) is still next.

**Auth/identity rework (§11) — COMPLETE (2026-07-15).** On branch `feat/auth-rework` (commits
`4879470`..`4af00e2`), NOT yet merged to `main`. Replaced the claim-on-login / lazy-self-provision
model with **Loomarr-owned identity**: the `users` table is the source of truth + allowlist. Gate:
`make check` (`-race`, lint 0) + `make test-pg` (migration `00009` on both dialects) + `openapi-verify`
+ `config-docs-verify`, **plus a live boot smoke with ZERO media-server config** — `POST /v1/setup/bootstrap`
created the owning admin, a 2nd call 409'd, local admin login returned an HttpOnly/SameSite=Strict session
cookie, wrong password 401'd, and the users table held exactly one row (admin, bcrypt hash set). Delivered:
migration `00009` (nullable `users.password_hash` — set ⇒ local/bcrypt user, null ⇒ imported media-server
user, the credential-path discriminator); `login.go` enforces the allowlist (a name+hash verifies in-app,
else verify vs the media server AND confirm the id is imported — an un-imported user is **rejected even with
valid Emby creds**, no lazy provision; all failures return one `ErrInvalidCredentials`, no user enumeration;
works with a nil media server = local-only); `Provisioner.Bootstrap` (first local admin, once via
`CountAdmins()==0`) + `Provisioner.Import` (explicit media-server ids, admin-only, the ONLY add path);
`POST /v1/setup/bootstrap` (unauthenticated, self-gated) + `POST /v1/users/import` (admin-only);
`store.GetUserByName`. **Closed BOTH lazy-provision hatches:** login (`syncUser` add-branch removed) AND
periodic sync (`UserSync.Sync` now skips un-imported users — it refreshes, never adds, else a sync would
silently re-import everyone). `bcrypt` promoted to a direct dep (§14 updated). Existing auth/flow tests
updated to seed the allowlist first (a stricter contract, not weakened). Reworked doc §11 + reconciled
§13 wizard (Claim→Bootstrap + Import steps), §16, §19 test spec, §21 phase-9/13 gate text. Supersedes the
deferred `loomarr-auth-rework` memory item. Unblocks Phase 13's wizard "Bootstrap" + "Import users" steps.

**Settings subsystem — cross-phase config retrofit — COMPLETE (2026-07-15).** Built `config-design.md`
for real (the deferred Phase-1/8/9 config work) on branch `feat/settings-subsystem` (commits
`7aa3fcc`..`17fe3cb`). Gate: `make check` (`-race`, lint 0) + `make test-pg` (settings audit columns on
both dialects) + `make openapi-verify` + `make config-docs-verify` all green, **plus a live boot smoke**
(temp SQLite): `/healthz` 200, `/readyz` ready, three generated secrets minted + persisted with audit
stamp, `GET /v1/settings` 403 unauth / 47 settings with the API_TOKEN break-glass (secrets **masked**,
value withheld), env-pin reported + **refused** on PATCH ("set via environment"), `job.workers` hot-applied
to db, and the feature gate flipped `acquisition` true the instant `seerr.url` was saved — all with **no
restart**. Delivered: a typed **registry** (single source of truth, ~45 keys transcribed from §15),
`env > database > default` resolution with **asymmetric errors** (bad env → boot fail; bad db → self-heal +
caution), `_FILE` secret loading + `<VAR>`+`_FILE` ambiguity boot-error, in-memory snapshot + `Watch`
**hot-apply**, the secrets lifecycle (idempotent gen, `Redactor` into slog — the **log-grep gate** proves
no secret is ever logged, masked reads, regen side-effects), feature gating from `RequiredFor` (the
requester OR-gate is the one explicit case), the `/v1/settings` + `/v1/setup/test` + secrets-regenerate
API, and `make config-docs` (→ `docs/configuration.md`, drift-gated in `make check` too). New
`internal/settings` package; `config.Config` shrunk to the env-only bootstrap set (§1: `DATABASE_URL`/
`AUTO_MIGRATE`/`LISTEN_ADDR`/`LOG_LEVEL`/`TZ`). **Full read-through rewire** — every consumer resolves via
the snapshot (library/requester/Tunarr connection providers read PER CALL; `reconcile`/`channels` runners
gained `WithInterval` re-tune; the LLM `Watch(llm.*)`-rebuilds). Migration `00008` adds settings
`updated_at`/`updated_by` (2nd real ALTER after `00007`). Closes the ChannelPolicy registry-default
deferral: the `SCHED_*`/`SEASONAL_MODE` policy defaults (§15) now resolve through the registry, not Go
constants. Like ChannelPolicy, a **cross-phase retrofit** (deepens Phase 1/8/9), not a new phase-table row.
**Unblocks Phase 13's wizard-as-settings** (`config-design.md` §5–§7). NOT yet merged to `main` (branch
awaits review). Known small follow-up: `Router`/`ExportOpenAPI` still duplicate the route-registration
list (a shared `registerAll` is the real fix); the media-server/tunarr connection Test probes are shallow
reachability checks.

**Phase 12.5 — End-to-end integration (the seams) — COMPLETE (2026-07-14).** All live-smoke seams
closed: #6/#7/#8/#12/#13 (earlier), then #9 (acquisitions→`ch.Lineup` pending), #10 (provisioner→
scheduler `eventEmitter`), #11 (`/v1/events` SSE), and the §10 filler redesign (Loomarr-owned
commercials via a Tunarr `local` source + per-channel filler-lists). The Emby ~4s Live-TV playback
stop was a **Firefox** client quirk (no code change; troubleshooting note added). Phases 0–12.5 done;
**next: Phase 13 (Web UI + onboarding — recreate the imported `design/` prototypes pixel-perfect in
Vite+React+Tailwind+shadcn; gallery + fe-visual + axe gates).** Real captures earlier: Ollama tool-use
+ Emby SearchTerm shape.
Remaining follow-ups (non-blocking): (a) ~~live TMDB capture~~ **DONE** 2026-07-13 (key supplied →
`fixtures/tmdb/*`; adapter confirmed correct; live grounding smoke passed); (b) Anthropic LLM
provider (opt-in); (c) Archive.org downloader live HTTP walk (sidecar manual-smoke, stubbed);
(d) carried from Phase 6 — Sonarr `import_webhook.json` fixture (28GB re-download; Sonarr webhook
conn id 3 left up to catch it — remove after). Phase-0 findings:
[`docs/engineering/phase-0-findings.md`](../phase-0-findings.md). Deferred captures:
Sonarr Grab/Download → Phase 6; Emby login success body → Phase 9.

## Visual corpus source-branch evidence (PR #982)

The source branch recorded the following private development evidence before delivery integration.
These captures and model runs were not repeated during backlog review.

Filler visual acquisition now has a rights-approved, privately materialized 120-case development
pool with 120 distinct Met creator, work, source-family, and exact-content identities. The first real
download exposed a Met CDN split in which `HEAD` advertised origin bytes while bare and shared-key
`GET` requests could return a differently sized cached representation. Both attempts failed closed
without a ledger. The adapter now freezes an exact `download=1` URL with a cache-isolation key derived
from the already frozen item-metadata SHA-256; the downloader's exact-byte rule was not relaxed. The
final schema-4 inventory is `3a663b77d675fa209cffb496a3f27505664989629b03f9f38cd8bf70fff49847`,
has the same 120 item/work/creator/metadata and representation facts as the original pool, and again
produced 120 mechanical rights passes with zero holds at
`d1e85d3e35ab6188200f4930b68f4740785f8b4cf041345072a0dc9d2b0e8896`. The maintainer's
development-only attestation is `848b8258c6f2606eb30221e6cd0f3cd6413697202c57b2f155b7db6bac57a039`;
the ordinary locker produced 120 item-bound approvals. The serial materializer downloaded and fully
decoded 120 JPEGs totaling exactly 292,769,745 bytes in 120 requests, with no duplicate content;
schema-3 ledger `9bda3154575635af954058d3f86a40744ad3820d5e91a9e4989e6546025e0152`.
Nomination preparation reopened every image and emitted 120 inert four-field rows; worksheet file
`04710cb604da6f28f4ff28b38ada65b95caa459bb1a79cf2b8fe9e3f8a70939c`, review CSV
`601889a02e80f4efa4fa34a91bb41159cd238a68ca2c2352683ca7d1f5285942`. No nomination, truth,
training, certification, provider transfer, production, ingestion, scheduling, or broadcast
authority exists.

A separate local model-assistance pass now covers all 120 exact Met images without editing that
worksheet. Marqo and Freepik completed 120/120 network-disabled inferences; Gemma 4 12B and Llama
3.2 Vision 11B completed two source-bound local VLM assessments per image. The conservative join
produced 47 two-VLM adult-positive proposals, 16 safe age-risk exclusions, two agreed no-visible-nudity
exclusions, and 55 targeted disagreements. Llama's single age-risk call versus Gemma's 15 establishes
that its adult-only output is too optimistic to clear a Gemma hold. Three Llama free-text generations
looped until the bound; a categorical-only recovery completed all three, so future assistance should
return closed fields and Loomarr-owned reason codes. Private proposal manifest file
`cc8d5fbde8d5d17222166e559abc49f32a8b06067d5f6ec8644dea8b87dda4e4`; internal digest
`3a2457ff1d4ebd8c84fabb2894dd401b31cb6aa92d9ab6953c87516513f6fc7c`. The batch could not honestly
supply 120 positives, so a stricter authorized 240-item Met refill was captured, rights-screened, and
materialized as 240 distinct images and 541,961,618 exact bytes. A real CDN mismatch exposed an unstable
`download=1` body; the downloader now selects the original-image route, assigns each Met GET a deterministic
per-run/case/attempt cache identity, and retries only exact representation-identity mismatch three times inside
the existing ceilings. Gemma covered all 240 refill images, Llama inspected its 93 clear candidates and
confirmed 91, and a
categorical Llama/Qwen 3.5 third-opinion pass examined only 20 high-signal unresolved cases. The refill yields
75 creator-independent positive proposals, 39 age-risk exclusions, 23 cross-batch creator holds, and 103
unresolved cases. Combined with the first 47 proposals, 122 independent candidates now exist—two reserves
above the 120-positive target. No model output entered either worksheet, and all truth, training,
provider-transfer, production, ingestion, scheduling, and broadcast authority remains false.
