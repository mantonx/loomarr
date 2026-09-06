package filler

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/loomarr/loomarr/internal/mediatools"
)

func TestResolveSplitSourcePrefersEvidenceAndRejectsDrift(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "archive-source.mkv")
	if err := os.WriteFile(source, []byte("archive source master"), 0o644); err != nil {
		t.Fatal(err)
	}
	sourceHash, err := ClipID(source)
	if err != nil {
		t.Fatal(err)
	}
	master, err := preserveSourceMaster(context.Background(), root, source, sourceHash, SidecarTags{})
	if err != nil {
		t.Fatal(err)
	}
	tool := mediatools.MediaToolIdentity{
		Name: "ffmpeg", Version: "ffmpeg version fixture",
		ExecutableSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	evidence, err := buildMediaDerivative(context.Background(), mediaDerivativeRequest{
		ClipDir: root, Source: master, Input: Probed{DurationMs: 61_000, Height: 480},
		Recipe: mediatools.EvidenceDerivativeRecipe(), Tool: tool,
		Probe: func(context.Context, string) (Probed, error) {
			return Probed{DurationMs: 61_000, Height: 480}, nil
		},
		Verify: func(_ context.Context, _, _ string, durationMs int64, keyframeSeconds int, hadAudio bool, targetLUFS float64) (mediatools.DerivativeQC, error) {
			return fixtureDerivativeQC(durationMs, keyframeSeconds, hadAudio, targetLUFS), nil
		},
		Transcode: func(_ context.Context, request mediatools.TranscodeRequest, _ func(int)) (MediaQuality, error) {
			return MediaQuality{DurationMs: 61_000}, os.WriteFile(request.Out, []byte("evidence derivative bytes"), 0o600)
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	playableRel := filepath.ToSlash(filepath.Join("catalog", "commercial.mp4"))
	playable := filepath.Join(root, filepath.FromSlash(playableRel))
	if err := os.MkdirAll(filepath.Dir(playable), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(playable, []byte("playback derivative bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	playableHash, err := ClipID(playable)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteSidecarTags(playable, SidecarTags{MediaAssets: &MediaAssetManifest{
		Version: mediaAssetManifestVersion, SourceMaster: master, Evidence: &evidence,
	}}, false); err != nil {
		t.Fatal(err)
	}
	clip := StoreClip{Clip: Clip{Hash: playableHash, Path: playableRel, DurationMs: 61_000}}

	bound, gotPath, err := resolveSplitSource(context.Background(), root, clip, SplitSourceAsset{})
	if err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.Join(root, filepath.FromSlash(evidence.Asset.Path))
	if bound.Role != SplitSourceEvidence || bound.SHA256 != evidence.Asset.SHA256 ||
		bound.ClipHash != evidence.Asset.ClipHash || bound.DurationMs != evidence.DurationMs || gotPath != wantPath {
		t.Fatalf("resolved source = (%+v, %q), want evidence %+v at %q", bound, gotPath, evidence, wantPath)
	}

	// Once bound, a later playback recipe may replace the visible rendition without changing the
	// review. The proposal continues to resolve the exact retained evidence bytes.
	if err := os.WriteFile(playable, []byte("new playback recipe bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, path, err := resolveSplitSource(context.Background(), root, clip, bound); err != nil || got != bound || path != wantPath {
		t.Fatalf("bound evidence after playback change = (%+v, %q, %v)", got, path, err)
	}

	if err := os.WriteFile(wantPath, []byte("mutated evidence derivative"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := resolveSplitSource(context.Background(), root, clip, bound); err == nil {
		t.Fatal("mutated evidence derivative still satisfied the bound split proposal")
	}
}
