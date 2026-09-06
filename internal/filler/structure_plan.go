package filler

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

func buildStructurePlan(source SplitSourceAsset, boundaries []StructureBoundary, claims []StructureRoleClaim, discards []StructureDiscardClaim, observations []StructureObservation, authority *structureDecisionProjectionAuthority) ([]StructurePlanSegment, error) {
	durationMs := source.DurationMs
	cutStatuses := map[int64]StructureBoundaryStatus{0: BoundaryResolved, durationMs: BoundaryResolved}
	for _, boundary := range boundaries {
		if prior, exists := cutStatuses[boundary.AtMs]; !exists || prior == BoundaryResolved {
			cutStatuses[boundary.AtMs] = boundary.Status
		}
	}
	// A V34 shadow discard may use the legacy detector's exact cut while V67 fusion reports a
	// narrower uncertainty midpoint. Preserve the discarded interval as unresolved structure
	// instead of moving or losing time; unresolved edges keep the assessment out of auto-publish.
	for _, discard := range discards {
		if discard.StartMs > 0 && discard.StartMs < durationMs {
			if _, exists := cutStatuses[discard.StartMs]; !exists {
				cutStatuses[discard.StartMs] = BoundaryUnresolved
			}
		}
		if discard.EndMs > 0 && discard.EndMs < durationMs {
			if _, exists := cutStatuses[discard.EndMs]; !exists {
				cutStatuses[discard.EndMs] = BoundaryUnresolved
			}
		}
	}
	cuts := make([]int64, 0, len(cutStatuses))
	for cut := range cutStatuses {
		cuts = append(cuts, cut)
	}
	slices.Sort(cuts)
	claimBySpan := make(map[[2]int64]StructureRoleClaim, len(claims))
	discardBySpan := make(map[[2]int64]StructureDiscardClaim, len(discards))
	knownEvidence := make(map[string]StructureObservation, len(observations))
	for _, o := range observations {
		knownEvidence[o.ID] = o
	}
	for _, claim := range claims {
		claim.Reason = strings.TrimSpace(claim.Reason)
		claim.EvidenceIDs = uniqueStrings(claim.EvidenceIDs)
		if claim.StartMs < 0 || claim.EndMs <= claim.StartMs || claim.EndMs > durationMs || !validStructureSegmentRole(claim.Role) || claim.Reason == "" || len(claim.EvidenceIDs) == 0 {
			return nil, errors.New("source structure: invalid role claim")
		}
		for _, id := range claim.EvidenceIDs {
			if _, ok := knownEvidence[id]; !ok {
				return nil, fmt.Errorf("source structure: role claim cites unknown observation %q", id)
			}
		}
		key := [2]int64{claim.StartMs, claim.EndMs}
		if _, duplicate := claimBySpan[key]; duplicate {
			return nil, fmt.Errorf("source structure: duplicate role claim for %d..%d", claim.StartMs, claim.EndMs)
		}
		claimBySpan[key] = claim
	}
	for _, discard := range discards {
		discard.EvidenceIDs = uniqueStrings(discard.EvidenceIDs)
		if discard.StartMs < 0 || discard.EndMs <= discard.StartMs || discard.EndMs > durationMs || !validStructureDiscardReason(discard.Reason) || len(discard.EvidenceIDs) == 0 {
			return nil, errors.New("source structure: invalid discard claim")
		}
		for _, id := range discard.EvidenceIDs {
			if _, ok := knownEvidence[id]; !ok {
				return nil, fmt.Errorf("source structure: discard claim cites unknown observation %q", id)
			}
		}
		key := [2]int64{discard.StartMs, discard.EndMs}
		if _, duplicate := discardBySpan[key]; duplicate {
			return nil, fmt.Errorf("source structure: duplicate discard claim for %d..%d", discard.StartMs, discard.EndMs)
		}
		if _, roleClaimed := claimBySpan[key]; roleClaimed {
			return nil, fmt.Errorf("source structure: span %d..%d has both role and discard claims", discard.StartMs, discard.EndMs)
		}
		discardBySpan[key] = discard
	}
	plan := make([]StructurePlanSegment, 0, len(cuts)-1)
	usedClaims, usedDiscards := 0, 0
	for i := 0; i+1 < len(cuts); i++ {
		segment := StructurePlanSegment{
			Index: i, StartMs: cuts[i], EndMs: cuts[i+1], Disposition: StructureUnresolved,
			StartStatus: cutStatuses[cuts[i]], EndStatus: cutStatuses[cuts[i+1]],
			Reason: "structure or role remains unresolved",
		}
		if claim, ok := claimBySpan[[2]int64{segment.StartMs, segment.EndMs}]; ok {
			usedClaims++
			segment.Role, segment.EvidenceIDs, segment.Reason = claim.Role, claim.EvidenceIDs, claim.Reason
			if segment.StartStatus == BoundaryResolved && segment.EndStatus == BoundaryResolved {
				switch claim.Role {
				case SegmentRoleCommercial, SegmentRolePromo, SegmentRoleBumper, SegmentRoleStationID, SegmentRolePSA, SegmentRoleTrailer, SegmentRoleInterstitial:
					if structureRoleClaimHasExactEvidence(claim, source, knownEvidence) || authority.authorizesKeep(claim, knownEvidence) {
						segment.Disposition, segment.Reason = StructureKeep, claim.Reason
					}
				case SegmentRoleProgrammeFragment, SegmentRoleNonFiller:
					segment.Disposition, segment.Reason = StructureDiscard, claim.Reason
					if claim.Role == SegmentRoleProgrammeFragment {
						segment.DiscardReason = DiscardProgrammeMaterial
					} else {
						segment.DiscardReason = DiscardNonFiller
					}
				}
			}
		}
		if discard, ok := discardBySpan[[2]int64{segment.StartMs, segment.EndMs}]; ok {
			usedDiscards++
			segment.Disposition, segment.Reason = StructureDiscard, string(discard.Reason)
			segment.DiscardReason, segment.EvidenceIDs = discard.Reason, discard.EvidenceIDs
		}
		plan = append(plan, segment)
	}
	if usedClaims != len(claimBySpan) {
		return nil, errors.New("source structure: role claim does not align with fused segment bounds")
	}
	if usedDiscards != len(discardBySpan) {
		return nil, errors.New("source structure: discard claim does not align with fused segment bounds")
	}
	return plan, validateStructureCoverage(plan, durationMs)
}

func (authority *structureDecisionProjectionAuthority) authorizesKeep(claim StructureRoleClaim, observations map[string]StructureObservation) bool {
	if authority == nil {
		return false
	}
	interval, ok := authority.intervals[[2]int64{claim.StartMs, claim.EndMs}]
	if !ok || interval.disposition != StructureKeep || interval.role != claim.Role {
		return false
	}
	for _, id := range claim.EvidenceIDs {
		observation, exists := observations[id]
		if exists && id == interval.observationID && observation.Kind == ObservationCompleteTimelineDecision &&
			observation.Effect == ObservationContextOnly && observation.StartMs == claim.StartMs && observation.EndMs == claim.EndMs &&
			observation.Producer == authority.reducerVersion && observation.EvidenceSHA256 == authority.artifactSHA256 && observation.RoleEvidence == nil {
			return true
		}
	}
	return false
}

func structureRoleClaimHasExactEvidence(claim StructureRoleClaim, source SplitSourceAsset, observations map[string]StructureObservation) bool {
	for _, id := range claim.EvidenceIDs {
		observation := observations[id]
		if observation.Kind != ObservationSegmentRole || observation.RoleEvidence == nil {
			continue
		}
		evidence := observation.RoleEvidence
		if evidence.Source == source && evidence.StartMs == claim.StartMs && evidence.EndMs == claim.EndMs && evidence.Role == claim.Role {
			return true
		}
	}
	return false
}

func reduceStructureKind(assessment SourceStructureAssessment) SourceStructureKind {
	if assessment.UnusableReason != "" {
		return StructureUnusable
	}
	for _, boundary := range assessment.Boundaries {
		if boundary.Status != BoundaryResolved {
			return StructureAmbiguous
		}
	}
	allResolved := true
	fillers, programmes := 0, 0
	for _, segment := range assessment.Plan {
		if segment.Disposition == StructureUnresolved || segment.Role == SegmentRoleAmbiguous || segment.Role == SegmentRoleUnusable || segment.Disposition == StructureKeep && segment.Role == "" {
			allResolved = false
		}
		if segment.Disposition == StructureKeep {
			fillers++
		}
		if segment.Role == SegmentRoleProgrammeFragment {
			programmes++
		}
	}
	if !allResolved {
		return StructureAmbiguous
	}
	if programmes > 0 && fillers > 0 {
		return StructureProgrammeSpots
	}
	if len(assessment.Plan) == 1 {
		return StructureSingleUnit
	}
	if fillers >= 2 && programmes == 0 {
		return StructureCompilationBreak
	}
	return StructureAmbiguous
}

func validateStructureCoverage(plan []StructurePlanSegment, durationMs int64) error {
	if durationMs <= 0 || len(plan) == 0 || plan[0].StartMs != 0 || plan[len(plan)-1].EndMs != durationMs {
		return errors.New("source structure: plan does not cover the complete source")
	}
	for i, segment := range plan {
		if segment.Index != i || segment.StartMs < 0 || segment.EndMs <= segment.StartMs || i > 0 && segment.StartMs != plan[i-1].EndMs || !validStructureDisposition(segment.Disposition) || !validStructureBoundaryStatus(segment.StartStatus) || !validStructureBoundaryStatus(segment.EndStatus) {
			return fmt.Errorf("source structure: invalid plan segment %d", i)
		}
		if segment.Disposition != StructureUnresolved && strings.TrimSpace(segment.Reason) == "" {
			return fmt.Errorf("source structure: resolved plan segment %d lacks a reason", i)
		}
		if segment.Disposition == StructureKeep && segment.Role == "" {
			return fmt.Errorf("source structure: kept plan segment %d lacks a role", i)
		}
		if segment.Disposition == StructureDiscard && !validStructureDiscardReason(segment.DiscardReason) {
			return fmt.Errorf("source structure: discarded plan segment %d lacks a closed reason", i)
		}
	}
	return nil
}

func validSourceStructureKind(kind SourceStructureKind) bool {
	return kind == StructureSingleUnit || kind == StructureCompilationBreak || kind == StructureProgrammeSpots || kind == StructureAmbiguous || kind == StructureUnusable
}

func validStructureSegmentRole(role StructureSegmentRole) bool {
	switch role {
	case SegmentRoleCommercial, SegmentRolePromo, SegmentRoleBumper, SegmentRoleStationID, SegmentRolePSA, SegmentRoleTrailer, SegmentRoleInterstitial, SegmentRoleProgrammeFragment, SegmentRoleNonFiller, SegmentRoleAmbiguous, SegmentRoleUnusable:
		return true
	default:
		return false
	}
}

func validStructureDisposition(disposition StructureSegmentDisposition) bool {
	return disposition == StructureKeep || disposition == StructureDiscard || disposition == StructureUnresolved
}

func validStructureBoundaryStatus(status StructureBoundaryStatus) bool {
	return status == BoundaryResolved || status == BoundaryUnresolved || status == BoundaryConflicted
}

func validStructureDiscardReason(reason StructureDiscardReason) bool {
	return reason == DiscardBelowClipFloor || reason == DiscardDuplicate || reason == DiscardProgrammeMaterial || reason == DiscardNonFiller || reason == DiscardUnusableFragment
}
