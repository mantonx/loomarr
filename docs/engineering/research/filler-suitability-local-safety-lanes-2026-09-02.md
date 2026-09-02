# Local safety signal lanes for filler suitability

Issue: [#905](https://github.com/loomarr/loomarr/issues/905)
Date: 2026-09-02
Status: research only. This note grants no ingestion, training, scheduling, or admission authority.

## Decision

Use three independent, source-relative, fail-closed signal lanes. Do not use
Apple Vision observations as a safety result, and do not replace Loomarr's
timestamped local Whisper lane with Apple Speech.

1. **Visual-sensitive-content lane (Mac candidate):** first use a throwaway signed
   macOS prototype to establish whether Loomarr can obtain the required
   entitlement and an enabled policy. Only if that passes should an evaluation
   helper submit the exact local source video to `SCSensitivityAnalyzer` and
   record its capability snapshot, file digest, result, completion/error, and
   framework/OS identity. A positive result quarantines the source and all
   mapped derivatives; unavailable policy, entitlement, platform, analysis error,
   or an unbound result is a coverage hold. It is not an auto-clear or a required
   production dependency.
2. **Spoken-language lane (portable):** reuse Loomarr's digest-pinned local
   `whisper-cli` engine and immutable timed-artifact machinery, but produce a new
   complete-source safety transcript. The ordinary catalog transcript is
   selective and samples only `LanguageSpan`; it cannot establish full audio
   coverage. Run a versioned restricted-lexicon matcher over the complete
   normalized transcript. A match, low-confidence/ambiguous match, transcript
   failure, missing timing, or a digest/version mismatch quarantines or holds
   according to the locked policy; only a complete, bound, certified run can
   contribute a clean observation.
3. **Written-text lane (not yet covered):** build a separately measured,
   full-duration OCR evidence contract before using visible text as a clean
   observation. Apply the same restricted matcher to timestamped OCR spans.
   Existing scene-selected Apple Vision OCR remains useful evidence, but its
   sparse frames cannot establish absence; until dense coverage is certified,
   visibly written prohibited language remains a coverage hold.
4. **Direct-video screen and quarantine remain separate:** local Boolean visual
   detection and transcript matching do not establish unit identity, temporal
   structure, or general semantic suitability. Use the existing bounded
   direct-video route only for its named ambiguity; project every positive or
   incomplete-modality result to the source-relative quarantine seam rather than
   clearing by majority vote.

This is an architectural inference from the documented APIs and Loomarr's
existing evidence boundaries, not a claim that any lane has certified recall.

## Verified capability boundary

| Capability | Documented fact | Constraint and consequence |
| --- | --- | --- |
| Sensitive Content Analysis (SCA) | `SCSensitivityAnalyzer` supports image and local video-file analysis; Apple describes its result as whether the checked media contains nudity/sensitive content. The installed SDK exposes `analyzeVideoFile:` from macOS 14/iOS 17 and a Boolean `sensitive` result. | It is a local-file visual screen, not a transcript, a category taxonomy, a time interval, or a general suitability classifier. A source-level positive is useful; a negative cannot clear the source. [Apple API](https://developer.apple.com/documentation/sensitivecontentanalysis/scsensitivityanalyzer), [video-file API](https://developer.apple.com/documentation/sensitivecontentanalysis/scsensitivityanalyzer/analyzevideofile%3Acompletionhandler%3A?language=objc); installed Xcode SDK `SCSensitivityAnalyzer.h:14-67`, `SCSensitivityAnalysis.h:14-21`. |
| SCA availability | Apple documents that successful detection requires `analysisPolicy != disabled`; the framework exposes a Sensitive Content Analysis client entitlement. The installed header says the policy follows Sensitive Content Warning or Communication Safety settings. | A server cannot assume availability on an arbitrary Mac, and a Linux/container install has no SCA lane. Missing entitlement, disabled policy, unsupported OS, or an error must be recorded as `visual_coverage_incomplete`, never as clean. [Apple SCA overview](https://developer.apple.com/documentation/sensitivecontentanalysis/); [analyzer API](https://developer.apple.com/documentation/sensitivecontentanalysis/scsensitivityanalyzer); installed SDK `SCSensitivityAnalyzer.h:16-31`. |
| SCA video granularity | Apple returns progress for a local video-file check; the installed header, whose API is available from macOS 14, returns one `SCSensitivityAnalysis` at completion. The newer `SCVideoStreamAnalyzer` is iOS 26 only, for call streams, and is unavailable on macOS. | Do not infer an offending timestamp from SCA. Preserve the source-level verdict and use direct-video/frame evidence only where an interval is needed for explanation. [Apple video handler](https://developer.apple.com/documentation/sensitivecontentanalysis/scsensitivityanalyzer/videoanalysishandler); installed SDK `SCSensitivityAnalyzer.h:47-65`, `SCVideoStreamAnalyzer.h:20-28, 87-154`. |
| Apple Speech | `SFSpeechURLRecognitionRequest` recognizes an **audio** file URL. Its on-device request is honored only when the recognizer supports on-device recognition; results include segments with text, confidence, timestamp, and duration. | It is not a direct-video API and its local availability varies by recognizer/locale/device. It adds no necessary evidence feature over the existing portable Whisper lane, and must not become a fallback that silently sends audio to the network. [Apple request API](https://developer.apple.com/documentation/speech/sfspeechurlrecognitionrequest), [on-device support](https://developer.apple.com/documentation/speech/sfspeechrecognizer/supportsondevicerecognition); installed SDK `SFSpeechRecognitionRequest.h:13-20, 75-85`, `SFTranscriptionSegment.h:15-76`. |
| Loomarr local Whisper | Loomarr already captures immutable transcripts from exact packet WAVs with an implementation version plus executable and model SHA-256 values. Its media seam represents timestamped transcript segments. Upstream documents `whisper-cli` transcription of a converted 16-bit WAV file and support for offline, Apple-Silicon-friendly execution. | Keep this as the one portable speech producer, but do not reuse the selective catalog transcript as safety coverage. Extract the complete source audio deterministically, bind its digest and time transform, and retain timed segments for spoken-language matching and source projection. [whisper.cpp source README](https://github.com/ggml-org/whisper.cpp); [`cmd/filler-bakeoff-transcribe/main.go:1-90`](../../../cmd/filler-bakeoff-transcribe/main.go#L1-L90); [`internal/mediatools/types.go:52-58`](../../../internal/mediatools/types.go#L52-L58). |

## What the existing Apple Vision observations do not prove

The evidence package records 708 Apple Vision observations on 226 frames from
the 48-case package. That is useful provenance for what was observed on those
sampled frames, not a safety detector run or a representative suitability
measurement. [`filler-model-led-identification-plan-2026-08-31.md:44-48`](../filler-model-led-identification-plan-2026-08-31.md#L44-L48)

Apple's Vision classification API produces labels that *describe an image*, and
its text request recognizes text in an image; neither documented API is a
nudity/sensitive-content verdict. [Apple image classification](https://developer.apple.com/documentation/vision/vnclassifyimagerequest), [Apple text recognition](https://developer.apple.com/documentation/vision/vnrecognizetextrequest); installed SDK `VNClassifyImageRequest.h:15-48`, `VNRecognizeTextRequest.h:18-83`.

Therefore those observations do **not** establish any of the following:

- that every frame or visual interval was examined;
- that a missing label/text observation is absence of sensitive content;
- recall, precision, calibration, or equivalence to SCA;
- spoken-language coverage; or
- permission to auto-admit a source, derivative, or compilation segment.

This matches the production vision rung: it extracts selected keyframes for a
provider and persists only grounded tagging observations, rather than a safety
verdict. [`internal/filler/stage_vision.go:63-160`](../../../internal/filler/stage_vision.go#L63-L160)

## Locked certification matrix

Lock a new, source-disjoint challenge before implementation. Store only
content-addressed private controls and public hashes/metadata; never place
prohibited text in the public report. Labels are restricted reviewer annotations
for lane-specific presence/absence and interval/source binding, not a second
full blind viewing pass. The matrix tests the three lanes separately and their
quarantine projection jointly.

| Cell | Locked controls and transformations | Required observations | Metric | Fail-closed threshold |
| --- | --- | --- | --- | --- |
| Visual positive | Rights-cleared source videos with a restricted visual-positive label; include short exposure, scene cut, crop/letterbox, transcode, VFR/CFR, muted, and derivative/compilation placement variants. | Exact source and derivative digests; SCA policy/entitlement/OS/framework identity; complete result; source projection. | Source recall and transformation recall. | Any positive source or transformed witness without a source quarantine is a certification failure. Any unavailable/error result is incomplete coverage, never a negative. |
| Visual clean | Rights-cleared clean videos across programme, advert, animation, monochrome, low light, and visually busy slices; apply the same transforms. | Same binding and completion fields; no mutation of source evidence. | Source false-positive rate, slice maximum. | More than 5% false positives in any locked clean slice fails release of automatic quarantine; clean negatives still do not authorize semantic admission. |
| Spoken positive | Restricted spoken-language positives with exact time spans; include quiet speech, music/overlap, accents/locales, speed/pitch changes, codec changes, clipping, and placement in compilations/derivatives. | WAV/source/derivative digests; Whisper implementation/binary/model digests; segment times/confidence; lexicon/policy version; projection. | Segment recall, source recall, boundary-overlap recall. | Any labeled positive with no matching transcript interval and source quarantine fails certification. Missing/untimed/ambiguous transcription is coverage incomplete, never clean. |
| Spoken clean | Clean speech, wordless clips, music-only clips, homophones/near-matches, captions-only content, and all target locales. | Same speech provenance and matcher decision. | Source false-positive rate and per-locale maximum. | More than 1% false positives in any locked spoken-clean slice fails automatic quarantine; a wordless sentinel is valid only after a complete bound transcription run. |
| Written-text positive | Restricted text-positive controls with exact visible intervals; include title cards, captions, overlays, motion, low contrast, rotation, crop/letterbox, compression, and brief-display variants. | Frame-decode coverage, maximum sampling gap, frame/OCR hashes, timestamped OCR spans, matcher/policy version, and source projection. | Interval recall, source recall, and shortest-detected display duration. | Any labeled positive outside a matched, source-projected OCR interval fails certification. A decode/OCR gap or a display shorter than the certified coverage floor remains incomplete, never clean. |
| Written-text clean | Clean cards, captions, logos, historical graphics, homophones/near-matches, stylized fonts, and all target scripts. | Same full-duration OCR and matcher authority. | Source false-positive rate and per-script/slice maximum. | More than 1% false positives in any locked text-clean slice fails automatic quarantine; sparse scene-selected OCR cannot enter this metric. |
| Projection and containment | One positive source represented by multiple rendered derivatives plus at least one previously unflagged derivative; include stale, mismatched, and unmapped-negative fixtures. | Immutable mapping, complete source set, verdict-to-source relation, quarantine action log. | 100% propagation and zero unbound action. | Any missing/extra/mismatched mapping, stale evidence, or positive that reaches playable status fails. |
| Combined/direct-video boundary | Temporal or cross-modal ambiguity cases, including visual-only, spoken-only, and both; direct-video observations only where its predeclared escalation reason fires. | Lane provenance plus direct-video provider/evidence identity and stated escalation reason. | Correct hold/quarantine routing; no unsupported clear. | A lane disagreement, direct-video failure, or absent escalation evidence becomes a hold. No majority vote may clear a source. |

For a future release candidate, require zero observed false negatives on each
locked positive cell and a one-sided 95% lower confidence bound of at least
95% for each source-level positive recall metric. Using a one-sided 95% exact
Clopper-Pearson bound, zero misses requires at least 59 independent labeled
positive sources per metric; count source families, not derivatives, as
independent. These are proposed certification thresholds, not results from the
existing 48-case evidence.

## Fit with current Loomarr seams

Loomarr already orders transcription before tagging and records a non-empty
wordless sentinel so a completed no-speech observation is not re-run as missing.
[`internal/filler/stage_transcribe.go:11-38`](../../../internal/filler/stage_transcribe.go#L11-L38),
[`internal/filler/stage_transcribe.go:80-132`](../../../internal/filler/stage_transcribe.go#L80-L132).
That production stage is deliberately selective and calls `LanguageSpan`, which
samples 1-11 seconds on an ordinary clip; its stored transcript is tagging
evidence, not a full-duration safety attestation.
[`internal/filler/language.go:69-86`](../../../internal/filler/language.go#L69-L86),
[`internal/filler/stage_transcribe.go:100-108`](../../../internal/filler/stage_transcribe.go#L100-L108).
The split grounder likewise refuses a segment whose frame evidence is missing,
routing it to review rather than confirmation.
[`internal/filler/stage_split.go:85-137`](../../../internal/filler/stage_split.go#L85-L137).
And a review disposition atomically holds a clip out of rotation.
[`internal/filler/pipeline.go:1002-1019`](../../../internal/filler/pipeline.go#L1002-L1019).

The new lanes must live outside the tagging stamp: `VisionTagged` means selected
frames were read for taxonomy grounding, not that SCA screened the full local
video. Their durable evidence must bind to the same source-relative mapping
used by #903/#904; a rendered-case verdict is insufficient to clear a source.
The existing plan already reserves direct video for named temporal ambiguity and
requires a second model family only for listed uncertainty/safety cases.
[`filler-model-led-identification-plan-2026-08-31.md:127-133`](../filler-model-led-identification-plan-2026-08-31.md#L127-L133)

## First implemented spoken diagnostic

Issue [#908](https://github.com/loomarr/loomarr/issues/908) now implements the
portable spoken-language seam. The scanner population is the exact 300-case
corpus rather than the 48-case review sample; that distinction mattered because
the confirmed prohibited source was outside the review sample. The module
strictly revalidates the corpus manifest, every label-blind packet and external
media file, 298 complete-span transcript artifacts with one common engine
identity, the review evidence projection, construction authority, and a private
opaque-rule policy before atomically publishing a private report.

The initial real policy intentionally represents only the one confirmed
prohibited root and is not a comprehensive broadcast-language policy. An
exact-word diagnostic exposed an ASR token carrying a morphological suffix, so
the policy contract now distinguishes `exact_words` from an explicitly chosen
`token_prefix`; it does not silently broaden every rule. Two byte-identical
full-corpus executions produce private report SHA-256
`5c73b2fea954e6faa2b86462de0c814bb8e134ab5ca2270d881401d384e24482`:
306 sources comprise 1 prohibited source, 8 coverage holds (2 corpus cases
without complete packet media and 6 construction-only programme parents), and
297 no-signal observations. The 36 constructed derivatives contain none from
the prohibited source; 12 remain coverage-held and 24 have no observed spoken
signal. The report contains no policy phrase or transcript text and grants no
training, ingestion, scheduling, or production authority.

The first generated development challenge has now run. It is explicitly not a
certification corpus: its 59 positive controls are TTS transformations rather
than independent real source families, and its eight clean controls can expose
obvious regressions but cannot establish a 1% false-positive bound. The first
generated version proved the scorer's independence guard by being rejected when
two silence controls and two music controls repeated media hashes. The repaired
v2 authority has 67 distinct source hashes/family IDs, covers all nine positive
and four clean slices, and remains permanently marked `development`.

The pinned local `small.en` lane detected 17/59 positives (28.81%; one-sided
95% exact lower bound 19.26%), missed 42, produced 0/8 clean false positives,
and had no challenge coverage holds. Projection SHA-256 is
`91a95bfc5263df75213a23d35471bb30582fc9c2c62a4043730fae528e4a5c24`;
the corrected score SHA-256 is
`e8e5fa81f11cde3a2fbfaa5cae69408403ad459d442c8624c44514389fd73a40`.
Both reproduce byte-for-byte. The scorer now reports the actual
Clopper-Pearson lower bound on failed runs rather than collapsing every miss to
zero; certification still requires zero misses and a lower bound of at least
95%.

Two model-side changes were measured against the same controls:

| Transcript candidate | Positive detections | Clean false positives | Authority consequence |
| --- | ---: | ---: | --- |
| pinned local `small.en` | 17/59 | 0/8 | locked development score |
| same weights + private policy-vocabulary prompt | 24/59 | 0/8 | development diagnostic only |
| OpenRouter `openai/whisper-large-v3` | 22/59 | 0/8 | development diagnostic only; $0.002960712 |
| pinned local `large-v3-turbo` | 17/59 | 0/8 | development diagnostic only; 1.6 GB model |
| same large weights + private policy-vocabulary prompt | 22/59 | 0/8 | development diagnostic only |
| union of all five | 35/59 | 0/8 | still 24 positive misses |

The prompted transcript-set SHA-256 is
`ad1fefdd04690da20e9a30cb8dd84d70caf1da3f2d900c1d7c06979b949fea20`.
The hosted diagnostic report SHA-256 is
`d28514f2973b9120d2af2a12c25a19fbf5cb143bf0b688370149fd4d0da13f3b`;
its pre-run capability snapshot SHA-256 is
`4e2cd072d2279a737859e109f0caac5ed173e9582f9bfd2ba9b71da0476fcb44`
and listed every then-available endpoint as live and ZDR. It is deliberately
not certification evidence: OpenRouter documents that `/audio/transcriptions`
currently ignores chat-style provider ordering, fallback, data-collection, and
per-request ZDR controls, so the run cannot prove one pinned upstream route.
[OpenRouter STT](https://openrouter.ai/docs/guides/overview/multimodal/stt),
[transcription routing limitation](https://openrouter.ai/blog/tutorials/transcription-on-openrouter/).

The failure is recognition, not missing audio: hosted large-v3 returned
non-empty transcripts for all 37 misses. A deterministic edit-distance probe
found only two prompted positive misses one edit from the restricted root,
while one clean near-match was also one edit away. Broad fuzzy matching would
therefore buy little recall and immediately violate the clean boundary. An LLM
fine-tune is not justified by this evidence: a text model cannot recover an
acoustic token the transcript lost, and direct-audio model training would be a
different, much larger evidence program.

The larger local comparison is also complete. Full `ggml-large-v3-turbo.bin`
from the same immutable model-repository revision has SHA-256
`1fc70f774d38eb169993ac391eea357ef47c88757ef72ee5943879b7e8e2bc69`
and ran through the pinned v1.9.1 arm64 container runtime. Its normalized
transcript set reproduces SHA-256
`cd66b6b38c1e6a5ffa83ca8e116e3c4a79212f02ff6bdf4547c92e485487e008`;
the byte-identical transcript files have SHA-256
`200c63760ab7682929f446bbcb3a42184173112c6c5b198000f998dfdc4c0b7f`
and byte-identical scores have SHA-256
`4e145703c3be87ac846e4c6665346c92059a7bebe86a11f69454ced25f6f405a`.
It still detected only 17/59 positives, including zero of six quiet-speech
controls, with 0/8 clean false positives. It overlapped only 9 of the baseline
`small.en` detections and added two unique detections beyond the prior
three-lane union. That diversity lifts the four-lane union to 33/59, but does
not justify replacing the shipped 466 MB model with a 1.6 GB model or adding an
ASR ensemble that still misses 26 positives.

Private vocabulary context raises the large model to 22/59 and recovers two
additional cases beyond that four-lane union. Its transcript-set SHA-256 is
`34b027baa5e067d68b387e516ca6a35f97135a07d5c277f036857a7ae0f2e0b0`;
the private transcript file SHA-256 is
`fd4e2172b206d6f0731de8ea20def307fbf2f149a38ca2347212d19eb503af8a`
and the private score SHA-256 is
`da66f275d76aa7873ea96211d94254090035eba85745806618d2c6bb7e863ebf`.
The five-lane union therefore reaches 35/59, still leaving 24 misses. A
grammar-constrained keyword probe was also rejected before a full run: after
correcting whisper.cpp's required leading-space tokenization, the clean
homophone produced a higher-confidence constrained token stream than a known
positive. No threshold could retain that positive without also holding the
clean control. This closes the Whisper model-size, prompt, fuzzy-match, forced
grammar, and ensemble branches for this policy.

Next build a purpose-built phonetic/keyword spotter, using ambiguous acoustic
matches as holds rather than silently broadening prohibited policy. Apple
Speech remains optional, but its on-device recognizer on the current host is
available and its `contextualStrings` API is specifically designed to bias
short unusual vocabulary; a development-only comparison may be useful after
one-time authorization, with `requiresOnDeviceRecognition=true` and no network
fallback. [Apple contextual strings](https://developer.apple.com/documentation/speech/sfspeechrecognitionrequest/contextualstrings),
[on-device requirement](https://developer.apple.com/documentation/speech/sfspeechrecognitionrequest/requiresondevicerecognition).

## Tracked implementation issues

These three issues are dependents of #905 and #903. They are deliberately
separate so an optional Apple-only evaluator cannot redefine portable speech or
written-text certification.

1. [#907](https://github.com/loomarr/loomarr/issues/907),
   `filler-suitability-sca-capability-prototype`: build an evaluation-only signed
   macOS prototype and rights-cleared fixtures that establish entitlement,
   policy, local-video completion, result shape, and repeatability. Stop without
   production integration if the entitlement cannot be obtained or policy
   cannot be controlled for a locked run.
2. [#908](https://github.com/loomarr/loomarr/issues/908),
   `filler-suitability-whisper-spoken-language-certification`: add a versioned,
   complete-source safety transcript contract using the existing pinned Whisper
   engine/artifact machinery, then a restricted lexicon matcher; exact
   interval/source binding; wordless/ambiguous/error handling; private locked
   challenge builder and all spoken/projection rows above. Do not treat the
   selective catalog transcript as complete. No Apple Speech dependency or
   network fallback.
3. [#909](https://github.com/loomarr/loomarr/issues/909),
   `filler-suitability-written-text-certification`: define and measure bounded
   full-duration frame/OCR coverage, apply the same restricted matcher to
   timestamped OCR spans, and certify the written-text rows above. Sparse
   scene-selected Vision observations remain evidence only.

A later issue may join their certified observations with the existing bounded
direct-video route, but only after all lane-specific challenges pass. The
current direct-video design correctly treats video as a distinct capability,
not an implication of image vision.
[`filler-admission-confidence.md:149-163`](filler-admission-confidence.md#L149-L163)

## Primary sources consulted

### Integration recheck — 2026-09-06 UTC

The named API claims were rechecked against installed Xcode `MacOSX26.5.sdk`
and `iPhoneOS26.5.sdk` headers during #1116 integration. Header paths are relative
to each SDK's `System/Library/Frameworks/<framework>.framework/Headers/`:

- `SensitiveContentAnalysis/SCSensitivityAnalyzer.h:15-63` confirms policy gating,
  image/local-video methods, macOS 14/iOS 17 availability, progress, and completion.
  `SCSensitivityAnalysis.h:12-18` exposes the Mac result's Boolean sensitivity field;
  it provides no source interval or category taxonomy.
- `SensitiveContentAnalysis/SCVideoStreamAnalyzer.h:28-85` confirms iOS 26-only
  stream support, macOS unavailability, and the client-entitlement requirement.
- `Speech/SFSpeechRecognitionRequest.h:60-83,123-133` confirms recorded audio input
  and conditional on-device recognition. `SFTranscriptionSegment.h:19-61` confirms
  text, confidence, timestamps, and durations.
- `Vision/VNClassifyImageRequest.h:20-34` and `VNRecognizeTextRequest.h:25-31`
  describe image classification and text recognition, not complete safety coverage.

Apple's [machine-readable framework documentation](https://developer.apple.com/tutorials/data/documentation/sensitivecontentanalysis.json)
also identifies the image/video APIs and authorization entitlement; it is an
accessible first-party counterpart when the HTML documentation requires JavaScript.
This closes the API-documentation verification gap, not the runtime gate:
entitlement acquisition, enabled policy, device/locale availability, representative
recall, and certification remain unproven. No entitlement request, model run,
private-media analysis, or admission occurred in this recheck.

### Original research sources

- Apple, [Sensitive Content Analysis](https://developer.apple.com/documentation/sensitivecontentanalysis/),
  [SCSensitivityAnalyzer](https://developer.apple.com/documentation/sensitivecontentanalysis/scsensitivityanalyzer),
  and [video-file analysis](https://developer.apple.com/documentation/sensitivecontentanalysis/scsensitivityanalyzer/analyzevideofile%3Acompletionhandler%3A?language=objc).
- Apple, [Speech on-device support](https://developer.apple.com/documentation/speech/sfspeechrecognizer/supportsondevicerecognition),
  [Vision image classification](https://developer.apple.com/documentation/vision/vnclassifyimagerequest),
  and [Vision text recognition](https://developer.apple.com/documentation/vision/vnrecognizetextrequest).
- Installed Apple Xcode macOS 26.5 and iPhoneOS 26.5 SDKs:
  `SensitiveContentAnalysis.framework` and `Speech.framework`/`Vision.framework`
  headers named above (read 2026-09-02).
- ggml-org, [whisper.cpp primary source](https://github.com/ggml-org/whisper.cpp),
  plus the pinned Loomarr transcript command cited above.
