package fillerreview

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/fillereval"
)

const (
	TemporalStructureComparisonSchemaVersion   = 4
	TemporalStructureComparisonContractVersion = "filler-temporal-structure-comparison-v4"
	TemporalStructureNearBoundaryMS            = 2_000
	TemporalStructureBroadBoundaryMS           = 5_000
)

type TemporalStructureComparisonConfig struct {
	PublicManifestPath   string
	PrivateAuthorityPath string
	AssessmentPaths      []string
	ExpectedCases        int
	ComparedAt           time.Time
	OutputPath           string
}

type TemporalStructureComparisonReport struct {
	SchemaVersion              int                                    `json:"schemaVersion"`
	ContractVersion            string                                 `json:"contractVersion"`
	ChallengeID                string                                 `json:"challengeId"`
	PublicManifestSHA256       string                                 `json:"publicManifestSha256"`
	PrivateAuthoritySHA256     string                                 `json:"privateAuthoritySha256"`
	ComparedAt                 time.Time                              `json:"comparedAt"`
	BoundaryTolerancesMS       []int64                                `json:"boundaryTolerancesMs"`
	Cases                      int                                    `json:"cases"`
	Assessors                  []TemporalStructureAssessorReference   `json:"assessors"`
	AssessorSummaries          []TemporalStructureAssessorSummary     `json:"assessorSummaries"`
	ConstructionSummaries      []TemporalStructureConstructionSummary `json:"constructionSummaries"`
	SliceSummaries             []TemporalStructureConstructionSummary `json:"sliceSummaries,omitempty"`
	PairSummaries              []TemporalStructurePairSummary         `json:"pairSummaries"`
	AllAssessorsExactCorrect   int                                    `json:"allAssessorsExactCorrect"`
	CaseComparisons            []TemporalStructureCaseComparison      `json:"caseComparisons"`
	DiagnosticCandidates       []TemporalStructureDiagnosticCandidate `json:"diagnosticCandidates,omitempty"`
	Disposition                TemporalStructureComparisonDisposition `json:"disposition"`
	ProductionAdmissionAllowed bool                                   `json:"productionAdmissionAllowed"`
}

type TemporalStructureAssessorReference struct {
	AssessmentSetSHA256 string                              `json:"assessmentSetSha256"`
	RawResultSHA256     string                              `json:"rawResultSha256"`
	SnapshotFileSHA256  string                              `json:"snapshotFileSha256"`
	CapabilitySHA256    string                              `json:"capabilitySnapshotSha256"`
	CompletedAt         time.Time                           `json:"completedAt"`
	Assessor            fillereval.TemporalAssessorIdentity `json:"assessor"`
}

type TemporalStructureAssessorSummary struct {
	AssessorID             string                           `json:"assessorId"`
	Cases                  int                              `json:"cases"`
	OperationalFailures    int                              `json:"operationalFailures"`
	SemanticAbstentions    int                              `json:"semanticAbstentions"`
	UnusableClaims         int                              `json:"unusableClaims"`
	UnitComparable         int                              `json:"unitComparable"`
	ExactUnitCorrect       int                              `json:"exactUnitCorrect"`
	StandaloneClassCorrect int                              `json:"standaloneClassCorrect"`
	RoleComparable         int                              `json:"roleComparable"`
	RoleCorrect            int                              `json:"roleCorrect"`
	ExactLabelCorrect      int                              `json:"exactLabelCorrect"`
	ExactSegmentPlans      int                              `json:"exactSegmentPlans"`
	CoverageComplete       int                              `json:"coverageComplete"`
	UnderSplits            int                              `json:"underSplits"`
	OverSplits             int                              `json:"overSplits"`
	SegmentRoleTargets     int                              `json:"segmentRoleTargets"`
	SegmentRoleCorrect     int                              `json:"segmentRoleCorrect"`
	Boundary               TemporalStructureBoundarySummary `json:"boundary"`
}

type TemporalStructureConstructionSummary struct {
	AssessorID             string                           `json:"assessorId"`
	TruthUnit              fillereval.UnitKind              `json:"truthUnit,omitempty"`
	Slice                  string                           `json:"slice,omitempty"`
	Cases                  int                              `json:"cases"`
	OperationalFailures    int                              `json:"operationalFailures"`
	ExactUnitCorrect       int                              `json:"exactUnitCorrect"`
	StandaloneClassCorrect int                              `json:"standaloneClassCorrect"`
	RoleComparable         int                              `json:"roleComparable"`
	RoleCorrect            int                              `json:"roleCorrect"`
	ExactLabelCorrect      int                              `json:"exactLabelCorrect"`
	ExactSegmentPlans      int                              `json:"exactSegmentPlans"`
	CoverageComplete       int                              `json:"coverageComplete"`
	UnderSplits            int                              `json:"underSplits"`
	OverSplits             int                              `json:"overSplits"`
	SegmentRoleTargets     int                              `json:"segmentRoleTargets"`
	SegmentRoleCorrect     int                              `json:"segmentRoleCorrect"`
	Boundary               TemporalStructureBoundarySummary `json:"boundary"`
}

type TemporalStructureBoundarySummary struct {
	TruthTargets      int    `json:"truthTargets"`
	ComparableTargets int    `json:"comparableTargets"`
	Within2000MS      int    `json:"within2000Ms"`
	Within5000MS      int    `json:"within5000Ms"`
	MedianDistanceMS  *int64 `json:"medianDistanceMs,omitempty"`
}

type TemporalStructurePairSummary struct {
	Pair                      string `json:"pair"`
	Cases                     int    `json:"cases"`
	OperationallyComparable   int    `json:"operationallyComparable"`
	ExactUnitAgreement        int    `json:"exactUnitAgreement"`
	StandaloneClassAgreement  int    `json:"standaloneClassAgreement"`
	RoleComparable            int    `json:"roleComparable"`
	RoleAgreement             int    `json:"roleAgreement"`
	ExactLabelAgreement       int    `json:"exactLabelAgreement"`
	ExactSegmentPlanAgreement int    `json:"exactSegmentPlanAgreement"`
}

type TemporalStructureTruthLabel struct {
	Unit   fillereval.UnitKind     `json:"unit"`
	Role   fillereval.TemporalRole `json:"role,omitempty"`
	Slices []string                `json:"slices,omitempty"`
}

type TemporalStructurePredictedLabel struct {
	Unit     fillereval.UnitKind                 `json:"unit,omitempty"`
	Role     fillereval.TemporalRole             `json:"role,omitempty"`
	Failure  fillereval.TemporalFailureCode      `json:"failure,omitempty"`
	Segments []TemporalStructurePredictedSegment `json:"segments,omitempty"`
}

type TemporalStructurePredictedSegment struct {
	StartMS int64                          `json:"startMs"`
	EndMS   int64                          `json:"endMs"`
	Role    fillereval.TemporalSegmentRole `json:"role"`
}

type TemporalStructureTruthSegment struct {
	StartMS int64                          `json:"startMs"`
	EndMS   int64                          `json:"endMs"`
	Role    fillereval.TemporalSegmentRole `json:"role"`
}

type TemporalStructureBoundaryDistance struct {
	Kind              string `json:"kind"`
	TruthAtMS         int64  `json:"truthAtMs"`
	NearestDecisiveMS int64  `json:"nearestDecisiveMs"`
	DistanceMS        int64  `json:"distanceMs"`
	Within2000MS      bool   `json:"within2000Ms"`
	Within5000MS      bool   `json:"within5000Ms"`
}

type TemporalStructureAssessorCaseResult struct {
	AssessorID             string                              `json:"assessorId"`
	Prediction             TemporalStructurePredictedLabel     `json:"prediction"`
	UnitCorrect            bool                                `json:"unitCorrect"`
	StandaloneClassCorrect bool                                `json:"standaloneClassCorrect"`
	RoleComparable         bool                                `json:"roleComparable"`
	RoleCorrect            bool                                `json:"roleCorrect"`
	ExactLabelCorrect      bool                                `json:"exactLabelCorrect"`
	CoverageComplete       bool                                `json:"coverageComplete"`
	UnderSplits            int                                 `json:"underSplits"`
	OverSplits             int                                 `json:"overSplits"`
	SegmentRoleTargets     int                                 `json:"segmentRoleTargets"`
	SegmentRoleCorrect     int                                 `json:"segmentRoleCorrect"`
	ExactSegmentPlan       bool                                `json:"exactSegmentPlan"`
	BoundaryDistances      []TemporalStructureBoundaryDistance `json:"boundaryDistances,omitempty"`
}

type TemporalStructureCaseComparison struct {
	Alias         string                                `json:"alias"`
	DurationMS    int64                                 `json:"durationMs"`
	Truth         TemporalStructureTruthLabel           `json:"truth"`
	TruthSegments []TemporalStructureTruthSegment       `json:"truthSegments"`
	Assessments   []TemporalStructureAssessorCaseResult `json:"assessments"`
}

type TemporalStructureDiagnosticCandidate struct {
	Alias   string   `json:"alias"`
	Reasons []string `json:"reasons"`
}

type TemporalStructureComparisonDisposition struct {
	NextAction              string   `json:"nextAction"`
	TargetedCases           []string `json:"targetedCases,omitempty"`
	BlindHumanAuditRequired bool     `json:"blindHumanAuditRequired"`
	TrainingAllowed         bool     `json:"trainingAllowed"`
}

// CompareTemporalStructureAssessments scores independently locked full-video
// model results against construction authority. It is an evaluation artifact,
// never a production-admission decision.
func CompareTemporalStructureAssessments(config TemporalStructureComparisonConfig) (TemporalStructureComparisonReport, error) {
	loaded, err := loadTemporalStructureComparison(config)
	if err != nil {
		return TemporalStructureComparisonReport{}, err
	}
	return buildTemporalStructureComparison(loaded, config.ComparedAt.UTC()), nil
}

func PublishTemporalStructureComparison(config TemporalStructureComparisonConfig) (TemporalStructureComparisonReport, string, error) {
	if strings.TrimSpace(config.OutputPath) == "" {
		return TemporalStructureComparisonReport{}, "", fmt.Errorf("temporal structure comparison output path is required")
	}
	report, err := CompareTemporalStructureAssessments(config)
	if err != nil {
		return TemporalStructureComparisonReport{}, "", err
	}
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return TemporalStructureComparisonReport{}, "", err
	}
	raw = append(raw, '\n')
	if err := writeTemporalTruthNew(config.OutputPath, raw, 0o600); err != nil {
		return TemporalStructureComparisonReport{}, "", fmt.Errorf("publish temporal structure comparison: %w", err)
	}
	return report, hashBytes(raw), nil
}

type temporalStructureComparisonLoaded struct {
	manifest     TemporalStructureChallengeManifest
	authority    TemporalStructureChallengeAuthority
	publicSHA    string
	authoritySHA string
	assessments  []temporalStructureLoadedAssessment
}

func loadTemporalStructureComparison(config TemporalStructureComparisonConfig) (temporalStructureComparisonLoaded, error) {
	if strings.TrimSpace(config.PublicManifestPath) == "" || strings.TrimSpace(config.PrivateAuthorityPath) == "" || len(config.AssessmentPaths) < 2 || config.ExpectedCases <= 0 || config.ComparedAt.IsZero() {
		return temporalStructureComparisonLoaded{}, fmt.Errorf("temporal structure comparison requires challenge authority, at least two assessment sets, exact case count, and comparison time")
	}
	manifest, authority, publicSHA, authoritySHA, err := LoadTemporalStructureChallenge(config.PublicManifestPath, config.PrivateAuthorityPath, config.ExpectedCases)
	if err != nil {
		return temporalStructureComparisonLoaded{}, err
	}
	loaded := temporalStructureComparisonLoaded{manifest: manifest, authority: authority, publicSHA: publicSHA, authoritySHA: authoritySHA}
	seenFiles := make(map[string]struct{}, len(config.AssessmentPaths))
	seenAssessors := make(map[string]struct{}, len(config.AssessmentPaths))
	modelFamilies := make(map[string]struct{}, len(config.AssessmentPaths))
	for _, path := range config.AssessmentPaths {
		assessment, err := loadTemporalStructureAssessment(path, manifest, publicSHA, authoritySHA)
		if err != nil {
			return temporalStructureComparisonLoaded{}, fmt.Errorf("assessment %q: %w", path, err)
		}
		if _, duplicate := seenFiles[assessment.fileSHA]; duplicate {
			return temporalStructureComparisonLoaded{}, fmt.Errorf("temporal structure comparison repeats an assessment file")
		}
		if _, duplicate := seenAssessors[assessment.set.Assessor.ID]; duplicate {
			return temporalStructureComparisonLoaded{}, fmt.Errorf("temporal structure comparison repeats assessor %q", assessment.set.Assessor.ID)
		}
		if config.ComparedAt.Before(assessment.set.CompletedAt) {
			return temporalStructureComparisonLoaded{}, fmt.Errorf("temporal structure comparison predates assessor %q", assessment.set.Assessor.ID)
		}
		seenFiles[assessment.fileSHA] = struct{}{}
		seenAssessors[assessment.set.Assessor.ID] = struct{}{}
		modelFamilies[strings.ToLower(assessment.set.Assessor.ModelFamily)] = struct{}{}
		loaded.assessments = append(loaded.assessments, assessment)
	}
	if len(modelFamilies) < 2 {
		return temporalStructureComparisonLoaded{}, fmt.Errorf("temporal structure comparison requires at least two model families")
	}
	sort.Slice(loaded.assessments, func(i, j int) bool {
		return loaded.assessments[i].set.Assessor.ID < loaded.assessments[j].set.Assessor.ID
	})
	return loaded, nil
}
