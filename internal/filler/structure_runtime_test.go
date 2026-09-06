package filler

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillerstructure"
)

type capturedStructureAssessor struct {
	profile  fillerstructure.AssessorProfile
	recorded fillerstructure.RecordedAssessment
	err      error
	order    *[]string
}

type capturedStructureMediaPreparer struct {
	media  StructureAssessmentMedia
	inputs []StructureAssessmentSource
	err    error
	order  *[]string
}

func (p *capturedStructureMediaPreparer) Prepare(_ context.Context, input StructureAssessmentSource) (StructureAssessmentMedia, error) {
	p.inputs = append(p.inputs, input)
	if p.order != nil {
		*p.order = append(*p.order, "prepare:"+input.Source.SHA256+":"+input.FullPath)
	}
	return p.media, p.err
}

func (a *capturedStructureAssessor) Profile() fillerstructure.AssessorProfile { return a.profile }

func (a *capturedStructureAssessor) AssessCompleteTimeline(_ context.Context, media StructureAssessmentMedia) (fillerstructure.RecordedAssessment, error) {
	*a.order = append(*a.order, a.profile.ID+":"+media.Source.SHA256+":"+media.Assessment.SHA256+":"+media.FullPath)
	return a.recorded, a.err
}

type capturedStructureEvidenceRepository struct {
	order       *[]string
	records     []fillerstructure.RecordedAssessment
	decisions   []fillerstructure.Artifact
	err         error
	decisionErr error
}

func (r *capturedStructureEvidenceRepository) PutStructureDecisionArtifact(_ context.Context, artifact fillerstructure.Artifact) error {
	if r.decisionErr != nil {
		return r.decisionErr
	}
	*r.order = append(*r.order, "persist:decision")
	r.decisions = append(r.decisions, artifact)
	return nil
}

func (r *capturedStructureEvidenceRepository) PutStructureAssessmentEvidence(_ context.Context, recorded fillerstructure.RecordedAssessment) error {
	if r.err != nil {
		return r.err
	}
	*r.order = append(*r.order, "persist:"+recorded.Record.Assessor.ID)
	r.records = append(r.records, recorded)
	return nil
}

func TestStructureAssessmentRuntimeCallsIndependentAssessorsSerially(t *testing.T) {
	source := structureSource(10_000)
	input := StructureAssessmentSource{Source: source, FullPath: "/tmp/source.mp4"}
	media := structureAssessmentMediaFixture(source, "/tmp/conditioned-source.mp4")
	order := []string{}
	assessors := runtimeAssessorFixtures(source, &order)
	preparer := &capturedStructureMediaPreparer{media: media, order: &order}
	evidence := &capturedStructureEvidenceRepository{order: &order}
	runtime, err := NewStructureAssessmentRuntime(assessors, preparer, evidence, 2_000, func() time.Time {
		return time.Date(2026, time.September, 9, 1, 0, 0, 0, time.UTC)
	})
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := runtime.Assess(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	wantOrder := []string{
		"prepare:" + source.SHA256 + ":" + input.FullPath,
		"assessor-a:" + source.SHA256 + ":" + media.Assessment.SHA256 + ":" + media.FullPath,
		"persist:assessor-a",
		"assessor-b:" + source.SHA256 + ":" + media.Assessment.SHA256 + ":" + media.FullPath,
		"persist:assessor-b",
		"persist:decision",
	}
	if !slices.Equal(order, wantOrder) || len(preparer.inputs) != 1 || len(evidence.records) != 2 || len(evidence.decisions) != 1 ||
		evidence.decisions[0].SHA256 != artifact.SHA256 || artifact.Decision.Status != fillerstructure.StatusConfirmed || len(artifact.Decision.Candidates) != 2 {
		t.Fatalf("order=%v artifact=%+v", order, artifact)
	}
}

func TestStructureAssessmentRuntimeRejectsMissingOrDriftedAuthority(t *testing.T) {
	source := structureSource(10_000)
	order := []string{}
	tests := []struct {
		name   string
		mutate func([]CompleteTimelineStructureAssessor)
	}{
		{name: "adapter error", mutate: func(items []CompleteTimelineStructureAssessor) {
			items[1].(*capturedStructureAssessor).err = errors.New("response was not persisted")
		}},
		{name: "profile drift", mutate: func(items []CompleteTimelineStructureAssessor) {
			items[1].(*capturedStructureAssessor).recorded.Record.Assessor.Model = "drifted"
		}},
		{name: "source drift", mutate: func(items []CompleteTimelineStructureAssessor) {
			items[1].(*capturedStructureAssessor).recorded.Record.Source.SHA256 = strings.Repeat("f", 64)
		}},
		{name: "raw response drift", mutate: func(items []CompleteTimelineStructureAssessor) {
			items[1].(*capturedStructureAssessor).recorded.RawResponse = []byte("replaced")
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			items := runtimeAssessorFixtures(source, &order)
			test.mutate(items)
			preparer := &capturedStructureMediaPreparer{media: structureAssessmentMediaFixture(source, "/tmp/conditioned.mp4")}
			runtime, err := NewStructureAssessmentRuntime(items, preparer, &capturedStructureEvidenceRepository{order: &order}, 2_000, time.Now)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := runtime.Assess(t.Context(), StructureAssessmentSource{Source: source, FullPath: "/tmp/source.mp4"}); err == nil {
				t.Fatal("invalid adapter result was reduced")
			}
		})
	}
}

func TestStructureAssessmentRuntimeRejectsNonIndependentConfigurationBeforeCalls(t *testing.T) {
	source := structureSource(10_000)
	order := []string{}
	assessors := runtimeAssessorFixtures(source, &order)
	second := assessors[1].(*capturedStructureAssessor)
	second.profile.ModelFamily = "family-a"
	second.recorded.Record.Assessor.ModelFamily = "family-a"
	if _, err := NewStructureAssessmentRuntime(assessors, &capturedStructureMediaPreparer{}, &capturedStructureEvidenceRepository{order: &order}, 2_000, time.Now); err == nil || len(order) != 0 {
		t.Fatalf("non-independent runtime error=%v calls=%v", err, order)
	}
}

func TestStructureAssessmentRuntimeRejectsInvalidPreparedMediaBeforeAssessors(t *testing.T) {
	source := structureSource(10_000)
	input := StructureAssessmentSource{Source: source, FullPath: "/tmp/source.mp4"}
	tests := []struct {
		name   string
		mutate func(*StructureAssessmentMedia)
	}{
		{name: "source drift", mutate: func(media *StructureAssessmentMedia) { media.Source.SHA256 = strings.Repeat("f", 64) }},
		{name: "profile missing", mutate: func(media *StructureAssessmentMedia) { media.Assessment.ProfileSHA256 = "" }},
		{name: "lineage missing", mutate: func(media *StructureAssessmentMedia) { media.Assessment.LineageSHA256 = "" }},
		{name: "duration drift", mutate: func(media *StructureAssessmentMedia) { media.Assessment.DurationMS += 1_001 }},
		{name: "source reused as derivative path", mutate: func(media *StructureAssessmentMedia) { media.FullPath = input.FullPath }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			order := []string{}
			media := structureAssessmentMediaFixture(source, "/tmp/conditioned.mp4")
			test.mutate(&media)
			runtime, err := NewStructureAssessmentRuntime(
				runtimeAssessorFixtures(source, &order), &capturedStructureMediaPreparer{media: media},
				&capturedStructureEvidenceRepository{order: &order}, 2_000, time.Now,
			)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := runtime.Assess(t.Context(), input); err == nil || len(order) != 0 {
				t.Fatalf("prepared media error=%v assessor order=%v", err, order)
			}
		})
	}
}

func TestStructureAssessmentRuntimeDoesNotReduceUnpersistedEvidence(t *testing.T) {
	source := structureSource(10_000)
	order := []string{}
	assessors := runtimeAssessorFixtures(source, &order)
	want := errors.New("evidence unavailable")
	repository := &capturedStructureEvidenceRepository{order: &order, err: want}
	preparer := &capturedStructureMediaPreparer{media: structureAssessmentMediaFixture(source, "/tmp/conditioned.mp4")}
	runtime, err := NewStructureAssessmentRuntime(assessors, preparer, repository, 2_000, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Assess(t.Context(), StructureAssessmentSource{Source: source, FullPath: "/tmp/source.mp4"}); !errors.Is(err, want) {
		t.Fatalf("error=%v, want persistence failure", err)
	}
	if len(order) != 1 || len(repository.records) != 0 {
		t.Fatalf("unpersisted evidence advanced execution: order=%v records=%d", order, len(repository.records))
	}
}

func TestStructureAssessmentRuntimeDoesNotReturnUnpersistedDecision(t *testing.T) {
	source := structureSource(10_000)
	order := []string{}
	want := errors.New("decision unavailable")
	repository := &capturedStructureEvidenceRepository{order: &order, decisionErr: want}
	preparer := &capturedStructureMediaPreparer{media: structureAssessmentMediaFixture(source, "/tmp/conditioned.mp4")}
	runtime, err := NewStructureAssessmentRuntime(runtimeAssessorFixtures(source, &order), preparer, repository, 2_000, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Assess(t.Context(), StructureAssessmentSource{Source: source, FullPath: "/tmp/source.mp4"}); !errors.Is(err, want) {
		t.Fatalf("error=%v, want decision persistence failure", err)
	}
	if len(repository.records) != 2 || len(repository.decisions) != 0 || !slices.Equal(order[len(order)-1:], []string{"persist:assessor-b"}) {
		t.Fatalf("decision persistence state: order=%v records=%d decisions=%d", order, len(repository.records), len(repository.decisions))
	}
}

func TestStructureAssessmentRuntimeDecisionReplaysItsPersistedAssessmentRecords(t *testing.T) {
	source := structureSource(10_000)
	order := []string{}
	repository := structureEvidenceRepositoryFixture(t)
	preparer := &capturedStructureMediaPreparer{media: structureAssessmentMediaFixture(source, "/tmp/conditioned.mp4")}
	runtime, err := NewStructureAssessmentRuntime(runtimeAssessorFixtures(source, &order), preparer, repository, 2_000, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := runtime.Assess(t.Context(), StructureAssessmentSource{Source: source, FullPath: "/tmp/source.mp4"})
	if err != nil {
		t.Fatal(err)
	}
	replayedArtifact, err := repository.GetStructureDecisionArtifact(t.Context(), artifact.SHA256)
	if err != nil || replayedArtifact.SHA256 != artifact.SHA256 {
		t.Fatalf("decision replay=%+v error=%v", replayedArtifact, err)
	}
	for _, candidate := range artifact.Decision.Candidates {
		recorded, loadErr := repository.GetStructureAssessmentEvidence(t.Context(), candidate.Assessor.AssessmentSHA256)
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		replayed, replayErr := recorded.Record.Candidate()
		if replayErr != nil || replayed.Assessor.AssessmentSHA256 != candidate.Assessor.AssessmentSHA256 || replayed.Unit != candidate.Unit || !slices.Equal(replayed.Segments, candidate.Segments) {
			t.Fatalf("candidate=%+v replayed=%+v error=%v", candidate, replayed, replayErr)
		}
	}
}

func runtimeAssessorFixtures(source SplitSourceAsset, order *[]string) []CompleteTimelineStructureAssessor {
	coreSource := fillerstructure.Source{SHA256: source.SHA256, Bytes: source.Bytes, DurationMS: source.DurationMs}
	media := structureAssessmentMediaFixture(source, "/tmp/conditioned.mp4").Assessment
	build := func(id, family, assessmentDigest string) *capturedStructureAssessor {
		profile := fillerstructure.AssessorProfile{
			ID: id, ModelFamily: family, Provider: "captured", Model: "video-model",
			ModelDigest: strings.Repeat("b", 64), CapabilitySHA256: strings.Repeat("c", 64),
			PromptVersion: "prompt-v1", EvidenceContract: "assessment-v1",
		}
		recorded, err := fillerstructure.NewAssessmentRecord(fillerstructure.AssessmentRecordInput{
			Source: coreSource, Media: media, Assessor: profile,
			MetadataSnapshotSHA256: strings.Repeat("f", 64),
			PromptSHA256:           fillerstructure.DirectVideoPromptSHA256(coreSource.DurationMS),
			SchemaSHA256:           fillerstructure.DirectVideoSchemaSHA256(coreSource.DurationMS),
			RequestSHA256:          strings.Repeat(assessmentDigest, 64), RawResponse: []byte(`{"id":"generation"}`),
			StructuredOutput: `{"segments":[{"endMs":5000,"role":"commercial","decisiveAtMs":[1000],"reason":"offer"},{"endMs":10000,"role":"promo","decisiveAtMs":[7000],"reason":"promotion"}]}`,
			ResolvedProvider: "captured", ResolvedModel: "video-model-revision",
			UpstreamProvider: "provider", UpstreamProviderSlug: "provider-slug", GenerationID: "generation",
			Tokens:           fillerstructure.AssessmentTokenUsage{Prompt: 100, Completion: 20, Video: 90},
			RequestedNanoUSD: 1_000, ReservedNanoUSD: 1_000, ChargedAmountUSD: "0.0000005",
			ChargedNanoUSD: 500, AccountedNanoUSD: 500, ChargeKnown: true,
			State:      fillerstructure.AssessmentRecordAccepted,
			AssessedAt: time.Date(2026, time.September, 9, 0, 0, 0, 0, time.UTC),
		})
		if err != nil {
			panic(err)
		}
		return &capturedStructureAssessor{profile: profile, recorded: recorded, order: order}
	}
	return []CompleteTimelineStructureAssessor{build("assessor-a", "family-a", "1"), build("assessor-b", "family-b", "2")}
}

func structureAssessmentMediaFixture(source SplitSourceAsset, path string) StructureAssessmentMedia {
	return StructureAssessmentMedia{
		Source: source,
		Assessment: fillerstructure.AssessmentMedia{
			SHA256: strings.Repeat("9", 64), Bytes: 1_024, DurationMS: source.DurationMs,
			ProfileSHA256: strings.Repeat("8", 64), LineageSHA256: strings.Repeat("7", 64),
		},
		FullPath: path,
	}
}
