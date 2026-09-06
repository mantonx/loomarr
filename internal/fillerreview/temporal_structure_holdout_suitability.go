package fillerreview

import (
	"fmt"
	"os"
	"strings"

	"github.com/loomarr/loomarr/internal/fillereval"
)

func loadTemporalStructureHoldoutSuitability(path string, evidence TemporalTruthEvidenceManifest, evidenceSHA string) (TemporalSuitabilityComparisonReport, string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return TemporalSuitabilityComparisonReport{}, "", err
	}
	report, err := readStrictJSON[TemporalSuitabilityComparisonReport](path)
	if err != nil {
		return TemporalSuitabilityComparisonReport{}, "", err
	}
	_, _, suitabilitySelectionSHA, err := selectTemporalSuitabilityCases(evidence, nil, len(evidence.Cases))
	if err != nil {
		return TemporalSuitabilityComparisonReport{}, "", fmt.Errorf("temporal structure holdout suitability selection: %w", err)
	}
	if report.SchemaVersion != TemporalSuitabilityComparisonSchemaVersion || report.ContractVersion != TemporalSuitabilityComparisonContractVersion || report.ComparedAt.IsZero() || report.EvidenceManifestSHA256 != evidenceSHA || report.SelectionSHA256 != suitabilitySelectionSHA || !reviewSHA256(report.FirstResultSHA256) || !reviewSHA256(report.SecondResultSHA256) || report.FirstResultSHA256 == report.SecondResultSHA256 || !validTemporalStructureHoldoutAssessor(report.FirstAssessor) || !validTemporalStructureHoldoutAssessor(report.SecondAssessor) || report.FirstAssessor.ID == report.SecondAssessor.ID || report.FirstAssessor.ModelFamily == report.SecondAssessor.ModelFamily || report.Cases != len(evidence.Cases) || len(report.CaseComparisons) != report.Cases || report.ProductionAdmissionAllowed {
		return TemporalSuitabilityComparisonReport{}, "", fmt.Errorf("temporal structure holdout suitability authority drift")
	}
	aliases := make(map[string]struct{}, len(evidence.Cases))
	for _, item := range evidence.Cases {
		aliases[item.Alias] = struct{}{}
	}
	seen := make(map[string]struct{}, report.Cases)
	var firstFailures, secondFailures, flagged, corroborated, uncorroborated, operational, coverage, candidate int
	for _, item := range report.CaseComparisons {
		durationMS, exists := temporalStructureHoldoutEvidenceDuration(evidence.Cases, item.EvidenceAlias)
		if _, aliasExists := aliases[item.EvidenceAlias]; !aliasExists || !exists || !validTemporalStructureHoldoutSuitabilityDisposition(item.Disposition) || !validTemporalStructureHoldoutSuitabilityOutcome(item.FirstOutcome) || !validTemporalStructureHoldoutSuitabilityOutcome(item.SecondOutcome) || validateTemporalStructureHoldoutSuitabilityFlags(item, durationMS) != nil {
			return TemporalSuitabilityComparisonReport{}, "", fmt.Errorf("temporal structure holdout suitability case is invalid")
		}
		if _, duplicate := seen[item.EvidenceAlias]; duplicate {
			return TemporalSuitabilityComparisonReport{}, "", fmt.Errorf("temporal structure holdout suitability repeats an alias")
		}
		seen[item.EvidenceAlias] = struct{}{}
		if strings.HasPrefix(item.FirstOutcome, "failure:") {
			firstFailures++
		}
		if strings.HasPrefix(item.SecondOutcome, "failure:") {
			secondFailures++
		}
		if len(item.UnionFlags) > 0 {
			flagged++
			if item.Disposition != "prohibited_hold" {
				return TemporalSuitabilityComparisonReport{}, "", fmt.Errorf("temporal structure holdout flagged suitability case is not held")
			}
			if len(item.CorroboratedFlags) > 0 {
				corroborated++
			} else {
				uncorroborated++
			}
		}
		switch item.Disposition {
		case "operational_hold":
			operational++
		case "coverage_hold":
			coverage++
		case "candidate_no_signal_observed":
			candidate++
		}
	}
	if len(seen) != report.Cases || firstFailures != report.FirstOperationalFailures || secondFailures != report.SecondOperationalFailures || flagged != report.FlaggedUnionCases || corroborated != report.CorroboratedProhibitedCases || uncorroborated != report.UncorroboratedProhibitedCases || operational != report.OperationalHoldCases || coverage != report.CoverageHoldCases || candidate != report.CandidateNoSignalCases || flagged+operational+coverage+candidate != report.Cases {
		return TemporalSuitabilityComparisonReport{}, "", fmt.Errorf("temporal structure holdout suitability summary does not match its cases")
	}
	return report, hashBytes(raw), nil
}

func temporalStructureHoldoutEvidenceDuration(cases []TemporalTruthEvidenceCase, alias string) (int64, bool) {
	for _, item := range cases {
		if item.Alias == alias {
			return item.DurationMS, true
		}
	}
	return 0, false
}

func validTemporalStructureHoldoutSuitabilityOutcome(value string) bool {
	return value == string(SuitabilityOutcomeProhibitedSignal) || value == string(SuitabilityOutcomeCoverageHold) || value == string(SuitabilityOutcomeNoSignalObserved) || strings.HasPrefix(value, "failure:") && strings.TrimPrefix(value, "failure:") != ""
}

func validateTemporalStructureHoldoutSuitabilityFlags(item TemporalSuitabilityCaseComparison, durationMS int64) error {
	union := make(map[SuitabilityFlag]struct{}, len(item.UnionFlags))
	for _, flag := range item.UnionFlags {
		if !validTemporalStructureHoldoutSuitabilityFlag(flag) {
			return fmt.Errorf("invalid union flag")
		}
		if _, duplicate := union[flag]; duplicate {
			return fmt.Errorf("duplicate union flag")
		}
		union[flag] = struct{}{}
	}
	corroborated := map[SuitabilityFlag]struct{}{}
	for _, flag := range item.CorroboratedFlags {
		if _, exists := union[flag]; !exists {
			return fmt.Errorf("corroborated flag is absent from union")
		}
		if _, duplicate := corroborated[flag]; duplicate {
			return fmt.Errorf("duplicate corroborated flag")
		}
		corroborated[flag] = struct{}{}
	}
	for _, observations := range [][]TemporalSuitabilityObservation{item.FirstOnlyFlags, item.SecondOnlyFlags} {
		for _, observation := range observations {
			if _, exists := union[observation.Kind]; !exists || observation.StartMS < 0 || observation.EndMS <= observation.StartMS || observation.EndMS > durationMS || !validTemporalStructureHoldoutSuitabilityModality(observation.Modality) {
				return fmt.Errorf("invalid one-family observation")
			}
		}
	}
	if (len(union) > 0) != (item.Disposition == "prohibited_hold") {
		return fmt.Errorf("flag and disposition mismatch")
	}
	return nil
}

func validTemporalStructureHoldoutSuitabilityFlag(value SuitabilityFlag) bool {
	return value == SuitabilityExplicitNudity || value == SuitabilityHatefulOrDegradingSlur
}

func validTemporalStructureHoldoutSuitabilityModality(value SuitabilityModality) bool {
	return value == SuitabilityModalityVideo || value == SuitabilityModalityAudio || value == SuitabilityModalityTranscript
}

func validTemporalStructureHoldoutAssessor(value fillereval.TemporalAssessorIdentity) bool {
	return strings.TrimSpace(value.ID) != "" && value.Provider == "openrouter" && strings.TrimSpace(value.Model) != "" && strings.TrimSpace(value.ModelFamily) != "" && strings.TrimSpace(value.ModelDigest) != "" && strings.TrimSpace(value.PromptVersion) != ""
}

func validTemporalStructureHoldoutSuitabilityDisposition(value string) bool {
	return value == "candidate_no_signal_observed" || value == "coverage_hold" || value == "operational_hold" || value == "prohibited_hold"
}
