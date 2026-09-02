// Package fillersafety owns the fail-closed spoken-safety cascade and its
// shadow evidence. It never grants filler admission, ingestion, scheduling,
// or training authority.
package fillersafety

// Outcome is the closed shadow disposition of one complete-source evaluation.
type Outcome string

const (
	OutcomeQuarantine        Outcome = "quarantine"
	OutcomeHold              Outcome = "hold"
	OutcomeCandidateRejected Outcome = "candidate_rejected"
)

// Reason identifies why the reducer selected an outcome without carrying
// source text or restricted policy values.
type Reason string

const (
	ReasonAudioProhibitedSignal Reason = "audio_prohibited_signal"
	ReasonVideoProhibitedSignal Reason = "video_prohibited_signal"
	ReasonProposalFailure       Reason = "proposal_failure"
	ReasonAudioUnclear          Reason = "audio_unclear"
	ReasonAudioFailure          Reason = "audio_failure"
	ReasonVideoIncomplete       Reason = "video_incomplete"
	ReasonVideoFailure          Reason = "video_failure"
	ReasonPresenceUnprojectable Reason = "presence_unprojectable"
	ReasonInvalidEvidence       Reason = "invalid_evidence"
)

// ProposalState is the terminal state of the local complete-source proposal step.
type ProposalState string

const (
	ProposalComplete ProposalState = "complete"
	ProposalFailed   ProposalState = "failed"
)

// AudioState is the closed result of adjudicating one proposed interval.
type AudioState string

const (
	AudioDetected              AudioState = "detected"
	AudioDetectedUnprojectable AudioState = "detected_unprojectable"
	AudioAbsent                AudioState = "absent"
	AudioUnclear               AudioState = "unclear"
	AudioFailed                AudioState = "failed"
	AudioInvalidResponse       AudioState = "invalid_response"
)

// VideoState is the closed result of complete-source video/audio corroboration.
type VideoState string

const (
	VideoNotRun                  VideoState = "not_run"
	VideoProhibited              VideoState = "prohibited"
	VideoProhibitedUnprojectable VideoState = "prohibited_unprojectable"
	VideoNoSignal                VideoState = "no_signal"
	VideoIncomplete              VideoState = "incomplete"
	VideoFailed                  VideoState = "failed"
	VideoInvalidResponse         VideoState = "invalid_response"
)

// Candidate is one opaque source-relative interval from the local proposer.
type Candidate struct {
	ID      string
	StartMS int64
	EndMS   int64
}

// AudioAssessment binds one adjudication result to its proposed candidate.
type AudioAssessment struct {
	CandidateID    string
	State          AudioState
	MatchedRuleIDs []string
}

// Evidence is the complete ordered input to deterministic cascade reduction.
type Evidence struct {
	ProposalState ProposalState
	Candidates    []Candidate
	Audio         []AudioAssessment
	Video         VideoState
}

// Result is evidence only. It deliberately contains no admission permission.
type Result struct {
	Outcome Outcome
	Reasons []Reason
}
