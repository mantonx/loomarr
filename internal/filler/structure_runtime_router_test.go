package filler

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/loomarr/loomarr/internal/fillerstructure"
	"github.com/loomarr/loomarr/internal/fillerstructurewindow"
)

type capturedStructureDecisionRoute struct {
	calls  []StructureAssessmentSource
	result fillerstructure.Artifact
	err    error
}

func (r *capturedStructureDecisionRoute) Assess(_ context.Context, input StructureAssessmentSource) (fillerstructure.Artifact, error) {
	r.calls = append(r.calls, input)
	return r.result, r.err
}

func TestStructureAssessmentRouterSelectsExactlyOneDurationSlice(t *testing.T) {
	short := &capturedStructureDecisionRoute{}
	long := &capturedStructureDecisionRoute{}
	router, err := NewStructureAssessmentRuntimeRouter(short, long, 120_000)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name       string
		durationMS int64
		wantShort  int
		wantLong   int
		wantError  bool
	}{
		{name: "below short ceiling", durationMS: 119_999, wantShort: 1},
		{name: "at short ceiling", durationMS: 120_000, wantShort: 1},
		{name: "above short ceiling", durationMS: 120_001, wantLong: 1},
		{name: "at window ceiling", durationMS: fillerstructurewindow.MaximumSourceDurationMS, wantLong: 1},
		{name: "above all capacity", durationMS: fillerstructurewindow.MaximumSourceDurationMS + 1, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			short.calls, long.calls = nil, nil
			input := structureRouteInput(test.durationMS)
			_, err := router.Assess(t.Context(), input)
			if (err != nil) != test.wantError || len(short.calls) != test.wantShort || len(long.calls) != test.wantLong {
				t.Fatalf("error=%v short=%d long=%d", err, len(short.calls), len(long.calls))
			}
		})
	}
}

func TestStructureAssessmentRouterNeverFallsBackAfterSelectedFailure(t *testing.T) {
	want := errors.New("selected runtime failed")
	short := &capturedStructureDecisionRoute{err: want}
	long := &capturedStructureDecisionRoute{}
	router, err := NewStructureAssessmentRuntimeRouter(short, long, 120_000)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := router.Assess(t.Context(), structureRouteInput(60_000)); !errors.Is(err, want) {
		t.Fatalf("error=%v, want selected failure", err)
	}
	if len(short.calls) != 1 || len(long.calls) != 0 {
		t.Fatalf("short=%d long=%d", len(short.calls), len(long.calls))
	}
}

func TestStructureAssessmentRouterRejectsInvalidEnvelopeBeforeWork(t *testing.T) {
	short := &capturedStructureDecisionRoute{}
	long := &capturedStructureDecisionRoute{}
	for _, ceiling := range []int64{0, -1, fillerstructurewindow.MaximumSourceDurationMS, fillerstructurewindow.MaximumSourceDurationMS + 1} {
		if _, err := NewStructureAssessmentRuntimeRouter(short, long, ceiling); err == nil {
			t.Fatalf("ceiling %d was accepted", ceiling)
		}
	}
	if _, err := NewStructureAssessmentRuntimeRouter(nil, long, 120_000); err == nil {
		t.Fatal("nil complete-video runtime was accepted")
	}
}

func structureRouteInput(durationMS int64) StructureAssessmentSource {
	return StructureAssessmentSource{Source: SplitSourceAsset{
		Role: SplitSourceLegacyPlayback, SHA256: strings.Repeat("a", 64), Bytes: 1_024,
		ClipHash: strings.Repeat("b", 64), Path: "source.mp4", DurationMs: durationMS,
	}, FullPath: "/retained/source.mp4"}
}
