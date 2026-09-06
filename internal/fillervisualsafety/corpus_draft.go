package fillervisualsafety

import (
	"context"
	"time"
)

const (
	VisualCorpusDraftAuthoritySchemaVersion   = 1
	VisualCorpusDraftAuthorityContractVersion = "filler-visual-corpus-draft-authority-v1"
	VisualCorpusDraftManifestSchemaVersion    = 1
	VisualCorpusDraftManifestContractVersion  = "filler-visual-corpus-draft-manifest-v1"
	VisualCorpusDraftOwnerSchemaVersion       = 1
	VisualCorpusDraftOwnerContractVersion     = "filler-visual-corpus-draft-owner-v1"

	VisualCorpusNominationPositive = "positive_candidate"
	VisualCorpusNominationClean    = "clean_candidate"

	VisualCorpusRightsCC0              = "cc0_1_0"
	VisualCorpusRightsPublicDomainMark = "public_domain_mark_1_0"

	VisualCorpusSubjectHistoricalAdult = "historical_art_adult_only"
	VisualCorpusSubjectNoRiskFound     = "no_sensitive_subject_identified"
	VisualCorpusGeneratedNo            = "not_generated"

	VisualCorpusTransportDecisionUnresolved = "lossless_carrier_family_decision_unresolved"

	MinimumVisualPositiveCandidateTarget = 120
	MinimumVisualCleanCandidateTarget    = 300
	MaximumVisualCorpusDraftCases        = 1_000
	MaximumVisualCorpusAssetBytes        = int64(128 << 20)
	MaximumVisualCorpusRightsBytes       = int64(256 << 10)
	MaximumVisualCorpusDraftBytes        = int64(32 << 30)
	MaximumVisualCorpusPixels            = int64(50_000_000)
)

// VisualCorpusDraftAuthority is authored before candidate-model execution. It
// binds already acquired source works and their independent rights reviews,
// but deliberately contains neither model output nor truth.
type VisualCorpusDraftAuthority struct {
	SchemaVersion           int                          `json:"schemaVersion"`
	ContractVersion         string                       `json:"contractVersion"`
	AuthoredAt              time.Time                    `json:"authoredAt"`
	PolicySHA256            string                       `json:"policySha256"`
	AliasSeedSHA256         string                       `json:"aliasSeedSha256"`
	PositiveCandidateTarget int                          `json:"positiveCandidateTarget"`
	CleanCandidateTarget    int                          `json:"cleanCandidateTarget"`
	TransportDecision       string                       `json:"transportDecision"`
	CandidateModelOutput    bool                         `json:"candidateModelOutput"`
	Candidates              []VisualCorpusDraftCandidate `json:"candidates"`
	SHA256                  string                       `json:"sha256"`
}

type VisualCorpusDraftCandidate struct {
	CandidateID         string                   `json:"candidateId"`
	Nomination          string                   `json:"nomination"`
	InstitutionID       string                   `json:"institutionId"`
	SourceWorkID        string                   `json:"sourceWorkId"`
	SourceFamilyID      string                   `json:"sourceFamilyId"`
	IndependenceGroupID string                   `json:"independenceGroupId"`
	CreatorID           string                   `json:"creatorId"`
	ObjectURL           string                   `json:"objectUrl"`
	RightsURL           string                   `json:"rightsUrl"`
	RightsBasis         string                   `json:"rightsBasis"`
	SubjectStatus       string                   `json:"subjectStatus"`
	GeneratedStatus     string                   `json:"generatedStatus"`
	AssetRelativePath   string                   `json:"assetRelativePath"`
	Asset               VisualCorpusFileIdentity `json:"asset"`
	RightsRelativePath  string                   `json:"rightsRelativePath"`
	RightsEvidence      VisualCorpusFileIdentity `json:"rightsEvidence"`
	Slices              []string                 `json:"slices"`
}

// VisualCorpusFileIdentity binds one already acquired private input without making its
// machine-local path part of the published reviewer package.
type VisualCorpusFileIdentity struct {
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

// VisualCorpusRightsEvidence is a maintainer-authored acquisition review. It
// permits only private corpus construction and model evaluation; production,
// training, and broadcast authority remain false.
type VisualCorpusRightsEvidence struct {
	SchemaVersion              int       `json:"schemaVersion"`
	Kind                       string    `json:"kind"`
	InventorySHA256            string    `json:"inventorySha256"`
	MaterializationSHA256      string    `json:"materializationSha256"`
	RightsApprovalSHA256       string    `json:"rightsApprovalSha256"`
	CaseID                     string    `json:"caseId"`
	ContentSHA256              string    `json:"contentSha256"`
	ReviewedAt                 time.Time `json:"reviewedAt"`
	ReviewedBy                 string    `json:"reviewedBy"`
	InstitutionID              string    `json:"institutionId"`
	SourceWorkID               string    `json:"sourceWorkId"`
	ObjectURL                  string    `json:"objectUrl"`
	RightsURL                  string    `json:"rightsUrl"`
	RightsBasis                string    `json:"rightsBasis"`
	SubjectStatus              string    `json:"subjectStatus"`
	GeneratedStatus            string    `json:"generatedStatus"`
	PrivateRetentionAllowed    bool      `json:"privateRetentionAllowed"`
	PrivateModelEvaluation     bool      `json:"privateModelEvaluation"`
	TrainingAllowed            bool      `json:"trainingAllowed"`
	ProductionBroadcastAllowed bool      `json:"productionBroadcastAllowed"`
}

type VisualCorpusDraftConfig struct {
	Authority     VisualCorpusDraftAuthority
	SourceRoot    string
	PolicyPath    string
	AliasSeedPath string
	OutputDir     string
	PreparedAt    time.Time
}

// VisualCorpusDraftManifest is the complete reviewer-visible package. It has
// no source identity, nomination, family, creator, model result, or truth.
type VisualCorpusDraftManifest struct {
	SchemaVersion              int                           `json:"schemaVersion"`
	ContractVersion            string                        `json:"contractVersion"`
	PreparedAt                 time.Time                     `json:"preparedAt"`
	AuthoritySHA256            string                        `json:"authoritySha256"`
	Policy                     CandidateBlindReviewAsset     `json:"policy"`
	ReviewBoard                CandidateBlindReviewAsset     `json:"reviewBoard"`
	Cases                      []VisualCorpusDraftReviewCase `json:"cases"`
	CandidateModelOutput       bool                          `json:"candidateModelOutput"`
	TruthAuthorityCreated      bool                          `json:"truthAuthorityCreated"`
	TrainingAllowed            bool                          `json:"trainingAllowed"`
	ProductionAdmissionAllowed bool                          `json:"productionAdmissionAllowed"`
	SHA256                     string                        `json:"sha256"`
}

type VisualCorpusDraftReviewCase struct {
	Alias          string                    `json:"alias"`
	Asset          CandidateBlindReviewAsset `json:"asset"`
	RightsEvidence CandidateBlindReviewAsset `json:"rightsEvidence"`
	MediaType      string                    `json:"mediaType"`
	Width          int                       `json:"width"`
	Height         int                       `json:"height"`
	PerceptualHash string                    `json:"perceptualHash"`
}

type VisualCorpusDraftOwnerMap struct {
	SchemaVersion   int                          `json:"schemaVersion"`
	ContractVersion string                       `json:"contractVersion"`
	PreparedAt      time.Time                    `json:"preparedAt"`
	Authority       VisualCorpusDraftAuthority   `json:"authority"`
	ReviewSHA256    string                       `json:"reviewSha256"`
	Cases           []VisualCorpusDraftOwnerCase `json:"cases"`
	SHA256          string                       `json:"sha256"`
}

type VisualCorpusDraftOwnerCase struct {
	Alias     string                     `json:"alias"`
	Candidate VisualCorpusDraftCandidate `json:"candidate"`
}

type VisualCorpusDraftResult struct {
	ManifestSHA256 string
	OwnerMapSHA256 string
	CaseCount      int
}

func SealVisualCorpusDraftAuthority(authority VisualCorpusDraftAuthority) (VisualCorpusDraftAuthority, error) {
	return sealVisualCorpusDraftAuthority(authority)
}

func ValidateVisualCorpusDraftAuthority(authority VisualCorpusDraftAuthority) error {
	return validateVisualCorpusDraftAuthority(authority)
}

func VisualCorpusDraftAuthoritySHA256(authority VisualCorpusDraftAuthority) string {
	authority.SHA256 = ""
	return digestJSON(authority)
}

// PrepareVisualCorpusDraft validates and atomically publishes the entire
// candidate-blind review package through one deep module interface.
func PrepareVisualCorpusDraft(ctx context.Context, config VisualCorpusDraftConfig) (VisualCorpusDraftResult, error) {
	return prepareVisualCorpusDraft(ctx, config)
}

func OpenVisualCorpusDraft(root string) (VisualCorpusDraftManifest, VisualCorpusDraftOwnerMap, error) {
	return openVisualCorpusDraft(root, false)
}

func VisualCorpusDraftManifestSHA256(manifest VisualCorpusDraftManifest) string {
	manifest.SHA256 = ""
	return digestJSON(manifest)
}

func VisualCorpusDraftOwnerSHA256(owner VisualCorpusDraftOwnerMap) string {
	owner.SHA256 = ""
	return digestJSON(owner)
}
