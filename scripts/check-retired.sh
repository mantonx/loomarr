#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
RETIRED=(
  'check: check-static test|retired: use make verify SCOPE=all for comprehensive local verification'
  '.github/dependabot.yml|dependency automation is owned by renovate.json; do not restore the legacy bot configuration'
  # The first full-corpus inventory put Archive.org identity at the document root, making mixed
  # authority certification impossible. No completed certification artifact uses it; schema v2
  # binds authority to every case and the strict decoder rejects the old shape.
  '"source": "archive.org"|retired: filler corpus inventory schema v1 was replaced, not adapted; authority belongs on each schema-v2 case'
  'Source: "archive.org"|retired: do not restore the single-source filler corpus inventory type'
  # The Blender pilot could not produce ten distinct live trailer candidates without duplicate
  # encodes, full-film relabeling, or dead media. Individually cleared works belong in the static
  # cohort; a dedicated lane/target would recreate the disproven source assumption.
  'filler-corpus-blender|retired: Blender works may enter the reviewed static cohort, but Blender is not a qualified pilot lane'
  'LOOMARR_FILLER_CORPUS_PILOT_DRAFT|retired: the pilot lock consumes the exact committed lane artifacts directly'
  # Loomarr publishes Linux amd64/arm64 containers and does not support a native Windows server.
  # These names were assurance surfaces for that unsupported target, not product invariants.
  'windows-playout|retired: Loomarr has no supported native Windows server build'
  'windows-compile|retired: Loomarr has no supported native Windows server build'
  'impact_windows|retired: Loomarr has no supported native Windows CI gate'
  # The project moved from the maintainer's personal namespace to the lowercase Loomarr
  # organization. Old GitHub links can be reclaimed and old GHCR coordinates name the wrong
  # publisher, so source, release metadata, and operator instructions must use one identity.
  'mantonx/loomarr|repository identity moved to the loomarr organization; use loomarr/loomarr'
  # §10 V51b — four per-capability sweeps became one ingest pipeline. Their schedule keys are the
  # dangerous half: `docs/help/` ships inside the binary and is read as INSTRUCTIONS, so a page
  # telling an operator to tune `JOB_FILLER_VISION_SCHEDULE` sends them to set an env var nothing
  # reads, on a box where vision is now scheduled by `job.filler_pipeline.schedule`. That is the
  # exact shape the deleted `/hooks/arr` webhook left behind.
  'job.filler_language.schedule|V51b: the language gate is a rung of the ingest pipeline; use job.filler_pipeline.schedule'
  'job.filler_split.schedule|V51b: splitting is a rung of the ingest pipeline; use job.filler_pipeline.schedule'
  'job.filler_transcribe.schedule|V51b: transcription is a rung of the ingest pipeline; use job.filler_pipeline.schedule'
  'job.filler_vision.schedule|V51b: vision is a rung of the ingest pipeline; use job.filler_pipeline.schedule'
  'JOB_FILLER_LANGUAGE_SCHEDULE|V51b: replaced by JOB_FILLER_PIPELINE_SCHEDULE'
  'JOB_FILLER_SPLIT_SCHEDULE|V51b: replaced by JOB_FILLER_PIPELINE_SCHEDULE'
  'JOB_FILLER_TRANSCRIBE_SCHEDULE|V51b: replaced by JOB_FILLER_PIPELINE_SCHEDULE'
  'JOB_FILLER_VISION_SCHEDULE|V51b: replaced by JOB_FILLER_PIPELINE_SCHEDULE'
  # V55 made graph mutation one atomic store operation: the node, closure, rollups, and category
  # shadow commit together. A disabled repair job left correctness behind an operator toggle and
  # exposed two public half-operations that callers could run in separate transactions.
  'filler-reindex|V55: taxonomy graph edits synchronously rebuild closure and rollups in one transaction'
  'filler.reindex.enabled|V55: taxonomy consistency is unconditional, not an operator setting'
  'FILLER_REINDEX_ENABLED|V55: taxonomy consistency is unconditional, not an operator setting'
  'job.filler_reindex.schedule|V55: taxonomy consistency is part of the graph edit, not a scheduled job'
  'JOB_FILLER_REINDEX_SCHEDULE|V55: taxonomy consistency is part of the graph edit, not a scheduled job'
  'ReindexJob|V55: ApplyTaxonomyEdit owns the atomic set-based rebuild'
  'NewReindexJob|V55: ApplyTaxonomyEdit owns the atomic set-based rebuild'
  'ReindexStore|V55: the store exposes one semantic ApplyTaxonomyEdit operation'
  'ReindexResult|V55: there is no asynchronous taxonomy repair result'
  'RebuildClosure|V55: closure rebuilding is private to boot and ApplyTaxonomyEdit'
  'RebuildRollups|V55: rollup rebuilding is private to ApplyTaxonomyEdit'
  'ListClipHashesLeaves|V55: the scheduled taxonomy repair work list was retired with its job'
  'UpdateClipTags|V55: taxonomy writes and scalar classification updates now have separate owners'
  # ⚠ Not a rename: "how often do we go LOOKING for compilations" stopped being a question with
  # an answer, because every long recording reaches the split rung as it is ingested. An operator
  # told to raise this to split more often would be tuning nothing; the real bound is
  # `filler.pipeline.max_splits`.
  'filler.split.every|V51b: splitting is a pipeline rung, not a sweep; the bound is filler.pipeline.max_splits'
  'FILLER_SPLIT_EVERY|V51b: splitting is a pipeline rung, not a sweep; the bound is FILLER_PIPELINE_MAX_SPLITS'
  # V51b folded on-file loudness normalisation into the transcode rung. `NormalizeInPlace` had no
  # production caller at all — the capability existed and the setting that gated it was inert —
  # so a doc describing it as a separate pass describes something that never ran.
  'NormalizeInPlace|V51b: loudness is applied by the transcode rung, in the pass that is already re-encoding'
  # §10 V51e — Incoming became ONE conveyor. `asks` and `pipeline` were separate arrays over
  # overlapping populations, and on a fresh scan 84 of 85 clips appeared in BOTH: a row demanding
  # a decision above a row captioned "nothing here needs you". The names are the dangerous half
  # here for the same reason the schedule keys were — a doc or a comment that still says "the
  # asks list" sends the next reader looking for a field the response does not have, and the
  # honest answer (`clips`, with `needsDecision` per row) is one word away from it.
  'IncomingAskDTO|V51e: one belt, one type — IncomingClipDTO, with needsDecision saying which end a clip is at'
  'NonTerminalOnly|V51e: PipelineFilter.ConveyorOnly returns running AND review — the two halves of one belt'
  'body.Asks|V51e: the response carries clips; a clip appears exactly once, whichever end it is at'
  'hooks/arr|the inbound arr webhook was deleted; acquisition state comes from polling'
  'WEBHOOK_SECRET|never existed as a generated secret; generated credentials are API_TOKEN and PLAYOUT_TOKEN'
  # Sessions are opaque random database credentials, hashed at rest and resolved per request.
  # There is no cookie-signing configuration; preserving either spelling would recreate a control
  # that cannot revoke or validate any session. Historical migration rows may remain inert.
  'SESSION_SECRET|retired: opaque database-backed sessions do not use a signing secret'
  'session_secret|retired: opaque database-backed sessions do not use a signing secret'
  # Tunarr 1.3.8 has no authentication contract. Keeping either spelling in examples or the
  # prototype advertises an inert credential and implies Loomarr sends a header that it does not.
  'TUNARR_API_KEY|retired: Tunarr has no API-key configuration; configure only its URL'
  'tunarr.api_key|retired: Tunarr has no API-key setting; configure only tunarr.url'
  'capture-collections.sh|deleted; running the app against a real Emby answered every question it existed to ask (design §6 records the findings)'
  # The packaging question §10 says "keeps being re-decided": sidecar → opt-in tag → single
  # image. Both intermediate answers left instructions behind that read as current — a
  # Sources row literally labelled "ingest sidecar", and copy telling operators to switch to
  # an image tag that is not published. Exactly the docs/help failure this script exists for.
  'loomarr-ingest|the ingest sidecar was folded into the core (internal/clipfetch); there is no separate service or image'
  'loomarr:filler|the two-tag split was replaced by the SINGLE image (§16) — yt-dlp/ffmpeg always ship, so telling an operator to switch tags is a dead end'
  # V38b: clips arrive because you added a SOURCE, not because you pasted a URL. The panel was
  # the odd one out once Sources had registration, per-row search, pulls and auto-fetch — and
  # leaving its name in help text would send an operator hunting a box that is not there.
  'IngestPanel|the paste-a-URL box was retired (V38b); clips arrive from a registered source — add one under Filler → Sources'
  # V41: CONTEXT.md defines the artifact as a PROPOSAL and explicitly bans "suggestion", but the
  # routes said /v1/suggestions and one operationId (submit-suggestion) sat among five
  # *-proposal siblings in the same file. The paths moved; this keeps the old ones from coming
  # back in help text an operator would follow to a 404.
  'v1/suggestions|renamed to /v1/proposals (V41) — CONTEXT.md defines the artifact as a Proposal and bans "suggestion"'
  # V47b: renamed the playout "doctor" to "playout status" — same read-only health projection,
  # clearer name. The old operation id and path must not survive in help text an operator would
  # follow to a 404.
  'get-playout-doctor|renamed to get-playout-status — same read-only playout health projection'
  'playout/doctor|renamed to /v1/playout/status — same read-only playout health projection'
  # V48: the playout copy-audience query changed from ?target=browser|mediaserver to
  # ?plan=baseline|hevc8|hevc10|full (a client DeviceProfile resolves to an EncodePlan). The VALUE
  # tokens are the retired identifiers — the bare word "target" survives as the SessionStat/health
  # DTO field by design, so only the `target=browser`/`target=mediaserver` query strings are banned.
  'target=browser|the playout copy-audience query is now ?plan= (V48); browser → ?plan=baseline'
  'target=mediaserver|the playout copy-audience query is now ?plan= (V48); mediaserver → ?plan=full'
  # The Go block supervisor owns finite Airing advancement and format acknowledgement. Restoring
  # either identifier recreates the anonymous media-tool sequencing seam that stalled at boundaries.
  '/v1/playout/playlist|retired: the block supervisor opens /v1/playout/program directly'
  'ConcatArgs|retired: BlockSpawner and BlockMuxArgs own finite block advancement'
  # Live TV wiring stopped being an operator ACTION: it is idempotent and fully derived from the
  # Tunarr connection, so it auto-runs on a Connections save (settings.go autoWireAfterSave) and a
  # manual endpoint would be a redundant no-op. The route was deleted; five documents kept telling
  # operators to call it, including the wizard walkthrough and the §7 route table — the exact
  # "docs/help ships as instructions" failure this script exists for. ⚠ NOT the same thing as
  # /v1/setup/livetv-reconnect, which is the force-re-wire for a stale channel→stream binding.
  'setup/livetv-connect|Live TV wiring auto-runs on a Connections save (settings.go autoWireAfterSave); there is no manual route. The force-re-wire is /v1/setup/livetv-reconnect'
  # V50a: the primitive vendor moved Radix → Base UI (design §14). Both are headless React
  # libraries with near-identical part names, so a copy-pasted snippet or a re-added dependency
  # would look ordinary in review while quietly pulling a second vendor back into the tree — which
  # is precisely what the consolidation bought. `asChild` rides along because it is the one API
  # that cannot survive the move: Base UI composes through a `render` PROP, so a prop still named
  # for merging onto a CHILD is the half-migrated vocabulary that outlives whoever reintroduced it.
  '@radix-ui|the primitive vendor is Base UI since V50a (design §14) — import from @base-ui/react'
  'asChild|Radix composition prop; Base UI composes with render={<El />} (design §14, V50a)'
  # V52 phase 8 (§22): the image service became the ONE pipeline, so the two artwork stores it
  # replaced retire together. Both are the docs/help hazard this script exists for — a page telling
  # an operator to fetch /v1/filler/thumb/{hash} or /v1/channels/{id}/icon sends them to a 404 on
  # every install, and those URLs are exactly the kind of thing that gets pasted into a
  # troubleshooting note and never revisited.
  'v1/filler/thumb|V52 phase 8: a clip still is an image-service image; the DTO carries thumbImage and the client renders /v1/images/{hash}'
  'v1/filler/hover|V52 phase 8: a clip hover loop is an image-service image; the DTO carries hoverImage'
  'clipThumbURL|V52 phase 8: retired with /v1/filler/thumb — render ClipDTO.thumbImage through the <Image> primitive'
  'clipHoverURL|V52 phase 8: retired with /v1/filler/hover — render ClipDTO.hoverImage through the <Image> primitive'
  # ⚠ The TABLE name, not the route: `channel_icons` was a second image store (bytes in the DB,
  # keyed by channel) and any doc describing where a channel icon LIVES is now wrong. The route
  # `/v1/channels/{id}/icon` is deliberately NOT banned — the POST upload half still exists at
  # exactly that path, and banning the string would forbid documenting a live endpoint.
  'channel_icons|V52 phase 8: dropped; a channel icon is an image-service image and its bytes live under images.dir, not in the database'
  'GetChannelIcon|V52 phase 8: removed with channel_icons; read the image record via the image service'
  'PutChannelIcon|V52 phase 8: removed with channel_icons; uploads go through images.Ingest'
  # ⚠ `ClipDTO.thumbnail`/`.preview` are gone from the WIRE while `clips.thumbnail`/`.preview`
  # remain as COLUMNS — the render pipeline writes files under FILLER_DIR and the adoption job
  # converts them. So the bare words cannot be banned; the dotted DTO forms can, and they are what
  # a client-facing doc would name.
  'ClipDTO.thumbnail|V52 phase 8: the wire field is thumbImage (an image record); clips.thumbnail survives as the render→adopt column'
  'ClipDTO.preview|V52 phase 8: the wire field is hoverImage (an image record); clips.preview survives as the render→adopt column'
  # V53e: 31 test files each hand-rolled a `stubFetch` that replaced global fetch. Every one was
  # UNTYPED (so a fixture could omit required fields indefinitely) and UNBOUND (so assertions
  # matched a url SUBSTRING the test itself wrote). The migration found ~40 defects across those
  # files, including catch-alls answering 13 real endpoints with `{}` in the suite whose entire
  # job is proving screens render real content. Without this line nothing stops #32.
  #
  # ⚠ THE CARVE-OUT IS THE SEARCH PATH, not an allow-rule: `packages/api/src/mutator/mutator.test.ts`
  # legitimately stubs fetch — it TESTS `customFetch`, asserting on `credentials: "include"` and the
  # CSRF header, neither of which an MSW resolver can observe because MSW intercepts BELOW the layer
  # under test. It survives only because SEARCH covers `web/apps/web/src` and not `web/packages`.
  # Anyone widening SEARCH to `web/` must add an explicit exemption for that file in the same edit,
  # or the guard fails on the one file that is right.
  'vi.stubGlobal("fetch"|V53e: use the shared MSW layer (src/test/msw) — a hand-rolled fetch stub is untyped AND unbound to a route'
  # V51f: three `filler.Policy` fields that were set in TESTS AND NOWHERE ELSE — no settings key,
  # no env var, no policy field, no UI. `EraStrict` is deleted outright (a narrow era range gives
  # a channel strictness through a control an operator can actually see); the duration bounds keep
  # their struct fields but are now wired to real settings, so the OLD names are what must not come
  # back. ⚠ These are listed because the code READ convincingly: `coverage.go`, `fit.go` and
  # `coverage-meter.tsx` all carried special copy for the strict-era branch, and `PoolReport.Eligible`
  # was headlined as "the number that surprises operators" while being arithmetically identical to
  # `Commercials` on every install ever run. Prose could not have caught that; a grep can.
  'EraStrict|deleted in V51f — it was unreachable (tests only). A narrow policy.filler.era range is how a channel gets era strictness'
  'FILLER_MIN_CLIP_SECONDS|the setting is FILLER_MIN_CLIP_DURATION (a duration like 15s), matching the neighbouring FILLER_MIN_DURATION'
  'FILLER_MAX_CLIP_SECONDS|the setting is FILLER_MAX_CLIP_DURATION (a duration like 90s), matching the neighbouring FILLER_MIN_DURATION'
  # V55 settings audit: these keys were registry declarations without production consumers, or
  # implementation policy presented as operator choice. Persisting and echoing a value is not a
  # feature; keep the old identifiers out of docs, examples, and code until a real consumer ships.
  'season.precision|V55: no production season-selection consumer'
  'SEASON_PRECISION|V55: no production season-selection consumer'
  'playout.transport|V55: internal playout has one implemented transport'
  'PLAYOUT_TRANSPORT|V55: internal playout has one implemented transport'
  'suggest.auto_approve|V55: the approval gate is mandatory, never a setting'
  'SUGGEST_AUTO_APPROVE|V55: the approval gate is mandatory, never a setting'
  'sched.backfill|V55: no production backfill consumer'
  'SCHED_BACKFILL|V55: no production backfill consumer'
  'ingest.max_concurrent|V55: ingest concurrency is pipeline-owned implementation policy'
  'INGEST_MAX_CONCURRENT|V55: ingest concurrency is pipeline-owned implementation policy'
  'filler.starter_collection|V55: starter media is not an operator setting'
  'FILLER_STARTER_COLLECTION|V55: starter media is not an operator setting'
  '"reconcile.every"|V55: superseded by the active channel and library schedules'
  $'\x60reconcile.every\x60|V55: superseded by the active channel and library schedules'
  'RECONCILE_EVERY=5m|V55: superseded by the active channel and library schedules'
  'event.webhook_url|V55: there is no webhook delivery consumer'
  'EVENT_WEBHOOK_URL|V55: there is no webhook delivery consumer'
  'images.formats|V55: output compatibility policy is owned by the image module'
  'IMAGES_FORMATS|V55: output compatibility policy is owned by the image module'
  'images.remote_max_concurrency|V55: provider concurrency is owned by the image module'
  'IMAGES_REMOTE_MAX_CONCURRENCY|V55: provider concurrency is owned by the image module'
  'images.remote_ttl|V55: the remote-artwork compliance ceiling is owned by the image module'
  'IMAGES_REMOTE_TTL|V55: the remote-artwork compliance ceiling is owned by the image module'
  'sched.default_strategy|V55: channel strategy is explicit and no runtime path consumed this default'
  'SCHED_DEFAULT_STRATEGY|V55: channel strategy is explicit and no runtime path consumed this default'
  'sched.episode_norepeat|V55: per-channel policy falls back to scheduler built-ins directly'
  'SCHED_EPISODE_NOREPEAT|V55: per-channel policy falls back to scheduler built-ins directly'
  'sched.movie_norepeat|V55: per-channel policy falls back to scheduler built-ins directly'
  'SCHED_MOVIE_NOREPEAT|V55: per-channel policy falls back to scheduler built-ins directly'
  'sched.series_min_gap|V55: per-channel policy falls back to scheduler built-ins directly'
  'SCHED_SERIES_MIN_GAP|V55: per-channel policy falls back to scheduler built-ins directly'
  'sched.block_max|V55: per-channel policy falls back to scheduler built-ins directly'
  'SCHED_BLOCK_MAX|V55: per-channel policy falls back to scheduler built-ins directly'
  'sched.ordering|V55: per-channel policy falls back to channel strategy and scheduler built-ins'
  'SCHED_ORDERING|V55: per-channel policy falls back to channel strategy and scheduler built-ins'
  'SEASONAL_MODE|V55: seasonal behaviour is per-channel policy, not a consumed global default'
  'PLAYOUT_SUBTITLES|V55: subtitle burn-in is not implemented by the encoder'
  'user.sync_every|V55: user import is explicit until a scheduled consumer exists'
	'USER_SYNC_EVERY|V55: user import is explicit until a scheduled consumer exists'
	# Scheduler task deepening: these implementation-stage rows and schedule knobs were folded
	# into operator outcomes. Persisted scheduled_jobs rows/settings are harmless legacy data;
	# reintroducing the identifiers would recreate duplicate controls.
	'activity-purge|scheduler task deepening: folded into housekeeping'
	'retention-purge|scheduler task deepening: folded into housekeeping'
	'session-sweep|scheduler task deepening: folded into housekeeping'
	'series-episode-refresh|scheduler task deepening: folded into channel-maintenance'
	'channel-sweep|scheduler task deepening: replaced by channel-maintenance'
	'images-rehydrate|scheduler task deepening: folded into images-maintenance'
	'images-gc|scheduler task deepening: folded into images-maintenance'
	'job.activity_purge.schedule|scheduler task deepening: one housekeeping schedule'
	'JOB_ACTIVITY_PURGE_SCHEDULE|scheduler task deepening: one housekeeping schedule'
	'job.retention_purge.schedule|scheduler task deepening: one housekeeping schedule'
	'JOB_RETENTION_PURGE_SCHEDULE|scheduler task deepening: one housekeeping schedule'
	'job.session_sweep.schedule|scheduler task deepening: one housekeeping schedule'
	'JOB_SESSION_SWEEP_SCHEDULE|scheduler task deepening: one housekeeping schedule'
	'job.series_episode_refresh.schedule|scheduler task deepening: one channel-maintenance schedule'
	'JOB_SERIES_EPISODE_REFRESH_SCHEDULE|scheduler task deepening: one channel-maintenance schedule'
	'job.channel_sweep.schedule|scheduler task deepening: replaced by channel-maintenance schedule'
	'JOB_CHANNEL_SWEEP_SCHEDULE|scheduler task deepening: replaced by channel-maintenance schedule'
	'job.images_rehydrate.schedule|scheduler task deepening: one image-maintenance schedule'
	'JOB_IMAGES_REHYDRATE_SCHEDULE|scheduler task deepening: one image-maintenance schedule'
	'job.images_gc.schedule|scheduler task deepening: one image-maintenance schedule'
	'JOB_IMAGES_GC_SCHEDULE|scheduler task deepening: one image-maintenance schedule'
	# Rust is the single image execution engine. These Go dependencies and entry points were the
	# former in-process renderer; restoring any of them would silently recreate the fallback the
	# worker handshake is designed to exclude.
	'github.com/gen2brain/webp|image encoding is owned by the required loomarr-image Rust worker'
	'go.n16f.net/thumbhash|placeholder generation is owned by the required loomarr-image Rust worker'
	'EncodeWebP|image encoding is owned by the required loomarr-image Rust worker'
	'ResizeLadder|image resizing is owned by the required loomarr-image Rust worker'
	'FFmpegAVIF|AVIF encoding is owned by the required loomarr-image Rust worker'
	'HasAVIFEncoder|worker startup self-test is the image capability gate'
  'AVIFEncoder|AVIF encoding is owned by the required loomarr-image Rust worker'
	'check-release-notices.sh|retired: releaseverify owns notice policy directly; do not restore the duplicate middle-man wrapper'
  'SetBcryptCostForTests|the mutable bcrypt cost test hook was removed; bcrypt is read-only legacy verification (§11/§14)'
	# SMTP is one notification provider. Restoring this route or operation recreates the second
	# test/configuration authority that Settings → Notifications removed.
  '/v1/notifications/email/test|SMTP tests use the selected notification provider row'
  'notifications-email-test|SMTP tests use the common notification-destination test operation'
  # P5c: the accepted React Native TV client is the only Shield implementation. These identifiers
  # recreate the removed Kotlin/Compose application, its JVM visual authority, or its old signing
  # seam. Historical engineering evidence is exempt below; shipping source and operator docs are not.
  'org.jetbrains.kotlin.android|P5c: the Shield application is React Native; do not restore the Kotlin Android plugin'
  'androidx.compose|P5c: the Shield presentation is the accepted React Native implementation'
  'io.github.takahirom.roborazzi|P5c: native reference captures replaced the retired JVM screenshot harness'
  'gen-android-tokens.mjs|P5c: no Kotlin token consumer remains'
  'with-shield-sideload-signing|P5b: all React Native release channels use the release-signing plugin'
)
# ⚠ `internal/store/migrations/` is exempt, and it is the one exemption that is forced rather than
# chosen. A migration that CREATES a table names it, and §16 makes applied migrations immutable —
# so `00012_channel_icons.sql` will say `channel_icons` for the life of the repository and cannot
# be annotated out of the way. The migration that DROPS it has to name it too. Neither is an
# instruction to an operator, which is what this ban protects; both are the historical record the
# forward-only rule exists to keep.
ALLOW_PATH='^(PROGRESS\.md|docs/engineering/|scripts/check-retired\.sh|internal/web/dist/|internal/store/migrations/)'
# A line may name a retired identifier when it is EXPLAINING that it is retired — that is how
# §10's "keeps being re-decided" history survives, and how a corrective comment ("this used to
# say X") points at the thing it corrects.
#
# ⚠ Two mechanisms, and the difference matters. The PHRASES below are inferred intent, and
# chasing them is a losing game: every rewording needs a new phrase, and each one loosens the
# check for everybody. `retired-ok` is the EXPLICIT opt-out — put it on the line when the
# mention is deliberate. Prefer it; a reader can see the claim, and it cannot be tripped by
# accident the way "no longer" can.

ALLOW_LINE='retired-ok|[Rr]etired|[Ss]uperseded|no longer exist|was deleted|was removed|used to|does not exist|removed because|keeps being re-decided'
# ⚠ internal/ and docker/ are searched too. They were not before, which is how a Go doc
# comment could keep describing "the sidecar's OWN configuration" — and how a dead
# LoadConfig reading five env vars absent from §15 survived as apparently-live architecture.
#
# ⚠ scripts/ is searched for the same reason, found the same way: latency-sweep.sh had been
# probing the retired /v1/suggestions since V41 — a hand-maintained ROUTE array sitting in the
# one directory the ban could not see, so the sweep silently measured a 404 as if it were an
# endpoint. This file excludes itself via ALLOW_PATH, so the RETIRED array above does not
# self-trip.
#
# ⚠ CHANGELOG.md, CONTRIBUTING.md, AGENTS.md and CONTEXT.md were added for the third instance
# of the same pattern, and this one was the most visible: CHANGELOG.md advertised
# "First-class Docker images (distroless `loomarr:latest`; `loomarr:filler` …)" — a tag that
# does not exist, on a base that is no longer distroless — while `loomarr:filler` sat in the
# RETIRED array above and `make retired-verify` reported clean. The changelog is the most
# product-facing document in the repo and the guard could not see it.
#
# The rule this keeps re-teaching: a hand-maintained list of WHERE to look drifts exactly like
# a hand-maintained list of WHAT to look for. Anything a human reads as a statement about the
# product belongs here. docs-site/ is deliberately absent — it renders docs/ and holds no prose
# of its own (design §13).
SEARCH=(docs internal docker scripts android web/apps/web/src .github README.md SECURITY.md
        CODE_OF_CONDUCT.md CLAUDE.md AGENTS.md CONTRIBUTING.md CONTEXT.md CHANGELOG.md
        THIRD_PARTY_NOTICES.md Dockerfile Makefile go.mod .env.example)
fail=0
for row in "${RETIRED[@]}"; do
  id="${row%%|*}"; why="${row#*|}"
  hits="$(grep -rInF "$id" "${SEARCH[@]}" 2>/dev/null | grep -Ev "$ALLOW_PATH" | grep -Ev "$ALLOW_LINE" || true)"
  if [[ -n "$hits" ]]; then
    fail=1
    printf '\nRETIRED IDENTIFIER STILL REFERENCED: %s\n  %s\n\n' "$id" "$why"
    printf '%s\n' "$hits" | sed 's/^/    /'
  fi
done
[[ "$fail" -ne 0 ]] && exit 1
printf 'retired-verify: clean (%d identifiers checked)\n' "${#RETIRED[@]}"
