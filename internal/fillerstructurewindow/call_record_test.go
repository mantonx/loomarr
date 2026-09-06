package fillerstructurewindow

import (
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillerstructure"
)

func TestRecordedAssessmentBindsAcceptedCallAndReplaysProjectedTimeline(t *testing.T) {
	set := callRecordMediaSetFixture(t)
	input := acceptedCallRecordInput(set)
	recorded, err := NewRecordedAssessment(input)
	if err != nil {
		t.Fatal(err)
	}
	window := set.Plan.Windows[input.WindowOrdinal]
	if recorded.Record.State != fillerstructure.AssessmentRecordAccepted ||
		recorded.Record.AssessmentSHA256 != recorded.Assessment.SHA256 ||
		recorded.Record.SHA256 != CallRecordSHA256(recorded.Record) ||
		recorded.Assessment.Segments[0].StartMS != window.MediaStartMS ||
		recorded.Assessment.Segments[len(recorded.Assessment.Segments)-1].EndMS != window.MediaEndMS {
		t.Fatalf("recorded assessment = %+v", recorded)
	}
	if err := ValidateRecordedAssessment(recorded); err != nil {
		t.Fatal(err)
	}
}

func TestRecordedAssessmentClosesEveryOperationalStateWithoutSegments(t *testing.T) {
	set := callRecordMediaSetFixture(t)
	tests := []struct {
		name   string
		state  fillerstructure.AssessmentRecordState
		fail   string
		mutate func(*CallRecordInput)
	}{
		{name: "provider", state: fillerstructure.AssessmentRecordFailed, fail: fillerstructure.AssessmentFailureProvider},
		{name: "invalid response", state: fillerstructure.AssessmentRecordFailed, fail: fillerstructure.AssessmentFailureInvalidResponse, mutate: func(input *CallRecordInput) {
			input.ResolvedProvider, input.ResolvedModel = "openrouter", "resolved-model"
			input.StructuredOutput = `{"segments":[]}`
		}},
		{name: "route mismatch", state: fillerstructure.AssessmentRecordFailed, fail: fillerstructure.AssessmentFailureRouteMismatch},
		{name: "transport", state: fillerstructure.AssessmentRecordUnsettled, fail: fillerstructure.AssessmentFailureTransport, mutate: func(input *CallRecordInput) {
			input.RawResponse, input.StructuredOutput = nil, ""
			input.GenerationID, input.ChargeKnown, input.ChargedAmountUSD, input.ChargedNanoUSD = "", false, "", 0
			input.AccountedNanoUSD = input.ReservedNanoUSD
		}},
		{name: "unsettled", state: fillerstructure.AssessmentRecordUnsettled, fail: fillerstructure.AssessmentFailureUnsettled, mutate: func(input *CallRecordInput) {
			input.StructuredOutput = ""
			input.GenerationID, input.ChargeKnown, input.ChargedAmountUSD, input.ChargedNanoUSD = "", false, "", 0
			input.AccountedNanoUSD = input.ReservedNanoUSD
		}},
		{name: "budget", state: fillerstructure.AssessmentRecordHeldBudget, fail: fillerstructure.AssessmentFailureBudget, mutate: func(input *CallRecordInput) {
			input.RawResponse, input.StructuredOutput = nil, ""
			input.GenerationID, input.ChargeKnown, input.ChargedAmountUSD, input.ChargedNanoUSD = "", false, "", 0
			input.Tokens = fillerstructure.AssessmentTokenUsage{}
			input.ReservedNanoUSD, input.AccountedNanoUSD = 0, 0
		}},
		{name: "over reservation", state: fillerstructure.AssessmentRecordOverReservation, fail: fillerstructure.AssessmentFailureOverReservation, mutate: func(input *CallRecordInput) {
			input.ChargedAmountUSD, input.ChargedNanoUSD, input.AccountedNanoUSD = "0.000003", 3_000, 3_000
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := acceptedCallRecordInput(set)
			input.State, input.Failure = test.state, test.fail
			input.ResolvedProvider, input.ResolvedModel = "", ""
			if test.mutate != nil {
				test.mutate(&input)
			}
			recorded, err := NewRecordedAssessment(input)
			if err != nil {
				t.Fatal(err)
			}
			if recorded.Assessment.State != AssessmentOperationalFailure || recorded.Assessment.Failure != test.fail ||
				len(recorded.Assessment.Segments) != 0 || recorded.Record.AssessmentSHA256 != recorded.Assessment.SHA256 {
				t.Fatalf("recorded assessment = %+v", recorded)
			}
		})
	}
}

func TestRecordedAssessmentRejectsTamperedBytesAssessmentAndAccounting(t *testing.T) {
	set := callRecordMediaSetFixture(t)
	valid, err := NewRecordedAssessment(acceptedCallRecordInput(set))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*RecordedAssessment)
	}{
		{name: "raw response", mutate: func(value *RecordedAssessment) { value.RawResponse = []byte("drift") }},
		{name: "metadata snapshot", mutate: func(value *RecordedAssessment) {
			value.Record.MetadataSnapshotSHA256 = ""
			value.Record.SHA256 = CallRecordSHA256(value.Record)
		}},
		{name: "structured output", mutate: func(value *RecordedAssessment) { value.StructuredOutput += " " }},
		{name: "semantic assessment", mutate: func(value *RecordedAssessment) { value.Assessment.Segments[0].EndMS++ }},
		{name: "accounted charge", mutate: func(value *RecordedAssessment) {
			value.Record.AccountedNanoUSD++
			value.Record.SHA256 = CallRecordSHA256(value.Record)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := valid
			value.RawResponse = append([]byte(nil), valid.RawResponse...)
			value.Assessment.Segments = append([]fillerstructure.Segment(nil), valid.Assessment.Segments...)
			test.mutate(&value)
			if err := ValidateRecordedAssessment(value); err == nil {
				t.Fatal("tampered recorded assessment was accepted")
			}
		})
	}
}

func callRecordMediaSetFixture(t *testing.T) MediaSet {
	t.Helper()
	plan, err := NewPlan(sourceFixture(300_000))
	if err != nil {
		t.Fatal(err)
	}
	return mediaSetForPlan(t, plan)
}

func acceptedCallRecordInput(set MediaSet) CallRecordInput {
	ordinal := 1
	window := set.Plan.Windows[ordinal]
	duration := window.MediaEndMS - window.MediaStartMS
	return CallRecordInput{
		MediaSet: set, WindowOrdinal: ordinal, Assessor: windowCallProfileFixture(),
		MetadataSnapshotSHA256: strings.Repeat("8", 64),
		PromptSHA256:           DirectVideoPromptSHA256(duration), SchemaSHA256: DirectVideoSchemaSHA256(duration),
		RequestSHA256: strings.Repeat("9", 64), RawResponse: []byte("provider response"),
		StructuredOutput: `{"segments":[{"endMs":15000,"role":"commercial","decisiveAtMs":[1000],"reason":"offer"},{"endMs":` + integerString(duration) + `,"role":"promo","decisiveAtMs":[20000],"reason":"promotion"}]}`,
		ResolvedProvider: "openrouter", ResolvedModel: "resolved-model", UpstreamProvider: "Provider",
		UpstreamProviderSlug: "provider", GenerationID: "generation-1",
		Tokens:           fillerstructure.AssessmentTokenUsage{Prompt: 100, Completion: 20, Video: 80},
		RequestedNanoUSD: 2_000, ReservedNanoUSD: 2_000, ChargedAmountUSD: "0.000001",
		ChargedNanoUSD: 1_000, AccountedNanoUSD: 1_000, ChargeKnown: true,
		State: fillerstructure.AssessmentRecordAccepted, AssessedAt: time.Date(2026, time.September, 12, 1, 2, 3, 0, time.UTC),
	}
}

func windowCallProfileFixture() fillerstructure.AssessorProfile {
	return fillerstructure.AssessorProfile{
		ID: "openrouter-a", ModelFamily: "family-a", Provider: "openrouter", Model: "requested-model",
		ModelDigest: strings.Repeat("a", 64), CapabilitySHA256: strings.Repeat("b", 64),
		PromptVersion: DirectVideoPromptVersion, EvidenceContract: CallRecordContractVersion,
	}
}
