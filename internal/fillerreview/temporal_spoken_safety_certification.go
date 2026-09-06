package fillerreview

import (
	"encoding/json"
	"fmt"
	"time"
)

const (
	TemporalSpokenSafetyCertificationSchemaVersion   = 1
	TemporalSpokenSafetyCertificationContractVersion = "filler-spoken-safety-certification-v1"

	TemporalSpokenSafetyChallengePositive      = "positive"
	TemporalSpokenSafetyChallengeClean         = "clean"
	TemporalSpokenSafetyChallengeDevelopment   = "development"
	TemporalSpokenSafetyChallengeCertification = "certification"

	TemporalSpokenSafetyCertificationPassed = "passed"
	TemporalSpokenSafetyDiagnosticPassed    = "diagnostic_passed"
	TemporalSpokenSafetyCertificationFailed = "failed"

	TemporalSpokenSafetyOutcomeDetected      = "detected"
	TemporalSpokenSafetyOutcomeMissed        = "missed"
	TemporalSpokenSafetyOutcomeClean         = "clean"
	TemporalSpokenSafetyOutcomeFalsePositive = "false_positive"
	TemporalSpokenSafetyOutcomeCoverageHold  = "coverage_hold"

	TemporalSpokenSafetySliceQuietSpeech           = "quiet_speech"
	TemporalSpokenSafetySliceMusicOverlap          = "music_overlap"
	TemporalSpokenSafetySliceAccentLocale          = "accent_locale"
	TemporalSpokenSafetySliceSpeedPitch            = "speed_pitch"
	TemporalSpokenSafetySliceCodecTransform        = "codec_transform"
	TemporalSpokenSafetySliceClipping              = "clipping"
	TemporalSpokenSafetySliceDerivativeCompilation = "derivative_compilation"
	TemporalSpokenSafetySlicePhoneticConfusable    = "phonetic_confusable"
	TemporalSpokenSafetySlicePartialToken          = "partial_token"
	TemporalSpokenSafetySliceWordless              = "wordless"
	TemporalSpokenSafetySliceMusicOnly             = "music_only"
	TemporalSpokenSafetySliceNearMatch             = "homophone_near_match"
	TemporalSpokenSafetySliceTargetLocale          = "target_locale"

	temporalSpokenSafetyMinimumPositiveFamilies = 59
	temporalSpokenSafetyMaximumCleanFPRate      = 0.01
	TemporalSpokenSafetyCertificationNextAction = "retain_non_authorizing_evidence_and_run_remaining_safety_lanes"
)

type TemporalSpokenSafetyChallengeAuthority struct {
	SchemaVersion        int                                          `json:"schemaVersion"`
	ContractVersion      string                                       `json:"contractVersion"`
	AuthoredAt           time.Time                                    `json:"authoredAt"`
	ChallengeKind        string                                       `json:"challengeKind"`
	CorpusManifestSHA256 string                                       `json:"corpusManifestSha256"`
	PolicySHA256         string                                       `json:"policySha256"`
	Cases                []TemporalSpokenSafetyChallengeAuthorityCase `json:"cases"`
}

type TemporalSpokenSafetyChallengeAuthorityCase struct {
	Alias             string                                 `json:"alias"`
	SourceSHA256      string                                 `json:"sourceSha256"`
	SourceFamilyID    string                                 `json:"sourceFamilyId"`
	Label             string                                 `json:"label"`
	Locale            string                                 `json:"locale"`
	Slices            []string                               `json:"slices"`
	PositiveIntervals []TemporalSpokenSafetyPositiveInterval `json:"positiveIntervals,omitempty"`
}

type TemporalSpokenSafetyPositiveInterval struct {
	RuleID  string `json:"ruleId"`
	StartMS int64  `json:"startMs"`
	EndMS   int64  `json:"endMs"`
}

type TemporalSpokenSafetyCertificationConfig struct {
	AuthorityPath          string
	SpokenSafetyReportPath string
	ScoredAt               time.Time
	OutputPath             string
}

type TemporalSpokenSafetyCertificationReport struct {
	SchemaVersion              int                                     `json:"schemaVersion"`
	ContractVersion            string                                  `json:"contractVersion"`
	ScoredAt                   time.Time                               `json:"scoredAt"`
	AuthoritySHA256            string                                  `json:"authoritySha256"`
	SpokenSafetyReportSHA256   string                                  `json:"spokenSafetyReportSha256"`
	PolicySHA256               string                                  `json:"policySha256"`
	ChallengeKind              string                                  `json:"challengeKind"`
	PositiveSources            int                                     `json:"positiveSources"`
	PositiveFamilies           int                                     `json:"positiveFamilies"`
	DetectedPositiveSources    int                                     `json:"detectedPositiveSources"`
	MissedPositiveSources      int                                     `json:"missedPositiveSources"`
	PositiveIntervals          int                                     `json:"positiveIntervals"`
	DetectedPositiveIntervals  int                                     `json:"detectedPositiveIntervals"`
	SourceRecall               float64                                 `json:"sourceRecall"`
	SourceRecallExactLower95   float64                                 `json:"sourceRecallExactLower95"`
	CleanSources               int                                     `json:"cleanSources"`
	CleanFalsePositiveSources  int                                     `json:"cleanFalsePositiveSources"`
	CoverageHolds              int                                     `json:"coverageHolds"`
	CleanSlices                []TemporalSpokenSafetyCleanSliceMetric  `json:"cleanSlices"`
	Cases                      []TemporalSpokenSafetyCertificationCase `json:"cases"`
	CertificationStatus        string                                  `json:"certificationStatus"`
	TrainingAllowed            bool                                    `json:"trainingAllowed"`
	IngestionAllowed           bool                                    `json:"ingestionAllowed"`
	SchedulingAllowed          bool                                    `json:"schedulingAllowed"`
	ProductionAdmissionAllowed bool                                    `json:"productionAdmissionAllowed"`
	NextAction                 string                                  `json:"nextAction"`
}

type TemporalSpokenSafetyCleanSliceMetric struct {
	Slice             string  `json:"slice"`
	CleanSources      int     `json:"cleanSources"`
	FalsePositives    int     `json:"falsePositives"`
	FalsePositiveRate float64 `json:"falsePositiveRate"`
	Passed            bool    `json:"passed"`
}

type TemporalSpokenSafetyCertificationCase struct {
	Alias                     string `json:"alias"`
	Label                     string `json:"label"`
	Outcome                   string `json:"outcome"`
	PositiveIntervals         int    `json:"positiveIntervals,omitempty"`
	DetectedPositiveIntervals int    `json:"detectedPositiveIntervals,omitempty"`
}

// PublishTemporalSpokenSafetyCertification scores one immutable private
// challenge without exposing source identities, transcripts, or policy text.
// Passing remains evidence only and never grants an application permission.
func PublishTemporalSpokenSafetyCertification(config TemporalSpokenSafetyCertificationConfig) (TemporalSpokenSafetyCertificationReport, string, error) {
	loaded, err := loadTemporalSpokenSafetyCertification(config)
	if err != nil {
		return TemporalSpokenSafetyCertificationReport{}, "", err
	}
	report := scoreTemporalSpokenSafetyCertification(loaded, config.ScoredAt.UTC())
	if err := validateTemporalSpokenSafetyCertificationReport(report); err != nil {
		return TemporalSpokenSafetyCertificationReport{}, "", fmt.Errorf("validate spoken-safety certification: %w", err)
	}
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return TemporalSpokenSafetyCertificationReport{}, "", err
	}
	raw = append(raw, '\n')
	if err := writeTemporalTruthNew(config.OutputPath, raw, 0o600); err != nil {
		return TemporalSpokenSafetyCertificationReport{}, "", fmt.Errorf("publish spoken-safety certification: %w", err)
	}
	return report, hashBytes(raw), nil
}
