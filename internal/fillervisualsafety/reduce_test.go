package fillervisualsafety_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillervisualsafety"
)

func TestReduceNeverTurnsNegativeVotesIntoAdmission(t *testing.T) {
	authority, plan, coverage := visualEvidenceFixture(t)
	portable := visualObservation(t, authority, coverage, fillervisualsafety.ProducerPortable, fillervisualsafety.ObservationNoSignal)
	apple := visualObservation(t, authority, coverage, fillervisualsafety.ProducerAppleSCA, fillervisualsafety.ObservationNoSignal)

	result := fillervisualsafety.Reduce(authority, coverage, plan, []fillervisualsafety.Observation{apple, portable})
	if err := fillervisualsafety.ValidateResult(result); err != nil {
		t.Fatal(err)
	}
	if result.Outcome != fillervisualsafety.OutcomeNoSignal || result.QuarantineRequired || result.ProductionAdmissionAllowed {
		t.Fatalf("negative observations gained authority: %#v", result)
	}
}

func TestReduceAnyValidPositiveQuarantinesDespiteDisagreementOrFailure(t *testing.T) {
	authority, plan, coverage := visualEvidenceFixture(t)
	portable := visualObservation(t, authority, coverage, fillervisualsafety.ProducerPortable, fillervisualsafety.ObservationNoSignal)
	direct := visualObservation(t, authority, coverage, fillervisualsafety.ProducerDirectVideo, fillervisualsafety.ObservationFailed)
	apple := visualObservation(t, authority, coverage, fillervisualsafety.ProducerAppleSCA, fillervisualsafety.ObservationProhibited)

	result := fillervisualsafety.Reduce(authority, coverage, plan, []fillervisualsafety.Observation{direct, portable, apple})
	if err := fillervisualsafety.ValidateResult(result); err != nil {
		t.Fatal(err)
	}
	if result.Outcome != fillervisualsafety.OutcomeQuarantine || !result.QuarantineRequired ||
		!slices.Contains(result.Reasons, fillervisualsafety.ReasonAppleProhibited) ||
		!slices.Contains(result.Reasons, fillervisualsafety.ReasonLaneFailed) {
		t.Fatalf("positive did not conservatively quarantine: %#v", result)
	}
}

func TestReduceMissingOrIncompletePortableCoverageHolds(t *testing.T) {
	authority, plan, coverage := visualEvidenceFixture(t)
	apple := visualObservation(t, authority, coverage, fillervisualsafety.ProducerAppleSCA, fillervisualsafety.ObservationNoSignal)

	missing := fillervisualsafety.Reduce(authority, coverage, plan, []fillervisualsafety.Observation{apple})
	if missing.Outcome != fillervisualsafety.OutcomeHold || !slices.Contains(missing.Reasons, fillervisualsafety.ReasonPortableMissing) {
		t.Fatalf("missing portable lane did not hold: %#v", missing)
	}

	portable := visualObservation(t, authority, coverage, fillervisualsafety.ProducerPortable, fillervisualsafety.ObservationIncomplete)
	incomplete := fillervisualsafety.Reduce(authority, coverage, plan, []fillervisualsafety.Observation{portable})
	if incomplete.Outcome != fillervisualsafety.OutcomeHold || !slices.Contains(incomplete.Reasons, fillervisualsafety.ReasonLaneIncomplete) {
		t.Fatalf("incomplete portable lane did not hold: %#v", incomplete)
	}
}

func TestReduceInvalidPositiveCannotCreateQuarantine(t *testing.T) {
	authority, plan, coverage := visualEvidenceFixture(t)
	portable := visualObservation(t, authority, coverage, fillervisualsafety.ProducerPortable, fillervisualsafety.ObservationNoSignal)
	apple := visualObservation(t, authority, coverage, fillervisualsafety.ProducerAppleSCA, fillervisualsafety.ObservationProhibited)
	apple.SourceSHA256 = strings.Repeat("f", 64)
	apple.SHA256 = fillervisualsafety.ObservationSHA256(apple)

	result := fillervisualsafety.Reduce(authority, coverage, plan, []fillervisualsafety.Observation{portable, apple})
	if result.Outcome != fillervisualsafety.OutcomeHold || result.QuarantineRequired ||
		!slices.Contains(result.Reasons, fillervisualsafety.ReasonInvalidEvidence) {
		t.Fatalf("invalid positive changed source state: %#v", result)
	}
}

func TestReduceRejectsSourceRelativeTimingOutsideTheAuthority(t *testing.T) {
	authority, plan, coverage := visualEvidenceFixture(t)
	portable := visualObservation(t, authority, coverage, fillervisualsafety.ProducerPortable, fillervisualsafety.ObservationNoSignal)
	direct := visualObservation(t, authority, coverage, fillervisualsafety.ProducerDirectVideo, fillervisualsafety.ObservationProhibited)
	direct.Intervals[0].EndMS = authority.DurationMS + 1
	direct.SHA256 = fillervisualsafety.ObservationSHA256(direct)

	result := fillervisualsafety.Reduce(authority, coverage, plan, []fillervisualsafety.Observation{portable, direct})
	if result.Outcome != fillervisualsafety.OutcomeHold || result.QuarantineRequired ||
		!slices.Contains(result.Reasons, fillervisualsafety.ReasonInvalidEvidence) {
		t.Fatalf("out-of-range positive changed source state: %#v", result)
	}
}

func TestNonApplePositiveRequiresAnInterval(t *testing.T) {
	authority, _, coverage := visualEvidenceFixture(t)
	observation := visualObservationInput(authority, coverage, fillervisualsafety.ProducerDirectVideo, fillervisualsafety.ObservationProhibited)
	observation.Intervals = []fillervisualsafety.Interval{}
	if _, err := fillervisualsafety.SealObservation(observation); err == nil {
		t.Fatal("expected interval-free direct-video positive to fail")
	}
}

func visualEvidenceFixture(t *testing.T) (fillervisualsafety.SourceAuthority, fillervisualsafety.CoveragePlan, fillervisualsafety.CoverageEvidence) {
	t.Helper()
	authority := visualAuthority(t, 3_050)
	plan, err := fillervisualsafety.PlanCoverage(authority, visualProfile(t))
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := fillervisualsafety.SealCoverageEvidence(
		plan,
		fillervisualsafety.ToolIdentity{Name: "ffmpeg", Version: "7.1", ExecutableSHA256: strings.Repeat("e", 64)},
		visualFrames(plan), true,
	)
	if err != nil {
		t.Fatal(err)
	}
	return authority, plan, evidence
}

func visualObservation(t *testing.T, authority fillervisualsafety.SourceAuthority, coverage fillervisualsafety.CoverageEvidence, family fillervisualsafety.ProducerFamily, state fillervisualsafety.ObservationState) fillervisualsafety.Observation {
	t.Helper()
	observation, err := fillervisualsafety.SealObservation(visualObservationInput(authority, coverage, family, state))
	if err != nil {
		t.Fatal(err)
	}
	return observation
}

func visualObservationInput(authority fillervisualsafety.SourceAuthority, coverage fillervisualsafety.CoverageEvidence, family fillervisualsafety.ProducerFamily, state fillervisualsafety.ObservationState) fillervisualsafety.Observation {
	observation := fillervisualsafety.Observation{
		SourceAuthoritySHA256: authority.SHA256, SourceSHA256: authority.SourceSHA256, PolicySHA256: authority.PolicySHA256,
		Profile: fillervisualsafety.ProducerProfile{
			Family: family, Implementation: string(family) + "-v1", CapabilitySHA256: strings.Repeat("1", 64), EvidenceContract: "visual-private-evidence-v1",
		},
		State: state, Intervals: []fillervisualsafety.Interval{}, PolicyMatchIDs: []string{},
		EvidenceSHA256: strings.Repeat("2", 64), AssessedAt: time.Date(2026, time.September, 4, 11, 0, 0, 0, time.UTC),
	}
	if family == fillervisualsafety.ProducerPortable {
		observation.CoverageEvidenceSHA256 = coverage.SHA256
	}
	if state == fillervisualsafety.ObservationProhibited {
		observation.PolicyMatchIDs = []string{"policy-match-1"}
		if family != fillervisualsafety.ProducerAppleSCA {
			observation.Intervals = []fillervisualsafety.Interval{{StartMS: 100, EndMS: 200}}
		}
	}
	return observation
}
