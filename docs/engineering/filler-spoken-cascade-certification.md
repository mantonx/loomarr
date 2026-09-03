# Spoken-safety cascade certification

This is the operator contract for `cmd/filler-spoken-cascade-certify`. The command scores
already-produced evidence. It does not read media, call a model, train a model, admit filler,
or change the library.

The workflow replaces a maintainer blind-viewing marathon with governed source truth plus
deterministic replay. Humans may author or adjudicate truth, but the maintainer does not have
to watch every source and the scorer never asks anyone to judge a model output interactively.

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
reused across cases. A valid authority covers all required positive and clean slices, contains
clean cases, and has at least 59 positive source families.

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
