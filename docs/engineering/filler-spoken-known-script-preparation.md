# Known-script spoken-safety positive preparation

`cmd/filler-spoken-known-script-prepare` turns already acquired,
consented real-speaker recordings into the positive candidate cohort consumed
by `cmd/filler-spoken-corpus-assemble`. It does not recruit or record a
participant, author a restricted script, create consent, send media to a
provider, or establish positive truth.

The exported interface is one deep operation:
`fillersafetycorpus.PrepareKnownScript(ctx, config)`. Its config names only a
private owner authority, private source root and alias seed, exact ffmpeg and
ffprobe executables, fixed preparation time, case/input/output/time ceilings,
and a new output directory. Consent, script, transformation, asset-rights,
source, interval, media, privacy, and atomic-publication checks stay inside the
module.

## Owner authority

The mode-`0600` owner authority uses schema version 1 and contract
`filler-spoken-known-script-authority-v1`. It binds the private policy and
implementation identities and a sorted member list. Each member supplies:

- one private participant, recording-session, and take identity;
- locale and accent, a versioned script ID and exact script file authority,
  and owner-reviewed policy-mapping evidence;
- the dry master and selected transformed-audio authorities;
- sorted positive slices and ordered source-relative intended intervals using
  only opaque policy-rule IDs;
- one deterministic transformation record with recipe ID/digest, rendering
  tool identity/time, master/output binding, and every music/noise asset; and
- one participant-specific consent contract and exact supporting documents.

The consent contract binds its signed/recorded consent, signer-authority
evidence, exact hosted-processor schedule, withdrawal instructions, review
identity/time, optional expiry or withdrawal, retention policy, redistribution
choice, no-endorsement acknowledgement, and explicit grants for collection,
private storage, deterministic modification, evidence extraction, independent
review, and hosted model evaluation. Private-only redistribution is valid
because this pipeline never publishes the recording. An expired or withdrawn
grant is unusable and causes the whole preparation to fail before ffmpeg.

Every external music/noise asset carries its own exact media and rights
evidence plus a complete certification rights contract. A `music_overlap`
case must bind at least one music asset. A case without an external asset uses
an empty asset list; it cannot claim an asset whose bytes or rights were not
reopened and verified.

The authority shape is:

```json
{
  "schemaVersion": 1,
  "contractVersion": "filler-spoken-known-script-authority-v1",
  "dataset": "consented-known-script",
  "authoredAt": "2026-09-04T12:00:00Z",
  "policySha256": "<private-policy-sha256>",
  "implementation": "spoken-safety-evaluator-v1",
  "members": [
    {
      "participantId": "private-participant-001",
      "sessionId": "session-001",
      "takeId": "take-001",
      "locale": "en-US",
      "accent": "owner-declared-accent",
      "scriptId": "script-v1-001",
      "script": {"path": "scripts/001.txt", "sha256": "<sha256>", "bytes": 64},
      "policyMapping": {"path": "mappings/001.json", "sha256": "<sha256>", "bytes": 256},
      "masterAudio": {"path": "masters/001.wav", "sha256": "<sha256>", "bytes": 65536},
      "selectedAudio": {"path": "variants/001.wav", "sha256": "<sha256>", "bytes": 65536},
      "slices": ["accent_locale"],
      "positiveIntervals": [{"ruleId": "rule-<opaque-id>", "startMs": 500, "endMs": 1500}],
      "transformation": {
        "recipeId": "dry-recording-v1",
        "recipeSha256": "<sha256>",
        "renderedAt": "2026-09-04T11:00:00Z",
        "tool": {"version": "ffmpeg 8.0", "binarySha256": "<sha256>"},
        "masterSha256": "<master-sha256>",
        "outputSha256": "<selected-audio-sha256>",
        "assets": []
      },
      "consent": {
        "schemaVersion": 1,
        "contractVersion": "filler-spoken-participant-consent-v1",
        "participantId": "private-participant-001",
        "document": {"path": "consent/001.pdf", "sha256": "<sha256>", "bytes": 4096},
        "signerAuthorityEvidence": {"path": "consent/001-signer.json", "sha256": "<sha256>", "bytes": 512},
        "processorSchedule": {"path": "consent/processors-v1.json", "sha256": "<sha256>", "bytes": 512},
        "withdrawalInstructions": {"path": "consent/withdrawal-v1.txt", "sha256": "<sha256>", "bytes": 256},
        "signedAt": "2026-09-01T12:00:00Z",
        "rightsReviewedAt": "2026-09-02T12:00:00Z",
        "rightsReviewerId": "owner-rights-reviewer",
        "redistributionScope": "private_only",
        "retentionPolicy": "until_withdrawal_or_rights_retirement",
        "withdrawalSupported": true,
        "noEndorsement": true,
        "grants": {
          "collection": true,
          "privateStorage": true,
          "technicalModification": true,
          "evidenceExtraction": true,
          "independentReview": true,
          "hostedModelEvaluation": true
        }
      }
    }
  ]
}
```

All digest placeholders are 64 lowercase hexadecimal characters. The processor
schedule is strict `filler-spoken-hosted-processor-schedule-v1` JSON. Each
sorted entry binds an OpenRouter HTTPS base URL, requested and resolved model,
upstream provider name and slug, and mandatory ZDR. The packager verifies and
embeds the parsed schedule in each private `rights.json`. The hosted reviewer
reopens that document and requires its exact request route to appear in the
schedule before it may extract audio, create a checkpoint, reserve spend, or
make HTTP. Preparation performs no network request.

```json
{
  "schemaVersion": 1,
  "contractVersion": "filler-spoken-hosted-processor-schedule-v1",
  "processors": [
    {
      "kind": "openrouter",
      "sourceBaseUrl": "https://openrouter.ai/api/v1",
      "requestedModel": "vendor/reviewer",
      "resolvedModel": "vendor/reviewer-version",
      "upstreamProvider": "Pinned Provider",
      "upstreamProviderSlug": "pinned-provider",
      "zdr": true
    }
  ]
}
```

## Family lock and output

Exactly one member is selected per participant. A participant, master, selected
audio, case, or derived source cannot be repeated. The authority must contain
at least 59 participants and cover every locked positive slice. Dry takes,
retakes, cuts, encodes, mixes, clipping, and placement variants never create a
second statistical family.

The adapter snapshots the selected audio and uses the same fixed neutral-video
recipe as VCTK. It strips metadata, emits mono AAC plus neutral H.264 video,
measures the result, fully decodes both streams, and checks every intended
interval against the measured duration. Tool and recipe identity are checked
again after the final case.

The atomically created mode-`0700` output contains private mode-`0600` files:

- `cohort.json`, with opaque positive candidate cases ready for assembly;
- `owner-map.json`, mapping opaque cases/families to participants and takes;
- `cases/<opaque-case>/source.mp4` and `transcript.txt`; and
- per-case `provenance.json` and `rights.json` containing exact private
  transformation and consent evidence.

Each `rights.json` uses the distinct
`filler-spoken-known-script-rights-v1` envelope; the nested participant grant
retains `filler-spoken-participant-consent-v1`. Keeping those identities
separate lets a hosted reviewer recognize the package without confusing a raw
consent record for review authorization.

```bash
go run ./cmd/filler-spoken-known-script-prepare \
  --authority /private/spoken-positive/authority.json \
  --source-root /private/spoken-positive/source \
  --seed /private/spoken-positive/alias-seed.bin \
  --ffmpeg /usr/local/bin/ffmpeg \
  --ffprobe /usr/local/bin/ffprobe \
  --prepared-at 2026-09-04T12:00:00Z \
  --expected-speakers 59 \
  --max-input-bytes 1073741824 \
  --max-output-bytes 1073741824 \
  --max-wall-time 2h \
  --output /private/spoken-positive/prepared
```

Preparation emits only a proposed `positive_candidate`. The complete assembled
draft still needs two independent model-family reviews and disagreement-only
adjudication before the authority locker can establish truth.
