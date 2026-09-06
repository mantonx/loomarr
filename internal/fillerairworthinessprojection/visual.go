package fillerairworthinessprojection

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/loomarr/loomarr/internal/fillerairworthiness"
	"github.com/loomarr/loomarr/internal/fillervisualsafety"
)

const (
	VisualAuthoritySchemaVersion   = 1
	VisualAuthorityContractVersion = "filler-visual-airworthiness-projection-authority-v1"
	VisualEvidenceContractVersion  = "filler-visual-airworthiness-projection-v1"
)

// VisualAuthority locks the complete visual coverage profile, exact producer
// profiles, and opaque match mapping certified for Airworthiness projection.
type VisualAuthority struct {
	SchemaVersion         int                                  `json:"schemaVersion"`
	ContractVersion       string                               `json:"contractVersion"`
	PolicySHA256          string                               `json:"policySha256"`
	CertificationSHA256   string                               `json:"certificationSha256"`
	CoverageProfileSHA256 string                               `json:"coverageProfileSha256"`
	Producers             []fillervisualsafety.ProducerProfile `json:"producers"`
	Rules                 []Rule                               `json:"rules"`
	SHA256                string                               `json:"sha256"`
}

type VisualProjection struct {
	Evidence    fillerairworthiness.AxisEvidence
	RawEvidence []byte
}

func SealVisualAuthority(authority VisualAuthority) (VisualAuthority, error) {
	authority.SchemaVersion = VisualAuthoritySchemaVersion
	authority.ContractVersion = VisualAuthorityContractVersion
	authority.Producers = slices.Clone(authority.Producers)
	authority.Rules = slices.Clone(authority.Rules)
	slices.SortFunc(authority.Producers, compareVisualProducers)
	slices.SortFunc(authority.Rules, func(left, right Rule) int { return strings.Compare(left.ID, right.ID) })
	authority.SHA256 = visualAuthoritySHA256(authority)
	if err := ValidateVisualAuthority(authority); err != nil {
		return VisualAuthority{}, err
	}
	return authority, nil
}

func ValidateVisualAuthority(authority VisualAuthority) error {
	if authority.SchemaVersion != VisualAuthoritySchemaVersion || authority.ContractVersion != VisualAuthorityContractVersion ||
		!validSHA256(authority.PolicySHA256) || !validSHA256(authority.CertificationSHA256) ||
		!validSHA256(authority.CoverageProfileSHA256) || len(authority.Producers) == 0 || len(authority.Rules) == 0 ||
		!slices.IsSortedFunc(authority.Producers, compareVisualProducers) ||
		!slices.IsSortedFunc(authority.Rules, func(left, right Rule) int { return strings.Compare(left.ID, right.ID) }) ||
		authority.SHA256 != visualAuthoritySHA256(authority) {
		return fmt.Errorf("visual Airworthiness projection authority is invalid")
	}
	for index, producer := range authority.Producers {
		if !validVisualProducer(producer) || index > 0 && compareVisualProducers(authority.Producers[index-1], producer) == 0 {
			return fmt.Errorf("visual Airworthiness producer profile is invalid or repeated")
		}
	}
	flags := make(map[fillerairworthiness.Flag]struct{})
	for index, rule := range authority.Rules {
		if !validPublicID(rule.ID) || index > 0 && authority.Rules[index-1].ID == rule.ID ||
			!validSeverity(rule.Severity) || !validContext(rule.Context) ||
			!slices.Contains(fillerairworthiness.AxesForFlag(rule.Flag), fillerairworthiness.AxisVisual) {
			return fmt.Errorf("visual Airworthiness projection rule is invalid")
		}
		flags[rule.Flag] = struct{}{}
	}
	_, err := fillerairworthiness.NormalizeAxisProfile(visualProfile(authority, flags))
	return err
}

func VisualProfile(authority VisualAuthority) (fillerairworthiness.AxisProfile, error) {
	if err := ValidateVisualAuthority(authority); err != nil {
		return fillerairworthiness.AxisProfile{}, err
	}
	flags := make(map[fillerairworthiness.Flag]struct{})
	for _, rule := range authority.Rules {
		flags[rule.Flag] = struct{}{}
	}
	return fillerairworthiness.NormalizeAxisProfile(visualProfile(authority, flags))
}

func visualProfile(authority VisualAuthority, flags map[fillerairworthiness.Flag]struct{}) fillerairworthiness.AxisProfile {
	certified := make([]fillerairworthiness.Flag, 0, len(flags))
	for flag := range flags {
		certified = append(certified, flag)
	}
	slices.Sort(certified)
	return fillerairworthiness.AxisProfile{
		Axis: fillerairworthiness.AxisVisual, EvidenceContract: VisualEvidenceContractVersion,
		PolicySHA256: authority.PolicySHA256, CertificationSHA256: authority.CertificationSHA256,
		ImplementationSHA256: authority.SHA256, CertifiedFlags: certified,
	}
}

// ProjectVisual reproduces the complete visual reducer before translating
// certified opaque matches. Apple source-level positives conservatively bind
// the complete source interval because that API supplies no timestamp.
func ProjectVisual(subject Subject, source fillervisualsafety.SourceAuthority, plan fillervisualsafety.CoveragePlan, coverage fillervisualsafety.CoverageEvidence, observations []fillervisualsafety.Observation, result fillervisualsafety.Result, authority VisualAuthority) (VisualProjection, error) {
	profile, err := VisualProfile(authority)
	if err != nil || !validSHA256(subject.SHA256) || !validSHA256(subject.EvidenceSHA256) || subject.DurationMS <= 0 ||
		fillervisualsafety.ValidateSourceAuthority(source) != nil || fillervisualsafety.ValidateCoveragePlan(plan) != nil ||
		fillervisualsafety.ValidateCoverageEvidence(plan, coverage) != nil || fillervisualsafety.ValidateResult(result) != nil ||
		source.SourceSHA256 != subject.EvidenceSHA256 || source.DurationMS != subject.DurationMS ||
		source.PolicySHA256 != authority.PolicySHA256 || plan.Profile.SHA256 != authority.CoverageProfileSHA256 ||
		!reflect.DeepEqual(fillervisualsafety.Reduce(source, coverage, plan, observations), result) {
		return VisualProjection{}, fmt.Errorf("visual Airworthiness projection input is invalid or drifted")
	}
	allowed := make(map[fillervisualsafety.ProducerProfile]struct{}, len(authority.Producers))
	for _, producer := range authority.Producers {
		allowed[producer] = struct{}{}
	}
	for _, observation := range observations {
		if fillervisualsafety.ValidateObservation(observation) != nil {
			return VisualProjection{}, fmt.Errorf("visual Airworthiness observation is invalid")
		}
		if _, exists := allowed[observation.Profile]; !exists {
			return VisualProjection{}, fmt.Errorf("visual Airworthiness producer profile is not certified")
		}
	}
	raw, err := json.Marshal(struct {
		Source       fillervisualsafety.SourceAuthority  `json:"source"`
		Plan         fillervisualsafety.CoveragePlan     `json:"plan"`
		Coverage     fillervisualsafety.CoverageEvidence `json:"coverage"`
		Observations []fillervisualsafety.Observation    `json:"observations"`
		Result       fillervisualsafety.Result           `json:"result"`
	}{source, plan, coverage, observations, result})
	if err != nil {
		return VisualProjection{}, err
	}
	evidence := fillerairworthiness.AxisEvidence{
		SubjectSHA256: subject.SHA256, Profile: profile, Coverage: fillerairworthiness.CoverageIncomplete,
		EvidenceSHA256: digest(raw), Observations: []fillerairworthiness.Observation{},
	}
	rules := make(map[string]Rule, len(authority.Rules))
	for _, rule := range authority.Rules {
		rules[rule.ID] = rule
	}
	unknown := false
	for _, observation := range observations {
		if observation.State != fillervisualsafety.ObservationProhibited {
			continue
		}
		for _, matchID := range observation.PolicyMatchIDs {
			rule, exists := rules[matchID]
			if !exists {
				unknown = true
				continue
			}
			intervals := observation.Intervals
			if observation.Profile.Family == fillervisualsafety.ProducerAppleSCA {
				intervals = []fillervisualsafety.Interval{{StartMS: 0, EndMS: subject.DurationMS}}
			}
			for index, interval := range intervals {
				evidence.Observations = append(evidence.Observations, fillerairworthiness.Observation{
					ID: visualObservationID(result.SHA256, observation.SHA256, matchID, index), Flag: rule.Flag,
					Severity: rule.Severity, Context: rule.Context, StartMS: interval.StartMS, EndMS: interval.EndMS,
				})
			}
		}
	}
	if result.Outcome == fillervisualsafety.OutcomeNoSignal ||
		result.Outcome == fillervisualsafety.OutcomeQuarantine && len(evidence.Observations) > 0 && !unknown && onlyVisualPositiveReasons(result.Reasons) {
		evidence.Coverage = fillerairworthiness.CoverageComplete
	}
	if err := fillerairworthiness.ValidateAxisEvidence(evidence, subject.DurationMS); err != nil {
		return VisualProjection{}, fmt.Errorf("visual Airworthiness projection is invalid: %w", err)
	}
	return VisualProjection{Evidence: evidence, RawEvidence: raw}, nil
}

func visualObservationID(resultSHA, observationSHA, matchID string, interval int) string {
	return fmt.Sprintf("visual-%s", digest([]byte(fmt.Sprintf("%s\x00%s\x00%s\x00%d", resultSHA, observationSHA, matchID, interval)))[:24])
}

func onlyVisualPositiveReasons(reasons []fillervisualsafety.Reason) bool {
	return len(reasons) > 0 && !slices.ContainsFunc(reasons, func(reason fillervisualsafety.Reason) bool {
		return reason != fillervisualsafety.ReasonPortableProhibited && reason != fillervisualsafety.ReasonDirectProhibited &&
			reason != fillervisualsafety.ReasonAppleProhibited
	})
}

func visualAuthoritySHA256(authority VisualAuthority) string {
	authority.SHA256 = ""
	raw, _ := json.Marshal(authority)
	return digest(raw)
}

func compareVisualProducers(left, right fillervisualsafety.ProducerProfile) int {
	leftValue := string(left.Family) + "\x00" + left.Implementation + "\x00" + left.CapabilitySHA256 + "\x00" + left.EvidenceContract
	rightValue := string(right.Family) + "\x00" + right.Implementation + "\x00" + right.CapabilitySHA256 + "\x00" + right.EvidenceContract
	return strings.Compare(leftValue, rightValue)
}

func validVisualProducer(producer fillervisualsafety.ProducerProfile) bool {
	return (producer.Family == fillervisualsafety.ProducerPortable || producer.Family == fillervisualsafety.ProducerDirectVideo ||
		producer.Family == fillervisualsafety.ProducerAppleSCA) && validPublicID(producer.Implementation) &&
		validSHA256(producer.CapabilitySHA256) && validPublicID(producer.EvidenceContract)
}

func validPublicID(value string) bool {
	return value != "" && len(value) <= 256 && value == strings.TrimSpace(value) &&
		!strings.ContainsAny(value, "\r\n\t")
}
