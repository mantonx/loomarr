package fillerreview

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/fillereval"
	"github.com/loomarr/loomarr/internal/fillerstructure"
)

const (
	TemporalStructureDecisionSchemaVersion   = 2
	TemporalStructureDecisionContractVersion = "filler-temporal-structure-decision-v2"

	TemporalStructureDecisionConfirmed = string(fillerstructure.StatusConfirmed)
	TemporalStructureDecisionHeld      = string(fillerstructure.StatusHeld)

	TemporalStructureDispositionFillerCandidate = string(fillerstructure.DispositionFillerCandidate)
	TemporalStructureDispositionNonFiller       = string(fillerstructure.DispositionNonFiller)
	TemporalStructureDispositionUnresolved      = string(fillerstructure.DispositionUnresolved)
)

// TemporalStructureDecisionConfig deliberately has no private-authority path.
// Decisions may bind its digest but may never inspect construction truth.
type TemporalStructureDecisionConfig struct {
	PublicManifestPath     string
	PrivateAuthoritySHA256 string
	AssessmentPaths        []string
	ExpectedCases          int
	DecidedAt              time.Time
	OutputPath             string
}

type TemporalStructureDecisionReport struct {
	SchemaVersion                int                                    `json:"schemaVersion"`
	ContractVersion              string                                 `json:"contractVersion"`
	ChallengeID                  string                                 `json:"challengeId"`
	PublicManifestSHA256         string                                 `json:"publicManifestSha256"`
	PrivateAuthoritySHA256       string                                 `json:"privateAuthoritySha256"`
	AssessmentMediaProfileSHA256 string                                 `json:"assessmentMediaProfileSha256"`
	DecidedAt                    time.Time                              `json:"decidedAt"`
	BoundaryToleranceMS          int64                                  `json:"boundaryToleranceMs"`
	Cases                        int                                    `json:"cases"`
	ConfirmedCases               int                                    `json:"confirmedCases"`
	HeldCases                    int                                    `json:"heldCases"`
	IndependentModelFamilies     int                                    `json:"independentModelFamilies"`
	Assessors                    []TemporalStructureAssessorReference   `json:"assessors"`
	HoldReasons                  []TemporalStructureDecisionReasonCount `json:"holdReasons,omitempty"`
	Decisions                    []TemporalStructureCaseDecision        `json:"decisions"`
	ProductionAdmissionAllowed   bool                                   `json:"productionAdmissionAllowed"`
}

type TemporalStructureDecisionReasonCount struct {
	Reason string `json:"reason"`
	Cases  int    `json:"cases"`
}

type TemporalStructureCaseDecision struct {
	Alias       string                                          `json:"alias"`
	DurationMS  int64                                           `json:"durationMs"`
	Status      string                                          `json:"status"`
	ReasonCodes []string                                        `json:"reasonCodes"`
	Unit        fillereval.UnitKind                             `json:"unit,omitempty"`
	Role        fillereval.TemporalRole                         `json:"role,omitempty"`
	Segments    []TemporalStructureDecisionSegment              `json:"segments,omitempty"`
	Candidates  []TemporalStructureDecisionCandidateObservation `json:"candidates"`
}

type TemporalStructureDecisionSegment struct {
	StartMS     int64                          `json:"startMs"`
	EndMS       int64                          `json:"endMs"`
	Disposition string                         `json:"disposition"`
	Role        fillereval.TemporalSegmentRole `json:"role"`
}

type TemporalStructureDecisionCandidateObservation struct {
	AssessorID  string                              `json:"assessorId"`
	ModelFamily string                              `json:"modelFamily"`
	Failure     fillereval.TemporalFailureCode      `json:"failure,omitempty"`
	Unit        fillereval.UnitKind                 `json:"unit,omitempty"`
	Role        fillereval.TemporalRole             `json:"role,omitempty"`
	Segments    []TemporalStructurePredictedSegment `json:"segments,omitempty"`
}

// PublishTemporalStructureDecisions reduces independently locked observations
// without opening private truth, then atomically publishes the immutable result.
func PublishTemporalStructureDecisions(config TemporalStructureDecisionConfig) (TemporalStructureDecisionReport, string, error) {
	if strings.TrimSpace(config.OutputPath) == "" {
		return TemporalStructureDecisionReport{}, "", fmt.Errorf("temporal structure decision output path is required")
	}
	loaded, err := loadTemporalStructureDecision(config)
	if err != nil {
		return TemporalStructureDecisionReport{}, "", err
	}
	report := buildTemporalStructureDecision(loaded, config.DecidedAt.UTC())
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return TemporalStructureDecisionReport{}, "", err
	}
	raw = append(raw, '\n')
	if err := writeTemporalTruthNew(config.OutputPath, raw, 0o600); err != nil {
		return TemporalStructureDecisionReport{}, "", fmt.Errorf("publish temporal structure decisions: %w", err)
	}
	return report, hashBytes(raw), nil
}

type temporalStructureDecisionLoaded struct {
	manifest     TemporalStructureChallengeManifest
	publicSHA    string
	authoritySHA string
	assessments  []temporalStructureLoadedAssessment
	families     int
}

func loadTemporalStructureDecision(config TemporalStructureDecisionConfig) (temporalStructureDecisionLoaded, error) {
	if strings.TrimSpace(config.PublicManifestPath) == "" || !reviewSHA256(config.PrivateAuthoritySHA256) || len(config.AssessmentPaths) < 2 || config.ExpectedCases <= 0 || config.DecidedAt.IsZero() {
		return temporalStructureDecisionLoaded{}, fmt.Errorf("temporal structure decision requires a public challenge, private-authority digest, at least two assessments, exact case count, and decision time")
	}
	manifest, publicSHA, err := LoadTemporalStructureChallengePublic(config.PublicManifestPath, config.ExpectedCases)
	if err != nil {
		return temporalStructureDecisionLoaded{}, err
	}
	loaded := temporalStructureDecisionLoaded{manifest: manifest, publicSHA: publicSHA, authoritySHA: config.PrivateAuthoritySHA256}
	seenFiles := make(map[string]struct{}, len(config.AssessmentPaths))
	seenAssessors := make(map[string]struct{}, len(config.AssessmentPaths))
	families := make(map[string]struct{}, len(config.AssessmentPaths))
	for _, path := range config.AssessmentPaths {
		assessment, err := loadTemporalStructureAssessment(path, manifest, publicSHA, config.PrivateAuthoritySHA256)
		if err != nil {
			return temporalStructureDecisionLoaded{}, fmt.Errorf("assessment %q: %w", path, err)
		}
		if _, duplicate := seenFiles[assessment.fileSHA]; duplicate {
			return temporalStructureDecisionLoaded{}, fmt.Errorf("temporal structure decision repeats an assessment file")
		}
		if _, duplicate := seenAssessors[assessment.set.Assessor.ID]; duplicate {
			return temporalStructureDecisionLoaded{}, fmt.Errorf("temporal structure decision repeats assessor %q", assessment.set.Assessor.ID)
		}
		if config.DecidedAt.Before(assessment.set.LockedAt) {
			return temporalStructureDecisionLoaded{}, fmt.Errorf("temporal structure decision predates locked assessor %q", assessment.set.Assessor.ID)
		}
		seenFiles[assessment.fileSHA] = struct{}{}
		seenAssessors[assessment.set.Assessor.ID] = struct{}{}
		families[strings.ToLower(strings.TrimSpace(assessment.set.Assessor.ModelFamily))] = struct{}{}
		loaded.assessments = append(loaded.assessments, assessment)
	}
	if len(families) < 2 {
		return temporalStructureDecisionLoaded{}, fmt.Errorf("temporal structure decision requires at least two independent model families")
	}
	loaded.families = len(families)
	sort.Slice(loaded.assessments, func(i, j int) bool {
		return loaded.assessments[i].set.Assessor.ID < loaded.assessments[j].set.Assessor.ID
	})
	return loaded, nil
}

func buildTemporalStructureDecision(loaded temporalStructureDecisionLoaded, decidedAt time.Time) TemporalStructureDecisionReport {
	report := TemporalStructureDecisionReport{
		SchemaVersion: TemporalStructureDecisionSchemaVersion, ContractVersion: TemporalStructureDecisionContractVersion,
		ChallengeID: loaded.manifest.ChallengeID, PublicManifestSHA256: loaded.publicSHA,
		PrivateAuthoritySHA256:       loaded.authoritySHA,
		AssessmentMediaProfileSHA256: loaded.manifest.AssessmentMediaProfileSHA256, DecidedAt: decidedAt,
		BoundaryToleranceMS: TemporalStructureNearBoundaryMS, Cases: len(loaded.manifest.Cases),
		IndependentModelFamilies: loaded.families, ProductionAdmissionAllowed: false,
	}
	for _, assessment := range loaded.assessments {
		report.Assessors = append(report.Assessors, TemporalStructureAssessorReference{
			AssessmentSetSHA256: assessment.fileSHA, RawResultSHA256: assessment.set.RawResultSHA256,
			SnapshotFileSHA256: assessment.set.SnapshotFileSHA256, CapabilitySHA256: assessment.set.CapabilitySnapshotSHA256,
			CompletedAt: assessment.set.CompletedAt, Assessor: assessment.set.Assessor,
		})
	}
	reasonCounts := make(map[string]int)
	for _, item := range loaded.manifest.Cases {
		candidates := make([]temporalStructureDecisionCandidate, 0, len(loaded.assessments))
		for _, assessment := range loaded.assessments {
			candidates = append(candidates, temporalStructureDecisionCandidate{
				assessor: assessment.set.Assessor, capabilitySHA: assessment.set.CapabilitySnapshotSHA256,
				evidenceContract: assessment.set.ContractVersion, assessmentSHA: assessment.fileSHA,
				assessment: assessment.byAlias[item.Alias],
			})
		}
		decision := reduceTemporalStructureDecision(item.Alias, item.Video.SHA256, item.Video.Bytes, item.Video.DurationMS, loaded.manifest.AssessmentMediaProfileSHA256, loaded.authoritySHA, candidates)
		report.Decisions = append(report.Decisions, decision)
		if decision.Status == TemporalStructureDecisionConfirmed {
			report.ConfirmedCases++
		} else {
			report.HeldCases++
			for _, reason := range decision.ReasonCodes {
				reasonCounts[reason]++
			}
		}
	}
	for reason, cases := range reasonCounts {
		report.HoldReasons = append(report.HoldReasons, TemporalStructureDecisionReasonCount{Reason: reason, Cases: cases})
	}
	sort.Slice(report.HoldReasons, func(i, j int) bool { return report.HoldReasons[i].Reason < report.HoldReasons[j].Reason })
	return report
}
