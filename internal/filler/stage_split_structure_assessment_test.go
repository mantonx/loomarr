package filler_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/filler"
	"github.com/loomarr/loomarr/internal/fillerstructure"
	"github.com/loomarr/loomarr/internal/fillerstructurewindow"
)

type capturedStructureDecisioner struct {
	artifact fillerstructure.Artifact
	media    []filler.StructureAssessmentSource
	err      error
}

func (d *capturedStructureDecisioner) Assess(_ context.Context, media filler.StructureAssessmentSource) (fillerstructure.Artifact, error) {
	d.media = append(d.media, media)
	return d.artifact, d.err
}

func TestSplitStagePersistsCompleteTimelineDecisionOnceAndProjectsItsCuts(t *testing.T) {
	st := newSplitMemStore()
	hash := seedCompilation(st, "comps/model-timeline.mp4", 60_000)
	clip := st.clips[hash]
	clip.IsComposite = true
	st.clips[hash] = clip
	tools := &fakeTools{chapters: []filler.Chapter{
		{StartMs: 0, EndMs: 28_000, Title: "detector left"},
		{StartMs: 28_000, EndMs: 60_000, Title: "detector right"},
	}}
	drop := t.TempDir()
	splitter := newSplitter(st, tools, nil, drop)
	proposal, err := splitter.Propose(t.Context(), hash)
	if err != nil {
		t.Fatal(err)
	}
	decisioner := &capturedStructureDecisioner{artifact: structureDecisionArtifact(t, proposal.Source, 30_000, false)}
	stage := filler.NewSplitStage(splitter, st).WithCompleteTimelineStructureAssessment(decisioner)

	result, err := stage.Run(t.Context(), clip)
	if err != nil || result.Verdict != filler.VerdictReview {
		t.Fatalf("result=%+v error=%v", result, err)
	}
	stored, err := st.GetSplitProposal(t.Context(), proposal.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(decisioner.media) != 1 || decisioner.media[0].Source != proposal.Source ||
		decisioner.media[0].FullPath != filepath.Join(drop, filepath.FromSlash(clip.Path)) || stored.StructureDecision == nil ||
		stored.StructureDecision.SHA256 != decisioner.artifact.SHA256 || stored.Structure == nil ||
		stored.Structure.Kind != filler.StructureCompilationBreak || len(stored.Structure.Plan) != 2 ||
		stored.Structure.Plan[0].EndMs != 30_000 || stored.Structure.Plan[1].StartMs != 30_000 {
		t.Fatalf("media=%+v stored=%+v", decisioner.media, stored)
	}
	if _, err := stage.Run(t.Context(), clip); err != nil {
		t.Fatal(err)
	}
	if len(decisioner.media) != 1 {
		t.Fatalf("persisted decision was reassessed: calls=%d", len(decisioner.media))
	}
}

func TestHeldCompleteTimelineDecisionDoesNotReplaceDetectorAssessment(t *testing.T) {
	st := newSplitMemStore()
	hash := seedCompilation(st, "comps/held-timeline.mp4", 60_000)
	tools := &fakeTools{chapters: []filler.Chapter{
		{StartMs: 0, EndMs: 28_000}, {StartMs: 28_000, EndMs: 60_000},
	}}
	splitter := newSplitter(st, tools, nil, t.TempDir())
	proposal, err := splitter.Propose(t.Context(), hash)
	if err != nil {
		t.Fatal(err)
	}
	detectorSHA := proposal.Structure.SHA256
	decisioner := &capturedStructureDecisioner{artifact: structureDecisionArtifact(t, proposal.Source, 30_000, true)}
	updated, err := splitter.AssessProposalStructure(t.Context(), *proposal, decisioner)
	if err != nil {
		t.Fatal(err)
	}
	if updated.StructureDecision == nil || updated.StructureDecision.Decision.Status != fillerstructure.StatusHeld ||
		updated.Structure == nil || updated.Structure.SHA256 != detectorSHA || updated.Structure.Plan[0].EndMs != 28_000 {
		t.Fatalf("updated proposal=%+v", updated)
	}
}

func TestSplitStageAssessmentFailureHoldsProposalForReview(t *testing.T) {
	st := newSplitMemStore()
	hash := seedCompilation(st, "comps/assessment-unavailable.mp4", 60_000)
	clip := st.clips[hash]
	clip.IsComposite = true
	st.clips[hash] = clip
	tools := &fakeTools{chapters: []filler.Chapter{
		{StartMs: 0, EndMs: 30_000}, {StartMs: 30_000, EndMs: 60_000},
	}}
	drop := t.TempDir()
	splitter := newSplitter(st, tools, nil, drop)
	proposal, err := splitter.Propose(t.Context(), hash)
	if err != nil {
		t.Fatal(err)
	}
	decisioner := &capturedStructureDecisioner{err: errors.New("provider unavailable at /private/provider-response.json")}
	stage := filler.NewSplitStage(splitter, st).WithCompleteTimelineStructureAssessment(decisioner)

	result, err := stage.Run(t.Context(), clip)
	if err != nil || result.Verdict != filler.VerdictReview {
		t.Fatalf("result=%+v error=%v", result, err)
	}
	if strings.Contains(result.Note, "provider") || strings.Contains(result.Note, "/private/") {
		t.Fatalf("review note exposes assessment error: %q", result.Note)
	}
	stored, err := st.GetSplitProposal(t.Context(), proposal.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Source != proposal.Source || len(stored.Segments) != len(proposal.Segments) || stored.StructureDecision != nil {
		t.Fatalf("assessment failure changed pending proposal: %+v", stored)
	}
	if len(st.clips) != 1 {
		t.Fatalf("assessment failure created split children: clips=%d", len(st.clips))
	}
}

func TestSplitStageAssessmentCancellationRemainsResumable(t *testing.T) {
	st := newSplitMemStore()
	hash := seedCompilation(st, "comps/assessment-cancelled.mp4", 60_000)
	clip := st.clips[hash]
	clip.IsComposite = true
	st.clips[hash] = clip
	tools := &fakeTools{chapters: []filler.Chapter{
		{StartMs: 0, EndMs: 30_000}, {StartMs: 30_000, EndMs: 60_000},
	}}
	splitter := newSplitter(st, tools, nil, t.TempDir())
	if _, err := splitter.Propose(t.Context(), hash); err != nil {
		t.Fatal(err)
	}
	decisioner := &capturedStructureDecisioner{err: context.Canceled}
	stage := filler.NewSplitStage(splitter, st).WithCompleteTimelineStructureAssessment(decisioner)

	result, err := stage.Run(t.Context(), clip)
	if !errors.Is(err, context.Canceled) || result.Verdict == filler.VerdictReview {
		t.Fatalf("result=%+v error=%v", result, err)
	}
}

func TestSplitStageProjectsConfirmedWindowSetDecisionIntoCompilationCuts(t *testing.T) {
	st := newSplitMemStore()
	hash := seedCompilation(st, "comps/window-timeline.mp4", 300_000)
	clip := st.clips[hash]
	clip.IsComposite = true
	st.clips[hash] = clip
	tools := &fakeTools{chapters: []filler.Chapter{
		{StartMs: 0, EndMs: 145_000, Title: "detector left"},
		{StartMs: 145_000, EndMs: 300_000, Title: "detector right"},
	}}
	drop := t.TempDir()
	splitter := newSplitter(st, tools, nil, drop)
	proposal, err := splitter.Propose(t.Context(), hash)
	if err != nil {
		t.Fatal(err)
	}
	artifact := structureWindowDecisionArtifact(t, proposal.Source, 150_000)
	stage := filler.NewSplitStage(splitter, st).WithCompleteTimelineStructureAssessment(&capturedStructureDecisioner{artifact: artifact})
	result, err := stage.Run(t.Context(), clip)
	if err != nil || result.Verdict != filler.VerdictReview {
		t.Fatalf("result=%+v error=%v", result, err)
	}
	stored, err := st.GetSplitProposal(t.Context(), proposal.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.StructureDecision == nil || stored.StructureDecision.Decision.Input.Kind != fillerstructure.AssessmentInputWindowMediaSet ||
		stored.Structure == nil || stored.Structure.Kind != filler.StructureCompilationBreak || len(stored.Structure.Plan) != 2 ||
		stored.Structure.Plan[0].StartMs != 0 || stored.Structure.Plan[0].EndMs != 150_000 ||
		stored.Structure.Plan[1].StartMs != 150_000 || stored.Structure.Plan[1].EndMs != 300_000 {
		t.Fatalf("stored proposal=%+v", stored)
	}
}

func structureDecisionArtifact(t *testing.T, source filler.SplitSourceAsset, joinMS int64, disagree bool) fillerstructure.Artifact {
	t.Helper()
	core := fillerstructure.Source{SHA256: source.SHA256, Bytes: source.Bytes, DurationMS: source.DurationMs}
	media := fillerstructure.AssessmentMedia{
		SHA256: strings.Repeat("9", 64), Bytes: source.Bytes, DurationMS: source.DurationMs,
		ProfileSHA256: strings.Repeat("8", 64), LineageSHA256: strings.Repeat("7", 64),
	}
	input, err := fillerstructure.NewCompleteVideoInput(core, media)
	if err != nil {
		t.Fatal(err)
	}
	candidate := func(id, family, digest string, segments []fillerstructure.Segment) fillerstructure.Candidate {
		return fillerstructure.Candidate{
			Source: core, InputSHA256: input.SHA256,
			Assessor: fillerstructure.Assessor{
				ID: id, ModelFamily: family, Provider: "captured", Model: "model-" + id,
				ModelDigest: strings.Repeat("a", 64), CapabilitySHA256: strings.Repeat("b", 64),
				PromptVersion: "prompt-v1", EvidenceContract: "assessment-v1",
				AssessmentSHA256: strings.Repeat(digest, 64),
			},
			Unit: fillerstructure.UnitCompilation, Segments: segments,
		}
	}
	segments := []fillerstructure.Segment{
		{StartMS: 0, EndMS: joinMS, Role: fillerstructure.RoleCommercial},
		{StartMS: joinMS, EndMS: source.DurationMs, Role: fillerstructure.RolePromo},
	}
	left := candidate("assessor-a", "family-a", "1", segments)
	right := candidate("assessor-b", "family-b", "2", segments)
	if disagree {
		right.Unit = fillerstructure.UnitStandalone
		right.Role = fillerstructure.RoleCommercial
		right.Segments = []fillerstructure.Segment{{StartMS: 0, EndMS: source.DurationMs, Role: fillerstructure.RoleCommercial}}
	}
	artifact, err := fillerstructure.NewArtifact(fillerstructure.Request{
		Source: core, Input: input, BoundaryToleranceMS: 2_000, Candidates: []fillerstructure.Candidate{left, right},
	}, time.Date(2026, time.September, 10, 7, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return artifact
}

func structureWindowDecisionArtifact(t *testing.T, source filler.SplitSourceAsset, joinMS int64) fillerstructure.Artifact {
	t.Helper()
	core := fillerstructure.Source{SHA256: source.SHA256, Bytes: source.Bytes, DurationMS: source.DurationMs}
	plan, err := fillerstructurewindow.NewPlan(core)
	if err != nil {
		t.Fatal(err)
	}
	media := make([]fillerstructure.AssessmentMedia, len(plan.Windows))
	for ordinal, window := range plan.Windows {
		media[ordinal] = fillerstructure.AssessmentMedia{
			SHA256: strings.Repeat(string(rune('1'+ordinal)), 64), Bytes: 1_024,
			DurationMS: window.MediaEndMS - window.MediaStartMS, ProfileSHA256: plan.Profile.AssessmentMediaProfileSHA256,
			LineageSHA256: strings.Repeat(string(rune('5'+ordinal)), 64),
		}
	}
	set, err := fillerstructurewindow.NewMediaSet(plan, media)
	if err != nil {
		t.Fatal(err)
	}
	input, err := fillerstructurewindow.ReducerInput(set)
	if err != nil {
		t.Fatal(err)
	}
	segments := []fillerstructure.Segment{
		{StartMS: 0, EndMS: joinMS, Role: fillerstructure.RoleCommercial},
		{StartMS: joinMS, EndMS: source.DurationMs, Role: fillerstructure.RolePromo},
	}
	candidate := func(id, family, digit string) fillerstructure.Candidate {
		value, candidateErr := fillerstructure.NewCandidate(core, input.SHA256, fillerstructure.Assessor{
			ID: id, ModelFamily: family, Provider: "captured", Model: "model-" + id,
			ModelDigest: strings.Repeat("a", 64), CapabilitySHA256: strings.Repeat("b", 64),
			PromptVersion:    fillerstructurewindow.DirectVideoPromptVersion,
			EvidenceContract: fillerstructurewindow.CallRecordContractVersion,
			AssessmentSHA256: strings.Repeat(digit, 64),
		}, "", segments)
		if candidateErr != nil {
			t.Fatal(candidateErr)
		}
		return value
	}
	artifact, err := fillerstructure.NewArtifact(fillerstructure.Request{
		Source: core, Input: input, BoundaryToleranceMS: 2_000,
		Candidates: []fillerstructure.Candidate{candidate("assessor-a", "family-a", "8"), candidate("assessor-b", "family-b", "9")},
	}, time.Date(2026, time.September, 12, 7, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return artifact
}
