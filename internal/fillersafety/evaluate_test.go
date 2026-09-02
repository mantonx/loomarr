package fillersafety

import (
	"context"
	"errors"
	"slices"
	"testing"
)

type fakeAudioExtractor struct {
	fail  map[string]bool
	calls []string
}

func (f *fakeAudioExtractor) Extract(_ context.Context, _ *CompleteMediaPlan, candidate Candidate) ([]byte, error) {
	f.calls = append(f.calls, candidate.ID)
	if f.fail[candidate.ID] {
		return nil, errors.New("private extraction detail")
	}
	return validCandidateWAV(), nil
}

type fakeAudioAdjudicator struct {
	states map[string]AudioState
	errors map[string]bool
	calls  []string
}

func (f *fakeAudioAdjudicator) adjudicate(_ context.Context, candidate Candidate, _ []byte) (audioAttempt, error) {
	f.calls = append(f.calls, candidate.ID)
	attempt := audioAttempt{Assessment: AudioAssessment{CandidateID: candidate.ID, State: f.states[candidate.ID]}, MatchedRuleIDs: []string{}}
	if f.errors[candidate.ID] {
		return attempt, errors.New("private provider detail")
	}
	return attempt, nil
}

type fakeVideoCorroborator struct {
	state VideoState
	err   error
	calls int
}

func (f *fakeVideoCorroborator) corroborate(context.Context, *CompleteMediaPlan) (videoAttempt, error) {
	f.calls++
	return videoAttempt{State: f.state, Flags: []videoFlag{}}, f.err
}

func TestEvaluateRunsSerialCascadeAndRejectsOnlyTwoModelNegatives(t *testing.T) {
	t.Parallel()
	plan := proposalTestPlan(t)
	identity := validProposerIdentityFixture()
	proposer := &fakeAcousticProposer{output: proposalOutput{Identity: identity, Complete: true, Candidates: []proposedInterval{{StartMS: 2_000, EndMS: 2_500}, {StartMS: 100, EndMS: 800}}}}
	extractor := &fakeAudioExtractor{}
	audio := &fakeAudioAdjudicator{states: map[string]AudioState{}}
	video := &fakeVideoCorroborator{state: VideoNoSignal}
	eval := &evaluator{proposer: proposer, proposerIdentity: identity, audioExtractor: extractor, audio: audio, video: video}

	firstID := proposalCandidateID(plan.AuthoritySHA256, proposedInterval{StartMS: 100, EndMS: 800})
	secondID := proposalCandidateID(plan.AuthoritySHA256, proposedInterval{StartMS: 2_000, EndMS: 2_500})
	audio.states[firstID], audio.states[secondID] = AudioAbsent, AudioAbsent
	got := eval.evaluate(context.Background(), plan)
	if got.Result.Outcome != OutcomeCandidateRejected || got.VideoAttempt == nil || got.VideoAttempt.State != VideoNoSignal || video.calls != 1 || !slices.Equal(extractor.calls, []string{firstID, secondID}) || !slices.Equal(audio.calls, []string{firstID, secondID}) {
		t.Fatalf("evaluation=%+v extract=%v audio=%v video=%d", got, extractor.calls, audio.calls, video.calls)
	}
}

func TestEvaluatePresenceOutranksFailureAndSkipsVideo(t *testing.T) {
	t.Parallel()
	plan := proposalTestPlan(t)
	identity := validProposerIdentityFixture()
	intervals := []proposedInterval{{StartMS: 100, EndMS: 800}, {StartMS: 2_000, EndMS: 2_500}}
	firstID := proposalCandidateID(plan.AuthoritySHA256, intervals[0])
	secondID := proposalCandidateID(plan.AuthoritySHA256, intervals[1])
	proposer := &fakeAcousticProposer{output: proposalOutput{Identity: identity, Complete: true, Candidates: intervals}}
	extractor := &fakeAudioExtractor{fail: map[string]bool{firstID: true}}
	audio := &fakeAudioAdjudicator{states: map[string]AudioState{secondID: AudioDetected}}
	video := &fakeVideoCorroborator{state: VideoNoSignal}
	got := (&evaluator{proposer: proposer, proposerIdentity: identity, audioExtractor: extractor, audio: audio, video: video}).evaluate(context.Background(), plan)
	if got.Result.Outcome != OutcomeQuarantine || !slices.Equal(got.Result.Reasons, []Reason{ReasonAudioFailure, ReasonAudioProhibitedSignal}) || video.calls != 0 {
		t.Fatalf("evaluation=%+v video=%d", got, video.calls)
	}
}

func TestEvaluateNoCandidatesStillRequiresCompleteVideo(t *testing.T) {
	t.Parallel()
	plan := proposalTestPlan(t)
	identity := validProposerIdentityFixture()
	proposer := &fakeAcousticProposer{output: proposalOutput{Identity: identity, Complete: true}}
	video := &fakeVideoCorroborator{state: VideoIncomplete}
	got := (&evaluator{proposer: proposer, proposerIdentity: identity, audioExtractor: &fakeAudioExtractor{}, audio: &fakeAudioAdjudicator{}, video: video}).evaluate(context.Background(), plan)
	if got.Result.Outcome != OutcomeHold || !slices.Equal(got.Result.Reasons, []Reason{ReasonVideoIncomplete}) || video.calls != 1 {
		t.Fatalf("evaluation=%+v video=%d", got, video.calls)
	}
}

func TestEvaluateAdapterErrorsCannotSmugglePresence(t *testing.T) {
	t.Parallel()
	plan := proposalTestPlan(t)
	identity := validProposerIdentityFixture()
	interval := proposedInterval{StartMS: 100, EndMS: 800}
	id := proposalCandidateID(plan.AuthoritySHA256, interval)
	proposer := &fakeAcousticProposer{output: proposalOutput{Identity: identity, Complete: true, Candidates: []proposedInterval{interval}}}
	audio := &fakeAudioAdjudicator{states: map[string]AudioState{id: AudioDetected}, errors: map[string]bool{id: true}}
	video := &fakeVideoCorroborator{state: VideoNoSignal}
	got := (&evaluator{proposer: proposer, proposerIdentity: identity, audioExtractor: &fakeAudioExtractor{}, audio: audio, video: video}).evaluate(context.Background(), plan)
	if got.Result.Outcome != OutcomeHold || !slices.Equal(got.Result.Reasons, []Reason{ReasonAudioFailure}) || video.calls != 0 {
		t.Fatalf("evaluation=%+v video=%d", got, video.calls)
	}
}
