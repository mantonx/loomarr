// Package fillersafetyreview runs one independent, exhaustive model review of
// an assembled spoken-safety certification draft. It never locks authority,
// scores certification, or admits media.
package fillersafetyreview

import (
	"context"
	"net/http"
	"time"

	"github.com/loomarr/loomarr/internal/fillerbakeoff"
	"github.com/loomarr/loomarr/internal/fillersafety"
	"github.com/loomarr/loomarr/internal/fillersafetycert"
	"github.com/loomarr/loomarr/internal/fillersafetycorpus"
	"github.com/loomarr/loomarr/internal/openroutermedia"
)

const (
	PlanSchemaVersion   = 1
	PlanContractVersion = "filler-spoken-model-review-plan-v1"

	checkpointSchemaVersion = 1
	checkpointFilename      = "checkpoint.json"
	activeLockFilename      = ".active.lock"

	attemptReserved  = "reserved"
	attemptAccepted  = "accepted"
	attemptFailed    = "failed"
	attemptUnsettled = "unsettled"

	failureProvider        = "provider"
	failureInvalidResponse = "invalid_response"
	failureUnclear         = "unclear"

	reviewPromptVersion  = "filler-spoken-independent-model-review-v1"
	reviewMaxTokens      = 1024
	maximumPlanBytes     = int64(8 << 20)
	maximumDocumentBytes = int64(64 << 20)
)

type Config struct {
	PlanPath            string
	InputRoot           string
	APIKey              string
	FFmpegPath          string
	CheckpointDirectory string
	OutputPath          string
}

type Plan struct {
	SchemaVersion        int                              `json:"schemaVersion"`
	ContractVersion      string                           `json:"contractVersion"`
	Draft                fillersafetycorpus.FileAuthority `json:"draft"`
	Worklist             fillersafetycorpus.FileAuthority `json:"worklist"`
	Snapshot             fillersafetycorpus.FileAuthority `json:"snapshot"`
	ReviewerID           string                           `json:"reviewerId"`
	ModelFamily          string                           `json:"modelFamily"`
	Model                string                           `json:"model"`
	ResolvedModel        string                           `json:"resolvedModel"`
	UpstreamProvider     string                           `json:"upstreamProvider"`
	UpstreamProviderSlug string                           `json:"upstreamProviderSlug"`
	DisableReasoning     bool                             `json:"disableReasoning"`
	ExpectedCases        int                              `json:"expectedCases"`
	MaximumRequests      int                              `json:"maximumRequests"`
	MaximumChargeNanoUSD int64                            `json:"maximumChargeNanoUsd"`
	MaximumSpendNanoUSD  int64                            `json:"maximumSpendNanoUsd"`
	MaximumInputBytes    int64                            `json:"maximumInputBytes"`
	MaximumAudioBytes    int64                            `json:"maximumAudioBytes"`
	PerCaseTimeoutMS     int64                            `json:"perCaseTimeoutMs"`
	MaximumWallTimeMS    int64                            `json:"maximumWallTimeMs"`
}

type Result struct {
	Cases            int
	Requests         int
	Rejected         int
	PromptTokens     int64
	CompletionTokens int64
	ChargedNanoUSD   int64
	ReviewSHA256     string
}

type modelObservation struct {
	Verdict                  string   `json:"verdict"`
	Audibility               string   `json:"audibility"`
	MatchedRuleIDs           []string `json:"matchedRuleIds"`
	ConfirmedIntervalIndexes []int    `json:"confirmedIntervalIndexes"`
}

type attempt struct {
	CaseID            string    `json:"caseId"`
	Attempt           int       `json:"attempt"`
	RequestedAt       time.Time `json:"requestedAt"`
	ReviewedAt        time.Time `json:"reviewedAt,omitempty"`
	RequestSHA256     string    `json:"requestSha256"`
	ResponseSHA256    string    `json:"responseSha256,omitempty"`
	GenerationID      string    `json:"generationId,omitempty"`
	State             string    `json:"state"`
	Failure           string    `json:"failure,omitempty"`
	ObservationSHA256 string    `json:"observationSha256,omitempty"`
	PromptTokens      int64     `json:"promptTokens,omitempty"`
	CompletionTokens  int64     `json:"completionTokens,omitempty"`
	ChargedAmountUSD  string    `json:"chargedAmountUsd,omitempty"`
	ChargedNanoUSD    int64     `json:"chargedNanoUsd,omitempty"`
	ReservedNanoUSD   int64     `json:"reservedNanoUsd"`
}

type acceptedCase struct {
	Assessment  fillersafetycert.ReviewAssessment `json:"assessment"`
	Observation modelObservation                  `json:"observation"`
	Attempt     int                               `json:"attempt"`
}

type checkpointIdentity struct {
	SchemaVersion        int                       `json:"schemaVersion"`
	PlanSHA256           string                    `json:"planSha256"`
	DraftSHA256          string                    `json:"draftSha256"`
	WorklistSHA256       string                    `json:"worklistSha256"`
	PolicySHA256         string                    `json:"policySha256"`
	SnapshotSHA256       string                    `json:"snapshotSha256"`
	ReviewerID           string                    `json:"reviewerId"`
	ModelFamily          string                    `json:"modelFamily"`
	Model                string                    `json:"model"`
	ResolvedModel        string                    `json:"resolvedModel"`
	UpstreamProvider     string                    `json:"upstreamProvider"`
	UpstreamProviderSlug string                    `json:"upstreamProviderSlug"`
	DisableReasoning     bool                      `json:"disableReasoning"`
	PromptSHA256         string                    `json:"promptSha256"`
	SchemaSHA256         string                    `json:"schemaSha256"`
	FFmpeg               fillersafety.ToolIdentity `json:"ffmpeg"`
	ExpectedCases        int                       `json:"expectedCases"`
	MaximumRequests      int                       `json:"maximumRequests"`
	MaximumChargeNanoUSD int64                     `json:"maximumChargeNanoUsd"`
	MaximumSpendNanoUSD  int64                     `json:"maximumSpendNanoUsd"`
}

type checkpoint struct {
	Identity    checkpointIdentity `json:"identity"`
	StartedAt   time.Time          `json:"startedAt"`
	CompletedAt time.Time          `json:"completedAt,omitempty"`
	Attempts    []attempt          `json:"attempts"`
	Accepted    []acceptedCase     `json:"accepted"`
}

type loadedInputs struct {
	plan              Plan
	planSHA256        string
	draft             fillersafetycert.AuthorityDraft
	draftSHA256       string
	worklist          fillersafetycorpus.ReviewWorklist
	worklistSHA256    string
	policy            fillersafety.Policy
	policySHA256      string
	policyBytes       int64
	snapshotSHA256    string
	snapshot          fillerbakeoff.OpenRouterSnapshot
	root              string
	inputBytes        int64
	knownScriptRights map[string]fillersafetycorpus.FileAuthority
}

type reviewRuntime struct {
	baseURL  string
	client   *http.Client
	now      func() time.Time
	call     func(context.Context, *http.Client, string, openroutermedia.Config) (openroutermedia.Result, error)
	extract  audioExtractor
	identify func(context.Context, string) (fillersafety.ToolIdentity, string, error)
}
