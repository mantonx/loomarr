package fillerreview

import (
	"fmt"
	"reflect"
	"sort"
	"time"
)

type temporalSuitabilityProjectionSourceState struct {
	part         TemporalStructureChallengeAuthorityPart
	aliases      map[string]struct{}
	disposition  string
	observations []TemporalSuitabilityProjectedObservation
}

func buildTemporalSuitabilityProjection(loaded temporalSuitabilityProjectionLoaded, projectedAt time.Time) (TemporalSuitabilityProjectionReport, error) {
	firstByAlias := suitabilityAssessmentIndex(loaded.first.Assessments)
	secondByAlias := suitabilityAssessmentIndex(loaded.second.Assessments)
	comparisonByAlias := make(map[string]TemporalSuitabilityCaseComparison, len(loaded.comparison.CaseComparisons))
	for _, item := range loaded.comparison.CaseComparisons {
		comparisonByAlias[item.EvidenceAlias] = item
	}

	sources := make(map[string]*temporalSuitabilityProjectionSourceState)
	caseSources := make(map[string][]string, len(loaded.authority.Cases))
	for _, item := range loaded.authority.Cases {
		first, firstExists := firstByAlias[item.Alias]
		second, secondExists := secondByAlias[item.Alias]
		_, comparisonExists := comparisonByAlias[item.Alias]
		if !firstExists || !secondExists || !comparisonExists {
			return TemporalSuitabilityProjectionReport{}, fmt.Errorf("suitability projection case %q lacks complete authority", item.Alias)
		}
		baseDisposition := temporalSuitabilityCoverageDisposition(first, second)
		uniqueSourceIDs := make(map[string]struct{}, len(item.Segments))
		for _, part := range item.Segments {
			state, exists := sources[part.SourceID]
			if !exists {
				state = &temporalSuitabilityProjectionSourceState{part: part, aliases: map[string]struct{}{}, disposition: TemporalSuitabilityDispositionCandidate}
				sources[part.SourceID] = state
			} else if !sameTemporalSuitabilityProjectionSource(state.part, part) {
				return TemporalSuitabilityProjectionReport{}, fmt.Errorf("suitability projection source %q has ambiguous authority", part.SourceID)
			}
			state.aliases[item.Alias] = struct{}{}
			state.disposition = strongerTemporalSuitabilityDisposition(state.disposition, baseDisposition)
			uniqueSourceIDs[part.SourceID] = struct{}{}
		}
		caseSources[item.Alias] = sortedStringSet(uniqueSourceIDs)
		for _, assessed := range []struct {
			id    string
			flags []TemporalSuitabilityObservation
		}{{loaded.first.Assessor.ID, first.Flags}, {loaded.second.Assessor.ID, second.Flags}} {
			for _, observation := range assessed.flags {
				projected, err := projectTemporalSuitabilityObservation(item, observation, assessed.id)
				if err != nil {
					return TemporalSuitabilityProjectionReport{}, fmt.Errorf("project suitability case %q: %w", item.Alias, err)
				}
				for sourceID, pieces := range projected {
					state := sources[sourceID]
					state.disposition = TemporalSuitabilityDispositionProhibited
					state.observations = append(state.observations, pieces...)
				}
			}
		}
	}

	report := TemporalSuitabilityProjectionReport{
		SchemaVersion: TemporalSuitabilityProjectionSchemaVersion, ContractVersion: TemporalSuitabilityProjectionContractVersion,
		ProjectedAt: projectedAt, PublicManifestSHA256: loaded.manifestSHA, StructureAuthoritySHA256: loaded.authoritySHA,
		SuitabilityComparisonSHA256: loaded.comparisonSHA, FirstResultSHA256: loaded.firstSHA, SecondResultSHA256: loaded.secondSHA,
		FirstAssessor: loaded.first.Assessor, SecondAssessor: loaded.second.Assessor,
		Cases: len(loaded.authority.Cases), Sources: len(sources), NextAction: "certify_suitability_recall_before_admission",
	}
	sourceIDs := make([]string, 0, len(sources))
	for sourceID := range sources {
		sourceIDs = append(sourceIDs, sourceID)
	}
	sort.Strings(sourceIDs)
	for _, sourceID := range sourceIDs {
		state := sources[sourceID]
		item := TemporalSuitabilitySourceDisposition{
			SourceID: sourceID, SourceSHA256: state.part.SourceSHA256, SourceDurationMS: state.part.SourceDurationMS,
			Provenance: state.part.Provenance, DerivedAliases: sortedStringSet(state.aliases), Disposition: state.disposition,
			Observations: mergeTemporalSuitabilityProjectedObservations(state.observations),
		}
		report.SourceDispositions = append(report.SourceDispositions, item)
		incrementTemporalSuitabilitySourceSummary(&report, item.Disposition)
	}

	authorityCases := append([]TemporalStructureChallengeAuthorityCase(nil), loaded.authority.Cases...)
	sort.Slice(authorityCases, func(i, j int) bool { return authorityCases[i].Alias < authorityCases[j].Alias })
	for _, item := range authorityCases {
		comparison := comparisonByAlias[item.Alias]
		projected := TemporalSuitabilityProjectedCase{
			EvidenceAlias: item.Alias, SourceIDs: caseSources[item.Alias], InputDisposition: comparison.Disposition,
			EffectiveDisposition: comparison.Disposition,
		}
		for _, sourceID := range projected.SourceIDs {
			if sources[sourceID].disposition == TemporalSuitabilityDispositionProhibited {
				projected.TriggerSourceIDs = append(projected.TriggerSourceIDs, sourceID)
			}
		}
		if len(projected.TriggerSourceIDs) > 0 {
			projected.EffectiveDisposition = TemporalSuitabilityDispositionProhibited
		}
		if projected.InputDisposition == TemporalSuitabilityDispositionProhibited && len(projected.TriggerSourceIDs) == 0 {
			return TemporalSuitabilityProjectionReport{}, fmt.Errorf("suitability projection case %q has an unmapped prohibited observation", item.Alias)
		}
		report.CaseDispositions = append(report.CaseDispositions, projected)
		incrementTemporalSuitabilityCaseSummary(&report, projected.EffectiveDisposition)
	}
	return report, nil
}

func sameTemporalSuitabilityProjectionSource(first, second TemporalStructureChallengeAuthorityPart) bool {
	return first.SourceID == second.SourceID && first.SourcePath == second.SourcePath && first.SourceSHA256 == second.SourceSHA256 && first.SourceDurationMS == second.SourceDurationMS && first.SourceRole == second.SourceRole && reflect.DeepEqual(first.Provenance, second.Provenance)
}

func temporalSuitabilityCoverageDisposition(first, second TemporalSuitabilityAssessment) string {
	if first.OperationalFailure != nil || second.OperationalFailure != nil {
		return TemporalSuitabilityDispositionOperational
	}
	if first.VisualAssessment != suitabilityVisualCompleted || first.SpokenLanguageAssessment != suitabilityLanguageCompleted || second.VisualAssessment != suitabilityVisualCompleted || second.SpokenLanguageAssessment != suitabilityLanguageCompleted {
		return TemporalSuitabilityDispositionCoverage
	}
	return TemporalSuitabilityDispositionCandidate
}

func projectTemporalSuitabilityObservation(item TemporalStructureChallengeAuthorityCase, observation TemporalSuitabilityObservation, assessorID string) (map[string][]TemporalSuitabilityProjectedObservation, error) {
	result := make(map[string][]TemporalSuitabilityProjectedObservation)
	for _, part := range item.Segments {
		start := max(observation.StartMS, part.OutputStartMS)
		end := min(observation.EndMS, part.OutputEndMS)
		if start >= end {
			continue
		}
		drift := absoluteInt64(part.RequestedMS - part.RenderedMS)
		relativeStart := max(int64(0), start-part.OutputStartMS-drift)
		relativeEnd := min(part.RequestedMS, end-part.OutputStartMS+drift)
		sourceStart := part.SourceStartMS + relativeStart
		sourceEnd := part.SourceStartMS + relativeEnd
		if sourceStart < 0 || sourceEnd <= sourceStart || sourceEnd > part.SourceDurationMS {
			return nil, fmt.Errorf("observation maps outside source %q", part.SourceID)
		}
		result[part.SourceID] = append(result[part.SourceID], TemporalSuitabilityProjectedObservation{
			Kind: observation.Kind, Modality: observation.Modality, StartMS: sourceStart, EndMS: sourceEnd,
			Witnesses: []TemporalSuitabilityProjectionWitness{{
				EvidenceAlias: item.Alias, AssessorID: assessorID, CaseStartMS: observation.StartMS, CaseEndMS: observation.EndMS,
				SourceStartMS: sourceStart, SourceEndMS: sourceEnd,
			}},
		})
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("observation %d-%dms overlaps no construction segment", observation.StartMS, observation.EndMS)
	}
	return result, nil
}

func mergeTemporalSuitabilityProjectedObservations(items []TemporalSuitabilityProjectedObservation) []TemporalSuitabilityProjectedObservation {
	sort.Slice(items, func(i, j int) bool { return lessTemporalSuitabilityProjectedObservation(items[i], items[j]) })
	result := make([]TemporalSuitabilityProjectedObservation, 0, len(items))
	for _, item := range items {
		last := len(result) - 1
		if last >= 0 && result[last].Kind == item.Kind && result[last].Modality == item.Modality && item.StartMS < result[last].EndMS {
			result[last].StartMS = min(result[last].StartMS, item.StartMS)
			result[last].EndMS = max(result[last].EndMS, item.EndMS)
			result[last].Witnesses = append(result[last].Witnesses, item.Witnesses...)
			continue
		}
		result = append(result, item)
	}
	for index := range result {
		sort.Slice(result[index].Witnesses, func(i, j int) bool {
			return lessTemporalSuitabilityProjectionWitness(result[index].Witnesses[i], result[index].Witnesses[j])
		})
	}
	return result
}

func lessTemporalSuitabilityProjectedObservation(first, second TemporalSuitabilityProjectedObservation) bool {
	if first.Kind != second.Kind {
		return first.Kind < second.Kind
	}
	if first.Modality != second.Modality {
		return first.Modality < second.Modality
	}
	if first.StartMS != second.StartMS {
		return first.StartMS < second.StartMS
	}
	return first.EndMS < second.EndMS
}

func lessTemporalSuitabilityProjectionWitness(first, second TemporalSuitabilityProjectionWitness) bool {
	if first.EvidenceAlias != second.EvidenceAlias {
		return first.EvidenceAlias < second.EvidenceAlias
	}
	if first.AssessorID != second.AssessorID {
		return first.AssessorID < second.AssessorID
	}
	if first.CaseStartMS != second.CaseStartMS {
		return first.CaseStartMS < second.CaseStartMS
	}
	if first.CaseEndMS != second.CaseEndMS {
		return first.CaseEndMS < second.CaseEndMS
	}
	if first.SourceStartMS != second.SourceStartMS {
		return first.SourceStartMS < second.SourceStartMS
	}
	return first.SourceEndMS < second.SourceEndMS
}

func strongerTemporalSuitabilityDisposition(first, second string) string {
	strength := map[string]int{
		TemporalSuitabilityDispositionCandidate: 0, TemporalSuitabilityDispositionCoverage: 1,
		TemporalSuitabilityDispositionOperational: 2, TemporalSuitabilityDispositionProhibited: 3,
	}
	if strength[second] > strength[first] {
		return second
	}
	return first
}

func sortedStringSet(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func incrementTemporalSuitabilitySourceSummary(report *TemporalSuitabilityProjectionReport, disposition string) {
	switch disposition {
	case TemporalSuitabilityDispositionProhibited:
		report.ProhibitedSources++
	case TemporalSuitabilityDispositionOperational:
		report.OperationalHoldSources++
	case TemporalSuitabilityDispositionCoverage:
		report.CoverageHoldSources++
	case TemporalSuitabilityDispositionCandidate:
		report.CandidateNoSignalSources++
	}
}

func incrementTemporalSuitabilityCaseSummary(report *TemporalSuitabilityProjectionReport, disposition string) {
	switch disposition {
	case TemporalSuitabilityDispositionProhibited:
		report.ProhibitedCases++
	case TemporalSuitabilityDispositionOperational:
		report.OperationalHoldCases++
	case TemporalSuitabilityDispositionCoverage:
		report.CoverageHoldCases++
	case TemporalSuitabilityDispositionCandidate:
		report.CandidateNoSignalCases++
	}
}
