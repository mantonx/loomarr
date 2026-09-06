package filler

import (
	"context"
	"fmt"
	"time"

	"github.com/loomarr/loomarr/internal/fillerstructure"
)

// AssessProposalStructure resolves and re-verifies the proposal's exact retained source, executes
// one complete-timeline decision, and attaches it without redrawing detector observations. The
// update is existing-only so a long provider call cannot resurrect a proposal resolved meanwhile.
func (sp *Splitter) AssessProposalStructure(ctx context.Context, proposal SplitProposal, decisioner CompleteTimelineStructureDecisioner) (SplitProposal, error) {
	if sp == nil || sp.store == nil || decisioner == nil || proposal.ID == "" || proposal.ClipHash == "" || !proposal.Ready() {
		return SplitProposal{}, fmt.Errorf("assess proposal structure: execution is unavailable or proposal is incomplete")
	}
	if proposal.StructureDecision != nil {
		return proposal, nil
	}
	clip, found, err := sp.store.GetClip(ctx, proposal.ClipHash)
	if err != nil {
		return SplitProposal{}, err
	}
	if !found {
		return SplitProposal{}, fmt.Errorf("assess proposal structure: compilation %s no longer exists", proposal.ClipHash)
	}
	source, fullPath, err := resolveSplitSource(ctx, sp.dropDir, clip, proposal.Source)
	if err != nil {
		return SplitProposal{}, fmt.Errorf("assess proposal structure source: %w", err)
	}
	artifact, err := decisioner.Assess(ctx, StructureAssessmentSource{Source: source, FullPath: fullPath})
	if err != nil {
		return SplitProposal{}, fmt.Errorf("assess complete proposal timeline: %w", err)
	}
	if err := fillerstructure.ValidateArtifact(artifact); err != nil ||
		artifact.Decision.Source.SHA256 != source.SHA256 || artifact.Decision.Source.Bytes != source.Bytes || artifact.Decision.Source.DurationMS != source.DurationMs {
		return SplitProposal{}, fmt.Errorf("assess proposal structure: decision is invalid or source-drifted")
	}
	updated := proposal
	if artifact.Decision.Status == fillerstructure.StatusConfirmed {
		assessment, projectionErr := ProjectConfirmedStructureDecision(source, proposal.Structure, artifact)
		if projectionErr != nil {
			return SplitProposal{}, projectionErr
		}
		updated.Structure = &assessment
	}
	updated.StructureDecision = &artifact
	updateCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := sp.store.UpdateSplitProposal(updateCtx, updated); err != nil {
		return SplitProposal{}, err
	}
	return updated, nil
}
