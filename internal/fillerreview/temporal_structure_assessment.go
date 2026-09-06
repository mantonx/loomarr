package fillerreview

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/fillereval"
	"github.com/loomarr/loomarr/internal/fillerstructure"
)

const (
	TemporalStructureAssessmentSchemaVersion   = 4
	TemporalStructureAssessmentContractVersion = "filler-temporal-structure-assessment-v4"
	temporalStructureMaximumDecisiveTimes      = fillerstructure.DirectVideoMaximumDecisiveTime
)

// TemporalStructureAssessmentSet is the immutable, full-video assessor
// result consumed by the construction-authority comparison. It deliberately
// binds both halves of the challenge while containing no private truth labels.
type TemporalStructureAssessmentSet struct {
	SchemaVersion              int                                 `json:"schemaVersion"`
	ContractVersion            string                              `json:"contractVersion"`
	ChallengeID                string                              `json:"challengeId"`
	PublicManifestSHA256       string                              `json:"publicManifestSha256"`
	PrivateAuthoritySHA256     string                              `json:"privateAuthoritySha256"`
	RawResultSHA256            string                              `json:"rawResultSha256"`
	SnapshotFileSHA256         string                              `json:"snapshotFileSha256"`
	CapabilitySnapshotSHA256   string                              `json:"capabilitySnapshotSha256"`
	CompletedAt                time.Time                           `json:"completedAt"`
	LockedAt                   time.Time                           `json:"lockedAt"`
	Assessor                   fillereval.TemporalAssessorIdentity `json:"assessor"`
	Assessments                []TemporalStructureAssessment       `json:"assessments"`
	ProductionAdmissionAllowed bool                                `json:"productionAdmissionAllowed"`
}

type TemporalStructureAssessment struct {
	Alias              string                                 `json:"alias"`
	Unit               *TemporalStructureUnitClaim            `json:"unit,omitempty"`
	Role               *TemporalStructureRoleClaim            `json:"role,omitempty"`
	Segments           []TemporalStructureSegmentClaim        `json:"segments,omitempty"`
	OperationalFailure *fillereval.TemporalOperationalFailure `json:"operationalFailure,omitempty"`
	Inference          fillereval.TemporalInference           `json:"inference"`
}

// TemporalStructureSegmentClaim is one interval in a complete coverage-preserving prediction.
// Roles are independent per interval; a compilation never inherits one reel-wide role.
type TemporalStructureSegmentClaim struct {
	StartMS      int64                          `json:"startMs"`
	EndMS        int64                          `json:"endMs"`
	Role         fillereval.TemporalSegmentRole `json:"role"`
	DecisiveAtMS []int64                        `json:"decisiveAtMs,omitempty"`
	Reason       string                         `json:"reason"`
}

type TemporalStructureUnitClaim struct {
	Kind         fillereval.UnitKind `json:"kind"`
	DecisiveAtMS []int64             `json:"decisiveAtMs,omitempty"`
	Reason       string              `json:"reason"`
}

type TemporalStructureRoleClaim struct {
	Kind         fillereval.TemporalRole `json:"kind"`
	DecisiveAtMS []int64                 `json:"decisiveAtMs,omitempty"`
	Reason       string                  `json:"reason"`
}

type temporalStructureLoadedAssessment struct {
	set      TemporalStructureAssessmentSet
	fileSHA  string
	byAlias  map[string]TemporalStructureAssessment
	filePath string
}

func loadTemporalStructureAssessment(path string, manifest TemporalStructureChallengeManifest, publicSHA, authoritySHA string) (temporalStructureLoadedAssessment, error) {
	set, err := readStrictJSON[TemporalStructureAssessmentSet](path)
	if err != nil {
		return temporalStructureLoadedAssessment{}, fmt.Errorf("read temporal structure assessment: %w", err)
	}
	fileSHA, err := hashFile(path)
	if err != nil {
		return temporalStructureLoadedAssessment{}, err
	}
	durationByAlias := make(map[string]int64, len(manifest.Cases))
	for _, item := range manifest.Cases {
		durationByAlias[item.Alias] = item.Video.DurationMS
	}
	byAlias, err := validateTemporalStructureAssessmentSet(set, manifest, publicSHA, authoritySHA, durationByAlias)
	if err != nil {
		return temporalStructureLoadedAssessment{}, err
	}
	return temporalStructureLoadedAssessment{set: set, fileSHA: fileSHA, byAlias: byAlias, filePath: path}, nil
}

func validateTemporalStructureAssessmentSet(set TemporalStructureAssessmentSet, manifest TemporalStructureChallengeManifest, publicSHA, authoritySHA string, durationByAlias map[string]int64) (map[string]TemporalStructureAssessment, error) {
	if set.SchemaVersion != TemporalStructureAssessmentSchemaVersion || set.ContractVersion != TemporalStructureAssessmentContractVersion || set.ChallengeID != manifest.ChallengeID || set.PublicManifestSHA256 != publicSHA || set.PrivateAuthoritySHA256 != authoritySHA || !reviewSHA256(set.RawResultSHA256) || !reviewSHA256(set.SnapshotFileSHA256) || !reviewSHA256(set.CapabilitySnapshotSHA256) || set.CompletedAt.Before(manifest.GeneratedAt) || set.LockedAt.Before(set.CompletedAt) || set.ProductionAdmissionAllowed {
		return nil, fmt.Errorf("temporal structure assessment identity or production disposition is invalid")
	}
	if strings.TrimSpace(set.Assessor.ID) == "" || strings.TrimSpace(set.Assessor.Provider) == "" || strings.TrimSpace(set.Assessor.Model) == "" || strings.TrimSpace(set.Assessor.ModelFamily) == "" || !reviewSHA256(set.Assessor.ModelDigest) || strings.TrimSpace(set.Assessor.PromptVersion) == "" {
		return nil, fmt.Errorf("temporal structure assessor identity is incomplete")
	}
	if len(set.Assessments) != len(durationByAlias) {
		return nil, fmt.Errorf("temporal structure assessor supplied %d cases; want %d", len(set.Assessments), len(durationByAlias))
	}
	byAlias := make(map[string]TemporalStructureAssessment, len(set.Assessments))
	for _, assessment := range set.Assessments {
		duration, exists := durationByAlias[assessment.Alias]
		if !exists {
			return nil, fmt.Errorf("temporal structure assessment names unknown alias %q", assessment.Alias)
		}
		if _, duplicate := byAlias[assessment.Alias]; duplicate {
			return nil, fmt.Errorf("temporal structure assessment repeats alias %q", assessment.Alias)
		}
		if err := validateTemporalStructureAssessment(assessment, duration, manifest.GeneratedAt, set.CompletedAt); err != nil {
			return nil, fmt.Errorf("temporal structure assessment %q: %w", assessment.Alias, err)
		}
		byAlias[assessment.Alias] = assessment
	}
	return byAlias, nil
}

func validateTemporalStructureAssessment(assessment TemporalStructureAssessment, durationMS int64, generatedAt, completedAt time.Time) error {
	if err := validateTemporalStructureInference(assessment.Inference, generatedAt, completedAt); err != nil {
		return err
	}
	if assessment.OperationalFailure != nil {
		if assessment.Unit != nil || assessment.Role != nil || len(assessment.Segments) != 0 || !validTemporalStructureFailure(assessment.OperationalFailure.Code) || strings.TrimSpace(assessment.OperationalFailure.Detail) == "" || assessment.Inference.Calls[len(assessment.Inference.Calls)-1].OperationalFailure != assessment.OperationalFailure.Code {
			return fmt.Errorf("operational failure is invalid or mixed with semantic claims")
		}
		return nil
	}
	if assessment.Unit == nil || !validTemporalStructureUnit(assessment.Unit.Kind) || strings.TrimSpace(assessment.Unit.Reason) == "" || !validTemporalStructureTimes(assessment.Unit.DecisiveAtMS, durationMS, temporalStructureUnitMayLackDecisiveEvidence(assessment.Unit.Kind)) {
		return fmt.Errorf("unit claim or decisive timestamps are invalid")
	}
	if assessment.Unit.Kind == fillereval.UnitStandalone {
		if assessment.Role == nil || !validHumanRole(assessment.Role.Kind) || strings.TrimSpace(assessment.Role.Reason) == "" || !validTemporalStructureTimes(assessment.Role.DecisiveAtMS, durationMS, assessment.Role.Kind == fillereval.TemporalRoleUnclear) {
			return fmt.Errorf("standalone role claim or decisive timestamps are invalid")
		}
	} else if assessment.Role != nil {
		return fmt.Errorf("non-standalone assessment carries a role claim")
	}
	if err := validateTemporalStructureSegments(assessment, durationMS); err != nil {
		return err
	}
	if len(assessment.Inference.Calls) != 1 || assessment.Inference.Calls[0].Axis != "structure" || assessment.Inference.Calls[0].OperationalFailure != "" {
		return fmt.Errorf("successful assessment requires one atomic structure call")
	}
	return nil
}

func validateTemporalStructureSegments(assessment TemporalStructureAssessment, durationMS int64) error {
	if len(assessment.Segments) == 0 || assessment.Segments[0].StartMS != 0 || assessment.Segments[len(assessment.Segments)-1].EndMS != durationMS {
		return fmt.Errorf("segment plan must cover the complete video")
	}
	programmeSegments := 0
	for index, segment := range assessment.Segments {
		mayLackDecisiveEvidence := segment.Role == fillereval.TemporalSegmentAmbiguous || segment.Role == fillereval.TemporalSegmentUnusable
		if segment.StartMS < 0 || segment.EndMS <= segment.StartMS || index > 0 && segment.StartMS != assessment.Segments[index-1].EndMS || !validTemporalSegmentRole(segment.Role) || strings.TrimSpace(segment.Reason) == "" || !validTemporalStructureTimes(segment.DecisiveAtMS, durationMS, mayLackDecisiveEvidence) {
			return fmt.Errorf("segment %d is invalid or breaks complete coverage", index)
		}
		for _, atMS := range segment.DecisiveAtMS {
			if atMS < segment.StartMS || atMS > segment.EndMS {
				return fmt.Errorf("segment %d decisive timestamp is outside its interval", index)
			}
		}
		if segment.Role == fillereval.TemporalSegmentProgrammeFragment {
			programmeSegments++
		}
	}
	switch assessment.Unit.Kind {
	case fillereval.UnitStandalone:
		if len(assessment.Segments) != 1 || string(assessment.Segments[0].Role) != string(assessment.Role.Kind) {
			return fmt.Errorf("standalone assessment requires one whole-video segment matching its role")
		}
	case fillereval.UnitCompilation:
		if len(assessment.Segments) < 2 || programmeSegments > 0 {
			return fmt.Errorf("compilation assessment requires at least two covered segments")
		}
	case fillereval.UnitProgrammeExcerpt:
		if len(assessment.Segments) != 1 || programmeSegments != 1 {
			return fmt.Errorf("programme excerpt requires an explicit programme_fragment segment")
		}
	case fillereval.UnitProgrammeSpots:
		fillerSegments := 0
		for _, segment := range assessment.Segments {
			if temporalStructureFillerSegmentRole(segment.Role) {
				fillerSegments++
			}
		}
		if programmeSegments < 2 || fillerSegments < 1 {
			return fmt.Errorf("programme with spots requires programme fragments around at least one filler segment")
		}
	}
	return nil
}

func validTemporalStructureUnit(unit fillereval.UnitKind) bool {
	return unit == fillereval.UnitStandalone || unit == fillereval.UnitCompilation || unit == fillereval.UnitProgrammeExcerpt || unit == fillereval.UnitProgrammeSpots || unit == fillereval.UnitUnusable || unit == fillereval.UnitUnclear
}

func temporalStructureUnitMayLackDecisiveEvidence(unit fillereval.UnitKind) bool {
	return unit == fillereval.UnitUnclear || unit == fillereval.UnitUnusable
}

func temporalStructureFillerSegmentRole(role fillereval.TemporalSegmentRole) bool {
	switch role {
	case fillereval.TemporalSegmentCommercial, fillereval.TemporalSegmentPromo, fillereval.TemporalSegmentBumper,
		fillereval.TemporalSegmentPSA, fillereval.TemporalSegmentStationID, fillereval.TemporalSegmentTrailer,
		fillereval.TemporalSegmentInterstitial:
		return true
	default:
		return false
	}
}

func validateTemporalStructureInference(inference fillereval.TemporalInference, generatedAt, completedAt time.Time) error {
	if inference.AssessedAt.Before(generatedAt) || inference.AssessedAt.After(completedAt) || inference.Attempts < 1 || inference.Attempts != len(inference.Calls) || inference.LatencyMS < 0 || inference.PromptTokens < 0 || inference.CompletionTokens < 0 {
		return fmt.Errorf("inference timing, attempts, or aggregate accounting is invalid")
	}
	var latencyMS, promptTokens, completionTokens int64
	for index, call := range inference.Calls {
		if call.Attempt != index+1 || call.LatencyMS < 0 || call.PromptTokens < 0 || call.CompletionTokens < 0 || call.Axis != "structure" || call.ResponseSHA256 != "" && !reviewSHA256(call.ResponseSHA256) || call.ResponseSHA256 == "" && call.OperationalFailure == "" || call.OperationalFailure != "" && !validTemporalStructureFailure(call.OperationalFailure) {
			return fmt.Errorf("inference call %d has invalid order, axis, digest, failure, or accounting", index+1)
		}
		latencyMS += call.LatencyMS
		promptTokens += call.PromptTokens
		completionTokens += call.CompletionTokens
	}
	if latencyMS != inference.LatencyMS || promptTokens != inference.PromptTokens || completionTokens != inference.CompletionTokens {
		return fmt.Errorf("inference aggregate accounting does not match its call ledger")
	}
	return nil
}

func validTemporalStructureTimes(values []int64, durationMS int64, mayBeEmpty bool) bool {
	if len(values) == 0 {
		return mayBeEmpty
	}
	if len(values) > temporalStructureMaximumDecisiveTimes {
		return false
	}
	if !sort.SliceIsSorted(values, func(i, j int) bool { return values[i] < values[j] }) {
		return false
	}
	previous := int64(-1)
	for _, value := range values {
		if value < 0 || value > durationMS || value == previous {
			return false
		}
		previous = value
	}
	return true
}

func validTemporalStructureFailure(code fillereval.TemporalFailureCode) bool {
	switch code {
	case fillereval.TemporalFailureTimeout, fillereval.TemporalFailureProvider, fillereval.TemporalFailureInvalidResponse, fillereval.TemporalFailureEvidence, fillereval.TemporalFailureContextExhausted:
		return true
	default:
		return false
	}
}

func validTemporalSegmentRole(role fillereval.TemporalSegmentRole) bool {
	switch role {
	case fillereval.TemporalSegmentCommercial, fillereval.TemporalSegmentPromo, fillereval.TemporalSegmentBumper,
		fillereval.TemporalSegmentPSA, fillereval.TemporalSegmentStationID, fillereval.TemporalSegmentTrailer,
		fillereval.TemporalSegmentInterstitial, fillereval.TemporalSegmentProgrammeFragment,
		fillereval.TemporalSegmentNonFiller, fillereval.TemporalSegmentAmbiguous, fillereval.TemporalSegmentUnusable:
		return true
	default:
		return false
	}
}
