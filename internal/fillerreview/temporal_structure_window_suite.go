package fillerreview

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/loomarr/loomarr/internal/fillerstructurewindowcert"
)

const (
	TemporalStructureWindowEvidenceSchemaVersion   = 1
	TemporalStructureWindowEvidenceContractVersion = "filler-temporal-structure-window-measured-evidence-v1"
	TemporalStructureWindowMotionScale             = int64(1_000_000)
)

type TemporalStructureWindowMotionMeasurer interface {
	Identity() TemporalTruthToolIdentity
	Measure(context.Context, string) (TemporalStructureWindowMotionSample, error)
}

type TemporalStructureWindowMotionSample struct {
	Frames           int64 `json:"frames"`
	SumMicroluma     int64 `json:"sumMicroluma"`
	P95Microluma     int64 `json:"p95Microluma"`
	MaximumMicroluma int64 `json:"maximumMicroluma"`
}

type TemporalStructureWindowSuiteConfig struct {
	WindowSetManifestPath  string
	WindowSetAuthorityPath string
	CorpusManifestPath     string
	CorpusAuthorityPath    string
	HoldoutAuthoringPath   string
	HoldoutReceiptPath     string
	EvidenceManifestPath   string
	EvidencePrivateMapPath string
	MeasuredAt             time.Time
	OutputDir              string
	Motion                 TemporalStructureWindowMotionMeasurer
}

type TemporalStructureWindowMeasuredEvidence struct {
	SchemaVersion                  int                                       `json:"schemaVersion"`
	ContractVersion                string                                    `json:"contractVersion"`
	MeasuredAt                     time.Time                                 `json:"measuredAt"`
	WindowSetManifestSHA256        string                                    `json:"windowSetManifestSha256"`
	WindowSetAuthoritySHA256       string                                    `json:"windowSetAuthoritySha256"`
	CorpusManifestSHA256           string                                    `json:"corpusManifestSha256"`
	CorpusAuthoritySHA256          string                                    `json:"corpusAuthoritySha256"`
	HoldoutAuthoringSHA256         string                                    `json:"holdoutAuthoringSha256"`
	HoldoutReceiptSHA256           string                                    `json:"holdoutReceiptSha256"`
	EvidenceManifestSHA256         string                                    `json:"evidenceManifestSha256"`
	EvidencePrivateMapSHA256       string                                    `json:"evidencePrivateMapSha256"`
	MotionTool                     TemporalTruthToolIdentity                 `json:"motionTool"`
	MotionScale                    int64                                     `json:"motionScale"`
	MinimumHighMotionMeanMicroluma int64                                     `json:"minimumHighMotionMeanMicroluma"`
	WordlessJoins                  []TemporalStructureWindowWordlessEvidence `json:"wordlessJoins"`
	MotionWindows                  []TemporalStructureWindowMotionEvidence   `json:"motionWindows"`
	TrainingAllowed                bool                                      `json:"trainingAllowed"`
	ProductionAdmissionAllowed     bool                                      `json:"productionAdmissionAllowed"`
	SHA256                         string                                    `json:"sha256"`
}

type TemporalStructureWindowWordlessEvidence struct {
	CaseID           string                                       `json:"caseId"`
	Alias            string                                       `json:"alias"`
	TargetBoundaryMS int64                                        `json:"targetBoundaryMs"`
	Sources          []TemporalStructureWindowWordlessSourceProof `json:"sources"`
}

type TemporalStructureWindowWordlessSourceProof struct {
	SourceID         string `json:"sourceId"`
	EvidenceAlias    string `json:"evidenceAlias"`
	TranscriptSHA256 string `json:"transcriptSha256"`
}

type TemporalStructureWindowMotionEvidence struct {
	CaseID           string `json:"caseId"`
	Alias            string `json:"alias"`
	WindowOrdinal    int    `json:"windowOrdinal"`
	MediaSHA256      string `json:"mediaSha256"`
	MediaDurationMS  int64  `json:"mediaDurationMs"`
	Frames           int64  `json:"frames"`
	SumMicroluma     int64  `json:"sumMicroluma"`
	MeanMicroluma    int64  `json:"meanMicroluma"`
	P95Microluma     int64  `json:"p95Microluma"`
	MaximumMicroluma int64  `json:"maximumMicroluma"`
	Selected         bool   `json:"selected"`
}

type TemporalStructureWindowSuiteResult struct {
	Cases           int
	WordlessCases   int
	HighMotionCases int
	WindowsMeasured int
	EvidenceSHA256  string
	SuiteSHA256     string
}

// BuildTemporalStructureWindowCertificationSuite measures only pre-model traits and publishes
// private certification input. Assessment results are deliberately not accepted by this interface.
func BuildTemporalStructureWindowCertificationSuite(ctx context.Context, config TemporalStructureWindowSuiteConfig) (TemporalStructureWindowSuiteResult, error) {
	loaded, err := loadTemporalStructureWindowSuite(config)
	if err != nil {
		return TemporalStructureWindowSuiteResult{}, err
	}
	evidence := TemporalStructureWindowMeasuredEvidence{
		SchemaVersion: TemporalStructureWindowEvidenceSchemaVersion, ContractVersion: TemporalStructureWindowEvidenceContractVersion,
		MeasuredAt: config.MeasuredAt.UTC(), WindowSetManifestSHA256: loaded.windowSetManifestSHA,
		WindowSetAuthoritySHA256: loaded.windowSetAuthoritySHA, CorpusManifestSHA256: loaded.corpusManifestSHA,
		CorpusAuthoritySHA256: loaded.corpusAuthoritySHA, HoldoutAuthoringSHA256: loaded.authoringSHA,
		HoldoutReceiptSHA256: loaded.receiptSHA, EvidenceManifestSHA256: loaded.evidenceManifestSHA,
		EvidencePrivateMapSHA256: loaded.evidencePrivateMapSHA, MotionTool: config.Motion.Identity(),
		MotionScale: TemporalStructureWindowMotionScale,
	}
	evidence.WordlessJoins, err = temporalStructureWindowWordlessEvidence(loaded)
	if err != nil {
		return TemporalStructureWindowSuiteResult{}, err
	}
	evidence.MotionWindows, evidence.MinimumHighMotionMeanMicroluma, err = temporalStructureWindowMotionEvidence(ctx, config, loaded)
	if err != nil {
		return TemporalStructureWindowSuiteResult{}, err
	}
	evidence.SHA256 = temporalStructureWindowMeasuredEvidenceSHA256(evidence)
	if err := validateTemporalStructureWindowMeasuredEvidence(evidence, loaded); err != nil {
		return TemporalStructureWindowSuiteResult{}, err
	}
	suite, err := temporalStructureWindowCertificationSuite(loaded, evidence)
	if err != nil {
		return TemporalStructureWindowSuiteResult{}, err
	}
	stage, err := beginTemporalTruthEvidenceStage(config.OutputDir)
	if err != nil {
		return TemporalStructureWindowSuiteResult{}, err
	}
	defer stage.Cleanup()
	privateRoot := filepath.Join(stage.path, "private")
	if err := os.MkdirAll(privateRoot, 0o700); err != nil {
		return TemporalStructureWindowSuiteResult{}, err
	}
	evidenceRaw, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		return TemporalStructureWindowSuiteResult{}, err
	}
	suiteRaw, err := json.MarshalIndent(suite, "", "  ")
	if err != nil {
		return TemporalStructureWindowSuiteResult{}, err
	}
	if err := writeTemporalTruthNew(filepath.Join(privateRoot, "measured-evidence.json"), append(evidenceRaw, '\n'), 0o600); err != nil {
		return TemporalStructureWindowSuiteResult{}, err
	}
	if err := writeTemporalTruthNew(filepath.Join(privateRoot, "suite.json"), append(suiteRaw, '\n'), 0o600); err != nil {
		return TemporalStructureWindowSuiteResult{}, err
	}
	if err := stage.Publish(); err != nil {
		return TemporalStructureWindowSuiteResult{}, err
	}
	result := TemporalStructureWindowSuiteResult{
		Cases: len(suite.Cases), WordlessCases: len(evidence.WordlessJoins),
		WindowsMeasured: len(evidence.MotionWindows), EvidenceSHA256: evidence.SHA256, SuiteSHA256: suite.SHA256,
	}
	for _, item := range evidence.MotionWindows {
		if item.Selected {
			result.HighMotionCases++
		}
	}
	return result, nil
}

func temporalStructureWindowCertificationSuite(loaded temporalStructureWindowSuiteLoaded, evidence TemporalStructureWindowMeasuredEvidence) (fillerstructurewindowcert.Suite, error) {
	privateByAlias := make(map[string]TemporalStructureWindowSetAuthorityCase, len(loaded.windowSetAuthority.Cases))
	for _, item := range loaded.windowSetAuthority.Cases {
		privateByAlias[item.Alias] = item
	}
	wordless := make(map[string]int64, len(evidence.WordlessJoins))
	for _, item := range evidence.WordlessJoins {
		wordless[item.CaseID] = item.TargetBoundaryMS
	}
	motion := make(map[string]int, fillerstructurewindowcert.MinimumSliceCases)
	for _, item := range evidence.MotionWindows {
		if item.Selected {
			motion[item.CaseID] = item.WindowOrdinal
		}
	}
	cases := make([]fillerstructurewindowcert.Case, 0, len(loaded.windowSetManifest.Cases))
	for _, public := range loaded.windowSetManifest.Cases {
		private, ok := privateByAlias[public.Alias]
		if !ok {
			return fillerstructurewindowcert.Suite{}, errors.New("window certification suite lacks private truth")
		}
		item := fillerstructurewindowcert.Case{ID: private.CaseID, MediaSet: public.MediaSet, Truth: private.Truth}
		if boundary, ok := wordless[private.CaseID]; ok {
			item.MeasuredEvidence = append(item.MeasuredEvidence, fillerstructurewindowcert.MeasuredSliceEvidence{
				Slice: fillerstructurewindowcert.SliceWordlessJoin, EvidenceContract: fillerstructurewindowcert.WordlessEvidenceContract,
				EvidenceSHA256: evidence.SHA256, TargetBoundaryMS: boundary, TargetWindowOrdinal: -1,
			})
		}
		if ordinal, ok := motion[private.CaseID]; ok {
			item.MeasuredEvidence = append(item.MeasuredEvidence, fillerstructurewindowcert.MeasuredSliceEvidence{
				Slice: fillerstructurewindowcert.SliceHighMotionWindow, EvidenceContract: fillerstructurewindowcert.MotionEvidenceContract,
				EvidenceSHA256: evidence.SHA256, TargetWindowOrdinal: ordinal,
			})
		}
		cases = append(cases, item)
	}
	sort.Slice(cases, func(i, j int) bool { return cases[i].ID < cases[j].ID })
	return fillerstructurewindowcert.NewSuite(cases)
}

func temporalStructureWindowMeasuredEvidenceSHA256(evidence TemporalStructureWindowMeasuredEvidence) string {
	evidence.SHA256 = ""
	raw, err := json.Marshal(evidence)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func validTemporalStructureWindowMotionSample(sample TemporalStructureWindowMotionSample) bool {
	maximum := int64(255) * TemporalStructureWindowMotionScale
	return sample.Frames > 0 && sample.SumMicroluma > 0 && sample.SumMicroluma <= sample.Frames*maximum &&
		sample.P95Microluma >= 0 && sample.MaximumMicroluma >= sample.P95Microluma && sample.MaximumMicroluma <= maximum
}
