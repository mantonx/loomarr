package fillerreview

import (
	"fmt"
	"slices"
	"strings"
)

func validateTemporalSpokenSafetyReport(report TemporalSpokenSafetyReport) error {
	if report.SchemaVersion != TemporalSpokenSafetySchemaVersion || report.ContractVersion != TemporalSpokenSafetyContractVersion || report.ProjectedAt.IsZero() || report.PolicyID == "" || report.Sources <= 0 || report.StructureCases <= 0 || report.CertificationStatus != temporalSpokenSafetyCertificationNotRun || report.TrainingAllowed || report.IngestionAllowed || report.SchedulingAllowed || report.ProductionAdmissionAllowed || report.NextAction != "run_source_disjoint_spoken_safety_certification_before_admission" {
		return fmt.Errorf("spoken-safety report identity, permissions, or next action is invalid")
	}
	for _, digest := range []string{report.CorpusManifestSHA256, report.PacketsSHA256, report.EvidenceManifestSHA256, report.EvidencePrivateMapSHA256, report.TranscriptSetSHA256, report.TranscriptFileSHA256, report.StructureManifestSHA256, report.StructureAuthoritySHA256, report.PolicySHA256, report.Engine.BinarySHA256, report.Engine.ModelSHA256} {
		if !reviewSHA256(digest) {
			return fmt.Errorf("spoken-safety report contains an invalid authority digest")
		}
	}
	if strings.TrimSpace(report.Engine.Provider) == "" || strings.TrimSpace(report.Engine.ImplementationVersion) == "" || strings.TrimSpace(report.Engine.Model) == "" || report.CorpusSources <= 0 || report.AdditionalStructureSources < 0 || report.CorpusSources+report.AdditionalStructureSources != report.Sources || len(report.SourceDispositions) != report.Sources || len(report.CaseDispositions) != report.StructureCases || report.CompleteTranscriptSources+report.CoverageHoldSources < report.Sources {
		return fmt.Errorf("spoken-safety report engine or summary is invalid")
	}
	if report.ProhibitedSources+report.CoverageHoldSources+report.NoSignalObservedSources != report.Sources || report.ProhibitedCases+report.CoverageHoldCases+report.NoSignalObservedCases != report.StructureCases {
		return fmt.Errorf("spoken-safety report summary is not exhaustive")
	}
	sources := make(map[string]TemporalSpokenSafetySourceDisposition, len(report.SourceDispositions))
	previousSource := ""
	complete := 0
	prohibited, coverage, noSignal := 0, 0, 0
	for _, source := range report.SourceDispositions {
		if !validTemporalSpokenSafetySourceID(source.SourceID) || source.SourceID <= previousSource || !reviewSHA256(source.SourceSHA256) || source.SourceDurationMS <= 0 || len(source.DerivedAliases) > 0 && !strictlySortedStrings(source.DerivedAliases) {
			return fmt.Errorf("spoken-safety source disposition is invalid or disordered")
		}
		previousSource = source.SourceID
		switch source.AuthorityKind {
		case TemporalSpokenSafetySourceCorpus:
			if !reviewSHA256(source.PacketSHA256) {
				return fmt.Errorf("spoken-safety corpus source has invalid packet authority")
			}
		case TemporalSpokenSafetySourceConstruction:
			if source.PacketSHA256 != "" || source.EvidenceAlias != "" || source.CoverageReason != temporalSpokenSafetyCoverageMissingTranscript {
				return fmt.Errorf("spoken-safety construction-only source has invalid authority")
			}
		default:
			return fmt.Errorf("spoken-safety source has unknown authority kind")
		}
		if source.TranscriptSHA256 != "" {
			complete++
			if !reviewSHA256(source.TranscriptSHA256) || !reviewSHA256(source.AudioSHA256) || source.AudioDurationMS <= 0 || source.CoverageReason != "" {
				return fmt.Errorf("spoken-safety complete transcript authority is invalid")
			}
		} else if source.AudioSHA256 != "" || source.AudioDurationMS != 0 || source.CoverageReason != temporalSpokenSafetyCoverageMissingTranscript && source.CoverageReason != temporalSpokenSafetyCoverageMissingSourceMedia || len(source.Matches) != 0 {
			return fmt.Errorf("spoken-safety missing transcript does not fail closed")
		}
		hasProhibited, hasAmbiguous := false, false
		var previousMatch *TemporalSpokenSafetyMatch
		for index := range source.Matches {
			match := &source.Matches[index]
			if !validTemporalSpokenSafetyRuleID(match.RuleID) || match.Class != TemporalSpokenSafetyMatchProhibited && match.Class != TemporalSpokenSafetyMatchAmbiguous || match.StartMS < 0 || match.EndMS <= match.StartMS || match.EndMS > source.SourceDurationMS {
				return fmt.Errorf("spoken-safety match is invalid")
			}
			if previousMatch != nil && lessTemporalSpokenSafetyMatch(*match, *previousMatch) {
				return fmt.Errorf("spoken-safety matches are disordered")
			}
			previousMatch = match
			hasProhibited = hasProhibited || match.Class == TemporalSpokenSafetyMatchProhibited
			hasAmbiguous = hasAmbiguous || match.Class == TemporalSpokenSafetyMatchAmbiguous
		}
		switch source.Disposition {
		case TemporalSpokenSafetyDispositionProhibited:
			prohibited++
			if !hasProhibited || source.TranscriptSHA256 == "" {
				return fmt.Errorf("spoken-safety prohibited source lacks an exact match")
			}
		case TemporalSpokenSafetyDispositionCoverage:
			coverage++
			if source.TranscriptSHA256 != "" && (!hasAmbiguous || hasProhibited) {
				return fmt.Errorf("spoken-safety covered source lacks ambiguous evidence")
			}
		case TemporalSpokenSafetyDispositionNoSignal:
			noSignal++
			if source.TranscriptSHA256 == "" || len(source.Matches) != 0 {
				return fmt.Errorf("spoken-safety no-signal source lacks complete clean observation")
			}
		default:
			return fmt.Errorf("spoken-safety source has unknown disposition")
		}
		sources[source.SourceID] = source
	}
	if complete != report.CompleteTranscriptSources || prohibited != report.ProhibitedSources || coverage != report.CoverageHoldSources || noSignal != report.NoSignalObservedSources {
		return fmt.Errorf("spoken-safety source summary does not reproduce")
	}
	previousAlias := ""
	caseProhibited, caseCoverage, caseNoSignal := 0, 0, 0
	for _, item := range report.CaseDispositions {
		if strings.TrimSpace(item.EvidenceAlias) == "" || item.EvidenceAlias <= previousAlias || len(item.SourceIDs) == 0 || !strictlySortedStrings(item.SourceIDs) || len(item.TriggerSources) > 0 && !strictlySortedStrings(item.TriggerSources) {
			return fmt.Errorf("spoken-safety case disposition is invalid or disordered")
		}
		previousAlias = item.EvidenceAlias
		want := TemporalSpokenSafetyDispositionNoSignal
		var triggers []string
		for _, id := range item.SourceIDs {
			source, exists := sources[id]
			if !exists {
				return fmt.Errorf("spoken-safety case names an unknown source")
			}
			switch source.Disposition {
			case TemporalSpokenSafetyDispositionProhibited:
				want = TemporalSpokenSafetyDispositionProhibited
				triggers = append(triggers, id)
			case TemporalSpokenSafetyDispositionCoverage:
				if want != TemporalSpokenSafetyDispositionProhibited {
					want = TemporalSpokenSafetyDispositionCoverage
				}
				triggers = append(triggers, id)
			}
		}
		if item.Disposition != want || !slices.Equal(item.TriggerSources, triggers) {
			return fmt.Errorf("spoken-safety case disposition does not reproduce")
		}
		switch want {
		case TemporalSpokenSafetyDispositionProhibited:
			caseProhibited++
		case TemporalSpokenSafetyDispositionCoverage:
			caseCoverage++
		case TemporalSpokenSafetyDispositionNoSignal:
			caseNoSignal++
		}
	}
	if caseProhibited != report.ProhibitedCases || caseCoverage != report.CoverageHoldCases || caseNoSignal != report.NoSignalObservedCases {
		return fmt.Errorf("spoken-safety case summary does not reproduce")
	}
	return nil
}

func lessTemporalSpokenSafetyMatch(first, second TemporalSpokenSafetyMatch) bool {
	if first.StartMS != second.StartMS {
		return first.StartMS < second.StartMS
	}
	if first.EndMS != second.EndMS {
		return first.EndMS < second.EndMS
	}
	if first.Class != second.Class {
		return first.Class < second.Class
	}
	return first.RuleID < second.RuleID
}
