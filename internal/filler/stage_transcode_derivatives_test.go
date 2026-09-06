package filler

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/mediatools"
)

func TestTranscodeStage_ConditionsEvidenceBoundChildAgainstExactEvidenceAsset(t *testing.T) {
	dir := t.TempDir()
	parentSource := filepath.Join(dir, "parent-source.mkv")
	if err := os.WriteFile(parentSource, []byte("parent source master bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	parentSourceHash, err := ClipID(parentSource)
	if err != nil {
		t.Fatal(err)
	}
	master, err := preserveSourceMaster(context.Background(), dir, parentSource, parentSourceHash, SidecarTags{})
	if err != nil {
		t.Fatal(err)
	}
	tool := mediatools.MediaToolIdentity{
		Name: "ffmpeg", Version: "ffmpeg version fixture",
		ExecutableSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	evidenceBytes := []byte("the exact evidence rendition used to detect and cut")
	evidence, err := buildMediaDerivative(context.Background(), mediaDerivativeRequest{
		ClipDir: dir, Source: master, Input: Probed{DurationMs: 61_000, Height: 480},
		Recipe: mediatools.EvidenceDerivativeRecipe(), Tool: tool,
		Probe: func(context.Context, string) (Probed, error) {
			return Probed{DurationMs: 61_000, Height: 480}, nil
		},
		Verify: func(_ context.Context, _, _ string, durationMs int64, keyframeSeconds int, hadAudio bool, targetLUFS float64) (mediatools.DerivativeQC, error) {
			return fixtureDerivativeQC(durationMs, keyframeSeconds, hadAudio, targetLUFS), nil
		},
		Transcode: func(_ context.Context, request mediatools.TranscodeRequest, _ func(int)) (MediaQuality, error) {
			return MediaQuality{DurationMs: 61_000}, os.WriteFile(request.Out, evidenceBytes, 0o600)
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	parentHash := writeContentAddressedClip(t, dir, []byte("playback rendition differs from evidence"), ".mp4")
	parentRel := filepath.ToSlash(ClipRelPath(parentHash, ".mp4"))
	parentFull := filepath.Join(dir, filepath.FromSlash(parentRel))
	if err := WriteSidecarTags(parentFull, SidecarTags{MediaAssets: &MediaAssetManifest{
		Version: mediaAssetManifestVersion, SourceMaster: master, Evidence: &evidence,
	}}, false); err != nil {
		t.Fatal(err)
	}
	childHash := writeContentAddressedClip(t, dir, []byte("reviewed child cut from evidence"), ".mkv")
	childRel := filepath.ToSlash(ClipRelPath(childHash, ".mkv"))
	childFull := filepath.Join(dir, filepath.FromSlash(childRel))
	if err := WriteSidecarTags(childFull, SidecarTags{ConditioningLineage: &ConditioningLineage{
		ChildHash: childHash, ParentHash: parentHash,
		ParentAssetRole: string(SplitSourceEvidence), ParentAssetSHA256: evidence.Asset.SHA256,
		IntendedStartMs: 1_000, IntendedEndMs: 31_000,
	}}, false); err != nil {
		t.Fatal(err)
	}

	before := completeConditioningMeasurement(-28.4)
	after := completeConditioningMeasurement(-23.1)
	for i := range after.Cuts[0].Streams {
		after.Cuts[0].Streams[i].StartError = mediatools.OptionalMilliseconds{}
		after.Cuts[0].Streams[i].EndError = mediatools.OptionalMilliseconds{}
	}
	stored := &transcodeStore{clips: map[string]StoreClip{parentHash: {Clip: Clip{
		Hash: parentHash, Path: parentRel, DurationMs: 61_000, IsComposite: true,
	}}}}
	stage := NewTranscodeStage(stored, func(context.Context, string) (Probed, error) {
		return Probed{DurationMs: 30_000, Height: 480}, nil
	}, dir, mediatools.DefaultMezzanine(), nil, nil, time.Now)
	stage.transcode = func(_ context.Context, request mediatools.TranscodeRequest, _ func(int)) (MediaQuality, error) {
		return after.Quality, os.WriteFile(request.Out, []byte("conditioned playback child"), 0o600)
	}
	measurements := 0
	stage.WithConditioning(func(_ context.Context, request mediatools.ConditioningRequest) (mediatools.ConditioningMeasurement, error) {
		gotParent, err := os.ReadFile(request.ParentPath)
		if err != nil || !bytes.Equal(gotParent, evidenceBytes) {
			t.Fatalf("conditioning parent bytes = %q, %v; want exact evidence bytes", gotParent, err)
		}
		measurements++
		if measurements == 1 {
			return before, nil
		}
		return after, nil
	})

	out, err := stage.Run(context.Background(), StoreClip{Clip: Clip{
		Hash: childHash, Path: childRel, Name: "Reviewed child", Kind: Commercial, ParentHash: parentHash,
	}})
	if err != nil || out.Verdict != VerdictContinue || measurements != 2 || stored.oldHash != childHash {
		t.Fatalf("evidence-bound conditioning = %+v, measurements=%d stored=%q err=%v", out, measurements, stored.oldHash, err)
	}
}

func TestTranscodeStage_BuildsEvidenceAndPlaybackIndependentlyFromMaster(t *testing.T) {
	dir := t.TempDir()
	oldHash := writeContentAddressedClip(t, dir, []byte("authoritative source master"), ".mkv")
	oldRel := filepath.ToSlash(ClipRelPath(oldHash, ".mkv"))
	stored := &transcodeStore{}
	probe := func(context.Context, string) (Probed, error) {
		return Probed{DurationMs: 30_000, Height: 480}, nil
	}
	stage := NewTranscodeStage(stored, probe, dir, mediatools.DefaultMezzanine(), nil, func() float64 { return -23 }, time.Now).WithMediaDerivatives()
	tool := mediatools.MediaToolIdentity{Name: "ffmpeg", Version: "ffmpeg version fixture", ExecutableSHA256: strings.Repeat("a", 64)}
	stage.identifyFFmpeg = func(context.Context, string) (mediatools.MediaToolIdentity, error) { return tool, nil }
	stage.verifyDerivative = func(_ context.Context, _, _ string, durationMs int64, keyframeSeconds int, hadAudio bool, targetLUFS float64) (mediatools.DerivativeQC, error) {
		return fixtureDerivativeQC(durationMs, keyframeSeconds, hadAudio, targetLUFS), nil
	}
	var evidenceInput, playbackInput string
	stage.evidenceTranscode = func(_ context.Context, request mediatools.TranscodeRequest, _ func(int)) (MediaQuality, error) {
		evidenceInput = request.In
		if request.Profile.CRF != 14 || request.TargetLUFS != 0 {
			t.Fatalf("evidence recipe leaked playback policy: %+v", request)
		}
		return MediaQuality{DurationMs: 30_000}, os.WriteFile(request.Out, []byte("evidence derivative"), 0o600)
	}
	stage.transcode = func(_ context.Context, request mediatools.TranscodeRequest, _ func(int)) (MediaQuality, error) {
		playbackInput = request.In
		if request.Profile.CRF != 20 || request.TargetLUFS != -23 {
			t.Fatalf("playback recipe = %+v", request)
		}
		return MediaQuality{DurationMs: 30_000}, os.WriteFile(request.Out, []byte("playback derivative"), 0o600)
	}

	out, err := stage.Run(context.Background(), StoreClip{Clip: Clip{Hash: oldHash, Path: oldRel, Name: "Advert", Kind: Commercial}})
	if err != nil {
		t.Fatal(err)
	}
	tags, state := ReadSidecarTagsState(filepath.Join(dir, filepath.FromSlash(out.Clip.Path)))
	if state != SidecarValid || tags.MediaAssets == nil || tags.MediaAssets.Evidence == nil || tags.MediaAssets.Playback == nil {
		t.Fatalf("media asset manifest = %+v state=%v", tags.MediaAssets, state)
	}
	masterPath := filepath.Join(dir, filepath.FromSlash(tags.MediaAssets.SourceMaster.Path))
	if evidenceInput != masterPath || playbackInput != masterPath {
		t.Fatalf("derivative inputs = evidence %q playback %q, want master %q", evidenceInput, playbackInput, masterPath)
	}
	if tags.MediaAssets.Evidence.InputSHA256 != tags.MediaAssets.SourceMaster.SHA256 ||
		tags.MediaAssets.Playback.InputSHA256 != tags.MediaAssets.SourceMaster.SHA256 {
		t.Fatalf("derivatives are not independently bound to master: %+v", tags.MediaAssets)
	}
	if !tags.MediaAssets.Evidence.QC.CompleteDecode || !tags.MediaAssets.Playback.QC.Seekable {
		t.Fatalf("derivative QC was not persisted: %+v", tags.MediaAssets)
	}
	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(tags.MediaAssets.Evidence.Asset.Path))); err != nil {
		t.Fatalf("evidence derivative is unavailable: %v", err)
	}
}

func TestTranscodeStage_PlaybackQCFailurePreservesSourceAndCatalogIdentity(t *testing.T) {
	dir := t.TempDir()
	sourceBytes := []byte("source must survive a failed playback verification")
	sourceHash := writeContentAddressedClip(t, dir, sourceBytes, ".mkv")
	sourceRel := filepath.ToSlash(ClipRelPath(sourceHash, ".mkv"))
	sourceFull := filepath.Join(dir, filepath.FromSlash(sourceRel))
	stored := &transcodeStore{}
	probe := func(context.Context, string) (Probed, error) {
		return Probed{DurationMs: 30_000, Height: 480}, nil
	}
	stage := NewTranscodeStage(stored, probe, dir, mediatools.DefaultMezzanine(), nil, nil, time.Now).WithMediaDerivatives()
	stage.identifyFFmpeg = func(context.Context, string) (mediatools.MediaToolIdentity, error) {
		return mediatools.MediaToolIdentity{Name: "ffmpeg", Version: "fixture", ExecutableSHA256: strings.Repeat("c", 64)}, nil
	}
	stage.evidenceTranscode = func(_ context.Context, request mediatools.TranscodeRequest, _ func(int)) (MediaQuality, error) {
		return MediaQuality{DurationMs: 30_000}, os.WriteFile(request.Out, []byte("verified evidence"), 0o600)
	}
	stage.transcode = func(_ context.Context, request mediatools.TranscodeRequest, _ func(int)) (MediaQuality, error) {
		return MediaQuality{DurationMs: 30_000}, os.WriteFile(request.Out, []byte("unseekable playback"), 0o600)
	}
	verified := 0
	stage.verifyDerivative = func(_ context.Context, _, _ string, durationMs int64, keyframeSeconds int, hadAudio bool, targetLUFS float64) (mediatools.DerivativeQC, error) {
		verified++
		if verified == 2 {
			return mediatools.DerivativeQC{}, errors.New("midpoint seek failed")
		}
		return fixtureDerivativeQC(durationMs, keyframeSeconds, hadAudio, targetLUFS), nil
	}

	if _, err := stage.Run(context.Background(), StoreClip{Clip: Clip{
		Hash: sourceHash, Path: sourceRel, Name: "Advert", Kind: Commercial,
	}}); err == nil || !strings.Contains(err.Error(), "midpoint seek failed") {
		t.Fatalf("playback verification error = %v", err)
	}
	got, err := os.ReadFile(sourceFull)
	if err != nil || !bytes.Equal(got, sourceBytes) {
		t.Fatalf("source after failed verification = %q, %v", got, err)
	}
	if stored.oldHash != "" {
		t.Fatalf("catalog was re-keyed after failed verification: %q", stored.oldHash)
	}
}

func TestTranscodeStage_BackfillsEvidenceWithoutReencodingExistingPlayback(t *testing.T) {
	dir := t.TempDir()
	sourceBytes := []byte("legacy playback is the best source still available")
	hash := writeContentAddressedClip(t, dir, sourceBytes, ".mp4")
	rel := filepath.ToSlash(ClipRelPath(hash, ".mp4"))
	full := filepath.Join(dir, filepath.FromSlash(rel))
	quality := MediaQuality{EvidenceVersion: mediatools.MediaQualityEvidenceV1,
		Provenance: mediatools.MediaQualityProvenanceFFmpegDetectors, DurationMs: 30_000}
	if err := WriteSidecarTags(full, SidecarTags{OriginalName: "Legacy advert", Mezzanine: mediatools.DefaultMezzanine().ID(), MediaQuality: &quality}, false); err != nil {
		t.Fatal(err)
	}
	probe := func(context.Context, string) (Probed, error) { return Probed{DurationMs: 30_000, Height: 480}, nil }
	stage := NewTranscodeStage(&transcodeStore{}, probe, dir, mediatools.DefaultMezzanine(), nil, nil, time.Now).WithMediaDerivatives()
	stage.identifyFFmpeg = func(context.Context, string) (mediatools.MediaToolIdentity, error) {
		return mediatools.MediaToolIdentity{Name: "ffmpeg", Version: "fixture", ExecutableSHA256: strings.Repeat("b", 64)}, nil
	}
	stage.verifyDerivative = func(_ context.Context, _, _ string, durationMs int64, keyframeSeconds int, hadAudio bool, targetLUFS float64) (mediatools.DerivativeQC, error) {
		return fixtureDerivativeQC(durationMs, keyframeSeconds, hadAudio, targetLUFS), nil
	}
	stage.evidenceTranscode = func(_ context.Context, request mediatools.TranscodeRequest, _ func(int)) (MediaQuality, error) {
		return quality, os.WriteFile(request.Out, []byte("evidence from legacy playback"), 0o600)
	}
	stage.transcode = func(context.Context, mediatools.TranscodeRequest, func(int)) (MediaQuality, error) {
		t.Fatal("backfill must not create another playback generation")
		return MediaQuality{}, nil
	}
	clip := StoreClip{Clip: Clip{Hash: hash, Path: rel, Name: "Legacy advert", Kind: Commercial}}
	if applies, _ := stage.Applies(context.Background(), clip); !applies {
		t.Fatal("legacy playback without evidence did not re-enter the rung")
	}
	out, err := stage.Run(context.Background(), clip)
	if err != nil || out.Verdict != VerdictContinue {
		t.Fatalf("backfill result = %+v, %v", out, err)
	}
	got, err := os.ReadFile(full)
	if err != nil || !bytes.Equal(got, sourceBytes) {
		t.Fatalf("legacy playback was rewritten: %q, %v", got, err)
	}
	tags, state := ReadSidecarTagsState(full)
	if state != SidecarValid || tags.MediaAssets == nil || tags.MediaAssets.Evidence == nil || tags.MediaAssets.Playback != nil {
		t.Fatalf("backfilled asset manifest = %+v state=%v", tags.MediaAssets, state)
	}
	if applies, reason := stage.Applies(context.Background(), clip); applies {
		t.Fatalf("completed evidence backfill still applies: %s", reason)
	}
}

func TestTranscodeStage_DerivativeReuseVerifiesCurrentBytes(t *testing.T) {
	for _, role := range []MediaAssetRole{MediaAssetEvidence, MediaAssetPlayback} {
		t.Run(string(role), func(t *testing.T) {
			dir := t.TempDir()
			stage, counts, clip := derivativeReuseFixture(t, dir)
			out, err := stage.Run(context.Background(), clip)
			if err != nil {
				t.Fatal(err)
			}
			clip = out.Clip
			if counts.evidence != 1 || counts.playback != 1 {
				t.Fatalf("initial derivative builds = %+v, want one per role", counts)
			}

			full := derivativeAssetPath(t, dir, clip, role)
			beforeSparse, err := ClipID(full)
			if err != nil {
				t.Fatal(err)
			}
			beforeFull, size, err := FileSHA256(full)
			if err != nil {
				t.Fatal(err)
			}
			mutateDerivativeMiddleByte(t, full, size)
			afterSparse, err := ClipID(full)
			if err != nil {
				t.Fatal(err)
			}
			afterFull, afterSize, err := FileSHA256(full)
			if err != nil {
				t.Fatal(err)
			}
			if afterSparse != beforeSparse {
				t.Fatalf("sparse ClipID changed after middle-byte mutation: %s => %s", beforeSparse, afterSparse)
			}
			if afterFull == beforeFull || afterSize != size {
				t.Fatalf("full identity after mutation = (%s, %d), want changed digest and size %d", afterFull, afterSize, size)
			}

			if applies, reason := stage.Applies(context.Background(), clip); !applies {
				t.Fatalf("drifted %s derivative did not re-enter transcode: %s", role, reason)
			}
		})
	}
}

func TestTranscodeStage_RunCannotReuseCachedQualityForDriftedDerivative(t *testing.T) {
	for _, role := range []MediaAssetRole{MediaAssetEvidence, MediaAssetPlayback} {
		t.Run(string(role), func(t *testing.T) {
			dir := t.TempDir()
			stage, counts, clip := derivativeReuseFixture(t, dir)
			out, err := stage.Run(context.Background(), clip)
			if err != nil {
				t.Fatal(err)
			}
			clip = out.Clip
			full := derivativeAssetPath(t, dir, clip, role)
			_, size, err := FileSHA256(full)
			if err != nil {
				t.Fatal(err)
			}
			mutateDerivativeMiddleByte(t, full, size)

			if _, err := stage.Run(context.Background(), clip); err == nil ||
				!strings.Contains(err.Error(), string(role)+" derivative bytes do not match the manifest") {
				t.Fatalf("drifted %s Run error = %v, want exact-byte rejection", role, err)
			}
			if counts.evidence != 1 || counts.playback != 1 {
				t.Fatalf("drifted direct Run built or reused unverified derivatives: %+v", counts)
			}
		})
	}
}

func TestTranscodeStage_UnchangedDerivativeRetryReusesCompletedQuality(t *testing.T) {
	dir := t.TempDir()
	stage, counts, clip := derivativeReuseFixture(t, dir)
	out, err := stage.Run(context.Background(), clip)
	if err != nil {
		t.Fatal(err)
	}
	clip = out.Clip
	if applies, reason := stage.Applies(context.Background(), clip); applies || reason == "" {
		t.Fatalf("unchanged retry applies=%v reason=%q", applies, reason)
	}
	if _, err := stage.Run(context.Background(), clip); err != nil {
		t.Fatal(err)
	}
	if counts.evidence != 1 || counts.playback != 1 {
		t.Fatalf("unchanged retry rebuilt derivatives: %+v", counts)
	}
}

func TestTranscodeStage_DerivativeReuseRejectsPathSubstitution(t *testing.T) {
	t.Run("relative escape", func(t *testing.T) {
		dir := t.TempDir()
		stage, _, clip := derivativeReuseFixture(t, dir)
		out, err := stage.Run(context.Background(), clip)
		if err != nil {
			t.Fatal(err)
		}
		full := filepath.Join(dir, filepath.FromSlash(out.Clip.Path))
		tags, state := ReadSidecarTagsState(full)
		if state != SidecarValid || tags.MediaAssets == nil || tags.MediaAssets.Evidence == nil {
			t.Fatalf("completed derivative manifest = %+v state=%v", tags.MediaAssets, state)
		}
		tags.MediaAssets.Evidence.Asset.Path = "../escaped.mp4"
		if err := tags.MediaAssets.validateReuseFiles(context.Background(), dir, out.Clip.Path, out.Clip.Hash); err == nil {
			t.Fatal("relative evidence path escape was accepted")
		}
	})

	t.Run("symlink", func(t *testing.T) {
		dir := t.TempDir()
		stage, _, clip := derivativeReuseFixture(t, dir)
		out, err := stage.Run(context.Background(), clip)
		if err != nil {
			t.Fatal(err)
		}
		clip = out.Clip
		evidence := derivativeAssetPath(t, dir, clip, MediaAssetEvidence)
		bytes, err := os.ReadFile(evidence)
		if err != nil {
			t.Fatal(err)
		}
		substitute := filepath.Join(t.TempDir(), "evidence.mp4")
		if err := os.WriteFile(substitute, bytes, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(evidence); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(substitute, evidence); err != nil {
			t.Fatal(err)
		}
		if applies, reason := stage.Applies(context.Background(), clip); !applies {
			t.Fatalf("symlink-substituted evidence did not re-enter transcode: %s", reason)
		}
		if _, err := stage.Run(context.Background(), clip); err == nil || !strings.Contains(err.Error(), "evidence derivative bytes do not match the manifest") {
			t.Fatalf("symlink-substituted evidence Run error = %v", err)
		}
	})
}

type derivativeBuildCounts struct {
	evidence int
	playback int
}

func derivativeReuseFixture(t *testing.T, dir string) (*TranscodeStage, *derivativeBuildCounts, StoreClip) {
	t.Helper()
	sourceHash := writeContentAddressedClip(t, dir, []byte(strings.Repeat("authoritative source master\n", 4096)), ".mkv")
	clip := StoreClip{Clip: Clip{Hash: sourceHash, Path: filepath.ToSlash(ClipRelPath(sourceHash, ".mkv")), Name: "Fixture", Kind: Commercial}}
	counts := new(derivativeBuildCounts)
	stage := NewTranscodeStage(&transcodeStore{}, func(context.Context, string) (Probed, error) {
		return Probed{DurationMs: 30_000, Height: 480}, nil
	}, dir, mediatools.DefaultMezzanine(), nil, func() float64 { return -23 }, time.Now).WithMediaDerivatives()
	stage.identifyFFmpeg = func(context.Context, string) (mediatools.MediaToolIdentity, error) {
		return mediatools.MediaToolIdentity{Name: "ffmpeg", Version: "fixture", ExecutableSHA256: strings.Repeat("a", 64)}, nil
	}
	stage.verifyDerivative = func(_ context.Context, _, _ string, durationMs int64, keyframeSeconds int, hadAudio bool, targetLUFS float64) (mediatools.DerivativeQC, error) {
		return fixtureDerivativeQC(durationMs, keyframeSeconds, hadAudio, targetLUFS), nil
	}
	stage.evidenceTranscode = func(_ context.Context, request mediatools.TranscodeRequest, _ func(int)) (MediaQuality, error) {
		counts.evidence++
		return MediaQuality{DurationMs: 30_000}, os.WriteFile(request.Out, []byte("evidence\n"+strings.Repeat("derivative-byte-", 200000)), 0o600)
	}
	stage.transcode = func(_ context.Context, request mediatools.TranscodeRequest, _ func(int)) (MediaQuality, error) {
		counts.playback++
		return MediaQuality{DurationMs: 30_000}, os.WriteFile(request.Out, []byte("playback\n"+strings.Repeat("derivative-byte-", 200000)), 0o600)
	}
	return stage, counts, clip
}

func derivativeAssetPath(t *testing.T, dir string, clip StoreClip, role MediaAssetRole) string {
	t.Helper()
	tags, state := ReadSidecarTagsState(filepath.Join(dir, filepath.FromSlash(clip.Path)))
	if state != SidecarValid || tags.MediaAssets == nil || tags.MediaAssets.Evidence == nil || tags.MediaAssets.Playback == nil {
		t.Fatalf("completed derivative manifest = %+v state=%v", tags.MediaAssets, state)
	}
	asset := tags.MediaAssets.Evidence.Asset
	if role == MediaAssetPlayback {
		asset = tags.MediaAssets.Playback.Asset
	}
	return filepath.Join(dir, filepath.FromSlash(asset.Path))
}

func mutateDerivativeMiddleByte(t *testing.T, path string, size int64) {
	t.Helper()
	if size <= 2*hashWindow {
		t.Fatalf("derivative is %d bytes; need more than two sparse-hash windows", size)
	}
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	offset := size / 2
	var value [1]byte
	if _, err := file.ReadAt(value[:], offset); err != nil {
		t.Fatal(err)
	}
	value[0] ^= 0xff
	if _, err := file.WriteAt(value[:], offset); err != nil {
		t.Fatal(err)
	}
}
