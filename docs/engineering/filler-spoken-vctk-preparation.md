# VCTK spoken-safety clean-control preparation

`cmd/filler-spoken-vctk-prepare` prepares an already-acquired VCTK 0.92 release
for later spoken-safety authority assembly. It does not download VCTK, decide
that speech is clean, run a reviewer or model, begin certification, or grant
ingestion.

The command consumes private `0600` release-authority JSON and a private alias
seed. The release authority binds the exact archive, README, CC BY 4.0 licence,
completed hosted-evaluation rights contract, and an owner-screened list of
eligible audio/transcript members. Member paths are relative to one declared
release root. Every member's exact bytes, stable speaker and utterance identity,
microphone, locale, and screening-evidence digest are pinned. Speaker `p315` is
always refused because VCTK 0.92 does not supply its transcript.

Selection is deterministic but secret: keyed ranks choose one eligible
utterance for each chosen speaker and then exactly 100 distinct speakers. A
second microphone, another utterance, a re-encode, or another derivative of the
same speaker never becomes another source family.

Each selected real utterance is wrapped into a complete audiovisual MP4 with a
fixed neutral-video recipe. The adapter pins the exact ffmpeg and ffprobe bytes
and versions, uses bounded single-threaded encoding, strips metadata, probes the
result, fully decodes both output streams, and records input/output hashes and
complete-span timing. The wall-time ceiling is an execution deadline, including
tool probes, encoding, and decode validation. This changes
the representation, not the speaker-family identity. The fixed visual carrier
exists only because the certification cascade requires both modalities; it is
not visual-suitability evidence.

The output directory is created atomically at mode `0700` and contains only
private `0600` files:

- `cohort.json`: 100 review-ready clean candidates with opaque case/family IDs,
  source authorities, complete source and transcript paths/digests,
  target-locale slice claims, and rights/truth-provenance paths;
- `owner-map.json`: the private mapping back to exact VCTK speaker, utterance,
  microphone, audio, and transcript identities;
- `evidence/release-authority.json`: the exact reviewed release authority used
  for every case's rights evidence; and
- `cases/<opaque-alias>/source.mp4`, `transcript.txt`, and `provenance.json`.

The cohort is not reviewed independently. A later assembler combines it with
consented positives and the other clean slices, then produces the one full
authority draft that independent review bundles bind. This prevents a VCTK-only
review from becoming stale as soon as positive cases are added.

```bash
go run ./cmd/filler-spoken-vctk-prepare \
  --release-authority /private/vctk/release-authority.json \
  --release-root /private/vctk/VCTK-Corpus-0.92 \
  --seed /private/vctk/selection-seed.bin \
  --ffmpeg /usr/local/bin/ffmpeg \
  --ffprobe /usr/local/bin/ffprobe \
  --policy-sha256 <private-policy-sha256> \
  --implementation spoken-safety-evaluator-v1 \
  --prepared-at 2026-09-03T12:00:00Z \
  --expected-speakers 100 \
  --max-input-bytes 1073741824 \
  --max-output-bytes 1073741824 \
  --max-wall-time 2h \
  --output /private/vctk/prepared
```

An existing output, source or evidence drift, unsafe path or symlink, duplicate
content/family, incomplete rights contract, tool drift, insufficient speakers,
resource ceiling, partial encode, decode failure, or non-reproducible identity
fails closed. Aggregate input accounting counts the authority, seed, and each
unique verified file once even when VCTK microphone records share a transcript.
Console output contains aggregate counts and document digests only.
