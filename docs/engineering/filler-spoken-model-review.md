# Spoken-safety model review

`cmd/filler-spoken-model-review` turns one assembled primary-review worklist
into the exact model-backed review document consumed by
`cmd/filler-spoken-cascade-authority`. It is the routine-review automation
step: run it once per independent model family, then use a human or a third
independent model only for disagreements the authority locker identifies.

The exported interface is one deep operation:
`fillersafetyreview.RunOpenRouter(ctx, config)`. Its config names a private
plan, the assembled private root, an API key, the local ffmpeg executable, a
private checkpoint directory, and a new output path. Worklist validation,
source verification, complete-audio extraction, prompt/schema construction,
route locking, charge accounting, checkpoint recovery, and canonical review
publication stay inside the module.

## Review plan

The mode-`0600` plan binds:

- exact relative paths, SHA-256 digests, and byte counts for the assembled
  `draft.json`, one primary worklist, and the OpenRouter snapshot;
- a reviewer ID and model family distinct from every evaluated model family;
- one requested model, canonical resolved model, upstream provider name and
  provider slug from the snapshot;
- the exact expected case count; and
- maximum requests, per-request charge, total spend, verified input bytes,
  extracted audio bytes, per-case time, and whole-run time.

The snapshot is the existing `fillerbakeoff.OpenRouterSnapshot` contract. It
must be fresh, list audio input plus strict structured output, and prove one
live ZDR endpoint. The request disables fallbacks and data collection. A
second primary reviewer needs another model family, reviewer ID, plan,
checkpoint directory, and output file.

Before any local media tool, checkpoint write, charge reservation, or HTTP
request, the reviewer reopens each rights document. The corpus module
recognizes the `filler-spoken-known-script-rights-v1` envelope, strictly decodes it, and
revalidates the participant binding, complete hosted-evaluation grant,
expiry/withdrawal state, processor schedule, and any time-sensitive asset
rights at the review time. It authorizes only an exact match for the review
plan's OpenRouter base URL, requested/resolved model, upstream provider
name/slug, and ZDR requirement. A recognized malformed contract or an
unmatched route stops preflight without printing participant identity, consent
contents, or private paths. Unrelated rights formats continue through their
existing contract-specific checks; their bytes are never mistaken for
participant consent.

```json
{
  "schemaVersion": 1,
  "contractVersion": "filler-spoken-model-review-plan-v1",
  "draft": {
    "path": "draft.json",
    "sha256": "<draft-sha256>",
    "bytes": 65536
  },
  "worklist": {
    "path": "primary-review-one.json",
    "sha256": "<worklist-sha256>",
    "bytes": 131072
  },
  "snapshot": {
    "path": "reviewer-one-openrouter-snapshot.json",
    "sha256": "<snapshot-sha256>",
    "bytes": 16384
  },
  "reviewerId": "primary-model-one",
  "modelFamily": "independent-audio-family-one",
  "model": "vendor/model-version",
  "resolvedModel": "vendor/model-version-revision",
  "upstreamProvider": "Pinned Provider",
  "upstreamProviderSlug": "pinned-provider",
  "disableReasoning": false,
  "expectedCases": 162,
  "maximumRequests": 166,
  "maximumChargeNanoUsd": 100000000,
  "maximumSpendNanoUsd": 16600000000,
  "maximumInputBytes": 2415919104,
  "maximumAudioBytes": 4194304,
  "perCaseTimeoutMs": 120000,
  "maximumWallTimeMs": 21600000
}
```

Digest placeholders above are replaced with 64 lowercase hexadecimal
characters. The total example spend ceiling is $16.60; an operator should use
the lowest measured ceiling supported by the selected route. Creating a plan
does not authorize a paid run. The command runs only when the operator supplies
the credential and those explicit budgets.

## Decision mapping

The model receives complete source audio, the private policy, the proposed
claim, and proposed positive intervals. It does not receive cascade output,
the sibling review, or a request to transcribe speech. Its strict response has
only a verdict, audibility, opaque matched-rule IDs, and confirmed proposed
interval indexes.

- A proposed positive becomes `verified` only when audio is clear, every
  proposed interval index is confirmed, and the expected opaque rules are
  matched. Its canonical review assessment copies the draft intervals.
- A proposed clean becomes `verified` only when no policy rule is matched and
  audibility is `clear` or `no_speech`.
- A clear contrary result becomes `rejected`. This is how a contaminated clean
  control or inaudible positive is removed without inventing replacement
  truth.
- `unclear`, degraded evidence, malformed output, incomplete interval
  confirmation, or an operational failure stops the run without publishing a
  review bundle.

The authority locker accepts only a draft whose final independent verdict is
`verified`. It still owns disagreement-only adjudication and model-family
independence.

## Recovery and outputs

Before each HTTP request, the checkpoint records the exact request digest and
full maximum-charge reservation at mode `0600`. A response settles exact
provider usage and either appends the next accepted assessment or records a
bounded failure. Accepted assessments must be a prefix of worklist order and
are never requested twice. An interrupted reserved request stays unsettled and
requires explicit operator reconciliation; restart never guesses that it was
free.

Once every case has a decisive assessment, the command writes one canonical
`filler-spoken-cascade-authority-review-v2` document with an atomic create-only
publication. Its embedded path-free model evidence binds the plan, worklist, policy,
ZDR snapshot, exact route, prompt/schema, ffmpeg identity, limits, aggregate
usage/cost, and each attempt's request/response/observation digests and settled
charge. The review and final authority attestations retain the canonical
evidence digest. It does not include raw media, policy text, transcript,
response body, API key, or private source path.

```bash
go run ./cmd/filler-spoken-model-review \
  --plan /private/spoken/reviewer-one-plan.json \
  --input-root /private/spoken/review-draft \
  --api-key-file /private/secrets/openrouter \
  --ffmpeg /usr/local/bin/ffmpeg \
  --checkpoint /private/spoken/reviewer-one-checkpoint \
  --output /private/spoken/reviewer-one.json
```

The implementation and tests use a fake provider. No real provider request or
paid inference is part of the implementation checkpoint.
