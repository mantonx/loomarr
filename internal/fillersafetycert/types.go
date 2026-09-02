// Package fillersafetycert owns deterministic, non-authorizing certification
// of the durable spoken-safety cascade. It never reads media or policy text.
package fillersafetycert

import (
	"time"

	"github.com/loomarr/loomarr/internal/fillersafety"
)

const (
	SchemaVersion   = 1
	ContractVersion = "filler-spoken-cascade-certification-v1"

	ChallengeDevelopment   = "development"
	ChallengeCertification = "certification"

	LabelPositive = "positive"
	LabelClean    = "clean"

	ReviewerHuman       = "human"
	ReviewerModel       = "model"
	ReviewerPrimary     = "primary"
	ReviewerAdjudicator = "adjudicator"

	StatusPassed           = "passed"
	StatusDiagnosticPassed = "diagnostic_passed"
	StatusFailed           = "failed"

	OutcomeDetected      = "detected"
	OutcomeMissed        = "missed"
	OutcomeClean         = "clean"
	OutcomeFalsePositive = "false_positive"
	OutcomeCoverageHold  = "coverage_hold"

	SliceQuietSpeech           = "quiet_speech"
	SliceMusicOverlap          = "music_overlap"
	SliceAccentLocale          = "accent_locale"
	SliceSpeedPitch            = "speed_pitch"
	SliceCodecTransform        = "codec_transform"
	SliceClipping              = "clipping"
	SliceDerivativeCompilation = "derivative_compilation"
	SlicePhoneticConfusable    = "phonetic_confusable"
	SlicePartialToken          = "partial_token"
	SliceWordless              = "wordless"
	SliceMusicOnly             = "music_only"
	SliceNearMatch             = "homophone_near_match"
	SliceTargetLocale          = "target_locale"

	MinimumPositiveFamilies = 59
	MaximumCleanFPRate      = 0.01
	NextAction              = "retain_non_authorizing_evidence_and_run_remaining_safety_lanes"
)

type Authority struct {
	SchemaVersion        int             `json:"schemaVersion"`
	ContractVersion      string          `json:"contractVersion"`
	AuthoredAt           time.Time       `json:"authoredAt"`
	ChallengeKind        string          `json:"challengeKind"`
	CorpusManifestSHA256 string          `json:"corpusManifestSha256"`
	PolicySHA256         string          `json:"policySha256"`
	ProposerSHA256       string          `json:"proposerSha256"`
	ProposerFamily       string          `json:"proposerFamily"`
	Implementation       string          `json:"implementation"`
	AudioRoute           RouteAuthority  `json:"audioRoute"`
	VideoRoute           RouteAuthority  `json:"videoRoute"`
	Cases                []AuthorityCase `json:"cases"`
}

type RouteAuthority struct {
	Role              string   `json:"role"`
	Rung              string   `json:"rung"`
	Modalities        []string `json:"modalities"`
	RequestedProvider string   `json:"requestedProvider"`
	RequestedModel    string   `json:"requestedModel"`
	ResolvedProvider  string   `json:"resolvedProvider"`
	ResolvedModel     string   `json:"resolvedModel"`
	UpstreamProvider  string   `json:"upstreamProvider"`
	ModelFamily       string   `json:"modelFamily"`
	CapabilitySHA256  string   `json:"capabilitySha256"`
	PromptSHA256      string   `json:"promptSha256"`
	SchemaSHA256      string   `json:"schemaSha256"`
}

type AuthorityCase struct {
	Alias                 string                `json:"alias"`
	SourceSHA256          string                `json:"sourceSha256"`
	SourceAuthoritySHA256 string                `json:"sourceAuthoritySha256"`
	SourceFamilyID        string                `json:"sourceFamilyId"`
	SourceBytes           int64                 `json:"sourceBytes"`
	DurationMS            int64                 `json:"durationMs"`
	TruthProvenanceSHA256 string                `json:"truthProvenanceSha256"`
	RightsSHA256          string                `json:"rightsSha256"`
	Label                 string                `json:"label"`
	Locale                string                `json:"locale"`
	Slices                []string              `json:"slices"`
	PositiveIntervals     []PositiveInterval    `json:"positiveIntervals,omitempty"`
	Reviewers             []ReviewerAttestation `json:"reviewers"`
}

type PositiveInterval struct {
	RuleID  string `json:"ruleId"`
	StartMS int64  `json:"startMs"`
	EndMS   int64  `json:"endMs"`
}

type ReviewerAttestation struct {
	ReviewerID        string `json:"reviewerId"`
	Role              string `json:"role"`
	Method            string `json:"method"`
	ModelFamily       string `json:"modelFamily,omitempty"`
	Decision          string `json:"decision"`
	AttestationSHA256 string `json:"attestationSha256"`
}

type ResultManifest struct {
	SchemaVersion   int         `json:"schemaVersion"`
	ContractVersion string      `json:"contractVersion"`
	ManifestedAt    time.Time   `json:"manifestedAt"`
	AuthoritySHA256 string      `json:"authoritySha256"`
	Runs            []ResultRun `json:"runs"`
}

type ResultRun struct {
	Alias           string                     `json:"alias"`
	Run             fillersafety.LedgerRun     `json:"run"`
	Events          []fillersafety.LedgerEvent `json:"events"`
	TerminalEventID string                     `json:"terminalEventId"`
	TerminalSHA256  string                     `json:"terminalSha256"`
}

type Config struct {
	AuthorityPath string
	ResultsPath   string
	ScoredAt      time.Time
	OutputPath    string
}

type Report struct {
	SchemaVersion              int                `json:"schemaVersion"`
	ContractVersion            string             `json:"contractVersion"`
	ScoredAt                   time.Time          `json:"scoredAt"`
	AuthoritySHA256            string             `json:"authoritySha256"`
	ResultManifestSHA256       string             `json:"resultManifestSha256"`
	PolicySHA256               string             `json:"policySha256"`
	ProposerSHA256             string             `json:"proposerSha256"`
	Implementation             string             `json:"implementation"`
	ChallengeKind              string             `json:"challengeKind"`
	PositiveSources            int                `json:"positiveSources"`
	PositiveFamilies           int                `json:"positiveFamilies"`
	DetectedPositiveSources    int                `json:"detectedPositiveSources"`
	MissedPositiveSources      int                `json:"missedPositiveSources"`
	PositiveIntervals          int                `json:"positiveIntervals"`
	DetectedPositiveIntervals  int                `json:"detectedPositiveIntervals"`
	SourceRecall               float64            `json:"sourceRecall"`
	SourceRecallExactLower95   float64            `json:"sourceRecallExactLower95"`
	CleanSources               int                `json:"cleanSources"`
	CleanFalsePositiveSources  int                `json:"cleanFalsePositiveSources"`
	CoverageHolds              int                `json:"coverageHolds"`
	CleanSlices                []CleanSliceMetric `json:"cleanSlices"`
	Cases                      []CaseReport       `json:"cases"`
	CertificationStatus        string             `json:"certificationStatus"`
	TrainingAllowed            bool               `json:"trainingAllowed"`
	IngestionAllowed           bool               `json:"ingestionAllowed"`
	SchedulingAllowed          bool               `json:"schedulingAllowed"`
	ProductionAdmissionAllowed bool               `json:"productionAdmissionAllowed"`
	NextAction                 string             `json:"nextAction"`
}

type CleanSliceMetric struct {
	Slice             string  `json:"slice"`
	CleanSources      int     `json:"cleanSources"`
	FalsePositives    int     `json:"falsePositives"`
	FalsePositiveRate float64 `json:"falsePositiveRate"`
	Passed            bool    `json:"passed"`
}

type CaseReport struct {
	Alias                     string `json:"alias"`
	Label                     string `json:"label"`
	Outcome                   string `json:"outcome"`
	PositiveIntervals         int    `json:"positiveIntervals,omitempty"`
	DetectedPositiveIntervals int    `json:"detectedPositiveIntervals,omitempty"`
}
