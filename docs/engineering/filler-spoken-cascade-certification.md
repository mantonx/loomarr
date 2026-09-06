# Spoken-safety cascade certification

This is the operator contract for `cmd/filler-spoken-cascade-certify`. The command scores
already-produced evidence. It does not read media, call a model, train a model, admit filler,
or change the library.

The workflow replaces a maintainer blind-viewing marathon with governed source truth plus
deterministic replay. Humans may author or adjudicate truth, but the maintainer does not have
to watch every source and the scorer never asks anyone to judge a model output interactively.

`cmd/filler-spoken-cascade-authority` builds the first input; the certification command scores
the second. Authority building and scoring are separate so no evaluated result can influence
truth or challenge membership.

## Building the authority

The builder consumes a private source-and-truth draft, two independent review bundles, an
optional adjudication bundle containing exactly the disputed cases, a private alias seed, and
the root containing the exact complete-audiovisual sources and rights/truth evidence. It checks
the actual source bytes through each certification-independent source authority and verifies the
draft-pinned rights/truth digests against their current files. Before any source planning or decode,
it also validates every case's rights, truth provenance, and source authority together at the fixed
authoring time. Shared rights bytes may be parsed once, but the source/provenance binding is checked
for every case. The closed supported set is the complete canonical VCTK release-authority v1 contract
and the canonical known-script rights v1 contract. Expired or invalid VCTK evaluation rights, a VCTK
member/provenance mismatch, an output/source mismatch, or expired, withdrawn, malformed, unsupported,
unbound, or asset-rights-invalid participant evidence invalidates the whole lock. It then derives
opaque case, source-family, and reviewer IDs. The output contains no path or private source,
family, or reviewer identifier.

Review bundles bind the exact draft digest and are complete and sorted. Reviewers are blind to
the cascade's output and to one another's answers; known-script reviewers may see the intended
claim and interval they must verify. A reviewer can be model-backed, but all model reviewer
families must differ from one another and from the proposer, native-audio adjudicator, and
complete-video corroborator families. Thus independent models can do routine review and a human
can be reserved for consent and genuine disagreements.

Review decisions are `verified` or `rejected`, not `positive` or `clean`. The draft already owns
the proposed label. This lets a reviewer reject either a contaminated clean control or an inaudible
positive without inventing different truth. A verified positive repeats the exact proposed
intervals; all clean and rejected decisions omit intervals. Two agreeing rejections fail authority
construction, while a primary disagreement requires a separate independent adjudicator whose
`verified` decision establishes the draft claim.

```bash
go run ./cmd/filler-spoken-cascade-authority \
  --draft /private/cascade-draft.json \
  --first-review /private/reviewer-a.json \
  --second-review /private/reviewer-b.json \
  --seed /private/alias-seed.bin \
  --source-root /private/corpus \
  --authored-at 2026-09-02T16:00:00Z \
  --expected-cases 159 \
  --max-source-bytes 10737418240 \
  --output /private/cascade-authority.json
```

Add `--adjudicator /private/adjudicator.json` only when the primary reviews disagree.
Every input and evidence file is a non-symlinked private file at mode `0600` or stricter; the
output is created once at `0600`. The command reads and hashes local files only. It does not
download data, manufacture consent, wrap audio-only datasets into video, transform sources,
call a provider, or spend money. Those acquisition and preparation operations remain explicit
upstream steps. Its immutable output is not a live withdrawal registry and grants no ingestion or
production-admission permission; a later live-use boundary must recheck current rights again.

## Inputs

Both inputs are immutable JSON files, regular files rather than symlinks, and mode `0600` or
stricter. Unknown fields, trailing JSON, files larger than 64 MiB, and input/output path reuse
are rejected.

### Authority

The authority is written before any evaluated run. Its exact file bytes become the
`authoritySha256` used by the result manifest and the `CertificationSHA256` used by every
durable run. Reformatting the authority therefore creates a new challenge identity.

It binds:

- development versus certification status;
- corpus, private policy, proposer, evaluator implementation, and both hosted routes;
- exact requested/resolved models, upstream provider, capability, prompt, and schema digests;
- source bytes, duration, content and source-authority digests, opaque source family, locale,
  and declared slice membership;
- truth-provenance and rights/consent digests;
- positive rule intervals using opaque rule IDs only; and
- two independent primary attestations, with one adjudicator only when the primaries disagree.

A model reviewer declares its model family. That family must differ from the proposer, audio
adjudicator, and video corroborator families. Reviewer IDs, aliases, families, and rule IDs
are opaque; the file contains no transcript, phrase, quote, or source path.

Cases and their slice lists are strictly sorted. Source content and source families cannot be
reused across cases. A valid certification authority covers all required positive and clean
slices, contains at least 59 positive source families and at least 100 independent clean source
families. The latter gives the 1% observed false-positive gate one-source granularity; it is not
a confidence-bound claim.

### Label-blind result manifest

The result manifest contains every authority alias exactly once, in sorted order. It does not
contain labels. Each item carries the path-free `fillersafety.LedgerRun`, its complete ordered
event ledger, and the ID and full-envelope SHA-256 of its terminal event.

Validation replays the ledger grammar and then cross-checks the source plan, proposal,
candidate coverage, reservations, settlements, terminal evidence, provider routes, schema,
and timestamps. Missing, extra, duplicate, incomplete, reordered, pre-authority, or drifted
runs invalidate the complete manifest. They never reduce a denominator.

Historical ledger rows without matched-rule attribution remain readable for recovery and
inspection. They cannot be certification input. New certifiable audio assessments carry a
sorted list of opaque matched-rule IDs; failure and absent states carry an explicit empty list.

## Scoring

A positive interval is detected only when a proposed interval overlaps it, the audio state is
`detected`, and that assessment contains the exact expected opaque rule ID. A different rule,
video-only signal, unprojectable presence, or unattributed candidate is not a hit.

A positive source passes only when every expected interval is detected and the run has no
coverage hold. Clean false positives are projectable audio detections. Provider failures,
unclear or invalid audio, unprojectable evidence, incomplete video, and video-only prohibited
signals are coverage holds rather than silent negatives.

Certification requires all of the following:

- at least 59 unique positive source families;
- zero missed positive sources;
- a one-sided exact 95% Clopper-Pearson source-recall lower bound of at least 95%;
- zero coverage holds; and
- no declared clean slice or clean locale above a 1% observed false-positive rate.

A development authority can produce only `diagnostic_passed`. Every report leaves training,
ingestion, scheduling, and production-admission permission false, including a passing report.

## Command

```bash
go run ./cmd/filler-spoken-cascade-certify \
  --authority /private/cascade-authority.json \
  --results /private/cascade-results.json \
  --scored-at 2026-09-02T18:00:00Z \
  --output /private/cascade-certification.json
```

`--scored-at` is fixed input rather than the wall clock so the same inputs reproduce the same
report. The output is created once at mode `0600`; an existing file is never overwritten. The
console receives aggregate counts and digests only.

The selected production-baseline proposer is now legally unblocked and exactly pinned without
external weights: it partitions the complete soundtrack into contiguous 28-second windows and
the existing extractor adds at most one second of context at each edge. The sherpa proposer
remains a development comparison only while its exact weight authority is unresolved.

The remaining operational prerequisite is a real source-disjoint private authority and its
independent execution through the pinned hosted routes. Until that exists, this command is
tested machinery, not a production certification claim.

The first upstream corpus step is the non-authorizing
[VCTK clean-control preparer](filler-spoken-vctk-preparation.md). Its output is only one
review-ready cohort; it cannot be scored or locked as a complete authority by itself.
The assembled full draft can be reviewed without a blind maintainer playback pass by the
[spoken-safety model-review operation](filler-spoken-model-review.md); two independent runs are
still required and any disagreement remains an explicit adjudication case.
