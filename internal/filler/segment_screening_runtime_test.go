package filler

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

type capturedSegmentScreeningEvaluator struct {
	axis       SegmentScreeningAxis
	profile    SegmentScreeningAxisProfile
	outcome    SegmentScreeningOutcome
	assessedAt time.Time
	err        error
	mutate     func(*RecordedSegmentScreeningAxisEvidence)
	order      *[]string
	seen       []SegmentScreeningMedia
}

func (e *capturedSegmentScreeningEvaluator) Axis() SegmentScreeningAxis { return e.axis }

func (e *capturedSegmentScreeningEvaluator) Evaluate(_ context.Context, media SegmentScreeningMedia) (RecordedSegmentScreeningAxisEvidence, error) {
	e.seen = append(e.seen, media)
	*e.order = append(*e.order, "evaluate:"+string(e.axis))
	if e.err != nil {
		return RecordedSegmentScreeningAxisEvidence{}, e.err
	}
	recorded, err := NewSegmentScreeningAxisEvidence(
		media.Subject, e.profile, e.outcome, "authority_clear", []byte("raw-"+string(e.axis)),
		e.assessedAt,
	)
	if err == nil && e.mutate != nil {
		e.mutate(&recorded)
	}
	return recorded, err
}

type capturedSegmentScreeningRepository struct {
	order        *[]string
	subjects     []SegmentScreeningSubject
	axis         []RecordedSegmentScreeningAxisEvidence
	aggregates   []SegmentScreeningEvidence
	subjectErr   error
	axisErrAt    int
	axisErr      error
	aggregateErr error
}

func (r *capturedSegmentScreeningRepository) PutSegmentScreeningSubject(_ context.Context, subject SegmentScreeningSubject) error {
	*r.order = append(*r.order, "persist:subject")
	if r.subjectErr != nil {
		return r.subjectErr
	}
	r.subjects = append(r.subjects, subject)
	return nil
}

func (r *capturedSegmentScreeningRepository) PutSegmentScreeningAxisEvidence(_ context.Context, recorded RecordedSegmentScreeningAxisEvidence) error {
	if r.axisErr != nil && len(r.axis) == r.axisErrAt {
		return r.axisErr
	}
	*r.order = append(*r.order, "persist:"+string(recorded.Evidence.Profile.Axis))
	r.axis = append(r.axis, recorded)
	return nil
}

func (r *capturedSegmentScreeningRepository) PutSegmentScreeningEvidence(_ context.Context, aggregate SegmentScreeningEvidence) error {
	if r.aggregateErr != nil {
		return r.aggregateErr
	}
	*r.order = append(*r.order, "persist:aggregate")
	r.aggregates = append(r.aggregates, aggregate)
	return nil
}

func TestSegmentScreeningRuntimePersistsSubjectThenCallsFiveAxesSerially(t *testing.T) {
	order := []string{}
	evaluators := segmentScreeningEvaluatorFixtures(&order)
	repository := &capturedSegmentScreeningRepository{order: &order}
	runtime, err := NewSegmentScreeningRuntime([]SegmentScreeningEvaluator{
		evaluators[ScreenRights], evaluators[ScreenPlayback], evaluators[ScreenWrittenSafety], evaluators[ScreenVisualSafety], evaluators[ScreenSpokenSafety],
	}, repository)
	if err != nil {
		t.Fatal(err)
	}
	media := segmentScreeningRuntimeMedia(t)
	aggregate, err := runtime.Screen(t.Context(), media)
	if err != nil {
		t.Fatal(err)
	}
	wantOrder := []string{
		"persist:subject",
		"evaluate:visual_safety", "persist:visual_safety",
		"evaluate:spoken_safety", "persist:spoken_safety",
		"evaluate:written_safety", "persist:written_safety",
		"evaluate:rights", "persist:rights",
		"evaluate:playback_integrity", "persist:playback_integrity",
		"persist:aggregate",
	}
	if !slices.Equal(order, wantOrder) || len(repository.subjects) != 1 || len(repository.axis) != 5 || len(repository.aggregates) != 1 || !aggregate.Passes() {
		t.Fatalf("order=%v subjects=%d axes=%d aggregates=%d aggregate=%+v", order, len(repository.subjects), len(repository.axis), len(repository.aggregates), aggregate)
	}
	if want := time.Date(2026, time.September, 12, 2, 4, 0, 0, time.UTC); aggregate.AssessedAt != want {
		t.Fatalf("aggregate assessedAt=%s, want latest immutable axis time %s", aggregate.AssessedAt, want)
	}
	for _, evaluator := range evaluators {
		if len(evaluator.seen) != 1 || evaluator.seen[0] != media {
			t.Fatalf("axis %q media=%+v", evaluator.axis, evaluator.seen)
		}
	}
}

func TestSegmentScreeningRuntimeDoesNotEvaluateBeforeSubjectOrAxisEvidenceIsDurable(t *testing.T) {
	t.Run("subject", func(t *testing.T) {
		order := []string{}
		evaluators := segmentScreeningEvaluatorFixtures(&order)
		want := errors.New("subject unavailable")
		repository := &capturedSegmentScreeningRepository{order: &order, subjectErr: want}
		runtime := mustSegmentScreeningRuntime(t, evaluators, repository)
		if _, err := runtime.Screen(t.Context(), segmentScreeningRuntimeMedia(t)); !errors.Is(err, want) {
			t.Fatalf("error=%v, want subject persistence failure", err)
		}
		if !slices.Equal(order, []string{"persist:subject"}) {
			t.Fatalf("screening advanced before subject was durable: %v", order)
		}
	})
	t.Run("axis", func(t *testing.T) {
		order := []string{}
		evaluators := segmentScreeningEvaluatorFixtures(&order)
		want := errors.New("axis unavailable")
		repository := &capturedSegmentScreeningRepository{order: &order, axisErr: want, axisErrAt: 1}
		runtime := mustSegmentScreeningRuntime(t, evaluators, repository)
		if _, err := runtime.Screen(t.Context(), segmentScreeningRuntimeMedia(t)); !errors.Is(err, want) {
			t.Fatalf("error=%v, want axis persistence failure", err)
		}
		wantOrder := []string{"persist:subject", "evaluate:visual_safety", "persist:visual_safety", "evaluate:spoken_safety"}
		if !slices.Equal(order, wantOrder) || len(repository.axis) != 1 || len(repository.aggregates) != 0 {
			t.Fatalf("unpersisted axis advanced runtime: order=%v axes=%d aggregates=%d", order, len(repository.axis), len(repository.aggregates))
		}
	})
}

func TestSegmentScreeningRuntimePersistsDurableHold(t *testing.T) {
	order := []string{}
	evaluators := segmentScreeningEvaluatorFixtures(&order)
	evaluators[ScreenRights].outcome = ScreenHold
	repository := &capturedSegmentScreeningRepository{order: &order}
	runtime := mustSegmentScreeningRuntime(t, evaluators, repository)
	aggregate, err := runtime.Screen(t.Context(), segmentScreeningRuntimeMedia(t))
	if err != nil {
		t.Fatal(err)
	}
	if aggregate.Passes() || len(repository.aggregates) != 1 || repository.aggregates[0].SHA256 != aggregate.SHA256 {
		t.Fatalf("hold was not persisted as a complete domain result: %+v", aggregate)
	}
}

func TestSegmentScreeningRuntimeRejectsIncompleteOrDuplicateAxesBeforeCalls(t *testing.T) {
	order := []string{}
	evaluators := segmentScreeningEvaluatorFixtures(&order)
	repository := &capturedSegmentScreeningRepository{order: &order}
	tests := []struct {
		name  string
		items []SegmentScreeningEvaluator
	}{
		{name: "missing", items: []SegmentScreeningEvaluator{evaluators[ScreenVisualSafety], evaluators[ScreenSpokenSafety], evaluators[ScreenWrittenSafety], evaluators[ScreenRights]}},
		{name: "duplicate", items: []SegmentScreeningEvaluator{evaluators[ScreenVisualSafety], evaluators[ScreenSpokenSafety], evaluators[ScreenWrittenSafety], evaluators[ScreenRights], evaluators[ScreenRights]}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewSegmentScreeningRuntime(test.items, repository); err == nil {
				t.Fatal("invalid evaluator set was accepted")
			}
		})
	}
	if len(order) != 0 {
		t.Fatalf("constructor called evaluators: %v", order)
	}
}

func TestSegmentScreeningRuntimeRetryReproducesAggregateIdentity(t *testing.T) {
	order := []string{}
	evaluators := segmentScreeningEvaluatorFixtures(&order)
	repository := &capturedSegmentScreeningRepository{order: &order}
	runtime := mustSegmentScreeningRuntime(t, evaluators, repository)
	media := segmentScreeningRuntimeMedia(t)

	first, err := runtime.Screen(t.Context(), media)
	if err != nil {
		t.Fatal(err)
	}
	second, err := runtime.Screen(t.Context(), media)
	if err != nil {
		t.Fatal(err)
	}
	if first.SHA256 != second.SHA256 || first.AssessedAt != second.AssessedAt ||
		!slices.Equal(first.Results, second.Results) || len(repository.aggregates) != 2 ||
		repository.aggregates[0].SHA256 != repository.aggregates[1].SHA256 {
		t.Fatalf("retry changed aggregate identity: first=%+v second=%+v", first, second)
	}
}

func TestSegmentScreeningRuntimeFailsClosedOnEvaluatorDrift(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[SegmentScreeningAxis]*capturedSegmentScreeningEvaluator)
	}{
		{name: "operational error", mutate: func(items map[SegmentScreeningAxis]*capturedSegmentScreeningEvaluator) {
			items[ScreenSpokenSafety].err = errors.New("settlement missing")
		}},
		{name: "axis drift", mutate: func(items map[SegmentScreeningAxis]*capturedSegmentScreeningEvaluator) {
			items[ScreenSpokenSafety].mutate = func(recorded *RecordedSegmentScreeningAxisEvidence) {
				recorded.Evidence.Profile.Axis = ScreenVisualSafety
				recorded.Evidence.SHA256 = SegmentScreeningAxisEvidenceSHA256(recorded.Evidence)
			}
		}},
		{name: "subject drift", mutate: func(items map[SegmentScreeningAxis]*capturedSegmentScreeningEvaluator) {
			items[ScreenSpokenSafety].mutate = func(recorded *RecordedSegmentScreeningAxisEvidence) {
				recorded.Evidence.SubjectSHA256 = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
				recorded.Evidence.SHA256 = SegmentScreeningAxisEvidenceSHA256(recorded.Evidence)
			}
		}},
		{name: "raw authority drift", mutate: func(items map[SegmentScreeningAxis]*capturedSegmentScreeningEvaluator) {
			items[ScreenSpokenSafety].mutate = func(recorded *RecordedSegmentScreeningAxisEvidence) { recorded.RawEvidence = nil }
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			order := []string{}
			items := segmentScreeningEvaluatorFixtures(&order)
			test.mutate(items)
			repository := &capturedSegmentScreeningRepository{order: &order}
			runtime := mustSegmentScreeningRuntime(t, items, repository)
			if _, err := runtime.Screen(t.Context(), segmentScreeningRuntimeMedia(t)); err == nil {
				t.Fatal("invalid evaluator result was accepted")
			}
			if len(repository.aggregates) != 0 {
				t.Fatalf("invalid aggregate was persisted: %+v", repository.aggregates)
			}
		})
	}
}

func TestSegmentScreeningRuntimeRequiresThreeDistinctAbsoluteArtifactPaths(t *testing.T) {
	order := []string{}
	evaluators := segmentScreeningEvaluatorFixtures(&order)
	repository := &capturedSegmentScreeningRepository{order: &order}
	runtime := mustSegmentScreeningRuntime(t, evaluators, repository)
	tests := []struct {
		name   string
		mutate func(*SegmentScreeningMedia)
	}{
		{name: "relative", mutate: func(media *SegmentScreeningMedia) { media.EvidencePath = "evidence.mp4" }},
		{name: "unclean", mutate: func(media *SegmentScreeningMedia) { media.PlaybackPath += "/../playback.mp4" }},
		{name: "duplicate", mutate: func(media *SegmentScreeningMedia) { media.PlaybackPath = media.EvidencePath }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			media := segmentScreeningRuntimeMedia(t)
			test.mutate(&media)
			if _, err := runtime.Screen(t.Context(), media); err == nil {
				t.Fatal("invalid paths were accepted")
			}
		})
	}
	if len(order) != 0 {
		t.Fatalf("invalid media reached repository or evaluators: %v", order)
	}
}

func mustSegmentScreeningRuntime(t *testing.T, items map[SegmentScreeningAxis]*capturedSegmentScreeningEvaluator, repository SegmentScreeningEvidenceRepository) *SegmentScreeningRuntime {
	t.Helper()
	runtime, err := NewSegmentScreeningRuntime([]SegmentScreeningEvaluator{
		items[ScreenVisualSafety], items[ScreenSpokenSafety], items[ScreenWrittenSafety], items[ScreenRights], items[ScreenPlayback],
	}, repository)
	if err != nil {
		t.Fatal(err)
	}
	return runtime
}

func segmentScreeningEvaluatorFixtures(order *[]string) map[SegmentScreeningAxis]*capturedSegmentScreeningEvaluator {
	items := make(map[SegmentScreeningAxis]*capturedSegmentScreeningEvaluator, len(segmentScreeningAxisOrder))
	for index, axis := range segmentScreeningAxisOrder {
		items[axis] = &capturedSegmentScreeningEvaluator{
			axis: axis, profile: screeningProfileFixture(axis, string(rune('1'+index))), outcome: ScreenPass,
			assessedAt: time.Date(2026, time.September, 12, 2, index, 0, 0, time.UTC), order: order,
		}
	}
	return items
}

func segmentScreeningRuntimeMedia(t *testing.T) SegmentScreeningMedia {
	t.Helper()
	return SegmentScreeningMedia{
		Subject:          screeningChildSubjectFixture(t),
		SourceMasterPath: "/private/filler/.loomarr-media/masters/aa/master.mp4",
		EvidencePath:     "/private/filler/.loomarr-media/evidence/bb/evidence.mp4",
		PlaybackPath:     "/private/filler/children/playback.mp4",
	}
}
