package fillersafetycert

import (
	"time"

	"github.com/loomarr/loomarr/internal/fillersafety"
)

const (
	AuthorityDraftSchemaVersion    = 1
	AuthorityDraftContractVersion  = "filler-spoken-cascade-authority-draft-v1"
	AuthorityReviewSchemaVersion   = 2
	AuthorityReviewContractVersion = "filler-spoken-cascade-authority-review-v2"

	ReviewDecisionVerified = "verified"
	ReviewDecisionRejected = "rejected"

	ModelReviewEvidenceSchemaVersion   = 1
	ModelReviewEvidenceContractVersion = "filler-spoken-model-review-evidence-v1"
	ModelReviewAttemptAccepted         = "accepted"
	ModelReviewAttemptFailed           = "failed"
)

// AuthorityDraft is the private, path-bearing source and truth declaration
// reviewed before any certification run. Its exact bytes are the corpus
// manifest identity; none of its private identifiers or paths reach Authority.
type AuthorityDraft struct {
	SchemaVersion   int                  `json:"schemaVersion"`
	ContractVersion string               `json:"contractVersion"`
	ChallengeKind   string               `json:"challengeKind"`
	PolicySHA256    string               `json:"policySha256"`
	ProposerSHA256  string               `json:"proposerSha256"`
	ProposerFamily  string               `json:"proposerFamily"`
	Implementation  string               `json:"implementation"`
	AudioRoute      RouteAuthority       `json:"audioRoute"`
	VideoRoute      RouteAuthority       `json:"videoRoute"`
	Cases           []AuthorityDraftCase `json:"cases"`
}

type AuthorityDraftCase struct {
	CaseID                string                       `json:"caseId"`
	SourcePath            string                       `json:"sourcePath"`
	SourceAuthority       fillersafety.SourceAuthority `json:"sourceAuthority"`
	SourceFamily          string                       `json:"sourceFamily"`
	TruthProvenancePath   string                       `json:"truthProvenancePath"`
	TruthProvenanceSHA256 string                       `json:"truthProvenanceSha256"`
	RightsPath            string                       `json:"rightsPath"`
	RightsSHA256          string                       `json:"rightsSha256"`
	Label                 string                       `json:"label"`
	Locale                string                       `json:"locale"`
	Slices                []string                     `json:"slices"`
	PositiveIntervals     []PositiveInterval           `json:"positiveIntervals,omitempty"`
}

// AuthorityReview is one independently produced, evaluation-output-blind
// submission over the exact draft. Primary submissions cover every case; an
// adjudicator covers exactly the cases on which the primaries disagree.
type AuthorityReview struct {
	SchemaVersion   int                  `json:"schemaVersion"`
	ContractVersion string               `json:"contractVersion"`
	DraftSHA256     string               `json:"draftSha256"`
	ReviewerID      string               `json:"reviewerId"`
	Role            string               `json:"role"`
	Method          string               `json:"method"`
	ModelFamily     string               `json:"modelFamily,omitempty"`
	EvidenceSHA256  string               `json:"evidenceSha256"`
	ModelEvidence   *ModelReviewEvidence `json:"modelEvidence,omitempty"`
	SubmittedAt     time.Time            `json:"submittedAt"`
	Assessments     []ReviewAssessment   `json:"assessments"`
}

// ModelReviewEvidence is the path-free execution authority embedded in a
// model-backed review. It contains identities and accounting, never content.
type ModelReviewEvidence struct {
	SchemaVersion        int                          `json:"schemaVersion"`
	ContractVersion      string                       `json:"contractVersion"`
	PlanSHA256           string                       `json:"planSha256"`
	WorklistSHA256       string                       `json:"worklistSha256"`
	PolicySHA256         string                       `json:"policySha256"`
	SnapshotSHA256       string                       `json:"snapshotSha256"`
	RequestedModel       string                       `json:"requestedModel"`
	ResolvedModel        string                       `json:"resolvedModel"`
	UpstreamProvider     string                       `json:"upstreamProvider"`
	UpstreamProviderSlug string                       `json:"upstreamProviderSlug"`
	DisableReasoning     bool                         `json:"disableReasoning"`
	ModelFamily          string                       `json:"modelFamily"`
	PromptSHA256         string                       `json:"promptSha256"`
	SchemaSHA256         string                       `json:"schemaSha256"`
	FFmpeg               fillersafety.ToolIdentity    `json:"ffmpeg"`
	StartedAt            time.Time                    `json:"startedAt"`
	CompletedAt          time.Time                    `json:"completedAt"`
	MaximumRequests      int                          `json:"maximumRequests"`
	MaximumChargeNanoUSD int64                        `json:"maximumChargeNanoUsd"`
	MaximumSpendNanoUSD  int64                        `json:"maximumSpendNanoUsd"`
	Requests             int                          `json:"requests"`
	PromptTokens         int64                        `json:"promptTokens"`
	CompletionTokens     int64                        `json:"completionTokens"`
	ChargedNanoUSD       int64                        `json:"chargedNanoUsd"`
	Attempts             []ModelReviewAttemptEvidence `json:"attempts"`
}

type ModelReviewAttemptEvidence struct {
	CaseID            string    `json:"caseId"`
	Attempt           int       `json:"attempt"`
	RequestedAt       time.Time `json:"requestedAt"`
	ReviewedAt        time.Time `json:"reviewedAt,omitempty"`
	RequestSHA256     string    `json:"requestSha256"`
	ResponseSHA256    string    `json:"responseSha256"`
	GenerationID      string    `json:"generationId"`
	State             string    `json:"state"`
	ObservationSHA256 string    `json:"observationSha256,omitempty"`
	PromptTokens      int64     `json:"promptTokens"`
	CompletionTokens  int64     `json:"completionTokens"`
	ChargedNanoUSD    int64     `json:"chargedNanoUsd"`
}

type ReviewAssessment struct {
	CaseID            string             `json:"caseId"`
	Decision          string             `json:"decision"`
	PositiveIntervals []PositiveInterval `json:"positiveIntervals,omitempty"`
}

// AuthorityEvidenceValidator lets the corpus owner interpret private,
// corpus-specific evidence while the certification locker stays format-neutral.
type AuthorityEvidenceValidator func(
	rightsRaw, truthProvenanceRaw []byte,
	item AuthorityDraftCase,
	authoredAt time.Time,
) error

type AuthorityBuildConfig struct {
	DraftPath          string
	FirstReviewPath    string
	SecondReviewPath   string
	AdjudicatorPath    string
	SeedPath           string
	SourceRoot         string
	AuthoredAt         time.Time
	ExpectedCases      int
	MaximumSourceBytes int64
	ValidateEvidence   AuthorityEvidenceValidator
	OutputPath         string
}

type AuthorityBuildResult struct {
	Cases            int
	PositiveFamilies int
	CleanFamilies    int
	AuthoritySHA256  string
}
