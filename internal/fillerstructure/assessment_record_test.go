package fillerstructure

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestAssessmentRecordBindsRawResponseAndParsedTimeline(t *testing.T) {
	recorded := acceptedAssessmentRecord(t)
	if err := ValidateRecordedAssessment(recorded); err != nil {
		t.Fatal(err)
	}
	candidate, err := recorded.Record.Candidate()
	if err != nil {
		t.Fatal(err)
	}
	input, err := NewCompleteVideoInput(recorded.Record.Source, recorded.Record.Media)
	if err != nil {
		t.Fatal(err)
	}
	if candidate.Assessor.AssessmentSHA256 != recorded.Record.SHA256 || candidate.Unit != UnitCompilation ||
		candidate.Source != recorded.Record.Source || candidate.InputSHA256 != input.SHA256 ||
		len(candidate.Segments) != 2 || candidate.Segments[0].EndMS != 5_000 || candidate.Segments[1].Role != RolePromo {
		t.Fatalf("record=%+v candidate=%+v", recorded.Record, candidate)
	}
}

func TestRecordedAssessmentRejectsResponseOrInterpretationDrift(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*RecordedAssessment)
	}{
		{name: "raw response", mutate: func(value *RecordedAssessment) { value.RawResponse = []byte("replacement") }},
		{name: "structured output", mutate: func(value *RecordedAssessment) { value.StructuredOutput = `{"segments":[]}` }},
		{name: "parsed result", mutate: func(value *RecordedAssessment) {
			value.Record.Result.Segments[0].Role = RolePSA
			value.Record.SHA256 = AssessmentRecordSHA256(value.Record)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := acceptedAssessmentRecord(t)
			value.Record.Result = &AssessmentResult{
				Unit: value.Record.Result.Unit, Role: value.Record.Result.Role,
				Segments: slices.Clone(value.Record.Result.Segments),
			}
			test.mutate(&value)
			if err := ValidateRecordedAssessment(value); err == nil {
				t.Fatal("drifted recorded assessment was accepted")
			}
		})
	}
}

func TestAssessmentRecordProjectsClosedOperationalStatesWithoutClaims(t *testing.T) {
	tests := []struct {
		name    string
		state   AssessmentRecordState
		failure string
		mutate  func(*AssessmentRecordInput)
	}{
		{name: "provider failure", state: AssessmentRecordFailed, failure: AssessmentFailureProvider, mutate: func(input *AssessmentRecordInput) {
			input.ResolvedProvider, input.ResolvedModel = "", ""
		}},
		{name: "transport failure without response", state: AssessmentRecordUnsettled, failure: AssessmentFailureTransport, mutate: func(input *AssessmentRecordInput) {
			input.RawResponse, input.StructuredOutput = nil, ""
			input.ResolvedProvider, input.ResolvedModel, input.GenerationID = "", "", ""
			input.ChargeKnown, input.ChargedAmountUSD, input.ChargedNanoUSD = false, "", 0
			input.AccountedNanoUSD = input.ReservedNanoUSD
		}},
		{name: "unsettled", state: AssessmentRecordUnsettled, failure: AssessmentFailureUnsettled, mutate: func(input *AssessmentRecordInput) {
			input.RawResponse, input.StructuredOutput = nil, ""
			input.ResolvedProvider, input.ResolvedModel, input.GenerationID = "", "", ""
			input.ChargeKnown, input.ChargedAmountUSD, input.ChargedNanoUSD = false, "", 0
			input.AccountedNanoUSD = input.ReservedNanoUSD
		}},
		{name: "budget", state: AssessmentRecordHeldBudget, failure: AssessmentFailureBudget, mutate: func(input *AssessmentRecordInput) {
			input.RawResponse, input.StructuredOutput = nil, ""
			input.ResolvedProvider, input.ResolvedModel, input.GenerationID = "", "", ""
			input.ReservedNanoUSD, input.AccountedNanoUSD = 0, 0
			input.ChargeKnown, input.ChargedAmountUSD, input.ChargedNanoUSD = false, "", 0
		}},
		{name: "over reservation", state: AssessmentRecordOverReservation, failure: AssessmentFailureOverReservation, mutate: func(input *AssessmentRecordInput) {
			input.StructuredOutput = ""
			input.ChargedAmountUSD, input.ChargedNanoUSD, input.AccountedNanoUSD = "0.000002", 2_000, 2_000
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := acceptedAssessmentInput()
			input.State, input.Failure, input.StructuredOutput = test.state, test.failure, ""
			if test.mutate != nil {
				test.mutate(&input)
			}
			recorded, err := NewAssessmentRecord(input)
			if err != nil {
				t.Fatal(err)
			}
			candidate, err := recorded.Record.Candidate()
			if err != nil || candidate.Failure != test.failure || candidate.Unit != "" || candidate.Role != "" || len(candidate.Segments) != 0 {
				t.Fatalf("candidate=%+v error=%v", candidate, err)
			}
		})
	}
}

func TestAssessmentRecordRejectsOpenOrContradictorySettlement(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*AssessmentRecordInput)
	}{
		{name: "accepted without response", mutate: func(input *AssessmentRecordInput) { input.RawResponse = nil }},
		{name: "metadata snapshot missing", mutate: func(input *AssessmentRecordInput) { input.MetadataSnapshotSHA256 = "" }},
		{name: "media profile missing", mutate: func(input *AssessmentRecordInput) { input.Media.ProfileSHA256 = "" }},
		{name: "media lineage missing", mutate: func(input *AssessmentRecordInput) { input.Media.LineageSHA256 = "" }},
		{name: "media duration drift", mutate: func(input *AssessmentRecordInput) { input.Media.DurationMS += 1_001 }},
		{name: "accepted over reservation", mutate: func(input *AssessmentRecordInput) { input.ChargedNanoUSD = 2_000; input.AccountedNanoUSD = 2_000 }},
		{name: "failed without closed reason", mutate: func(input *AssessmentRecordInput) {
			input.State, input.Failure, input.StructuredOutput = AssessmentRecordFailed, "", ""
		}},
		{name: "unsettled drops accounting", mutate: func(input *AssessmentRecordInput) {
			input.State, input.Failure, input.StructuredOutput = AssessmentRecordUnsettled, AssessmentFailureUnsettled, ""
			input.ChargeKnown, input.ChargedAmountUSD, input.ChargedNanoUSD, input.AccountedNanoUSD = false, "", 0, 0
		}},
		{name: "failed charge exceeds reservation", mutate: func(input *AssessmentRecordInput) {
			input.State, input.Failure, input.StructuredOutput = AssessmentRecordFailed, AssessmentFailureProvider, ""
			input.ResolvedProvider, input.ResolvedModel = "", ""
			input.ChargedAmountUSD, input.ChargedNanoUSD, input.AccountedNanoUSD = "0.000002", 2_000, 2_000
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := acceptedAssessmentInput()
			test.mutate(&input)
			if _, err := NewAssessmentRecord(input); err == nil {
				t.Fatal("invalid assessment record was accepted")
			}
		})
	}
}

func acceptedAssessmentRecord(t *testing.T) RecordedAssessment {
	t.Helper()
	recorded, err := NewAssessmentRecord(acceptedAssessmentInput())
	if err != nil {
		t.Fatal(err)
	}
	return recorded
}

func acceptedAssessmentInput() AssessmentRecordInput {
	return AssessmentRecordInput{
		Source: Source{SHA256: strings.Repeat("a", 64), Bytes: 2_048, DurationMS: 10_000},
		Media:  AssessmentMedia{SHA256: strings.Repeat("1", 64), Bytes: 1_024, DurationMS: 10_000, ProfileSHA256: strings.Repeat("2", 64), LineageSHA256: strings.Repeat("3", 64)},
		Assessor: AssessorProfile{
			ID: "assessor-a", ModelFamily: "family-a", Provider: "openrouter", Model: "vendor/model",
			ModelDigest: strings.Repeat("b", 64), CapabilitySHA256: strings.Repeat("c", 64),
			PromptVersion: DirectVideoPromptVersion, EvidenceContract: "assessment-v1",
		},
		MetadataSnapshotSHA256: strings.Repeat("f", 64),
		PromptSHA256:           strings.Repeat("d", 64), SchemaSHA256: strings.Repeat("e", 64),
		RequestSHA256: strings.Repeat("f", 64), RawResponse: []byte(`{"id":"generation"}`),
		StructuredOutput: `{"segments":[{"endMs":5000,"role":"commercial","decisiveAtMs":[1000],"reason":"offer"},{"endMs":10000,"role":"promo","decisiveAtMs":[7000],"reason":"promotion"}]}`,
		ResolvedProvider: "openrouter", ResolvedModel: "vendor/model-2026",
		UpstreamProvider: "Provider", UpstreamProviderSlug: "provider", GenerationID: "generation",
		Tokens:           AssessmentTokenUsage{Prompt: 100, Completion: 20, Video: 90},
		RequestedNanoUSD: 1_000, ReservedNanoUSD: 1_000, ChargedAmountUSD: "0.0000005",
		ChargedNanoUSD: 500, AccountedNanoUSD: 500, ChargeKnown: true,
		State: AssessmentRecordAccepted, AssessedAt: time.Date(2026, time.September, 9, 1, 0, 0, 0, time.UTC),
	}
}
