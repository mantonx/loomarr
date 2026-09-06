package fillervisualsafety_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillervisualsafety"
)

func TestBuildCandidateBlindReviewBundlePublishesCompleteSourceAndFrames(t *testing.T) {
	t.Parallel()

	prepared, ffmpeg := realDecoderSource(t, realDecoderOptions{
		rate: "25", rateNumerator: 25, rateDenominator: 1, durationMS: 3_080, lastFrameMS: 3_040, driftMS: 300,
		container: "mkv", codec: "ffv1", timeBaseDenominator: 1_000,
	})
	policyPath := attachReviewPolicy(t, prepared)
	output := filepath.Join(t.TempDir(), "visual-review")
	result, err := fillervisualsafety.BuildCandidateBlindReviewBundle(context.Background(), fillervisualsafety.CandidateBlindReviewConfig{
		Alias: "visual-case-001", SourceFamilyID: "source-family-001", RightsSHA256: repeatedDigest("e"),
		SelectionOrigin: fillervisualsafety.ReviewSelectionTargetedDiagnostic,
		Source:          fillervisualsafety.SourceRequest{Authority: prepared.Authority, Path: prepared.SnapshotPath},
		Profile:         prepared.Plan.Profile, PolicyPath: policyPath, FFmpegPath: ffmpeg, OutputDir: output,
		PreparedAt: time.Date(2026, time.September, 4, 18, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("BuildCandidateBlindReviewBundle() error = %v", err)
	}
	if result.FrameCount != len(prepared.Plan.Points) || result.PackageSHA256 == "" || result.OwnerMapSHA256 == "" {
		t.Fatalf("result = %+v", result)
	}

	manifest, owner, err := fillervisualsafety.OpenCandidateBlindReviewBundle(output)
	if err != nil {
		t.Fatalf("OpenCandidateBlindReviewBundle() error = %v", err)
	}
	if manifest.SHA256 != result.PackageSHA256 || owner.SHA256 != result.OwnerMapSHA256 ||
		manifest.Alias != owner.Alias || owner.SourceAuthority != prepared.Authority ||
		manifest.Policy.SHA256 != prepared.Authority.PolicySHA256 || manifest.Policy.RelativePath != "policy.json" ||
		manifest.Coverage.SHA256 == "" || len(manifest.Frames) != len(prepared.Plan.Points) {
		t.Fatalf("manifest = %+v; owner = %+v", manifest, owner)
	}
	if manifest.CandidateEvidenceIncluded || manifest.CandidateScoresIncluded || manifest.TruthAuthorityCreated ||
		manifest.TrainingAllowed || manifest.ProductionAdmissionAllowed {
		t.Fatalf("candidate-blind package gained authority: %+v", manifest)
	}

	reviewRaw, err := os.ReadFile(filepath.Join(output, "review", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{prepared.Authority.SourceID, prepared.SnapshotPath, "capabilitySha256", "inference", "maximumScore", "threshold"} {
		if bytes.Contains(reviewRaw, []byte(forbidden)) {
			t.Fatalf("review package contains candidate or owner field %q", forbidden)
		}
	}
	for _, path := range []string{
		output, filepath.Join(output, "review"), filepath.Join(output, "review", "frames"),
	} {
		assertPrivateMode(t, path, 0o700)
	}
	for _, path := range []string{
		filepath.Join(output, "owner-map.json"), filepath.Join(output, "review", "manifest.json"),
		filepath.Join(output, "review", manifest.Policy.RelativePath),
		filepath.Join(output, "review", manifest.Source.RelativePath),
	} {
		assertPrivateMode(t, path, 0o600)
	}
	for _, frame := range manifest.Frames {
		assertPrivateMode(t, filepath.Join(output, "review", filepath.FromSlash(frame.RelativePath)), 0o600)
	}
}

func TestOpenCandidateBlindReviewBundleRejectsTamperingAndExtraEvidence(t *testing.T) {
	prepared, ffmpeg := realDecoderSource(t, realDecoderOptions{
		rate: "10", rateNumerator: 10, rateDenominator: 1, durationMS: 3_000, lastFrameMS: 2_900, driftMS: 1,
		container: "mkv", codec: "ffv1", timeBaseDenominator: 1_000,
	})
	policyPath := attachReviewPolicy(t, prepared)
	build := func(t *testing.T) string {
		t.Helper()
		output := filepath.Join(t.TempDir(), "visual-review")
		_, err := fillervisualsafety.BuildCandidateBlindReviewBundle(context.Background(), fillervisualsafety.CandidateBlindReviewConfig{
			Alias: "visual-case-001", SourceFamilyID: "source-family-001", RightsSHA256: repeatedDigest("e"),
			SelectionOrigin: fillervisualsafety.ReviewSelectionTargetedDiagnostic,
			Source:          fillervisualsafety.SourceRequest{Authority: prepared.Authority, Path: prepared.SnapshotPath},
			Profile:         prepared.Plan.Profile, PolicyPath: policyPath, FFmpegPath: ffmpeg, OutputDir: output,
			PreparedAt: time.Date(2026, time.September, 4, 18, 0, 0, 0, time.UTC),
		})
		if err != nil {
			t.Fatal(err)
		}
		return output
	}

	t.Run("frame bytes", func(t *testing.T) {
		output := build(t)
		manifest, _, err := fillervisualsafety.OpenCandidateBlindReviewBundle(output)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(output, "review", filepath.FromSlash(manifest.Frames[0].RelativePath))
		if err := os.WriteFile(path, []byte("changed"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := fillervisualsafety.OpenCandidateBlindReviewBundle(output); err == nil {
			t.Fatal("expected changed frame bytes to fail")
		}
	})

	t.Run("extra evidence", func(t *testing.T) {
		output := build(t)
		if err := os.WriteFile(filepath.Join(output, "review", "candidate-scores.json"), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := fillervisualsafety.OpenCandidateBlindReviewBundle(output); err == nil {
			t.Fatal("expected extra candidate evidence to fail")
		}
	})

	t.Run("policy bytes", func(t *testing.T) {
		output := build(t)
		path := filepath.Join(output, "review", "policy.json")
		if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := fillervisualsafety.OpenCandidateBlindReviewBundle(output); err == nil {
			t.Fatal("expected changed policy bytes to fail")
		}
	})

	t.Run("owner map", func(t *testing.T) {
		output := build(t)
		path := filepath.Join(output, "owner-map.json")
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var owner fillervisualsafety.CandidateBlindReviewOwnerMap
		if err := json.Unmarshal(raw, &owner); err != nil {
			t.Fatal(err)
		}
		owner.SourceFamilyID = "changed-family"
		raw, _ = json.Marshal(owner)
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := fillervisualsafety.OpenCandidateBlindReviewBundle(output); err == nil {
			t.Fatal("expected changed owner map to fail")
		}
	})
}

func TestBuildCandidateBlindReviewBundleNeverOverwritesOutput(t *testing.T) {
	t.Parallel()

	output := filepath.Join(t.TempDir(), "visual-review")
	if err := os.Mkdir(output, 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := fillervisualsafety.BuildCandidateBlindReviewBundle(context.Background(), fillervisualsafety.CandidateBlindReviewConfig{
		OutputDir: output,
	})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("error = %v", err)
	}
}

func TestBuildCandidateBlindReviewBundleRemovesFailedReservation(t *testing.T) {
	t.Parallel()

	output := filepath.Join(t.TempDir(), "visual-review")
	_, err := fillervisualsafety.BuildCandidateBlindReviewBundle(context.Background(), fillervisualsafety.CandidateBlindReviewConfig{
		OutputDir: output,
	})
	if err == nil {
		t.Fatal("expected invalid configuration to fail")
	}
	if _, statErr := os.Lstat(output); !os.IsNotExist(statErr) {
		t.Fatalf("failed bundle reservation survived: %v", statErr)
	}
}

func TestBuildCandidateBlindReviewBundleRejectsPolicyNotBoundBySource(t *testing.T) {
	t.Parallel()

	prepared, ffmpeg := realDecoderSource(t, realDecoderOptions{
		rate: "10", rateNumerator: 10, rateDenominator: 1, durationMS: 3_000, lastFrameMS: 2_900, driftMS: 1,
		container: "mkv", codec: "ffv1", timeBaseDenominator: 1_000,
	})
	policyPath := attachReviewPolicy(t, prepared)
	prepared.Authority.PolicySHA256 = repeatedDigest("a")
	prepared.Authority.SHA256 = fillervisualsafety.SourceAuthoritySHA256(prepared.Authority)
	output := filepath.Join(t.TempDir(), "visual-review")
	_, err := fillervisualsafety.BuildCandidateBlindReviewBundle(context.Background(), fillervisualsafety.CandidateBlindReviewConfig{
		Alias: "visual-case-001", SourceFamilyID: "source-family-001", RightsSHA256: repeatedDigest("e"),
		SelectionOrigin: fillervisualsafety.ReviewSelectionTargetedDiagnostic,
		Source:          fillervisualsafety.SourceRequest{Authority: prepared.Authority, Path: prepared.SnapshotPath},
		Profile:         prepared.Plan.Profile, PolicyPath: policyPath, FFmpegPath: ffmpeg, OutputDir: output,
		PreparedAt: time.Date(2026, time.September, 4, 18, 0, 0, 0, time.UTC),
	})
	if err == nil {
		t.Fatal("expected policy digest mismatch to fail")
	}
	if _, statErr := os.Lstat(output); !os.IsNotExist(statErr) {
		t.Fatalf("failed policy bundle survived: %v", statErr)
	}
}

func attachReviewPolicy(t *testing.T, prepared *fillervisualsafety.PreparedSource) string {
	t.Helper()
	raw := []byte("{\n  \"schemaVersion\": 1,\n  \"kind\": \"loomarr-visual-sensitive-content-development-policy-v1\",\n  \"developmentOnly\": true,\n  \"productionAdmissionAllowed\": false,\n  \"policyMatches\": [\n    {\n      \"id\": \"explicit_nudity_v1\",\n      \"definition\": \"Visible genitals, exposed female breasts or nipples, or clearly exposed buttocks. Ordinary swimwear does not match.\"\n    }\n  ]\n}\n")
	digest := sha256.Sum256(raw)
	prepared.Authority.PolicySHA256 = fmt.Sprintf("%x", digest)
	prepared.Authority.SHA256 = ""
	authority, err := fillervisualsafety.SealSourceAuthority(prepared.Authority)
	if err != nil {
		t.Fatal(err)
	}
	prepared.Authority = authority
	path := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertPrivateMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != want {
		t.Fatalf("%s mode = %o, want %o", path, info.Mode().Perm(), want)
	}
}
