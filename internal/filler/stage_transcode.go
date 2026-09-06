package filler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/loomarr/loomarr/internal/diagnostics"
	"github.com/loomarr/loomarr/internal/mediatools"
)

// The TRANSCODE stage (§10 V66): retains the source and independently produces evidence and
// playback derivatives under measured recipes.
//
// See transcode.go for what the profile is and — more importantly — what it is NOT. This file is
// the rung: when it applies, what it writes, and how the clip's row follows the file.

// TranscodeClipStore is the slice of the store the transcode stage writes through.
type TranscodeClipStore interface {
	GetClip(ctx context.Context, hash string) (StoreClip, bool, error)
	// ReplaceClipIdentity atomically re-keys the clip and everything that refers to it. A
	// transcode changes bytes, so keeping the intake hash would make the next scan discover a
	// second, metadata-empty clip under the transformed bytes' real identity.
	ReplaceClipIdentity(ctx context.Context, oldHash string, c StoreClip) error
	// CommitConditioningPublication atomically classifies and commits the exact owner-bound
	// source/target catalog transition. It may adopt a held target reconstructed by Sync or
	// recognize an already committed target after process loss; every other state fails closed.
	CommitConditioningPublication(ctx context.Context, publication ConditioningPublication, target StoreClip) error
}

// TranscodeStage owns retained-source resolution and verified derivative publication.
type TranscodeStage struct {
	store   TranscodeClipStore
	probe   Prober
	clipDir string
	profile MezzanineProfile
	// ffmpegPath is the operator's configured binary; empty falls back to PATH.
	ffmpegPath func() string
	// targetLUFS folds loudness normalisation into the same pass. A closure returning 0 leaves the
	// audio alone, which is what `filler.autofile.normalize_loudness` off means.
	targetLUFS func() float64
	now        func() time.Time
	// transcode is a seam around the ffmpeg driver. Production always uses mediatools.Transcode;
	// tests replace it with a byte writer so the lifecycle can be exercised without a host binary.
	transcode func(context.Context, mediatools.TranscodeRequest, func(int)) (MediaQuality, error)
	// evidenceTranscode and identifyFFmpeg are enabled together by WithMediaDerivatives. Keeping
	// this explicit lets legacy recovery tests exercise only the historical playback saga while
	// the production composition root always opts into the V66 two-derivative contract.
	evidenceTranscode func(context.Context, mediatools.TranscodeRequest, func(int)) (MediaQuality, error)
	identifyFFmpeg    func(context.Context, string) (mediatools.MediaToolIdentity, error)
	verifyDerivative  func(context.Context, string, string, int64, int, bool, float64) (mediatools.DerivativeQC, error)
	// inspect backfills quality facts for a mezzanine made before those facts rode the encode.
	inspect   func(context.Context, string, string, int64, bool) (MediaQuality, error)
	condition func(context.Context, mediatools.ConditioningRequest) (mediatools.ConditioningMeasurement, error)
	// removeSource is the local-filesystem boundary after durable re-key. Production uses
	// os.Remove; tests inject its failure to prove the explicit quarantine survives a restart.
	removeSource func(string) error
	diagnostics  *diagnostics.ProcessManager
}

// WithMediaDerivatives enables the V66 evidence/playback recipe contract. Production composition
// always calls it; the method exists so media construction remains a testable boundary.
func (s *TranscodeStage) WithMediaDerivatives() *TranscodeStage {
	if s != nil {
		s.evidenceTranscode = mediatools.Transcode
		s.identifyFFmpeg = mediatools.IdentifyFFmpeg
		s.verifyDerivative = mediatools.VerifyDerivative
	}
	return s
}

// WithConditioning joins the read-only conditioning inspector to this existing pipeline rung.
func (s *TranscodeStage) WithConditioning(measure func(context.Context, mediatools.ConditioningRequest) (mediatools.ConditioningMeasurement, error)) *TranscodeStage {
	if s != nil {
		s.condition = measure
	}
	return s
}

// WithDiagnostics observes ffmpeg without changing the stage's failure or retry contract.
func (s *TranscodeStage) WithDiagnostics(manager *diagnostics.ProcessManager) *TranscodeStage {
	if s != nil {
		s.diagnostics = manager
	}
	return s
}

// NewTranscodeStage builds the stage.
func NewTranscodeStage(store TranscodeClipStore, probe Prober, clipDir string, profile MezzanineProfile, ffmpegPath func() string, targetLUFS func() float64, now func() time.Time) *TranscodeStage {
	if probe == nil {
		probe = FFprobe
	}
	if now == nil {
		now = time.Now
	}
	if profile.VideoCodec == "" {
		profile = mediatools.DefaultMezzanine()
	}
	return &TranscodeStage{
		store: store, probe: probe, clipDir: clipDir, profile: profile,
		ffmpegPath: ffmpegPath, targetLUFS: targetLUFS, now: now,
		transcode:    mediatools.Transcode,
		inspect:      mediatools.InspectQuality,
		removeSource: os.Remove,
	}
}

func (s *TranscodeStage) ID() StageID     { return StageTranscode }
func (s *TranscodeStage) Cost() StageCost { return CostTranscode }

// Applies unless this clip already carries this profile's mark.
//
// ⚠ **The sidecar marker is a SECOND line of defence, not the primary one.** The pipeline ladder
// is what normally stops a re-encode: the rung is `done` and is never revisited. The marker exists
// for the case the sibling table was built to survive — a `clips` rebuild that also loses the
// pipeline row — because a second generation of loss on a file whose original is gone is not
// recoverable, and re-reading a small JSON file is a cheap way never to risk it.
//
// It records the profile ID rather than a bare flag, so a future profile change re-encodes from
// the operator's own file rather than being silently skipped as "already done".
func (s *TranscodeStage) Applies(_ context.Context, c StoreClip) (bool, string) {
	if c.Path == "" {
		return false, "the clip has no file"
	}
	full := filepath.Join(s.clipDir, filepath.FromSlash(c.Path))
	// A rebuilt child must enter Run even when the mezzanine marker survived: Run resolves the
	// parent identity and revalidates the conditioning restart record before declaring it done.
	if c.ParentHash != "" {
		return true, ""
	}
	if tags, ok := ReadSidecarTags(full); ok && tags.Mezzanine == s.profile.ID() && tags.MediaQuality != nil {
		// A clean report is finished work. An anomalous report must remain applicable so a
		// failed hold/tombstone write can cheaply re-emit the same safety verdict next pass.
		if verdict, _, _ := EvaluateMediaQuality(*tags.MediaQuality); verdict == VerdictContinue {
			if s.evidenceTranscode != nil && (tags.MediaAssets == nil || tags.MediaAssets.Evidence == nil) {
				return true, ""
			}
			return false, "already encoded to the ingest profile"
		}
	}
	return true, ""
}

const transcodeStagingDir = ".transcode"

// Run re-encodes the clip, hashes the transformed bytes, files them at that identity's canonical
// path, and atomically re-keys the catalog metadata. The original remains intact until the new
// file, sidecar and database references are all durable.
func (s *TranscodeStage) Run(ctx context.Context, c StoreClip) (StageResult, error) {
	if s.store == nil {
		return StageResult{}, fmt.Errorf("transcode %s: no clip store is configured", c.Path)
	}
	oldRel := c.Path
	oldFull := filepath.Join(s.clipDir, filepath.FromSlash(oldRel))
	tags, hasTags := ReadSidecarTags(oldFull)
	sourceMaster, err := retainedSourceMaster(ctx, s.clipDir, oldFull, c.Hash, tags)
	if err != nil {
		if c.ParentHash != "" {
			return conditioningReview(c, "source master could not be retained"), nil
		}
		return StageResult{}, fmt.Errorf("transcode %s: %w", oldRel, err)
	}
	sourceMasterFull := filepath.Join(s.clipDir, filepath.FromSlash(sourceMaster.Path))
	inputFull := sourceMasterFull
	var conditioningReq mediatools.ConditioningRequest
	var conditioningBefore *mediatools.ConditioningMeasurement
	var conditioningStageDir, conditioningParentFull string
	var conditioningParentHash string
	if c.ParentHash != "" {
		strictTags, present, readErr := readConditioningSidecar(oldFull)
		if readErr != nil {
			return StageResult{}, readErr
		}
		tags, hasTags = strictTags, present
		if !hasTags || !validConditioningLineage(tags.ConditioningLineage, c.ParentHash) ||
			(tags.Mezzanine != s.profile.ID() && tags.ConditioningLineage.ChildHash != c.Hash) {
			return conditioningReview(c, "conditioning lineage is missing or does not match the child"), nil
		}
		parent, found, err := s.store.GetClip(ctx, c.ParentHash)
		if err != nil {
			return StageResult{}, fmt.Errorf("get conditioning parent %s: %w", c.ParentHash, err)
		}
		if !found || parent.Hash != c.ParentHash || parent.Path == "" || !parent.IsComposite {
			return conditioningReview(c, "conditioning parent is unavailable"), nil
		}
		conditioningParentFull = filepath.Join(s.clipDir, filepath.FromSlash(parent.Path))
		conditioningParentHash = parent.Hash
		if tags.ConditioningLineage.ParentAssetRole != "" {
			var parentSource SplitSourceAsset
			switch SplitSourceRole(tags.ConditioningLineage.ParentAssetRole) {
			case SplitSourceEvidence:
				parentTags, ok := ReadSidecarTags(conditioningParentFull)
				if !ok || parentTags.MediaAssets == nil || parentTags.MediaAssets.Evidence == nil ||
					parentTags.MediaAssets.Evidence.Asset.SHA256 != tags.ConditioningLineage.ParentAssetSHA256 {
					return conditioningReview(c, "conditioning parent evidence asset is unavailable"), nil
				}
				evidence := parentTags.MediaAssets.Evidence
				parentSource = SplitSourceAsset{
					Role: SplitSourceEvidence, SHA256: evidence.Asset.SHA256, Bytes: evidence.Asset.Bytes,
					ClipHash: evidence.Asset.ClipHash, Path: evidence.Asset.Path, DurationMs: evidence.DurationMs,
				}
			case SplitSourceLegacyPlayback:
				digest, size, err := FileSHA256(conditioningParentFull)
				if err != nil || digest != tags.ConditioningLineage.ParentAssetSHA256 {
					return conditioningReview(c, "conditioning parent playback asset changed"), nil
				}
				parentSource = SplitSourceAsset{
					Role: SplitSourceLegacyPlayback, SHA256: digest, Bytes: size,
					ClipHash: parent.Hash, Path: parent.Path, DurationMs: parent.DurationMs,
				}
			default:
				return conditioningReview(c, "conditioning parent asset role is invalid"), nil
			}
			resolvedParent, resolvedPath, err := resolveSplitSource(ctx, s.clipDir, parent, parentSource)
			if err != nil {
				return conditioningReview(c, "conditioning parent asset changed"), nil
			}
			conditioningParentFull = resolvedPath
			conditioningParentHash = resolvedParent.ClipHash
		}
		stageRoot := filepath.Join(s.clipDir, transcodeStagingDir)
		if err := os.MkdirAll(stageRoot, 0o755); err != nil {
			return StageResult{}, fmt.Errorf("create transcode staging folder: %w", err)
		}
		conditioningStageDir, err = os.MkdirTemp(stageRoot, "conditioning-")
		if err != nil {
			return StageResult{}, fmt.Errorf("create conditioning staging folder: %w", err)
		}
		defer func() { _ = os.RemoveAll(conditioningStageDir) }()
		// After a committed re-key c.Hash names playback bytes while the retained master keeps the
		// reviewed stream-copy child's sparse identity. Restart validation must bind each role to
		// its own identity rather than asking the master to masquerade as playback.
		snapshots, err := snapshotConditioningArtifacts(ctx, conditioningStageDir, inputFull, conditioningParentFull, sourceMaster.ClipHash, conditioningParentHash)
		if err != nil {
			return conditioningReview(c, err.Error()), nil
		}
		inputFull = snapshots.Source
		conditioningReq = mediatools.ConditioningRequest{
			Path:       inputFull,
			ParentPath: snapshots.Parent,
			IntendedCuts: []mediatools.Interval{{
				StartMs: tags.ConditioningLineage.IntendedStartMs,
				EndMs:   tags.ConditioningLineage.IntendedEndMs,
			}},
		}
		if tags.Mezzanine == s.profile.ID() {
			if tags.MediaQuality == nil || tags.Conditioning == nil ||
				mediatools.ValidateMediaQualityEvidence(*tags.MediaQuality) != nil ||
				validateConditioningPair(*tags.Conditioning, tags.Conditioning.BeforeRewriteHash, tags.Conditioning.AfterRewriteHash) != nil ||
				!conditioningEvidenceMatchesLineage(*tags.Conditioning, *tags.ConditioningLineage) ||
				!reflect.DeepEqual(*tags.MediaQuality, tags.Conditioning.AfterRewrite.Quality) {
				return conditioningReview(c, "persisted conditioning evidence is incomplete or invalid"), nil
			}
			if tags.ConditioningPublication != nil {
				publication := tags.ConditioningPublication
				if !validConditioningPublication(publication, publication.SourceHash, c.Hash) {
					return conditioningReview(c, "persisted conditioning publication is invalid"), nil
				}
				if err := s.store.CommitConditioningPublication(ctx, *publication, c); err != nil {
					return conditioningCommitOutcome(c, err)
				}
				if err := clearConditioningPublication(oldFull, publication); err != nil {
					return conditioningCommitOutcome(c, err)
				}
			}
			return mediaQualityResult(c, *tags.MediaQuality), nil
		}
		if s.condition == nil {
			return conditioningReview(c, "conditioning measurement is unavailable"), nil
		}
		measured, err := s.condition(ctx, conditioningReq)
		if err != nil {
			return conditioningReview(c, "conditioning measurement failed"), nil
		}
		if ctx.Err() != nil {
			return conditioningReview(c, "conditioning measurement was cancelled"), nil
		}
		if err := mediatools.ValidateConditioningEvidence(measured, mediatools.ConditioningBeforeRewrite); err != nil {
			return conditioningReview(c, "conditioning measurement is incomplete or invalid"), nil
		}
		intended := mediatools.Interval{StartMs: tags.ConditioningLineage.IntendedStartMs, EndMs: tags.ConditioningLineage.IntendedEndMs}
		if err := mediatools.ValidateConditioningCutEvidence(measured, intended); err != nil {
			return conditioningReview(c, "conditioning cut evidence is incomplete or unavailable"), nil
		}
		conditioningBefore = &measured
	}

	// The inspection report is also the retry record for the airability gate. Re-emit its
	// decision without decoding or encoding again if the previous pass could not hold the clip.
	if hasTags && tags.Mezzanine == s.profile.ID() && tags.MediaQuality != nil {
		if s.evidenceTranscode == nil || tags.MediaAssets != nil && tags.MediaAssets.Evidence != nil {
			return mediaQualityResult(c, *tags.MediaQuality), nil
		}
		input, err := s.probe(ctx, inputFull)
		if err != nil {
			return StageResult{}, fmt.Errorf("probe retained source before evidence backfill: %w", err)
		}
		ffmpeg := ""
		if s.ffmpegPath != nil {
			ffmpeg = s.ffmpegPath()
		}
		evidence, _, err := s.prepareEvidenceDerivative(ctx, sourceMaster, input, ffmpeg)
		if err != nil {
			return StageResult{}, fmt.Errorf("build evidence derivative for existing playback: %w", err)
		}
		tags.MediaAssets = &MediaAssetManifest{Version: mediaAssetManifestVersion, SourceMaster: sourceMaster, Evidence: evidence}
		if err := tags.MediaAssets.validate(); err != nil {
			return StageResult{}, err
		}
		if err := WriteSidecarTags(oldFull, tags, false); err != nil {
			return StageResult{}, fmt.Errorf("persist evidence derivative for existing playback: %w", err)
		}
		return mediaQualityResult(c, *tags.MediaQuality), nil
	}

	// Older mezzanines already carry the profile marker but predate content inspection. Re-encoding
	// would add a needless generation of loss, so spend this rung's bounded transcode budget on one
	// detector-only decode, persist the evidence beside the bytes, and decide from that.
	if hasTags && tags.Mezzanine == s.profile.ID() && tags.MediaQuality == nil {
		in, err := s.probe(ctx, oldFull)
		if err != nil {
			return StageResult{}, fmt.Errorf("re-probe %s before quality backfill: %w", oldRel, err)
		}
		ffmpeg := ""
		if s.ffmpegPath != nil {
			ffmpeg = s.ffmpegPath()
		}
		quality, err := s.inspect(ctx, ffmpeg, oldFull, in.DurationMs, !in.Silent)
		if err != nil {
			return StageResult{}, err
		}
		tags.MediaQuality = &quality
		if err := WriteSidecarTags(oldFull, tags, false); err != nil {
			return StageResult{}, fmt.Errorf("persist quality inspection %s: %w", oldRel, err)
		}
		return mediaQualityResult(c, quality), nil
	}

	stageDir := filepath.Join(s.clipDir, transcodeStagingDir)
	if err := os.MkdirAll(stageDir, 0o755); err != nil {
		return StageResult{}, fmt.Errorf("create transcode staging folder: %w", err)
	}
	if conditioningStageDir != "" {
		stageDir = conditioningStageDir
	}
	stageFull := filepath.Join(stageDir, c.Hash+".mp4")
	_ = os.Remove(stageFull)
	_ = os.Remove(sidecarPathFor(stageFull))
	defer func() {
		_ = os.Remove(stageFull)
		_ = os.Remove(sidecarPathFor(stageFull))
	}()

	// Measure the input so the output can be checked against it. The probe rung has already run,
	// but `HadAudio` is not on the clip row and re-probing one file is cheap next to an encode.
	in, err := s.probe(ctx, inputFull)
	if err != nil {
		if conditioningBefore != nil && isConditioningCancellation(err) {
			return conditioningReview(c, "conditioning transcode was cancelled"), nil
		}
		return StageResult{}, fmt.Errorf("re-probe %s before transcode: %w", oldRel, err)
	}
	if conditioningBefore != nil && ctx.Err() != nil {
		return conditioningReview(c, "conditioning transcode was cancelled"), nil
	}

	lufs := 0.0
	if s.targetLUFS != nil {
		lufs = s.targetLUFS()
	}
	ffmpeg := ""
	if s.ffmpegPath != nil {
		ffmpeg = s.ffmpegPath()
	}
	evidence, mediaTool, err := s.prepareEvidenceDerivative(ctx, sourceMaster, in, ffmpeg)
	if err != nil {
		return StageResult{}, fmt.Errorf("transcode %s: %w", oldRel, err)
	}

	req := mediatools.TranscodeRequest{
		In: inputFull, Out: stageFull,
		DurationMs: in.DurationMs, HadAudio: !in.Silent,
		InputProbe: &in,
		TargetLUFS: lufs, Profile: s.profile,
		FFmpegPath: ffmpeg, Probe: s.probe,
		Diagnostics: s.diagnostics,
	}
	quality, err := s.transcode(ctx, req, func(pct int) {
		if evidence != nil {
			reportProgress(ctx, StageTranscode, 40+pct*50/100)
			return
		}
		reportProgress(ctx, StageTranscode, pct*90/100)
	})
	if err != nil {
		if conditioningBefore != nil && isConditioningCancellation(err) {
			return conditioningReview(c, "conditioning transcode was cancelled"), nil
		}
		return StageResult{}, err
	}
	if conditioningBefore != nil && ctx.Err() != nil {
		return conditioningReview(c, "conditioning transcode was cancelled"), nil
	}
	var conditioningAfter *mediatools.ConditioningMeasurement
	var conditioningParentEdges *mediatools.ConditioningCutMeasurement
	if conditioningBefore != nil {
		conditioningReq.Path = stageFull
		measured, err := s.condition(ctx, conditioningReq)
		if err != nil {
			return conditioningReview(c, "conditioning measurement failed after transcode"), nil
		}
		if ctx.Err() != nil {
			return conditioningReview(c, "conditioning measurement after transcode was cancelled"), nil
		}
		if err := mediatools.ValidateConditioningEvidence(measured, mediatools.ConditioningAfterRewrite); err != nil {
			return conditioningReview(c, "conditioning measurement after transcode is incomplete or invalid"), nil
		}
		parentEdges, err := mediatools.DeriveConditioningParentEdges(*conditioningBefore, measured)
		if err != nil {
			return conditioningReview(c, "conditioning parent edges after transcode are unavailable"), nil
		}
		conditioningAfter = &measured
		conditioningParentEdges = &parentEdges
	}

	// Re-measure the staged file: the encode may legitimately shift the duration by a frame,
	// and the row must describe what is on disk.
	out, err := s.probe(ctx, stageFull)
	if err != nil {
		if conditioningBefore != nil && isConditioningCancellation(err) {
			return conditioningReview(c, "conditioning transcode was cancelled"), nil
		}
		return StageResult{}, fmt.Errorf("re-probe %s after transcode: %w", oldRel, err)
	}
	if conditioningBefore != nil && ctx.Err() != nil {
		return conditioningReview(c, "conditioning transcode was cancelled"), nil
	}
	quality.DurationMs = out.DurationMs
	if conditioningBefore != nil && (mediatools.ValidateMediaQualityEvidence(quality) != nil ||
		!reflect.DeepEqual(quality, conditioningAfter.Quality)) {
		return conditioningReview(c, "transcode media-quality evidence does not match post-rewrite measurement"), nil
	}
	var playbackQC mediatools.DerivativeQC
	if evidence != nil {
		if s.verifyDerivative == nil {
			return StageResult{}, errors.New("playback derivative verification is unavailable")
		}
		playbackQC, err = s.verifyDerivative(ctx, ffmpeg, stageFull, out.DurationMs,
			s.profile.KeyframeSeconds, !out.Silent, lufs)
		if err != nil {
			return StageResult{}, fmt.Errorf("verify playback derivative: %w", err)
		}
		reportProgress(ctx, StageTranscode, 95)
	}
	newHash, err := ClipID(stageFull)
	if err != nil {
		return StageResult{}, fmt.Errorf("hash transformed clip %s: %w", oldRel, err)
	}
	newRel := filepath.ToSlash(ClipRelPath(newHash, ".mp4"))
	newFull := filepath.Join(s.clipDir, filepath.FromSlash(newRel))
	assets := MediaAssetManifest{Version: mediaAssetManifestVersion, SourceMaster: sourceMaster, Evidence: evidence}
	if evidence != nil {
		playbackRecipe := mediatools.PlaybackDerivativeRecipe(s.profile, lufs)
		playbackRecipeDigest, recipeErr := playbackRecipe.Digest()
		if recipeErr != nil {
			return StageResult{}, recipeErr
		}
		playbackDigest, playbackBytes, digestErr := FileSHA256(stageFull)
		if digestErr != nil {
			return StageResult{}, fmt.Errorf("digest playback derivative: %w", digestErr)
		}
		assets.Playback = &MediaDerivativeLineage{
			Asset:       MediaAssetIdentity{Role: MediaAssetPlayback, SHA256: playbackDigest, Bytes: playbackBytes, ClipHash: newHash, Path: newRel},
			InputSHA256: sourceMaster.SHA256, Recipe: playbackRecipe, RecipeSHA256: playbackRecipeDigest,
			Tool: mediaTool, DurationMs: out.DurationMs, Quality: quality, QC: playbackQC,
			InputProbe: in, OutputProbe: out,
		}
		if err := assets.validate(); err != nil {
			return StageResult{}, fmt.Errorf("build media asset manifest: %w", err)
		}
	}
	var builtConditioning *ConditioningEvidence
	if conditioningBefore != nil {
		builtConditioning = &ConditioningEvidence{
			BeforeRewriteHash:              c.Hash,
			AfterRewriteHash:               newHash,
			BeforeRewrite:                  *conditioningBefore,
			AfterRewrite:                   *conditioningAfter,
			DerivedParentEdgesAfterRewrite: *conditioningParentEdges,
		}
	}
	alreadyPublished := false
	var publication *ConditioningPublication
	if conditioningBefore != nil && newFull != oldFull {
		publication = &ConditioningPublication{
			State: "pending", SourceHash: c.Hash, TargetHash: newHash,
		}
		owner := make([]byte, 16)
		if _, err := rand.Read(owner); err != nil {
			return conditioningReview(c, "conditioning publication owner could not be created"), nil
		}
		publication.Owner = hex.EncodeToString(owner)
	}
	if newFull != oldFull {
		if _, err := os.Stat(newFull); err == nil {
			// A previous attempt may have published the verified pair and then lost the database
			// commit. The marker distinguishes that recoverable saga state from an unrelated sparse-
			// hash collision; retry the durable re-key without overwriting either file.
			if err := validateRecoveredTranscode(ctx, stageFull, newFull, newHash, s.profile.ID(),
				tags.ConditioningLineage, builtConditioning, quality, lufs, &assets); err != nil {
				if builtConditioning != nil {
					return conditioningReview(c, err.Error()), nil
				}
				return StageResult{}, fmt.Errorf("transcode %s: %w", oldRel, err)
			}
			if publication != nil {
				existingTags, ok := ReadSidecarTags(newFull)
				if !ok || !validConditioningPublication(existingTags.ConditioningPublication, c.Hash, newHash) {
					return conditioningReview(c, "existing transformed artifact has no owned pending publication"), nil
				}
				publication = existingTags.ConditioningPublication
			}
			alreadyPublished = true
		} else if !os.IsNotExist(err) {
			return StageResult{}, fmt.Errorf("transcode %s: inspect transformed identity: %w", oldRel, err)
		}
	}
	if conditioningBefore != nil {
		sourceEqual, sourceErr := exactFileBytesEqual(ctx, sourceMasterFull, inputFull, mediatools.ConditioningMaxSnapshotBytes)
		parentEqual, parentErr := exactFileBytesEqual(ctx, conditioningParentFull, conditioningReq.ParentPath, mediatools.ConditioningMaxSnapshotBytes)
		if sourceErr != nil || parentErr != nil || !sourceEqual || !parentEqual {
			return conditioningReview(c, "conditioning source or parent changed while evidence was prepared"), nil
		}
		if ctx.Err() != nil {
			return conditioningReview(c, "conditioning transcode was cancelled"), nil
		}
	}
	// Retaining a master stabilizes the recipe input; it does not grant ownership of whatever path
	// now occupies the catalog name. Re-open the visible source before publication so a replacement
	// cannot be re-keyed and then deleted as if it were the bytes we preserved.
	if sourceMaster.ClipHash == c.Hash {
		sourceStillOwned, sourceErr := exactFileBytesEqual(ctx, oldFull, sourceMasterFull, mediatools.ConditioningMaxSnapshotBytes)
		if sourceErr != nil || !sourceStillOwned {
			if conditioningBefore != nil {
				return conditioningReview(c, "conditioning source changed while evidence was prepared"), nil
			}
			return StageResult{}, fmt.Errorf("transcode %s: source changed while derivatives were prepared", oldRel)
		}
	}

	// Build the replacement sidecar while the media is still hidden from the catalog scan. Split
	// children historically had no sidecar; seeding OriginalName from the durable display name is
	// what prevents their next scan from presenting the old hash as the title.
	if err := copySidecarForTransform(oldFull, stageFull); err != nil {
		return StageResult{}, fmt.Errorf("transcode %s: copy sidecar: %w", oldRel, err)
	}
	tags, _ = ReadSidecarTags(stageFull)
	if tags.OriginalName == "" {
		tags.OriginalName = c.Name
	}
	tags.Kind = string(c.Kind)
	tags.Era = c.Era
	tags.Audience = string(c.Audience)
	tags.Category = c.Category
	tags.Brand = c.Brand
	tags.Transcript = c.Transcript
	tags.Confidence = c.Confidence
	tags.SuggestedEra = c.SuggestedEra
	tags.Mezzanine = s.profile.ID()
	tags.MediaAssets = &assets
	tags.SupersededByHash = ""
	tags.ConditioningPublication = publication
	tags.MediaQuality = &quality
	if conditioningBefore != nil {
		tags.Conditioning = builtConditioning
	}
	if lufs != 0 {
		tags.NormalizedLUFS = lufs
	}
	if err := WriteSidecarTags(stageFull, tags, false); err != nil {
		return StageResult{}, fmt.Errorf("transcode %s: write replacement sidecar: %w", oldRel, err)
	}
	if conditioningBefore != nil && ctx.Err() != nil {
		return conditioningReview(c, "conditioning transcode was cancelled"), nil
	}

	// Sidecar first: the scan ignores it without media. Hard links publish without replacement,
	// so a race or sparse-hash collision can never overwrite an existing content-addressed clip.
	// When the transformed bytes already have the input identity AND canonical path, the media is
	// already published by definition. Install only the newly measured sidecar; trying to link the
	// staged bytes onto that same existing path turns an idempotent transform into EEXIST.
	if err := publishTranscodeReplacement(stageFull, newFull, oldFull, tags, alreadyPublished); err != nil {
		return StageResult{}, fmt.Errorf("transcode %s: publish transformed media: %w", oldRel, err)
	}

	updated := c
	updated.Hash = newHash
	updated.Path = newRel
	updated.TunarrProgramID = "" // the registered program named the old path; the next scan refreshes it
	updated.DurationMs = out.DurationMs
	if q := QualityFromHeight(out.Height); q != "" {
		updated.Quality = q
	}
	updated.UpdatedAt = s.now().UTC()
	if conditioningBefore != nil && newFull != oldFull {
		if ctx.Err() != nil {
			return conditioningReview(c, "conditioning transcode was cancelled"), nil
		}
		sourceTags, ok, readErr := readConditioningSidecar(oldFull)
		if readErr != nil {
			return StageResult{}, readErr
		}
		if !ok {
			return conditioningReview(c, "conditioning source sidecar is unavailable before re-key"), nil
		}
		sourceTags.SupersededByHash = newHash
		if err := WriteSidecarTags(oldFull, sourceTags, false); err != nil {
			return StageResult{}, fmt.Errorf("quarantine conditioning source sidecar: %w", err)
		}
	}
	var commitErr error
	if publication != nil {
		commitErr = s.store.CommitConditioningPublication(ctx, *publication, updated)
	} else {
		commitErr = s.store.ReplaceClipIdentity(ctx, c.Hash, updated)
	}
	if commitErr != nil {
		if conditioningBefore != nil {
			return conditioningCommitOutcome(c, commitErr)
		}
		return StageResult{}, commitErr
	}
	if publication != nil {
		if err := clearConditioningPublication(newFull, publication); err != nil {
			return conditioningCommitOutcome(updated, err)
		}
	}

	// The old file goes only AFTER every database reference points at the new identity. Its durable
	// supersededByHash marker keeps a cleanup failure preserved but quarantined across a restart.
	if newFull != oldFull {
		if conditioningBefore != nil && ctx.Err() != nil {
			return conditioningReview(updated, "conditioning transcode was cancelled after durable re-key"), nil
		}
		removeSource := s.removeSource
		if removeSource == nil {
			removeSource = os.Remove
		}
		if err := removeSource(oldFull); err != nil && !os.IsNotExist(err) {
			return StageResult{Clip: updated, Verdict: VerdictContinue,
				Note: "re-encoded, but the original file could not be deleted"}, nil
		}
		_ = os.Remove(sidecarPathFor(oldFull))
	}

	return mediaQualityResult(updated, quality), nil
}

func validConditioningPublication(p *ConditioningPublication, sourceHash, targetHash string) bool {
	return p != nil && p.State == "pending" && len(p.Owner) == 32 &&
		isContentHash(p.SourceHash) && isContentHash(p.TargetHash) &&
		p.SourceHash == sourceHash && p.TargetHash == targetHash
}

func conditioningEvidenceMatchesLineage(e ConditioningEvidence, lineage ConditioningLineage) bool {
	want := mediatools.Interval{StartMs: lineage.IntendedStartMs, EndMs: lineage.IntendedEndMs}
	return len(e.BeforeRewrite.Cuts) > 0 && len(e.AfterRewrite.Cuts) > 0 &&
		e.BeforeRewrite.Cuts[0].Intended == want && e.AfterRewrite.Cuts[0].Intended == want
}

// readConditioningSidecar distinguishes absent/corrupt ownership evidence from filesystem
// failures. The former is a safe review outcome; the latter must escape so pipeline retry logic
// does not mistake an unavailable filesystem for completed work.
func readConditioningSidecar(path string) (SidecarTags, bool, error) {
	raw, err := os.ReadFile(sidecarPathFor(path))
	if err != nil {
		if os.IsNotExist(err) {
			return SidecarTags{}, false, nil
		}
		return SidecarTags{}, false, fmt.Errorf("read conditioning sidecar %s: %w", path, err)
	}
	tags, state, _ := decodeSidecarTags(raw)
	if state != SidecarValid {
		return SidecarTags{}, false, nil
	}
	return tags, true, nil
}

func clearConditioningPublication(targetFull string, publication *ConditioningPublication) error {
	path := sidecarPathFor(targetFull)
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read conditioned publication sidecar: %w", err)
	}
	committedTags, state, _ := decodeSidecarTags(raw)
	if state != SidecarValid || !reflect.DeepEqual(committedTags.ConditioningPublication, publication) {
		return ErrConditioningOwnershipMismatch
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return ErrConditioningOwnershipMismatch
	}
	ours, ok := doc[loomarrKey].(map[string]any)
	if !ok {
		return ErrConditioningOwnershipMismatch
	}
	delete(ours, "conditioningPublication")
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("conditioned publication could not be cleared after re-key")
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil { //nolint:gosec // metadata beside operator-owned media
		return fmt.Errorf("conditioned publication could not be cleared after re-key")
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("conditioned publication could not be cleared after re-key")
	}
	return nil
}

func conditioningReview(c StoreClip, note string) StageResult {
	return StageResult{Clip: c, Verdict: VerdictReview, Note: note}
}

func conditioningCommitOutcome(c StoreClip, err error) (StageResult, error) {
	if errors.Is(err, ErrConditioningOwnershipMismatch) {
		return conditioningReview(c, "conditioned replacement could not be committed"), nil
	}
	return StageResult{}, err
}

func isConditioningCancellation(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func validateConditioningPair(e ConditioningEvidence, beforeHash, afterHash string) error {
	if !isContentHash(beforeHash) || !isContentHash(afterHash) ||
		e.BeforeRewriteHash != beforeHash || e.AfterRewriteHash != afterHash {
		return fmt.Errorf("persisted conditioning artifact identities are invalid")
	}
	if err := mediatools.ValidateConditioningPair(e.BeforeRewrite, e.AfterRewrite); err != nil {
		return err
	}
	derived, err := mediatools.DeriveConditioningParentEdges(e.BeforeRewrite, e.AfterRewrite)
	if err != nil || len(derived.Streams) != len(e.DerivedParentEdgesAfterRewrite.Streams) {
		return fmt.Errorf("persisted post-rewrite parent edges are invalid")
	}
	for i := range derived.Streams {
		if derived.Streams[i] != e.DerivedParentEdgesAfterRewrite.Streams[i] {
			return fmt.Errorf("persisted post-rewrite parent edges do not match measured timing")
		}
	}
	return nil
}

func validConditioningLineage(lineage *ConditioningLineage, childParentHash string) bool {
	if lineage == nil || !isContentHash(lineage.ChildHash) || !isContentHash(lineage.ParentHash) ||
		lineage.ParentHash != childParentHash || lineage.IntendedStartMs < 0 || lineage.IntendedEndMs <= lineage.IntendedStartMs {
		return false
	}
	assetBound := lineage.ParentAssetRole != "" || lineage.ParentAssetSHA256 != ""
	role := SplitSourceRole(lineage.ParentAssetRole)
	return !assetBound || ((role == SplitSourceEvidence || role == SplitSourceLegacyPlayback) && isContentHash(lineage.ParentAssetSHA256))
}

func mediaQualityResult(c StoreClip, quality MediaQuality) StageResult {
	verdict, reason, detail := EvaluateMediaQuality(quality)
	result := StageResult{Clip: c, Verdict: verdict}
	switch verdict {
	case VerdictReject:
		result.Reason, result.Detail = reason, detail
	case VerdictReview:
		result.Note = detail
	}
	return result
}

func copySidecarForTransform(oldMedia, stagedMedia string) error {
	from := sidecarPathFor(oldMedia)
	if _, err := os.Stat(from); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	return copyFile(from, sidecarPathFor(stagedMedia))
}

// publishHiddenMediaPair links the final sidecar before atomically making the media name visible.
// Both paths must share a filesystem; callers stage below the filler root to guarantee that fact.
func publishHiddenMediaPair(stagedMedia, finalMedia string) error {
	if stagedMedia == finalMedia {
		return nil
	}
	if err := prepareHiddenSidecar(stagedMedia, finalMedia); err != nil {
		return err
	}
	if err := publishPreparedMedia(stagedMedia, finalMedia); err != nil {
		_ = os.Remove(sidecarPathFor(finalMedia))
		return err
	}
	return nil
}

func prepareHiddenSidecar(stagedMedia, finalMedia string) error {
	stagedSidecar, finalSidecar := sidecarPathFor(stagedMedia), sidecarPathFor(finalMedia)
	if err := os.Link(stagedSidecar, finalSidecar); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrExist) {
		return err
	}
	if _, err := os.Stat(finalMedia); err == nil || !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("final sidecar already belongs to published or unreadable media")
	}
	staged, stagedErr := os.ReadFile(stagedSidecar)
	final, finalErr := os.ReadFile(finalSidecar)
	if stagedErr != nil || finalErr != nil || string(staged) != string(final) {
		return fmt.Errorf("orphan final sidecar does not match staged metadata")
	}
	return nil
}

func publishPreparedMedia(stagedMedia, finalMedia string) error {
	if err := os.Link(stagedMedia, finalMedia); err != nil {
		return err
	}
	stagedSidecar := sidecarPathFor(stagedMedia)
	_ = os.Remove(stagedSidecar)
	_ = os.Remove(stagedMedia)
	return nil
}
