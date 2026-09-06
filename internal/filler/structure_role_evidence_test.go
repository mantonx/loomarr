package filler

import (
	"strings"
	"testing"
	"time"
)

func structureRoleEvidenceFixture(t *testing.T) StructureRoleEvidence {
	t.Helper()
	evidence, err := NewStructureRoleEvidence(StructureRoleEvidenceInput{
		Source: structureSource(60_000), StartMs: 0, EndMs: 30_000,
		Role: SegmentRoleCommercial, Reason: "a separately framed product offer",
		Frames:        [][]byte{[]byte("opening frame"), []byte("closing card")},
		PromptVersion: "segment-role-v1", Prompt: "classify this exact segment", Response: `{"role":"commercial"}`,
		RequestedProvider: "ollama", ResolvedProvider: "ollama", RequestedModel: "vision:latest", ResolvedModel: "vision@sha256:abc",
		Modalities: []string{"text", "image", "image"}, Tokens: StructureRoleTokenUsage{Prompt: 20, Completion: 5, Image: 2},
		LatencyMs: 125, Attempts: 1, AssessedAt: time.Date(2026, time.September, 4, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	return evidence
}

func TestNewStructureRoleEvidenceBindsExactRequestAndResponse(t *testing.T) {
	evidence := structureRoleEvidenceFixture(t)
	if len(evidence.FrameSHA256) != 2 || evidence.FrameSHA256[0] == evidence.FrameSHA256[1] || !strings.Contains(evidence.RequestedModel, "latest") || evidence.RequestSHA256 == evidence.ResponseSHA256 || evidence.SHA256 == "" {
		t.Fatalf("role evidence = %+v", evidence)
	}
	observation, err := NewStructureRoleObservation("role-1", evidence)
	if err != nil {
		t.Fatal(err)
	}
	assessment := assessStructure(t, 60_000,
		[]StructureObservation{
			structureObservation("chapter", ObservationChapterEdge, ObservationProposesBoundary, 30_000, 30_000),
			observation,
			structureRoleObservation(t, 60_000, 30_000, 60_000, SegmentRolePromo, "right-role"),
		},
		[]StructureRoleClaim{
			structureClaim(0, 30_000, SegmentRoleCommercial, observation.ID),
			structureClaim(30_000, 60_000, SegmentRolePromo, "right-role"),
		})
	if assessment.Observations[0].RoleEvidence == nil && assessment.Observations[1].RoleEvidence == nil {
		t.Fatal("role evidence disappeared from canonical assessment")
	}
}

func TestNewStructureRoleEvidenceBindsBoundedVideoInsteadOfFrames(t *testing.T) {
	evidence, err := NewStructureRoleEvidence(StructureRoleEvidenceInput{
		Source: structureSource(60_000), StartMs: 10_000, EndMs: 40_000,
		Role: SegmentRolePromo, Reason: "the complete bounded sequence promotes another programme",
		Video: []byte("bounded mp4 evidence"), PromptVersion: "segment-video-role-v1",
		Prompt: "classify this exact bounded video", Response: `{"role":"promo","reason":"programme promotion"}`,
		RequestedProvider: "openrouter", ResolvedProvider: "openrouter", RequestedModel: "video-model", ResolvedModel: "video-model",
		Modalities: []string{"video", "audio", "text"}, Tokens: StructureRoleTokenUsage{Prompt: 30, Completion: 8, Video: 10, Audio: 4},
		LatencyMs: 250, Attempts: 1, GenerationID: "generation-1", AssessedAt: time.Date(2026, time.September, 4, 12, 30, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if evidence.VideoSHA256 == "" || len(evidence.FrameSHA256) != 0 || evidence.RequestSHA256 == evidence.VideoSHA256 {
		t.Fatalf("video role evidence = %+v", evidence)
	}
	mutated := evidence
	mutated.VideoSHA256 = strings.Repeat("a", 64)
	mutated.SHA256 = StructureRoleEvidenceSHA256(mutated)
	if err := ValidateStructureRoleEvidence(mutated); err == nil {
		t.Fatal("video mutation survived request binding")
	}
}

func TestNewStructureRoleEvidenceRequiresExactlyOneMediaForm(t *testing.T) {
	base := StructureRoleEvidenceInput{
		Source: structureSource(60_000), StartMs: 0, EndMs: 30_000,
		Role: SegmentRoleCommercial, Reason: "product offer", PromptVersion: "role-v1", Prompt: "classify", Response: `{}`,
		RequestedProvider: "provider", ResolvedProvider: "provider", RequestedModel: "model", ResolvedModel: "model",
		Modalities: []string{"image", "text"}, Attempts: 1, AssessedAt: time.Now(),
	}
	for name, mutate := range map[string]func(*StructureRoleEvidenceInput){
		"neither": func(*StructureRoleEvidenceInput) {},
		"both": func(input *StructureRoleEvidenceInput) {
			input.Frames = [][]byte{[]byte("frame")}
			input.Video = []byte("video")
		},
	} {
		t.Run(name, func(t *testing.T) {
			input := base
			mutate(&input)
			if _, err := NewStructureRoleEvidence(input); err == nil {
				t.Fatal("ambiguous media evidence was accepted")
			}
		})
	}
}

func TestValidateStructureRoleEvidenceRejectsMutationEvenWhenRehashed(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*StructureRoleEvidence)
	}{
		{name: "request", mutate: func(e *StructureRoleEvidence) { e.StartMs++ }},
		{name: "source", mutate: func(e *StructureRoleEvidence) { e.Source.SHA256 = strings.Repeat("f", 64) }},
		{name: "provider", mutate: func(e *StructureRoleEvidence) { e.ResolvedProvider = "" }},
		{name: "modalities", mutate: func(e *StructureRoleEvidence) { e.Modalities = []string{"text"} }},
		{name: "frame", mutate: func(e *StructureRoleEvidence) { e.FrameSHA256[0] = "bad" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			evidence := structureRoleEvidenceFixture(t)
			test.mutate(&evidence)
			evidence.SHA256 = StructureRoleEvidenceSHA256(evidence)
			if err := ValidateStructureRoleEvidence(evidence); err == nil {
				t.Fatal("mutated role evidence was accepted")
			}
		})
	}
}

func TestAssessSourceStructureRejectsRoleEvidenceForAnotherSource(t *testing.T) {
	evidence := structureRoleEvidenceFixture(t)
	observation, err := NewStructureRoleObservation("role-1", evidence)
	if err != nil {
		t.Fatal(err)
	}
	source := structureSource(60_000)
	source.SHA256 = strings.Repeat("d", 64)
	_, err = AssessSourceStructure(SourceStructureInput{
		Source: source, Observations: []StructureObservation{observation},
		AssessedAt: time.Date(2026, time.September, 4, 13, 0, 0, 0, time.UTC),
	})
	if err == nil || !strings.Contains(err.Error(), "different source") {
		t.Fatalf("source-binding error = %v", err)
	}
}
