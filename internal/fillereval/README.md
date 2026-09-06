# Filler admission certification

This package scores captured filler-admission decisions. It deliberately has no provider, network,
media-decoder, store, or application dependency: the same decision JSONL can be replayed after a
prompt, model, policy, or scorer change without spending money or changing production state.

The checked-in `corpus/seed-v1.json` is a schema and regression seed, **not a certification corpus**.
It contains synthetic evidence for failure classes such as conflicting years, brief end cards,
prompt injection, corrupt media, programme excerpts, and ambiguous compilation boundaries. A real
certification manifest references external, legally usable media by content hash and records source,
licence, similarity cluster, campaign, source family, split, labels, evidence, and slices. Non-redistributable media never
enters git.

Schema v5 distinguishes development seeds from certification manifests, preserves every inference
step in a multi-rung prediction, and carries campaign identity for diversity enforcement. A certification case also
locks its evidence packet and item metadata, records item-level rights adjudication and the bounded
source segment, and preserves two independent blind-review submission hashes. Matching submissions
become final directly; divergent submissions require a reasoned third-party adjudication. The report
records the exact manifest SHA-256 and scores only the explicitly selected development or holdout
split, so development examples cannot inflate certification.

Run `make filler-corpus-review` separately for each reviewer. It emits an independently shuffled,
reviewer-visible packet with random aliases and an owner-only map bound to the exact draft digest.
The packet excludes internal case IDs, split/cluster assignment, source filename, creator, campaign,
and labels. Keep each map from its reviewer. A hash-only packet is not inspectable evidence:
`make filler-corpus-review-package` joins it privately to the provider evidence packets, verifies every
hash, and atomically materializes only alias-relative audio, frames, video, and sanitized decoder facts.
Choose explicit `hardlink` mode on one filesystem or `copy` for a portable package; symlinks are never
emitted. Source text and identities, rights facts, and the private map stay out of the package. Its
packet-ordered label template is deliberately invalid until one reviewer fills every line.
`make filler-corpus-review-ollama` can complete one such package with a reviewer-only local
multimodal model. It re-hashes the package, binds transcripts by audio SHA-256 without sending case
IDs, verifies the concrete Ollama tag and digest, sends four ordered frames plus the shared
transcript serially, and atomically publishes `labels.jsonl` with `review-run.json`. The attestation
locks the package, prompt, model, transcript set, latency, tokens, and exact submission hash. Run it
once per independently shuffled package with distinct reviewer-only model families; those families
and the adjudicator family are excluded from the scored candidate matrix for that corpus generation.
`make filler-corpus-review-openrouter` applies the same evidence contract to a fresh capability
snapshot and one exact ZDR upstream route. It disables fallback, requires strict structured output,
reserves every request against explicit per-call and total nano-USD ceilings, records the selected
provider, generation, tokens, latency, and charged amount for every case, and publishes nothing after
any route, schema, accounting, or timeout failure.
The command's `--inspect-checkpoint` mode is the non-spending inspection boundary only for Reviewer B's
exact 300-case checkpoint. It requires the same local package, transcript, historical snapshot, route
and prompt identity, and original ceilings, but does not apply the live run's 24-hour snapshot window,
accept or read a credential, or create a provider client or lock. The checkpoint directory must be
exactly `0700`; the checkpoint and any active lock must be regular, non-symlinked files at exactly
`0600`; package directories and artifacts, transcripts, and the snapshot follow the same exact
`0700`/`0600` rule. Setuid, setgid, sticky, symlinked, and other typed or permission variants fail.
The package directory contains exactly its manifest, declared instructions and template, declared
signals, and their required ancestor directories; the checkpoint directory contains exactly its
checkpoint and optional active lock. Unreferenced files, directories, devices, or other objects fail.
Every object is validated and read through one retained descriptor or descriptor-rooted open, so a
pathname replacement cannot switch the bytes after validation. The offline branch has no credential
lookup or provider-run capability. Successful output is one sanitized, content-addressed JSON object
containing only permitted hashes, aggregate case and historical request accounting, historically
recorded immutable ceilings, and remaining allowance. It emits no raw batch, reviewer, model,
provider, route, or prompt-version value. An incomplete checkpoint always awaits explicit maintainer
approval. A valid lock reports only that an active lock is present; it does not establish staleness or
authorize recovery; an empty present lock is invalid and is never treated as absence. Inspection
authorizes no provider call, recovery run, or spend. Unsafe types or
modes, identity or ceiling drift, duplicate or out-of-order state, an unsettled reservation, invalid
accounting, or an invalid lock fails without stdout or input-directory mutation.

`make filler-corpus-lock` combines the draft, both maps,
and two independently authored JSONL review batches. Each line has `alias`, `reviewerId`, `batchId`,
`reviewedAt`, and `labels`; labels
contain disposition, reject class, content role, taxonomy, policy flags, slices, evidence, and the
answerable review question. A reviewer file must use one identity and one batch throughout. When the
two canonical label hashes differ, `LOOMARR_FILLER_CORPUS_ADJUDICATIONS` names a third JSONL file with
`caseId`, a distinct `adjudicatorId`, `adjudicatedAt`, `reason`, and final `labels`. The command writes
nothing until every draft case is covered and the complete certification manifest validates. The
draft must still be unlocked and contain no labels, reviews, or adjudication. Unknown and trailing
JSON fields fail rather than being retained as an implicit older format, and both blind submissions
must be complete even when a third reviewer chooses one side of a disagreement. There is no case-ID
review submission compatibility format because no completed certification artifact consumes one.

`make filler-corpus-archive` is the metadata-only acquisition preflight for Archive.org. It requires
an identified User-Agent, explicit snapshot time, request/item/per-item-byte/total-byte ceilings, and
a delay of at least 500 ms. It runs serially, caches the exact search and item responses, checks that
search and item licenses agree, excludes NC/ND licenses, records response hashes and retrieval times,
and selects a bounded video representation. Its output is only a candidate inventory: uploader
license metadata still needs independent item-level rights adjudication before it can enter a draft
manifest, and the command never downloads media or invokes a model.

LOC, NASA, CDC, and Commons adapters promote their bounded discovery lanes through the same strict
source-neutral inventory contract. `make filler-corpus-direct` captures a bounded, authored lane
without pretending a local folder grants rights: its schema-v2 manifest predeclares the exact item
count, one contracting-owner authority, and positive acquisition quotas for known corpus roles. One
manifest per owner keeps source concentration measurable instead of collapsing all direct work into
one generic authority. Every case freezes its creator, campaign, and source-family identity before
split planning. The command hashes every unique media payload plus separate rights and provenance
evidence beneath one symlink-safe root and rejects any quota, path, byte, or wall-time violation.
Certification alone owns the final truth and holdout role quotas.
Public and direct inventories combine before one independent rights review.
Schema v4 keeps every bounded capture that found a case. Role-specific searches may overlap only
when the frozen item metadata and selected representation are identical; the combiner then retains
the sorted union of capture IDs and discovery hints. Conflicting duplicates and duplicate capture
identities fail closed. Older single-capture case shapes are rejected rather than adapted.

`make filler-corpus-download` is the separately authorized media step. It accepts only `approved`
rights rows tied to the exact inventory and metadata SHA-256 values, reviewer, review time, rationale,
redistribution decision, attribution, and restrictions; `held` rows remain out of the plan. Before the first request
it proves the approved count and predicted bytes fit explicit ceilings. Downloads remain serial and identified,
redirects stay within each authority's frozen and built-in host policy, and bodies cannot exceed
their recorded size. MP4, JPEG, and PNG are the only admitted representation types. Image responses must
match the declared MIME type and exact byte count, decode completely as that format, end at the terminal
JPEG/PNG boundary, and stay within the 50-million-pixel ceiling. Source checksums are checked when present,
and the schema-3 external ledger adds a locally computed SHA-256, verified media type, image dimensions,
capture identities, role hints, creator, source-family, subject-term, and exact profile/processor provenance.
The downloader validates that completed ledger through the same shared `fillercorpus` interface used by
downstream consumers before it publishes anything; the old command-private schema is not accepted.
Exact duplicate media is rejected. Downloaded files are published without overwrite at mode `0600` beneath
a mode-`0700` directory. Already-local direct-cohort cases are not downloaded again. A
failed or stale approval writes no ledger and cannot silently widen the selected corpus.
The caller must choose `development` or `certification` through
`LOOMARR_FILLER_CORPUS_RIGHTS_PROFILE`. Certification additionally pins
`LOOMARR_FILLER_CORPUS_PROCESSOR_ID` and `LOOMARR_FILLER_CORPUS_PROCESSOR_TERMS_SHA256`; a schema-v3
development approval, changed processor, or changed terms snapshot fails before media access.

`make filler-corpus-met-rights-propose` removes the repetitive mechanical portion of the Met review
without bypassing it. The offline command reopens every exact raw object response, requires private
regular cache files and complete declared coverage, reproduces the inventory projection, and holds
missing or non-empty `rightsAndReproduction`, missing public-domain assertions, changed bytes, or
source-field drift. Its policy-evidence input pins the official API documentation and the exact
official Open Access repository commit while explicitly retaining the documented CC0/image and
non-copyright limitations. The path-free output contains only case ids, metadata digests, closed
status/reason codes, aggregate counts, and policy citations. Rights approval, download, truth,
training, production, scheduling, and broadcast authority are always false; passing rows still need
the existing independent item-level decision.

`make filler-corpus-met-rights-attestation` turns a complete zero-hold pre-screen into one pending
maintainer attestation bound to the exact inventory, inert worksheet, pre-screen, and pinned policy
evidence digests. The maintainer reviews the recorded limitations once, names themself and the review
time, changes `acceptance` from `pending` to `accepted`, and writes one versioned rationale. Then
`make filler-corpus-met-rights-complete` expands that exact attestation into the ordinary completed
CSV with a separate immutable identity and decision row for every item. Any held case, changed input,
mixed authority, stale review, widened use, attribution requirement, or restriction rejects the whole
batch. The CSV remains non-authorizing until `filler-corpus-rights-lock` independently revalidates all
rows; certification, provider transfer, training, production, ingestion, scheduling, and broadcast
remain excluded.

The Met adapter has separate frozen discovery profiles for policy-positive and clean-control
candidates. `met-clean-v1.json` deliberately supplies only search and exclusion predicates: a matching
record is still a rights-reviewable candidate, never clean truth. Clean retention requires opening the
exact materialized image, recording the no-sensitive-subject judgment and diagnostic slices, and locking
that review through the ordinary visual nomination path. One independently sourced still work may count
once after deterministic lossless wrapping; alternate carriers and transforms remain in that same family.

`make filler-visual-corpus-nomination-prepare` is the narrow handoff for the institutional still-art lane.
It strictly reopens the exact inventory, schema-3 materialization ledger, and every private image, then emits
one inert JSON worksheet plus a spreadsheet-safe CSV. Immutable source, creator, family, rights-approval,
file, media, and normalized-image identities are prefilled. The maintainer edits only `nomination`,
`subject_status`, `generated_status`, and `slices_json`. The current closed projection accepts only Met JPEGs
whose independent rights rationale starts with the versioned
`met_cc0_open_access_object_reviewed_v1: ` basis and a non-empty explanation and carries no restriction; it does not infer a
new licence from tags or search results. Preparation also emits a private `review.html` keyboard board. It
shows the exact worksheet-bound image and source metadata and exports the same CSV. A separately selected local
model-assistance JSON may prioritize proposals and explicitly exclude all non-proposals only after its worksheet,
rank, case, and source-content bindings reproduce in the browser. It cannot author a positive or clean decision,
and it is not consumed by the lock operation. When every worksheet row carries only the canonical
`policy-clean-nomination` role, the board instead starts in a 12-image contact sheet. It permits one explicit
human clean confirmation for the eligible undecided images on a page only after a fully bound clean-assistance
manifest covers every exact case with two distinct local vision-model families and a local OCR text-safety
screen, every exact source image loads, and the reviewer checks the page-level broadcast-safety attestation.
Model-positive, age-risk, overlap-hold, and
targeted-review rows are withheld from that action and must be opened for an individual decision; loading model
assistance clears any earlier convenience clean decision on those rows. Positive decisions are always individual.
The clean board also accepts the audience-aware v2 assistance contract. That version must embed one record for
every worksheet case from the declared complete frontier-review ledger, using the closed suitability vocabulary
and exact worksheet/rank/source bindings. The browser recomputes every record digest, the canonical JSONL ledger
digest, per-action and per-flag counts, proposal-to-record observations and eligibility, and the sealed manifest
digest. Any observed flag requires individual review and is shown only as a concise factual signal. The reviewer
attestation names the visual suitability categories explicitly; spoken language remains a separate complete-source
evaluator. A v2 record remains routing assistance: absence is not truth and neither the record nor the manifest can
grant clean, Airworthiness, training, production, ingestion, scheduling, or broadcast authority.

`make filler-visual-corpus-nomination-lock` reconstructs that worksheet from the original bytes before
accepting the completed CSV. It rejects changed cells, blank or unknown dispositions, contradictory judgments,
changed or symlinked media, exact and normalized duplicates, repeated published source-work/family identities,
and repeated positive creators. An explicit `exclude` disposition requires the other three judgment cells to
remain empty/`[]`; it publishes no candidate and cannot be confused with an unresolved blank row. The locked set
binds the exact canonical decision digest and reviewed/excluded counts. A successful lock atomically publishes a
private source root containing only accepted exact images, schema-2 rights evidence bound to inventory,
materialization, approval, case, and content digests, and draft-compatible candidate records. Every output
explicitly leaves model output, truth, training, production use, ingestion, scheduling, and broadcast authority
false. Whole-corpus population, independence, model blindness, review, and certification remain owned by the
visual corpus draft and V68 certificate.

`make filler-corpus-rights-review` converts a frozen mixed-authority inventory into a deterministic worksheet
bounded by explicit minimum and maximum item counts. It exposes the source assertions and selected
representation in immutable JSON plus a spreadsheet-safe CSV, but leaves every authority field
blank. Reviewers edit only `reviewer_id`, `reviewed_at`, `decision`, `basis`, `redistributable`,
`required_credit`, and `restrictions_json`. Local rows expose the exact media, rights-evidence, and
provenance-evidence paths and hashes. This is a review queue, not evidence that any row is legally reusable.
For the explicit `certification` profile, the worksheet instead uses schema v4 and requires the
maintainer/counsel-approved agreement and processor identities. Its per-master schedule separately
records signer authority, every required grant, six embedded-rights categories, redistribution
scope, territory, term/expiry, withdrawal, attribution/restrictions, and any adjudication. All
authority cells remain blank in the generated CSV.

`make filler-corpus-rights-lock` validates the completed CSV against both the original byte-exact
inventory and the inert JSON worksheet. Every row must be present once, immutable source fields must
match, decisions must be complete and time-bound, and approved BY/BY-SA media must carry attribution.
Only a fully valid review is atomically converted to the JSONL consumed by the downloader.
Schema-v4 approval is impossible while any required fact is missing, unknown, conflicting,
expired, malformed, or inconsistent. Held rows retain stable machine-readable reasons. Schema-v3
worksheets remain lockable only under the development profile and cannot pass certification
download or preparation.

`make filler-corpus-prepare` is the next mechanical boundary. Its required profile and schema-v4 plan
must both identify either a development seed or a certification corpus. Development preparation uses
every and only approved row from a fully reviewed inventory and keeps held rows inert; certification
requires every inventory row to be approved and assigned to the complete development/holdout split.
It re-hashes every selected source file, measures the
bounded segment, and stages the four frames, 16 kHz mono audio, and direct-video derivatives under aggregate resource
ceilings. It writes an unlabeled provenance-complete draft and the exact label-blind packet JSONL
consumed by the paid runner. The plan cannot contain truth, taxonomy, policy flags, evidence labels,
or a review answer; those exist only in the two independent review submissions. Blind review and
locking preserve the draft kind, so development labels cannot be promoted into certification.

`make filler-corpus-pilot-rights-review` prepares the distinct five-lane source-yield review packet
from the checked-in locked pilot. Its fifty rows bind every source assertion and representation to
the pilot digest while leaving all reviewer fields blank. `make filler-corpus-pilot-rights-lock`
requires one independently attested reviewer to complete every row, reports whether each lane has at
least five rights-approved and product-relevant candidates, and emits `downloadAuthority: false`.
This qualifies or rejects an adapter lane; it never authorizes acquisition.

`make filler-eval-contract` verifies the scorer and seed. `make filler-eval-cert` scores a JSONL file
named by `LOOMARR_FILLER_EVAL_PREDICTIONS`; the remaining `LOOMARR_FILLER_EVAL_*` variables identify
the corpus, selected split, captured run time, every versioned input, and positive request, spend,
and concurrency ceilings. The scorer is fail-closed: fewer than 1,126 independently clustered holdout cases, missing or
duplicate predictions, cross-split similarity leakage, incomplete attribution, operational failure,
wrong role/taxonomy, exceeded run ceilings, weak confidence bounds, or a missed
precision/coverage/review gate produces a
non-certifying report and nonzero exit.

`make filler-temporal-assess-ollama` runs the non-certifying 32-case temporal challenge through one
digest-pinned local model family. It verifies the identity-blind package and every frame, asks the
unit question first, asks the role question only for standalone units, and publishes an immutable
assessment set with a per-call ledger. Run a distinct model family into a second output, then use
`make filler-temporal-compare` to produce the deterministic unit/role disagreement and calibration
report. Operational failures remain failures and neither command changes corpus truth.

`make filler-temporal-select` projects that report's deterministic stratified candidates into an
immutable, label-free selection bound to the package, both assessment sets, and comparison digest.
`make filler-temporal-assess-openrouter` is the separate paid/manual calibration boundary for a
stronger hosted model such as Qwen3.8. It verifies a less-than-24-hour capability snapshot, pins one
ZDR provider route with fallback disabled, reserves the maximum charge before every serial request,
and asks at most one unit plus one conditional role question per selected case. All request, spend,
and per-request charge ceilings are mandatory. The hosted model digest is the immutable capability
snapshot digest; the result is diagnostic evidence only and cannot certify or mutate corpus truth.

`make filler-temporal-calibration-report` is the inference-free interpretation step. It replays the
original two-model comparison, verifies that the immutable selection is exactly its deterministic
projection, validates the hosted result and all evidence references, and reports per-axis whether the
third model preserved an agreement control, matched either local model, matched neither, or could not
be compared. It reports operational failures and unclear claims separately. The report deliberately
does not call any model truth or recommend promotion without independent calibration labels. Its
versioned disposition policy is declared before the hosted run: fewer than three agreement controls
or either missing dispute axis repairs the selection; an operational failure repeats only the bounded
hosted calibration; and any semantic abstention, broken control, novel claim, non-comparable claim,
or mixed local support repairs the evidence or prompt. Only preserved controls plus unanimous local
support on each dispute axis permits revising the assessor mix and repeating the complete 32-case
diagnostic. The disposition never authorizes the 300-case relabel directly.

`make filler-media-integrity-prepare` turns a private purpose-built authority manifest and the exact
full-decode report into a label-free public package plus owner-only map. Integrity slices and
presentation/source defects use separate closed vocabularies; public bytes contain only fresh aliases
and content/measurement digests. `make filler-media-integrity-score` locks the private comparison
against the same production-policy report, records confusion and one-sided Wilson bounds per slice,
keeps operational failures as holds, and preserves noisy/static misses as measured gaps. Neither
command reads media, calls a provider, tunes production thresholds, or permits production admission.

Predictions record the inference role/rung, requested and resolved route, derivative bounds, detailed
token categories, attempts, generation id, and the provider's exact charged decimal alongside an
integer nanodollar projection. A failed call with missing or malformed settlement keeps those charge
fields missing and records its still-consumed reservation separately. Reports compare total cost, cost per correct automation, cost per
admit, and per-slice/per-rung cost; they never sum provider charges with binary floating point. The
scorer never reads its wall clock, and reports one-sided confidence bounds for every selective-risk,
coverage, review, and slice measure used in certification.

Provider execution belongs to `internal/fillerbakeoff`. That runner requires explicit request,
spend, and concurrency ceilings, accepts only locked certification manifests and label-blind
content-addressed packets, re-hashes external derivatives before spend, escalates through typed
text/frame/video/premium routes on named evaluator reasons, and writes this package's prediction shape. Multi-rung predictions retain
one immutable inference step per call so per-rung cost and attempts are not collapsed into the
terminal route. There is no scalar inference-ledger compatibility shape: schema v5 captures every
attempt in `steps`, while deterministic outcomes and pre-request holds have no step. An explicit
semantic abstention is a successful step with a bounded reason and no evidence; it is not rewritten
as provider failure and cannot be referenced as support. The replay command itself never contacts
OpenRouter or starts local inference.

`make filler-bakeoff-openrouter` is the paid/manual capture boundary. It consumes a locked manifest,
label-blind packet JSONL, external derivative root, and strict versioned JSON containing the run,
admission policy, and ordered routes. It also requires the immutable output of
`make filler-openrouter-snapshot`; both run snapshot identities must equal that artifact's SHA-256,
and every route must match both the requested model ID and catalog canonical revision on a live ZDR
endpoint recorded within the preceding 24 hours.
`OPENROUTER_API_KEY` is read only from the environment. The
adapter performs one request per reserved rung, pins the upstream provider with fallback disabled,
requires strict structured output and ZDR routing, records OpenRouter routing metadata and exact
`usage.cost`, and writes a private atomic prediction JSONL for separate `filler-eval-cert` replay.
