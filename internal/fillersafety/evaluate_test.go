package fillersafety

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/loomarr/loomarr/internal/testkit"
)

func TestEvaluateRunsSerialCascadeAndRejectsOnlyTwoModelNegatives(t *testing.T) {
	t.Parallel()
	plan := proposalTestPlan(t)
	identity := validProposerIdentityFixture()
	proposer := &testkit.Recorder[proposalRequest, proposalOutput]{Respond: func(proposalRequest) (proposalOutput, error) {
		return proposalOutput{Identity: identity, Complete: true, Candidates: []proposedInterval{{StartMS: 2_000, EndMS: 2_500}, {StartMS: 100, EndMS: 800}}}, nil
	}}
	extractor := &testkit.Recorder[Candidate, []byte]{Respond: func(Candidate) ([]byte, error) { return validCandidateWAV(), nil }}
	states := map[string]AudioState{}
	audio := &testkit.Recorder[Candidate, audioAttempt]{Respond: func(candidate Candidate) (audioAttempt, error) {
		return audioAttempt{Assessment: AudioAssessment{CandidateID: candidate.ID, State: states[candidate.ID]}, MatchedRuleIDs: []string{}}, nil
	}}
	video := &testkit.Recorder[*CompleteMediaPlan, videoAttempt]{Respond: func(*CompleteMediaPlan) (videoAttempt, error) {
		return videoAttempt{State: VideoNoSignal, Flags: []videoFlag{}}, nil
	}}
	eval := evaluatorFromRecorders(identity, proposer, extractor, audio, video)

	firstID := proposalCandidateID(plan.AuthoritySHA256, proposedInterval{StartMS: 100, EndMS: 800})
	secondID := proposalCandidateID(plan.AuthoritySHA256, proposedInterval{StartMS: 2_000, EndMS: 2_500})
	states[firstID], states[secondID] = AudioAbsent, AudioAbsent
	got := eval.evaluate(context.Background(), plan)
	extractCalls, audioCalls := extractor.Inputs(), audio.Inputs()
	if got.Result.Outcome != OutcomeCandidateRejected || got.VideoAttempt == nil || got.VideoAttempt.State != VideoNoSignal || video.Calls() != 1 || !slices.Equal([]string{extractCalls[0].ID, extractCalls[1].ID}, []string{firstID, secondID}) || !slices.Equal([]string{audioCalls[0].ID, audioCalls[1].ID}, []string{firstID, secondID}) {
		t.Fatalf("evaluation=%+v extract=%v audio=%v video=%d", got, extractCalls, audioCalls, video.Calls())
	}
}

func TestEvaluatePresenceOutranksFailureAndSkipsVideo(t *testing.T) {
	t.Parallel()
	plan := proposalTestPlan(t)
	identity := validProposerIdentityFixture()
	intervals := []proposedInterval{{StartMS: 100, EndMS: 800}, {StartMS: 2_000, EndMS: 2_500}}
	firstID := proposalCandidateID(plan.AuthoritySHA256, intervals[0])
	secondID := proposalCandidateID(plan.AuthoritySHA256, intervals[1])
	proposer := &testkit.Recorder[proposalRequest, proposalOutput]{Respond: func(proposalRequest) (proposalOutput, error) {
		return proposalOutput{Identity: identity, Complete: true, Candidates: intervals}, nil
	}}
	extractor := &testkit.Recorder[Candidate, []byte]{Respond: func(candidate Candidate) ([]byte, error) {
		if candidate.ID == firstID {
			return nil, errors.New("private extraction detail")
		}
		return validCandidateWAV(), nil
	}}
	audio := &testkit.Recorder[Candidate, audioAttempt]{Respond: func(candidate Candidate) (audioAttempt, error) {
		return audioAttempt{Assessment: AudioAssessment{CandidateID: candidate.ID, State: map[string]AudioState{secondID: AudioDetected}[candidate.ID]}, MatchedRuleIDs: []string{}}, nil
	}}
	video := &testkit.Recorder[*CompleteMediaPlan, videoAttempt]{Respond: func(*CompleteMediaPlan) (videoAttempt, error) {
		return videoAttempt{State: VideoNoSignal, Flags: []videoFlag{}}, nil
	}}
	got := evaluatorFromRecorders(identity, proposer, extractor, audio, video).evaluate(context.Background(), plan)
	if got.Result.Outcome != OutcomeQuarantine || !slices.Equal(got.Result.Reasons, []Reason{ReasonAudioFailure, ReasonAudioProhibitedSignal}) || video.Calls() != 0 {
		t.Fatalf("evaluation=%+v video=%d", got, video.Calls())
	}
}

func TestEvaluateNoCandidatesStillRequiresCompleteVideo(t *testing.T) {
	t.Parallel()
	plan := proposalTestPlan(t)
	identity := validProposerIdentityFixture()
	proposer := &testkit.Recorder[proposalRequest, proposalOutput]{Respond: func(proposalRequest) (proposalOutput, error) {
		return proposalOutput{Identity: identity, Complete: true}, nil
	}}
	video := &testkit.Recorder[*CompleteMediaPlan, videoAttempt]{Respond: func(*CompleteMediaPlan) (videoAttempt, error) {
		return videoAttempt{State: VideoIncomplete, Flags: []videoFlag{}}, nil
	}}
	emptyExtractor := &testkit.Recorder[Candidate, []byte]{Respond: func(Candidate) ([]byte, error) { return validCandidateWAV(), nil }}
	emptyAudio := &testkit.Recorder[Candidate, audioAttempt]{Respond: func(candidate Candidate) (audioAttempt, error) {
		return audioAttempt{Assessment: AudioAssessment{CandidateID: candidate.ID, State: AudioAbsent}, MatchedRuleIDs: []string{}}, nil
	}}
	got := evaluatorFromRecorders(identity, proposer, emptyExtractor, emptyAudio, video).evaluate(context.Background(), plan)
	if got.Result.Outcome != OutcomeHold || !slices.Equal(got.Result.Reasons, []Reason{ReasonVideoIncomplete}) || video.Calls() != 1 {
		t.Fatalf("evaluation=%+v video=%d", got, video.Calls())
	}
}

func TestEvaluateAdapterErrorsCannotSmugglePresence(t *testing.T) {
	t.Parallel()
	plan := proposalTestPlan(t)
	identity := validProposerIdentityFixture()
	interval := proposedInterval{StartMS: 100, EndMS: 800}
	proposer := &testkit.Recorder[proposalRequest, proposalOutput]{Respond: func(proposalRequest) (proposalOutput, error) {
		return proposalOutput{Identity: identity, Complete: true, Candidates: []proposedInterval{interval}}, nil
	}}
	audio := &testkit.Recorder[Candidate, audioAttempt]{Respond: func(candidate Candidate) (audioAttempt, error) {
		return audioAttempt{Assessment: AudioAssessment{CandidateID: candidate.ID, State: AudioDetected}, MatchedRuleIDs: []string{}}, errors.New("private provider detail")
	}}
	video := &testkit.Recorder[*CompleteMediaPlan, videoAttempt]{Respond: func(*CompleteMediaPlan) (videoAttempt, error) {
		return videoAttempt{State: VideoNoSignal, Flags: []videoFlag{}}, nil
	}}
	extractor := &testkit.Recorder[Candidate, []byte]{Respond: func(Candidate) ([]byte, error) { return validCandidateWAV(), nil }}
	got := evaluatorFromRecorders(identity, proposer, extractor, audio, video).evaluate(context.Background(), plan)
	if got.Result.Outcome != OutcomeHold || !slices.Equal(got.Result.Reasons, []Reason{ReasonAudioFailure}) || video.Calls() != 0 {
		t.Fatalf("evaluation=%+v video=%d", got, video.Calls())
	}
}

func evaluatorFromRecorders(identity proposerIdentity, proposer *testkit.Recorder[proposalRequest, proposalOutput], extractor *testkit.Recorder[Candidate, []byte], audio *testkit.Recorder[Candidate, audioAttempt], video *testkit.Recorder[*CompleteMediaPlan, videoAttempt]) *evaluator {
	return &evaluator{
		proposer: func(_ context.Context, request proposalRequest) (proposalOutput, error) {
			return proposer.Call(request)
		}, proposerIdentity: identity,
		audioExtractor: func(_ context.Context, _ *CompleteMediaPlan, candidate Candidate) ([]byte, error) {
			return extractor.Call(candidate)
		},
		audio: func(_ context.Context, candidate Candidate, _ []byte) (audioAttempt, error) {
			return audio.Call(candidate)
		},
		video: func(_ context.Context, plan *CompleteMediaPlan) (videoAttempt, error) { return video.Call(plan) },
	}
}
