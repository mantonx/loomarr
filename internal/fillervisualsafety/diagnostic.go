package fillervisualsafety

import "time"

const (
	PortableDiagnosticAuthoritySchemaVersion   = 1
	PortableDiagnosticAuthorityContractVersion = "filler-visual-portable-diagnostic-authority-v1"
	PortableDiagnosticReportSchemaVersion      = 1
	PortableDiagnosticReportContractVersion    = "filler-visual-portable-diagnostic-report-v1"

	MaximumDiagnosticCases      = 1_000
	MaximumDiagnosticThresholds = 32
)

type DiagnosticTruthLabel string

const (
	DiagnosticTruthPositive   DiagnosticTruthLabel = "positive"
	DiagnosticTruthClean      DiagnosticTruthLabel = "clean"
	DiagnosticTruthUnresolved DiagnosticTruthLabel = "unresolved"
)

type DiagnosticRunState string

const (
	DiagnosticRunComplete     DiagnosticRunState = "complete"
	DiagnosticRunCoverageHold DiagnosticRunState = "coverage_hold"
	DiagnosticRunWorkerFailed DiagnosticRunState = "worker_failed"
)

const (
	DiagnosticScoreSoftmax           = "softmax"
	DiagnosticScoreCumulativeSoftmax = "cumulative_softmax"
)

const (
	DiagnosticSliceShortExposure        = "short_exposure"
	DiagnosticSliceCuts                 = "cuts"
	DiagnosticSliceCropLetterbox        = "crop_letterbox"
	DiagnosticSliceTranscode            = "transcode"
	DiagnosticSliceVFRCFR               = "vfr_cfr"
	DiagnosticSliceAnimation            = "animation"
	DiagnosticSliceMonochrome           = "monochrome"
	DiagnosticSliceLowLight             = "low_light"
	DiagnosticSliceMultiplePeople       = "multiple_people"
	DiagnosticSliceCompilationPlacement = "compilation_placement"
	DiagnosticSliceDamagedTail          = "damaged_tail"
	DiagnosticSliceProgramme            = "programme"
	DiagnosticSliceAdvertising          = "advertising"
	DiagnosticSliceHistoricalGraphics   = "historical_graphics"
	DiagnosticSliceSkinTone             = "skin_tone"
	DiagnosticSliceMedical              = "medical"
	DiagnosticSliceBeach                = "beach"
	DiagnosticSliceUnderwear            = "underwear"
	DiagnosticSliceVisuallyBusy         = "visually_busy"
)

// PortableDiagnosticAuthority locks truth, rights, candidate identity, and
// thresholds before candidate execution. Unresolved cases deliberately carry
// no truth authority and cannot contribute to accuracy metrics.
type PortableDiagnosticAuthority struct {
	SchemaVersion                 int                      `json:"schemaVersion"`
	ContractVersion               string                   `json:"contractVersion"`
	AuthoredAt                    time.Time                `json:"authoredAt"`
	PolicySHA256                  string                   `json:"policySha256"`
	CapabilitySHA256              string                   `json:"capabilitySha256"`
	CoverageProfileSHA256         string                   `json:"coverageProfileSha256"`
	ModelID                       string                   `json:"modelId"`
	PositiveOutputLabel           string                   `json:"positiveOutputLabel"`
	ScoreTransform                string                   `json:"scoreTransform"`
	Thresholds                    []float64                `json:"thresholds"`
	MaximumCleanFalsePositiveRate float64                  `json:"maximumCleanFalsePositiveRate"`
	Implementation                string                   `json:"implementation"`
	Cases                         []PortableDiagnosticCase `json:"cases"`
	SHA256                        string                   `json:"sha256"`
}

type PortableDiagnosticCase struct {
	Alias                string               `json:"alias"`
	SourceAuthority      SourceAuthority      `json:"sourceAuthority"`
	SourceFamilyID       string               `json:"sourceFamilyId"`
	RightsSHA256         string               `json:"rightsSha256"`
	TruthLabel           DiagnosticTruthLabel `json:"truthLabel"`
	TruthAuthoritySHA256 string               `json:"truthAuthoritySha256,omitempty"`
	Slices               []string             `json:"slices"`
	PositiveIntervals    []Interval           `json:"positiveIntervals,omitempty"`
}

// PortableDiagnosticRun records either a complete evidence pair or one
// bounded operational failure. Every case still binds its deterministic plan.
type PortableDiagnosticRun struct {
	Alias       string                     `json:"alias"`
	State       DiagnosticRunState         `json:"state"`
	FailureCode string                     `json:"failureCode,omitempty"`
	Plan        CoveragePlan               `json:"plan"`
	Coverage    *CoverageEvidence          `json:"coverage,omitempty"`
	Inference   *PortableInferenceEvidence `json:"inference,omitempty"`
}

type PortableDiagnosticReport struct {
	SchemaVersion              int                            `json:"schemaVersion"`
	ContractVersion            string                         `json:"contractVersion"`
	ScoredAt                   time.Time                      `json:"scoredAt"`
	AuthoritySHA256            string                         `json:"authoritySha256"`
	CapabilitySHA256           string                         `json:"capabilitySha256"`
	CoverageProfileSHA256      string                         `json:"coverageProfileSha256"`
	ModelID                    string                         `json:"modelId"`
	PositiveOutputLabel        string                         `json:"positiveOutputLabel"`
	ScoreTransform             string                         `json:"scoreTransform"`
	Cases                      []PortableDiagnosticCaseReport `json:"cases"`
	Thresholds                 []PortableDiagnosticThreshold  `json:"thresholds"`
	TargetedReviewAliases      []string                       `json:"targetedReviewAliases"`
	BlindHumanAuditRequired    bool                           `json:"blindHumanAuditRequired"`
	TruthCreatedByCandidate    bool                           `json:"truthCreatedByCandidate"`
	TrainingAllowed            bool                           `json:"trainingAllowed"`
	ProductionAdmissionAllowed bool                           `json:"productionAdmissionAllowed"`
	NextAction                 string                         `json:"nextAction"`
	SHA256                     string                         `json:"sha256"`
}

type PortableDiagnosticCaseReport struct {
	Alias            string                            `json:"alias"`
	TruthLabel       DiagnosticTruthLabel              `json:"truthLabel"`
	RunState         DiagnosticRunState                `json:"runState"`
	FailureCode      string                            `json:"failureCode,omitempty"`
	ScoreAvailable   bool                              `json:"scoreAvailable"`
	FrameCount       int                               `json:"frameCount"`
	MaximumScore     float64                           `json:"maximumScore"`
	MaximumScoreAtMS int64                             `json:"maximumScoreAtMs"`
	Thresholds       []PortableDiagnosticCaseThreshold `json:"thresholds"`
	TargetedReasons  []string                          `json:"targetedReasons"`
}

type PortableDiagnosticCaseThreshold struct {
	Threshold                 float64 `json:"threshold"`
	Signaled                  bool    `json:"signaled"`
	DetectedPositiveIntervals int     `json:"detectedPositiveIntervals,omitempty"`
}

type PortableDiagnosticThreshold struct {
	Threshold                  float64                         `json:"threshold"`
	PositiveFamilies           int                             `json:"positiveFamilies"`
	DetectedPositiveFamilies   int                             `json:"detectedPositiveFamilies"`
	MissedPositiveFamilies     int                             `json:"missedPositiveFamilies"`
	PositiveRecall             float64                         `json:"positiveRecall"`
	PositiveRecallExactLower95 float64                         `json:"positiveRecallExactLower95"`
	CleanFamilies              int                             `json:"cleanFamilies"`
	CleanFalsePositiveFamilies int                             `json:"cleanFalsePositiveFamilies"`
	CleanFalsePositiveRate     float64                         `json:"cleanFalsePositiveRate"`
	UnresolvedFamilies         int                             `json:"unresolvedFamilies"`
	UnresolvedSignaledFamilies int                             `json:"unresolvedSignaledFamilies"`
	OperationalHolds           int                             `json:"operationalHolds"`
	CleanSlices                []PortableDiagnosticSliceMetric `json:"cleanSlices"`
}

type PortableDiagnosticSliceMetric struct {
	Slice             string  `json:"slice"`
	CleanFamilies     int     `json:"cleanFamilies"`
	FalsePositives    int     `json:"falsePositives"`
	FalsePositiveRate float64 `json:"falsePositiveRate"`
	WithinCeiling     bool    `json:"withinCeiling"`
}
