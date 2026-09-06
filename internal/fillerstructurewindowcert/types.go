// Package fillerstructurewindowcert certifies the long-reel window protocol against private,
// known-truth timelines. It grants neither training nor production materialization authority.
package fillerstructurewindowcert

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"time"

	"github.com/loomarr/loomarr/internal/fillerstructure"
	"github.com/loomarr/loomarr/internal/fillerstructurewindow"
)

const (
	SuiteSchemaVersion   = 1
	SuiteContractVersion = "filler-structure-window-certification-suite-v1"

	ReportSchemaVersion   = 1
	ReportContractVersion = "filler-structure-window-certification-report-v1"

	BoundaryToleranceMS int64 = 2_000
	MinimumSliceCases   int   = 6

	WordlessEvidenceContract = "filler-structure-wordless-boundary-evidence-v1"
	MotionEvidenceContract   = "filler-structure-window-motion-evidence-v1"
)

type Slice string

const (
	SliceSeamOverlap      Slice = "seam_overlap"
	SliceSeamPrimaryLeft  Slice = "seam_primary_left"
	SliceSeamPrimaryRight Slice = "seam_primary_right"
	SliceAdjacentSameRole Slice = "adjacent_same_role"
	SliceCrossingSeam     Slice = "crossing_seam"
	SliceProgrammeFiller  Slice = "programme_filler_join"
	SliceWordlessJoin     Slice = "wordless_join"
	SliceHighMotionWindow Slice = "high_motion_window"
	SliceHighByteWindow   Slice = "high_byte_window"
)

var requiredSlices = []Slice{
	SliceSeamOverlap,
	SliceSeamPrimaryLeft,
	SliceSeamPrimaryRight,
	SliceAdjacentSameRole,
	SliceCrossingSeam,
	SliceProgrammeFiller,
	SliceWordlessJoin,
	SliceHighMotionWindow,
	SliceHighByteWindow,
}

// RequiredSlices returns a fresh copy of the immutable certification slice vocabulary.
func RequiredSlices() []Slice { return slices.Clone(requiredSlices) }

// MeasuredSliceEvidence binds a content trait that cannot be derived from timeline geometry to
// separately validated evidence. Certification deliberately does not infer these traits from labels.
type MeasuredSliceEvidence struct {
	Slice               Slice  `json:"slice"`
	EvidenceContract    string `json:"evidenceContract"`
	EvidenceSHA256      string `json:"evidenceSha256"`
	TargetBoundaryMS    int64  `json:"targetBoundaryMs"`
	TargetWindowOrdinal int    `json:"targetWindowOrdinal"`
}

type Case struct {
	ID               string                         `json:"id"`
	MediaSet         fillerstructurewindow.MediaSet `json:"mediaSet"`
	Truth            []fillerstructure.Segment      `json:"truth"`
	Slices           []Slice                        `json:"slices"`
	MeasuredEvidence []MeasuredSliceEvidence        `json:"measuredEvidence,omitempty"`
}

type Suite struct {
	SchemaVersion        int    `json:"schemaVersion"`
	ContractVersion      string `json:"contractVersion"`
	BoundaryToleranceMS  int64  `json:"boundaryToleranceMs"`
	HighByteMinimumBytes int64  `json:"highByteMinimumBytes"`
	Cases                []Case `json:"cases"`
	SHA256               string `json:"sha256"`
}

type CaseResult struct {
	CaseID   string                               `json:"caseId"`
	Stitches []fillerstructurewindow.StitchResult `json:"stitches"`
}

type SliceResult struct {
	Slice        Slice    `json:"slice"`
	Cases        int      `json:"cases"`
	DecidedCases int      `json:"decidedCases"`
	HeldCases    int      `json:"heldCases"`
	WrongCases   int      `json:"wrongCases"`
	FailureCodes []string `json:"failureCodes,omitempty"`
	Passed       bool     `json:"passed"`
}

type Report struct {
	SchemaVersion                   int                               `json:"schemaVersion"`
	ContractVersion                 string                            `json:"contractVersion"`
	CertifiedAt                     time.Time                         `json:"certifiedAt"`
	SuiteSHA256                     string                            `json:"suiteSha256"`
	ReducerVersion                  string                            `json:"reducerVersion"`
	BoundaryToleranceMS             int64                             `json:"boundaryToleranceMs"`
	HighByteMinimumBytes            int64                             `json:"highByteMinimumBytes"`
	AssessorProfiles                []fillerstructure.AssessorProfile `json:"assessorProfiles"`
	Cases                           int                               `json:"cases"`
	DecidedCases                    int                               `json:"decidedCases"`
	HeldCases                       int                               `json:"heldCases"`
	WrongCases                      int                               `json:"wrongCases"`
	Slices                          []SliceResult                     `json:"slices"`
	FailureCodes                    []string                          `json:"failureCodes,omitempty"`
	Status                          string                            `json:"status"`
	TrainingAllowed                 bool                              `json:"trainingAllowed"`
	AutomaticMaterializationAllowed bool                              `json:"automaticMaterializationAllowed"`
	NextAction                      string                            `json:"nextAction"`
	SHA256                          string                            `json:"sha256"`
}

func SuiteSHA256(suite Suite) string {
	suite.SHA256 = ""
	raw, err := json.Marshal(suite)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func ReportSHA256(report Report) string {
	report.SHA256 = ""
	raw, err := json.Marshal(report)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
