package filler

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/loomarr/loomarr/internal/diagnostics"
	"github.com/loomarr/loomarr/internal/mediatools"
)

type mediaDerivativeRequest struct {
	ClipDir     string
	Source      MediaAssetIdentity
	Input       Probed
	Recipe      mediatools.DerivativeRecipe
	Tool        mediatools.MediaToolIdentity
	FFmpegPath  string
	Probe       Prober
	Diagnostics *diagnostics.ProcessManager
	Transcode   func(context.Context, mediatools.TranscodeRequest, func(int)) (MediaQuality, error)
	Verify      func(context.Context, string, string, int64, int, bool, float64) (mediatools.DerivativeQC, error)
	OnProgress  func(int)
}

func (s *TranscodeStage) prepareEvidenceDerivative(ctx context.Context, source MediaAssetIdentity, input Probed, ffmpeg string) (*MediaDerivativeLineage, mediatools.MediaToolIdentity, error) {
	if s.evidenceTranscode == nil {
		return nil, mediatools.MediaToolIdentity{}, nil
	}
	if s.identifyFFmpeg == nil {
		return nil, mediatools.MediaToolIdentity{}, fmt.Errorf("media tool identity is unavailable")
	}
	tool, err := s.identifyFFmpeg(ctx, ffmpeg)
	if err != nil {
		return nil, mediatools.MediaToolIdentity{}, err
	}
	built, err := buildMediaDerivative(ctx, mediaDerivativeRequest{
		ClipDir: s.clipDir, Source: source, Input: input,
		Recipe: mediatools.EvidenceDerivativeRecipe(), Tool: tool,
		FFmpegPath: ffmpeg, Probe: s.probe, Diagnostics: s.diagnostics,
		Transcode: s.evidenceTranscode, Verify: s.verifyDerivative,
		OnProgress: func(percent int) { reportProgress(ctx, StageTranscode, percent*40/100) },
	})
	if err != nil {
		return nil, mediatools.MediaToolIdentity{}, err
	}
	return &built, tool, nil
}

func buildMediaDerivative(ctx context.Context, request mediaDerivativeRequest) (MediaDerivativeLineage, error) {
	if err := validateMediaAssetIdentity(request.Source, MediaAssetSourceMaster, mediaMasterDirName); err != nil {
		return MediaDerivativeLineage{}, err
	}
	if err := request.Recipe.Validate(); err != nil {
		return MediaDerivativeLineage{}, err
	}
	if request.Recipe.Role != mediatools.DerivativeEvidence {
		return MediaDerivativeLineage{}, fmt.Errorf("build media derivative: role %q is not evidence", request.Recipe.Role)
	}
	if err := request.Tool.Validate(); err != nil {
		return MediaDerivativeLineage{}, err
	}
	if request.Transcode == nil || request.Verify == nil || request.Probe == nil || request.Input.DurationMs <= 0 || request.Input.Height <= 0 {
		return MediaDerivativeLineage{}, fmt.Errorf("build media derivative: transcode, verification, probe, and measured input are required")
	}
	recipeDigest, err := request.Recipe.Digest()
	if err != nil {
		return MediaDerivativeLineage{}, err
	}
	sourcePath := filepath.Join(request.ClipDir, filepath.FromSlash(request.Source.Path))
	stageDir := filepath.Join(request.ClipDir, MediaAssetRootName, ".staging")
	if err := os.MkdirAll(stageDir, 0o755); err != nil {
		return MediaDerivativeLineage{}, fmt.Errorf("build media derivative: create staging directory: %w", err)
	}
	stage, err := os.CreateTemp(stageDir, "evidence-*.mp4")
	if err != nil {
		return MediaDerivativeLineage{}, fmt.Errorf("build media derivative: create staging file: %w", err)
	}
	stagePath := stage.Name()
	if err := stage.Close(); err != nil {
		_ = os.Remove(stagePath)
		return MediaDerivativeLineage{}, err
	}
	_ = os.Remove(stagePath)
	defer func() {
		_ = os.Remove(stagePath)
		_ = os.Remove(sidecarPathFor(stagePath))
	}()
	quality, err := request.Transcode(ctx, mediatools.TranscodeRequest{
		In: sourcePath, Out: stagePath, DurationMs: request.Input.DurationMs, HadAudio: !request.Input.Silent,
		InputProbe: &request.Input,
		TargetLUFS: request.Recipe.TargetLUFS, Profile: request.Recipe.Profile(), FFmpegPath: request.FFmpegPath,
		Probe: request.Probe, Diagnostics: request.Diagnostics,
	}, request.OnProgress)
	if err != nil {
		return MediaDerivativeLineage{}, fmt.Errorf("build evidence derivative: %w", err)
	}
	output, err := request.Probe(ctx, stagePath)
	if err != nil || output.DurationMs <= 0 || output.Height <= 0 || (!request.Input.Silent && output.Silent) {
		return MediaDerivativeLineage{}, fmt.Errorf("build evidence derivative: output streams are incomplete")
	}
	qc, err := request.Verify(ctx, request.FFmpegPath, stagePath, output.DurationMs,
		request.Recipe.KeyframeSeconds, !output.Silent, request.Recipe.TargetLUFS)
	if err != nil {
		return MediaDerivativeLineage{}, fmt.Errorf("build evidence derivative: %w", err)
	}
	digest, size, err := FileSHA256(stagePath)
	if err != nil {
		return MediaDerivativeLineage{}, err
	}
	clipHash, err := ClipID(stagePath)
	if err != nil {
		return MediaDerivativeLineage{}, err
	}
	rel := filepath.ToSlash(filepath.Join(MediaAssetRootName, mediaEvidenceDirName,
		request.Source.SHA256[:2], request.Source.SHA256[2:4], recipeDigest[:16], digest+".mp4"))
	target := filepath.Join(request.ClipDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return MediaDerivativeLineage{}, err
	}
	if err := publishRetainedMaster(ctx, stagePath, target, digest, size); err != nil {
		return MediaDerivativeLineage{}, fmt.Errorf("publish evidence derivative: %w", err)
	}
	lineage := MediaDerivativeLineage{
		Asset:       MediaAssetIdentity{Role: MediaAssetEvidence, SHA256: digest, Bytes: size, ClipHash: clipHash, Path: rel},
		InputSHA256: request.Source.SHA256, Recipe: request.Recipe, RecipeSHA256: recipeDigest,
		Tool: request.Tool, DurationMs: output.DurationMs, Quality: quality, QC: qc,
		InputProbe: request.Input, OutputProbe: output,
	}
	if err := validateMediaDerivative(lineage, MediaAssetEvidence, mediaEvidenceDirName, request.Source.SHA256); err != nil {
		return MediaDerivativeLineage{}, err
	}
	return lineage, nil
}
