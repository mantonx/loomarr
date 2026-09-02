package fillersafety

import (
	"reflect"
	"testing"
)

func TestReduceAppliesAsymmetricCascade(t *testing.T) {
	t.Parallel()
	candidates := []Candidate{{ID: "candidate-a", StartMS: 100, EndMS: 300}, {ID: "candidate-b", StartMS: 500, EndMS: 800}}
	tests := []struct {
		name     string
		evidence Evidence
		want     Result
	}{
		{name: "no candidates still requires video", evidence: Evidence{ProposalState: ProposalComplete, Video: VideoNoSignal}, want: Result{Outcome: OutcomeCandidateRejected, Reasons: []Reason{}}},
		{name: "all absences reject candidate", evidence: Evidence{ProposalState: ProposalComplete, Candidates: candidates, Audio: audio(candidates, AudioAbsent, AudioAbsent), Video: VideoNoSignal}, want: Result{Outcome: OutcomeCandidateRejected, Reasons: []Reason{}}},
		{name: "audio detection quarantines", evidence: Evidence{ProposalState: ProposalComplete, Candidates: candidates, Audio: audio(candidates, AudioDetected, AudioAbsent), Video: VideoNotRun}, want: Result{Outcome: OutcomeQuarantine, Reasons: []Reason{ReasonAudioProhibitedSignal}}},
		{name: "valid presence outranks another hold", evidence: Evidence{ProposalState: ProposalComplete, Candidates: candidates, Audio: audio(candidates, AudioFailed, AudioDetected), Video: VideoNotRun}, want: Result{Outcome: OutcomeQuarantine, Reasons: []Reason{ReasonAudioFailure, ReasonAudioProhibitedSignal}}},
		{name: "video detection quarantines", evidence: Evidence{ProposalState: ProposalComplete, Candidates: candidates, Audio: audio(candidates, AudioAbsent, AudioAbsent), Video: VideoProhibited}, want: Result{Outcome: OutcomeQuarantine, Reasons: []Reason{ReasonVideoProhibitedSignal}}},
		{name: "audio unclear holds", evidence: Evidence{ProposalState: ProposalComplete, Candidates: candidates, Audio: audio(candidates, AudioUnclear, AudioAbsent), Video: VideoNotRun}, want: Result{Outcome: OutcomeHold, Reasons: []Reason{ReasonAudioUnclear}}},
		{name: "audio failure holds", evidence: Evidence{ProposalState: ProposalComplete, Candidates: candidates, Audio: audio(candidates, AudioInvalidResponse, AudioAbsent), Video: VideoNotRun}, want: Result{Outcome: OutcomeHold, Reasons: []Reason{ReasonAudioFailure}}},
		{name: "unprojectable audio presence holds", evidence: Evidence{ProposalState: ProposalComplete, Candidates: candidates, Audio: audio(candidates, AudioDetectedUnprojectable, AudioAbsent), Video: VideoNotRun}, want: Result{Outcome: OutcomeHold, Reasons: []Reason{ReasonPresenceUnprojectable}}},
		{name: "unprojectable video presence holds", evidence: Evidence{ProposalState: ProposalComplete, Candidates: candidates, Audio: audio(candidates, AudioAbsent, AudioAbsent), Video: VideoProhibitedUnprojectable}, want: Result{Outcome: OutcomeHold, Reasons: []Reason{ReasonPresenceUnprojectable}}},
		{name: "incomplete video holds", evidence: Evidence{ProposalState: ProposalComplete, Candidates: candidates, Audio: audio(candidates, AudioAbsent, AudioAbsent), Video: VideoIncomplete}, want: Result{Outcome: OutcomeHold, Reasons: []Reason{ReasonVideoIncomplete}}},
		{name: "video failure holds", evidence: Evidence{ProposalState: ProposalComplete, Candidates: candidates, Audio: audio(candidates, AudioAbsent, AudioAbsent), Video: VideoFailed}, want: Result{Outcome: OutcomeHold, Reasons: []Reason{ReasonVideoFailure}}},
		{name: "proposal failure holds", evidence: Evidence{ProposalState: ProposalFailed, Video: VideoNotRun}, want: Result{Outcome: OutcomeHold, Reasons: []Reason{ReasonProposalFailure}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := Reduce(test.evidence); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("Reduce()=%+v want=%+v", got, test.want)
			}
		})
	}
}

func TestReduceHoldsInvalidOrOutOfOrderEvidence(t *testing.T) {
	t.Parallel()
	valid := Candidate{ID: "candidate-a", StartMS: 100, EndMS: 300}
	tests := []Evidence{
		{},
		{ProposalState: ProposalFailed, Candidates: []Candidate{valid}, Video: VideoNotRun},
		{ProposalState: ProposalComplete, Candidates: []Candidate{{ID: "", StartMS: 100, EndMS: 300}}, Audio: []AudioAssessment{{State: AudioAbsent}}, Video: VideoNoSignal},
		{ProposalState: ProposalComplete, Candidates: []Candidate{{ID: "candidate-a", StartMS: 300, EndMS: 100}}, Audio: []AudioAssessment{{CandidateID: "candidate-a", State: AudioAbsent}}, Video: VideoNoSignal},
		{ProposalState: ProposalComplete, Candidates: []Candidate{valid}, Video: VideoNoSignal},
		{ProposalState: ProposalComplete, Candidates: []Candidate{valid}, Audio: []AudioAssessment{{CandidateID: "other", State: AudioAbsent}}, Video: VideoNoSignal},
		{ProposalState: ProposalComplete, Candidates: []Candidate{valid}, Audio: []AudioAssessment{{CandidateID: valid.ID, State: "future"}}, Video: VideoNoSignal},
		{ProposalState: ProposalComplete, Candidates: []Candidate{valid}, Audio: []AudioAssessment{{CandidateID: valid.ID, State: AudioDetected}}, Video: VideoNoSignal},
		{ProposalState: ProposalComplete, Candidates: []Candidate{valid}, Audio: []AudioAssessment{{CandidateID: valid.ID, State: AudioAbsent}}, Video: VideoNotRun},
	}
	for index, evidence := range tests {
		if got := Reduce(evidence); !reflect.DeepEqual(got, Result{Outcome: OutcomeHold, Reasons: []Reason{ReasonInvalidEvidence}}) {
			t.Fatalf("case %d: Reduce()=%+v", index, got)
		}
	}
}

func TestReduceRequiresCanonicalCandidateOrder(t *testing.T) {
	t.Parallel()
	candidates := []Candidate{{ID: "candidate-b", StartMS: 500, EndMS: 800}, {ID: "candidate-a", StartMS: 100, EndMS: 300}}
	got := Reduce(Evidence{ProposalState: ProposalComplete, Candidates: candidates, Audio: audio(candidates, AudioAbsent, AudioAbsent), Video: VideoNoSignal})
	if !reflect.DeepEqual(got, Result{Outcome: OutcomeHold, Reasons: []Reason{ReasonInvalidEvidence}}) {
		t.Fatalf("Reduce()=%+v", got)
	}
}

func audio(candidates []Candidate, states ...AudioState) []AudioAssessment {
	assessments := make([]AudioAssessment, len(candidates))
	for index := range candidates {
		assessments[index] = AudioAssessment{CandidateID: candidates[index].ID, State: states[index]}
	}
	return assessments
}
