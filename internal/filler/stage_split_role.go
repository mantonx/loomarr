package filler

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/llm"
)

func structureRoleEvidenceFromVision(source SplitSourceAsset, segment SplitSegment, prompt string, frames [][]byte, response llm.Response, out visionOutput, assessedAt time.Time) (*StructureRoleEvidence, error) {
	role := StructureSegmentRole(strings.TrimSpace(out.Role))
	if !validStructureSegmentRole(role) || strings.TrimSpace(out.RoleReason) == "" {
		return nil, nil
	}
	var charge *StructureRoleCharge
	if response.Attribution.Charge != nil {
		charge = &StructureRoleCharge{Amount: response.Attribution.Charge.Amount, Currency: response.Attribution.Charge.Currency}
	}
	tokens := response.Attribution.Tokens
	evidence, err := NewStructureRoleEvidence(StructureRoleEvidenceInput{
		Source: source, StartMs: segment.StartMs, EndMs: segment.EndMs, Role: role, Reason: out.RoleReason,
		Frames: frames, PromptVersion: visionPromptVersion, Prompt: prompt, Response: response.Content,
		RequestedProvider: response.Attribution.RequestedProvider, ResolvedProvider: response.Attribution.ResolvedProvider,
		RequestedModel: response.Attribution.RequestedModel, ResolvedModel: response.Attribution.ResolvedModel,
		Modalities: slices.Clone(response.Attribution.Modalities),
		Tokens: StructureRoleTokenUsage{
			Prompt: tokens.Prompt, Completion: tokens.Completion, Reasoning: tokens.Reasoning,
			Cached: tokens.Cached, CacheWrite: tokens.CacheWrite, Image: tokens.Image, Audio: tokens.Audio, Video: tokens.Video,
		},
		Charge: charge, LatencyMs: response.Attribution.Latency.Milliseconds(), Attempts: response.Attribution.Attempts,
		GenerationID: response.Attribution.GenerationID, AssessedAt: assessedAt,
	})
	if err != nil {
		return nil, err
	}
	return &evidence, nil
}

// reassessProposalStructure adds the proposal's retained per-span role evidence to the immutable
// detector facts. Evidence for a span that does not exactly match the deterministic plan remains
// auditable but cannot become a role claim.
func reassessProposalStructure(proposal SplitProposal, assessedAt time.Time) (SourceStructureAssessment, error) {
	if proposal.Structure == nil {
		return SourceStructureAssessment{}, fmt.Errorf("source structure: proposal has no assessment to enrich")
	}
	var authority *structureDecisionProjectionAuthority
	if err := ValidateSourceStructureAssessment(*proposal.Structure); err != nil {
		if proposal.StructureDecision == nil {
			return SourceStructureAssessment{}, err
		}
		authority, err = newStructureDecisionProjectionAuthority(proposal.Source, *proposal.StructureDecision)
		if err != nil {
			return SourceStructureAssessment{}, err
		}
		if err := validateSourceStructureAssessment(*proposal.Structure, authority); err != nil {
			return SourceStructureAssessment{}, err
		}
	}
	replacedSpans := make(map[[2]int64]struct{}, len(proposal.Segments))
	for _, segment := range proposal.Segments {
		if segment.RoleEvidence != nil {
			replacedSpans[[2]int64{segment.StartMs, segment.EndMs}] = struct{}{}
		}
	}
	observations := make([]StructureObservation, 0, len(proposal.Structure.Observations)+len(proposal.Segments))
	for _, observation := range proposal.Structure.Observations {
		if observation.Kind == ObservationSegmentRole {
			if _, replaced := replacedSpans[[2]int64{observation.StartMs, observation.EndMs}]; replaced {
				continue
			}
		}
		observations = append(observations, observation)
	}
	discards := structureDiscardClaims(proposal.Structure.Plan)
	planSpans := make(map[[2]int64]struct{}, len(proposal.Structure.Plan))
	claimBySpan := make(map[[2]int64]StructureRoleClaim, len(proposal.Structure.Plan))
	for _, segment := range proposal.Structure.Plan {
		span := [2]int64{segment.StartMs, segment.EndMs}
		planSpans[span] = struct{}{}
		if segment.Role != "" {
			claimBySpan[span] = StructureRoleClaim{
				StartMs: segment.StartMs, EndMs: segment.EndMs, Role: segment.Role,
				EvidenceIDs: slices.Clone(segment.EvidenceIDs), Reason: segment.Reason,
			}
		}
	}
	for _, segment := range proposal.Segments {
		if segment.RoleEvidence == nil {
			continue
		}
		evidence := cloneStructureRoleEvidence(*segment.RoleEvidence)
		if evidence.Source != proposal.Source || evidence.StartMs != segment.StartMs || evidence.EndMs != segment.EndMs {
			return SourceStructureAssessment{}, fmt.Errorf("source structure: segment %d role evidence binds another source or span", segment.Index)
		}
		observation, err := NewStructureRoleObservation("role-"+evidence.SHA256, evidence)
		if err != nil {
			return SourceStructureAssessment{}, err
		}
		observations = append(observations, observation)
		span := [2]int64{segment.StartMs, segment.EndMs}
		if _, exact := planSpans[span]; exact {
			claimBySpan[span] = StructureRoleClaim{
				StartMs: segment.StartMs, EndMs: segment.EndMs, Role: evidence.Role,
				EvidenceIDs: []string{observation.ID}, Reason: evidence.Reason,
			}
		}
	}
	claims := make([]StructureRoleClaim, 0, len(claimBySpan))
	for _, segment := range proposal.Structure.Plan {
		if claim, exists := claimBySpan[[2]int64{segment.StartMs, segment.EndMs}]; exists {
			claims = append(claims, claim)
		}
	}
	assessment, err := assessSourceStructure(SourceStructureInput{
		Source: proposal.Source, Observations: observations, RoleClaims: claims, DiscardClaims: discards,
		AssessedAt: assessedAt, UnusableReason: proposal.Structure.UnusableReason,
	}, authority)
	if err != nil {
		return SourceStructureAssessment{}, err
	}
	if authority != nil {
		if err := ValidateStructureDecisionProjection(assessment, *proposal.StructureDecision); err != nil {
			return SourceStructureAssessment{}, err
		}
	}
	return assessment, nil
}

func structureDiscardClaims(plan []StructurePlanSegment) []StructureDiscardClaim {
	var claims []StructureDiscardClaim
	for _, segment := range plan {
		if segment.Disposition == StructureDiscard && segment.Role == "" {
			claims = append(claims, StructureDiscardClaim{
				StartMs: segment.StartMs, EndMs: segment.EndMs, Reason: segment.DiscardReason,
				EvidenceIDs: slices.Clone(segment.EvidenceIDs),
			})
		}
	}
	return claims
}
