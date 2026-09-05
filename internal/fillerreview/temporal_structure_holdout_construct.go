package fillerreview

import (
	"fmt"
	"sort"

	"github.com/loomarr/loomarr/internal/fillereval"
)

func constructTemporalStructureHoldout(config TemporalStructureHoldoutConfig, loaded temporalStructureHoldoutLoaded, anchors []temporalStructureHoldoutSelectedAnchor, parents []TemporalStructureChallengeSource) (TemporalStructureChallengeAuthoring, TemporalStructureHoldoutReceipt, error) {
	if len(anchors) != temporalStructureHoldoutClassCases || len(parents) != temporalStructureHoldoutParentSources {
		return TemporalStructureChallengeAuthoring{}, TemporalStructureHoldoutReceipt{}, fmt.Errorf("temporal structure holdout selection counts are invalid")
	}
	if err := validateTemporalStructureHoldoutSourceSeparation(anchors, parents); err != nil {
		return TemporalStructureChallengeAuthoring{}, TemporalStructureHoldoutReceipt{}, err
	}
	authoring := TemporalStructureChallengeAuthoring{
		SchemaVersion: TemporalStructureChallengeSchemaVersion, ContractVersion: TemporalStructureChallengeContractVersion,
	}
	receipt := TemporalStructureHoldoutReceipt{
		SchemaVersion: TemporalStructureHoldoutSchemaVersion, ContractVersion: TemporalStructureHoldoutContractVersion,
		PlannedAt: config.PlannedAt.UTC(), PlanKind: loaded.prior.planKind,
		SeedSHA256: hashBytes([]byte(config.Seed)), Inputs: loaded.inputs,
		Cases: TemporalStructureHoldoutCases, StandaloneCases: temporalStructureHoldoutClassCases,
		CompilationCases: temporalStructureHoldoutClassCases, ProgrammeExcerptCases: temporalStructureHoldoutClassCases,
		IndependentSources: temporalStructureHoldoutClassCases, ProgrammeParents: temporalStructureHoldoutParentSources,
		StandaloneRoleCounts:    map[fillereval.TemporalRole]int{},
		PriorExposure:           cloneTemporalStructureTrainingExclusion(loaded.prior.exposure),
		FutureTrainingExclusion: cloneTemporalStructureTrainingExclusion(loaded.prior.exposure),
		TrainingAllowed:         false, ProductionAdmissionAllowed: false,
	}
	for index := range anchors {
		relative, err := temporalStructureHoldoutRelativeEvidencePath(config.SourceRoot, config.EvidenceManifestPath, anchors[index].source.Path)
		if err != nil {
			return TemporalStructureChallengeAuthoring{}, TemporalStructureHoldoutReceipt{}, err
		}
		anchors[index].source.Path = relative
		authoring.Sources = append(authoring.Sources, anchors[index].source)
		receipt.SelectedAnchors = append(receipt.SelectedAnchors, anchors[index].receipt)
		receipt.StandaloneRoleCounts[anchors[index].receipt.Role]++
		receipt.FutureTrainingExclusion.SourceSHA256 = append(receipt.FutureTrainingExclusion.SourceSHA256, anchors[index].source.SHA256)
		receipt.FutureTrainingExclusion.FamilyIDs = append(receipt.FutureTrainingExclusion.FamilyIDs, anchors[index].receipt.FamilyID)
		authoring.Cases = append(authoring.Cases, TemporalStructureChallengeCase{
			ID:   temporalStructureHoldoutCaseID(config.Seed, "standalone", anchors[index].source.ID),
			Unit: fillereval.UnitStandalone, Role: anchors[index].receipt.Role,
			Segments: []TemporalStructureChallengeSegment{{SourceID: anchors[index].source.ID, DurationMS: anchors[index].source.DurationMS}},
		})
	}
	authoring.Sources = append(authoring.Sources, parents...)
	for _, parent := range parents {
		receipt.FutureTrainingExclusion.SourceSHA256 = append(receipt.FutureTrainingExclusion.SourceSHA256, parent.SHA256)
		receipt.FutureTrainingExclusion.ProgrammeProvenance = append(receipt.FutureTrainingExclusion.ProgrammeProvenance, TemporalStructureHoldoutProgrammeProvenance{
			Authority: parent.Provenance.Authority, Reference: parent.Provenance.Reference,
		})
	}
	compilationCases, compilationReceipt, err := constructTemporalStructureHoldoutCompilations(config.Seed, anchors)
	if err != nil {
		return TemporalStructureChallengeAuthoring{}, TemporalStructureHoldoutReceipt{}, err
	}
	authoring.Cases = append(authoring.Cases, compilationCases...)
	receipt.CompilationConstructions = compilationReceipt
	programmeCases, programmeReceipt := constructTemporalStructureHoldoutProgrammeCuts(config.Seed, parents)
	authoring.Cases = append(authoring.Cases, programmeCases...)
	receipt.ProgrammeConstructions = programmeReceipt
	sort.Slice(authoring.Sources, func(i, j int) bool { return authoring.Sources[i].ID < authoring.Sources[j].ID })
	sort.Slice(authoring.Cases, func(i, j int) bool { return authoring.Cases[i].ID < authoring.Cases[j].ID })
	sort.Slice(receipt.SelectedAnchors, func(i, j int) bool { return receipt.SelectedAnchors[i].SourceID < receipt.SelectedAnchors[j].SourceID })
	sort.Slice(receipt.CompilationConstructions, func(i, j int) bool {
		return receipt.CompilationConstructions[i].CaseID < receipt.CompilationConstructions[j].CaseID
	})
	sort.Slice(receipt.ProgrammeConstructions, func(i, j int) bool {
		return receipt.ProgrammeConstructions[i].CaseID < receipt.ProgrammeConstructions[j].CaseID
	})
	sort.Strings(receipt.FutureTrainingExclusion.SourceSHA256)
	sort.Strings(receipt.FutureTrainingExclusion.FamilyIDs)
	sort.Slice(receipt.FutureTrainingExclusion.ProgrammeProvenance, func(i, j int) bool {
		left, right := receipt.FutureTrainingExclusion.ProgrammeProvenance[i], receipt.FutureTrainingExclusion.ProgrammeProvenance[j]
		return left.Authority+"\x00"+left.Reference < right.Authority+"\x00"+right.Reference
	})
	prepared, err := prepareTemporalStructureChallenge(TemporalStructureChallengeConfig{SourceRoot: config.SourceRoot, Seed: config.Seed}, authoring)
	if err != nil {
		return TemporalStructureChallengeAuthoring{}, TemporalStructureHoldoutReceipt{}, err
	}
	if len(prepared) != TemporalStructureHoldoutCases {
		return TemporalStructureChallengeAuthoring{}, TemporalStructureHoldoutReceipt{}, fmt.Errorf("temporal structure holdout authoring did not produce 36 unique blinded cases")
	}
	return authoring, receipt, nil
}

func constructTemporalStructureHoldoutCompilations(seed string, selected []temporalStructureHoldoutSelectedAnchor) ([]TemporalStructureChallengeCase, []TemporalStructureHoldoutCompilation, error) {
	pairs, err := selectTemporalStructureHoldoutCompilationPairs(seed, selected)
	if err != nil {
		return nil, nil, err
	}
	cases := make([]TemporalStructureChallengeCase, 0, len(pairs))
	receipts := make([]TemporalStructureHoldoutCompilation, 0, len(pairs))
	for _, item := range pairs {
		first, second := selected[item.first], selected[item.second]
		durationMS := first.source.DurationMS + second.source.DurationMS
		caseID := temporalStructureHoldoutCaseID(seed, "compilation", first.source.ID+"\x00"+second.source.ID)
		cases = append(cases, TemporalStructureChallengeCase{
			ID: caseID, Unit: fillereval.UnitCompilation,
			Segments: []TemporalStructureChallengeSegment{
				{SourceID: first.source.ID, DurationMS: first.source.DurationMS},
				{SourceID: second.source.ID, DurationMS: second.source.DurationMS},
			},
		})
		receipts = append(receipts, TemporalStructureHoldoutCompilation{
			CaseID: caseID, FirstSourceID: first.source.ID, SecondSourceID: second.source.ID,
			JoinBand: item.band, TransitionStratum: item.stratum, JoinAtMS: first.source.DurationMS, DurationMS: durationMS,
			Roles: []string{string(first.receipt.Role), string(second.receipt.Role)},
		})
	}
	return cases, receipts, nil
}

type temporalStructureHoldoutCompilationPair struct {
	first    int
	second   int
	band     string
	sameRole bool
	stratum  TemporalTransitionStratum
	rank     string
}

func selectTemporalStructureHoldoutCompilationPairs(seed string, anchors []temporalStructureHoldoutSelectedAnchor) ([]temporalStructureHoldoutCompilationPair, error) {
	var result []temporalStructureHoldoutCompilationPair
	for _, band := range []string{"early", "middle", "late"} {
		candidates := temporalStructureHoldoutPairCandidates(seed, anchors, band)
		chosen, ok := chooseBalancedTemporalStructureHoldoutPairs(candidates, anchors, nil, map[string]struct{}{}, map[TemporalTransitionStratum]int{}, 0, 4, 2)
		if !ok {
			return nil, fmt.Errorf("temporal structure holdout anchor durations cannot supply four source-disjoint %s joins balanced across same-role and cross-role pairs", band)
		}
		result = append(result, chosen...)
	}
	return result, nil
}

func temporalStructureHoldoutPairCandidates(seed string, anchors []temporalStructureHoldoutSelectedAnchor, band string) []temporalStructureHoldoutCompilationPair {
	var candidates []temporalStructureHoldoutCompilationPair
	for first := range anchors {
		for second := range anchors {
			if first == second || temporalStructureHoldoutJoinBand(anchors[first].source.DurationMS, anchors[first].source.DurationMS+anchors[second].source.DurationMS) != band {
				continue
			}
			identity := anchors[first].source.ID + "\x00" + anchors[second].source.ID
			stratum, resolved := temporalTransitionStratum(anchors[first].transition, anchors[second].transition)
			if !resolved {
				continue
			}
			candidates = append(candidates, temporalStructureHoldoutCompilationPair{
				first: first, second: second, band: band,
				sameRole: anchors[first].receipt.Role == anchors[second].receipt.Role,
				stratum:  stratum,
				rank:     hashBytes([]byte(seed + "\x00compilation-pair\x00" + band + "\x00" + identity)),
			})
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].rank < candidates[j].rank })
	return candidates
}

func chooseBalancedTemporalStructureHoldoutPairs(candidates []temporalStructureHoldoutCompilationPair, anchors []temporalStructureHoldoutSelectedAnchor, chosen []temporalStructureHoldoutCompilationPair, used map[string]struct{}, strata map[TemporalTransitionStratum]int, sameRoles, want, wantSameRoles int) ([]temporalStructureHoldoutCompilationPair, bool) {
	if len(chosen) == want {
		return chosen, sameRoles == wantSameRoles && strata[TemporalTransitionBlackBoundary] > 0 && strata[TemporalTransitionAudibleNonblackCut] > 0 && strata[TemporalTransitionSilenceTouchedNonblackCut] > 0
	}
	if sameRoles > wantSameRoles || sameRoles+(want-len(chosen)) < wantSameRoles {
		return nil, false
	}
	for index, candidate := range candidates {
		firstID, secondID := anchors[candidate.first].source.ID, anchors[candidate.second].source.ID
		if _, exists := used[firstID]; exists {
			continue
		}
		if _, exists := used[secondID]; exists {
			continue
		}
		used[firstID], used[secondID] = struct{}{}, struct{}{}
		strata[candidate.stratum]++
		nextSameRoles := sameRoles
		if candidate.sameRole {
			nextSameRoles++
		}
		if result, ok := chooseBalancedTemporalStructureHoldoutPairs(candidates[index+1:], anchors, append(chosen, candidate), used, strata, nextSameRoles, want, wantSameRoles); ok {
			return result, true
		}
		delete(used, firstID)
		delete(used, secondID)
		strata[candidate.stratum]--
	}
	return nil, false
}

func validateTemporalStructureHoldoutSourceSeparation(anchors []temporalStructureHoldoutSelectedAnchor, parents []TemporalStructureChallengeSource) error {
	anchorSHA := make(map[string]struct{}, len(anchors))
	anchorProvenance := make(map[string]struct{}, len(anchors))
	for _, anchor := range anchors {
		anchorSHA[anchor.source.SHA256] = struct{}{}
		anchorProvenance[anchor.source.Provenance.Authority+"\x00"+anchor.source.Provenance.Reference] = struct{}{}
	}
	for _, parent := range parents {
		if _, exists := anchorSHA[parent.SHA256]; exists {
			return fmt.Errorf("temporal structure holdout programme parent repeats bounded filler bytes")
		}
		if _, exists := anchorProvenance[parent.Provenance.Authority+"\x00"+parent.Provenance.Reference]; exists {
			return fmt.Errorf("temporal structure holdout programme parent repeats bounded filler provenance")
		}
	}
	return nil
}

func temporalStructureHoldoutJoinBand(joinAtMS, durationMS int64) string {
	switch {
	case joinAtMS*5 <= durationMS*2:
		return "early"
	case joinAtMS*5 >= durationMS*3:
		return "late"
	default:
		return "middle"
	}
}

func constructTemporalStructureHoldoutProgrammeCuts(seed string, parents []TemporalStructureChallengeSource) ([]TemporalStructureChallengeCase, []TemporalStructureHoldoutProgrammeCut) {
	var cases []TemporalStructureChallengeCase
	var receipts []TemporalStructureHoldoutProgrammeCut
	for parentIndex, parent := range parents {
		allCuts := map[string]struct {
			pattern    string
			start      int64
			durationMS int64
		}{
			"dependent_start": {pattern: "dependent_start", start: 10_000, durationMS: 30_000},
			"dependent_end":   {pattern: "dependent_end", start: parent.DurationMS - 55_000, durationMS: 45_000},
			"both_edges":      {pattern: "both_edges", start: (parent.DurationMS - 45_000) / 2, durationMS: 45_000},
		}
		patternPairs := [][2]string{{"dependent_start", "dependent_end"}, {"dependent_start", "both_edges"}, {"dependent_end", "both_edges"}}
		patterns := patternPairs[parentIndex%len(patternPairs)]
		for _, pattern := range patterns {
			cut := allCuts[pattern]
			caseID := temporalStructureHoldoutCaseID(seed, "programme_excerpt", fmt.Sprintf("%s\x00%s\x00%d\x00%d", parent.ID, cut.pattern, cut.start, cut.durationMS))
			cases = append(cases, TemporalStructureChallengeCase{
				ID: caseID, Unit: fillereval.UnitProgrammeExcerpt,
				Segments: []TemporalStructureChallengeSegment{{SourceID: parent.ID, StartMS: cut.start, DurationMS: cut.durationMS}},
			})
			receipts = append(receipts, TemporalStructureHoldoutProgrammeCut{
				CaseID: caseID, SourceID: parent.ID, Pattern: cut.pattern, StartMS: cut.start,
				DurationMS: cut.durationMS, ParentEndMS: parent.DurationMS,
			})
		}
	}
	return cases, receipts
}

func temporalStructureHoldoutCaseID(seed, kind, identity string) string {
	return "holdout-" + hashBytes([]byte(seed + "\x00" + kind + "\x00" + identity))[:24]
}
