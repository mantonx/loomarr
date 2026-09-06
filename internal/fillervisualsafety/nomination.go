package fillervisualsafety

import (
	"context"
	"time"

	"github.com/loomarr/loomarr/internal/fillercorpus"
)

const (
	VisualCorpusNominationWorksheetSchemaVersion   = 1
	VisualCorpusNominationWorksheetContractVersion = "filler-visual-corpus-nomination-worksheet-v1"
	VisualCorpusNominationSetSchemaVersion         = 2
	VisualCorpusNominationSetContractVersion       = "filler-visual-corpus-nomination-set-v2"
	VisualCorpusMetCC0ApprovalBasisPrefix          = fillercorpus.MetRightsApprovalBasisPrefix
	VisualCorpusNominationExclude                  = "exclude"
	VisualCorpusCleanNominationRoleHint            = "policy-clean-nomination"
)

// VisualCorpusNominationWorksheet freezes every mechanically derived field
// before a maintainer authors the four visual judgments in its CSV companion.
type VisualCorpusNominationWorksheet struct {
	SchemaVersion         int                         `json:"schemaVersion"`
	ContractVersion       string                      `json:"contractVersion"`
	Profile               string                      `json:"profile"`
	InventorySHA256       string                      `json:"inventorySha256"`
	MaterializationSHA256 string                      `json:"materializationSha256"`
	PreparedAt            time.Time                   `json:"preparedAt"`
	Instructions          []string                    `json:"instructions"`
	Cases                 []VisualCorpusNominationRow `json:"cases"`
	CandidateModelOutput  bool                        `json:"candidateModelOutput"`
	TruthAuthorityCreated bool                        `json:"truthAuthorityCreated"`
	TrainingAllowed       bool                        `json:"trainingAllowed"`
	ProductionUseAllowed  bool                        `json:"productionUseAllowed"`
	SHA256                string                      `json:"sha256"`
}

type VisualCorpusNominationRow struct {
	Rank                 int                      `json:"rank"`
	CaseID               string                   `json:"caseId"`
	CaptureIDs           []string                 `json:"captureIds"`
	Authority            string                   `json:"authority"`
	ItemID               string                   `json:"itemId"`
	RoleHints            []string                 `json:"roleHints"`
	Creator              []string                 `json:"creator"`
	SubjectTerms         []string                 `json:"subjectTerms"`
	SourceFamily         string                   `json:"sourceFamily"`
	InstitutionID        string                   `json:"institutionId"`
	SourceWorkID         string                   `json:"sourceWorkId"`
	SourceFamilyID       string                   `json:"sourceFamilyId"`
	IndependenceGroupID  string                   `json:"independenceGroupId"`
	CreatorID            string                   `json:"creatorId"`
	ObjectURL            string                   `json:"objectUrl"`
	RightsURL            string                   `json:"rightsUrl"`
	RightsBasis          string                   `json:"rightsBasis"`
	MetadataSHA256       string                   `json:"metadataSha256"`
	LocalFile            string                   `json:"localFile"`
	Asset                VisualCorpusFileIdentity `json:"asset"`
	MediaType            string                   `json:"mediaType"`
	Width                int                      `json:"width"`
	Height               int                      `json:"height"`
	PerceptualHash       string                   `json:"perceptualHash"`
	RightsApprovalSHA256 string                   `json:"rightsApprovalSha256"`
	RightsReviewerID     string                   `json:"rightsReviewerId"`
	RightsReviewedAt     time.Time                `json:"rightsReviewedAt"`
	RightsReviewBasis    string                   `json:"rightsReviewBasis"`
	RightsRequiredCredit string                   `json:"rightsRequiredCredit,omitempty"`
	RightsRestrictions   []string                 `json:"rightsRestrictions"`
}

type VisualCorpusNominationPrepareConfig struct {
	InventoryJSON       []byte
	MaterializationJSON []byte
	MediaRoot           string
	PreparedAt          time.Time
}

type VisualCorpusNominationLockConfig struct {
	Prepare      VisualCorpusNominationPrepareConfig
	Worksheet    VisualCorpusNominationWorksheet
	CompletedCSV [][]string
	ReviewedBy   string
	ReviewedAt   time.Time
	OutputDir    string
}

// VisualCorpusNominationSet is a private, canonical source-root handoff. It
// contains candidate nominations, not truth, certification, or product authority.
type VisualCorpusNominationSet struct {
	SchemaVersion         int                          `json:"schemaVersion"`
	ContractVersion       string                       `json:"contractVersion"`
	WorksheetSHA256       string                       `json:"worksheetSha256"`
	ReviewDecisionsSHA256 string                       `json:"reviewDecisionsSha256"`
	InventorySHA256       string                       `json:"inventorySha256"`
	MaterializationSHA256 string                       `json:"materializationSha256"`
	LockedAt              time.Time                    `json:"lockedAt"`
	ReviewedBy            string                       `json:"reviewedBy"`
	ReviewedCaseCount     int                          `json:"reviewedCaseCount"`
	ExcludedCaseCount     int                          `json:"excludedCaseCount"`
	Candidates            []VisualCorpusDraftCandidate `json:"candidates"`
	CandidateModelOutput  bool                         `json:"candidateModelOutput"`
	TruthAuthorityCreated bool                         `json:"truthAuthorityCreated"`
	TrainingAllowed       bool                         `json:"trainingAllowed"`
	ProductionUseAllowed  bool                         `json:"productionUseAllowed"`
	SHA256                string                       `json:"sha256"`
}

type VisualCorpusNominationResult struct {
	SetSHA256      string
	ReviewedCount  int
	CandidateCount int
	ExcludedCount  int
}

func PrepareVisualCorpusNominationWorksheet(ctx context.Context, config VisualCorpusNominationPrepareConfig) (VisualCorpusNominationWorksheet, error) {
	return prepareVisualCorpusNominationWorksheet(ctx, config)
}

func LockVisualCorpusNominations(ctx context.Context, config VisualCorpusNominationLockConfig) (VisualCorpusNominationResult, error) {
	return lockVisualCorpusNominations(ctx, config)
}

func OpenVisualCorpusNominationSet(root string) (VisualCorpusNominationSet, error) {
	return openVisualCorpusNominationSet(root, false)
}

func VisualCorpusNominationWorksheetSHA256(worksheet VisualCorpusNominationWorksheet) string {
	worksheet.SHA256 = ""
	return digestJSON(worksheet)
}

func VisualCorpusNominationSetSHA256(set VisualCorpusNominationSet) string {
	set.SHA256 = ""
	return digestJSON(set)
}
