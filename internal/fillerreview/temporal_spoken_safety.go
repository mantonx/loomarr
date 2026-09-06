package fillerreview

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/fillerbakeoff"
	"github.com/loomarr/loomarr/internal/fillereval"
	"github.com/loomarr/loomarr/internal/fillersafety"
)

const (
	TemporalSpokenSafetySchemaVersion         = 1
	TemporalSpokenSafetyContractVersion       = "filler-temporal-spoken-safety-v1"
	TemporalSpokenSafetyPolicySchemaVersion   = fillersafety.PolicySchemaVersion
	TemporalSpokenSafetyPolicyContractVersion = fillersafety.PolicyContractVersion

	TemporalSpokenSafetyMatchProhibited = fillersafety.PolicyClassProhibited
	TemporalSpokenSafetyMatchAmbiguous  = fillersafety.PolicyClassAmbiguous
	TemporalSpokenSafetyModeExactWords  = fillersafety.PolicyModeExactWords
	TemporalSpokenSafetyModeTokenPrefix = fillersafety.PolicyModeTokenPrefix

	TemporalSpokenSafetyDispositionProhibited = "prohibited_hold"
	TemporalSpokenSafetyDispositionCoverage   = "coverage_hold"
	TemporalSpokenSafetyDispositionNoSignal   = "no_spoken_signal_observed"
	TemporalSpokenSafetySourceCorpus          = "corpus"
	TemporalSpokenSafetySourceConstruction    = "construction_only"

	temporalSpokenSafetyCoverageMissingTranscript  = "missing_complete_transcript"
	temporalSpokenSafetyCoverageMissingSourceMedia = "missing_complete_source_media"
	temporalSpokenSafetyCertificationNotRun        = "not_run"
	temporalSpokenSafetyDurationToleranceMS        = 1_000
)

type TemporalSpokenSafetyPolicy = fillersafety.Policy

type TemporalSpokenSafetyPolicyRule = fillersafety.PolicyRule

type TemporalSpokenSafetyPolicyBuildConfig struct {
	PolicyID                 string
	GeneratedAt              time.Time
	MaximumInterSegmentGapMS int64
	ProhibitedPhrases        []string
	AmbiguousPhrases         []string
	ProhibitedPrefixes       []string
	AmbiguousPrefixes        []string
	Random                   io.Reader
	OutputPath               string
}

// PublishTemporalSpokenSafetyPolicy writes a private policy whose public rule
// identifiers are random and therefore do not disclose phrases through a
// dictionary-attackable naming convention.
func PublishTemporalSpokenSafetyPolicy(config TemporalSpokenSafetyPolicyBuildConfig) (TemporalSpokenSafetyPolicy, string, error) {
	if config.Random == nil {
		config.Random = rand.Reader
	}
	policy := TemporalSpokenSafetyPolicy{
		SchemaVersion: TemporalSpokenSafetyPolicySchemaVersion, ContractVersion: TemporalSpokenSafetyPolicyContractVersion,
		PolicyID: config.PolicyID, GeneratedAt: config.GeneratedAt.UTC(), MaximumInterSegmentGapMS: config.MaximumInterSegmentGapMS,
	}
	for _, group := range []struct {
		class   string
		mode    string
		phrases []string
	}{
		{TemporalSpokenSafetyMatchProhibited, TemporalSpokenSafetyModeExactWords, config.ProhibitedPhrases},
		{TemporalSpokenSafetyMatchAmbiguous, TemporalSpokenSafetyModeExactWords, config.AmbiguousPhrases},
		{TemporalSpokenSafetyMatchProhibited, TemporalSpokenSafetyModeTokenPrefix, config.ProhibitedPrefixes},
		{TemporalSpokenSafetyMatchAmbiguous, TemporalSpokenSafetyModeTokenPrefix, config.AmbiguousPrefixes},
	} {
		for _, phrase := range group.phrases {
			idBytes := make([]byte, 12)
			if _, err := io.ReadFull(config.Random, idBytes); err != nil {
				return TemporalSpokenSafetyPolicy{}, "", fmt.Errorf("create opaque spoken-safety rule id: %w", err)
			}
			policy.Rules = append(policy.Rules, TemporalSpokenSafetyPolicyRule{
				ID: "rule-" + hex.EncodeToString(idBytes), Class: group.class, MatchMode: group.mode,
				Variants: []string{strings.TrimSpace(phrase)},
			})
		}
	}
	if err := validateTemporalSpokenSafetyPolicy(policy); err != nil {
		return TemporalSpokenSafetyPolicy{}, "", err
	}
	raw, err := json.MarshalIndent(policy, "", "  ")
	if err != nil {
		return TemporalSpokenSafetyPolicy{}, "", err
	}
	raw = append(raw, '\n')
	if err := writeTemporalTruthNew(config.OutputPath, raw, 0o600); err != nil {
		return TemporalSpokenSafetyPolicy{}, "", fmt.Errorf("publish spoken-safety policy: %w", err)
	}
	return policy, hashBytes(raw), nil
}

type TemporalSpokenSafetyConfig struct {
	CorpusManifestPath     string
	PacketsPath            string
	CorpusRoot             string
	CorpusSplit            fillereval.Split
	EvidenceVersion        string
	ExpectedCorpusCases    int
	EvidenceManifestPath   string
	EvidencePrivateMapPath string
	TranscriptSetPath      string
	StructureManifestPath  string
	StructureAuthorityPath string
	ExpectedStructureCases int
	PolicyPath             string
	ProjectedAt            time.Time
	OutputPath             string
}

type TemporalSpokenSafetyReport struct {
	SchemaVersion              int                                     `json:"schemaVersion"`
	ContractVersion            string                                  `json:"contractVersion"`
	ProjectedAt                time.Time                               `json:"projectedAt"`
	CorpusManifestSHA256       string                                  `json:"corpusManifestSha256"`
	PacketsSHA256              string                                  `json:"packetsSha256"`
	EvidenceManifestSHA256     string                                  `json:"evidenceManifestSha256"`
	EvidencePrivateMapSHA256   string                                  `json:"evidencePrivateMapSha256"`
	TranscriptSetSHA256        string                                  `json:"transcriptSetSha256"`
	TranscriptFileSHA256       string                                  `json:"transcriptFileSha256"`
	StructureManifestSHA256    string                                  `json:"structureManifestSha256"`
	StructureAuthoritySHA256   string                                  `json:"structureAuthoritySha256"`
	PolicySHA256               string                                  `json:"policySha256"`
	PolicyID                   string                                  `json:"policyId"`
	Engine                     fillerbakeoff.TranscriptEngineIdentity  `json:"engine"`
	CorpusSources              int                                     `json:"corpusSources"`
	AdditionalStructureSources int                                     `json:"additionalStructureSources"`
	Sources                    int                                     `json:"sources"`
	CompleteTranscriptSources  int                                     `json:"completeTranscriptSources"`
	ProhibitedSources          int                                     `json:"prohibitedSources"`
	CoverageHoldSources        int                                     `json:"coverageHoldSources"`
	NoSignalObservedSources    int                                     `json:"noSignalObservedSources"`
	StructureCases             int                                     `json:"structureCases"`
	ProhibitedCases            int                                     `json:"prohibitedCases"`
	CoverageHoldCases          int                                     `json:"coverageHoldCases"`
	NoSignalObservedCases      int                                     `json:"noSignalObservedCases"`
	SourceDispositions         []TemporalSpokenSafetySourceDisposition `json:"sourceDispositions"`
	CaseDispositions           []TemporalSpokenSafetyCaseDisposition   `json:"caseDispositions"`
	CertificationStatus        string                                  `json:"certificationStatus"`
	TrainingAllowed            bool                                    `json:"trainingAllowed"`
	IngestionAllowed           bool                                    `json:"ingestionAllowed"`
	SchedulingAllowed          bool                                    `json:"schedulingAllowed"`
	ProductionAdmissionAllowed bool                                    `json:"productionAdmissionAllowed"`
	NextAction                 string                                  `json:"nextAction"`
}

type TemporalSpokenSafetySourceDisposition struct {
	SourceID         string                      `json:"sourceId"`
	AuthorityKind    string                      `json:"authorityKind"`
	EvidenceAlias    string                      `json:"evidenceAlias,omitempty"`
	SourceSHA256     string                      `json:"sourceSha256"`
	PacketSHA256     string                      `json:"packetSha256,omitempty"`
	SourceDurationMS int64                       `json:"sourceDurationMs"`
	TranscriptSHA256 string                      `json:"transcriptSha256,omitempty"`
	AudioSHA256      string                      `json:"audioSha256,omitempty"`
	AudioDurationMS  int64                       `json:"audioDurationMs,omitempty"`
	DerivedAliases   []string                    `json:"derivedAliases,omitempty"`
	Disposition      string                      `json:"disposition"`
	CoverageReason   string                      `json:"coverageReason,omitempty"`
	Matches          []TemporalSpokenSafetyMatch `json:"matches,omitempty"`
}

type TemporalSpokenSafetyMatch struct {
	RuleID  string `json:"ruleId"`
	Class   string `json:"class"`
	StartMS int64  `json:"startMs"`
	EndMS   int64  `json:"endMs"`
}

type TemporalSpokenSafetyCaseDisposition struct {
	EvidenceAlias  string   `json:"evidenceAlias"`
	SourceIDs      []string `json:"sourceIds"`
	Disposition    string   `json:"disposition"`
	TriggerSources []string `json:"triggerSources,omitempty"`
}

func PublishTemporalSpokenSafety(config TemporalSpokenSafetyConfig) (TemporalSpokenSafetyReport, string, error) {
	loaded, err := loadTemporalSpokenSafety(config)
	if err != nil {
		return TemporalSpokenSafetyReport{}, "", err
	}
	report, err := buildTemporalSpokenSafety(loaded, config.ProjectedAt.UTC())
	if err != nil {
		return TemporalSpokenSafetyReport{}, "", err
	}
	if err := validateTemporalSpokenSafetyReport(report); err != nil {
		return TemporalSpokenSafetyReport{}, "", fmt.Errorf("validate spoken-safety report: %w", err)
	}
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return TemporalSpokenSafetyReport{}, "", err
	}
	raw = append(raw, '\n')
	if err := writeTemporalTruthNew(config.OutputPath, raw, 0o600); err != nil {
		return TemporalSpokenSafetyReport{}, "", fmt.Errorf("publish spoken-safety report: %w", err)
	}
	return report, hashBytes(raw), nil
}
