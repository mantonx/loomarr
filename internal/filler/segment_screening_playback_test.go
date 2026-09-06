package filler

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/mediatools"
)

func TestPlaybackIntegrityEvaluatorVerifiesRenderedChildAndReplaysSettledResult(t *testing.T) {
	media := playbackIntegrityMediaFixture(t, validPlaybackIntegrityQuality())
	repository, err := NewFileSegmentScreeningEvidenceRepository(filepath.Join(t.TempDir(), "screening-evidence"))
	if err != nil {
		t.Fatal(err)
	}
	clockCalls := 0
	evaluator := mustPlaybackIntegrityEvaluator(t, repository, func() time.Time {
		clockCalls++
		return time.Date(2026, time.September, 14, 1, 0, 0, 0, time.UTC)
	})
	first, err := evaluator.Evaluate(t.Context(), media)
	if err != nil || first.Evidence.Outcome != ScreenPass || first.Evidence.ReasonCode != "playback_verified" {
		t.Fatalf("first=%+v error=%v", first, err)
	}
	if bytes.Contains(first.RawEvidence, []byte(filepath.Dir(media.PlaybackPath))) {
		t.Fatal("private artifact path leaked into playback evidence")
	}
	if err := repository.PutSegmentScreeningAxisEvidence(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	second, err := evaluator.Evaluate(t.Context(), media)
	if err != nil || second.Evidence.SHA256 != first.Evidence.SHA256 || clockCalls != 1 {
		t.Fatalf("second=%+v clockCalls=%d error=%v", second, clockCalls, err)
	}
}

func TestPlaybackIntegrityEvaluatorHoldsArtifactOrSidecarDrift(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(t *testing.T, media SegmentScreeningMedia)
		wantReason string
	}{
		{name: "playback bytes", wantReason: "playback_identity_drift", mutate: func(t *testing.T, media SegmentScreeningMedia) {
			t.Helper()
			body, err := os.ReadFile(media.PlaybackPath)
			if err != nil {
				t.Fatal(err)
			}
			for index := range body {
				body[index] = 'x'
			}
			if err := os.WriteFile(media.PlaybackPath, body, 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "missing sidecar", wantReason: "playback_sidecar_missing", mutate: func(t *testing.T, media SegmentScreeningMedia) {
			t.Helper()
			if err := os.Remove(sidecarPathFor(media.PlaybackPath)); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "invalid sidecar", wantReason: "playback_sidecar_invalid", mutate: func(t *testing.T, media SegmentScreeningMedia) {
			t.Helper()
			if err := os.WriteFile(sidecarPathFor(media.PlaybackPath), []byte("{"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "oversized sidecar", wantReason: "playback_sidecar_invalid", mutate: func(t *testing.T, media SegmentScreeningMedia) {
			t.Helper()
			if err := os.Truncate(sidecarPathFor(media.PlaybackPath), segmentScreeningSidecarMaxBytes+1); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "symlinked sidecar", wantReason: "playback_sidecar_invalid", mutate: func(t *testing.T, media SegmentScreeningMedia) {
			t.Helper()
			path := sidecarPathFor(media.PlaybackPath)
			target := path + ".target"
			if err := os.Rename(path, target); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			media := playbackIntegrityMediaFixture(t, validPlaybackIntegrityQuality())
			test.mutate(t, media)
			repository, err := NewFileSegmentScreeningEvidenceRepository(filepath.Join(t.TempDir(), "screening-evidence"))
			if err != nil {
				t.Fatal(err)
			}
			evaluator := mustPlaybackIntegrityEvaluator(t, repository, time.Now)
			recorded, err := evaluator.Evaluate(t.Context(), media)
			if err != nil || recorded.Evidence.Outcome != ScreenHold || recorded.Evidence.ReasonCode != test.wantReason {
				t.Fatalf("recorded=%+v error=%v", recorded, err)
			}
		})
	}
}

func TestPlaybackIntegrityEvaluatorMapsMeasuredQualityToClosedOutcome(t *testing.T) {
	tests := []struct {
		name        string
		quality     MediaQuality
		wantOutcome SegmentScreeningOutcome
		wantReason  string
	}{
		{name: "dead air", quality: playbackIntegrityQuality([]Interval{{StartMs: 0, EndMs: 27_000}}, nil, nil), wantOutcome: ScreenReject, wantReason: "playback_quality_reject"},
		{name: "long black", quality: playbackIntegrityQuality([]Interval{{StartMs: 0, EndMs: 5_000}}, nil, nil), wantOutcome: ScreenHold, wantReason: "playback_quality_hold"},
		{name: "clear", quality: validPlaybackIntegrityQuality(), wantOutcome: ScreenPass, wantReason: "playback_verified"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			media := playbackIntegrityMediaFixture(t, test.quality)
			repository, err := NewFileSegmentScreeningEvidenceRepository(filepath.Join(t.TempDir(), "screening-evidence"))
			if err != nil {
				t.Fatal(err)
			}
			evaluator := mustPlaybackIntegrityEvaluator(t, repository, time.Now)
			recorded, err := evaluator.Evaluate(t.Context(), media)
			if err != nil || recorded.Evidence.Outcome != test.wantOutcome || recorded.Evidence.ReasonCode != test.wantReason {
				t.Fatalf("recorded=%+v error=%v", recorded, err)
			}
		})
	}
}

func TestPlaybackIntegrityEvaluatorRefusesASecondAnswerForSettledOperation(t *testing.T) {
	media := playbackIntegrityMediaFixture(t, validPlaybackIntegrityQuality())
	repository, err := NewFileSegmentScreeningEvidenceRepository(filepath.Join(t.TempDir(), "screening-evidence"))
	if err != nil {
		t.Fatal(err)
	}
	evaluator := mustPlaybackIntegrityEvaluator(t, repository, time.Now)
	settled, err := evaluator.Evaluate(t.Context(), media)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.PutSegmentScreeningAxisEvidence(t.Context(), settled); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(media.PlaybackPath)
	if err != nil {
		t.Fatal(err)
	}
	for index := range body {
		body[index] = 'x'
	}
	if err := os.WriteFile(media.PlaybackPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := evaluator.Evaluate(t.Context(), media); err == nil {
		t.Fatal("settled pass was silently replaced with a different answer")
	}
}

func mustPlaybackIntegrityEvaluator(t *testing.T, repository SegmentScreeningAxisEvidenceReplay, now func() time.Time) *PlaybackIntegrityEvaluator {
	t.Helper()
	profile := screeningProfileFixture(ScreenPlayback, "4")
	profile.EvidenceContract = playbackIntegrityEvidenceContractVersion
	evaluator, err := NewPlaybackIntegrityEvaluator(profile, repository, now)
	if err != nil {
		t.Fatal(err)
	}
	return evaluator
}

func validPlaybackIntegrityQuality() MediaQuality {
	return playbackIntegrityQuality(nil, nil, nil)
}

func playbackIntegrityQuality(black, silence, freeze []Interval) MediaQuality {
	return MediaQuality{
		EvidenceVersion: mediatools.MediaQualityEvidenceV1,
		Provenance:      mediatools.MediaQualityProvenanceFFmpegDetectors,
		DurationMs:      30_000,
		Black:           black,
		Silence:         silence,
		Freeze:          freeze,
	}
}

func playbackIntegrityMediaFixture(t *testing.T, quality MediaQuality) SegmentScreeningMedia {
	t.Helper()
	root := t.TempDir()
	manifest := screeningSubjectManifest(t)
	manifest.Evidence.Quality = quality
	manifest.Playback.Quality = quality
	sourcePath := filepath.Join(root, filepath.FromSlash(manifest.SourceMaster.Path))
	evidencePath := filepath.Join(root, filepath.FromSlash(manifest.Evidence.Asset.Path))
	playbackPath := filepath.Join(root, filepath.FromSlash(manifest.Playback.Asset.Path))
	writeScreeningArtifactFixture(t, sourcePath, []byte("source-master-bytes"), &manifest.SourceMaster)
	writeScreeningArtifactFixture(t, evidencePath, []byte("evidence-derivative-bytes"), &manifest.Evidence.Asset)
	writeScreeningArtifactFixture(t, playbackPath, []byte("final-playback-derivative-bytes"), &manifest.Playback.Asset)
	manifest.Evidence.InputSHA256 = manifest.SourceMaster.SHA256
	manifest.Playback.InputSHA256 = manifest.SourceMaster.SHA256
	lineage := ConditioningLineage{
		ChildHash: manifest.SourceMaster.ClipHash, ParentHash: "7777777777777777777777777777777777777777777777777777777777777777",
		ParentAssetRole: string(SplitSourceEvidence), ParentAssetSHA256: "8888888888888888888888888888888888888888888888888888888888888888",
		StructureDecisionSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		StructureRole:           SegmentRoleCommercial,
		IntendedStartMs:         1_000, IntendedEndMs: 31_000,
	}
	measurement := completeConditioningMeasurement(-23)
	measurement.Quality = quality
	conditioning := ConditioningEvidence{
		BeforeRewriteHash: manifest.SourceMaster.ClipHash, AfterRewriteHash: manifest.Playback.Asset.ClipHash,
		BeforeRewrite: measurement, AfterRewrite: measurement, DerivedParentEdgesAfterRewrite: measurement.Cuts[0],
	}
	tags := SidecarTags{
		SourceID: "archive:commercials", AcquisitionID: "acq-17", MediaAssets: &manifest,
		ConditioningLineage: &lineage, Conditioning: &conditioning,
	}
	if err := WriteSidecarTags(playbackPath, tags, false); err != nil {
		t.Fatal(err)
	}
	subject, err := NewSegmentScreeningSubject(manifest.Playback.Asset.ClipHash, tags)
	if err != nil {
		t.Fatal(err)
	}
	return SegmentScreeningMedia{
		Subject: subject, Manifest: manifest,
		SourceMasterPath: sourcePath, EvidencePath: evidencePath, PlaybackPath: playbackPath,
	}
}

func writeScreeningArtifactFixture(t *testing.T, path string, body []byte, identity *MediaAssetIdentity) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	sha256, bytes, err := FileSHA256(path)
	if err != nil {
		t.Fatal(err)
	}
	clipHash, err := ClipID(path)
	if err != nil {
		t.Fatal(err)
	}
	identity.SHA256, identity.Bytes, identity.ClipHash = sha256, bytes, clipHash
}
