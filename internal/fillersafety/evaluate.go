package fillersafety

import "context"

type audioAdjudicator interface {
	adjudicate(context.Context, Candidate, []byte) (audioAttempt, error)
}

type videoCorroborator interface {
	corroborate(context.Context, *CompleteMediaPlan) (videoAttempt, error)
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

func (e *evaluator) evaluate(ctx context.Context, plan *CompleteMediaPlan) evaluation {
	evidence := runProposal(ctx, e.proposer, e.proposerIdentity, plan)
	completed := evaluation{Evidence: evidence, AudioAttempts: []audioAttempt{}}
	if evidence.ProposalState != ProposalComplete || e.audioExtractor == nil || e.audio == nil || e.video == nil {
		completed.Result = Reduce(evidence)
		return completed
	}

	evidence.Audio = make([]AudioAssessment, 0, len(evidence.Candidates))
	for _, candidate := range evidence.Candidates {
		assessment := AudioAssessment{CandidateID: candidate.ID, State: AudioFailed}
		wav, err := e.audioExtractor.Extract(ctx, plan, candidate)
		if err != nil {
			evidence.Audio = append(evidence.Audio, assessment)
			completed.AudioAttempts = append(completed.AudioAttempts, audioAttempt{Assessment: assessment, MatchedRuleIDs: []string{}})
			continue
		}
		attempt, callErr := e.audio.adjudicate(ctx, candidate, wav)
		if callErr != nil && attempt.Assessment.State != AudioInvalidResponse {
			attempt.Assessment = assessment
		}
		if attempt.Assessment.CandidateID != candidate.ID || !validAudioState(attempt.Assessment.State) {
			attempt.Assessment = AudioAssessment{CandidateID: candidate.ID, State: AudioInvalidResponse}
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
		attempt, err := e.video.corroborate(ctx, plan)
		if err != nil && attempt.State != VideoProhibitedUnprojectable && attempt.State != VideoIncomplete && attempt.State != VideoInvalidResponse {
			attempt.State = VideoFailed
		}
		if !validCompletedVideoState(attempt.State) {
			attempt.State = VideoInvalidResponse
		}
		evidence.Video = attempt.State
		completed.VideoAttempt = &attempt
	}
	completed.Evidence = evidence
	completed.Result = Reduce(evidence)
	return completed
}
