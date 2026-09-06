package filler

// structureDecisionSHA256ForInterval returns provenance only when the proposal is an exact
// projection of one confirmed artifact and this child is one of that decision's keep spans.
// Manual boundary edits and detector-only cuts deliberately return no model-decision identity.
func structureDecisionSHA256ForInterval(proposal SplitProposal, segment SplitSegment) string {
	authority, ok := structureDecisionAuthorityForInterval(proposal, segment)
	if !ok {
		return ""
	}
	return authority.sha256
}

type structureDecisionIntervalAuthority struct {
	sha256 string
	role   StructureSegmentRole
}

// structureDecisionAuthorityForInterval returns the exact role as well as the artifact identity.
// Materialization must not preserve the decision digest while throwing away the decision's answer.
func structureDecisionAuthorityForInterval(proposal SplitProposal, segment SplitSegment) (structureDecisionIntervalAuthority, bool) {
	if proposal.Structure == nil || proposal.StructureDecision == nil ||
		proposal.Structure.Source != proposal.Source ||
		ValidateStructureDecisionProjection(*proposal.Structure, *proposal.StructureDecision) != nil {
		return structureDecisionIntervalAuthority{}, false
	}
	for _, planned := range proposal.Structure.Plan {
		if planned.Disposition == StructureKeep && certifiedFillerRole(planned.Role) && planned.StartMs == segment.StartMs && planned.EndMs == segment.EndMs {
			return structureDecisionIntervalAuthority{sha256: proposal.StructureDecision.SHA256, role: planned.Role}, true
		}
	}
	return structureDecisionIntervalAuthority{}, false
}

func catalogKindForStructureRole(role StructureSegmentRole) (Kind, bool) {
	switch role {
	case SegmentRoleCommercial:
		return Commercial, true
	case SegmentRolePromo, SegmentRoleInterstitial:
		return Interstitial, true
	case SegmentRoleBumper:
		return Bumper, true
	case SegmentRoleStationID:
		return StationID, true
	case SegmentRolePSA:
		return PSA, true
	case SegmentRoleTrailer:
		return Trailer, true
	default:
		return "", false
	}
}
