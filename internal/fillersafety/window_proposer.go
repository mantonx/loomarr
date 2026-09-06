package fillersafety

import (
	"context"
	"path/filepath"
)

const (
	completeAudioWindowImplementation       = "complete-audio-window-proposer-v1"
	completeAudioWindowMS             int64 = 28_000
	maxCompleteAudioWindowMS                = int64(maxProposedCandidates) * completeAudioWindowMS
)

type completeAudioWindowProposer struct{}

var _ candidateProposer = completeAudioWindowProposer{}.Propose

func newCompleteAudioWindowProposer() (candidateProposer, proposerIdentity) {
	return completeAudioWindowProposer{}.Propose, completeAudioWindowIdentity()
}

func (completeAudioWindowProposer) Propose(ctx context.Context, request proposalRequest) (proposalOutput, error) {
	identity := completeAudioWindowIdentity()
	if ctx == nil || ctx.Err() != nil || !validCompleteAudioWindowRequest(request) {
		return proposalOutput{Identity: identity}, ErrEvaluationInvalid
	}
	intervals := make([]proposedInterval, 0, (request.DurationMS+completeAudioWindowMS-1)/completeAudioWindowMS)
	for startMS := int64(0); startMS < request.DurationMS; startMS += completeAudioWindowMS {
		intervals = append(intervals, proposedInterval{StartMS: startMS, EndMS: min(request.DurationMS, startMS+completeAudioWindowMS)})
	}
	return proposalOutput{Identity: identity, Complete: true, Candidates: intervals}, nil
}

func completeAudioWindowIdentity() proposerIdentity {
	configSHA256 := canonicalJSONSHA256(struct {
		Version       string `json:"version"`
		WindowMS      int64  `json:"windowMs"`
		MaxCandidates int    `json:"maxCandidates"`
	}{
		Version: completeAudioWindowImplementation, WindowMS: completeAudioWindowMS,
		MaxCandidates: maxProposedCandidates,
	})
	return proposerIdentity{
		Kind: proposerDeterministic, Implementation: completeAudioWindowImplementation,
		ConfigSHA256: configSHA256,
	}
}

func validCompleteAudioWindowRequest(request proposalRequest) bool {
	return validSHA256(request.AuthoritySHA256) && validSHA256(request.PolicySHA256) &&
		validSHA256(request.SourceSHA256) && request.SourceBytes > 0 && request.DurationMS > 0 &&
		request.DurationMS <= maxCompleteAudioWindowMS && filepath.IsAbs(request.SourcePath) && validToolIdentity(request.FFmpeg)
}
