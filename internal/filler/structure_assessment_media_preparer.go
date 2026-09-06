package filler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/loomarr/loomarr/internal/fillerstructure"
	"github.com/loomarr/loomarr/internal/fillerstructuremedia"
	"github.com/loomarr/loomarr/internal/mediatools"
)

const structureAssessmentToolOutputMaximumBytes = 256 << 10

// FFmpegStructureAssessmentMediaPreparer owns the one expensive rewrite before independent
// assessors run. Its narrow result hides staging, reuse, tool identity, and lineage persistence.
type FFmpegStructureAssessmentMediaPreparer struct {
	sourceRoot string
	mediaRoot  string
	ffmpegPath string
	probe      Prober
	identify   func(context.Context, string) (mediatools.MediaToolIdentity, error)
	run        func(context.Context, string, []string) error
	decode     func(context.Context, string, string) error
	snapshot   func(context.Context, string, string) error
}

// NewFFmpegStructureAssessmentMediaPreparer binds production media preparation to the configured
// ffmpeg toolchain, the retained-source root, and a separately owned private derivative root.
func NewFFmpegStructureAssessmentMediaPreparer(sourceRoot, mediaRoot, ffmpegPath string) (*FFmpegStructureAssessmentMediaPreparer, error) {
	if sourceRoot == "" || !filepath.IsAbs(sourceRoot) || filepath.Clean(sourceRoot) != sourceRoot ||
		mediaRoot == "" || !filepath.IsAbs(mediaRoot) || filepath.Clean(mediaRoot) != mediaRoot {
		return nil, errors.New("structure assessment media preparer requires clean absolute source and media roots")
	}
	return &FFmpegStructureAssessmentMediaPreparer{
		sourceRoot: sourceRoot, mediaRoot: mediaRoot, ffmpegPath: ffmpegPath, probe: FFprobeNextTo(ffmpegPath),
		identify: mediatools.IdentifyFFmpeg, run: runStructureAssessmentFFmpeg,
		decode:   runStructureAssessmentDecode,
		snapshot: snapshotOwnedFile,
	}, nil
}

func (p *FFmpegStructureAssessmentMediaPreparer) Prepare(ctx context.Context, input StructureAssessmentSource) (StructureAssessmentMedia, error) {
	if p == nil || p.run == nil || p.decode == nil {
		return StructureAssessmentMedia{}, errors.New("structure assessment media preparer is unavailable")
	}
	prepared, err := p.prepareSource(ctx, input)
	if err != nil {
		return StructureAssessmentMedia{}, err
	}
	defer func() { _ = os.Remove(prepared.SnapshotPath) }()
	stageDir := filepath.Join(p.mediaRoot, MediaAssetRootName, ".staging")
	profile, tool, sourceIdentity := prepared.Profile, prepared.Tool, prepared.Identity
	operation := structureAssessmentOperationSHA256(sourceIdentity, profile, tool)
	if !isContentHash(operation) {
		return StructureAssessmentMedia{}, errors.New("structure assessment media operation identity is invalid")
	}
	indexPath := structureAssessmentIndexPath(p.mediaRoot, operation)
	index, found, err := loadStructureAssessmentIndex(indexPath, operation)
	if err != nil {
		return StructureAssessmentMedia{}, err
	}
	if found {
		return p.reuse(ctx, input.Source, sourceIdentity, profile, tool, operation, index)
	}

	outputMarker, err := os.CreateTemp(stageDir, "structure-media-*.mp4")
	if err != nil {
		return StructureAssessmentMedia{}, err
	}
	outputPath := outputMarker.Name()
	if err := outputMarker.Close(); err != nil {
		_ = os.Remove(outputPath)
		return StructureAssessmentMedia{}, err
	}
	_ = os.Remove(outputPath)
	defer func() { _ = os.Remove(outputPath) }()
	arguments := fillerstructuremedia.PartArguments(prepared.SnapshotPath, 0, input.Source.DurationMs, outputPath)
	if err := p.run(ctx, mediatools.FFmpegOr(p.ffmpegPath), arguments); err != nil {
		return StructureAssessmentMedia{}, fmt.Errorf("render structure assessment media: %w", err)
	}
	outputProbe, err := p.probe(ctx, outputPath)
	if err != nil {
		return StructureAssessmentMedia{}, fmt.Errorf("probe structure assessment media: %w", err)
	}
	if err := validateStructureAssessmentOutput(outputPath, outputProbe, input.Source, profile); err != nil {
		return StructureAssessmentMedia{}, err
	}
	if err := p.decode(ctx, p.ffmpegPath, outputPath); err != nil {
		return StructureAssessmentMedia{}, fmt.Errorf("decode structure assessment media: %w", err)
	}
	mediaSHA, mediaBytes, err := FileSHA256(outputPath)
	if err != nil {
		return StructureAssessmentMedia{}, err
	}
	lineage := StructureAssessmentMediaLineage{
		SchemaVersion: structureAssessmentMediaSchemaVersion, ContractVersion: structureAssessmentMediaContractVersion,
		OperationSHA256: operation, Source: sourceIdentity, Profile: profile, Tool: tool,
		Media: StructureAssessmentMediaDerivative{SHA256: mediaSHA, Bytes: mediaBytes, DurationMS: outputProbe.DurationMs},
	}
	lineage.SHA256 = structureAssessmentLineageSHA256(lineage)
	if err := validateStructureAssessmentLineage(lineage); err != nil {
		return StructureAssessmentMedia{}, err
	}
	mediaPath := structureAssessmentMediaPath(p.mediaRoot, mediaSHA)
	if err := publishStructureAssessmentMedia(ctx, outputPath, mediaPath, mediaSHA, mediaBytes); err != nil {
		return StructureAssessmentMedia{}, fmt.Errorf("publish structure assessment media: %w", err)
	}
	lineageStage, lineageRaw, err := writeStructureAssessmentJSON(stageDir, "structure-lineage-*", lineage)
	if err != nil {
		return StructureAssessmentMedia{}, err
	}
	defer func() { _ = os.Remove(lineageStage) }()
	if err := publishStructureAssessmentArtifact(ctx, lineageStage, structureAssessmentLineagePath(p.mediaRoot, lineage.SHA256), lineageRaw); err != nil {
		return StructureAssessmentMedia{}, fmt.Errorf("publish structure assessment lineage: %w", err)
	}
	index = structureAssessmentMediaIndex{
		SchemaVersion: structureAssessmentIndexSchemaVersion, ContractVersion: structureAssessmentIndexContractVersion,
		OperationSHA256: operation, LineageSHA256: lineage.SHA256,
	}
	indexStage, indexRaw, err := writeStructureAssessmentJSON(stageDir, "structure-index-*", index)
	if err != nil {
		return StructureAssessmentMedia{}, err
	}
	defer func() { _ = os.Remove(indexStage) }()
	if err := publishStructureAssessmentArtifact(ctx, indexStage, indexPath, indexRaw); err != nil {
		return StructureAssessmentMedia{}, fmt.Errorf("publish structure assessment index: %w", err)
	}
	return preparedStructureAssessmentMedia(input.Source, lineage, mediaPath), nil
}

func (p *FFmpegStructureAssessmentMediaPreparer) reuse(ctx context.Context, source SplitSourceAsset, sourceIdentity StructureAssessmentMediaSourceIdentity, profile fillerstructuremedia.Profile, tool mediatools.MediaToolIdentity, operation string, index structureAssessmentMediaIndex) (StructureAssessmentMedia, error) {
	lineage, err := loadStructureAssessmentLineage(structureAssessmentLineagePath(p.mediaRoot, index.LineageSHA256), index.LineageSHA256)
	if err != nil {
		return StructureAssessmentMedia{}, err
	}
	if lineage.OperationSHA256 != operation || lineage.Source != sourceIdentity || lineage.Profile != profile || lineage.Tool != tool {
		return StructureAssessmentMedia{}, errors.New("structure assessment media reuse authority drifted")
	}
	mediaPath := structureAssessmentMediaPath(p.mediaRoot, lineage.Media.SHA256)
	mediaSHA, mediaBytes, err := FileSHA256(mediaPath)
	if err != nil || mediaSHA != lineage.Media.SHA256 || mediaBytes != lineage.Media.Bytes {
		return StructureAssessmentMedia{}, errors.New("reused structure assessment media bytes do not match lineage")
	}
	probe, err := p.probe(ctx, mediaPath)
	if err != nil {
		return StructureAssessmentMedia{}, fmt.Errorf("probe reused structure assessment media: %w", err)
	}
	if err := validateStructureAssessmentOutput(mediaPath, probe, source, profile); err != nil || probe.DurationMs != lineage.Media.DurationMS {
		return StructureAssessmentMedia{}, errors.New("reused structure assessment media profile does not match lineage")
	}
	if err := p.decode(ctx, p.ffmpegPath, mediaPath); err != nil {
		return StructureAssessmentMedia{}, fmt.Errorf("decode reused structure assessment media: %w", err)
	}
	return preparedStructureAssessmentMedia(source, lineage, mediaPath), nil
}

func validateStructureAssessmentSourceSnapshot(path string, source SplitSourceAsset) error {
	digest, size, err := FileSHA256(path)
	if err != nil || digest != source.SHA256 || size != source.Bytes {
		return errors.New("structure assessment source bytes do not match authority")
	}
	sparse, err := ClipID(path)
	if err != nil || sparse != source.ClipHash {
		return errors.New("structure assessment source sparse identity does not match authority")
	}
	return nil
}

func validateStructureAssessmentOutput(path string, probe Probed, source SplitSourceAsset, profile fillerstructuremedia.Profile) error {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > profile.MaximumVideoBytes {
		return errors.New("structure assessment media is not a bounded regular file")
	}
	sampleAspect := strings.ReplaceAll(probe.SampleAspect, ":", "/")
	durationDrift := absoluteDurationDifference(probe.DurationMs, source.DurationMs)
	if probe.NoVideo || probe.Silent || probe.Width != profile.Width || probe.Height != profile.Height ||
		probe.Cadence != profile.FrameRate || sampleAspect != profile.SampleAspectRatio ||
		probe.DurationMs <= 0 || durationDrift > profile.MaximumTimelineDriftMS {
		return fmt.Errorf("structure assessment media does not match the canonical profile: video=%t audio=%t dimensions=%dx%d want=%dx%d cadence=%q want=%q sample_aspect=%q want=%q duration_ms=%d want=%d drift_ms=%d maximum_drift_ms=%d",
			!probe.NoVideo, !probe.Silent, probe.Width, probe.Height, profile.Width, profile.Height,
			probe.Cadence, profile.FrameRate, sampleAspect, profile.SampleAspectRatio,
			probe.DurationMs, source.DurationMs, durationDrift, profile.MaximumTimelineDriftMS)
	}
	return nil
}

func preparedStructureAssessmentMedia(source SplitSourceAsset, lineage StructureAssessmentMediaLineage, path string) StructureAssessmentMedia {
	return StructureAssessmentMedia{
		Source: source,
		Assessment: fillerstructure.AssessmentMedia{
			SHA256: lineage.Media.SHA256, Bytes: lineage.Media.Bytes, DurationMS: lineage.Media.DurationMS,
			ProfileSHA256: lineage.Profile.SHA256, LineageSHA256: lineage.SHA256,
		},
		FullPath: path,
	}
}

type boundedStructureAssessmentOutput struct {
	mu       sync.Mutex
	buffer   bytes.Buffer
	overflow bool
}

func (w *boundedStructureAssessmentOutput) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	remaining := structureAssessmentToolOutputMaximumBytes - w.buffer.Len()
	if remaining > 0 {
		_, _ = w.buffer.Write(data[:min(len(data), remaining)])
	}
	if len(data) > remaining {
		w.overflow = true
	}
	return len(data), nil
}

func runStructureAssessmentFFmpeg(ctx context.Context, executable string, arguments []string) error {
	output := &boundedStructureAssessmentOutput{}
	command := exec.CommandContext(ctx, executable, arguments...) //nolint:gosec // operator-configured media tool
	command.Stdout, command.Stderr = output, output
	err := command.Run()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if output.overflow {
		return errors.New("ffmpeg output exceeded 256 KiB")
	}
	if err != nil {
		output.mu.Lock()
		message := strings.TrimSpace(output.buffer.String())
		output.mu.Unlock()
		return fmt.Errorf("%w: %s", err, message)
	}
	return nil
}

func runStructureAssessmentDecode(ctx context.Context, ffmpegPath, path string) error {
	return runStructureAssessmentFFmpeg(ctx, mediatools.FFmpegOr(ffmpegPath), []string{
		"-nostdin", "-hide_banner", "-v", "error", "-i", path,
		"-map", "0:v:0", "-map", "0:a:0", "-f", "null", "-",
	})
}

var _ StructureAssessmentMediaPreparer = (*FFmpegStructureAssessmentMediaPreparer)(nil)
