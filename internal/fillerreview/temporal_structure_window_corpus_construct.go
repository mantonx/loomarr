package fillerreview

import (
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/loomarr/loomarr/internal/fillereval"
	"github.com/loomarr/loomarr/internal/fillerstructure"
	"github.com/loomarr/loomarr/internal/fillerstructurewindow"
)

const temporalStructureWindowCorpusProgrammeSuffixMS int64 = 60_000

type temporalStructureWindowAnchor struct {
	source  TemporalStructureChallengeSource
	receipt TemporalStructureHoldoutAnchor
}

type temporalStructureWindowPair struct {
	first, second temporalStructureWindowAnchor
	rank          string
}

func constructTemporalStructureWindowCorpusPlan(
	authoring TemporalStructureChallengeAuthoring,
	authoringSHA string,
	receipt TemporalStructureHoldoutReceipt,
	receiptSHA, seed string,
	plannedAt time.Time,
) (TemporalStructureWindowCorpusPlan, error) {
	sources := make(map[string]TemporalStructureChallengeSource, len(authoring.Sources))
	var parents []TemporalStructureChallengeSource
	for _, source := range authoring.Sources {
		sources[source.ID] = source
		if source.Provenance.Kind == TemporalStructureSourceProgrammeParent {
			parents = append(parents, source)
		}
	}
	anchors := make([]temporalStructureWindowAnchor, 0, len(receipt.SelectedAnchors))
	for _, selected := range receipt.SelectedAnchors {
		source, ok := sources[selected.SourceID]
		if !ok {
			return TemporalStructureWindowCorpusPlan{}, fmt.Errorf("window corpus anchor %q is absent from authoring", selected.SourceID)
		}
		anchors = append(anchors, temporalStructureWindowAnchor{source: source, receipt: selected})
	}
	sort.Slice(parents, func(i, j int) bool {
		left := hashBytes([]byte(seed + "\x00window-parent\x00" + parents[i].ID))
		right := hashBytes([]byte(seed + "\x00window-parent\x00" + parents[j].ID))
		return left < right
	})
	if len(parents) != temporalStructureHoldoutParentSources {
		return TemporalStructureWindowCorpusPlan{}, fmt.Errorf("window corpus requires the six locked programme parents")
	}

	patterns := []struct {
		name       string
		boundaryMS int64
	}{
		{name: TemporalStructureWindowPatternSeamOverlap, boundaryMS: fillerstructurewindow.PrimarySpanMS - 10_000},
		{name: TemporalStructureWindowPatternSeamPrimaryLeft, boundaryMS: fillerstructurewindow.PrimarySpanMS - 1_000},
		{name: TemporalStructureWindowPatternSeamPrimaryRight, boundaryMS: fillerstructurewindow.PrimarySpanMS + 1_000},
	}
	var cases []TemporalStructureWindowCorpusCase
	for _, pattern := range patterns {
		pairs, err := selectTemporalStructureWindowPairs(seed, pattern.name, pattern.boundaryMS, anchors)
		if err != nil {
			return TemporalStructureWindowCorpusPlan{}, err
		}
		for _, pair := range pairs {
			parent := parents[len(cases)%len(parents)]
			cases = append(cases, buildTemporalStructureWindowJoinCase(seed, pattern.name, pattern.boundaryMS, parent, pair))
		}
	}
	crossing := slices.Clone(anchors)
	sort.Slice(crossing, func(i, j int) bool {
		left := hashBytes([]byte(seed + "\x00window-crossing\x00" + crossing[i].source.ID))
		right := hashBytes([]byte(seed + "\x00window-crossing\x00" + crossing[j].source.ID))
		return left < right
	})
	for index, anchor := range crossing[:TemporalStructureWindowCorpusCasesPerPattern] {
		parent := parents[index%len(parents)]
		cases = append(cases, buildTemporalStructureWindowCrossingCase(seed, parent, anchor))
	}
	lower, err := selectTemporalStructureWindowEdgeAnchors(seed, TemporalStructureWindowPatternDurationLowerEdge, TemporalStructureWindowLowerEdgeDurationMS, anchors)
	if err != nil {
		return TemporalStructureWindowCorpusPlan{}, err
	}
	upper, err := selectTemporalStructureWindowEdgeAnchors(seed, TemporalStructureWindowPatternDurationUpperEdge, TemporalStructureWindowUpperEdgeDurationMS, anchors)
	if err != nil {
		return TemporalStructureWindowCorpusPlan{}, err
	}
	for index, anchor := range lower {
		cases = append(cases, buildTemporalStructureWindowDurationEdgeCase(
			seed, TemporalStructureWindowPatternDurationLowerEdge, TemporalStructureWindowLowerEdgeDurationMS,
			parents[index], anchor,
		))
	}
	for index, anchor := range upper {
		cases = append(cases, buildTemporalStructureWindowDurationEdgeCase(
			seed, TemporalStructureWindowPatternDurationUpperEdge, TemporalStructureWindowUpperEdgeDurationMS,
			parents[index+TemporalStructureWindowCorpusEdgeCases], anchor,
		))
	}
	sort.Slice(cases, func(i, j int) bool { return cases[i].ID < cases[j].ID })
	plan := TemporalStructureWindowCorpusPlan{
		SchemaVersion: TemporalStructureWindowCorpusSchemaVersion, ContractVersion: TemporalStructureWindowCorpusContractVersion,
		PlannedAt: plannedAt, HoldoutAuthoringSHA256: authoringSHA, HoldoutReceiptSHA256: receiptSHA,
		SeedSHA256: hashBytes([]byte(seed)), Cases: cases, TrainingAllowed: false, ProductionAllowed: false,
	}
	plan.SHA256 = temporalStructureWindowCorpusPlanSHA256(plan)
	return plan, nil
}

func selectTemporalStructureWindowEdgeAnchors(seed, pattern string, durationMS int64, anchors []temporalStructureWindowAnchor) ([]temporalStructureWindowAnchor, error) {
	candidates := slices.Clone(anchors)
	sort.Slice(candidates, func(i, j int) bool {
		left := hashBytes([]byte(seed + "\x00window-edge\x00" + pattern + "\x00" + candidates[i].source.ID))
		right := hashBytes([]byte(seed + "\x00window-edge\x00" + pattern + "\x00" + candidates[j].source.ID))
		return left < right
	})
	selected := make([]temporalStructureWindowAnchor, 0, TemporalStructureWindowCorpusEdgeCases)
	families := make(map[string]struct{}, TemporalStructureWindowCorpusEdgeCases)
	for _, candidate := range candidates {
		if candidate.source.DurationMS >= durationMS-20_000 {
			continue
		}
		if _, duplicate := families[candidate.receipt.FamilyID]; duplicate {
			continue
		}
		families[candidate.receipt.FamilyID] = struct{}{}
		selected = append(selected, candidate)
		if len(selected) == TemporalStructureWindowCorpusEdgeCases {
			return selected, nil
		}
	}
	return nil, fmt.Errorf("window corpus cannot construct two family-distinct %s cases at %dms", pattern, durationMS)
}

func selectTemporalStructureWindowPairs(seed, pattern string, boundaryMS int64, anchors []temporalStructureWindowAnchor) ([]temporalStructureWindowPair, error) {
	byRole := make(map[fillereval.TemporalRole][]temporalStructureWindowPair)
	var all []temporalStructureWindowPair
	for _, first := range anchors {
		if first.source.DurationMS >= boundaryMS-5_000 {
			continue
		}
		for _, second := range anchors {
			if first.source.ID == second.source.ID || first.receipt.Role != second.receipt.Role || first.receipt.FamilyID == second.receipt.FamilyID {
				continue
			}
			pair := temporalStructureWindowPair{
				first: first, second: second,
				rank: hashBytes([]byte(seed + "\x00window-pair\x00" + pattern + "\x00" + first.source.ID + "\x00" + second.source.ID)),
			}
			byRole[first.receipt.Role] = append(byRole[first.receipt.Role], pair)
			all = append(all, pair)
		}
	}
	roles := make([]fillereval.TemporalRole, 0, len(byRole))
	for role := range byRole {
		roles = append(roles, role)
	}
	sort.Slice(roles, func(i, j int) bool { return roles[i] < roles[j] })
	if len(roles) != len(temporalStructureHoldoutRoleQuotas) {
		return nil, fmt.Errorf("window corpus cannot represent every locked filler role at %s", pattern)
	}
	selected := make([]temporalStructureWindowPair, 0, TemporalStructureWindowCorpusCasesPerPattern)
	seen := make(map[string]struct{})
	for _, role := range roles {
		candidates := byRole[role]
		sort.Slice(candidates, func(i, j int) bool { return candidates[i].rank < candidates[j].rank })
		if len(candidates) == 0 {
			continue
		}
		selected = append(selected, candidates[0])
		seen[temporalStructureWindowPairID(candidates[0])] = struct{}{}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].rank < all[j].rank })
	for _, candidate := range all {
		if len(selected) == TemporalStructureWindowCorpusCasesPerPattern {
			break
		}
		id := temporalStructureWindowPairID(candidate)
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		selected = append(selected, candidate)
	}
	if len(selected) != TemporalStructureWindowCorpusCasesPerPattern {
		return nil, fmt.Errorf("window corpus cannot construct six independently paired %s joins", pattern)
	}
	return selected, nil
}

func buildTemporalStructureWindowJoinCase(seed, pattern string, boundaryMS int64, parent TemporalStructureChallengeSource, pair temporalStructureWindowPair) TemporalStructureWindowCorpusCase {
	prefixMS := boundaryMS - pair.first.source.DurationMS
	prefixStartMS := temporalStructureWindowProgrammePrefixStart(parent)
	endStartMS := parent.DurationMS - temporalStructureWindowCorpusProgrammeSuffixMS - 10_000
	segments := []TemporalStructureChallengeSegment{
		{SourceID: parent.ID, StartMS: prefixStartMS, DurationMS: prefixMS},
		{SourceID: pair.first.source.ID, DurationMS: pair.first.source.DurationMS},
		{SourceID: pair.second.source.ID, DurationMS: pair.second.source.DurationMS},
		{SourceID: parent.ID, StartMS: endStartMS, DurationMS: temporalStructureWindowCorpusProgrammeSuffixMS},
	}
	truth := temporalStructureWindowTruth(segments, map[string]fillerstructure.Role{
		parent.ID:             fillerstructure.RoleProgrammeFragment,
		pair.first.source.ID:  fillerstructure.Role(pair.first.receipt.Role),
		pair.second.source.ID: fillerstructure.Role(pair.second.receipt.Role),
	})
	identity := pattern + "\x00" + parent.ID + "\x00" + pair.first.source.ID + "\x00" + pair.second.source.ID
	return TemporalStructureWindowCorpusCase{
		ID: "window-" + hashBytes([]byte(seed + "\x00" + identity))[:24], Pattern: pattern,
		TargetSeamMS: fillerstructurewindow.PrimarySpanMS, TargetBoundaryMS: boundaryMS,
		DurationMS:      truth[len(truth)-1].EndMS,
		FillerFamilyIDs: []string{pair.first.receipt.FamilyID, pair.second.receipt.FamilyID},
		Segments:        segments, Truth: truth,
	}
}

func buildTemporalStructureWindowCrossingCase(seed string, parent TemporalStructureChallengeSource, anchor temporalStructureWindowAnchor) TemporalStructureWindowCorpusCase {
	prefixMS := fillerstructurewindow.PrimarySpanMS - anchor.source.DurationMS/2
	prefixStartMS := temporalStructureWindowProgrammePrefixStart(parent)
	endStartMS := parent.DurationMS - temporalStructureWindowCorpusProgrammeSuffixMS - 10_000
	segments := []TemporalStructureChallengeSegment{
		{SourceID: parent.ID, StartMS: prefixStartMS, DurationMS: prefixMS},
		{SourceID: anchor.source.ID, DurationMS: anchor.source.DurationMS},
		{SourceID: parent.ID, StartMS: endStartMS, DurationMS: temporalStructureWindowCorpusProgrammeSuffixMS},
	}
	truth := temporalStructureWindowTruth(segments, map[string]fillerstructure.Role{
		parent.ID:        fillerstructure.RoleProgrammeFragment,
		anchor.source.ID: fillerstructure.Role(anchor.receipt.Role),
	})
	identity := TemporalStructureWindowPatternCrossingSeam + "\x00" + parent.ID + "\x00" + anchor.source.ID
	return TemporalStructureWindowCorpusCase{
		ID: "window-" + hashBytes([]byte(seed + "\x00" + identity))[:24], Pattern: TemporalStructureWindowPatternCrossingSeam,
		TargetSeamMS: fillerstructurewindow.PrimarySpanMS, DurationMS: truth[len(truth)-1].EndMS,
		FillerFamilyIDs: []string{anchor.receipt.FamilyID}, Segments: segments, Truth: truth,
	}
}

func buildTemporalStructureWindowDurationEdgeCase(seed, pattern string, durationMS int64, parent TemporalStructureChallengeSource, anchor temporalStructureWindowAnchor) TemporalStructureWindowCorpusCase {
	programmeMS := durationMS - anchor.source.DurationMS
	prefixMS := programmeMS / 2
	suffixMS := programmeMS - prefixMS
	prefixStartMS := temporalStructureWindowProgrammePrefixStart(parent)
	endStartMS := temporalStructureWindowProgrammeEdgeSuffixStart(parent)
	segments := []TemporalStructureChallengeSegment{
		{SourceID: parent.ID, StartMS: prefixStartMS, DurationMS: prefixMS},
		{SourceID: anchor.source.ID, DurationMS: anchor.source.DurationMS},
		{SourceID: parent.ID, StartMS: endStartMS, DurationMS: suffixMS},
	}
	truth := temporalStructureWindowTruth(segments, map[string]fillerstructure.Role{
		parent.ID: fillerstructure.RoleProgrammeFragment, anchor.source.ID: fillerstructure.Role(anchor.receipt.Role),
	})
	identity := pattern + "\x00" + parent.ID + "\x00" + anchor.source.ID
	return TemporalStructureWindowCorpusCase{
		ID: "window-" + hashBytes([]byte(seed + "\x00" + identity))[:24], Pattern: pattern,
		TargetSeamMS: fillerstructurewindow.PrimarySpanMS, DurationMS: truth[len(truth)-1].EndMS,
		FillerFamilyIDs: []string{anchor.receipt.FamilyID}, Segments: segments, Truth: truth,
	}
}

func temporalStructureWindowProgrammePrefixStart(parent TemporalStructureChallengeSource) int64 {
	return parent.DurationMS / 3
}

func temporalStructureWindowProgrammeEdgeSuffixStart(parent TemporalStructureChallengeSource) int64 {
	return parent.DurationMS * 2 / 3
}

func temporalStructureWindowTruth(segments []TemporalStructureChallengeSegment, roles map[string]fillerstructure.Role) []fillerstructure.Segment {
	truth := make([]fillerstructure.Segment, len(segments))
	var startMS int64
	for index, segment := range segments {
		truth[index] = fillerstructure.Segment{StartMS: startMS, EndMS: startMS + segment.DurationMS, Role: roles[segment.SourceID]}
		startMS = truth[index].EndMS
	}
	return truth
}

func temporalStructureWindowPairID(pair temporalStructureWindowPair) string {
	return pair.first.source.ID + "\x00" + pair.second.source.ID
}
