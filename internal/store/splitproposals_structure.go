package store

import (
	"fmt"

	"github.com/loomarr/loomarr/internal/filler"
	"github.com/loomarr/loomarr/internal/fillerstructure"
)

func validateSplitProposalStructureDecision(proposal filler.SplitProposal) error {
	if proposal.StructureDecision == nil {
		return nil
	}
	artifact := *proposal.StructureDecision
	if err := fillerstructure.ValidateArtifact(artifact); err != nil {
		return fmt.Errorf("invalid structure decision artifact: %w", err)
	}
	if artifact.Decision.Source.SHA256 != proposal.Source.SHA256 ||
		artifact.Decision.Source.Bytes != proposal.Source.Bytes ||
		artifact.Decision.Source.DurationMS != proposal.Source.DurationMs {
		return fmt.Errorf("structure decision artifact does not bind the proposal source")
	}
	return nil
}

func validateSplitProposalStructureAssessment(proposal filler.SplitProposal) error {
	if proposal.Structure == nil {
		return nil
	}
	strictErr := filler.ValidateSourceStructureAssessment(*proposal.Structure)
	if strictErr == nil {
		return nil
	}
	if proposal.StructureDecision != nil {
		if err := filler.ValidateStructureDecisionProjection(*proposal.Structure, *proposal.StructureDecision); err == nil {
			return nil
		}
	}
	return strictErr
}
