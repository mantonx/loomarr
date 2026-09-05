# Loomarr

Loomarr turns a natural-language channel intent into a live, self-maintaining TV channel:
it suggests a lineup, acquires what is missing, schedules it with commercial breaks, and
converges it on Loomarr's internal playout or an optional Tunarr backend.

This file is a **glossary and nothing else** — what each word *means*. It deliberately holds
no behavior, no endpoints, and no decisions.

⚠ **`docs/design.md` remains the single source of truth** for how the system behaves, and
its numbered sections (`§7`, `§11`, …) are cited from ~2,600 places in the code. Where a
term's *behavior* matters, the § reference next to it is the authority. This file provides one
place to look up a term without loading the much larger behavior specification.

## Language

### Content and acquisition

**Title**:
A unit of content the app wants — one movie or one series. Identity is always an external
provider id, never a name string (§3).
_Avoid_: item, content. A Media Item is the distinct inventory node below, not a synonym for Title.

**Key**:
The stable identity string derived from a Title, identical whether it came from a suggestion
or a webhook: `series:tvdb:<id>`, else `<mediatype>:tmdb:<id>` (§3).
_Avoid_: id, slug

**Record**:
The persisted provisioning state of one Key — its state, library item id, deadline, attempts
and last error (§3).
_Avoid_: row, entity

**Provisioning state**:
Where a Record sits on its way into the library: `wanted` → `requested` → `downloading` →
`available`, or `unavailable` when it gave up (§4). Only `available` is schedulable.
_Avoid_: status (reserved for Proposals and Jobs), download state

**Library**:
The operator's media server (Emby or Jellyfin) — the upstream authority on what is currently
available and the first importer into Loomarr's Media Inventory. Loomarr reads it and never writes
to it; the Library is not the database of record for Loomarr's accumulated metadata.
_Avoid_: media server (acceptable in prose, but `library` is the term in code), Emby, Jellyfin

**Media Inventory**:
Loomarr's durable, provider-neutral knowledge of Media Items, Media Sources, Origins, and current
Observations. It retains broad safe metadata for future consumers; it is not an audio cache and its
presence never authorizes scheduling or playout.
_Avoid_: metadata cache, Library cache

**Media Item**:
One canonical metadata-bearing inventory node. It may be structural (series, season, collection),
playable (movie, episode, extra), or a future kind. Playability comes from having a usable Media
Source, not from the item kind. A Title may link to a Media Item but is not replaced by it.
_Avoid_: Title, programme

**Media Source**:
One concrete playable representation of a Media Item, such as a Library-served original, mapped
local file, or future scanner-discovered file. An item may have zero or many sources.
_Avoid_: stream URL, Path

**Origin**:
The exact authority and stable external identity from which Loomarr learned an inventory item or
source. Cross-Origin merging requires grounded provider identifiers or an explicit link; a name or
filename is never identity.
_Avoid_: provider id (one fact an Origin may carry)

**Observation**:
A versioned, time-stamped, provenance-bearing set of facts imported or measured for an exact item or
source revision. Missing means unknown and remains distinct from an explicitly observed empty value.
_Avoid_: metadata blob, cache entry

**Source Access**:
The transient adapter that opens a resolved Media Source as a local or authenticated HTTP input.
Inventory persists only a protected locator; credentials and authenticated URLs never enter an
Observation, readiness index, publication metadata, or diagnostics.
_Avoid_: source URL, persisted input

**Acquisition**:
A pick that is not in the Library yet and must be requested through Seerr → Sonarr/Radarr.
The opposite of an in-library pick, which is free and instant.
_Avoid_: download, request (a request is the downstream act, not the pick)

### Suggestion and approval

**Intent**:
The operator's natural-language description of a channel — the input to the Suggester.
_Avoid_: prompt, query, description

**Channel Concept**:
An inert, evidence-backed draft Intent that Loomarr recommends an operator might choose to build.
It is not a Proposal and carries no approval, acquisition, or Channel authority.
_Avoid_: recommendation (the act, not the artifact), suggestion, template, proposal

**Development Corpus**:
A digest-pinned synthetic split used to change or diagnose an AI prompt, schema, or transport. It is
disjoint from certification evidence and cannot support a ship claim.
_Avoid_: test set, holdout, certification corpus

**Proposal Job**:
One caller-owned durable execution of an Intent. Its id is the correlation spine for generation,
the optional Proposal it produces, and the intent-bound Channel created on approval. A Proposal Job
may have multiple execution Attempts after worker-loss recovery or a channel Refine, but it is never
the Proposal itself.
_Avoid_: request, suggestion, generation job, Proposal

**Proposal Job Attempt**:
One leased execution of a Proposal Job, identified by the Proposal Job id plus a monotonically
increasing attempt number. Attempts finish as `succeeded`, `failed`, or `interrupted`; an expired
`running` attempt is durably interrupted before its replacement is claimed.
_Avoid_: retry (an operator retry creates a new Proposal Job; recovery creates a new Attempt)

**Proposal**:
The Suggester's grounded answer to an Intent: a lineup of picks plus an extracted policy.
Statuses are `submitted`, `approved`, `denied` (§7, §8).
Lives at `/v1/proposals*`; every operationId is `*-proposal(s)`.
_Avoid_: suggestion, recommendation, plan

**First-channel Journey**:
The authoritative, caller-visible snapshot composed from one Proposal Job, its newest Proposal, and
its intent-bound Channel. It explains the current milestone and permitted next actions without
moving ownership of those domain records into a new mega-state-machine.
_Avoid_: wizard state, SSE state, workflow row

⚠ The routes said `/v1/suggestions` until V41 (retired-ok — named here to record the rename),
and one operationId (`submit-suggestion`) sat
among five `*-proposal` siblings in the same file — so one resource was submitted as a
"suggestion" and read, approved and denied as a "proposal". A glossary nothing follows is not a
glossary. `scripts/check-retired.sh` now guards the old path.

⚠ Two survivors are deliberate, and both are the VERB, not the artifact. The Proposal Job's
persisted `kind` is `"suggest"` (renaming it is a data migration, and the job is not the
proposal), and the SSE frame `"suggestion"` reports that job's PHASE — its Go→TS handler pairing
has no drift guard, so churning it is real risk for no glossary gain. The banned noun is the name
for the artifact; `internal/suggest` remains the package that produces one.

**Grounding**:
The rule that the model may only pick from candidates a Catalog operation actually returned this
run. An id the catalog never surfaced cannot enter a Proposal (§8).
_Avoid_: validation, filtering

**The approval gate**:
The rule that nothing spends real resources until an admin approves it. Acquisition happens
on approval, never on suggestion (§7).
_Avoid_: review, confirmation

**Refine**:
Re-running the Suggester against a stored Intent *plus* the channel's current lineup, to
adjust rather than replace it (§7, §8.2).
_Avoid_: regenerate, retry

### Channels and programming

**Channel**:
A Loomarr-owned channel definition — identity, Lineup, and Policy — whose desired state is
materialized locally and, when Tunarr is selected, projected into a Tunarr channel.
_Avoid_: station, feed

**Lineup**:
The ordered set of Titles a Channel draws from. Editing it is a whole-list replace, diffed
server-side (§7).
_Avoid_: playlist, schedule (a schedule is the time-bound result, not the source set)

**Policy** (`ChannelPolicy`):
The per-channel programming rules: scope, audience, ordering, separation, seasonal, filler,
window (`programming-design.md` §2).
_Avoid_: config, settings (both mean the app-wide subsystem), preferences

**Operator-set**:
A Policy field an admin edited by hand. A later Proposal may not overwrite it — the audience
ceiling may still be tightened, never relaxed (`programming-design.md` §2).
_Avoid_: locked, pinned, dirty, overridden

**Provenance**:
Whether a scheduling rule was authored by the model (`llm`) or a person (`operator`). A refine
replaces the `llm` rules and preserves the `operator` ones.
_Avoid_: origin, author

**Pending slot**:
A Lineup entry whose Title is not `available`. It renders as flex to Tunarr and swaps to a
program in place only if that Title independently reaches `available` — so a manual edit can
never make unapproved content play (§7).
_Avoid_: placeholder, gap, empty slot

**Scheduler decision trace**:
The bounded, versioned record of how one `ComputeDesiredAt` run turned a Channel's approved
Lineup, live availability, Policy, and wall-clock into a cycle. It is current schedule evidence,
not the immutable evidence attached to the originating Proposal (§8; `programming-design.md` §8.4).
_Avoid_: proposal trace, rationale, chain-of-thought

### Commercials

**Clip**:
One piece of filler content. Identity is its **sparse content hash** — 64 hex characters, not
its path (§10 V38c). A file that moves within `FILLER_DIR` is the same Clip; two copies at
different paths are one Clip.
_Avoid_: commercial, ad, asset

**Composite**:
A Clip that is a *container* of other clips — "KCPQ/Fox commercials, 5/28/1996" — kept as the
parent after splitting. A Composite is **not airable**: it is excluded from selection, and its
Segments are what play (§10 V45).
_Avoid_: compilation, reel, source clip

**Segment**:
A Clip produced by splitting a Composite, carrying lineage back to its parent (§10 V45).
_Avoid_: cut, chunk, part

**Taxon** (and **Tag**):
The clip vocabulary is a **forest of taxa on independent axes** — product, format, seasonal,
audience-cue — where a leaf tag like `beer` rolls up to `alcohol` and `drinks`. A Clip carries
a **set** of tags, not one category, so a curation rule can ask "is `cereal` a kind of food?"
A model's output is resolved against the vocabulary or dropped (§10 V45a).
_Avoid_: category (the flat 12-value string this replaced), genre, label

**Pod**:
An assembled commercial break — an ordered set of Clips inserted between programs (§10).
_Avoid_: break, ad block, interstitial

**Filler**:
Loomarr-owned non-program content as a whole. It lives in a Tunarr-local media source, never
in the operator's Library, so it structurally cannot leak into a programming Lineup (§10).
_Avoid_: bumpers, interstitials (both are *kinds* of filler, not the category)

**Filler role**:
What kind of non-program item a Clip is, such as commercial, promo, bumper, PSA, station ID,
trailer, or interstitial. A Filler role says nothing about Media quality or Airworthiness.
_Avoid_: category, approval, suitability

**Media quality**:
Whether a Clip is technically intact, complete, and presentable enough for its intended playout.
Media quality says nothing about the Clip's Filler role or Airworthiness.
_Avoid_: usable (too narrow), appropriate, safe

**Airworthiness**:
Whether a Clip is permitted for unattended playout under the operator's audience and content
policy. A recognized Filler role and acceptable Media quality never imply Airworthiness.
_Avoid_: appropriateness (too vague), safety (an overclaim), admission (the larger decision)

**Suitability flag**:
A closed, evidence-backed description of content relevant to Airworthiness, such as explicit
nudity or hateful language. It is an observation, not the policy verdict that consumes it.
_Avoid_: warning, rating, rejection reason

### Delivery

**Playout**:
Loomarr serving its own video streams, as opposed to delegating to Tunarr (§9.1).
_Avoid_: streaming, transcoding (transcoding is one step within playout)

**Reconcile**:
Bringing owned state to its desired form — a Channel's local schedule and optional Tunarr
projection, or the Library into Records. Always best-effort and repeatable; there is no manual
"rebuild" (§7, §9).
_Avoid_: sync, push, publish

**Image** (and **Rendition**):
Every picture in Loomarr — channel icon, clip still, TMDB poster — is one **Image** travelling
one pipeline; a **Rendition** is a particular size and format of it (§22). Callers hand bytes
or a URL and receive an Image; they ask for a Rendition and receive a file. Nothing outside
that package knows the disk layout, the hash, the format ladder, or which encoder ran.
_Avoid_: thumbnail, asset, poster (all are Renditions of an Image)

**Image worker**:
The required Rust process behind the Image service's one rendering seam (§22). It validates and
interprets pixels and writes unpublished Renditions; Go still owns Image identity, policy, and
publication. It is an implementation detail shipped in the Loomarr container, not a sidecar or an
optional integration.
_Avoid_: image service (that includes the Go-owned domain), daemon, fallback

### Filler geography

**Installation geography**:
The optional home country and local market that constrain automated filler use across the instance and supply the default for Channels without their own geography.
_Avoid_: guide timezone, locale, server location

**Channel geography**:
The country and optional local market whose viewers a Channel is programmed for. It inherits Installation geography; a Channel may choose a market but cannot escape a configured Installation country.
_Avoid_: station location, timezone, region

**Source geography**:
The asserted country and optional local market served by a registered filler Source. It constrains acquisition before discovery or download; an unknown Source is ineligible once Installation country is configured.
_Avoid_: provider location, uploader location, inferred locale

**Geographic scope**:
Whether a Clip is applicable throughout its country (`national`), only inside one named market (`local`), or is not yet known (`unknown`).
_Avoid_: region, location type, reach

**Market**:
An operator-visible local broadcast area within one country, such as New York or Seattle. It is an explicit identity, never inferred from a timezone.
_Avoid_: timezone, city (a Market may cover more than one city), region

### People and access

**The allowlist**:
The `users` table. You can sign in iff you have a row — a credential proves *who you are*, the
row decides *whether you may enter* (§11). The central authorization invariant.
_Avoid_: whitelist, user list, ACL

**Credential path**:
A way of proving identity: local password, imported media-server account, or SSO. All three
resolve to the same allowlist and none of them provisions a row (§11).
_Avoid_: auth method, provider, login type

**Imported**:
A media-server account an admin explicitly added to the allowlist. Signing in is never
self-provisioning (§11).
_Avoid_: synced, linked, connected

**Contact address**:
An optional destination attached to a person for account messages. It has its own verification
and provenance; it is never a username, credential, or reason to admit someone (§11).
_Avoid_: login email, account email, identity

**Invitation**:
An administrator's durable decision to reserve one local or imported identity and admit it with a
specific Loomarr role after explicit redemption (§11). It is not the link that carries that decision.
_Avoid_: invite link, registration, user

**Invitation grant**:
One expiring, single-use bearer capability for redeeming an Invitation. Only its hash is durable;
email, copied-link, and QR presentation are ways a grant may reach a person (§11).
_Avoid_: invitation, token (too broad), device code

**Notification intent**:
A durable, channel-neutral request for Loomarr to tell one recipient about a typed domain event,
such as an account Invitation or local-password recovery (§11).
_Avoid_: email, message, job

**Delivery means**:
One configured way to execute a Notification intent. Email is the first means; later means do not
change the callers that create intents (§11).
_Avoid_: provider, notification type, channel (already a television Channel)

**Delivery attempt**:
One bounded execution of a Notification intent through one Delivery means, with a safe outcome and
retry classification but no credential or bearer URL (§11).
_Avoid_: notification, send, retry

**Sharing affordance**:
An immediate, administrator-controlled presentation of an Invitation grant, currently copy or QR.
It is not a Delivery means and creates no claim that Loomarr contacted the recipient (§11).
_Avoid_: notification, delivery, device pairing

### Operations

**Activity**:
A curated, operator-readable historical statement about something Loomarr did. Activity is a
product feed, not a technical diagnostic stream.
_Avoid_: log, event log, audit log

**Diagnostic event**:
One structured technical observation from the Loomarr server or a connected Loomarr client,
retained so an operator or support agent can correlate and investigate behaviour.
_Avoid_: activity, log line, console message

**Process run**:
One bounded lifecycle of an external media process Loomarr owns, such as an ffmpeg playout parent,
programme encoder, HLS remux, preparation, or probe.
_Avoid_: stream, session (both already mean different things)

**Process output**:
The bounded technical text emitted by one Process run and retained for diagnosis; media bytes are
not Process output.
_Avoid_: stream output, console log

**Support bundle**:
A bounded, redacted diagnostic package an administrator deliberately assembles for download and,
in the future, explicit submission for help.
_Avoid_: log dump, crash report

**Startup report**:
The ordered health account for one Loomarr application generation, from configuration through
readiness, including required failures and optional degradation.
_Avoid_: boot log, startup table (the table is one rendering of the report)

**Job**:
A named unit of recurring or long-running work on the job bus, with a cron default and a
settings key. Reports progress over SSE and is cancellable (§18).
_Avoid_: task, cron, worker

**Phase**:
One numbered step of the build plan (§21). Its status and gate evidence live in `PROGRESS.md`,
never in an issue.
_Avoid_: milestone, sprint, epic

**Gate**:
The set of tests that must pass for a Phase to count as done. Never stubbed, skipped, or
weakened (AGENTS.md prime directive #2).
_Avoid_: check, CI (CI is where gates run, not what they are)
