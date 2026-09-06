package app

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/loomarr/loomarr/internal/mediatools"

	"github.com/loomarr/loomarr/internal/clipfetch"
	"github.com/loomarr/loomarr/internal/diagnostics"
	"github.com/loomarr/loomarr/internal/filler"
	"github.com/loomarr/loomarr/internal/library"
	"github.com/loomarr/loomarr/internal/llm"
	"github.com/loomarr/loomarr/internal/metrics"
	"github.com/loomarr/loomarr/internal/programmer"
	"github.com/loomarr/loomarr/internal/store"
)

// This file holds the composition root's per-subsystem builders (§14.1).
//
// ⚠ **The shape matters, and it is NOT the one §14.1 rejected.** That rejection was for methods
// on a shared builder struct, which would convert ~70 composition locals into fields on a
// mutable carrier — widening their scope and trading compile-time use-before-assignment errors
// for runtime nils. A `buildX` FUNCTION takes only what it needs and RETURNS values, so nothing
// widens and the compiler still catches use-before-assignment. §14.1 records the distinction.
//
// The measurement that makes this tractable: the 434-line filler section reads only
// EIGHT locals from earlier sections, while its 133-line suggester section reads far more.
// Coupling, not size, predicts extraction cost — extract along the seam, not the line count.

// buildTagger constructs the AI-tagging provider and the manual whole-catalog tagger (§10).
//
// Returns BOTH because they have different lifetimes and different consumers: the provider is
// also handed to the ingest pipeline's tag rung (§10 V51b), which is why it is hoisted out of
// the `if` rather than living inside the tagger. Nil for both is the honest un-opted-in state,
// and every reader treats it that way — the manual sweep becomes a no-op, and the rung reports
// "no language model is configured" on each clip's ladder rather than silently doing nothing.
func buildTagger(st store.Store, set resolved, layout filler.Layout, log *slog.Logger, wake *fillerChannelWake, recorder *metrics.Recorder) (llm.Provider, *filler.Tagger) {
	if !set.boolv("filler.ai_tagging") {
		return nil, nil
	}
	provider := activeFillerProvider(set, recorder)
	if provider == nil {
		return nil, nil
	}

	// The generation's clip-root FS lets tagging read the info-JSON sidecars ingest writes beside
	// each clip (§10). A zero layout yields nil and tagging falls back to filenames.
	tagger := filler.NewTagger(fillerTagStoreAdapter{st: st, wake: wake}, provider, layout.FS(), time.Now, log).
		// Auto-filing (§10 V38): a held clip whose grounding-capped score clears the threshold
		// is filed without a human. Closures, not captured values, so a changed threshold
		// applies on the next run rather than the next restart.
		//
		// ⚠ `boolv`, NOT `boolOn`. The two differ only when the settings service cannot answer,
		// and here that difference is the whole safety property: `boolOn` fails OPEN (returns
		// true), which would publish unreviewed clips to live channels exactly when the install
		// is degraded. Holding is the safe failure.
		WithAutoFile(filler.AutoFilePolicy{
			Enabled:       func() bool { return set.boolv("filler.autofile.enabled") },
			MinConfidence: func() int { return set.intv("filler.autofile.min_confidence") },
			SourceAllowed: fillerSourceAutoAdmit(st),
		})

	return provider, tagger
}

// fillerSourceAutoAdmit resolves the source-specific admission decision at run time (§10 V57).
// The folder policy owns hand-copied, manual-ingest and pre-provenance clips. A named source that
// cannot be resolved fails closed; silently treating an unknown id as trusted would turn broken
// attribution into publication authority.
func fillerSourceAutoAdmit(st store.FillerSourceStore) func(context.Context, string) (bool, error) {
	return func(ctx context.Context, source string) (bool, error) {
		if source == "" || source == "filler-dir" {
			source = "folder"
		}
		sources, err := st.ListFillerSources(ctx)
		if err != nil {
			return false, err
		}
		for _, candidate := range sources {
			if candidate.ID == source {
				return candidate.AutoAdmit, nil
			}
		}
		return false, nil
	}
}

// buildSyncer constructs the catalog syncer and its scan sources (§10 V38c).
//
// ⚠ Policy remains live, while the storage layout is immutable for this application generation.
// The source switch and minimum duration therefore remain closures, but scan and intake share the
// one captured root/watch pair so a settings write cannot move files between generations.
//
// ⚠ The library scanner stays wired when no media server is configured, and that is a SUPPORTED
// install rather than a degraded one: its adapter maps the explicit unconfigured result to an
// empty optional source, so folder rows still drain and a saved connection enables the next scan.
func buildSyncer(st store.Store, set resolved, layout filler.Layout, log *slog.Logger,
	fillerProg *programmer.Tunarr, lib *library.Client) *filler.Syncer {
	src := filler.DirSource{
		Layout: layout,
		Probe:  filler.FFprobeNextTo(set.str("playout.ffmpeg_path")),
		// ⚠ **Artwork was relying on its nil default, which ignored `playout.ffmpeg_path`
		// entirely** and shelled out to whatever `ffmpeg` PATH resolved to. An operator who
		// points that setting at a custom build (the whole reason it exists — see the
		// hardware-encode notes) got their frames from a DIFFERENT binary than playout uses,
		// silently, and the setting appeared to do nothing here.
		Artwork: filler.FFmpegArtwork(set.str("playout.ffmpeg_path")),
		// The quality gate's floor (§10 V40).
		MinDuration: func() time.Duration { return set.dur("filler.min_duration") },
		// ⚠ **Log was never assigned either**, so the "some thumbnails could not be generated"
		// warning has never once been emitted. That count exists precisely because extraction is
		// best-effort and failures are skipped — the shape that already produced one
		// silently-empty catalog in this repo's history (see FFprobeNextTo). A generator that
		// counts failures into a logger nobody wired is the same silence with extra steps.
		Log: log.Warn,
	}
	// Keep the adapter present across the process lifetime, but resolve availability per
	// call. An install may start internal-only and add Tunarr later; boot-gating this field
	// left every clip without a Tunarr program uuid until restart. The adapter itself no-ops
	// while the URL is empty, so local-only scans stay quiet and fully supported.
	src.Tunarr = fillerSourceAdapter{
		prog:       fillerProg,
		configured: func() bool { return set.str("tunarr.url") != "" },
	}

	syncer := filler.NewSyncer(src, fillerStoreAdapter{st}, layout, time.Now, log).
		WithEnabled(func() bool { return set.boolOn("filler.source.folder.enabled") }).
		WithAcquisitionAuthority(st)

	// Keep the library scanner wired while the connection is empty. The adapter treats the
	// library module's explicit unconfigured result as an empty optional source, then starts
	// using a newly saved connection on the next scan without rebuilding the syncer.
	libScanner := filler.NewLibraryScanner(
		fillerLibraryAdapter{lib},
		func(msg string, args ...any) { log.Warn(msg, args...) },
	)
	return syncer.WithScanSources(fillerScanSourceAdapter{st}, libScanner)
}

// buildFetcher constructs the clip downloader (§10, §16). Nil when ffmpeg is absent, which is a
// normal state on `loomarr:latest` rather than an error — the `ingest` feature gate reports it.
//
// ⚠ **TWO downloaders, resolved INDEPENDENTLY** (§10 V38b, wiring fixed V38c.8). archive.org is
// fetched over plain HTTP and needs only ffmpeg; yt-dlp is for YouTube and shells out to ffmpeg
// itself. Requiring BOTH was the V38b defect surviving in the wiring after the feature GATE was
// split, and the result was worse than the original bug: `features.ingest` reported true
// (correctly — ffmpeg is present), the Sources rows offered "Fetch now", and every archive fetch
// then failed at the point of use with "ingest tooling not present in this image". Two claims
// that cannot both be true, which is the exact shape that started V38b.
//
// ⚠ An UNSET path falls back to a PATH lookup, matching `settings.toolRunnable` — §15 has always
// described these as defaulting to the vendored binaries, and only the Docker image set them, so
// a source build had ingest off with the tools installed.
func buildFetcher(set resolved, layout filler.Layout, log *slog.Logger, artifacts clipfetch.ArtifactWriter) *clipfetch.Ingestor {
	ytPath := resolveTool(set.str("ingest.ytdlp_path"), "yt-dlp")
	ffPath := resolveTool(set.str("ingest.ffmpeg_path"), "ffmpeg")
	if ffPath == "" {
		return nil
	}

	// ⚠ A nil YouTube downloader is FINE — `downloaderFor` returns nil per kind and the Ingestor
	// counts that source as failed rather than dying. So a box with ffmpeg and no yt-dlp fetches
	// archive collections (the seeded ones) and reports honestly on playlists, instead of
	// refusing everything.
	var ytDL clipfetch.Downloader
	if ytPath != "" {
		ytDL = clipfetch.NewYtDlpDownloader(ytPath, ffPath)
	}
	log.Info("filler ingest available", "ytdlp", orNone(ytPath), "ffmpeg", ffPath)
	return clipfetch.New(ytDL, clipfetch.NewArchiveDownloader(), layout.WatchDir(), log).WithArtifactWriter(artifacts)
}

// buildSplitter constructs the compilation splitter (§10, V34). Nil without a drop-folder — clip
// paths are relative to it, so there is nothing to cut.
//
// ⚠ ffmpeg/ffprobe come from `playout.ffmpeg_path`, a core runtime dep on the single image, NOT
// from the ingest pair. Splitting works on files already on disk and must not die just because
// yt-dlp is absent. whisper is optional: without it, over-long segments come back Unsplittable
// rather than guessed (§15).
//
// The LLM provider wires whenever one is configured — splitting's rescue and classification are
// operator-invoked, so they are not gated by `filler.ai_tagging`, which gates the batch job.
func buildSplitter(st store.Store, set resolved, layout filler.Layout, log *slog.Logger, wake *fillerChannelWake, recorder *metrics.Recorder) *filler.Splitter {
	dir := layout.ClipDir()
	if dir == "" {
		return nil
	}

	splitProvider := activeFillerProvider(set, recorder)

	tools := buildFillerMediaTools(set, recorder)

	// The same live minimum is enforced during detection and at the scan boundary (§10 V34).
	return filler.NewSplitter(fillerSplitStoreAdapter{st: st, wake: wake}, tools, splitProvider, dir,
		func() time.Duration { return set.dur("filler.min_duration") }, newID, time.Now, log)
}

// activeFillerProvider resolves the same branded provider selection as the AI surface. OpenRouter
// credentials live under llm.api_key.openrouter rather than the legacy base key; every filler text
// path must therefore build from Selection instead of reconstructing the wire from registry rows.
func activeFillerProvider(set resolved, recorder *metrics.Recorder) llm.Provider {
	sel := resolveSelection(set)
	if sel.URL == "" {
		return nil
	}
	return buildProviderFor(sel, recorder)
}

// buildFillerMediaTools selects local whisper or hosted timed transcription behind the same
// MediaTools interface. Every selector is a closure: changing provider, model, URL or key applies
// to the next span without restarting, matching the rest of the filler settings contract.
func buildFillerMediaTools(set resolved, recorder *metrics.Recorder) *mediatools.FFmpegTools {
	ffmpegPath := set.str("playout.ffmpeg_path")
	tools := mediatools.NewFFmpegTools(ffmpegPath, filler.FFprobePathNextTo(ffmpegPath),
		set.str("ingest.whisper_path"), set.str("ingest.whisper_model"), "")
	hosted := &mediatools.HostedTranscriber{
		FFmpegPath: ffmpegPath,
		Client: func() mediatools.AudioTranscriptionClient {
			sel := resolveSelection(set)
			if sel.URL == "" {
				return nil
			}
			return hostedSTTAdapter{llm.NewOpenAIForProvider(sel.Provider, sel.URL, set.str("filler.transcribe.model"), sel.APIKey).WithMetrics(recorder)}
		},
		Model: func() string { return set.str("filler.transcribe.model") },
	}
	return tools.WithTranscriber(func() mediatools.SpanTranscriber {
		if set.str("filler.transcribe.provider") != "hosted" {
			return nil
		}
		return hosted
	})
}

// buildPipeline constructs the ingest pipeline: one driver over nine rungs (§10 V51b/V61).
//
// ⚠ **This block was measured as an 11-input seam and skipped on that basis. The measurement
// was wrong, and the way it was wrong is worth keeping.** The window had been drawn at the
// `pipelineStages` slice, so `langDetect`, `fillerTools`, `fillerDrop`, `visionProvider` and
// `clipDir` — all of them DEFINED in the preamble immediately above and referenced nowhere
// else in the composition root — counted as inputs crossing the boundary. They are locals of this
// unit, not couplings to the rest of the composition root. Widened to the whole unit, the
// external inputs are the six parameters below, which is `buildSyncer`'s number.
//
// The lesson generalises past this function: a coupling count is a property of the BOUNDARY,
// not of the code, and a boundary drawn mid-unit reports the unit's own locals as coupling.
// Measure from the first line the unit owns, or the number means nothing.
//
// ⚠ Every rung is registered UNCONDITIONALLY, even when the thing it needs is absent — a stage
// missing from the slice reports "not available on this install" on every clip's ladder, while
// one that is present says why it skipped in the operator's own terms. That is what makes the
// ladder explain an install rather than merely show gaps in it. Do not make registration
// conditional to "clean up" the nil cases.
func buildPipeline(st store.Store, set resolved, layout filler.Layout, log *slog.Logger, emitter *eventEmitter,
	splitter *filler.Splitter, taggerProvider llm.Provider, wake *fillerChannelWake,
	processDiagnostics *diagnostics.ProcessManager, admissionObserver filler.AdmissionObserver,
	recorder *metrics.Recorder) *filler.Pipeline {
	// The language gate (§10 V40). Registered unconditionally: `filler.language` empty makes
	// Run a no-op, so an install that has not opted in pays nothing and the Tasks row still
	// exists to be seen and paused.
	//
	// ⚠ The DETECTOR is chosen at construction, not per run, because the two backends need
	// entirely different collaborators — one a local binary and a model file, the other an
	// HTTP client with a key. `filler.language` and the reject rule still hot-apply; changing
	// the PROVIDER needs a restart, which is the same bargain `llm.provider` makes.
	var langDetect filler.LanguageDetector
	if set.str("filler.language_provider") == "hosted" {
		// ⚠ Its OWN client rather than the tagger's. The tagging provider is built inside
		// `if filler.ai_tagging && llm.url != ""`, so reusing it would silently tie the
		// language gate to a setting that has nothing to do with it — switch AI tagging off
		// and clips would stop being checked, with nothing saying why.
		//
		// ⚠ Nil asker ⇒ the detector reports "cannot tell" and the gate keeps every clip.
		// That is the honest state for an install that selected `hosted` without configuring
		// a service URL: inert, not broken, and not silently deleting things. A key is not a
		// universal prerequisite because a Custom OpenAI-compatible endpoint may be keyless.
		// ⚠ **CLOSURES, not resolved values.** The first cut called `set.str(...)` here and
		// baked the URL, model and key into a client at boot — so changing `llm.model` in
		// Settings did nothing, the detector kept calling whatever was configured at startup,
		// and every clip failed with a real 404 ("No endpoints found that support input
		// audio") about a request the operator thought they had already fixed. Cost a live
		// debugging session to find, because the error was accurate and the config looked right.
		//
		// Everything else in this feature reads live; the one setting that decides whether the
		// backend can work at all must too.
		langDetect = filler.NewHostedLanguage(
			func() filler.AudioAsker { return hostedLanguageAsker(set, recorder) },
			func() string { return set.str("llm.model") },
			set.str("playout.ffmpeg_path"), "")
	} else {
		// ⚠ `filler.language_model`, NOT `ingest.whisper_model`. The latter is
		// `ggml-small.en.bin` — an ENGLISH-ONLY build that does not identify languages at all,
		// so pointing this at it would answer "en" for every clip on earth and the gate would
		// silently never reject anything.
		langDetect = filler.NewWhisperLanguage(
			set.str("ingest.whisper_path"), set.str("filler.language_model"),
			set.str("playout.ffmpeg_path"), "")
	}
	// The ffmpeg tooling the metadata rungs share (a core runtime dep — NOT the ingest pair, so
	// they run on files already on disk regardless of whether yt-dlp is present).
	fillerTools := buildFillerMediaTools(set, recorder)

	// The generation's clip-root FS reads the info-JSON sidecars ingest writes beside each clip.
	// nil ⇒ every clip reads as thin-sourced and filename-only tagged.
	fillerDrop := layout.FS()

	// Vision: keyframes → a multimodal model, resolved LIVE (§10 V54a).
	//
	// ⚠ **`filler.vision.enabled` is the only part still read at boot**, and only to decide whether
	// to wire anything at all; the endpoint itself is resolved per call by `hotVisionProvider`, so
	// changing the provider, URL, key or model in Settings applies to the next clip. This used to
	// build the provider here, from `llm.url` and `llm.api_key`, behind an unlogged
	// `&& set.str("llm.url") != ""` — so an install with vision enabled and no reachable endpoint
	// was indistinguishable from one with vision switched off.
	var visionProvider llm.VisionProvider
	if set.boolv("filler.vision.enabled") {
		h := &hotVisionProvider{set: set, log: log, metrics: recorder}
		// Resolve ONCE at boot purely to report it. A failure here is not fatal — the operator may
		// fix the setting without restarting, which is the entire point of resolving per call —
		// but it must not be silent, because this is the diagnosis for "grounding never runs".
		if _, err := h.resolve(); err != nil {
			log.Warn("filler vision enabled but not currently reachable; grounding will no-op until this is fixed",
				"err", err)
		}
		visionProvider = h
	}
	// ── The ingest pipeline (§10 V51b) ────────────────────────────────────────────────────
	//
	// One driver over nine rungs, replacing `filler-language`, `filler-split`,
	// `filler-transcribe` and `filler-vision`.
	//
	// ⚠ **Every rung is registered unconditionally, even when the thing it needs is absent.**
	// A stage missing from this slice is reported as "not available on this install" on every
	// clip's ladder, while one that is present says WHY it skipped in the operator's own terms
	// ("vision tagging is off", "no language backend is configured"). Registering all of them
	// and letting `Applies` answer is what makes the ladder explain an install rather than
	// merely show gaps in it — the same visible-but-idle contract the Tasks page rows use.
	clipDir := layout.ClipDir()
	pipelineStages := []filler.Stage{
		filler.NewProbeStage(
			filler.FFprobeNextTo(set.str("playout.ffmpeg_path")), fillerPipelineClipAdapter{st}, clipDir,
			func() int64 { return set.dur("filler.min_duration").Milliseconds() },
			func() time.Duration { return set.dur("filler.autosplit.max_duration") }, time.Now),
		filler.NewTranscodeStage(
			fillerPipelineClipAdapter{st}, filler.FFprobeNextTo(set.str("playout.ffmpeg_path")),
			clipDir, mediatools.DefaultMezzanine(),
			func() string { return set.str("playout.ffmpeg_path") },
			// ⚠ The loudness target is applied only when the operator opted in. This is the
			// FIRST production caller of on-file loudness normalisation: V42 built the pass,
			// and `filler.autofile.normalize_loudness` gated a function nothing called, so the
			// setting has been inert since it shipped. Folding it into the encode that is
			// happening anyway is what finally wires it.
			func() float64 {
				if !set.boolv("filler.autofile.normalize_loudness") {
					return 0
				}
				// ⚠ `filler.target_lufs` is a STRING setting (an empty value means "no
				// normalisation at all", which no numeric kind can express), so it is parsed
				// here. An unparseable value yields 0 — the same as off, which is the safe
				// direction: the alternative is applying a garbage target to the operator's
				// audio.
				lufs, err := strconv.ParseFloat(set.str("filler.target_lufs"), 64)
				if err != nil {
					return 0
				}
				return lufs
			}, time.Now).WithMediaDerivatives().WithConditioning(fillerTools.MeasureConditioning).WithDiagnostics(processDiagnostics),
		filler.NewLanguageStage(langDetect, fillerLanguageStoreAdapter{st}, clipDir,
			func() string { return set.str("filler.language") }, time.Now),
		filler.NewTranscribeStage(fillerTools, fillerTranscribeStoreAdapter{st}, clipDir, fillerDrop,
			func() bool { return set.boolv("filler.transcribe.enabled") }, time.Now),
		filler.NewTagStage(taggerProvider, fillerTagStoreAdapter{st: st, wake: wake}, fillerDrop, time.Now),
		filler.NewVisionStage(fillerTools, visionProvider, fillerVisionStoreAdapter{st}, clipDir,
			func() bool { return set.boolv("filler.vision.enabled") }, time.Now),
		// V61 shadow execution is the fail-closed seam immediately before the V38 compatibility
		// gate. It records only production facts with known provenance; score remains filing
		// authority until certification explicitly enables a slice.
		filler.NewAdmissionStage(admissionObserver),
		filler.NewScoreStage(fillerTagStoreAdapter{st: st, wake: wake}, &filler.AutoFilePolicy{
			// ⚠ `boolv`, the FAIL-CLOSED read, not `boolOn`. The two differ only when the
			// settings service cannot answer, and here that difference is the safety property:
			// failing OPEN would publish unreviewed clips to live channels exactly when the
			// install is degraded.
			Enabled:       func() bool { return set.boolv("filler.autofile.enabled") },
			MinConfidence: func() int { return set.intv("filler.autofile.min_confidence") },
			SourceAllowed: fillerSourceAutoAdmit(st),
		}, func() bool { return set.boolv("filler.reject.unidentified") }, time.Now),
	}
	if splitter != nil {
		// ⚠ Appended rather than placed in order — `NewPipeline` indexes the slice by stage id
		// and `StageOrder` is the ONE definition of the sequence, so the order here is
		// irrelevant. Stating that is worth a line, because a slice that looks like a pipeline
		// invites someone to "fix" its order.
		pipelineStages = append(pipelineStages,
			filler.NewSplitStage(splitter, fillerSplitStoreAdapter{st: st, wake: wake}).
				WithLogger(log).
				WithAutoConfirm(filler.AutoSplitPolicy{
					Enabled:       func() bool { return set.boolv("filler.autosplit.enabled") },
					MinConfidence: func() int { return set.intv("filler.autosplit.min_confidence") },
					MaxDuration:   func() time.Duration { return set.dur("filler.autosplit.max_duration") },
				}, func() time.Duration { return set.dur("filler.min_duration") }).
				// The split-time grounder (§10 V54). Without it the auto-confirm gate has no data
				// and `filler.autosplit.enabled` — default ON — can never fire.
				//
				// ⚠ Gated on the SAME `filler.vision.enabled` the vision rung uses, via a zero
				// budget rather than a nil grounder: an operator who turned vision off did not
				// ask for it back on a different rung, and one switch governing both is what
				// keeps "is Loomarr sending my frames to a model" answerable in one place.
				WithSegmentVision(&filler.SegmentVision{
					Tools:    fillerTools,
					Provider: visionProvider,
					Taxa:     fillerVisionStoreAdapter{st},
					ClipDir:  clipDir,
					Budget: func() int {
						if !set.boolv("filler.vision.enabled") {
							return 0
						}
						return set.intv("filler.pipeline.max_split_vision")
					},
				}))
	}
	fillerPipeline := filler.NewPipeline(st, fillerPipelineClipAdapter{st}, pipelineStages,
		filler.Budget{
			MaxClips: func() int { return set.intv("filler.pipeline.max_clips") },
			// ⚠ A closure RETURNING ZERO means "never transcode on this box", which is a
			// different state from a nil closure meaning "use the default". That three-state
			// encoding is the only way an operator can turn the most expensive rung off.
			MaxTranscodes: func() int { return set.intv("filler.transcode.max_per_run") },
			MaxWhisper:    func() int { return set.intv("filler.pipeline.max_whisper") },
			MaxVision:     func() int { return set.intv("filler.pipeline.max_vision") },
			MaxSplits:     func() int { return set.intv("filler.pipeline.max_splits") },
		},
		emitter.FillerClipStage, time.Now, log).
		WithRewind(fillerRewindAdapter{st}, clipDir)
	log.Info("filler ingest pipeline registered",
		"stages", len(pipelineStages), "language", set.str("filler.language"),
		"transcribe", set.boolv("filler.transcribe.enabled"),
		"vision", set.boolv("filler.vision.enabled"), "vision_provider", visionProvider != nil,
		"autosplit", set.boolv("filler.autosplit.enabled"))
	return fillerPipeline
}

// hostedLanguageAsker resolves the canonical active selection on every call. Hosted credentials
// are stored per provider (llm.api_key.openrouter, llm.api_key.custom, …), so reading the legacy
// base key here made the main picker work while the filler language request was sent without the
// selected provider's key. Custom OpenAI-compatible endpoints may legitimately need no key; URL,
// not credential presence, is therefore the availability boundary.
func hostedLanguageAsker(set resolved, recorder *metrics.Recorder) filler.AudioAsker {
	sel := resolveSelection(set)
	if sel.URL == "" {
		return nil // not configured ⇒ the gate keeps every clip
	}
	return audioAskerAdapter{llm.NewOpenAIForProvider(sel.Provider, sel.URL, sel.Model, sel.APIKey).WithMetrics(recorder)}
}

// buildPodAdapter constructs the pod assembler: the thing that picks which commercials fill a
// break, shared by the §12 preview endpoint, the reconciler and internal playout (§10).
//
// ⚠ Construction ONLY. The three lines that wire this into the channel engine, the preview
// adapter and the playout resolver stay at the call site deliberately — they are cross-subsystem
// back-patches (the resolver is built with the channel engine, the adapter needs the filler
// catalog), and pulling them in here would mean handing a builder the two services it exists to
// stay independent of.
func buildPodAdapter(st store.Store, set resolved, log *slog.Logger) *filler.PodAdapter {
	// ⚠ **A CLOSURE, not a struct literal — the whole filler policy is resolved per pod.**
	// This was a value built here at boot, so every reader took a snapshot: writing
	// `filler.pod_max`, `filler.min_quality`, `filler.min_clip_duration` or
	// `filler.max_clip_duration` changed the stored setting, the API read the new value
	// back, and the assembler went on using the old one until the process restarted.
	//
	// Caught on the live stack, not by a test: `filler.max_clip_duration=45s` left
	// `PoolReport.Eligible` at 20 with a 64s clip in the catalog; it dropped to 19 only
	// after a re-exec. Every unit test builds a `filler.Policy` literal and calls the domain
	// functions directly, so the setting→policy edge is the one segment they cannot reach.
	//
	// config-design §3 defines hot-apply for long-lived clients; `AutoSplitPolicy` already
	// resolves per call and says so. This is that contract, honoured by the pod path too.
	podAdapter := filler.NewPodAdapter(clipCatalogAdapter{st}, clipExposureAdapter{st}, func() filler.Policy {
		return filler.Policy{
			Geography:       filler.Geography{Country: set.str("filler.home_country"), Market: set.str("filler.home_market")}.Normalize(),
			PodMax:          set.intv("filler.pod_max"),
			BreakDurationMs: set.dur("filler.break_duration").Milliseconds(),
			// V17c: 0 (the default) leaves selection exactly as it was before the floor
			// existed — see the warning on Policy.MinQualityHeight.
			MinQualityHeight: set.intv("filler.min_quality"),
			// V51f: the pod-eligibility duration bounds finally have keys behind them. Both
			// default to 0s = off, so `durationEligible` keeps returning true on an untouched
			// install — the difference is that it CAN now return false, which is what lets
			// `PoolReport.Eligible` differ from `Commercials` instead of restating it.
			MinClipMs: set.dur("filler.min_clip_duration").Milliseconds(),
			MaxClipMs: set.dur("filler.max_clip_duration").Milliseconds(),
			Cooldown:  time.Duration(set.intv("filler.cooldown_seconds")) * time.Second,
		}
	}, log)
	return podAdapter
}

// visionWiring is the resolved endpoint the frame-reader talks to (§10 V54a, §15).
type visionWiring struct {
	provider, identity, url, model, key string
	// own reports that vision was pointed at its OWN service rather than inheriting the main
	// LLM's. It is logged, because "which endpoint is vision actually using" was previously
	// underivable from any output the process produced.
	own bool
}

// visionEndpoint resolves where clip frames get read, and by what.
//
// ⚠ **`filler.vision.model` alone was a half-separation, and the missing half failed silently.**
// The model knob let an operator name a vision model, but the provider was built from
// `llm.provider`/`llm.url`/`llm.api_key` — so naming a LOCAL model while the main LLM was hosted
// sent an Ollama tag to the hosted endpoint. Measured on the maintainer's stack: `llava:7b` →
// `https://openrouter.ai/api/v1` → 401 on every segment. `ground` reports that as zero looks, the
// gate refuses the reel with "a segment could not be classified", and nothing anywhere says why.
// A model name is useless without the host that serves it.
//
// ⚠ **The key is NEVER inherited once a vision provider is named.** Declaring a separate service
// means declaring its own credentials: inheriting would send the operator's hosted key to whatever
// host they just named, `localhost` included. A local Ollama needs none, so the common case sends
// nothing.
func visionEndpoint(set resolved) visionWiring {
	sel := resolveSelection(set)
	wireProvider := sel.Provider
	if wireProvider != "" && wireProvider != "ollama" {
		// OpenRouter and Custom are Loomarr's provider identities; both speak the
		// OpenAI-compatible wire used by the vision client.
		wireProvider = "openai"
	}
	v := visionWiring{
		provider: wireProvider,
		identity: sel.Provider,
		url:      sel.URL,
		key:      sel.APIKey,
		model:    set.str("filler.vision.model"),
	}
	// Empty ⇒ reuse the main model, for an install whose model already sees images. Same
	// separation `filler.language_model` makes for the audio gate.
	if v.model == "" {
		v.model = sel.Model
	}
	// ⚠ "" as well as `inherit`: the declared default is the word, but an env var set to empty
	// resolves to "" and means the same thing — inherit, not "no provider".
	if p := set.str("filler.vision.provider"); p != "" && p != "inherit" {
		v.provider, v.identity, v.url, v.key, v.own = p, p, set.str("filler.vision.url"), set.str("filler.vision.api_key"), true
		// A local Ollama on the conventional port is the case worth needing no second setting —
		// the same default `ollamaBase` applies to probes and pulls.
		if v.url == "" && p == "ollama" {
			v.url = defaultOllamaBase
		}
	}
	return v
}

// hotVisionProvider re-resolves `visionEndpoint` on every call, so an operator changing
// `filler.vision.*` in Settings takes effect on the next clip rather than on the next restart
// (config-design §8 hot-apply).
//
// ⚠ **The alternative was a provider captured at boot, which is what this replaces.** A settings
// subsystem whose whole point is `env > database > default` resolved live, wired to a knob that
// silently needs a restart, produces the worst kind of control: one that looks like it worked.
// The operator saves "Ollama", the next reel still 401s against the hosted endpoint, and nothing
// connects the two.
//
// ⚠ Memoised on the RESOLVED WIRING, not built per call. Rebuilding would discard the HTTP
// connection pool on every segment — 60 of them in one pass at the default `max_split_vision`.
type hotVisionProvider struct {
	set     resolved
	log     *slog.Logger
	metrics *metrics.Recorder

	mu   sync.Mutex
	last visionWiring
	cur  llm.VisionProvider
}

func (h *hotVisionProvider) AskAboutImages(ctx context.Context, prompt string, jpegs [][]byte) (llm.Response, error) {
	p, err := h.resolve()
	if err != nil {
		return llm.Response{}, err
	}
	return p.AskAboutImages(ctx, prompt, jpegs)
}

// resolve returns the provider for the CURRENT settings, rebuilding only when they have changed.
//
// ⚠ The errors are the point of this method existing separately. `ground` treats a provider
// failure as "stop, try again next pass", and every one of these used to be an unlogged nil
// provider instead — indistinguishable from vision being switched off.
func (h *hotVisionProvider) resolve() (llm.VisionProvider, error) {
	v := visionEndpoint(h.set)
	if v.url == "" {
		return nil, fmt.Errorf("no vision endpoint configured: set filler.vision.url, or llm.url for the main provider")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cur != nil && h.last == v {
		return h.cur, nil
	}
	var p llm.VisionProvider
	switch v.provider {
	case "openai":
		p = llm.NewOpenAIForProvider(v.identity, v.url, v.model, v.key).WithMetrics(h.metrics)
	case "ollama":
		p = llm.NewOllama(v.url, v.model).WithMetrics(h.metrics)
	default:
		return nil, fmt.Errorf("provider %q has no vision path", v.provider)
	}
	if h.log != nil && h.last != v {
		// The line that answers "which endpoint is vision actually using" — underivable from any
		// output this process produced before V54a.
		h.log.Info("filler vision endpoint resolved",
			"provider", v.identity, "wire_provider", v.provider, "url", v.url, "model", v.model, "own_endpoint", v.own)
	}
	h.last, h.cur = v, p
	return p, nil
}
