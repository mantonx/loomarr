package filler

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillerairworthiness"
	"github.com/loomarr/loomarr/internal/fillerairworthinessprojection"
	"github.com/loomarr/loomarr/internal/fillervisualsafety"
)

func TestVisualSafetyEvaluatorProjectsPositiveAndReplaysSettledOperation(t *testing.T) {
	media := playbackIntegrityMediaFixture(t, validPlaybackIntegrityQuality())
	fixture := visualSafetyProducerFixture(t, media)
	positive := fixture.observation(t, fillervisualsafety.ObservationProhibited, "adult-nudity")
	fixture.evidence.Observations = []fillervisualsafety.Observation{positive}
	fixture.evidence.Result = fillervisualsafety.Reduce(
		fixture.evidence.Source, fixture.evidence.Coverage, fixture.evidence.Plan, fixture.evidence.Observations,
	)
	repository, err := NewFileSegmentScreeningEvidenceRepository(filepath.Join(t.TempDir(), "screening-evidence"))
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	var request VisualSafetyProducerRequest
	producer := visualSafetyProducerFunc(func(_ context.Context, got VisualSafetyProducerRequest) (VisualSafetyProducerEvidence, error) {
		calls++
		request = got
		produced := fixture.evidence
		produced.OperationSHA256 = got.OperationSHA256
		return produced, nil
	})
	evaluator, err := NewVisualSafetyEvaluator(fixture.authority, repository, producer)
	if err != nil {
		t.Fatal(err)
	}

	first, err := evaluator.Evaluate(t.Context(), media)
	if err != nil {
		t.Fatal(err)
	}
	if first.Evidence.Outcome != ScreenPass || first.Evidence.ReasonCode != "visual_safety_evidence_complete" ||
		first.Evidence.Suitability == nil || first.Evidence.Suitability.Coverage != fillerairworthiness.CoverageComplete ||
		len(first.Evidence.Suitability.Observations) != 1 ||
		first.Evidence.Suitability.Observations[0].Flag != fillerairworthiness.FlagAdultNudity {
		t.Fatalf("visual positive = %+v", first)
	}
	wantOperation := segmentScreeningOperationSHA256(media.Subject.SHA256, evaluator.projected.profile)
	if calls != 1 || request.OperationSHA256 != wantOperation || request.EvidencePath != media.EvidencePath ||
		request.Subject != projectedSafetySubject(media.Subject) {
		t.Fatalf("producer request = %+v, calls = %d", request, calls)
	}
	if bytes.Contains(first.RawEvidence, []byte(filepath.Dir(media.EvidencePath))) {
		t.Fatal("visual evidence leaked its private media path")
	}
	assertSafetyObservationRejectsAirworthiness(t, media.Subject, first, fillerairworthiness.FlagAdultNudity)

	if err := repository.PutSegmentScreeningAxisEvidence(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	second, err := evaluator.Evaluate(t.Context(), media)
	if err != nil || second.Evidence.SHA256 != first.Evidence.SHA256 || calls != 1 {
		t.Fatalf("visual replay = %+v, calls = %d, error = %v", second, calls, err)
	}
}

func TestVisualSafetyEvaluatorMapsCertifiedNegativeAndUnknownPositive(t *testing.T) {
	for _, test := range []struct {
		name         string
		state        fillervisualsafety.ObservationState
		match        string
		wantOutcome  SegmentScreeningOutcome
		wantCoverage fillerairworthiness.Coverage
		wantCount    int
	}{
		{name: "certified negative", state: fillervisualsafety.ObservationNoSignal, wantOutcome: ScreenPass, wantCoverage: fillerairworthiness.CoverageComplete},
		{name: "unknown positive", state: fillervisualsafety.ObservationProhibited, match: "unknown-match", wantOutcome: ScreenHold, wantCoverage: fillerairworthiness.CoverageIncomplete},
	} {
		t.Run(test.name, func(t *testing.T) {
			media := playbackIntegrityMediaFixture(t, validPlaybackIntegrityQuality())
			fixture := visualSafetyProducerFixture(t, media)
			observation := fixture.observation(t, test.state, test.match)
			fixture.evidence.Observations = []fillervisualsafety.Observation{observation}
			fixture.evidence.Result = fillervisualsafety.Reduce(
				fixture.evidence.Source, fixture.evidence.Coverage, fixture.evidence.Plan, fixture.evidence.Observations,
			)
			repository, err := NewFileSegmentScreeningEvidenceRepository(filepath.Join(t.TempDir(), "screening-evidence"))
			if err != nil {
				t.Fatal(err)
			}
			evaluator, err := NewVisualSafetyEvaluator(fixture.authority, repository, visualSafetyProducerFunc(
				func(_ context.Context, request VisualSafetyProducerRequest) (VisualSafetyProducerEvidence, error) {
					produced := fixture.evidence
					produced.OperationSHA256 = request.OperationSHA256
					return produced, nil
				},
			))
			if err != nil {
				t.Fatal(err)
			}
			recorded, err := evaluator.Evaluate(t.Context(), media)
			if err != nil || recorded.Evidence.Outcome != test.wantOutcome || recorded.Evidence.Suitability == nil ||
				recorded.Evidence.Suitability.Coverage != test.wantCoverage || len(recorded.Evidence.Suitability.Observations) != test.wantCount {
				t.Fatalf("visual projection = %+v, error = %v", recorded, err)
			}
		})
	}
}

func TestVisualSafetyEvaluatorRejectsSourceAndResultDrift(t *testing.T) {
	for _, test := range []struct {
		name           string
		wrongOperation bool
		mutate         func(*VisualSafetyProducerEvidence)
	}{
		{name: "producer operation", wrongOperation: true, mutate: func(*VisualSafetyProducerEvidence) {}},
		{name: "source bytes", mutate: func(evidence *VisualSafetyProducerEvidence) {
			evidence.Source.SourceBytes++
			evidence.Source.SHA256 = fillervisualsafety.SourceAuthoritySHA256(evidence.Source)
		}},
		{name: "irreproducible result", mutate: func(evidence *VisualSafetyProducerEvidence) {
			evidence.Result.Outcome = fillervisualsafety.OutcomeHold
			evidence.Result.SHA256 = fillervisualsafety.ResultSHA256(evidence.Result)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			media := playbackIntegrityMediaFixture(t, validPlaybackIntegrityQuality())
			fixture := visualSafetyProducerFixture(t, media)
			negative := fixture.observation(t, fillervisualsafety.ObservationNoSignal, "")
			fixture.evidence.Observations = []fillervisualsafety.Observation{negative}
			fixture.evidence.Result = fillervisualsafety.Reduce(
				fixture.evidence.Source, fixture.evidence.Coverage, fixture.evidence.Plan, fixture.evidence.Observations,
			)
			test.mutate(&fixture.evidence)
			repository, err := NewFileSegmentScreeningEvidenceRepository(filepath.Join(t.TempDir(), "screening-evidence"))
			if err != nil {
				t.Fatal(err)
			}
			evaluator, err := NewVisualSafetyEvaluator(fixture.authority, repository, visualSafetyProducerFunc(
				func(_ context.Context, request VisualSafetyProducerRequest) (VisualSafetyProducerEvidence, error) {
					produced := fixture.evidence
					produced.OperationSHA256 = request.OperationSHA256
					if test.wrongOperation {
						produced.OperationSHA256 = strings.Repeat("f", 64)
					}
					return produced, nil
				},
			))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := evaluator.Evaluate(t.Context(), media); err == nil {
				t.Fatal("drifted visual operation produced authority")
			}
		})
	}
}

type visualSafetyProducerFunc func(context.Context, VisualSafetyProducerRequest) (VisualSafetyProducerEvidence, error)

func (produce visualSafetyProducerFunc) EvaluateVisualSafety(ctx context.Context, request VisualSafetyProducerRequest) (VisualSafetyProducerEvidence, error) {
	return produce(ctx, request)
}

type visualSafetyFixture struct {
	authority fillerairworthinessprojection.VisualAuthority
	profile   fillervisualsafety.ProducerProfile
	evidence  VisualSafetyProducerEvidence
}

func visualSafetyProducerFixture(t *testing.T, media SegmentScreeningMedia) visualSafetyFixture {
	t.Helper()
	measuredAt := time.Date(2026, time.September, 4, 10, 0, 0, 0, time.UTC)
	source, err := fillervisualsafety.SealSourceAuthority(fillervisualsafety.SourceAuthority{
		SourceID: "rendered-child", SourceSHA256: media.Subject.EvidenceSHA256, SourceBytes: media.Subject.EvidenceBytes,
		DurationMS: media.Subject.EvidenceDurationMs, PolicySHA256: strings.Repeat("b", 64), Implementation: "probe-v1",
		Video: fillervisualsafety.VideoStreamIdentity{
			Index: 0, Codec: "h264", Width: 640, Height: 480, FirstFrameMS: 0,
			LastFrameMS: media.Subject.EvidenceDurationMs - 1, FrameRateNumerator: 30, FrameRateDenominator: 1,
			TimeBaseNumerator: 1, TimeBaseDenominator: 90_000, DurationMS: media.Subject.EvidenceDurationMs,
		},
		Probe: fillervisualsafety.ToolIdentity{
			Name: "ffprobe", Version: "7.1", ExecutableSHA256: strings.Repeat("3", 64),
		},
		MeasuredAt: measuredAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	coverageProfile, err := fillervisualsafety.SealCoverageProfile(fillervisualsafety.CoverageProfile{
		Implementation: "dense-v1", MaximumSourceDurationMS: 60_000, ObservationIntervalMS: 10_000,
		MaximumTimestampDriftMS: 0, MaximumObservations: 10, MinimumCoveredExposureMS: 10_001,
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := fillervisualsafety.PlanCoverage(source, coverageProfile)
	if err != nil {
		t.Fatal(err)
	}
	frames := make([]fillervisualsafety.FrameEvidence, len(plan.Points))
	for index, point := range plan.Points {
		frames[index] = fillervisualsafety.FrameEvidence{
			Ordinal: point.Ordinal, RequestedMS: point.RequestedMS, ObservedMS: point.RequestedMS,
			SHA256: strings.Repeat("4", 64), Bytes: 100, Width: 640, Height: 480,
		}
	}
	coverage, err := fillervisualsafety.SealCoverageEvidence(plan, fillervisualsafety.ToolIdentity{
		Name: "ffmpeg", Version: "7.1", ExecutableSHA256: strings.Repeat("5", 64),
	}, frames, true)
	if err != nil {
		t.Fatal(err)
	}
	profile := fillervisualsafety.ProducerProfile{
		Family: fillervisualsafety.ProducerPortable, Implementation: "portable-v1",
		CapabilitySHA256: strings.Repeat("6", 64), EvidenceContract: "private-v1",
	}
	authority, err := fillerairworthinessprojection.SealVisualAuthority(fillerairworthinessprojection.VisualAuthority{
		PolicySHA256: source.PolicySHA256, CertificationSHA256: strings.Repeat("c", 64),
		CoverageProfileSHA256: coverageProfile.SHA256, Producers: []fillervisualsafety.ProducerProfile{profile},
		Rules: []fillerairworthinessprojection.Rule{{
			ID: "adult-nudity", Flag: fillerairworthiness.FlagAdultNudity,
			Severity: fillerairworthiness.SeverityHigh, Context: fillerairworthiness.ContextDepiction,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return visualSafetyFixture{
		authority: authority, profile: profile,
		evidence: VisualSafetyProducerEvidence{Source: source, Plan: plan, Coverage: coverage},
	}
}

func (fixture visualSafetyFixture) observation(t *testing.T, state fillervisualsafety.ObservationState, match string) fillervisualsafety.Observation {
	t.Helper()
	observation := fillervisualsafety.Observation{
		SourceAuthoritySHA256: fixture.evidence.Source.SHA256, SourceSHA256: fixture.evidence.Source.SourceSHA256,
		PolicySHA256: fixture.evidence.Source.PolicySHA256, CoverageEvidenceSHA256: fixture.evidence.Coverage.SHA256,
		Profile: fixture.profile, State: state, Intervals: []fillervisualsafety.Interval{}, PolicyMatchIDs: []string{},
		EvidenceSHA256: strings.Repeat("8", 64), AssessedAt: fixture.evidence.Source.MeasuredAt.Add(time.Minute),
	}
	if state == fillervisualsafety.ObservationProhibited {
		observation.Intervals = []fillervisualsafety.Interval{{StartMS: 100, EndMS: 200}}
		observation.PolicyMatchIDs = []string{match}
	}
	sealed, err := fillervisualsafety.SealObservation(observation)
	if err != nil {
		t.Fatal(err)
	}
	return sealed
}
