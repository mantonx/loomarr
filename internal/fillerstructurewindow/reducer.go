package fillerstructurewindow

import (
	"github.com/loomarr/loomarr/internal/fillerstructure"
)

// ReducerInput projects a validated media set into the provider-neutral assessment-input manifest.
func ReducerInput(set MediaSet) (fillerstructure.AssessmentInput, error) {
	if err := ValidateMediaSet(set); err != nil {
		return fillerstructure.AssessmentInput{}, err
	}
	media := make([]fillerstructure.AssessmentMedia, len(set.Windows))
	for ordinal := range set.Windows {
		media[ordinal] = set.Windows[ordinal].Media
	}
	return fillerstructure.NewWindowMediaSetInput(set.Plan.Source, set.Plan.SHA256, media)
}

// ReducerCandidate turns one replay-validated family stitch into one source-level candidate.
// It does not treat the constituent windows as independent votes.
func ReducerCandidate(result StitchResult) (fillerstructure.AssessmentInput, fillerstructure.Candidate, error) {
	if err := ValidateStitchResult(result); err != nil {
		return fillerstructure.AssessmentInput{}, fillerstructure.Candidate{}, err
	}
	input, err := ReducerInput(result.MediaSet)
	if err != nil {
		return fillerstructure.AssessmentInput{}, fillerstructure.Candidate{}, err
	}
	profile := result.Assessor
	assessor := fillerstructure.Assessor{
		ID: profile.ID, ModelFamily: profile.ModelFamily, Provider: profile.Provider,
		Model: profile.Model, ModelDigest: profile.ModelDigest, CapabilitySHA256: profile.CapabilitySHA256,
		PromptVersion: profile.PromptVersion, EvidenceContract: profile.EvidenceContract,
		AssessmentSHA256: result.SHA256,
	}
	failure := ""
	segments := result.Segments
	if result.Status == StitchHeld {
		failure, segments = result.HoldReason, nil
	}
	candidate, err := fillerstructure.NewCandidate(result.MediaSet.Plan.Source, input.SHA256, assessor, failure, segments)
	if err != nil {
		return fillerstructure.AssessmentInput{}, fillerstructure.Candidate{}, err
	}
	return input, candidate, nil
}
