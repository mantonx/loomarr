package fillerreview

import (
	"fmt"
	"strings"
)

func validateTemporalSuitabilityProjectionReport(report TemporalSuitabilityProjectionReport) error {
	if report.SchemaVersion != TemporalSuitabilityProjectionSchemaVersion || report.ContractVersion != TemporalSuitabilityProjectionContractVersion || report.ProjectedAt.IsZero() || !reviewSHA256(report.PublicManifestSHA256) || !reviewSHA256(report.StructureAuthoritySHA256) || !reviewSHA256(report.SuitabilityComparisonSHA256) || !reviewSHA256(report.FirstResultSHA256) || !reviewSHA256(report.SecondResultSHA256) || report.FirstResultSHA256 == report.SecondResultSHA256 {
		return fmt.Errorf("identity or input authority is invalid")
	}
	if !validTemporalStructureHoldoutAssessor(report.FirstAssessor) || !validTemporalStructureHoldoutAssessor(report.SecondAssessor) || report.FirstAssessor.ID == report.SecondAssessor.ID || report.FirstAssessor.ModelFamily == report.SecondAssessor.ModelFamily {
		return fmt.Errorf("assessor authority is invalid")
	}
	if report.Cases <= 0 || report.Sources <= 0 || len(report.CaseDispositions) != report.Cases || len(report.SourceDispositions) != report.Sources || report.TrainingAllowed || report.IngestionAllowed || report.SchedulingAllowed || report.ProductionAdmissionAllowed || report.NextAction != "certify_suitability_recall_before_admission" {
		return fmt.Errorf("counts or permission boundary is invalid")
	}

	sources := make(map[string]TemporalSuitabilitySourceDisposition, report.Sources)
	var prohibitedSources, operationalSources, coverageSources, candidateSources int
	for index, item := range report.SourceDispositions {
		if strings.TrimSpace(item.SourceID) == "" || !reviewSHA256(item.SourceSHA256) || item.SourceDurationMS <= 0 || !validTemporalSuitabilityProjectionDisposition(item.Disposition) || len(item.DerivedAliases) == 0 || !strictlySortedStrings(item.DerivedAliases) {
			return fmt.Errorf("source disposition %d is invalid", index)
		}
		if index > 0 && report.SourceDispositions[index-1].SourceID >= item.SourceID {
			return fmt.Errorf("source dispositions are not canonical")
		}
		if _, duplicate := sources[item.SourceID]; duplicate {
			return fmt.Errorf("source disposition repeats %q", item.SourceID)
		}
		if item.Provenance.Kind != TemporalStructureSourceBoundedItem && item.Provenance.Kind != TemporalStructureSourceProgrammeParent || strings.TrimSpace(item.Provenance.Authority) == "" || strings.TrimSpace(item.Provenance.Reference) == "" || !reviewSHA256(item.Provenance.MetadataSHA256) || item.Provenance.RetrievedAt.IsZero() {
			return fmt.Errorf("source disposition %q has invalid provenance", item.SourceID)
		}
		if (item.Disposition == TemporalSuitabilityDispositionProhibited) != (len(item.Observations) > 0) {
			return fmt.Errorf("source disposition %q mismatches its prohibited observations", item.SourceID)
		}
		if err := validateTemporalSuitabilityProjectedObservations(item, report.FirstAssessor.ID, report.SecondAssessor.ID); err != nil {
			return err
		}
		sources[item.SourceID] = item
		switch item.Disposition {
		case TemporalSuitabilityDispositionProhibited:
			prohibitedSources++
		case TemporalSuitabilityDispositionOperational:
			operationalSources++
		case TemporalSuitabilityDispositionCoverage:
			coverageSources++
		case TemporalSuitabilityDispositionCandidate:
			candidateSources++
		}
	}
	if prohibitedSources != report.ProhibitedSources || operationalSources != report.OperationalHoldSources || coverageSources != report.CoverageHoldSources || candidateSources != report.CandidateNoSignalSources || prohibitedSources+operationalSources+coverageSources+candidateSources != report.Sources {
		return fmt.Errorf("source summary does not match source dispositions")
	}

	cases := make(map[string]TemporalSuitabilityProjectedCase, report.Cases)
	var prohibitedCases, operationalCases, coverageCases, candidateCases int
	for index, item := range report.CaseDispositions {
		if strings.TrimSpace(item.EvidenceAlias) == "" || !validTemporalSuitabilityProjectionDisposition(item.InputDisposition) || !validTemporalSuitabilityProjectionDisposition(item.EffectiveDisposition) || len(item.SourceIDs) == 0 || !strictlySortedStrings(item.SourceIDs) || len(item.TriggerSourceIDs) > 0 && !strictlySortedStrings(item.TriggerSourceIDs) {
			return fmt.Errorf("case disposition %d is invalid", index)
		}
		if index > 0 && report.CaseDispositions[index-1].EvidenceAlias >= item.EvidenceAlias {
			return fmt.Errorf("case dispositions are not canonical")
		}
		if _, duplicate := cases[item.EvidenceAlias]; duplicate {
			return fmt.Errorf("case disposition repeats %q", item.EvidenceAlias)
		}
		triggerSet := make(map[string]struct{}, len(item.TriggerSourceIDs))
		for _, sourceID := range item.TriggerSourceIDs {
			source, exists := sources[sourceID]
			if !exists || source.Disposition != TemporalSuitabilityDispositionProhibited || !containsSortedString(item.SourceIDs, sourceID) {
				return fmt.Errorf("case %q has an invalid trigger source", item.EvidenceAlias)
			}
			triggerSet[sourceID] = struct{}{}
		}
		for _, sourceID := range item.SourceIDs {
			source, exists := sources[sourceID]
			if !exists || !containsSortedString(source.DerivedAliases, item.EvidenceAlias) {
				return fmt.Errorf("case %q does not bind source %q", item.EvidenceAlias, sourceID)
			}
			if source.Disposition == TemporalSuitabilityDispositionProhibited {
				if _, exists := triggerSet[sourceID]; !exists {
					return fmt.Errorf("case %q does not inherit a source quarantine", item.EvidenceAlias)
				}
			}
		}
		expected := item.InputDisposition
		if len(item.TriggerSourceIDs) > 0 {
			expected = TemporalSuitabilityDispositionProhibited
		}
		if item.EffectiveDisposition != expected {
			return fmt.Errorf("case %q has an invalid effective disposition", item.EvidenceAlias)
		}
		cases[item.EvidenceAlias] = item
		switch item.EffectiveDisposition {
		case TemporalSuitabilityDispositionProhibited:
			prohibitedCases++
		case TemporalSuitabilityDispositionOperational:
			operationalCases++
		case TemporalSuitabilityDispositionCoverage:
			coverageCases++
		case TemporalSuitabilityDispositionCandidate:
			candidateCases++
		}
	}
	if prohibitedCases != report.ProhibitedCases || operationalCases != report.OperationalHoldCases || coverageCases != report.CoverageHoldCases || candidateCases != report.CandidateNoSignalCases || prohibitedCases+operationalCases+coverageCases+candidateCases != report.Cases {
		return fmt.Errorf("case summary does not match case dispositions")
	}
	for _, source := range report.SourceDispositions {
		for _, alias := range source.DerivedAliases {
			item, exists := cases[alias]
			if !exists || !containsSortedString(item.SourceIDs, source.SourceID) {
				return fmt.Errorf("source %q names an unknown derivative", source.SourceID)
			}
		}
	}
	return nil
}

func validateTemporalSuitabilityProjectedObservations(source TemporalSuitabilitySourceDisposition, firstAssessorID, secondAssessorID string) error {
	for index, item := range source.Observations {
		if !validTemporalStructureHoldoutSuitabilityFlag(item.Kind) || !validTemporalStructureHoldoutSuitabilityModality(item.Modality) || item.StartMS < 0 || item.EndMS <= item.StartMS || item.EndMS > source.SourceDurationMS || len(item.Witnesses) == 0 {
			return fmt.Errorf("source %q observation %d is invalid", source.SourceID, index)
		}
		if index > 0 && !lessTemporalSuitabilityProjectedObservation(source.Observations[index-1], item) {
			return fmt.Errorf("source %q observations are not canonical", source.SourceID)
		}
		if index > 0 {
			previous := source.Observations[index-1]
			if previous.Kind == item.Kind && previous.Modality == item.Modality && item.StartMS < previous.EndMS {
				return fmt.Errorf("source %q retains overlapping observations", source.SourceID)
			}
		}
		for witnessIndex, witness := range item.Witnesses {
			if !containsSortedString(source.DerivedAliases, witness.EvidenceAlias) || witness.AssessorID != firstAssessorID && witness.AssessorID != secondAssessorID || witness.CaseStartMS < 0 || witness.CaseEndMS <= witness.CaseStartMS || witness.SourceStartMS < item.StartMS || witness.SourceEndMS > item.EndMS || witness.SourceEndMS <= witness.SourceStartMS {
				return fmt.Errorf("source %q observation %d witness %d is invalid", source.SourceID, index, witnessIndex)
			}
			if witnessIndex > 0 && !lessTemporalSuitabilityProjectionWitness(item.Witnesses[witnessIndex-1], witness) {
				return fmt.Errorf("source %q observation witnesses are not canonical", source.SourceID)
			}
		}
	}
	return nil
}

func validTemporalSuitabilityProjectionDisposition(value string) bool {
	return value == TemporalSuitabilityDispositionProhibited || value == TemporalSuitabilityDispositionOperational || value == TemporalSuitabilityDispositionCoverage || value == TemporalSuitabilityDispositionCandidate
}

func strictlySortedStrings(values []string) bool {
	for index, value := range values {
		if strings.TrimSpace(value) == "" || index > 0 && values[index-1] >= value {
			return false
		}
	}
	return len(values) > 0
}

func containsSortedString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
		if value > target {
			return false
		}
	}
	return false
}
