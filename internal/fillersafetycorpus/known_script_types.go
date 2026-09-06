package fillersafetycorpus

import (
	"time"

	"github.com/loomarr/loomarr/internal/fillercorpus"
	"github.com/loomarr/loomarr/internal/fillersafety"
)

const (
	KnownScriptAuthoritySchemaVersion   = 1
	KnownScriptAuthorityContractVersion = "filler-spoken-known-script-authority-v1"
	KnownScriptConsentSchemaVersion     = 1
	KnownScriptConsentContractVersion   = "filler-spoken-participant-consent-v1"
	KnownScriptMappingSchemaVersion     = 1
	KnownScriptMappingContractVersion   = "filler-spoken-known-script-policy-mapping-v1"
	KnownScriptProcessorSchemaVersion   = 1
	KnownScriptProcessorContractVersion = "filler-spoken-hosted-processor-schedule-v1"
	KnownScriptRightsSchemaVersion      = 1
	KnownScriptRightsContractVersion    = "filler-spoken-known-script-rights-v1"
	KnownScriptOwnerMapContractVersion  = "filler-spoken-known-script-owner-map-v1"
	KnownScriptDatasetID                = "consented-known-script"
	KnownScriptPackagingRecipe          = "known-script-neutral-video-640x360-30fps-h264-aac-v1"

	KnownScriptRedistributionPrivate = "private_only"
	KnownScriptRedistributionAllowed = "master_and_permitted_derivatives"
	KnownScriptRetentionWithdrawal   = "until_withdrawal_or_rights_retirement"

	KnownScriptAssetMusic = "music"
	KnownScriptAssetNoise = "noise"

	KnownScriptProcessorOpenRouter = "openrouter"
)

type KnownScriptAuthority struct {
	SchemaVersion   int                 `json:"schemaVersion"`
	ContractVersion string              `json:"contractVersion"`
	Dataset         string              `json:"dataset"`
	AuthoredAt      time.Time           `json:"authoredAt"`
	PolicySHA256    string              `json:"policySha256"`
	Implementation  string              `json:"implementation"`
	Members         []KnownScriptMember `json:"members"`
}

type KnownScriptMember struct {
	ParticipantID     string                     `json:"participantId"`
	SessionID         string                     `json:"sessionId"`
	TakeID            string                     `json:"takeId"`
	Locale            string                     `json:"locale"`
	Accent            string                     `json:"accent"`
	ScriptID          string                     `json:"scriptId"`
	Script            FileAuthority              `json:"script"`
	PolicyMapping     FileAuthority              `json:"policyMapping"`
	MasterAudio       FileAuthority              `json:"masterAudio"`
	SelectedAudio     FileAuthority              `json:"selectedAudio"`
	Slices            []string                   `json:"slices"`
	PositiveIntervals []PreparedPositiveInterval `json:"positiveIntervals"`
	Transformation    KnownScriptTransformation  `json:"transformation"`
	Consent           KnownScriptConsent         `json:"consent"`
}

type KnownScriptConsent struct {
	SchemaVersion           int                      `json:"schemaVersion"`
	ContractVersion         string                   `json:"contractVersion"`
	ParticipantID           string                   `json:"participantId"`
	Document                FileAuthority            `json:"document"`
	SignerAuthorityEvidence FileAuthority            `json:"signerAuthorityEvidence"`
	ProcessorSchedule       FileAuthority            `json:"processorSchedule"`
	WithdrawalInstructions  FileAuthority            `json:"withdrawalInstructions"`
	SignedAt                time.Time                `json:"signedAt"`
	RightsReviewedAt        time.Time                `json:"rightsReviewedAt"`
	RightsReviewerID        string                   `json:"rightsReviewerId"`
	ExpiresAt               *time.Time               `json:"expiresAt,omitempty"`
	WithdrawnAt             *time.Time               `json:"withdrawnAt,omitempty"`
	RedistributionScope     string                   `json:"redistributionScope"`
	RetentionPolicy         string                   `json:"retentionPolicy"`
	WithdrawalSupported     bool                     `json:"withdrawalSupported"`
	NoEndorsement           bool                     `json:"noEndorsement"`
	Grants                  KnownScriptConsentGrants `json:"grants"`
}

type KnownScriptConsentGrants struct {
	Collection            bool `json:"collection"`
	PrivateStorage        bool `json:"privateStorage"`
	TechnicalModification bool `json:"technicalModification"`
	EvidenceExtraction    bool `json:"evidenceExtraction"`
	IndependentReview     bool `json:"independentReview"`
	HostedModelEvaluation bool `json:"hostedModelEvaluation"`
}

type KnownScriptProcessorSchedule struct {
	SchemaVersion   int                          `json:"schemaVersion"`
	ContractVersion string                       `json:"contractVersion"`
	Processors      []KnownScriptHostedProcessor `json:"processors"`
}

type KnownScriptHostedProcessor struct {
	Kind                 string `json:"kind"`
	SourceBaseURL        string `json:"sourceBaseUrl"`
	RequestedModel       string `json:"requestedModel"`
	ResolvedModel        string `json:"resolvedModel"`
	UpstreamProvider     string `json:"upstreamProvider"`
	UpstreamProviderSlug string `json:"upstreamProviderSlug"`
	ZDR                  bool   `json:"zdr"`
}

type knownScriptRights struct {
	SchemaVersion     int                          `json:"schemaVersion"`
	ContractVersion   string                       `json:"contractVersion"`
	PreparedAt        time.Time                    `json:"preparedAt"`
	AuthoritySHA256   string                       `json:"authoritySha256"`
	ParticipantID     string                       `json:"participantId"`
	Consent           KnownScriptConsent           `json:"consent"`
	ProcessorSchedule KnownScriptProcessorSchedule `json:"processorSchedule"`
	Assets            []KnownScriptAsset           `json:"assets"`
}

type KnownScriptTransformation struct {
	RecipeID     string                    `json:"recipeId"`
	RecipeSHA256 string                    `json:"recipeSha256"`
	RenderedAt   time.Time                 `json:"renderedAt"`
	Tool         fillersafety.ToolIdentity `json:"tool"`
	MasterSHA256 string                    `json:"masterSha256"`
	OutputSHA256 string                    `json:"outputSha256"`
	Assets       []KnownScriptAsset        `json:"assets"`
}

type KnownScriptAsset struct {
	Role           string                             `json:"role"`
	Media          FileAuthority                      `json:"media"`
	RightsEvidence FileAuthority                      `json:"rightsEvidence"`
	RightsContract fillercorpus.HoldoutRightsContract `json:"rightsContract"`
}

type KnownScriptPolicyMapping struct {
	SchemaVersion   int                        `json:"schemaVersion"`
	ContractVersion string                     `json:"contractVersion"`
	ScriptID        string                     `json:"scriptId"`
	ScriptSHA256    string                     `json:"scriptSha256"`
	PolicySHA256    string                     `json:"policySha256"`
	Intervals       []PreparedPositiveInterval `json:"intervals"`
}

type PrepareKnownScriptConfig struct {
	AuthorityPath      string
	SourceRoot         string
	SeedPath           string
	FFmpegPath         string
	FFprobePath        string
	PreparedAt         time.Time
	ExpectedSpeakers   int
	MaximumInputBytes  int64
	MaximumOutputBytes int64
	MaximumWallTime    time.Duration
	OutputDirectory    string
}

type PrepareKnownScriptResult struct {
	Speakers       int
	CohortSHA256   string
	OwnerMapSHA256 string
	InputBytes     int64
	OutputBytes    int64
}

type KnownScriptOwnerMap struct {
	SchemaVersion   int                        `json:"schemaVersion"`
	ContractVersion string                     `json:"contractVersion"`
	PreparedAt      time.Time                  `json:"preparedAt"`
	CohortSHA256    string                     `json:"cohortSha256"`
	Entries         []KnownScriptOwnerMapEntry `json:"entries"`
}

type KnownScriptOwnerMapEntry struct {
	CaseID            string `json:"caseId"`
	SourceFamily      string `json:"sourceFamily"`
	ParticipantID     string `json:"participantId"`
	SessionID         string `json:"sessionId"`
	TakeID            string `json:"takeId"`
	ScriptID          string `json:"scriptId"`
	MasterAudioPath   string `json:"masterAudioPath"`
	SelectedAudioPath string `json:"selectedAudioPath"`
}
