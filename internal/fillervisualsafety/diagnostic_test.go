package fillervisualsafety_test

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillervisualsafety"
)

func TestEvaluatePortableDiagnosticSweepsThresholdsWithoutCreatingTruthOrAdmission(t *testing.T) {
	authority, capability, profile, runs := portableDiagnosticFixture(t)

	report, err := fillervisualsafety.EvaluatePortableDiagnostic(authority, capability, profile, runs, authority.AuthoredAt.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := fillervisualsafety.ValidatePortableDiagnosticReport(authority, capability, profile, runs, report); err != nil {
		t.Fatal(err)
	}
	if report.TruthCreatedByCandidate || report.BlindHumanAuditRequired || report.TrainingAllowed || report.ProductionAdmissionAllowed {
		t.Fatalf("diagnostic granted forbidden authority: %#v", report)
	}
	if report.NextAction != "inspect_targeted_diagnostics" || len(report.TargetedReviewAliases) != 1 || report.TargetedReviewAliases[0] != "case-unresolved" {
		t.Fatalf("diagnostic did not produce the bounded review worklist: %#v", report.TargetedReviewAliases)
	}
	if len(report.Thresholds) != 3 {
		t.Fatalf("threshold metrics = %d", len(report.Thresholds))
	}
	low, middle, high := report.Thresholds[0], report.Thresholds[1], report.Thresholds[2]
	if low.DetectedPositiveFamilies != 1 || low.MissedPositiveFamilies != 0 || low.CleanFalsePositiveFamilies != 0 ||
		low.UnresolvedSignaledFamilies != 1 || middle.DetectedPositiveFamilies != 1 || middle.UnresolvedSignaledFamilies != 0 ||
		high.DetectedPositiveFamilies != 0 || high.MissedPositiveFamilies != 1 {
		t.Fatalf("unexpected threshold sweep: %#v", report.Thresholds)
	}
	if math.Abs(low.PositiveRecallExactLower95-0.05) > 0.0000001 {
		t.Fatalf("one-sided exact lower bound = %.9f", low.PositiveRecallExactLower95)
	}
	if low.CleanSlices[0].Slice != "advertising" || !low.CleanSlices[0].WithinCeiling {
		t.Fatalf("clean slice metric = %#v", low.CleanSlices)
	}

	mutated := report
	mutated.Thresholds[0].DetectedPositiveFamilies = 0
	mutated.SHA256 = fillervisualsafety.PortableDiagnosticReportSHA256(mutated)
	if err := fillervisualsafety.ValidatePortableDiagnosticReport(authority, capability, profile, runs, mutated); err == nil {
		t.Fatal("report validation accepted a reproducibly rehashed scoring lie")
	}
}

func TestPortableDiagnosticAuthoritySeparatesLockedTruthFromUnresolvedCandidates(t *testing.T) {
	authority, capability, profile, _ := portableDiagnosticFixture(t)

	tests := map[string]func(*fillervisualsafety.PortableDiagnosticAuthority){
		"candidate cannot mint truth": func(value *fillervisualsafety.PortableDiagnosticAuthority) {
			value.Cases[2].TruthLabel = fillervisualsafety.DiagnosticTruthPositive
		},
		"unresolved cannot smuggle truth": func(value *fillervisualsafety.PortableDiagnosticAuthority) {
			value.Cases[2].TruthAuthoritySHA256 = strings.Repeat("9", 64)
		},
		"source family collision": func(value *fillervisualsafety.PortableDiagnosticAuthority) {
			value.Cases[2].SourceFamilyID = value.Cases[1].SourceFamilyID
		},
		"source content collision": func(value *fillervisualsafety.PortableDiagnosticAuthority) {
			value.Cases[2].SourceAuthority.SourceSHA256 = value.Cases[1].SourceAuthority.SourceSHA256
			value.Cases[2].SourceAuthority.SHA256 = fillervisualsafety.SourceAuthoritySHA256(value.Cases[2].SourceAuthority)
		},
		"positive below coverage floor": func(value *fillervisualsafety.PortableDiagnosticAuthority) {
			value.Cases[1].PositiveIntervals = []fillervisualsafety.Interval{{StartMS: 0, EndMS: 1_000}}
		},
		"threshold order drift": func(value *fillervisualsafety.PortableDiagnosticAuthority) {
			value.Thresholds[0], value.Thresholds[1] = value.Thresholds[1], value.Thresholds[0]
		},
		"unknown score transform": func(value *fillervisualsafety.PortableDiagnosticAuthority) {
			value.ScoreTransform = "model_owned_threshold"
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			candidate := authority
			candidate.Thresholds = append([]float64(nil), authority.Thresholds...)
			candidate.Cases = append([]fillervisualsafety.PortableDiagnosticCase(nil), authority.Cases...)
			for index := range candidate.Cases {
				candidate.Cases[index].PositiveIntervals = append([]fillervisualsafety.Interval(nil), authority.Cases[index].PositiveIntervals...)
			}
			mutate(&candidate)
			candidate.SHA256 = fillervisualsafety.PortableDiagnosticAuthoritySHA256(candidate)
			if err := fillervisualsafety.ValidatePortableDiagnosticAuthority(candidate, capability, profile); err == nil {
				t.Fatal("authority validation accepted invalid candidate")
			}
		})
	}
}

func TestEvaluatePortableDiagnosticSupportsOrderedCumulativeSoftmax(t *testing.T) {
	base, capability, profile, _ := portableDiagnosticFixture(t)
	base.ModelID = "freepik-nsfw"
	base.PositiveOutputLabel = "medium"
	base.ScoreTransform = fillervisualsafety.DiagnosticScoreCumulativeSoftmax
	base.SHA256 = ""
	authority, err := fillervisualsafety.SealPortableDiagnosticAuthority(base, capability, profile)
	if err != nil {
		t.Fatal(err)
	}
	runs := []fillervisualsafety.PortableDiagnosticRun{
		diagnosticRun(t, authority.Cases[0], capability, profile, "freepik-nsfw", []float64{0.10, 0.20, 0.10, 0.10}),
		diagnosticRun(t, authority.Cases[1], capability, profile, "freepik-nsfw", []float64{0.10, 0.90, 0.20, 0.10}),
		diagnosticRun(t, authority.Cases[2], capability, profile, "freepik-nsfw", []float64{0.10, 0.80, 0.10, 0.10}),
	}
	report, err := fillervisualsafety.EvaluatePortableDiagnostic(
		authority, capability, profile, runs, authority.AuthoredAt.Add(time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	if report.ScoreTransform != fillervisualsafety.DiagnosticScoreCumulativeSoftmax ||
		report.Thresholds[0].DetectedPositiveFamilies != 1 || report.Thresholds[1].UnresolvedSignaledFamilies != 0 {
		t.Fatalf("cumulative threshold sweep = %#v", report.Thresholds)
	}

	invalid := authority
	invalid.PositiveOutputLabel = "neutral"
	invalid.SHA256 = fillervisualsafety.PortableDiagnosticAuthoritySHA256(invalid)
	if err := fillervisualsafety.ValidatePortableDiagnosticAuthority(invalid, capability, profile); err == nil {
		t.Fatal("cumulative scoring accepted a tautological first output label")
	}
}

func TestEvaluatePortableDiagnosticReportsOperationalFailureWithoutPartialEvidence(t *testing.T) {
	authority, capability, profile, runs := portableDiagnosticFixture(t)
	runs[1].State = fillervisualsafety.DiagnosticRunWorkerFailed
	runs[1].FailureCode = "worker_timeout"
	runs[1].Coverage = nil
	runs[1].Inference = nil

	report, err := fillervisualsafety.EvaluatePortableDiagnostic(authority, capability, profile, runs, authority.AuthoredAt.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if report.Thresholds[0].OperationalHolds != 1 || report.Thresholds[0].MissedPositiveFamilies != 1 ||
		len(report.Cases[1].TargetedReasons) != 1 || report.Cases[1].TargetedReasons[0] != "operational_failure" {
		t.Fatalf("failure was not retained conservatively: %#v", report)
	}
	if _, err := fillervisualsafety.EvaluatePortableDiagnostic(authority, capability, profile, runs[:2], authority.AuthoredAt.Add(time.Minute)); err == nil {
		t.Fatal("diagnostic accepted a missing case run")
	}
}

func portableDiagnosticFixture(t *testing.T) (fillervisualsafety.PortableDiagnosticAuthority, fillervisualsafety.PortableCapability, fillervisualsafety.CoverageProfile, []fillervisualsafety.PortableDiagnosticRun) {
	t.Helper()
	capability, err := fillervisualsafety.SealPortableCapability(portableCapabilityInput())
	if err != nil {
		t.Fatal(err)
	}
	profile := visualProfile(t)
	measured := time.Date(2026, time.September, 4, 10, 0, 0, 0, time.UTC)
	cases := []fillervisualsafety.PortableDiagnosticCase{
		diagnosticCase(t, "case-clean", "source-clean", "d", "family-clean", fillervisualsafety.DiagnosticTruthClean, nil),
		diagnosticCase(t, "case-positive", "source-positive", "a", "family-positive", fillervisualsafety.DiagnosticTruthPositive,
			[]fillervisualsafety.Interval{{StartMS: 0, EndMS: 2_500}}),
		diagnosticCase(t, "case-unresolved", "source-unresolved", "9", "family-unresolved", fillervisualsafety.DiagnosticTruthUnresolved, nil),
	}
	authority, err := fillervisualsafety.SealPortableDiagnosticAuthority(fillervisualsafety.PortableDiagnosticAuthority{
		AuthoredAt: measured.Add(time.Hour), PolicySHA256: strings.Repeat("b", 64), CapabilitySHA256: capability.SHA256,
		CoverageProfileSHA256: profile.SHA256, ModelID: "marqo-nsfw-384", PositiveOutputLabel: "NSFW",
		ScoreTransform: fillervisualsafety.DiagnosticScoreSoftmax, Thresholds: []float64{0.50, 0.85, 0.95},
		MaximumCleanFalsePositiveRate: 0.25, Implementation: "portable-visual-diagnostic-v1", Cases: cases,
	}, capability, profile)
	if err != nil {
		t.Fatal(err)
	}
	runs := []fillervisualsafety.PortableDiagnosticRun{
		diagnosticRun(t, authority.Cases[0], capability, profile, "marqo-nsfw-384", []float64{0.10, 0.20, 0.10, 0.10}),
		diagnosticRun(t, authority.Cases[1], capability, profile, "marqo-nsfw-384", []float64{0.10, 0.90, 0.20, 0.10}),
		diagnosticRun(t, authority.Cases[2], capability, profile, "marqo-nsfw-384", []float64{0.10, 0.80, 0.10, 0.10}),
	}
	return authority, capability, profile, runs
}

func diagnosticCase(t *testing.T, alias, sourceID, digestCharacter, family string, label fillervisualsafety.DiagnosticTruthLabel, intervals []fillervisualsafety.Interval) fillervisualsafety.PortableDiagnosticCase {
	t.Helper()
	source := visualAuthority(t, 3_001)
	source.SourceID = sourceID
	source.SourceSHA256 = strings.Repeat(digestCharacter, 64)
	source.SHA256 = fillervisualsafety.SourceAuthoritySHA256(source)
	truth := strings.Repeat("7", 64)
	if label == fillervisualsafety.DiagnosticTruthUnresolved {
		truth = ""
	}
	return fillervisualsafety.PortableDiagnosticCase{
		Alias: alias, SourceAuthority: source, SourceFamilyID: family, RightsSHA256: strings.Repeat("8", 64),
		TruthLabel: label, TruthAuthoritySHA256: truth, Slices: diagnosticSlices(label), PositiveIntervals: intervals,
	}
}

func diagnosticSlices(label fillervisualsafety.DiagnosticTruthLabel) []string {
	if label == fillervisualsafety.DiagnosticTruthPositive {
		return []string{fillervisualsafety.DiagnosticSliceShortExposure}
	}
	return []string{fillervisualsafety.DiagnosticSliceAdvertising}
}

func diagnosticRun(t *testing.T, item fillervisualsafety.PortableDiagnosticCase, capability fillervisualsafety.PortableCapability, profile fillervisualsafety.CoverageProfile, modelID string, probabilities []float64) fillervisualsafety.PortableDiagnosticRun {
	t.Helper()
	plan, err := fillervisualsafety.PlanCoverage(item.SourceAuthority, profile)
	if err != nil {
		t.Fatal(err)
	}
	frames := visualFrames(plan)
	for index := range frames {
		frames[index].Bytes = int64(frames[index].Width * frames[index].Height * 3)
	}
	coverage, err := fillervisualsafety.SealCoverageEvidence(plan, fillervisualsafety.ToolIdentity{
		Name: "ffmpeg", Version: "7.1", ExecutableSHA256: strings.Repeat("e", 64),
	}, frames, true)
	if err != nil {
		t.Fatal(err)
	}
	responses := make([]fillervisualsafety.PortableFrameResponse, len(frames))
	if len(probabilities) != len(frames) {
		t.Fatalf("probability fixture has %d scores for %d frames", len(probabilities), len(frames))
	}
	for index, frame := range frames {
		request, requestErr := fillervisualsafety.SealPortableFrameRequest(capability, plan, frame, fillervisualsafety.PixelRGB24)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		probability := probabilities[index]
		freepikLogits := []float64{0, 0, 0, 0}
		marqoLogits := []float64{0, 0}
		switch modelID {
		case "freepik-nsfw":
			freepikLogits = []float64{math.Log1p(-probability), -1_000, math.Log(probability * 0.75), math.Log(probability * 0.25)}
		case "marqo-nsfw-384":
			marqoLogits = []float64{math.Log(probability), math.Log1p(-probability)}
		default:
			t.Fatalf("unknown diagnostic model %q", modelID)
		}
		responses[index], err = fillervisualsafety.SealPortableFrameResponse(capability, plan, request, 1, []fillervisualsafety.PortableModelScores{
			{ModelID: "freepik-nsfw", Logits: freepikLogits},
			{ModelID: "marqo-nsfw-384", Logits: marqoLogits},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	inference, err := fillervisualsafety.SealPortableInferenceEvidence(capability, plan, coverage, responses)
	if err != nil {
		t.Fatal(err)
	}
	return fillervisualsafety.PortableDiagnosticRun{
		Alias: item.Alias, State: fillervisualsafety.DiagnosticRunComplete, Plan: plan, Coverage: &coverage, Inference: &inference,
	}
}
