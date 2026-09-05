package fillerquarantine

import (
	"context"
	"time"

	"github.com/loomarr/loomarr/internal/fillerreference"
	"github.com/loomarr/loomarr/internal/fillerreview"
	"github.com/loomarr/loomarr/internal/mediatools"
)

const (
	SchemaVersion   = 1
	ContractVersion = "filler-corpus-quarantine-inspection-v1"

	DispositionEligibleForRightsReview = "eligible_for_rights_review"
	DispositionHold                    = "hold"

	ComparisonCandidate = "candidate"
	ComparisonPrior     = "prior_holdout"

	qualityFailureCoverage = 0.95
)

// Media is the quarantine inspection seam. The inspector owns all authority,
// identity, ordering, comparison, and disposition policy; an adapter owns only
// deterministic media-tool execution.
type Media interface {
	Identity() fillerreview.TemporalTruthMediaIdentity
	Probe(context.Context, string) (mediatools.Probed, error)
	Quality(context.Context, string, int64, bool) (mediatools.MediaQuality, error)
	Fingerprint(context.Context, string, int64, bool) ([]uint64, []uint32, error)
}

type Config struct {
	InventoryPath           string
	DownloadLedgerPath      string
	DownloadRoot            string
	PriorPublicManifestPath string
	PriorAuthorityPath      string
	PriorSourceRoot         string
	ExpectedPriorCases      int
	MaxMediaWallTime        time.Duration
	GeneratedAt             time.Time
	Media                   Media
}

type Report struct {
	SchemaVersion   int                                     `json:"schemaVersion"`
	ContractVersion string                                  `json:"contractVersion"`
	GeneratedAt     time.Time                               `json:"generatedAt"`
	Inputs          InputIdentity                           `json:"inputs"`
	MediaTools      fillerreview.TemporalTruthMediaIdentity `json:"mediaTools"`
	Ceilings        Ceilings                                `json:"ceilings"`
	Algorithm       string                                  `json:"duplicateAlgorithm"`
	Summary         Summary                                 `json:"summary"`
	PriorSources    []PriorSource                           `json:"priorSources"`
	Cases           []Case                                  `json:"cases"`
	Comparisons     []Comparison                            `json:"comparisons"`
	Authority       AuthorityDisposition                    `json:"authority"`
}

type Ceilings struct {
	MaxMediaWallTimeMS int64 `json:"maxMediaWallTimeMs"`
}

type InputIdentity struct {
	InventorySHA256           string `json:"inventorySha256"`
	DownloadLedgerSHA256      string `json:"downloadLedgerSha256"`
	PriorPublicManifestSHA256 string `json:"priorPublicManifestSha256"`
	PriorAuthoritySHA256      string `json:"priorAuthoritySha256"`
}

type Summary struct {
	Cases                     int `json:"cases"`
	EligibleForRightsReview   int `json:"eligibleForRightsReview"`
	Held                      int `json:"held"`
	PriorSources              int `json:"priorSources"`
	UnavailablePriorSources   int `json:"unavailablePriorSources"`
	ExactExposureCollisions   int `json:"exactExposureCollisions"`
	RelatedCandidatePairs     int `json:"relatedCandidatePairs"`
	RelatedPriorExposurePairs int `json:"relatedPriorExposurePairs"`
}

type PriorSource struct {
	SourceID     string              `json:"sourceId"`
	SourcePath   string              `json:"sourcePath"`
	SourceSHA256 string              `json:"sourceSha256"`
	DurationMS   int64               `json:"durationMs"`
	Available    bool                `json:"available"`
	Fingerprint  FingerprintEvidence `json:"fingerprint,omitempty"`
}

type Case struct {
	CaseID        string              `json:"caseId"`
	LocalFile     string              `json:"localFile"`
	ContentSHA256 string              `json:"contentSha256"`
	Bytes         int64               `json:"bytes"`
	ExpectedMedia MediaExpectation    `json:"expectedMedia"`
	Media         MediaEvidence       `json:"media"`
	Fingerprint   FingerprintEvidence `json:"fingerprint"`
	ExactExposure []ExactExposure     `json:"exactExposure,omitempty"`
	Disposition   string              `json:"disposition"`
	HoldReasons   []string            `json:"holdReasons,omitempty"`
}

type MediaExpectation struct {
	Bytes      int64 `json:"bytes"`
	DurationMS int64 `json:"durationMs,omitempty"`
	Width      int   `json:"width,omitempty"`
	Height     int   `json:"height,omitempty"`
}

type MediaEvidence struct {
	DurationMS int64                   `json:"durationMs"`
	Width      int                     `json:"width"`
	Height     int                     `json:"height"`
	HasVideo   bool                    `json:"hasVideo"`
	HasAudio   bool                    `json:"hasAudio"`
	Quality    mediatools.MediaQuality `json:"quality"`
}

type FingerprintEvidence struct {
	FrameCount       int    `json:"frameCount"`
	FrameSHA256      string `json:"frameSha256"`
	AudioBinCount    int    `json:"audioBinCount"`
	AudioRMSSHA256   string `json:"audioRmsSha256"`
	VisualComparable bool   `json:"visualComparable"`
	AudioComparable  bool   `json:"audioComparable"`
}

type ExactExposure struct {
	Scope    string `json:"scope"`
	Identity string `json:"identity"`
	SHA256   string `json:"sha256"`
}

type Comparison struct {
	Scope   string                              `json:"scope"`
	CaseA   string                              `json:"caseA"`
	CaseB   string                              `json:"caseB"`
	Related bool                                `json:"related"`
	Basis   []string                            `json:"basis,omitempty"`
	Visual  fillerreference.DuplicateComparison `json:"visual"`
	Audio   fillerreference.AudioComparison     `json:"audio"`
}

type AuthorityDisposition struct {
	CopyAndStorage           bool `json:"copyAndStorage"`
	LocalTechnicalInspection bool `json:"localTechnicalInspection"`
	ProviderTransfer         bool `json:"providerTransfer"`
	Redistribution           bool `json:"redistribution"`
	CorpusPreparation        bool `json:"corpusPreparation"`
	Training                 bool `json:"training"`
	CatalogIngestion         bool `json:"catalogIngestion"`
	Scheduling               bool `json:"scheduling"`
	ProductionAdmission      bool `json:"productionAdmission"`
}

type fingerprint struct {
	frames []uint64
	audio  []uint32
}
