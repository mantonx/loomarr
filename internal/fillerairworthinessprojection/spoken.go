// Package fillerairworthinessprojection authenticates safety-producer output
// and translates opaque certified matches into closed Airworthiness evidence.
package fillerairworthinessprojection

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/loomarr/loomarr/internal/fillerairworthiness"
	"github.com/loomarr/loomarr/internal/fillersafety"
)

const (
	SpokenAuthoritySchemaVersion   = 1
	SpokenAuthorityContractVersion = "filler-spoken-airworthiness-projection-authority-v1"
	SpokenEvidenceContractVersion  = "filler-spoken-airworthiness-projection-v1"
)

type Rule struct {
	ID       string                       `json:"id"`
	Flag     fillerairworthiness.Flag     `json:"flag"`
	Severity fillerairworthiness.Severity `json:"severity"`
	Context  fillerairworthiness.Context  `json:"context"`
}

// SpokenAuthority binds the exact producer and its opaque rule-to-suitability
// mapping. It contains no restricted words or model prose.
type SpokenAuthority struct {
	SchemaVersion            int    `json:"schemaVersion"`
	ContractVersion          string `json:"contractVersion"`
	PolicySHA256             string `json:"policySha256"`
	CertificationSHA256      string `json:"certificationSha256"`
	ProposerSHA256           string `json:"proposerSha256"`
	EvaluationImplementation string `json:"evaluationImplementation"`
	Rules                    []Rule `json:"rules"`
	SHA256                   string `json:"sha256"`
}

type Subject struct {
	SHA256         string
	EvidenceSHA256 string
	EvidenceBytes  int64
	DurationMS     int64
}

type SpokenProjection struct {
	Evidence    fillerairworthiness.AxisEvidence
	RawEvidence []byte
}

func SealSpokenAuthority(authority SpokenAuthority) (SpokenAuthority, error) {
	authority.SchemaVersion = SpokenAuthoritySchemaVersion
	authority.ContractVersion = SpokenAuthorityContractVersion
	authority.Rules = slices.Clone(authority.Rules)
	slices.SortFunc(authority.Rules, func(left, right Rule) int { return strings.Compare(left.ID, right.ID) })
	authority.SHA256 = spokenAuthoritySHA256(authority)
	if err := ValidateSpokenAuthority(authority); err != nil {
		return SpokenAuthority{}, err
	}
	return authority, nil
}

func ValidateSpokenAuthority(authority SpokenAuthority) error {
	if authority.SchemaVersion != SpokenAuthoritySchemaVersion || authority.ContractVersion != SpokenAuthorityContractVersion ||
		!validSHA256(authority.PolicySHA256) || !validSHA256(authority.CertificationSHA256) ||
		!validSHA256(authority.ProposerSHA256) || !validToken(authority.EvaluationImplementation) ||
		len(authority.Rules) == 0 || !slices.IsSortedFunc(authority.Rules, func(left, right Rule) int { return strings.Compare(left.ID, right.ID) }) ||
		authority.SHA256 != spokenAuthoritySHA256(authority) {
		return fmt.Errorf("spoken Airworthiness projection authority is invalid")
	}
	seenFlags := make(map[fillerairworthiness.Flag]struct{})
	for index, rule := range authority.Rules {
		if !fillersafety.ValidPolicyRuleID(rule.ID) || index > 0 && authority.Rules[index-1].ID == rule.ID ||
			!validSeverity(rule.Severity) || !validContext(rule.Context) ||
			!slices.Contains(fillerairworthiness.AxesForFlag(rule.Flag), fillerairworthiness.AxisSpoken) {
			return fmt.Errorf("spoken Airworthiness projection rule is invalid")
		}
		seenFlags[rule.Flag] = struct{}{}
	}
	_, err := fillerairworthiness.NormalizeAxisProfile(spokenProfile(authority, seenFlags))
	return err
}

func SpokenProfile(authority SpokenAuthority) (fillerairworthiness.AxisProfile, error) {
	if err := ValidateSpokenAuthority(authority); err != nil {
		return fillerairworthiness.AxisProfile{}, err
	}
	flags := make(map[fillerairworthiness.Flag]struct{})
	for _, rule := range authority.Rules {
		flags[rule.Flag] = struct{}{}
	}
	return fillerairworthiness.NormalizeAxisProfile(spokenProfile(authority, flags))
}

func spokenProfile(authority SpokenAuthority, flags map[fillerairworthiness.Flag]struct{}) fillerairworthiness.AxisProfile {
	certified := make([]fillerairworthiness.Flag, 0, len(flags))
	for flag := range flags {
		certified = append(certified, flag)
	}
	slices.Sort(certified)
	return fillerairworthiness.AxisProfile{
		Axis: fillerairworthiness.AxisSpoken, EvidenceContract: SpokenEvidenceContractVersion,
		PolicySHA256: authority.PolicySHA256, CertificationSHA256: authority.CertificationSHA256,
		ImplementationSHA256: authority.SHA256, CertifiedFlags: certified,
	}
}

// ProjectSpoken reproduces a complete spoken report before translating known
// positive rule ids. Unknown matches retain positive evidence where possible
// but make coverage incomplete; a negative clears only certified flags.
func ProjectSpoken(subject Subject, report fillersafety.EvaluationReport, authority SpokenAuthority) (SpokenProjection, error) {
	profile, err := SpokenProfile(authority)
	if err != nil || fillersafety.ValidateEvaluationReport(report) != nil || !validSHA256(subject.SHA256) ||
		!validSHA256(subject.EvidenceSHA256) || subject.EvidenceBytes <= 0 || subject.DurationMS <= 0 ||
		report.Run.SourceSHA256 != subject.EvidenceSHA256 || report.Run.DurationMS != subject.DurationMS ||
		report.Run.SourceBytes != subject.EvidenceBytes ||
		report.Run.PolicySHA256 != authority.PolicySHA256 || report.Run.CertificationSHA256 != authority.CertificationSHA256 ||
		report.Run.ProposerSHA256 != authority.ProposerSHA256 || report.Run.Implementation != authority.EvaluationImplementation {
		return SpokenProjection{}, fmt.Errorf("spoken Airworthiness projection input is invalid or drifted")
	}
	raw, err := json.Marshal(report)
	if err != nil {
		return SpokenProjection{}, err
	}
	evidence := fillerairworthiness.AxisEvidence{
		SubjectSHA256: subject.SHA256, Profile: profile, EvidenceSHA256: digest(raw),
		Coverage: fillerairworthiness.CoverageIncomplete, Observations: []fillerairworthiness.Observation{},
	}
	rules := make(map[string]Rule, len(authority.Rules))
	for _, rule := range authority.Rules {
		rules[rule.ID] = rule
	}
	unknown := false
	for index, assessment := range report.Evidence.Audio {
		if assessment.State != fillersafety.AudioDetected {
			continue
		}
		candidate := report.Evidence.Candidates[index]
		if len(assessment.MatchedRuleIDs) == 0 {
			unknown = true
		}
		for _, ruleID := range assessment.MatchedRuleIDs {
			rule, exists := rules[ruleID]
			if !exists {
				unknown = true
				continue
			}
			evidence.Observations = append(evidence.Observations, fillerairworthiness.Observation{
				ID: observationID(report.SHA256, candidate.ID, ruleID), Flag: rule.Flag,
				Severity: rule.Severity, Context: rule.Context, StartMS: candidate.StartMS, EndMS: candidate.EndMS,
			})
		}
	}
	if report.Result.Outcome == fillersafety.OutcomeCandidateRejected {
		evidence.Coverage = fillerairworthiness.CoverageComplete
	} else if report.Result.Outcome == fillersafety.OutcomeQuarantine && len(evidence.Observations) > 0 && !unknown &&
		reflect.DeepEqual(report.Result.Reasons, []fillersafety.Reason{fillersafety.ReasonAudioProhibitedSignal}) {
		evidence.Coverage = fillerairworthiness.CoverageComplete
	}
	if err := fillerairworthiness.ValidateAxisEvidence(evidence, subject.DurationMS); err != nil {
		return SpokenProjection{}, fmt.Errorf("spoken Airworthiness projection is invalid: %w", err)
	}
	return SpokenProjection{Evidence: evidence, RawEvidence: raw}, nil
}

func observationID(reportSHA256, candidateID, ruleID string) string {
	return "spoken-" + digest([]byte(reportSHA256 + "\x00" + candidateID + "\x00" + ruleID))[:24]
}

func spokenAuthoritySHA256(authority SpokenAuthority) string {
	authority.SHA256 = ""
	raw, _ := json.Marshal(authority)
	return digest(raw)
}

func digest(raw []byte) string { value := sha256.Sum256(raw); return hex.EncodeToString(value[:]) }

func validSHA256(value string) bool {
	decoded, err := hex.DecodeString(value)
	return len(value) == 64 && value == strings.ToLower(value) && err == nil && len(decoded) == sha256.Size
}

func validToken(value string) bool {
	return value != "" && len(value) <= 128 && strings.TrimSpace(value) == value
}

func validSeverity(value fillerairworthiness.Severity) bool {
	return value == fillerairworthiness.SeverityLow || value == fillerairworthiness.SeverityModerate || value == fillerairworthiness.SeverityHigh
}

func validContext(value fillerairworthiness.Context) bool {
	return value == fillerairworthiness.ContextPresence || value == fillerairworthiness.ContextDepiction ||
		value == fillerairworthiness.ContextPromotion || value == fillerairworthiness.ContextInstruction
}
