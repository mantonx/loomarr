package fillerreview

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/mediatools"
)

const (
	TemporalTransitionAuthoritySchemaVersion   = 1
	TemporalTransitionAuthorityContractVersion = "filler-temporal-transition-authority-v1"
	TemporalTransitionEdgeWindowMS             = int64(1_000)
	TemporalTransitionSupportWindowMS          = int64(100)
	TemporalTransitionBoundaryToleranceMS      = int64(34)
	TemporalTransitionBlackMinimumMS           = int64(40)
	TemporalTransitionBlackPixelThreshold      = 0.10
	TemporalTransitionSilenceMinimumMS         = int64(40)
	TemporalTransitionSilenceThresholdDB       = -40
)

type TemporalTransitionStratum string

const (
	TemporalTransitionBlackBoundary             TemporalTransitionStratum = "black_boundary"
	TemporalTransitionAudibleNonblackCut        TemporalTransitionStratum = "audible_nonblack_cut"
	TemporalTransitionSilenceTouchedNonblackCut TemporalTransitionStratum = "silence_touched_nonblack_cut"
)

type TemporalTransitionAuthorityConfig struct {
	EvidenceManifestPath   string
	EvidencePrivateMapPath string
	GeneratedAt            time.Time
	PerCaseTimeout         time.Duration
	OutputDir              string
	Media                  TemporalTransitionEvidenceMedia
}

type TemporalTransitionEvidenceMedia interface {
	Identity() TemporalTruthToolIdentity
	MeasureEdges(context.Context, string, int64) (TemporalTransitionEdges, error)
}

type TemporalTransitionAuthority struct {
	SchemaVersion              int                               `json:"schemaVersion"`
	ContractVersion            string                            `json:"contractVersion"`
	GeneratedAt                time.Time                         `json:"generatedAt"`
	EvidenceManifestSHA256     string                            `json:"evidenceManifestSha256"`
	EvidencePrivateMapSHA256   string                            `json:"evidencePrivateMapSha256"`
	FFmpeg                     TemporalTruthToolIdentity         `json:"ffmpeg"`
	Policy                     TemporalTransitionPolicy          `json:"policy"`
	Cases                      []TemporalTransitionAuthorityCase `json:"cases"`
	TrainingAllowed            bool                              `json:"trainingAllowed"`
	ProductionAdmissionAllowed bool                              `json:"productionAdmissionAllowed"`
}

type TemporalTransitionPolicy struct {
	Profile             string  `json:"profile"`
	EdgeWindowMS        int64   `json:"edgeWindowMs"`
	SupportWindowMS     int64   `json:"supportWindowMs"`
	BoundaryToleranceMS int64   `json:"boundaryToleranceMs"`
	BlackMinimumMS      int64   `json:"blackMinimumMs"`
	BlackPixelThreshold float64 `json:"blackPixelThreshold"`
	SilenceMinimumMS    int64   `json:"silenceMinimumMs"`
	SilenceThresholdDB  int     `json:"silenceThresholdDb"`
}

type TemporalTransitionAuthorityCase struct {
	EvidenceAlias string                 `json:"evidenceAlias"`
	CaseID        string                 `json:"caseId"`
	SourceSHA256  string                 `json:"sourceSha256"`
	DurationMS    int64                  `json:"durationMs"`
	Head          TemporalTransitionEdge `json:"head"`
	Tail          TemporalTransitionEdge `json:"tail"`
}

type TemporalTransitionEdges struct {
	Head TemporalTransitionEdge
	Tail TemporalTransitionEdge
}

type TemporalTransitionEdge struct {
	StartMS       int64                 `json:"startMs"`
	EndMS         int64                 `json:"endMs"`
	Black         []mediatools.Interval `json:"black,omitempty"`
	Silence       []mediatools.Interval `json:"silence,omitempty"`
	RMSMilliDBFS  int64                 `json:"rmsMilliDbfs"`
	PeakMilliDBFS int64                 `json:"peakMilliDbfs"`
}

type TemporalTransitionAuthorityResult struct {
	Cases           int
	AuthoritySHA256 string
}

func temporalTransitionPolicy() TemporalTransitionPolicy {
	return TemporalTransitionPolicy{
		Profile:      "960x720-30fps-yuv420p-48khz-stereo-v1",
		EdgeWindowMS: TemporalTransitionEdgeWindowMS, SupportWindowMS: TemporalTransitionSupportWindowMS,
		BoundaryToleranceMS: TemporalTransitionBoundaryToleranceMS,
		BlackMinimumMS:      TemporalTransitionBlackMinimumMS, BlackPixelThreshold: TemporalTransitionBlackPixelThreshold,
		SilenceMinimumMS: TemporalTransitionSilenceMinimumMS, SilenceThresholdDB: TemporalTransitionSilenceThresholdDB,
	}
}

// BuildTemporalTransitionAuthority measures exact evidence bytes before any
// semantic or safety eligibility is consulted. The output is private planning
// authority only; it grants no training or admission permission.
func BuildTemporalTransitionAuthority(ctx context.Context, config TemporalTransitionAuthorityConfig) (TemporalTransitionAuthorityResult, error) {
	if strings.TrimSpace(config.EvidenceManifestPath) == "" || strings.TrimSpace(config.EvidencePrivateMapPath) == "" || strings.TrimSpace(config.OutputDir) == "" || config.GeneratedAt.IsZero() || config.PerCaseTimeout <= 0 || config.Media == nil {
		return TemporalTransitionAuthorityResult{}, fmt.Errorf("temporal transition authority requires evidence, private map, fixed time, timeout, output, and media")
	}
	stage, err := beginTemporalTruthEvidenceStage(config.OutputDir)
	if err != nil {
		return TemporalTransitionAuthorityResult{}, err
	}
	defer stage.Cleanup()
	manifest, manifestSHA, err := LoadTemporalTruthEvidence(config.EvidenceManifestPath)
	if err != nil {
		return TemporalTransitionAuthorityResult{}, err
	}
	privateMap, privateMapSHA, err := loadTemporalTransitionPrivateMap(config.EvidencePrivateMapPath, manifest, manifestSHA)
	if err != nil {
		return TemporalTransitionAuthorityResult{}, err
	}
	identity := config.Media.Identity()
	if strings.TrimSpace(identity.Path) == "" || strings.TrimSpace(identity.Version) == "" || !reviewSHA256(identity.BinarySHA256) {
		return TemporalTransitionAuthorityResult{}, fmt.Errorf("temporal transition FFmpeg identity is invalid")
	}
	mapping := make(map[string]TemporalTruthEvidencePrivateEntry, len(privateMap.Entries))
	for _, item := range privateMap.Entries {
		mapping[item.Alias] = item
	}
	authority := TemporalTransitionAuthority{
		SchemaVersion: TemporalTransitionAuthoritySchemaVersion, ContractVersion: TemporalTransitionAuthorityContractVersion,
		GeneratedAt: config.GeneratedAt.UTC(), EvidenceManifestSHA256: manifestSHA,
		EvidencePrivateMapSHA256: privateMapSHA, FFmpeg: identity, Policy: temporalTransitionPolicy(),
		TrainingAllowed: false, ProductionAdmissionAllowed: false,
	}
	for _, item := range manifest.Cases {
		mapped := mapping[item.Alias]
		path, err := resolveWithin(filepath.Dir(config.EvidenceManifestPath), item.Video.Path)
		if err != nil {
			return TemporalTransitionAuthorityResult{}, err
		}
		caseAuthority := TemporalTransitionAuthorityCase{
			EvidenceAlias: item.Alias, CaseID: mapped.CaseID, SourceSHA256: item.Video.SHA256, DurationMS: item.Video.DurationMS,
		}
		caseCtx, cancel := context.WithTimeout(ctx, config.PerCaseTimeout)
		edges, measureErr := config.Media.MeasureEdges(caseCtx, path, item.Video.DurationMS)
		cancel()
		if measureErr != nil {
			return TemporalTransitionAuthorityResult{}, fmt.Errorf("measure temporal transition alias %q: %w", item.Alias, measureErr)
		}
		caseAuthority.Head, caseAuthority.Tail = edges.Head, edges.Tail
		authority.Cases = append(authority.Cases, caseAuthority)
	}
	sort.Slice(authority.Cases, func(i, j int) bool { return authority.Cases[i].EvidenceAlias < authority.Cases[j].EvidenceAlias })
	if err := validateTemporalTransitionAuthority(authority, manifest, manifestSHA, privateMap, privateMapSHA, config.GeneratedAt); err != nil {
		return TemporalTransitionAuthorityResult{}, err
	}
	raw, err := json.MarshalIndent(authority, "", "  ")
	if err != nil {
		return TemporalTransitionAuthorityResult{}, err
	}
	raw = append(raw, '\n')
	if err := writeTemporalTruthNew(filepath.Join(stage.path, "authority.json"), raw, 0o600); err != nil {
		return TemporalTransitionAuthorityResult{}, err
	}
	if err := stage.Publish(); err != nil {
		return TemporalTransitionAuthorityResult{}, err
	}
	return TemporalTransitionAuthorityResult{Cases: len(authority.Cases), AuthoritySHA256: hashBytes(raw)}, nil
}

func loadTemporalTransitionPrivateMap(path string, manifest TemporalTruthEvidenceManifest, manifestSHA string) (TemporalTruthEvidencePrivateMap, string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return TemporalTruthEvidencePrivateMap{}, "", err
	}
	privateMap, err := readStrictJSON[TemporalTruthEvidencePrivateMap](path)
	if err != nil {
		return TemporalTruthEvidencePrivateMap{}, "", err
	}
	if err := validateTemporalHumanEvidenceJoin(manifest, manifestSHA, privateMap); err != nil {
		return TemporalTruthEvidencePrivateMap{}, "", err
	}
	return privateMap, hashBytes(raw), nil
}
