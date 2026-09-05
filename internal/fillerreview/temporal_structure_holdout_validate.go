package fillerreview

import (
	"fmt"
	"sort"
	"strings"

	"github.com/loomarr/loomarr/internal/fillereval"
)

func validateTemporalStructureHoldoutReceipt(receipt TemporalStructureHoldoutReceipt, authoring TemporalStructureChallengeAuthoring, transition *TemporalTransitionAuthority) error {
	if receipt.SchemaVersion != TemporalStructureHoldoutSchemaVersion || receipt.ContractVersion != TemporalStructureHoldoutContractVersion || receipt.PlannedAt.IsZero() || !reviewSHA256(receipt.SeedSHA256) || !reviewSHA256(receipt.AuthoringSHA256) || receipt.Cases != TemporalStructureHoldoutCases || receipt.StandaloneCases != temporalStructureHoldoutClassCases || receipt.CompilationCases != temporalStructureHoldoutClassCases || receipt.ProgrammeExcerptCases != temporalStructureHoldoutClassCases || receipt.IndependentSources != temporalStructureHoldoutClassCases || receipt.ProgrammeParents != temporalStructureHoldoutParentSources || len(receipt.SelectedAnchors) != temporalStructureHoldoutClassCases || len(receipt.CompilationConstructions) != temporalStructureHoldoutClassCases || len(receipt.ProgrammeConstructions) != temporalStructureHoldoutClassCases || receipt.BlindHumanAuditRequired == nil || *receipt.BlindHumanAuditRequired || receipt.TrainingAllowed == nil || *receipt.TrainingAllowed || receipt.ProductionAdmissionAllowed == nil || *receipt.ProductionAdmissionAllowed || len(authoring.Cases) != receipt.Cases || len(authoring.Sources) != temporalStructureHoldoutClassCases+temporalStructureHoldoutParentSources {
		return fmt.Errorf("temporal structure holdout receipt counts or disposition are invalid")
	}
	if err := validateTemporalStructureHoldoutInputs(receipt.Inputs); err != nil {
		return err
	}
	sources, cases, unitCounts, err := indexTemporalStructureHoldoutAuthoring(authoring)
	if err != nil {
		return err
	}
	if unitCounts[fillereval.UnitStandalone] != temporalStructureHoldoutClassCases || unitCounts[fillereval.UnitCompilation] != temporalStructureHoldoutClassCases || unitCounts[fillereval.UnitProgrammeExcerpt] != temporalStructureHoldoutClassCases {
		return fmt.Errorf("temporal structure holdout authoring is not balanced by unit")
	}
	anchors, err := validateTemporalStructureHoldoutAnchors(receipt, sources)
	if err != nil {
		return err
	}
	if err := validateTemporalStructureHoldoutTrainingExclusion(receipt.FutureTrainingExclusion, anchors, sources); err != nil {
		return err
	}
	if err := validateTemporalStructureHoldoutStandaloneCases(cases, anchors); err != nil {
		return err
	}
	if err := validateTemporalStructureHoldoutCompilations(receipt.CompilationConstructions, cases, anchors, transition); err != nil {
		return err
	}
	return validateTemporalStructureHoldoutProgrammeCuts(receipt.ProgrammeConstructions, cases, sources)
}

func validateTemporalStructureHoldoutTrainingExclusion(exclusion TemporalStructureHoldoutTrainingExclusion, anchors map[string]TemporalStructureHoldoutAnchor, sources map[string]TemporalStructureChallengeSource) error {
	if exclusion.Split != "holdout" || len(exclusion.SourceSHA256) != len(sources) || len(exclusion.FamilyIDs) != len(anchors) || len(exclusion.ProgrammeProvenance) != temporalStructureHoldoutParentSources || !sort.StringsAreSorted(exclusion.SourceSHA256) || !sort.StringsAreSorted(exclusion.FamilyIDs) || !sort.SliceIsSorted(exclusion.ProgrammeProvenance, func(i, j int) bool {
		left, right := exclusion.ProgrammeProvenance[i], exclusion.ProgrammeProvenance[j]
		return left.Authority+"\x00"+left.Reference < right.Authority+"\x00"+right.Reference
	}) {
		return fmt.Errorf("temporal structure holdout future training exclusion is invalid")
	}
	wantSources, wantFamilies, wantProvenance := map[string]struct{}{}, map[string]struct{}{}, map[string]struct{}{}
	for _, source := range sources {
		wantSources[source.SHA256] = struct{}{}
		if source.Provenance.Kind == TemporalStructureSourceProgrammeParent {
			wantProvenance[source.Provenance.Authority+"\x00"+source.Provenance.Reference] = struct{}{}
		}
	}
	for _, anchor := range anchors {
		wantFamilies[anchor.FamilyID] = struct{}{}
	}
	for _, digest := range exclusion.SourceSHA256 {
		if _, exists := wantSources[digest]; !exists || !reviewSHA256(digest) {
			return fmt.Errorf("temporal structure holdout future training source exclusion drift")
		}
		delete(wantSources, digest)
	}
	for _, family := range exclusion.FamilyIDs {
		if _, exists := wantFamilies[family]; !exists {
			return fmt.Errorf("temporal structure holdout future training family exclusion drift")
		}
		delete(wantFamilies, family)
	}
	for _, provenance := range exclusion.ProgrammeProvenance {
		key := provenance.Authority + "\x00" + provenance.Reference
		if _, exists := wantProvenance[key]; !exists {
			return fmt.Errorf("temporal structure holdout future training programme exclusion drift")
		}
		delete(wantProvenance, key)
	}
	if len(wantSources) != 0 || len(wantFamilies) != 0 || len(wantProvenance) != 0 {
		return fmt.Errorf("temporal structure holdout future training exclusion is incomplete")
	}
	return nil
}

func validateTemporalStructureHoldoutInputs(inputs []TemporalStructureHoldoutInput) error {
	want := map[string]struct{}{
		"evidence_manifest": {}, "evidence_private_map": {}, "family_audit": {}, "human_assessment": {},
		"human_attestation": {}, "media_quality": {}, "programme_inventory": {}, "reference_audit": {},
		"selection": {}, "suitability": {},
		"transition_authority": {},
	}
	if len(inputs) != len(want) || !sort.SliceIsSorted(inputs, func(i, j int) bool { return inputs[i].Name < inputs[j].Name }) {
		return fmt.Errorf("temporal structure holdout receipt input authority is incomplete or unordered")
	}
	for _, input := range inputs {
		if _, exists := want[input.Name]; !exists || !reviewSHA256(input.SHA256) {
			return fmt.Errorf("temporal structure holdout receipt contains an invalid input")
		}
		delete(want, input.Name)
	}
	return nil
}

func indexTemporalStructureHoldoutAuthoring(authoring TemporalStructureChallengeAuthoring) (map[string]TemporalStructureChallengeSource, map[string]TemporalStructureChallengeCase, map[fillereval.UnitKind]int, error) {
	sources := make(map[string]TemporalStructureChallengeSource, len(authoring.Sources))
	for _, source := range authoring.Sources {
		if _, duplicate := sources[source.ID]; duplicate {
			return nil, nil, nil, fmt.Errorf("temporal structure holdout repeats source %q", source.ID)
		}
		sources[source.ID] = source
	}
	cases := make(map[string]TemporalStructureChallengeCase, len(authoring.Cases))
	unitCounts := make(map[fillereval.UnitKind]int)
	for _, item := range authoring.Cases {
		if _, duplicate := cases[item.ID]; duplicate {
			return nil, nil, nil, fmt.Errorf("temporal structure holdout repeats case %q", item.ID)
		}
		cases[item.ID] = item
		unitCounts[item.Unit]++
	}
	return sources, cases, unitCounts, nil
}

func validateTemporalStructureHoldoutAnchors(receipt TemporalStructureHoldoutReceipt, sources map[string]TemporalStructureChallengeSource) (map[string]TemporalStructureHoldoutAnchor, error) {
	anchors := make(map[string]TemporalStructureHoldoutAnchor, len(receipt.SelectedAnchors))
	seenAliases, seenCases, seenFamilies, seenRanks, seenSourceSHA := map[string]struct{}{}, map[string]struct{}{}, map[string]struct{}{}, map[string]struct{}{}, map[string]struct{}{}
	roleCounts := make(map[fillereval.TemporalRole]int)
	for _, item := range receipt.SelectedAnchors {
		source, exists := sources[item.SourceID]
		if !exists || source.Provenance.Kind != TemporalStructureSourceBoundedItem || source.DurationMS != item.DurationMS || source.StandaloneRole != item.Role || item.DurationMS <= 0 || strings.TrimSpace(item.EvidenceAlias) == "" || strings.TrimSpace(item.CaseID) == "" || strings.TrimSpace(item.FamilyID) == "" || !reviewSHA256(item.RankSHA256) {
			return nil, fmt.Errorf("temporal structure holdout contains an invalid selected anchor")
		}
		if _, duplicate := anchors[item.SourceID]; duplicate {
			return nil, fmt.Errorf("temporal structure holdout repeats an anchor source")
		}
		if _, duplicate := seenSourceSHA[source.SHA256]; duplicate {
			return nil, fmt.Errorf("temporal structure holdout repeats anchor source bytes")
		}
		seenSourceSHA[source.SHA256] = struct{}{}
		for label, seen := range map[string]map[string]struct{}{"alias": seenAliases, "case": seenCases, "family": seenFamilies, "rank": seenRanks} {
			value := map[string]string{"alias": item.EvidenceAlias, "case": item.CaseID, "family": item.FamilyID, "rank": item.RankSHA256}[label]
			if _, duplicate := seen[value]; duplicate {
				return nil, fmt.Errorf("temporal structure holdout repeats an anchor %s", label)
			}
			seen[value] = struct{}{}
		}
		anchors[item.SourceID] = item
		roleCounts[item.Role]++
	}
	for role, quota := range temporalStructureHoldoutRoleQuotas {
		if roleCounts[role] != quota || receipt.StandaloneRoleCounts[role] != quota {
			return nil, fmt.Errorf("temporal structure holdout standalone role quota drift for %s", role)
		}
	}
	if len(roleCounts) != len(temporalStructureHoldoutRoleQuotas) || len(receipt.StandaloneRoleCounts) != len(temporalStructureHoldoutRoleQuotas) {
		return nil, fmt.Errorf("temporal structure holdout contains an unexpected standalone role")
	}
	return anchors, nil
}

func validateTemporalStructureHoldoutStandaloneCases(cases map[string]TemporalStructureChallengeCase, anchors map[string]TemporalStructureHoldoutAnchor) error {
	seen := map[string]struct{}{}
	for _, item := range cases {
		if item.Unit != fillereval.UnitStandalone {
			continue
		}
		if len(item.Segments) != 1 {
			return fmt.Errorf("temporal structure holdout standalone case must have one segment")
		}
		anchor, exists := anchors[item.Segments[0].SourceID]
		if !exists || item.Role != anchor.Role || item.Segments[0].StartMS != 0 || item.Segments[0].DurationMS != anchor.DurationMS {
			return fmt.Errorf("temporal structure holdout standalone case drifts from its anchor")
		}
		seen[anchor.SourceID] = struct{}{}
	}
	if len(seen) != len(anchors) {
		return fmt.Errorf("temporal structure holdout does not use each selected anchor exactly once as standalone")
	}
	return nil
}

func validateTemporalStructureHoldoutCompilations(items []TemporalStructureHoldoutCompilation, cases map[string]TemporalStructureChallengeCase, anchors map[string]TemporalStructureHoldoutAnchor, transition *TemporalTransitionAuthority) error {
	bands, seenCases, seenPairs := map[string]int{}, map[string]struct{}{}, map[string]struct{}{}
	sameRoleByBand := map[string]int{}
	strataByBand := map[string]map[TemporalTransitionStratum]int{"early": {}, "middle": {}, "late": {}}
	usedByBand := map[string]map[string]struct{}{"early": {}, "middle": {}, "late": {}}
	transitionByAlias := map[string]TemporalTransitionAuthorityCase{}
	if transition != nil {
		transitionByAlias = make(map[string]TemporalTransitionAuthorityCase, len(transition.Cases))
		for _, item := range transition.Cases {
			transitionByAlias[item.EvidenceAlias] = item
		}
	}
	for _, item := range items {
		challenge, exists := cases[item.CaseID]
		first, firstExists := anchors[item.FirstSourceID]
		second, secondExists := anchors[item.SecondSourceID]
		pairID := item.FirstSourceID + "\x00" + item.SecondSourceID
		if !exists || challenge.Unit != fillereval.UnitCompilation || !firstExists || !secondExists || item.FirstSourceID == item.SecondSourceID || len(challenge.Segments) != 2 || item.JoinAtMS != first.DurationMS || item.DurationMS != first.DurationMS+second.DurationMS || item.JoinBand != temporalStructureHoldoutJoinBand(item.JoinAtMS, item.DurationMS) || len(item.Roles) != 2 || item.Roles[0] != string(first.Role) || item.Roles[1] != string(second.Role) || challenge.Segments[0].SourceID != item.FirstSourceID || challenge.Segments[0].DurationMS != first.DurationMS || challenge.Segments[1].SourceID != item.SecondSourceID || challenge.Segments[1].DurationMS != second.DurationMS {
			return fmt.Errorf("temporal structure holdout contains an invalid compilation construction")
		}
		if transition != nil {
			firstTransition, firstTransitionExists := transitionByAlias[first.EvidenceAlias]
			secondTransition, secondTransitionExists := transitionByAlias[second.EvidenceAlias]
			wantStratum, resolved := temporalTransitionStratum(firstTransition, secondTransition)
			if !firstTransitionExists || !secondTransitionExists || !resolved || item.TransitionStratum != wantStratum {
				return fmt.Errorf("temporal structure holdout compilation transition authority drift")
			}
		}
		if _, duplicate := seenCases[item.CaseID]; duplicate {
			return fmt.Errorf("temporal structure holdout repeats a compilation case")
		}
		if _, duplicate := seenPairs[pairID]; duplicate {
			return fmt.Errorf("temporal structure holdout repeats a compilation pair")
		}
		usedSources, validBand := usedByBand[item.JoinBand]
		if !validBand {
			return fmt.Errorf("temporal structure holdout contains an unknown compilation join band")
		}
		if _, duplicate := usedSources[item.FirstSourceID]; duplicate {
			return fmt.Errorf("temporal structure holdout compilation band reuses a source")
		}
		if _, duplicate := usedSources[item.SecondSourceID]; duplicate {
			return fmt.Errorf("temporal structure holdout compilation band reuses a source")
		}
		usedSources[item.FirstSourceID], usedSources[item.SecondSourceID] = struct{}{}, struct{}{}
		seenCases[item.CaseID], seenPairs[pairID] = struct{}{}, struct{}{}
		bands[item.JoinBand]++
		switch item.TransitionStratum {
		case TemporalTransitionBlackBoundary, TemporalTransitionAudibleNonblackCut, TemporalTransitionSilenceTouchedNonblackCut:
			strataByBand[item.JoinBand][item.TransitionStratum]++
		default:
			return fmt.Errorf("temporal structure holdout contains an unknown transition stratum")
		}
		if first.Role == second.Role {
			sameRoleByBand[item.JoinBand]++
		}
	}
	if bands["early"] != 4 || bands["middle"] != 4 || bands["late"] != 4 || len(bands) != 3 {
		return fmt.Errorf("temporal structure holdout compilation join bands are unbalanced")
	}
	if sameRoleByBand["early"] != 2 || sameRoleByBand["middle"] != 2 || sameRoleByBand["late"] != 2 {
		return fmt.Errorf("temporal structure holdout compilation roles are unbalanced")
	}
	for _, band := range []string{"early", "middle", "late"} {
		if strataByBand[band][TemporalTransitionBlackBoundary] == 0 || strataByBand[band][TemporalTransitionAudibleNonblackCut] == 0 || strataByBand[band][TemporalTransitionSilenceTouchedNonblackCut] == 0 {
			return fmt.Errorf("temporal structure holdout transition strata are incomplete in %s", band)
		}
	}
	return nil
}

func validateTemporalStructureHoldoutProgrammeCuts(items []TemporalStructureHoldoutProgrammeCut, cases map[string]TemporalStructureChallengeCase, sources map[string]TemporalStructureChallengeSource) error {
	parents, seenCases, seenPatterns := map[string]int{}, map[string]struct{}{}, map[string]struct{}{}
	patternCounts := map[string]int{}
	for _, item := range items {
		challenge, caseExists := cases[item.CaseID]
		source, sourceExists := sources[item.SourceID]
		patternID := item.SourceID + "\x00" + item.Pattern
		if !caseExists || challenge.Unit != fillereval.UnitProgrammeExcerpt || !sourceExists || source.Provenance.Kind != TemporalStructureSourceProgrammeParent || source.DurationMS != item.ParentEndMS || len(challenge.Segments) != 1 || challenge.Segments[0].SourceID != item.SourceID || challenge.Segments[0].StartMS != item.StartMS || challenge.Segments[0].DurationMS != item.DurationMS || item.StartMS < 10_000 || item.StartMS+item.DurationMS > item.ParentEndMS-10_000 {
			return fmt.Errorf("temporal structure holdout contains an invalid programme cut")
		}
		switch item.Pattern {
		case "dependent_start":
			if item.StartMS != 10_000 || item.DurationMS != 30_000 {
				return fmt.Errorf("temporal structure holdout start cut drift")
			}
		case "dependent_end":
			if item.StartMS != item.ParentEndMS-55_000 || item.DurationMS != 45_000 {
				return fmt.Errorf("temporal structure holdout end cut drift")
			}
		case "both_edges":
			if item.StartMS != (item.ParentEndMS-45_000)/2 || item.DurationMS != 45_000 {
				return fmt.Errorf("temporal structure holdout both-edge cut drift")
			}
		default:
			return fmt.Errorf("temporal structure holdout contains an unknown programme cut pattern")
		}
		if _, duplicate := seenCases[item.CaseID]; duplicate {
			return fmt.Errorf("temporal structure holdout repeats a programme case")
		}
		if _, duplicate := seenPatterns[patternID]; duplicate {
			return fmt.Errorf("temporal structure holdout repeats a programme cut pattern")
		}
		seenCases[item.CaseID], seenPatterns[patternID] = struct{}{}, struct{}{}
		parents[item.SourceID]++
		patternCounts[item.Pattern]++
	}
	if len(parents) != temporalStructureHoldoutParentSources {
		return fmt.Errorf("temporal structure holdout does not use six distinct programme parents")
	}
	for _, count := range parents {
		if count != 2 {
			return fmt.Errorf("temporal structure holdout must make two cuts per programme parent")
		}
	}
	if patternCounts["dependent_start"] != 4 || patternCounts["dependent_end"] != 4 || patternCounts["both_edges"] != 4 || len(patternCounts) != 3 {
		return fmt.Errorf("temporal structure holdout programme patterns are unbalanced")
	}
	return nil
}
