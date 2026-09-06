package store

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/filler"
	"github.com/loomarr/loomarr/internal/fillerstructure"
)

func TestSplitProposalProjectedStructureRoundTripRequiresPairedArtifact(t *testing.T) {
	source := filler.SplitSourceAsset{
		Role: filler.SplitSourceLegacyPlayback, SHA256: strings.Repeat("a", 64), Bytes: 100,
		ClipHash: strings.Repeat("b", 64), Path: "aa/bb/source.mp4", DurationMs: 60_000,
	}
	decisionSource := fillerstructure.Source{SHA256: source.SHA256, Bytes: source.Bytes, DurationMS: source.DurationMs}
	media := fillerstructure.AssessmentMedia{SHA256: strings.Repeat("2", 64), Bytes: source.Bytes, DurationMS: source.DurationMs, ProfileSHA256: strings.Repeat("3", 64), LineageSHA256: strings.Repeat("4", 64)}
	input, err := fillerstructure.NewCompleteVideoInput(decisionSource, media)
	if err != nil {
		t.Fatal(err)
	}
	segments := []fillerstructure.Segment{
		{StartMS: 0, EndMS: 30_000, Role: fillerstructure.RoleCommercial},
		{StartMS: 30_000, EndMS: 60_000, Role: fillerstructure.RolePromo},
	}
	candidate := func(id, family, digest string) fillerstructure.Candidate {
		return fillerstructure.Candidate{
			Source: decisionSource, InputSHA256: input.SHA256,
			Assessor: fillerstructure.Assessor{ID: id, ModelFamily: family, Provider: "provider", Model: "model", ModelDigest: strings.Repeat("d", 64), CapabilitySHA256: strings.Repeat("1", 64), PromptVersion: "prompt-v1", EvidenceContract: "assessment-v1", AssessmentSHA256: strings.Repeat(digest, 64)},
			Unit:     fillerstructure.UnitCompilation, Segments: append([]fillerstructure.Segment(nil), segments...),
		}
	}
	artifact, err := fillerstructure.NewArtifact(fillerstructure.Request{
		Source: decisionSource, Input: input, BoundaryToleranceMS: 2_000,
		Candidates: []fillerstructure.Candidate{candidate("assessor-a", "family-a", "e"), candidate("assessor-b", "family-b", "f")},
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	assessment, err := filler.ProjectConfirmedStructureDecision(source, nil, artifact)
	if err != nil {
		t.Fatal(err)
	}
	proposal := filler.SplitProposal{ID: "proposal", ClipHash: source.ClipHash, Source: source, Structure: &assessment, StructureDecision: &artifact}
	raw, err := marshalSplitProposal(proposal)
	if err != nil {
		t.Fatal(err)
	}
	var replayed filler.SplitProposal
	if err := unmarshalSplitProposal(string(raw), &replayed); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(replayed.Structure, proposal.Structure) || !reflect.DeepEqual(replayed.StructureDecision, proposal.StructureDecision) {
		t.Fatalf("paired projection changed across persistence: got=%+v want=%+v", replayed, proposal)
	}
	proposal.StructureDecision = nil
	if _, err := marshalSplitProposal(proposal); err == nil {
		t.Fatal("projected assessment persisted without its replay artifact")
	}
}

func TestUnmarshalSplitProposal_AcceptsLegacyBareSegmentArray(t *testing.T) {
	var p filler.SplitProposal
	err := unmarshalSplitProposal(`[{"index":0,"startMs":0,"endMs":30000,"name":"legacy"}]`, &p)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Ready() || len(p.Segments) != 1 || p.Segments[0].Name != "legacy" {
		t.Fatalf("legacy proposal = %+v, want one ready segment", p)
	}
}

func TestMarshalSplitProposalRejectsRoleEvidenceForAnotherSpan(t *testing.T) {
	source := filler.SplitSourceAsset{
		Role: filler.SplitSourceLegacyPlayback, SHA256: strings.Repeat("a", 64), Bytes: 100,
		ClipHash: strings.Repeat("b", 64), Path: "aa/bb/source.mp4", DurationMs: 60_000,
	}
	evidence, err := filler.NewStructureRoleEvidence(filler.StructureRoleEvidenceInput{
		Source: source, StartMs: 0, EndMs: 30_000, Role: filler.SegmentRoleCommercial, Reason: "product offer",
		Frames: [][]byte{[]byte("frame")}, PromptVersion: "prompt-v1", Prompt: "prompt", Response: `{"role":"commercial"}`,
		RequestedProvider: "ollama", ResolvedProvider: "ollama", RequestedModel: "vision", ResolvedModel: "vision",
		Modalities: []string{"image", "text"}, Attempts: 1, AssessedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	proposal := filler.SplitProposal{
		ID: "proposal", ClipHash: source.ClipHash, Source: source,
		Segments: []filler.SplitSegment{{StartMs: 0, EndMs: 30_001, RoleEvidence: &evidence}},
	}
	if _, err := marshalSplitProposal(proposal); err == nil || !strings.Contains(err.Error(), "does not bind") {
		t.Fatalf("span-binding error = %v", err)
	}
}

func TestMarshalSplitProposalRejectsStructureDecisionForAnotherSource(t *testing.T) {
	source := filler.SplitSourceAsset{
		Role: filler.SplitSourceLegacyPlayback, SHA256: strings.Repeat("a", 64), Bytes: 100,
		ClipHash: strings.Repeat("b", 64), Path: "aa/bb/source.mp4", DurationMs: 60_000,
	}
	decisionSource := fillerstructure.Source{SHA256: strings.Repeat("c", 64), Bytes: source.Bytes, DurationMS: source.DurationMs}
	media := fillerstructure.AssessmentMedia{SHA256: strings.Repeat("2", 64), Bytes: source.Bytes, DurationMS: source.DurationMs, ProfileSHA256: strings.Repeat("3", 64), LineageSHA256: strings.Repeat("4", 64)}
	input, err := fillerstructure.NewCompleteVideoInput(decisionSource, media)
	if err != nil {
		t.Fatal(err)
	}
	candidate := func(id, family, assessmentDigest string) fillerstructure.Candidate {
		return fillerstructure.Candidate{
			Source: decisionSource, InputSHA256: input.SHA256,
			Assessor: fillerstructure.Assessor{
				ID: id, ModelFamily: family, Provider: "provider", Model: "model",
				ModelDigest: strings.Repeat("d", 64), CapabilitySHA256: strings.Repeat("1", 64),
				PromptVersion: "prompt-v1", EvidenceContract: "assessment-v1",
				AssessmentSHA256: assessmentDigest,
			},
			Unit: fillerstructure.UnitStandalone, Role: fillerstructure.RoleCommercial,
			Segments: []fillerstructure.Segment{{StartMS: 0, EndMS: source.DurationMs, Role: fillerstructure.RoleCommercial}},
		}
	}
	artifact, err := fillerstructure.NewArtifact(fillerstructure.Request{
		Source: decisionSource, Input: input, BoundaryToleranceMS: 2_000,
		Candidates: []fillerstructure.Candidate{
			candidate("assessor-a", "family-a", strings.Repeat("e", 64)),
			candidate("assessor-b", "family-b", strings.Repeat("f", 64)),
		},
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	proposal := filler.SplitProposal{ID: "proposal", ClipHash: source.ClipHash, Source: source, StructureDecision: &artifact}
	if _, err := marshalSplitProposal(proposal); err == nil || !strings.Contains(err.Error(), "does not bind") {
		t.Fatalf("source-binding error = %v", err)
	}
}
