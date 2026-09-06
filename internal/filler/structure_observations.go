package filler

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

func normalizeStructureObservations(in []StructureObservation, durationMs int64) ([]StructureObservation, error) {
	out := slices.Clone(in)
	sort.Slice(out, func(i, j int) bool {
		if out[i].StartMs != out[j].StartMs {
			return out[i].StartMs < out[j].StartMs
		}
		return out[i].ID < out[j].ID
	})
	seen := make(map[string]struct{}, len(out))
	for i := range out {
		o := &out[i]
		if o.RoleEvidence != nil {
			roleEvidence := *o.RoleEvidence
			roleEvidence.FrameSHA256 = slices.Clone(roleEvidence.FrameSHA256)
			roleEvidence.Modalities = slices.Clone(roleEvidence.Modalities)
			roleEvidence.Charge = cloneStructureRoleCharge(roleEvidence.Charge)
			o.RoleEvidence = &roleEvidence
		}
		o.ID, o.Producer = strings.TrimSpace(o.ID), strings.TrimSpace(o.Producer)
		if o.ID == "" || o.Producer == "" || !isContentHash(o.EvidenceSHA256) || !validStructureObservationKind(o.Kind) || !validStructureObservationEffect(o.Effect) || o.StartMs < 0 || o.EndMs < o.StartMs || o.EndMs > durationMs {
			return nil, fmt.Errorf("source structure: invalid observation %q", o.ID)
		}
		if o.Kind == ObservationSceneChange || o.Kind == ObservationStandardDuration {
			if o.Effect != ObservationContextOnly {
				return nil, fmt.Errorf("source structure: %s may only be context", o.Kind)
			}
		}
		if o.Kind == ObservationSegmentRole {
			if o.Effect != ObservationContextOnly || o.RoleEvidence == nil || ValidateStructureRoleEvidence(*o.RoleEvidence) != nil || o.StartMs != o.RoleEvidence.StartMs || o.EndMs != o.RoleEvidence.EndMs || o.EvidenceSHA256 != o.RoleEvidence.SHA256 {
				return nil, fmt.Errorf("source structure: segment role observation %q is invalid", o.ID)
			}
		} else if o.RoleEvidence != nil {
			return nil, fmt.Errorf("source structure: non-role observation %q carries role evidence", o.ID)
		}
		if _, duplicate := seen[o.ID]; duplicate {
			return nil, fmt.Errorf("source structure: duplicate observation %q", o.ID)
		}
		seen[o.ID] = struct{}{}
	}
	return out, nil
}

func fuseStructureBoundaries(observations []StructureObservation, durationMs int64) []StructureBoundary {
	var candidates []StructureBoundary
	for _, o := range observations {
		if o.Effect != ObservationProposesBoundary && o.Effect != ObservationSupportsBoundary {
			continue
		}
		if o.StartMs <= 0 || o.EndMs >= durationMs {
			continue
		}
		matched := -1
		for i := range candidates {
			if windowsOverlap(candidates[i].WindowStartMs, candidates[i].WindowEndMs, o.StartMs, o.EndMs) {
				matched = i
				break
			}
		}
		if matched < 0 {
			candidates = append(candidates, StructureBoundary{WindowStartMs: o.StartMs, WindowEndMs: o.EndMs})
			matched = len(candidates) - 1
		} else {
			candidates[matched].WindowStartMs = max(candidates[matched].WindowStartMs, o.StartMs)
			candidates[matched].WindowEndMs = min(candidates[matched].WindowEndMs, o.EndMs)
		}
		candidates[matched].ObservationIDs = append(candidates[matched].ObservationIDs, o.ID)
	}
	byID := make(map[string]StructureObservation, len(observations))
	for _, o := range observations {
		byID[o.ID] = o
	}
	for i := range candidates {
		c := &candidates[i]
		c.AtMs = (c.WindowStartMs + c.WindowEndMs) / 2
		hasChapter, hasBlack, hasSilence, hasCompleteDecision := false, false, false, false
		for _, id := range c.ObservationIDs {
			switch byID[id].Kind {
			case ObservationChapterEdge:
				hasChapter = true
				c.AtMs = byID[id].StartMs
			case ObservationBlackInterval:
				hasBlack = true
			case ObservationSilenceInterval:
				hasSilence = true
			case ObservationCompleteTimelineDecision:
				hasCompleteDecision = true
			}
		}
		for _, o := range observations {
			if o.Effect == ObservationContradictsBoundary && o.StartMs <= c.AtMs && c.AtMs <= o.EndMs {
				c.ConflictIDs = append(c.ConflictIDs, o.ID)
			}
		}
		slices.Sort(c.ObservationIDs)
		slices.Sort(c.ConflictIDs)
		switch {
		case len(c.ConflictIDs) > 0:
			c.Status = BoundaryConflicted
		case hasCompleteDecision || hasChapter || hasBlack && hasSilence:
			c.Status = BoundaryResolved
		default:
			c.Status = BoundaryUnresolved
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].AtMs < candidates[j].AtMs })
	return candidates
}

func windowsOverlap(aStart, aEnd, bStart, bEnd int64) bool {
	return aStart <= bEnd && bStart <= aEnd
}

func validStructureObservationKind(kind StructureObservationKind) bool {
	switch kind {
	case ObservationChapterEdge, ObservationBlackInterval, ObservationSilenceInterval, ObservationTranscriptChange, ObservationOCRLogoChange, ObservationAudioContinuity, ObservationVisualContinuity, ObservationSceneChange, ObservationStandardDuration, ObservationSegmentRole, ObservationCompleteTimelineDecision:
		return true
	default:
		return false
	}
}

func validateStructureRoleObservationSources(observations []StructureObservation, source SplitSourceAsset) error {
	for _, observation := range observations {
		if observation.RoleEvidence != nil && observation.RoleEvidence.Source != source {
			return fmt.Errorf("source structure: role observation %q binds a different source", observation.ID)
		}
	}
	return nil
}

func validStructureObservationEffect(effect StructureObservationEffect) bool {
	return effect == ObservationProposesBoundary || effect == ObservationSupportsBoundary || effect == ObservationContradictsBoundary || effect == ObservationContextOnly
}
