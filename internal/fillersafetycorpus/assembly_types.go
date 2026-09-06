package fillersafetycorpus

import (
	"time"

	"github.com/loomarr/loomarr/internal/fillersafetycert"
)

const (
	AssemblyPlanSchemaVersion          = 1
	AssemblyPlanContractVersion        = "filler-spoken-corpus-assembly-plan-v1"
	ReviewWorklistSchemaVersion        = 1
	ReviewWorklistContractVersion      = "filler-spoken-primary-review-worklist-v1"
	maximumAssemblyPlanBytes           = int64(8 << 20)
	maximumAssemblyPolicyBytes         = int64(8 << 20)
	maximumPreparedCohortDocumentBytes = int64(64 << 20)
)

type ReviewDraftConfig struct {
	PlanPath        string
	InputRoot       string
	OutputDirectory string
}

type AssemblyPlan struct {
	SchemaVersion      int                             `json:"schemaVersion"`
	ContractVersion    string                          `json:"contractVersion"`
	AssembledAt        time.Time                       `json:"assembledAt"`
	ChallengeKind      string                          `json:"challengeKind"`
	Policy             FileAuthority                   `json:"policy"`
	ProposerSHA256     string                          `json:"proposerSha256"`
	ProposerFamily     string                          `json:"proposerFamily"`
	Implementation     string                          `json:"implementation"`
	AudioRoute         fillersafetycert.RouteAuthority `json:"audioRoute"`
	VideoRoute         fillersafetycert.RouteAuthority `json:"videoRoute"`
	Cohorts            []AssemblyCohort                `json:"cohorts"`
	ExpectedCases      int                             `json:"expectedCases"`
	MaximumInputBytes  int64                           `json:"maximumInputBytes"`
	MaximumOutputBytes int64                           `json:"maximumOutputBytes"`
	MaximumWallTimeMS  int64                           `json:"maximumWallTimeMs"`
}

type AssemblyCohort struct {
	CohortPath    string `json:"cohortPath"`
	SourceRoot    string `json:"sourceRoot"`
	SHA256        string `json:"sha256"`
	Kind          string `json:"kind"`
	Dataset       string `json:"dataset"`
	ExpectedCases int    `json:"expectedCases"`
	MaximumBytes  int64  `json:"maximumBytes"`
}

type ReviewDraftResult struct {
	Cases            int
	PositiveFamilies int
	CleanFamilies    int
	DraftSHA256      string
	WorklistSHA256   string
	InputBytes       int64
	OutputBytes      int64
}

type ReviewWorklist struct {
	SchemaVersion   int                  `json:"schemaVersion"`
	ContractVersion string               `json:"contractVersion"`
	AssembledAt     time.Time            `json:"assembledAt"`
	DraftSHA256     string               `json:"draftSha256"`
	PolicyPath      string               `json:"policyPath"`
	PolicySHA256    string               `json:"policySha256"`
	Cases           []ReviewWorklistCase `json:"cases"`
}

type ReviewWorklistCase struct {
	CaseID                string                     `json:"caseId"`
	SourcePath            string                     `json:"sourcePath"`
	SourceSHA256          string                     `json:"sourceSha256"`
	SourceAuthoritySHA256 string                     `json:"sourceAuthoritySha256"`
	SourceBytes           int64                      `json:"sourceBytes"`
	DurationMS            int64                      `json:"durationMs"`
	TranscriptPath        string                     `json:"transcriptPath,omitempty"`
	TranscriptSHA256      string                     `json:"transcriptSha256,omitempty"`
	TranscriptBytes       int64                      `json:"transcriptBytes,omitempty"`
	TruthProvenancePath   string                     `json:"truthProvenancePath"`
	TruthProvenanceSHA256 string                     `json:"truthProvenanceSha256"`
	TruthProvenanceBytes  int64                      `json:"truthProvenanceBytes"`
	RightsPath            string                     `json:"rightsPath"`
	RightsSHA256          string                     `json:"rightsSha256"`
	RightsBytes           int64                      `json:"rightsBytes"`
	Claim                 string                     `json:"claim"`
	Locale                string                     `json:"locale"`
	Slices                []string                   `json:"slices"`
	PositiveIntervals     []PreparedPositiveInterval `json:"positiveIntervals,omitempty"`
}
