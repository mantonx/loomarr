package fillervisualsafety

import (
	"context"
	"time"
)

const (
	CandidateBlindReviewSchemaVersion   = 1
	CandidateBlindReviewContractVersion = "filler-visual-candidate-blind-review-v1"
	CandidateBlindOwnerSchemaVersion    = 1
	CandidateBlindOwnerContractVersion  = "filler-visual-candidate-blind-owner-v1"

	ReviewSelectionIndependentCorpus  = "independent_corpus"
	ReviewSelectionTargetedDiagnostic = "targeted_diagnostic"
	ReviewScopeCompleteSource         = "complete_source_and_planned_frames"

	MaximumReviewPackageBytes    = int64(4 << 30)
	maximumReviewFrameAssetBytes = int64(128 << 20)
	maximumReviewManifestBytes   = int64(8 << 20)
	maximumReviewPolicyBytes     = int64(256 << 10)
)

// CandidateBlindReviewConfig joins the private source locator to the
// path-free authorities needed to build one independently reviewable bundle.
// SelectionOrigin stays only in the owner map so it cannot bias a reviewer.
type CandidateBlindReviewConfig struct {
	Alias           string
	SourceFamilyID  string
	RightsSHA256    string
	SelectionOrigin string
	Source          SourceRequest
	Profile         CoverageProfile
	PolicyPath      string
	FFmpegPath      string
	OutputDir       string
	PreparedAt      time.Time
}

// CandidateBlindReviewManifest is the only document a reviewer receives. It
// contains the complete exact source and every planned full-resolution frame,
// but no source metadata, candidate identity, score, threshold, or verdict.
type CandidateBlindReviewManifest struct {
	SchemaVersion              int                         `json:"schemaVersion"`
	ContractVersion            string                      `json:"contractVersion"`
	PreparedAt                 time.Time                   `json:"preparedAt"`
	Alias                      string                      `json:"alias"`
	Policy                     CandidateBlindReviewAsset   `json:"policy"`
	CoverageProfileSHA256      string                      `json:"coverageProfileSha256"`
	ReviewScope                string                      `json:"reviewScope"`
	MinimumCoveredExposureMS   int64                       `json:"minimumCoveredExposureMs"`
	Plan                       CoveragePlan                `json:"plan"`
	Coverage                   CoverageEvidence            `json:"coverage"`
	Source                     CandidateBlindReviewAsset   `json:"source"`
	Frames                     []CandidateBlindReviewFrame `json:"frames"`
	CandidateEvidenceIncluded  bool                        `json:"candidateEvidenceIncluded"`
	CandidateScoresIncluded    bool                        `json:"candidateScoresIncluded"`
	TruthAuthorityCreated      bool                        `json:"truthAuthorityCreated"`
	TrainingAllowed            bool                        `json:"trainingAllowed"`
	ProductionAdmissionAllowed bool                        `json:"productionAdmissionAllowed"`
	SHA256                     string                      `json:"sha256"`
}

type CandidateBlindReviewAsset struct {
	RelativePath string `json:"relativePath"`
	SHA256       string `json:"sha256"`
	Bytes        int64  `json:"bytes"`
}

// CandidateBlindReviewPolicy is the complete closed rule set shown to a
// reviewer. Development bundles cannot grant production or training use.
type CandidateBlindReviewPolicy struct {
	SchemaVersion              int                               `json:"schemaVersion"`
	Kind                       string                            `json:"kind"`
	DevelopmentOnly            bool                              `json:"developmentOnly"`
	ProductionAdmissionAllowed bool                              `json:"productionAdmissionAllowed"`
	PolicyMatches              []CandidateBlindReviewPolicyMatch `json:"policyMatches"`
}

type CandidateBlindReviewPolicyMatch struct {
	ID         string `json:"id"`
	Definition string `json:"definition"`
}

type CandidateBlindReviewFrame struct {
	Ordinal      int    `json:"ordinal"`
	RequestedMS  int64  `json:"requestedMs"`
	ObservedMS   int64  `json:"observedMs"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	RGB24SHA256  string `json:"rgb24Sha256"`
	RelativePath string `json:"relativePath"`
	PNGSHA256    string `json:"pngSha256"`
	PNGBytes     int64  `json:"pngBytes"`
}

// CandidateBlindReviewOwnerMap stays outside the reviewer package. It restores
// source, family, rights, and selection provenance only after review.
type CandidateBlindReviewOwnerMap struct {
	SchemaVersion   int             `json:"schemaVersion"`
	ContractVersion string          `json:"contractVersion"`
	PreparedAt      time.Time       `json:"preparedAt"`
	Alias           string          `json:"alias"`
	SourceAuthority SourceAuthority `json:"sourceAuthority"`
	SourceFamilyID  string          `json:"sourceFamilyId"`
	RightsSHA256    string          `json:"rightsSha256"`
	SelectionOrigin string          `json:"selectionOrigin"`
	ReviewSHA256    string          `json:"reviewSha256"`
	SHA256          string          `json:"sha256"`
}

type CandidateBlindReviewResult struct {
	PackageSHA256  string
	OwnerMapSHA256 string
	FrameCount     int
}

// BuildCandidateBlindReviewBundle atomically publishes one owner-only map and
// one separately shareable reviewer directory.
func BuildCandidateBlindReviewBundle(ctx context.Context, config CandidateBlindReviewConfig) (CandidateBlindReviewResult, error) {
	return buildCandidateBlindReviewBundle(ctx, config)
}

func CandidateBlindReviewSHA256(manifest CandidateBlindReviewManifest) string {
	manifest.SHA256 = ""
	return digestJSON(manifest)
}

func CandidateBlindReviewOwnerSHA256(owner CandidateBlindReviewOwnerMap) string {
	owner.SHA256 = ""
	return digestJSON(owner)
}
