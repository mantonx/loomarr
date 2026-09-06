package filler

import (
	"fmt"
	"time"

	"github.com/loomarr/loomarr/internal/fillerstructure"
	"github.com/loomarr/loomarr/internal/fillerstructurewindow"
)

// StructureMaterializationPolicy is the release boundary between a valid assessment and unattended
// creation of held child work items. It cannot authorize broadcast admission.
type StructureMaterializationPolicy struct {
	Authority       *fillerstructure.Authority
	WindowAuthority *fillerstructurewindow.MaterializationAuthority
}

const (
	RejectStructureMissing     AutoSplitReject = "the source has no complete structure assessment"
	RejectStructureInvalid     AutoSplitReject = "the source structure assessment is invalid"
	RejectStructureAmbiguous   AutoSplitReject = "the source structure or a segment role remains unresolved"
	RejectStructureMismatch    AutoSplitReject = "the proposed cuts do not match the complete structure plan"
	RejectStructureUncertified AutoSplitReject = "this source and signal path is not certified for automatic splitting"
)

// CertifiedStructureMaterializable applies V67's complete-plan rules independently of the
// compatibility detector's cut coordinates and confidence score. Confirm means "create a held child",
// never "make this media airable". If any keep interval fails, the plan remains together for review.
func CertifiedStructureMaterializable(p SplitProposal, auto *AutoSplitPolicy, certification *StructureMaterializationPolicy, minClipDuration time.Duration) SplitPartition {
	if p.Structure == nil {
		return certifiedSplitReject(p.Segments, RejectStructureMissing)
	}
	assessment := *p.Structure
	if validateSourceStructureAssessmentOrProjection(assessment, p.StructureDecision) != nil || assessment.Source != p.Source {
		return certifiedSplitReject(p.Segments, RejectStructureInvalid)
	}
	if assessment.Kind != StructureCompilationBreak && assessment.Kind != StructureProgrammeSpots {
		return certifiedSplitReject(p.Segments, RejectStructureAmbiguous)
	}
	if len(p.Spawned) > 0 {
		// V54 proposals that already materialized a partial generation lack source-span lineage for
		// those older children. They stay reviewable; V67 never guesses which plan spans they own.
		return certifiedSplitReject(p.Segments, RejectStructureMismatch)
	}
	keep, discard, err := projectCertifiedStructureSegments(p.Segments, assessment.Plan)
	if err != nil || len(keep) == 0 {
		return certifiedSplitReject(p.Segments, RejectStructureMismatch)
	}
	if p.StructureDecision == nil {
		return SplitPartition{Reject: RejectStructureUncertified, Hold: keep, Discard: discard}
	}
	if ValidateStructureDecisionProjection(assessment, *p.StructureDecision) != nil {
		return SplitPartition{Reject: RejectStructureMismatch, Hold: keep, Discard: discard}
	}
	if verifyStructureMaterializationAuthority(*p.StructureDecision, certification) != nil {
		return SplitPartition{Reject: RejectStructureUncertified, Hold: keep, Discard: discard}
	}
	certified := certifiedPlanMaterializable(keep, auto, minClipDuration)
	if certified.Reject != AutoSplitOK {
		certified.Discard = discard
		return certified
	}
	if len(certified.Hold) > 0 {
		return SplitPartition{Reject: certified.Verdict(), Hold: certified.Hold, Discard: discard}
	}
	certified.Discard = discard
	return certified
}

func verifyStructureMaterializationAuthority(artifact fillerstructure.Artifact, policy *StructureMaterializationPolicy) error {
	if policy == nil {
		return fmt.Errorf("structure materialization policy is unavailable")
	}
	switch artifact.Decision.Input.Kind {
	case fillerstructure.AssessmentInputCompleteVideo:
		if policy.Authority == nil {
			return fmt.Errorf("complete-video structure authority is unavailable")
		}
		return fillerstructure.VerifyAuthority(artifact, *policy.Authority)
	case fillerstructure.AssessmentInputWindowMediaSet:
		if policy.WindowAuthority == nil {
			return fmt.Errorf("window structure authority is unavailable")
		}
		return fillerstructurewindow.VerifyMaterializationAuthority(artifact, *policy.WindowAuthority)
	default:
		return fmt.Errorf("structure assessment input kind is unsupported")
	}
}

func projectCertifiedStructureSegments(existing []SplitSegment, plan []StructurePlanSegment) ([]SplitSegment, []SplitSegment, error) {
	type span struct{ start, end int64 }
	metadata := make(map[span]SplitSegment, len(existing))
	for _, segment := range existing {
		key := span{segment.StartMs, segment.EndMs}
		if _, duplicate := metadata[key]; duplicate {
			return nil, nil, fmt.Errorf("certified split projection repeats a detector span")
		}
		metadata[key] = segment
	}
	var keep, discard []SplitSegment
	for _, planned := range plan {
		segment := metadata[span{planned.StartMs, planned.EndMs}]
		segment.Index, segment.StartMs, segment.EndMs = planned.Index, planned.StartMs, planned.EndMs
		segment.HoldReason = ""
		switch planned.Disposition {
		case StructureKeep:
			if !certifiedFillerRole(planned.Role) {
				return nil, nil, fmt.Errorf("certified split projection has a non-filler keep interval")
			}
			keep = append(keep, segment)
		case StructureDiscard:
			discard = append(discard, segment)
		default:
			return nil, nil, fmt.Errorf("certified split projection has an unresolved interval")
		}
	}
	return keep, discard, nil
}

func certifiedPlanMaterializable(segments []SplitSegment, policy *AutoSplitPolicy, minClipDuration time.Duration) SplitPartition {
	if policy == nil || policy.Enabled == nil || !policy.Enabled() {
		return SplitPartition{Reject: RejectDisabled, Hold: segments}
	}
	if len(segments) == 0 {
		return SplitPartition{Reject: RejectNoSegments}
	}
	maxDuration := 120 * time.Second
	if policy.MaxDuration != nil {
		if configured := policy.MaxDuration(); configured > 0 {
			maxDuration = configured
		}
	}
	partition := SplitPartition{}
	for _, segment := range segments {
		if reason := structureMaterializationSegmentVerdict(segment, minClipDuration, maxDuration); reason != AutoSplitOK {
			segment.HoldReason = string(reason)
			partition.Hold = append(partition.Hold, segment)
			continue
		}
		partition.Confirm = append(partition.Confirm, segment)
	}
	if len(partition.Hold) > 0 {
		reject := partition.Verdict()
		type span struct{ start, end int64 }
		reasons := make(map[span]string, len(partition.Hold))
		for _, held := range partition.Hold {
			reasons[span{held.StartMs, held.EndMs}] = held.HoldReason
		}
		allHeld := append([]SplitSegment(nil), segments...)
		for index := range allHeld {
			allHeld[index].HoldReason = reasons[span{allHeld[index].StartMs, allHeld[index].EndMs}]
			if allHeld[index].HoldReason == "" {
				allHeld[index].HoldReason = string(reject)
			}
		}
		partition.Confirm = nil
		partition.Hold = allHeld
		partition.Reject = reject
	}
	return partition
}

// structureMaterializationSegmentVerdict checks only whether the decided interval can become a
// held child. Role came from the certified complete-timeline plan; category, audience, era, safety,
// rights, and playback facts belong to the child pipeline and cannot be required before it exists.
func structureMaterializationSegmentVerdict(segment SplitSegment, minClipDuration, maxDuration time.Duration) AutoSplitReject {
	if segment.DupOf != "" {
		return RejectDuplicate
	}
	span := time.Duration(segment.EndMs-segment.StartMs) * time.Millisecond
	if span > maxDuration {
		return RejectTooLong
	}
	if minClipDuration > 0 && span < minClipDuration {
		return RejectTooShort
	}
	return AutoSplitOK
}

func certifiedSplitReject(segments []SplitSegment, reason AutoSplitReject) SplitPartition {
	hold := append([]SplitSegment(nil), segments...)
	for i := range hold {
		hold[i].HoldReason = string(reason)
	}
	return SplitPartition{Reject: reason, Hold: hold}
}

func certifiedFillerRole(role StructureSegmentRole) bool {
	switch role {
	case SegmentRoleCommercial, SegmentRolePromo, SegmentRoleBumper, SegmentRoleStationID, SegmentRolePSA, SegmentRoleTrailer, SegmentRoleInterstitial:
		return true
	default:
		return false
	}
}
