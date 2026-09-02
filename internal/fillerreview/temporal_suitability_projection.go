package fillerreview

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/loomarr/loomarr/internal/fillereval"
)

const (
	TemporalSuitabilityProjectionSchemaVersion   = 1
	TemporalSuitabilityProjectionContractVersion = "filler-temporal-suitability-source-projection-v1"

	TemporalSuitabilityDispositionProhibited  = "prohibited_hold"
	TemporalSuitabilityDispositionOperational = "operational_hold"
	TemporalSuitabilityDispositionCoverage    = "coverage_hold"
	TemporalSuitabilityDispositionCandidate   = "candidate_no_signal_observed"
)

type TemporalSuitabilityProjectionConfig struct {
	PublicManifestPath        string
	StructureAuthorityPath    string
	SuitabilityComparisonPath string
	FirstResultPath           string
	SecondResultPath          string
	ExpectedCases             int
	ProjectedAt               time.Time
	OutputPath                string
}

type TemporalSuitabilityProjectionReport struct {
	SchemaVersion               int                                    `json:"schemaVersion"`
	ContractVersion             string                                 `json:"contractVersion"`
	ProjectedAt                 time.Time                              `json:"projectedAt"`
	PublicManifestSHA256        string                                 `json:"publicManifestSha256"`
	StructureAuthoritySHA256    string                                 `json:"structureAuthoritySha256"`
	SuitabilityComparisonSHA256 string                                 `json:"suitabilityComparisonSha256"`
	FirstResultSHA256           string                                 `json:"firstResultSha256"`
	SecondResultSHA256          string                                 `json:"secondResultSha256"`
	FirstAssessor               fillereval.TemporalAssessorIdentity    `json:"firstAssessor"`
	SecondAssessor              fillereval.TemporalAssessorIdentity    `json:"secondAssessor"`
	Cases                       int                                    `json:"cases"`
	Sources                     int                                    `json:"sources"`
	ProhibitedSources           int                                    `json:"prohibitedSources"`
	OperationalHoldSources      int                                    `json:"operationalHoldSources"`
	CoverageHoldSources         int                                    `json:"coverageHoldSources"`
	CandidateNoSignalSources    int                                    `json:"candidateNoSignalSources"`
	ProhibitedCases             int                                    `json:"prohibitedCases"`
	OperationalHoldCases        int                                    `json:"operationalHoldCases"`
	CoverageHoldCases           int                                    `json:"coverageHoldCases"`
	CandidateNoSignalCases      int                                    `json:"candidateNoSignalCases"`
	SourceDispositions          []TemporalSuitabilitySourceDisposition `json:"sourceDispositions"`
	CaseDispositions            []TemporalSuitabilityProjectedCase     `json:"caseDispositions"`
	TrainingAllowed             bool                                   `json:"trainingAllowed"`
	IngestionAllowed            bool                                   `json:"ingestionAllowed"`
	SchedulingAllowed           bool                                   `json:"schedulingAllowed"`
	ProductionAdmissionAllowed  bool                                   `json:"productionAdmissionAllowed"`
	NextAction                  string                                 `json:"nextAction"`
}

type TemporalSuitabilitySourceDisposition struct {
	SourceID         string                                    `json:"sourceId"`
	SourceSHA256     string                                    `json:"sourceSha256"`
	SourceDurationMS int64                                     `json:"sourceDurationMs"`
	Provenance       TemporalStructureSourceProvenance         `json:"provenance"`
	DerivedAliases   []string                                  `json:"derivedAliases"`
	Disposition      string                                    `json:"disposition"`
	Observations     []TemporalSuitabilityProjectedObservation `json:"observations,omitempty"`
}

type TemporalSuitabilityProjectedObservation struct {
	Kind      SuitabilityFlag                        `json:"kind"`
	Modality  SuitabilityModality                    `json:"modality"`
	StartMS   int64                                  `json:"startMs"`
	EndMS     int64                                  `json:"endMs"`
	Witnesses []TemporalSuitabilityProjectionWitness `json:"witnesses"`
}

type TemporalSuitabilityProjectionWitness struct {
	EvidenceAlias string `json:"evidenceAlias"`
	AssessorID    string `json:"assessorId"`
	CaseStartMS   int64  `json:"caseStartMs"`
	CaseEndMS     int64  `json:"caseEndMs"`
	SourceStartMS int64  `json:"sourceStartMs"`
	SourceEndMS   int64  `json:"sourceEndMs"`
}

type TemporalSuitabilityProjectedCase struct {
	EvidenceAlias        string   `json:"evidenceAlias"`
	SourceIDs            []string `json:"sourceIds"`
	InputDisposition     string   `json:"inputDisposition"`
	EffectiveDisposition string   `json:"effectiveDisposition"`
	TriggerSourceIDs     []string `json:"triggerSourceIds,omitempty"`
}

func PublishTemporalSuitabilityProjection(config TemporalSuitabilityProjectionConfig) (TemporalSuitabilityProjectionReport, string, error) {
	loaded, err := loadTemporalSuitabilityProjection(config)
	if err != nil {
		return TemporalSuitabilityProjectionReport{}, "", err
	}
	report, err := buildTemporalSuitabilityProjection(loaded, config.ProjectedAt.UTC())
	if err != nil {
		return TemporalSuitabilityProjectionReport{}, "", err
	}
	if err := validateTemporalSuitabilityProjectionReport(report); err != nil {
		return TemporalSuitabilityProjectionReport{}, "", fmt.Errorf("validate suitability projection: %w", err)
	}
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return TemporalSuitabilityProjectionReport{}, "", err
	}
	raw = append(raw, '\n')
	if err := writeTemporalTruthNew(config.OutputPath, raw, 0o600); err != nil {
		return TemporalSuitabilityProjectionReport{}, "", fmt.Errorf("publish suitability projection: %w", err)
	}
	return report, hashBytes(raw), nil
}
