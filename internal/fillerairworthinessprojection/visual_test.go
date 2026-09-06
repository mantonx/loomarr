package fillerairworthinessprojection

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillerairworthiness"
	"github.com/loomarr/loomarr/internal/fillervisualsafety"
)

func TestProjectVisualPublishesTimedAdultNudityObservation(t *testing.T) {
	t.Parallel()
	fixture := newVisualFixture(t)
	positive := fixture.observation(t, fixture.portable, fillervisualsafety.ObservationProhibited, "adult-nudity")
	result := fillervisualsafety.Reduce(fixture.source, fixture.coverage, fixture.plan, []fillervisualsafety.Observation{positive})
	projection, err := ProjectVisual(fixture.subject, fixture.source, fixture.plan, fixture.coverage,
		[]fillervisualsafety.Observation{positive}, result, fixture.authority)
	if err != nil {
		t.Fatal(err)
	}
	if projection.Evidence.Coverage != fillerairworthiness.CoverageComplete || len(projection.Evidence.Observations) != 1 {
		t.Fatalf("projection = %#v", projection)
	}
	observation := projection.Evidence.Observations[0]
	if observation.Flag != fillerairworthiness.FlagAdultNudity || observation.StartMS != 100 || observation.EndMS != 200 ||
		observation.Severity != fillerairworthiness.SeverityHigh || observation.Context != fillerairworthiness.ContextDepiction {
		t.Fatalf("observation = %#v", observation)
	}
}

func TestProjectVisualApplePositiveUsesConservativeSourceInterval(t *testing.T) {
	t.Parallel()
	fixture := newVisualFixture(t)
	portable := fixture.observation(t, fixture.portable, fillervisualsafety.ObservationNoSignal, "")
	apple := fixture.observation(t, fixture.apple, fillervisualsafety.ObservationProhibited, "adult-nudity")
	items := []fillervisualsafety.Observation{portable, apple}
	result := fillervisualsafety.Reduce(fixture.source, fixture.coverage, fixture.plan, items)
	projection, err := ProjectVisual(fixture.subject, fixture.source, fixture.plan, fixture.coverage, items, result, fixture.authority)
	if err != nil {
		t.Fatal(err)
	}
	observation := projection.Evidence.Observations[0]
	if observation.StartMS != 0 || observation.EndMS != fixture.subject.DurationMS {
		t.Fatalf("Apple interval = %#v", observation)
	}
}

func TestProjectVisualNegativeCoversOnlyCertifiedAdultNudity(t *testing.T) {
	t.Parallel()
	fixture := newVisualFixture(t)
	negative := fixture.observation(t, fixture.portable, fillervisualsafety.ObservationNoSignal, "")
	result := fillervisualsafety.Reduce(fixture.source, fixture.coverage, fixture.plan, []fillervisualsafety.Observation{negative})
	projection, err := ProjectVisual(fixture.subject, fixture.source, fixture.plan, fixture.coverage,
		[]fillervisualsafety.Observation{negative}, result, fixture.authority)
	if err != nil {
		t.Fatal(err)
	}
	if projection.Evidence.Coverage != fillerairworthiness.CoverageComplete || len(projection.Evidence.Observations) != 0 ||
		!slices.Equal(projection.Evidence.Profile.CertifiedFlags, []fillerairworthiness.Flag{fillerairworthiness.FlagAdultNudity}) {
		t.Fatalf("projection = %#v", projection)
	}
}

func TestProjectVisualUnknownPositiveRemainsIncomplete(t *testing.T) {
	t.Parallel()
	fixture := newVisualFixture(t)
	positive := fixture.observation(t, fixture.portable, fillervisualsafety.ObservationProhibited, "unknown-match")
	result := fillervisualsafety.Reduce(fixture.source, fixture.coverage, fixture.plan, []fillervisualsafety.Observation{positive})
	projection, err := ProjectVisual(fixture.subject, fixture.source, fixture.plan, fixture.coverage,
		[]fillervisualsafety.Observation{positive}, result, fixture.authority)
	if err != nil {
		t.Fatal(err)
	}
	if projection.Evidence.Coverage != fillerairworthiness.CoverageIncomplete || len(projection.Evidence.Observations) != 0 {
		t.Fatalf("unknown positive projection = %#v", projection)
	}
}

func TestProjectVisualRejectsSourceResultAndProducerDrift(t *testing.T) {
	t.Parallel()
	fixture := newVisualFixture(t)
	negative := fixture.observation(t, fixture.portable, fillervisualsafety.ObservationNoSignal, "")
	items := []fillervisualsafety.Observation{negative}
	result := fillervisualsafety.Reduce(fixture.source, fixture.coverage, fixture.plan, items)

	subject := fixture.subject
	subject.EvidenceSHA256 = strings.Repeat("f", 64)
	if _, err := ProjectVisual(subject, fixture.source, fixture.plan, fixture.coverage, items, result, fixture.authority); err == nil {
		t.Fatal("source-drifted subject projected")
	}

	result.Outcome = fillervisualsafety.OutcomeHold
	result.SHA256 = fillervisualsafety.ResultSHA256(result)
	if _, err := ProjectVisual(fixture.subject, fixture.source, fixture.plan, fixture.coverage, items, result, fixture.authority); err == nil {
		t.Fatal("irreproducible result projected")
	}

	result = fillervisualsafety.Reduce(fixture.source, fixture.coverage, fixture.plan, items)
	drifted := negative
	drifted.Profile.Implementation = "uncertified-v2"
	drifted.SHA256 = fillervisualsafety.ObservationSHA256(drifted)
	items = []fillervisualsafety.Observation{drifted}
	result = fillervisualsafety.Reduce(fixture.source, fixture.coverage, fixture.plan, items)
	if _, err := ProjectVisual(fixture.subject, fixture.source, fixture.plan, fixture.coverage, items, result, fixture.authority); err == nil {
		t.Fatal("uncertified producer projected")
	}
}

type visualFixture struct {
	subject         Subject
	source          fillervisualsafety.SourceAuthority
	plan            fillervisualsafety.CoveragePlan
	coverage        fillervisualsafety.CoverageEvidence
	portable, apple fillervisualsafety.ProducerProfile
	authority       VisualAuthority
}

func newVisualFixture(t *testing.T) visualFixture {
	t.Helper()
	duration := int64(3_050)
	source, err := fillervisualsafety.SealSourceAuthority(fillervisualsafety.SourceAuthority{
		SourceID: "visual-source", SourceSHA256: strings.Repeat("2", 64), SourceBytes: 10_000,
		DurationMS: duration, PolicySHA256: strings.Repeat("b", 64), Implementation: "probe-v1",
		Video: fillervisualsafety.VideoStreamIdentity{Index: 0, Codec: "h264", Width: 640, Height: 480,
			FirstFrameMS: 0, LastFrameMS: duration - 1, FrameRateNumerator: 30, FrameRateDenominator: 1,
			TimeBaseNumerator: 1, TimeBaseDenominator: 90_000, DurationMS: duration},
		Probe:      fillervisualsafety.ToolIdentity{Name: "ffprobe", Version: "7.1", ExecutableSHA256: strings.Repeat("3", 64)},
		MeasuredAt: time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	profile, err := fillervisualsafety.SealCoverageProfile(fillervisualsafety.CoverageProfile{
		Implementation: "dense-v1", MaximumSourceDurationMS: 10_000, ObservationIntervalMS: 1_000,
		MaximumTimestampDriftMS: 10, MaximumObservations: 100, MinimumCoveredExposureMS: 1_021,
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := fillervisualsafety.PlanCoverage(source, profile)
	if err != nil {
		t.Fatal(err)
	}
	frames := make([]fillervisualsafety.FrameEvidence, len(plan.Points))
	for index, point := range plan.Points {
		frames[index] = fillervisualsafety.FrameEvidence{Ordinal: point.Ordinal, RequestedMS: point.RequestedMS,
			ObservedMS: point.RequestedMS, SHA256: strings.Repeat("4", 64), Bytes: 100, Width: 640, Height: 480}
	}
	coverage, err := fillervisualsafety.SealCoverageEvidence(plan,
		fillervisualsafety.ToolIdentity{Name: "ffmpeg", Version: "7.1", ExecutableSHA256: strings.Repeat("5", 64)}, frames, true)
	if err != nil {
		t.Fatal(err)
	}
	portable := fillervisualsafety.ProducerProfile{Family: fillervisualsafety.ProducerPortable, Implementation: "portable-v1", CapabilitySHA256: strings.Repeat("6", 64), EvidenceContract: "private-v1"}
	apple := fillervisualsafety.ProducerProfile{Family: fillervisualsafety.ProducerAppleSCA, Implementation: "apple-v1", CapabilitySHA256: strings.Repeat("7", 64), EvidenceContract: "private-v1"}
	authority, err := SealVisualAuthority(VisualAuthority{
		PolicySHA256: source.PolicySHA256, CertificationSHA256: strings.Repeat("c", 64),
		CoverageProfileSHA256: profile.SHA256, Producers: []fillervisualsafety.ProducerProfile{portable, apple},
		Rules: []Rule{{ID: "adult-nudity", Flag: fillerairworthiness.FlagAdultNudity,
			Severity: fillerairworthiness.SeverityHigh, Context: fillerairworthiness.ContextDepiction}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return visualFixture{Subject{strings.Repeat("a", 64), source.SourceSHA256, duration}, source, plan, coverage, portable, apple, authority}
}

func (fixture visualFixture) observation(t *testing.T, profile fillervisualsafety.ProducerProfile, state fillervisualsafety.ObservationState, match string) fillervisualsafety.Observation {
	t.Helper()
	value := fillervisualsafety.Observation{SourceAuthoritySHA256: fixture.source.SHA256, SourceSHA256: fixture.source.SourceSHA256,
		PolicySHA256: fixture.source.PolicySHA256, Profile: profile, State: state,
		Intervals: []fillervisualsafety.Interval{}, PolicyMatchIDs: []string{}, EvidenceSHA256: strings.Repeat("8", 64),
		AssessedAt: fixture.source.MeasuredAt.Add(time.Minute)}
	if profile.Family == fillervisualsafety.ProducerPortable {
		value.CoverageEvidenceSHA256 = fixture.coverage.SHA256
	}
	if state == fillervisualsafety.ObservationProhibited {
		value.PolicyMatchIDs = []string{match}
		if profile.Family != fillervisualsafety.ProducerAppleSCA {
			value.Intervals = []fillervisualsafety.Interval{{StartMS: 100, EndMS: 200}}
		}
	}
	sealed, err := fillervisualsafety.SealObservation(value)
	if err != nil {
		t.Fatal(err)
	}
	return sealed
}
