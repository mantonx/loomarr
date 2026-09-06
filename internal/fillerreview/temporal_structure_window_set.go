package fillerreview

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/filler"
	"github.com/loomarr/loomarr/internal/fillerstructure"
	"github.com/loomarr/loomarr/internal/fillerstructuremedia"
	"github.com/loomarr/loomarr/internal/fillerstructurewindow"
)

const (
	TemporalStructureWindowSetSchemaVersion   = 1
	TemporalStructureWindowSetContractVersion = "filler-temporal-structure-window-set-v1"
)

// TemporalStructureWindowSetPreparerFactory binds the production preparer to the packager's
// private staging root. The staged root is intentionally an implementation detail, not authority.
type TemporalStructureWindowSetPreparerFactory func(string) (filler.StructureAssessmentWindowMediaPreparer, error)

type TemporalStructureWindowSetConfig struct {
	CorpusManifestPath  string
	CorpusAuthorityPath string
	PreparedAt          time.Time
	OutputDir           string
	NewPreparer         TemporalStructureWindowSetPreparerFactory
}

type TemporalStructureWindowSetManifest struct {
	SchemaVersion                int                                    `json:"schemaVersion"`
	ContractVersion              string                                 `json:"contractVersion"`
	PreparedAt                   time.Time                              `json:"preparedAt"`
	CorpusManifestSHA256         string                                 `json:"corpusManifestSha256"`
	AssessmentMediaProfileSHA256 string                                 `json:"assessmentMediaProfileSha256"`
	Cases                        []TemporalStructureWindowSetPublicCase `json:"cases"`
	TrainingAllowed              bool                                   `json:"trainingAllowed"`
	ProductionAdmissionAllowed   bool                                   `json:"productionAdmissionAllowed"`
}

type TemporalStructureWindowSetPublicCase struct {
	Alias    string                             `json:"alias"`
	Source   filler.SplitSourceAsset            `json:"source"`
	MediaSet fillerstructurewindow.MediaSet     `json:"mediaSet"`
	Windows  []TemporalStructureWindowSetWindow `json:"windows"`
}

type TemporalStructureWindowSetWindow struct {
	Ordinal int    `json:"ordinal"`
	Path    string `json:"path"`
}

type TemporalStructureWindowSetAuthority struct {
	SchemaVersion         int                                       `json:"schemaVersion"`
	ContractVersion       string                                    `json:"contractVersion"`
	PreparedAt            time.Time                                 `json:"preparedAt"`
	CorpusManifestSHA256  string                                    `json:"corpusManifestSha256"`
	CorpusAuthoritySHA256 string                                    `json:"corpusAuthoritySha256"`
	PublicManifestSHA256  string                                    `json:"publicManifestSha256"`
	Cases                 []TemporalStructureWindowSetAuthorityCase `json:"cases"`
	TrainingAllowed       bool                                      `json:"trainingAllowed"`
	ProductionAllowed     bool                                      `json:"productionAllowed"`
}

type TemporalStructureWindowSetAuthorityCase struct {
	Alias  string                    `json:"alias"`
	CaseID string                    `json:"caseId"`
	Truth  []fillerstructure.Segment `json:"truth"`
}

type TemporalStructureWindowSetResult struct {
	Cases                  int
	Windows                int
	PublicManifestSHA256   string
	PrivateAuthoritySHA256 string
}

// BuildTemporalStructureWindowSet packages exact composites and their production-prepared windows
// atomically. It emits certification input only and performs no semantic measurement or model call.
func BuildTemporalStructureWindowSet(ctx context.Context, config TemporalStructureWindowSetConfig) (TemporalStructureWindowSetResult, error) {
	if strings.TrimSpace(config.CorpusManifestPath) == "" || strings.TrimSpace(config.CorpusAuthorityPath) == "" ||
		config.PreparedAt.IsZero() || strings.TrimSpace(config.OutputDir) == "" || config.NewPreparer == nil {
		return TemporalStructureWindowSetResult{}, errors.New("window set requires corpus manifest and authority, fixed preparation time, output, and production preparer factory")
	}
	corpusManifest, corpusAuthority, corpusManifestSHA, corpusAuthoritySHA, err := LoadTemporalStructureWindowCorpusMedia(
		config.CorpusManifestPath, config.CorpusAuthorityPath, TemporalStructureWindowCorpusCases,
	)
	if err != nil {
		return TemporalStructureWindowSetResult{}, err
	}
	privateByAlias := make(map[string]TemporalStructureWindowMediaAuthorityCase, len(corpusAuthority.Cases))
	for _, item := range corpusAuthority.Cases {
		privateByAlias[item.Alias] = item
	}
	stage, err := beginTemporalTruthEvidenceStage(config.OutputDir)
	if err != nil {
		return TemporalStructureWindowSetResult{}, err
	}
	defer stage.Cleanup()
	publicRoot, privateRoot := filepath.Join(stage.path, "public"), filepath.Join(stage.path, "private")
	if err := os.MkdirAll(publicRoot, 0o750); err != nil {
		return TemporalStructureWindowSetResult{}, err
	}
	if err := os.MkdirAll(privateRoot, 0o700); err != nil {
		return TemporalStructureWindowSetResult{}, err
	}
	preparer, err := config.NewPreparer(publicRoot)
	if err != nil {
		return TemporalStructureWindowSetResult{}, fmt.Errorf("create production window preparer: %w", err)
	}
	manifest := TemporalStructureWindowSetManifest{
		SchemaVersion: TemporalStructureWindowSetSchemaVersion, ContractVersion: TemporalStructureWindowSetContractVersion,
		PreparedAt: config.PreparedAt.UTC(), CorpusManifestSHA256: corpusManifestSHA,
		AssessmentMediaProfileSHA256: fillerstructuremedia.CanonicalProfile().SHA256,
		Cases:                        make([]TemporalStructureWindowSetPublicCase, 0, len(corpusManifest.Cases)),
	}
	authority := TemporalStructureWindowSetAuthority{
		SchemaVersion: TemporalStructureWindowSetSchemaVersion, ContractVersion: TemporalStructureWindowSetContractVersion,
		PreparedAt: config.PreparedAt.UTC(), CorpusManifestSHA256: corpusManifestSHA, CorpusAuthoritySHA256: corpusAuthoritySHA,
		Cases: make([]TemporalStructureWindowSetAuthorityCase, 0, len(corpusManifest.Cases)),
	}
	corpusRoot := filepath.Dir(config.CorpusManifestPath)
	totalWindows := 0
	for _, item := range corpusManifest.Cases {
		if err := ctx.Err(); err != nil {
			return TemporalStructureWindowSetResult{}, err
		}
		sourcePath := filepath.Join(corpusRoot, filepath.FromSlash(item.Source.Path))
		stagedPath := filepath.Join(publicRoot, filepath.FromSlash(item.Source.Path))
		if err := copyTemporalStructureWindowSetSource(ctx, sourcePath, stagedPath, item.Source); err != nil {
			return TemporalStructureWindowSetResult{}, fmt.Errorf("snapshot window set source %q: %w", item.Alias, err)
		}
		prepared, err := preparer.PrepareWindows(ctx, filler.StructureAssessmentSource{Source: item.Source, FullPath: stagedPath}, item.Plan)
		if err != nil {
			return TemporalStructureWindowSetResult{}, fmt.Errorf("prepare window set case %q: %w", item.Alias, err)
		}
		publicCase := TemporalStructureWindowSetPublicCase{Alias: item.Alias, Source: item.Source, MediaSet: prepared.Authority}
		for ordinal, window := range prepared.Windows {
			relative, err := filepath.Rel(publicRoot, window.FullPath)
			if err != nil || relative == "." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
				return TemporalStructureWindowSetResult{}, fmt.Errorf("prepared window %d path escapes package", ordinal)
			}
			publicCase.Windows = append(publicCase.Windows, TemporalStructureWindowSetWindow{Ordinal: ordinal, Path: filepath.ToSlash(relative)})
		}
		privateCase, ok := privateByAlias[item.Alias]
		if !ok {
			return TemporalStructureWindowSetResult{}, fmt.Errorf("window set case %q lacks private truth", item.Alias)
		}
		manifest.Cases = append(manifest.Cases, publicCase)
		authority.Cases = append(authority.Cases, TemporalStructureWindowSetAuthorityCase{
			Alias: item.Alias, CaseID: privateCase.CaseID, Truth: append([]fillerstructure.Segment(nil), privateCase.Truth...),
		})
		totalWindows += len(publicCase.Windows)
	}
	manifestRaw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return TemporalStructureWindowSetResult{}, err
	}
	manifestRaw = append(manifestRaw, '\n')
	authority.PublicManifestSHA256 = hashBytes(manifestRaw)
	authorityRaw, err := json.MarshalIndent(authority, "", "  ")
	if err != nil {
		return TemporalStructureWindowSetResult{}, err
	}
	authorityRaw = append(authorityRaw, '\n')
	manifestPath := filepath.Join(publicRoot, "manifest.json")
	if err := writeTemporalTruthNew(manifestPath, manifestRaw, 0o640); err != nil {
		return TemporalStructureWindowSetResult{}, err
	}
	if err := writeTemporalTruthNew(filepath.Join(privateRoot, "authority.json"), authorityRaw, 0o600); err != nil {
		return TemporalStructureWindowSetResult{}, err
	}
	if err := auditTemporalStructureWindowSetLeakage(manifestRaw, corpusAuthority); err != nil {
		return TemporalStructureWindowSetResult{}, err
	}
	if err := validateTemporalStructureWindowSet(manifestPath, manifest, authority, authority.PublicManifestSHA256, corpusManifest, corpusAuthority, corpusManifestSHA, corpusAuthoritySHA); err != nil {
		return TemporalStructureWindowSetResult{}, err
	}
	if err := stage.Publish(); err != nil {
		return TemporalStructureWindowSetResult{}, err
	}
	return TemporalStructureWindowSetResult{
		Cases: len(manifest.Cases), Windows: totalWindows, PublicManifestSHA256: authority.PublicManifestSHA256,
		PrivateAuthoritySHA256: hashBytes(authorityRaw),
	}, nil
}

func copyTemporalStructureWindowSetSource(ctx context.Context, source, destination string, identity filler.SplitSourceAsset) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o750); err != nil {
		return err
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = input.Close() }()
	info, err := input.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != identity.Bytes || info.Size() > TemporalStructureWindowMaximumSourceBytes {
		return errors.New("source is not the declared bounded regular file")
	}
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		return err
	}
	outputOpen := true
	defer func() {
		if outputOpen {
			_ = output.Close()
		}
	}()
	buffer := make([]byte, 1<<20)
	var copied int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		read, readErr := input.Read(buffer)
		if read > 0 {
			written, writeErr := output.Write(buffer[:read])
			copied += int64(written)
			if writeErr != nil || written != read {
				return errors.New("window set source snapshot was incomplete")
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	if err := output.Close(); err != nil {
		return err
	}
	outputOpen = false
	if copied != identity.Bytes {
		return errors.New("window set source snapshot size drifted")
	}
	digest, size, err := filler.FileSHA256(destination)
	if err != nil || digest != identity.SHA256 || size != identity.Bytes {
		return errors.New("window set source snapshot bytes drifted")
	}
	clipHash, err := filler.ClipID(destination)
	if err != nil || clipHash != identity.ClipHash {
		return errors.New("window set source snapshot sparse identity drifted")
	}
	return nil
}
