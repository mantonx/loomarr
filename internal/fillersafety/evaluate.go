package fillersafety

import "context"

type audioAdjudicator interface {
	identity(int64) hostedCallIdentity
	adjudicate(context.Context, Candidate, []byte, func(string) error) (audioAttempt, error)
}

type videoCorroborator interface {
	identity(int64) hostedCallIdentity
	corroborate(context.Context, *CompleteMediaPlan, func(string) error) (videoAttempt, error)
}

type cascadeJournal interface {
	proposal(context.Context, Evidence) error
	audio(context.Context, audioAdjudicator, *CompleteMediaPlan, Candidate, []byte) (audioAttempt, error)
	video(context.Context, videoCorroborator, *CompleteMediaPlan) (videoAttempt, error)
}

// Function adapters keep the private invocation seam narrow while carrying
// the route identity that must be journalled before the transport is invoked.
type audioAdjudicatorFuncs struct {
	identify func(int64) hostedCallIdentity
	invoke   func(context.Context, Candidate, []byte, func(string) error) (audioAttempt, error)
}

func (a audioAdjudicatorFuncs) identity(durationMS int64) hostedCallIdentity {
	return a.identify(durationMS)
}

func (a audioAdjudicatorFuncs) adjudicate(ctx context.Context, candidate Candidate, wav []byte, reserve func(string) error) (audioAttempt, error) {
	return a.invoke(ctx, candidate, wav, reserve)
}

type videoCorroboratorFuncs struct {
	identify func(int64) hostedCallIdentity
	invoke   func(context.Context, *CompleteMediaPlan, func(string) error) (videoAttempt, error)
}

func (v videoCorroboratorFuncs) identity(durationMS int64) hostedCallIdentity {
	return v.identify(durationMS)
}

func (v videoCorroboratorFuncs) corroborate(ctx context.Context, plan *CompleteMediaPlan, reserve func(string) error) (videoAttempt, error) {
	return v.invoke(ctx, plan, reserve)
}

type evaluator struct {
	proposer         acousticProposer
	proposerIdentity proposerIdentity
	audioExtractor   candidateAudioExtractor
	audio            audioAdjudicator
	video            videoCorroborator
}

type evaluation struct {
	Evidence      Evidence
	Result        Result
	AudioAttempts []audioAttempt
	VideoAttempt  *videoAttempt
}

func (e *evaluator) evaluate(ctx context.Context, plan *CompleteMediaPlan, journal cascadeJournal) (evaluation, error) {
	evidence := runProposal(ctx, e.proposer, e.proposerIdentity, plan)
	completed := evaluation{Evidence: evidence, AudioAttempts: []audioAttempt{}}
	if journal == nil {
		completed.Result = Reduce(evidence)
		return completed, ErrEvaluationInvalid
	}
	if err := journal.proposal(ctx, evidence); err != nil {
		completed.Result = Reduce(evidence)
		return completed, err
	}
	if evidence.ProposalState != ProposalComplete || e.audioExtractor == nil || e.audio == nil || e.video == nil {
		completed.Result = Reduce(evidence)
		return completed, nil
	}

	evidence.Audio = make([]AudioAssessment, 0, len(evidence.Candidates))
	for _, candidate := range evidence.Candidates {
		assessment := AudioAssessment{CandidateID: candidate.ID, State: AudioFailed}
		wav, err := e.audioExtractor(ctx, plan, candidate)
		if err != nil {
			evidence.Audio = append(evidence.Audio, assessment)
			completed.AudioAttempts = append(completed.AudioAttempts, audioAttempt{Assessment: assessment, MatchedRuleIDs: []string{}})
			continue
		}
		attempt, err := journal.audio(ctx, e.audio, plan, candidate, wav)
		if err != nil {
			completed.Evidence = evidence
			completed.Result = Reduce(evidence)
			return completed, err
		}
		evidence.Audio = append(evidence.Audio, attempt.Assessment)
		completed.AudioAttempts = append(completed.AudioAttempts, attempt)
	}

	allAbsent := true
	for _, assessment := range evidence.Audio {
		if assessment.State != AudioAbsent {
			allAbsent = false
			break
		}
	}
	if allAbsent {
		attempt, err := journal.video(ctx, e.video, plan)
		if err != nil {
			completed.Evidence = evidence
			completed.Result = Reduce(evidence)
			return completed, err
		}
		evidence.Video = attempt.State
		completed.VideoAttempt = &attempt
	}
	completed.Evidence = evidence
	completed.Result = Reduce(evidence)
	return completed, nil
}

type unrecordedCascadeJournal struct{}

func (unrecordedCascadeJournal) proposal(context.Context, Evidence) error { return nil }

func (unrecordedCascadeJournal) audio(
	ctx context.Context,
	adapter audioAdjudicator,
	_ *CompleteMediaPlan,
	candidate Candidate,
	wav []byte,
) (audioAttempt, error) {
	attempt, err := adapter.adjudicate(ctx, candidate, wav, func(string) error { return nil })
	return normalizedAudioAttempt(candidate, attempt, err), nil
}

func (unrecordedCascadeJournal) video(
	ctx context.Context,
	adapter videoCorroborator,
	plan *CompleteMediaPlan,
) (videoAttempt, error) {
	attempt, err := adapter.corroborate(ctx, plan, func(string) error { return nil })
	return normalizedVideoAttempt(attempt, err), nil
}

func normalizedAudioAttempt(candidate Candidate, attempt audioAttempt, callErr error) audioAttempt {
	if callErr != nil && attempt.Assessment.State != AudioInvalidResponse {
		attempt.Assessment = AudioAssessment{CandidateID: candidate.ID, State: AudioFailed}
	}
	if attempt.Assessment.CandidateID != candidate.ID || !validAudioState(attempt.Assessment.State) {
		attempt.Assessment = AudioAssessment{CandidateID: candidate.ID, State: AudioInvalidResponse}
	}
	return attempt
}

func normalizedVideoAttempt(attempt videoAttempt, callErr error) videoAttempt {
	if callErr != nil && attempt.State != VideoProhibitedUnprojectable && attempt.State != VideoIncomplete && attempt.State != VideoInvalidResponse {
		attempt.State = VideoFailed
	}
	if !validCompletedVideoState(attempt.State) {
		attempt.State = VideoInvalidResponse
	}
	return attempt
}
