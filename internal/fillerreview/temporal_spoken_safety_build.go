package fillerreview

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/fillerbakeoff"
)

type temporalSpokenSafetySourceState struct {
	result             TemporalSpokenSafetySourceDisposition
	caseID             string
	projectionVideoSHA string
	segments           []fillerbakeoff.TranscriptSegment
	derivatives        map[string]struct{}
}

func buildTemporalSpokenSafety(loaded temporalSpokenSafetyLoaded, projectedAt time.Time) (TemporalSpokenSafetyReport, error) {
	states := make(map[string]*temporalSpokenSafetySourceState, len(loaded.corpusCases))
	byCaseID := make(map[string]*temporalSpokenSafetySourceState, len(loaded.corpusCases))
	for caseID, item := range loaded.corpusCases {
		packet := loaded.packets[caseID]
		id := temporalSpokenSafetySourceID("corpus", caseID)
		state := &temporalSpokenSafetySourceState{
			caseID: caseID, derivatives: map[string]struct{}{},
			result: TemporalSpokenSafetySourceDisposition{
				SourceID: id, AuthorityKind: TemporalSpokenSafetySourceCorpus,
				SourceSHA256: item.ContentSHA256, PacketSHA256: fillerbakeoff.PacketSHA256(packet),
				SourceDurationMS: item.Provenance.SegmentDurationMS,
			},
		}
		video, _, err := temporalSpokenSafetyPacketSignals(packet)
		if err != nil {
			return TemporalSpokenSafetyReport{}, err
		}
		if video == nil {
			state.result.CoverageReason = temporalSpokenSafetyCoverageMissingSourceMedia
		}
		if artifact, exists := loaded.transcripts[caseID]; exists {
			state.segments = artifact.Segments
			state.result.TranscriptSHA256 = temporalTruthJSONSHA(artifact)
			state.result.AudioSHA256 = artifact.AudioSHA256
			state.result.AudioDurationMS = artifact.AudioDurationMS
		} else if state.result.CoverageReason == "" {
			state.result.CoverageReason = temporalSpokenSafetyCoverageMissingTranscript
		}
		if _, duplicate := states[id]; duplicate {
			return TemporalSpokenSafetyReport{}, fmt.Errorf("spoken-safety source identity collision")
		}
		states[id], byCaseID[caseID] = state, state
	}
	evidenceByAlias := make(map[string]TemporalTruthEvidenceCase, len(loaded.evidence.Cases))
	for _, item := range loaded.evidence.Cases {
		evidenceByAlias[item.Alias] = item
	}
	for _, mapping := range loaded.privateMap.Entries {
		state := byCaseID[mapping.CaseID]
		if state == nil || state.result.EvidenceAlias != "" {
			return TemporalSpokenSafetyReport{}, fmt.Errorf("spoken-safety evidence projection is ambiguous")
		}
		state.result.EvidenceAlias = mapping.Alias
		state.projectionVideoSHA = evidenceByAlias[mapping.Alias].Video.SHA256
	}

	caseSources := make(map[string]map[string]struct{}, len(loaded.structure.Cases))
	structureSources := map[string]*temporalSpokenSafetySourceState{}
	for _, item := range loaded.structure.Cases {
		caseSet := map[string]struct{}{}
		for _, part := range item.Segments {
			state, exists := structureSources[part.SourceID]
			if !exists {
				switch part.Provenance.Kind {
				case TemporalStructureSourceBoundedItem:
					state = byCaseID[part.Provenance.Reference]
					if state == nil || state.projectionVideoSHA != part.SourceSHA256 || absoluteInt64(state.result.SourceDurationMS-part.SourceDurationMS) > temporalSpokenSafetyDurationToleranceMS {
						return TemporalSpokenSafetyReport{}, fmt.Errorf("spoken-safety bounded structure source does not bind transcript evidence")
					}
				case TemporalStructureSourceProgrammeParent:
					id := temporalSpokenSafetySourceID("structure", part.SourceID)
					state = &temporalSpokenSafetySourceState{
						derivatives: map[string]struct{}{},
						result: TemporalSpokenSafetySourceDisposition{
							SourceID: id, AuthorityKind: TemporalSpokenSafetySourceConstruction,
							SourceSHA256: part.SourceSHA256, SourceDurationMS: part.SourceDurationMS,
							CoverageReason: temporalSpokenSafetyCoverageMissingTranscript,
						},
					}
					if _, collision := states[id]; collision {
						return TemporalSpokenSafetyReport{}, fmt.Errorf("spoken-safety structure source identity collision")
					}
					states[id] = state
				default:
					return TemporalSpokenSafetyReport{}, fmt.Errorf("spoken-safety structure source has unknown provenance")
				}
				structureSources[part.SourceID] = state
			} else if (state.projectionVideoSHA != "" && state.projectionVideoSHA != part.SourceSHA256) ||
				(state.projectionVideoSHA == "" && state.result.SourceSHA256 != part.SourceSHA256) ||
				absoluteInt64(state.result.SourceDurationMS-part.SourceDurationMS) > temporalSpokenSafetyDurationToleranceMS {
				return TemporalSpokenSafetyReport{}, fmt.Errorf("spoken-safety structure source authority is ambiguous")
			}
			state.derivatives[item.Alias] = struct{}{}
			caseSet[state.result.SourceID] = struct{}{}
		}
		caseSources[item.Alias] = caseSet
	}

	report := TemporalSpokenSafetyReport{
		SchemaVersion: TemporalSpokenSafetySchemaVersion, ContractVersion: TemporalSpokenSafetyContractVersion,
		ProjectedAt: projectedAt, CorpusManifestSHA256: loaded.corpusSHA, PacketsSHA256: loaded.packetsSHA,
		EvidenceManifestSHA256: loaded.evidenceSHA, EvidencePrivateMapSHA256: loaded.privateMapSHA,
		TranscriptSetSHA256: loaded.transcriptSetSHA, TranscriptFileSHA256: loaded.transcriptFileSHA,
		StructureManifestSHA256: loaded.structureSHA, StructureAuthoritySHA256: loaded.authoritySHA,
		PolicySHA256: loaded.policySHA, PolicyID: loaded.policy.PolicyID, Engine: loaded.engine,
		CorpusSources: len(loaded.corpusCases), AdditionalStructureSources: len(states) - len(loaded.corpusCases),
		Sources: len(states), StructureCases: len(loaded.structure.Cases), CertificationStatus: temporalSpokenSafetyCertificationNotRun,
		NextAction: "run_source_disjoint_spoken_safety_certification_before_admission",
	}
	sourceIDs := make([]string, 0, len(states))
	for id := range states {
		sourceIDs = append(sourceIDs, id)
	}
	sort.Strings(sourceIDs)
	for _, id := range sourceIDs {
		state := states[id]
		state.result.DerivedAliases = sortedTemporalSpokenSafetyStrings(state.derivatives)
		if state.result.TranscriptSHA256 == "" {
			state.result.Disposition = TemporalSpokenSafetyDispositionCoverage
		} else {
			report.CompleteTranscriptSources++
			state.result.Matches = matchTemporalSpokenSafety(loaded.policy, state.segments)
			state.result.Disposition = temporalSpokenSafetyDispositionForMatches(state.result.Matches)
		}
		report.SourceDispositions = append(report.SourceDispositions, state.result)
		switch state.result.Disposition {
		case TemporalSpokenSafetyDispositionProhibited:
			report.ProhibitedSources++
		case TemporalSpokenSafetyDispositionCoverage:
			report.CoverageHoldSources++
		case TemporalSpokenSafetyDispositionNoSignal:
			report.NoSignalObservedSources++
		}
	}

	dispositionBySource := make(map[string]string, len(report.SourceDispositions))
	for _, source := range report.SourceDispositions {
		dispositionBySource[source.SourceID] = source.Disposition
	}
	aliases := make([]string, 0, len(caseSources))
	for alias := range caseSources {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	for _, alias := range aliases {
		sourceIDs := sortedTemporalSpokenSafetyStrings(caseSources[alias])
		item := TemporalSpokenSafetyCaseDisposition{EvidenceAlias: alias, SourceIDs: sourceIDs, Disposition: TemporalSpokenSafetyDispositionNoSignal}
		for _, id := range sourceIDs {
			switch dispositionBySource[id] {
			case TemporalSpokenSafetyDispositionProhibited:
				item.Disposition = TemporalSpokenSafetyDispositionProhibited
				item.TriggerSources = append(item.TriggerSources, id)
			case TemporalSpokenSafetyDispositionCoverage:
				if item.Disposition != TemporalSpokenSafetyDispositionProhibited {
					item.Disposition = TemporalSpokenSafetyDispositionCoverage
				}
				item.TriggerSources = append(item.TriggerSources, id)
			}
		}
		sort.Strings(item.TriggerSources)
		report.CaseDispositions = append(report.CaseDispositions, item)
		switch item.Disposition {
		case TemporalSpokenSafetyDispositionProhibited:
			report.ProhibitedCases++
		case TemporalSpokenSafetyDispositionCoverage:
			report.CoverageHoldCases++
		case TemporalSpokenSafetyDispositionNoSignal:
			report.NoSignalObservedCases++
		}
	}
	return report, nil
}

func temporalSpokenSafetySourceID(kind, authority string) string {
	return "source-" + hashBytes([]byte("spoken-safety\x00" + kind + "\x00" + authority))[:24]
}

func temporalSpokenSafetyDispositionForMatches(matches []TemporalSpokenSafetyMatch) string {
	disposition := TemporalSpokenSafetyDispositionNoSignal
	for _, match := range matches {
		if match.Class == TemporalSpokenSafetyMatchProhibited {
			return TemporalSpokenSafetyDispositionProhibited
		}
		if match.Class == TemporalSpokenSafetyMatchAmbiguous {
			disposition = TemporalSpokenSafetyDispositionCoverage
		}
	}
	return disposition
}

func validTemporalSpokenSafetySourceID(value string) bool {
	if len(value) != len("source-")+24 || !strings.HasPrefix(value, "source-") {
		return false
	}
	for _, r := range value[len("source-"):] {
		if r < '0' || r > '9' && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}
