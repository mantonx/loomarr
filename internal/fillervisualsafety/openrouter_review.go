package fillervisualsafety

import (
	"context"
	"net/http"
	"time"

	"github.com/loomarr/loomarr/internal/fillerbakeoff"
)

const (
	CandidateBlindOpenRouterSchemaVersion   = 1
	CandidateBlindOpenRouterContractVersion = "filler-visual-candidate-blind-openrouter-review-v1"
	CandidateBlindOpenRouterPromptVersion   = "filler-visual-candidate-blind-policy-review-v1"

	CandidateBlindCoverageCompleted    = "completed"
	CandidateBlindCoverageInsufficient = "insufficient"

	CandidateBlindCertaintyObserved  = "observed"
	CandidateBlindCertaintyUncertain = "uncertain"

	CandidateBlindOutcomeProhibitedSignal = "prohibited_signal_nominated"
	CandidateBlindOutcomeCoverageHold     = "coverage_insufficient"
	CandidateBlindOutcomeNoSignal         = "no_prohibited_signal_observed"

	CandidateBlindAttemptReserved  = "reserved"
	CandidateBlindAttemptAccepted  = "accepted"
	CandidateBlindAttemptFailed    = "failed"
	CandidateBlindAttemptUnsettled = "unsettled"
)

// CandidateBlindOpenRouterConfig describes exactly one already-authorized,
// fallback-disabled model review. One invocation can make at most one request.
type CandidateBlindOpenRouterConfig struct {
	BundlePath              string
	ExpectedPackageSHA256   string
	ExpectedOwnerMapSHA256  string
	ExpectedSelectionOrigin string
	OutputDir               string
	FFmpegPath              string
	BaseURL                 string
	APIKey                  string
	Snapshot                fillerbakeoff.OpenRouterSnapshot
	Model                   string
	ModelFamily             string
	UpstreamProvider        string
	UpstreamProviderSlug    string
	ReviewerID              string
	PerRequestTimeout       time.Duration
	MaxChargeNanoUSD        int64
	ReasoningEnabled        bool
	AllowInsecureTestURL    bool
	Client                  *http.Client
	Now                     func() time.Time
}

type CandidateBlindHostedInput struct {
	SchemaVersion          int                          `json:"schemaVersion"`
	ReviewPackageSHA256    string                       `json:"reviewPackageSha256"`
	CoverageEvidenceSHA256 string                       `json:"coverageEvidenceSha256"`
	PolicySHA256           string                       `json:"policySha256"`
	FFmpeg                 ToolIdentity                 `json:"ffmpeg"`
	CarrierRecipeSHA256    string                       `json:"carrierRecipeSha256"`
	Carrier                CandidateBlindReviewAsset    `json:"carrier"`
	ContactSheets          []CandidateBlindContactSheet `json:"contactSheets"`
	SHA256                 string                       `json:"sha256"`
}

type CandidateBlindContactSheet struct {
	CandidateBlindReviewAsset
	FirstOrdinal    int   `json:"firstOrdinal"`
	LastOrdinal     int   `json:"lastOrdinal"`
	FirstObservedMS int64 `json:"firstObservedMs"`
	LastObservedMS  int64 `json:"lastObservedMs"`
	Columns         int   `json:"columns"`
	Rows            int   `json:"rows"`
}

type CandidateBlindOpenRouterMatch struct {
	PolicyMatchID string `json:"policyMatchId"`
	StartMS       int64  `json:"startMs"`
	EndMS         int64  `json:"endMs"`
	Certainty     string `json:"certainty"`
}

type CandidateBlindOpenRouterAssessment struct {
	CoverageAssessment string                          `json:"coverageAssessment"`
	Matches            []CandidateBlindOpenRouterMatch `json:"matches"`
	Outcome            string                          `json:"outcome"`
}

type CandidateBlindOpenRouterAttempt struct {
	RequestedAt        time.Time `json:"requestedAt"`
	RequestSHA256      string    `json:"requestSha256"`
	ResponseSHA256     string    `json:"responseSha256,omitempty"`
	RawResponsePath    string    `json:"rawResponsePath,omitempty"`
	GenerationID       string    `json:"generationId,omitempty"`
	State              string    `json:"state"`
	LatencyMS          int64     `json:"latencyMs,omitempty"`
	PromptTokens       int64     `json:"promptTokens,omitempty"`
	CompletionTokens   int64     `json:"completionTokens,omitempty"`
	ReasoningBytes     int       `json:"reasoningBytes,omitempty"`
	ChargedAmountUSD   string    `json:"chargedAmountUsd,omitempty"`
	ChargedNanoUSD     int64     `json:"chargedNanoUsd,omitempty"`
	ReservedNanoUSD    int64     `json:"reservedNanoUsd"`
	OperationalFailure string    `json:"operationalFailure,omitempty"`
}

type CandidateBlindOpenRouterResult struct {
	SchemaVersion              int                                `json:"schemaVersion"`
	ContractVersion            string                             `json:"contractVersion"`
	ReviewPackageSHA256        string                             `json:"reviewPackageSha256"`
	OwnerMapSHA256             string                             `json:"ownerMapSha256"`
	SelectionOrigin            string                             `json:"selectionOrigin"`
	CapabilitySnapshotSHA256   string                             `json:"capabilitySnapshotSha256"`
	Model                      string                             `json:"model"`
	ModelFamily                string                             `json:"modelFamily"`
	ResolvedModel              string                             `json:"resolvedModel"`
	UpstreamProvider           string                             `json:"upstreamProvider"`
	UpstreamProviderSlug       string                             `json:"upstreamProviderSlug"`
	ReviewerID                 string                             `json:"reviewerId"`
	PromptVersion              string                             `json:"promptVersion"`
	PromptSHA256               string                             `json:"promptSha256"`
	SchemaSHA256               string                             `json:"schemaSha256"`
	ReasoningEnabled           bool                               `json:"reasoningEnabled"`
	MaxRequests                int                                `json:"maxRequests"`
	MaxChargeNanoUSD           int64                              `json:"maxChargeNanoUsd"`
	Input                      CandidateBlindHostedInput          `json:"input"`
	Attempt                    CandidateBlindOpenRouterAttempt    `json:"attempt"`
	Assessment                 CandidateBlindOpenRouterAssessment `json:"assessment"`
	ReviewedAt                 time.Time                          `json:"reviewedAt"`
	TruthAuthorityCreated      bool                               `json:"truthAuthorityCreated"`
	TrainingAllowed            bool                               `json:"trainingAllowed"`
	ProductionAdmissionAllowed bool                               `json:"productionAdmissionAllowed"`
	SHA256                     string                             `json:"sha256"`
}

// RunCandidateBlindOpenRouterReview prepares complete-video and exhaustive
// contact-sheet inputs, durably reserves one request, and returns only a
// non-authorizing review nomination.
func RunCandidateBlindOpenRouterReview(ctx context.Context, config CandidateBlindOpenRouterConfig) (CandidateBlindOpenRouterResult, error) {
	return runCandidateBlindOpenRouterReview(ctx, config)
}

func CandidateBlindHostedInputSHA256(input CandidateBlindHostedInput) string {
	input.SHA256 = ""
	return digestJSON(input)
}

func CandidateBlindOpenRouterResultSHA256(result CandidateBlindOpenRouterResult) string {
	result.SHA256 = ""
	return digestJSON(result)
}
