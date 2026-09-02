# Local phonetic / keyword-spotting options for spoken safety

**Scope.** This is a research recommendation and measured development result
for [#908](https://github.com/loomarr/loomarr/issues/908), not an implementation
decision or certification result. It follows the five-lane Whisper result in
[the local safety-lanes research](filler-suitability-local-safety-lanes-2026-09-02.md):
the union still missed 24 of 59 generated positive controls, and both fuzzy
matching and forced-grammar decoding collided with a clean near-match. The next
useful lane therefore needed a different acoustic model, not another text
matcher or an LLM over a lost transcript.

The 67 controls are TTS-derived development data. They are useful for rejecting
a bad candidate and selecting a provisional setting, but are not independent
real source families, training data, or certification evidence. The threshold
sweep below used the whole development set, so none of its rows is a held-out
estimate.

## Decision

Do **not** integrate sherpa-onnx open-vocabulary keyword spotting as a standalone
lane. Its generated-control result is strong, its timing evidence is usable,
and both release Linux architectures execute hermetically, but a frozen replay
against the actual 298-audio corpus creates 78-88 candidate cases while
recovering the one already-known positive. That recreates the manual-review
problem rather than solving it.

Vosk constrained grammar has now also failed that stop rule: it finds 35/59
generated positives with 0/8 generated clean false positives, but proposes
141/298 real corpus cases. Stop adding standalone decoders. Retain sherpa only
as a high-recall **proposal** stage whose short intervals must be adjudicated by
a second audio-capable model. An LLM belongs in that bounded second stage; the
evidence does not support training one. PocketSphinx is no longer worth an
automatic third bake-off.

## What a viable lane must produce

The lane receives complete, digest-bound 16 kHz mono WAVs and emits private hit
evidence:

- an opaque policy-rule ID and a digest of the private lexicon/configuration,
  never its text;
- a source-relative interval and a replayable frozen score/threshold decision;
- engine, native runtime, model, architecture, and WAV digests; and
- `hit`, `no-signal`, or `coverage-hold` -- unavailable artifacts, decode
  failures, unlocalizable hits, or ambiguous acoustic matches are holds, never
  clean results.

It must run locally without an API key or network fallback on Apple Silicon and
both release Linux architectures. A future 64 GB M5 Pro improves headroom and
throughput; it does not establish accuracy, portability, or certification. A
production dependency requires a design §14 amendment, and all Loomarr-owned
application logic remains Go.

For certification, freeze the selected engine, model, lexicon transform,
threshold, interval rule, and hold policy before an independent run. Require at
least the proposed 59 source-family-disjoint real positives, a substantially
larger independently clean corpus capable of exercising the 1% boundary, and
all transformation/locale slices. A hit may quarantine; neither a lane's miss
nor an ensemble's no-hit clears a source.

## Measured sherpa-onnx development prototype

The prototype uses official sherpa-onnx v1.13.7, published 2026-09-01, and the
official English GigaSpeech XL Zipformer KWS model. The model is a roughly 3.3M
parameter, open-vocabulary keyword recognizer rather than a phrase-specific
classifier; sherpa constrains its transducer decoder with a caller-supplied BPE
keyword list and exposes per-keyword boost and trigger-threshold controls.
[sherpa KWS design](https://k2-fsa.github.io/sherpa/onnx/kws/index.html),
[English model documentation](https://k2-fsa.github.io/sherpa/onnx/kws/pretrained_models/index.html),
[v1.13.7 release](https://github.com/k2-fsa/sherpa-onnx/releases/tag/v1.13.7).

Pinned private artifacts:

| Artifact | Size | SHA-256 |
| --- | ---: | --- |
| `sherpa-onnx-v1.13.7-osx-arm64-shared-no-tts.tar.bz2` | 18,192,670 bytes | `6a78081a617727ebb91a6449aaa9d98fa556272f8f7600a7c2308c9f100e2953` |
| `sherpa-onnx-v1.13.7-linux-x64-shared-no-tts.tar.bz2` | 24,522,109 bytes | `e5abe50fae5e25ad6b70bc74b51984ccea77df2571f211833b572fcc0d1c3bef` |
| `sherpa-onnx-v1.13.7-linux-aarch64-shared-cpu.tar.bz2` | 27,755,388 bytes | `7ab34c29ad9927e772f32be43efddd5e971987dc59e5f9aa3c09513348e4505b` |
| `sherpa-onnx-kws-zipformer-gigaspeech-3.3M-2024-01-01.tar.bz2` | 17,626,723 bytes | `f170013b4716e41b62b9bfd809687c207cef798ef9bc6534d524e17af9b6561a` |
| private BPE keyword authority | private | `7ff508af8c51995a9ce9e9822ba44ae8159f9aa7638b9d4acb0eff0547e6e9d8` |

The runtime digest matches GitHub's release-asset digest. The model digest
matches sherpa's published KWS checksum file. The extracted int8 model occupies
19 MB and the macOS runtime 58 MB. All raw keyword, detection, and result files
remain private mode `0600`; public notes contain no policy phrase.

The first known-positive/clean-near-match probe used sherpa's documented example
setting (`keywords_score=3`, `keywords_threshold=0.1`). It detected the positive
and did not fire on the clean near-match that scored above the positive under
Whisper forced grammar. The complete macOS-arm64 67-control sweep then produced:

| Score | Threshold | Positive detections | Clean false positives |
| ---: | ---: | ---: | ---: |
| 1 | 0.10 | 24/59 | 0/8 |
| 2 | 0.10 | 37/59 | 0/8 |
| 3 | 0.40 | 16/59 | 0/8 |
| 3 | 0.25 | 29/59 | 0/8 |
| 3 | 0.20 | 36/59 | 0/8 |
| 3 | 0.10 | 40/59 | 0/8 |
| 3 | 0.05, 0.02, or 0.01 | 40/59 | 0/8 |
| 4 | 0.10 | 42/59 | 0/8 |
| **4** | **0.05** | **45/59** | **0/8** |
| 4 | 0.02 | 45/59 | 0/8 |
| 5 | 0.10 | 36/59 | 0/8 |
| 5 | 0.05 | 44/59 | 0/8 |

The provisional `score=4`, `threshold=0.05` row detected 5/7 accent/locale,
5/7 clipping, 5/7 codec, 7/7 derivative/compilation, 7/7 music-overlap,
5/6 partial-token, 5/6 phonetic-confusable, 2/6 quiet-speech, and 4/6
speed/pitch controls. Its eight clean controls comprise two each of clean
near-match, music-only, target-locale, and wordless content. Eight controls are
far too few to estimate a 1% false-positive ceiling.

This row adds 16 detections outside the five-Whisper union. The combined
development union rises from 35/59 to 51/59, leaving eight positive misses. The
private result JSONL SHA-256 is
`ba8eefafe3244a76b6aa963c6abbc7323fe237515eb7eafa3fe54c212b90a6c9`;
the redacted score SHA-256 is
`d421b04a0d85879e707494fbf8edfe9a4db43757bbfa974f525ffc68d792116b`.
An exact repeat reproduced both the detection IDs and raw JSONL byte-for-byte.

The provisional setting was then replayed without network access in read-only
Debian containers using the exact official release artifacts. It was not
retuned for either platform:

| Runtime | Unique positive sources | Raw hit events | Clean false positives | Adds beyond Whisper | Combined union |
| --- | ---: | ---: | ---: | ---: | ---: |
| macOS arm64 | 45/59 | 45 | 0/8 | 16 | 51/59 |
| Linux arm64 | 43/59 | 44 | 0/8 | 15 | 50/59 |
| Linux amd64 | 43/59 | 43 | 0/8 | 15 | 50/59 |

The two release Linux architectures produce the same 43 unique source
decisions, and each reproduces its raw result byte-for-byte on an exact repeat.
Linux arm64 emits a second intra-positive event for one already-detected source;
amd64 emits one event for it. All 44 arm64 and 43 amd64 events overlap an
authored positive interval. macOS additionally detects two borderline positive
sources. Therefore the release-facing development result is **43/59,
0/8 clean, and 50/59 in union with Whisper**, not the more favorable macOS
number. The lane is deterministic per pinned platform artifact but not bit- or
event-identical across platforms. A durable worker must retain all bounded hit
intervals, freeze architecture-specific runtime identity, and verify semantic
source verdicts rather than pretending raw floating-point inference is
cross-platform identical.

### The real corpus is the stop condition

The provisional Linux-arm64 setting was frozen and run, without network access,
against the finalized manifest-backed corpus: 298 complete audio cases, 297
unique WAVs, and 6.27 hours. It emitted 130 raw events across 88 cases. The one
source already prohibited by the complete Whisper projection was among them;
the other 87 cases had previously produced only `no_spoken_signal_observed`.
That earlier disposition is not ground truth -- Whisper can miss the term -- but
a 29.5% candidate rate is operationally incapable of replacing broad manual
review.

The already-measured stricter setting (`score=4`, `threshold=0.10`) did not fix
the problem. It emitted 117 events across 78 cases, again retaining the known
source. Only 71 case decisions were common to both settings: the stricter run
removed 17 candidates but introduced seven different ones. Decoder pruning
therefore makes this an operating-point change, not a monotonic confidence
filter.

Existing independent model evidence reinforces the stop without asking the
maintainer to inspect those candidates. Of the 47 prior real cases that join
cleanly to the source projection, 12 are sherpa candidates. Gemini 3.7 Flash
reported no prohibited signal for ten, the known prohibited signal for one,
and one operational failure. Qwen 3.8 reported eight coverage holds, two
no-signal results, the same prohibited source, and one failure. These broad
model assessments are not labels or a false-positive estimate, but they show
that sherpa's queue is not a defensible set of automatic prohibited verdicts.

Private result SHA-256 values are
`03608e6a375ee425015c94caaefcd92dc3cf001c1768d4410d800f992c036d0b`
for the 0.05 raw JSONL and
`d77cb05279226f695093db08da6bb945cec4721518bf43c32450fc7a3a6fa519`
for 0.10. Their redacted diagnostics are
`075610140e008bf19c1b9d56cbfb2fffaa28af86a029161bd32837bd96c889c1`
and
`96915d670928f376e612d408608d17d11f2c00fcf664669cf93b018fe2c72490`.
All remain private mode `0600`. An abandoned 149-audio staging directory was
identified and excluded before any result was accepted; this is why a future
worker must consume a manifest authority rather than a directory glob.

### Vosk confirms the standalone-decoder stop

The Vosk comparison used `vosk==0.3.45` on pinned Python 3.11 arm64, the
official `vosk-model-small-en-us-0.15`, and one locked grammar containing the
private target plus `[unk]`. The model archive SHA-256 is
`30f26242c4eb449f948e42cb302dd7a686cb29a3423a8367f99ff41780942498`;
the private grammar SHA-256 is
`d718c7fe6f4281d108104c85e9a19468d8eedee0e5e8bb162d1c6caad77b558c`.
The model catalogue labels this 40 MB download Apache-2.0 and approximately
300 MB at runtime. [Vosk model catalogue](https://alphacephei.com/vosk/models).

On generated controls it detected 35/59 positives, all with an interval
overlapping authored truth, and 0/8 clean false positives. It added seven
sources outside the five-Whisper union, taking that union to 42/59. The private
control result and score SHA-256 values are
`762f3b8dd0a0468f066f61818dd57048cb2d01b77bfaad0ac49a167742b8927c`
and
`f333471adb58fd21684fddca21dc195fcc6f4021d4c15dfa80c12b16ce164554`.

The unchanged grammar then emitted 299 events across **141/298** finalized real
audio cases. It retained the known prohibited source; the other 140 had only a
prior Whisper `no_spoken_signal_observed` disposition. Among the 47 prior
direct-video cases that join to the source projection, 19 are Vosk candidates;
Gemini reported no prohibited signal in 15, two prohibited outcomes (only one
with an audio flag), and two failures. The private real result SHA-256 is
`f4319c926a19f48be9417588022a5886cbf04bb4678bf94c0bccec1ed0eebe4e`;
its redacted diagnostic is
`75cf3820f3436f87227eaecbe11e0c8456b88f2a42e9252aad48d2cc5644d0f4`.

Vosk's explicit unknown alternative did not produce the hoped-for precision.
The generated clean controls were not representative enough to expose the
problem. This closes the standalone KWS/constrained-ASR branch rather than
justifying another grammar or threshold tuning loop.

### Timing evidence is available

The initial paper comparison incorrectly treated sherpa as event-only. The
v1.13.7 result contract contains `start_time`, decoded BPE `tokens`, and one
timestamp per token; the C API documents the same fields and JSON shape.
[C++ result contract](https://github.com/k2-fsa/sherpa-onnx/blob/v1.13.7/sherpa-onnx/csrc/keyword-spotter.h#L20-L50),
[C API result](https://k2-fsa.github.io/sherpa/onnx/c-api/html/structSherpaOnnxKeywordResult.html).
The implementation converts decoder frame positions using the model frame
shift and subsampling factor.
[timestamp conversion](https://github.com/k2-fsa/sherpa-onnx/blob/v1.13.7/sherpa-onnx/csrc/keyword-spotter-transducer-impl.h#L27-L55).

For this development replay, the source-relative interval from the first token
timestamp through the last token timestamp plus one 40 ms subsampled frame was
0.20-1.20 seconds wide. All 45 detected positives overlapped the corresponding
authored positive interval. This establishes that bounded interval evidence is
possible; the interval construction still must be specified, tested at segment
boundaries/endpoints, and frozen before certification.

### The remaining sherpa blockers

1. **Model rights.** The archive's embedded model-card metadata says `Apache
   License 2.0`, but the archive contains no `LICENSE` or `NOTICE`, and the
   upstream project has redirected license questions to an open request for
   explicit weight, commercial-use, and redistribution terms. Treat that as
   insufficient for shipping, not as a reason to discard the measured engine.
   [license request](https://github.com/k2-fsa/sherpa-onnx/issues/3802),
   [engine license](https://github.com/k2-fsa/sherpa-onnx/blob/v1.13.7/LICENSE).
2. **Cross-platform contract.** The exact official Linux amd64 and arm64
   artifacts now execute and agree on unique source decisions, but raw event
   count differs on one positive source and macOS detects two additional
   positives. Certification must use the actual shipping artifact per
   architecture and predeclare whether equivalence is source-verdict,
   interval-overlap, or exact-event based. It cannot require an impossible
   cross-platform raw digest match.
3. **Boundary design.** The most conservative production shape is a required,
   exec'd worker with a small Go adapter, not cgo in the main Loomarr process.
   Its protocol must omit the raw keyword, bind every digest, bound memory and
   runtime, reject malformed/multiple results, and fail closed.
4. **Independent evidence.** The chosen setting was selected on the same
   generated rows reported above. It earns a locked real-source experiment,
   not a recall claim, automatic admission, or permission to ingest.

## Ranked alternatives

| Rank | Candidate | Evidence shape and trade-off | Current verdict |
| --- | --- | --- | --- |
| 1 | **Bounded acoustic proposal + audio-capable LLM** | sherpa supplies short source-relative intervals; a distinct direct-audio model decides only whether the private target is audible in each bound window. The second stage sees audio rather than a lossy transcript. | **Build and calibrate next.** One deep module must own manifest validation, cropping, pinned route/spend, redaction, replay evidence, and fail-closed outcomes. |
| 2 | **sherpa-onnx Zipformer KWS** | Distinct GigaSpeech-trained acoustic model, arbitrary private BPE keywords, token timing, tiny footprint, and release-Linux 43/59 with 0/8 generated clean controls. The same frozen setting proposes 88/298 real cases; a stricter setting still proposes 78. | **Reject as a standalone verdict lane.** Retain only as an interval proposer for automated second-stage verification. Weight-license provenance still blocks shipping. |
| 3 | **Vosk small English with locked grammar** | Kaldi/Vosk supplies word start/end/confidence and an explicit unknown path, but the frozen comparator still proposes 141/298 real cases after 35/59 generated recall. | **Reject as a standalone lane.** Do not tune another grammar. [Vosk API](https://github.com/alphacep/vosk-api/blob/master/src/vosk_api.h#L132-L209), [model catalogue](https://alphacephei.com/vosk/models). |
| 4 | **PocketSphinx keyphrase search** | Direct HMM/phonetic search with frame segmentation and permissive source/model terms, but older US/Canadian-English acoustics, pronunciation-dictionary work, and no maintained upstream Go boundary. | **Do not run.** Two newer distinct decoders already establish the real-corpus failure mode. [KWS options](https://github.com/cmusphinx/pocketsphinx/blob/main/src/config_macro.h#L146-L158), [segment APIs](https://github.com/cmusphinx/pocketsphinx/blob/main/include/pocketsphinx.h#L964-L1008). |
| 5 | **Custom wake-phrase model or vendor lane** | Training requires governed positive/adversarial-negative corpora and an independent holdout. Porcupine externalizes custom keyword setup; Apple Speech is platform-only ASR. | **Reject now.** [openWakeWord training and licensing](https://github.com/dscripka/openWakeWord#training-new-models), [Apple contextual strings](https://developer.apple.com/documentation/speech/sfspeechrecognitionrequest/contextualstrings). |

## Why using an LLM is different from training one

A text LLM cannot recover a policy token that ASR failed to represent. A
direct-audio LLM or custom keyword model would be a new labelled-audio training
programme, not a shortcut around data collection. The generated controls are
neither representative training data nor an independent holdout. They do show
that a small task-specific decoder can recover Whisper misses, while the real
corpus shows that it cannot decide safely by itself. An existing audio-capable
LLM can still be useful as a second opinion over only the proposed intervals;
that is inference and calibration, not fine-tuning.

The standalone decoder search is closed. Build a two-stage diagnostic: sherpa
proposes bounded audio intervals; a local or snapshot-pinned ZDR audio-capable
model judges only those intervals; disagreements and operational failures
remain holds. Current OpenRouter catalog data lists Gemini 3.8 Flash with native
audio input, while Qwen 3.8 exposes video but not direct audio; Claude currently
offers neither audio nor video input there.
[OpenRouter models catalog](https://openrouter.ai/api/v1/models?output_modalities=text).
Authenticated snapshot SHA-256
`be19f594987572b193beadd1f9f24148d858b9c611a7b6b02e225dd3d19fef59`
confirms a live ZDR Google Vertex route for pinned
`google/gemini-3.8-flash-20260902` with strict structured output. It also
confirms Qwen 3.8 27B as video-only and the newer Claude Fable 5.1 as
text/image/file-only. No paid inference was made. Lock the cascade before the
independent real positive and clean challenge. Do not enter another
model/threshold bake-off.
