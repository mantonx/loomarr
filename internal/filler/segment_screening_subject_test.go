package filler

import (
	"strings"
	"testing"

	"github.com/loomarr/loomarr/internal/mediatools"
)

func screeningSubjectManifest(t *testing.T) MediaAssetManifest {
	t.Helper()
	tool := mediatools.MediaToolIdentity{
		Name: "ffmpeg", Version: "ffmpeg version fixture", ExecutableSHA256: strings.Repeat("9", 64),
	}
	evidenceRecipe := mediatools.EvidenceDerivativeRecipe()
	evidenceRecipeSHA, err := evidenceRecipe.Digest()
	if err != nil {
		t.Fatal(err)
	}
	playbackRecipe := mediatools.PlaybackDerivativeRecipe(mediatools.DefaultMezzanine(), 0)
	playbackRecipeSHA, err := playbackRecipe.Digest()
	if err != nil {
		t.Fatal(err)
	}
	input := Probed{DurationMs: 30_000, Height: 480}
	output := Probed{DurationMs: 30_000, Height: 480}
	quality := MediaQuality{
		EvidenceVersion: mediatools.MediaQualityEvidenceV1,
		Provenance:      mediatools.MediaQualityProvenanceFFmpegDetectors,
		DurationMs:      30_000,
	}
	source := MediaAssetIdentity{
		Role: MediaAssetSourceMaster, SHA256: strings.Repeat("1", 64), Bytes: 1_000,
		ClipHash: strings.Repeat("2", 64), Path: ".loomarr-media/masters/11/11/source.mp4",
	}
	manifest := MediaAssetManifest{
		Version: mediaAssetManifestVersion, SourceMaster: source,
		Evidence: &MediaDerivativeLineage{
			Asset: MediaAssetIdentity{
				Role: MediaAssetEvidence, SHA256: strings.Repeat("3", 64), Bytes: 900,
				ClipHash: strings.Repeat("4", 64), Path: ".loomarr-media/evidence/11/11/recipe/evidence.mp4",
			},
			InputSHA256: source.SHA256, Recipe: evidenceRecipe, RecipeSHA256: evidenceRecipeSHA,
			Tool: tool, DurationMs: 30_000, QC: fixtureDerivativeQC(30_000, evidenceRecipe.KeyframeSeconds, true, 0),
			InputProbe: input, OutputProbe: output, Quality: quality,
		},
		Playback: &MediaDerivativeLineage{
			Asset: MediaAssetIdentity{
				Role: MediaAssetPlayback, SHA256: strings.Repeat("5", 64), Bytes: 800,
				ClipHash: strings.Repeat("6", 64), Path: "66/66/playback.mp4",
			},
			InputSHA256: source.SHA256, Recipe: playbackRecipe, RecipeSHA256: playbackRecipeSHA,
			Tool: tool, DurationMs: 30_000, QC: fixtureDerivativeQC(30_000, playbackRecipe.KeyframeSeconds, true, 0),
			InputProbe: input, OutputProbe: output, Quality: quality,
		},
	}
	if err := manifest.validate(); err != nil {
		t.Fatal(err)
	}
	return manifest
}

func TestSegmentScreeningSubjectBindsRenderedChildAndExactParentInterval(t *testing.T) {
	manifest := screeningSubjectManifest(t)
	lineage := ConditioningLineage{
		ChildHash: manifest.SourceMaster.ClipHash, ParentHash: strings.Repeat("7", 64),
		ParentAssetRole: string(SplitSourceEvidence), ParentAssetSHA256: strings.Repeat("8", 64),
		StructureDecisionSHA256: strings.Repeat("a", 64), StructureRole: SegmentRoleCommercial,
		IntendedStartMs: 1_000, IntendedEndMs: 31_000,
	}
	measurement := completeConditioningMeasurement(-23)
	conditioning := ConditioningEvidence{
		BeforeRewriteHash: lineage.ChildHash, AfterRewriteHash: manifest.Playback.Asset.ClipHash,
		BeforeRewrite: measurement, AfterRewrite: measurement,
		DerivedParentEdgesAfterRewrite: measurement.Cuts[0],
	}
	subject, err := NewSegmentScreeningSubject(manifest.Playback.Asset.ClipHash, SidecarTags{
		SourceID: "archive:commercials", AcquisitionID: "acq-17", MediaAssets: &manifest,
		ConditioningLineage: &lineage, Conditioning: &conditioning,
	})
	if err != nil {
		t.Fatal(err)
	}
	if ValidateSegmentScreeningSubject(subject) != nil || subject.Lineage == nil ||
		subject.Lineage.IntendedStartMs != 1_000 || subject.Lineage.IntendedEndMs != 31_000 ||
		subject.CatalogHash != manifest.Playback.Asset.ClipHash || subject.ConditioningSHA256 != ConditioningEvidenceSHA256(conditioning) {
		t.Fatalf("subject = %+v", subject)
	}
}

func TestSegmentScreeningSubjectRejectsIncompleteOrDriftedChildIdentity(t *testing.T) {
	manifest := screeningSubjectManifest(t)
	standalone, err := NewSegmentScreeningSubject(manifest.Playback.Asset.ClipHash, SidecarTags{MediaAssets: &manifest})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*SegmentScreeningSubject)
	}{
		{name: "playback", mutate: func(s *SegmentScreeningSubject) { s.PlaybackSHA256 = "" }},
		{name: "manifest", mutate: func(s *SegmentScreeningSubject) { s.MediaManifestSHA256 = "" }},
		{name: "source id", mutate: func(s *SegmentScreeningSubject) { s.SourceID = " padded " }},
		{name: "conditioning without lineage", mutate: func(s *SegmentScreeningSubject) { s.ConditioningSHA256 = strings.Repeat("e", 64) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := standalone
			test.mutate(&candidate)
			candidate.SHA256 = SegmentScreeningSubjectSHA256(candidate)
			if err := ValidateSegmentScreeningSubject(candidate); err == nil {
				t.Fatal("drifted subject was accepted")
			}
		})
	}
}

func TestSegmentScreeningSubjectRequiresMeasuredDerivativeQuality(t *testing.T) {
	manifest := screeningSubjectManifest(t)
	manifest.Playback.Quality = MediaQuality{}
	if _, err := NewSegmentScreeningSubject(manifest.Playback.Asset.ClipHash, SidecarTags{MediaAssets: &manifest}); err == nil {
		t.Fatal("subject accepted playback without measured media quality")
	}
}

func TestSegmentScreeningSubjectRequiresPlaybackQualityToMatchConditioning(t *testing.T) {
	media := playbackIntegrityMediaFixture(t, validPlaybackIntegrityQuality())
	tags, ok := ReadSidecarTags(media.PlaybackPath)
	if !ok {
		t.Fatal("playback sidecar is unavailable")
	}
	playback := *tags.MediaAssets.Playback
	playback.Quality.Black = []Interval{{StartMs: 0, EndMs: 5_000}}
	manifest := *tags.MediaAssets
	manifest.Playback = &playback
	tags.MediaAssets = &manifest
	if _, err := NewSegmentScreeningSubject(playback.Asset.ClipHash, tags); err == nil {
		t.Fatal("subject accepted playback quality that differed from post-rewrite conditioning")
	}
}

func TestVerifySegmentScreeningSubjectRejectsCurrentArtifactDrift(t *testing.T) {
	manifest := screeningSubjectManifest(t)
	tags := SidecarTags{MediaAssets: &manifest}
	subject, err := NewSegmentScreeningSubject(manifest.Playback.Asset.ClipHash, tags)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifySegmentScreeningSubject(subject, manifest.Playback.Asset.ClipHash, tags); err != nil {
		t.Fatal(err)
	}
	drifted := manifest
	playback := *manifest.Playback
	playback.Asset.SHA256 = strings.Repeat("f", 64)
	drifted.Playback = &playback
	if err := VerifySegmentScreeningSubject(subject, playback.Asset.ClipHash, SidecarTags{MediaAssets: &drifted}); err == nil {
		t.Fatal("current playback drift was accepted")
	}
}

func TestMediaAssetManifestAuthorityIdentityIgnoresOnlyPaths(t *testing.T) {
	manifest := screeningSubjectManifest(t)
	want := MediaAssetManifestAuthoritySHA256(manifest)
	manifest.SourceMaster.Path = ".loomarr-media/masters/11/11/relocated.mp4"
	manifest.Evidence.Asset.Path = ".loomarr-media/evidence/11/11/recipe/relocated.mp4"
	manifest.Playback.Asset.Path = "66/66/relocated.mp4"
	if got := MediaAssetManifestAuthoritySHA256(manifest); got != want {
		t.Fatalf("path relocation changed media authority: got %s want %s", got, want)
	}
	manifest.Playback.Asset.SHA256 = strings.Repeat("f", 64)
	if got := MediaAssetManifestAuthoritySHA256(manifest); got == want {
		t.Fatal("playback byte drift did not change media authority")
	}
}
