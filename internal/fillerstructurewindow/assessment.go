package fillerstructurewindow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/fillerstructure"
)

const (
	AssessmentSchemaVersion   = 2
	AssessmentContractVersion = "filler-structure-window-assessment-v2"
)

type AssessmentState string

const (
	AssessmentAccepted           AssessmentState = "accepted"
	AssessmentOperationalFailure AssessmentState = "operational_failure"
)

// Assessment is one assessor family's immutable answer for one planned media window. Accepted
// segments use source-relative coordinates and cover the complete media window, including context.
type Assessment struct {
	SchemaVersion   int                             `json:"schemaVersion"`
	ContractVersion string                          `json:"contractVersion"`
	PlanSHA256      string                          `json:"planSha256"`
	MediaSetSHA256  string                          `json:"mediaSetSha256"`
	WindowOrdinal   int                             `json:"windowOrdinal"`
	Source          fillerstructure.Source          `json:"source"`
	Media           fillerstructure.AssessmentMedia `json:"media"`
	Assessor        fillerstructure.AssessorProfile `json:"assessor"`
	State           AssessmentState                 `json:"state"`
	Failure         string                          `json:"failure,omitempty"`
	Segments        []fillerstructure.Segment       `json:"segments,omitempty"`
	AssessedAt      time.Time                       `json:"assessedAt"`
	SHA256          string                          `json:"sha256"`
}

type AssessmentInput struct {
	MediaSet      MediaSet
	WindowOrdinal int
	Assessor      fillerstructure.AssessorProfile
	Failure       string
	Segments      []fillerstructure.Segment
	AssessedAt    time.Time
}

// NewAssessment closes either one exhaustive window answer or one attributable operational
// failure. Failure text is a closed machine reason, not unrestricted provider output.
func NewAssessment(input AssessmentInput) (Assessment, error) {
	state := AssessmentAccepted
	failure := strings.TrimSpace(input.Failure)
	if failure != input.Failure {
		return Assessment{}, errors.New("structure window assessment failure is not canonical")
	}
	if failure != "" {
		state = AssessmentOperationalFailure
	}
	plan := input.MediaSet.Plan
	var media fillerstructure.AssessmentMedia
	if input.WindowOrdinal >= 0 && input.WindowOrdinal < len(input.MediaSet.Windows) {
		media = input.MediaSet.Windows[input.WindowOrdinal].Media
	}
	assessment := Assessment{
		SchemaVersion: AssessmentSchemaVersion, ContractVersion: AssessmentContractVersion,
		PlanSHA256: plan.SHA256, MediaSetSHA256: input.MediaSet.SHA256,
		WindowOrdinal: input.WindowOrdinal, Source: plan.Source,
		Media: media, Assessor: input.Assessor, State: state, Failure: failure,
		Segments: slices.Clone(input.Segments), AssessedAt: input.AssessedAt.UTC().Round(0),
	}
	assessment.SHA256 = AssessmentSHA256(assessment)
	return assessment, ValidateAssessment(input.MediaSet, assessment)
}

// AssessmentSHA256 returns the record identity with its self-digest excluded.
func AssessmentSHA256(assessment Assessment) string {
	assessment.SHA256 = ""
	raw, err := json.Marshal(assessment)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}
