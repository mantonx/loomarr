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

## Exact next implementation issues

Create these three issues, in this order, as dependents of #905 and #903. They
are deliberately separate so an optional Apple-only evaluator cannot redefine
portable speech or written-text certification.

1. `filler-suitability-sca-capability-prototype`: build an evaluation-only signed
   macOS prototype and rights-cleared fixtures that establish entitlement,
   policy, local-video completion, result shape, and repeatability. Stop without
   production integration if the entitlement cannot be obtained or policy
   cannot be controlled for a locked run.
2. `filler-suitability-whisper-spoken-language-certification`: add a versioned,
   complete-source safety transcript contract using the existing pinned Whisper
   engine/artifact machinery, then a restricted lexicon matcher; exact
   interval/source binding; wordless/ambiguous/error handling; private locked
   challenge builder and all spoken/projection rows above. Do not treat the
   selective catalog transcript as complete. No Apple Speech dependency or
   network fallback.
3. `filler-suitability-written-text-certification`: define and measure bounded
   full-duration frame/OCR coverage, apply the same restricted matcher to
   timestamped OCR spans, and certify the written-text rows above. Sparse
   scene-selected Vision observations remain evidence only.

A later issue may join their certified observations with the existing bounded
direct-video route, but only after all lane-specific challenges pass. The
current direct-video design correctly treats video as a distinct capability,
not an implication of image vision.
[`filler-admission-confidence.md:149-163`](filler-admission-confidence.md#L149-L163)

## Primary sources consulted

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
