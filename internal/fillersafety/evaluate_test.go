package fillersafety

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/loomarr/loomarr/internal/testkit/recordfixture"
)

func validHostedCallIdentityFixture() hostedCallIdentity {
	return hostedCallIdentity{
		RequestedProvider: "openrouter", RequestedModel: "vendor/model",
		ResolvedProvider: "openrouter", ResolvedModel: "vendor/model-2026",
		UpstreamProvider: "Pinned Provider", CapabilitySHA256: strings.Repeat("a", 64),
		PromptSHA256: strings.Repeat("b", 64), SchemaSHA256: strings.Repeat("c", 64), MaxChargeNanoUSD: 100,
	}
}

func TestEvaluateRunsSerialCascadeAndRejectsOnlyTwoModelNegatives(t *testing.T) {
	t.Parallel()
	plan, identity := proposalTestPlan(t), validProposerIdentityFixture()
	proposer := proposalRecorder(identity, []proposedInterval{{StartMS: 2_000, EndMS: 2_500}, {StartMS: 100, EndMS: 800}})
	extractor := wavRecorder(nil)
	states := map[string]AudioState{}
	audio := &recordfixture.Recorder[Candidate, audioAttempt]{Respond: func(candidate Candidate) (audioAttempt, error) {
		return audioAttempt{Assessment: AudioAssessment{CandidateID: candidate.ID, State: states[candidate.ID]}, MatchedRuleIDs: []string{}}, nil
	}}
	video := videoRecorder(VideoNoSignal)
	eval := evaluatorFromRecorders(identity, proposer, extractor, audio, video)
	firstID := proposalCandidateID(plan.AuthoritySHA256, proposedInterval{StartMS: 100, EndMS: 800})
	secondID := proposalCandidateID(plan.AuthoritySHA256, proposedInterval{StartMS: 2_000, EndMS: 2_500})
	states[firstID], states[secondID] = AudioAbsent, AudioAbsent
	got, err := eval.evaluate(context.Background(), plan, unrecordedCascadeJournal{})
	if err != nil {
		t.Fatal(err)
	}
	extractCalls, audioCalls := extractor.Inputs(), audio.Inputs()
	if got.Result.Outcome != OutcomeCandidateRejected || got.VideoAttempt == nil || got.VideoAttempt.State != VideoNoSignal || video.Calls() != 1 || !slices.Equal([]string{extractCalls[0].ID, extractCalls[1].ID}, []string{firstID, secondID}) || !slices.Equal([]string{audioCalls[0].ID, audioCalls[1].ID}, []string{firstID, secondID}) {
		t.Fatalf("evaluation=%+v extract=%v audio=%v video=%d", got, extractCalls, audioCalls, video.Calls())
	}
}

func TestEvaluatePresenceOutranksFailureAndSkipsVideo(t *testing.T) {
	t.Parallel()
	plan, identity := proposalTestPlan(t), validProposerIdentityFixture()
	intervals := []proposedInterval{{StartMS: 100, EndMS: 800}, {StartMS: 2_000, EndMS: 2_500}}
	firstID := proposalCandidateID(plan.AuthoritySHA256, intervals[0])
	secondID := proposalCandidateID(plan.AuthoritySHA256, intervals[1])
	extractor := wavRecorder(map[string]bool{firstID: true})
	audio := &recordfixture.Recorder[Candidate, audioAttempt]{Respond: func(candidate Candidate) (audioAttempt, error) {
		return audioAttempt{Assessment: AudioAssessment{CandidateID: candidate.ID, State: map[string]AudioState{secondID: AudioDetected}[candidate.ID]}, MatchedRuleIDs: []string{}}, nil
	}}
	video := videoRecorder(VideoNoSignal)
	got, err := evaluatorFromRecorders(identity, proposalRecorder(identity, intervals), extractor, audio, video).evaluate(context.Background(), plan, unrecordedCascadeJournal{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Result.Outcome != OutcomeQuarantine || !slices.Equal(got.Result.Reasons, []Reason{ReasonAudioFailure, ReasonAudioProhibitedSignal}) || video.Calls() != 0 {
		t.Fatalf("evaluation=%+v video=%d", got, video.Calls())
	}
}

func TestEvaluateNoCandidatesStillRequiresCompleteVideo(t *testing.T) {
	t.Parallel()
	plan, identity := proposalTestPlan(t), validProposerIdentityFixture()
	video := videoRecorder(VideoIncomplete)
	got, err := evaluatorFromRecorders(identity, proposalRecorder(identity, nil), wavRecorder(nil), absentAudioRecorder(), video).evaluate(context.Background(), plan, unrecordedCascadeJournal{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Result.Outcome != OutcomeHold || !slices.Equal(got.Result.Reasons, []Reason{ReasonVideoIncomplete}) || video.Calls() != 1 {
		t.Fatalf("evaluation=%+v video=%d", got, video.Calls())
	}
}

func TestEvaluateAdapterErrorsCannotSmugglePresence(t *testing.T) {
	t.Parallel()
	plan, identity := proposalTestPlan(t), validProposerIdentityFixture()
	interval := proposedInterval{StartMS: 100, EndMS: 800}
	audio := &recordfixture.Recorder[Candidate, audioAttempt]{Respond: func(candidate Candidate) (audioAttempt, error) {
		return audioAttempt{Assessment: AudioAssessment{CandidateID: candidate.ID, State: AudioDetected}, MatchedRuleIDs: []string{}}, errors.New("private provider detail")
	}}
	video := videoRecorder(VideoNoSignal)
	got, err := evaluatorFromRecorders(identity, proposalRecorder(identity, []proposedInterval{interval}), wavRecorder(nil), audio, video).evaluate(context.Background(), plan, unrecordedCascadeJournal{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Result.Outcome != OutcomeHold || !slices.Equal(got.Result.Reasons, []Reason{ReasonAudioFailure}) || video.Calls() != 0 {
		t.Fatalf("evaluation=%+v video=%d", got, video.Calls())
	}
}

func proposalRecorder(identity proposerIdentity, intervals []proposedInterval) *recordfixture.Recorder[proposalRequest, proposalOutput] {
	return &recordfixture.Recorder[proposalRequest, proposalOutput]{Respond: func(proposalRequest) (proposalOutput, error) {
		return proposalOutput{Identity: identity, Complete: true, Candidates: intervals}, nil
	}}
}

func wavRecorder(fail map[string]bool) *recordfixture.Recorder[Candidate, []byte] {
	return &recordfixture.Recorder[Candidate, []byte]{Respond: func(candidate Candidate) ([]byte, error) {
		if fail[candidate.ID] {
			return nil, errors.New("private extraction detail")
		}
		return validCandidateWAV(), nil
	}}
}

func absentAudioRecorder() *recordfixture.Recorder[Candidate, audioAttempt] {
	return &recordfixture.Recorder[Candidate, audioAttempt]{Respond: func(candidate Candidate) (audioAttempt, error) {
		return audioAttempt{Assessment: AudioAssessment{CandidateID: candidate.ID, State: AudioAbsent}, MatchedRuleIDs: []string{}}, nil
	}}
}

func videoRecorder(state VideoState) *recordfixture.Recorder[*CompleteMediaPlan, videoAttempt] {
	return &recordfixture.Recorder[*CompleteMediaPlan, videoAttempt]{Respond: func(*CompleteMediaPlan) (videoAttempt, error) {
		return videoAttempt{State: state, Flags: []videoFlag{}}, nil
	}}
}

func evaluatorFromRecorders(identity proposerIdentity, proposer *recordfixture.Recorder[proposalRequest, proposalOutput], extractor *recordfixture.Recorder[Candidate, []byte], audio *recordfixture.Recorder[Candidate, audioAttempt], video *recordfixture.Recorder[*CompleteMediaPlan, videoAttempt]) *evaluator {
	return &evaluator{
		proposer: func(_ context.Context, request proposalRequest) (proposalOutput, error) {
			return proposer.Call(request)
		}, proposerIdentity: identity,
		audioExtractor: func(_ context.Context, _ *CompleteMediaPlan, candidate Candidate) ([]byte, error) {
			return extractor.Call(candidate)
		},
		audio: audioAdjudicatorFuncs{
			identify: func(int64) hostedCallIdentity { return validHostedCallIdentityFixture() },
			invoke: func(_ context.Context, candidate Candidate, _ []byte, reserve func(string) error) (audioAttempt, error) {
				if err := reserve(strings.Repeat("d", 64)); err != nil {
					return audioAttempt{Assessment: AudioAssessment{CandidateID: candidate.ID, State: AudioFailed}, MatchedRuleIDs: []string{}}, err
				}
				return audio.Call(candidate)
			},
		},
		video: videoCorroboratorFuncs{
			identify: func(int64) hostedCallIdentity { return validHostedCallIdentityFixture() },
			invoke: func(_ context.Context, plan *CompleteMediaPlan, reserve func(string) error) (videoAttempt, error) {
				if err := reserve(strings.Repeat("e", 64)); err != nil {
					return videoAttempt{State: VideoFailed, Flags: []videoFlag{}}, err
				}
				return video.Call(plan)
			},
		},
	}
}
