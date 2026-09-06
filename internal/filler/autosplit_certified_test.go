package filler

import (
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillerstructure"
	"github.com/loomarr/loomarr/internal/fillerstructurewindow"
)

func certifiedAutoPolicy() *AutoSplitPolicy {
	return &AutoSplitPolicy{
		Enabled: func() bool { return true }, MinConfidence: func() int { return 70 },
		MaxDuration: func() time.Duration { return 2 * time.Minute },
	}
}

func allowCertifiedStructure(t *testing.T) *StructureMaterializationPolicy {
	t.Helper()
	authority := fillerstructure.Authority{
		SchemaVersion: fillerstructure.AuthoritySchemaVersion, ContractVersion: fillerstructure.AuthorityContractVersion,
		CertificateSHA256: strings.Repeat("f", 64), AssessmentMediaProfileSHA256: strings.Repeat("8", 64),
		MinimumSourceDurationMS: 1, MaximumSourceDurationMS: 120_000, MaximumAssessmentMediaBytes: 64 << 20,
		ReducerVersion:                  fillerstructure.ReducerContractVersion,
		BoundaryToleranceMS:             2_000,
		AllowedUnits:                    []fillerstructure.Unit{fillerstructure.UnitCompilation, fillerstructure.UnitProgrammeSpots},
		AllowedRoles:                    []fillerstructure.Role{fillerstructure.RoleCommercial, fillerstructure.RoleProgrammeFragment, fillerstructure.RolePromo},
		AutomaticMaterializationAllowed: true,
	}
	for _, pair := range [][2]string{{"assessor-a", "family-a"}, {"assessor-b", "family-b"}} {
		authority.Assessors = append(authority.Assessors, fillerstructure.AssessorProfile{
			ID: pair[0], ModelFamily: pair[1], Provider: "fixture-provider", Model: "fixture-model",
			ModelDigest: strings.Repeat("b", 64), CapabilitySHA256: strings.Repeat("c", 64),
			PromptVersion: "prompt-v1", EvidenceContract: "assessment-v1",
		})
	}
	authority.SHA256 = fillerstructure.AuthoritySHA256(authority)
	return &StructureMaterializationPolicy{Authority: &authority}
}

func passingStructureDecision(t *testing.T, assessment SourceStructureAssessment) *fillerstructure.Artifact {
	t.Helper()
	unit := fillerstructure.UnitCompilation
	if assessment.Kind == StructureProgrammeSpots {
		unit = fillerstructure.UnitProgrammeSpots
	}
	source := fillerstructure.Source{SHA256: assessment.Source.SHA256, Bytes: assessment.Source.Bytes, DurationMS: assessment.DurationMs}
	media := fillerstructure.AssessmentMedia{SHA256: strings.Repeat("9", 64), Bytes: assessment.Source.Bytes, DurationMS: assessment.DurationMs, ProfileSHA256: strings.Repeat("8", 64), LineageSHA256: strings.Repeat("7", 64)}
	input, err := fillerstructure.NewCompleteVideoInput(source, media)
	if err != nil {
		t.Fatal(err)
	}
	segments := make([]fillerstructure.Segment, 0, len(assessment.Plan))
	for _, planned := range assessment.Plan {
		segments = append(segments, fillerstructure.Segment{
			StartMS: planned.StartMs, EndMS: planned.EndMs, Role: fillerstructure.Role(planned.Role),
		})
	}
	candidate := func(id, family, digest string) fillerstructure.Candidate {
		return fillerstructure.Candidate{
			Source: source, InputSHA256: input.SHA256,
			Assessor: fillerstructure.Assessor{
				ID: id, ModelFamily: family, Provider: "fixture-provider", Model: "fixture-model",
				ModelDigest: strings.Repeat("b", 64), CapabilitySHA256: strings.Repeat("c", 64),
				PromptVersion: "prompt-v1", EvidenceContract: "assessment-v1",
				AssessmentSHA256: strings.Repeat(digest, 64),
			},
			Unit: unit, Segments: segments,
		}
	}
	artifact, err := fillerstructure.NewArtifact(fillerstructure.Request{
		Source: source, Input: input, BoundaryToleranceMS: 2_000,
		Candidates: []fillerstructure.Candidate{
			candidate("assessor-a", "family-a", "1"), candidate("assessor-b", "family-b", "2"),
		},
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return &artifact
}

func certifiedStructureProposal(t *testing.T) SplitProposal {
	t.Helper()
	observations := []StructureObservation{
		structureObservation("black", ObservationBlackInterval, ObservationProposesBoundary, 29_900, 30_100),
		structureObservation("silence", ObservationSilenceInterval, ObservationProposesBoundary, 29_900, 30_100),
		structureRoleObservation(t, 60_000, 0, 30_000, SegmentRoleCommercial, "role-left"),
		structureRoleObservation(t, 60_000, 30_000, 60_000, SegmentRolePromo, "role-right"),
	}
	assessment := assessStructure(t, 60_000, observations, []StructureRoleClaim{
		structureClaim(0, 30_000, SegmentRoleCommercial, "role-left"),
		structureClaim(30_000, 60_000, SegmentRolePromo, "role-right"),
	})
	segments := []SplitSegment{
		{StartMs: 0, EndMs: 30_000, Category: "toys", BoundaryConfidence: 90},
		{StartMs: 30_000, EndMs: 60_000, Category: "television", BoundaryConfidence: 90},
	}
	artifact := passingStructureDecision(t, assessment)
	projected, err := ProjectConfirmedStructureDecision(assessment.Source, &assessment, *artifact)
	if err != nil {
		t.Fatal(err)
	}
	return SplitProposal{Source: assessment.Source, Structure: &projected, StructureDecision: artifact, Segments: segments}
}

func TestCertifiedStructureMaterializableRequiresCompleteCertifiedPlan(t *testing.T) {
	proposal := certifiedStructureProposal(t)
	partition := CertifiedStructureMaterializable(proposal, certifiedAutoPolicy(), allowCertifiedStructure(t), 10*time.Second)
	if partition.Reject != AutoSplitOK || len(partition.Confirm) != 2 || len(partition.Hold) != 0 || len(partition.Discard) != 0 {
		t.Fatalf("certified partition = %+v", partition)
	}

	tests := []struct {
		name   string
		mutate func(*SplitProposal, *StructureMaterializationPolicy)
		want   AutoSplitReject
	}{
		{name: "missing assessment", mutate: func(p *SplitProposal, _ *StructureMaterializationPolicy) { p.Structure = nil }, want: RejectStructureMissing},
		{name: "missing structure decision", mutate: func(p *SplitProposal, _ *StructureMaterializationPolicy) { p.StructureDecision = nil }, want: RejectStructureInvalid},
		{name: "uncertified slice", mutate: func(_ *SplitProposal, c *StructureMaterializationPolicy) {
			c.Authority.AutomaticMaterializationAllowed = false
			c.Authority.SHA256 = fillerstructure.AuthoritySHA256(*c.Authority)
		}, want: RejectStructureUncertified},
		{name: "duplicate detector span", mutate: func(p *SplitProposal, _ *StructureMaterializationPolicy) {
			p.Segments = append(p.Segments, p.Segments[0])
		}, want: RejectStructureMismatch},
		{name: "prior partial generation", mutate: func(p *SplitProposal, _ *StructureMaterializationPolicy) { p.Spawned = []string{"child"} }, want: RejectStructureMismatch},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := certifiedStructureProposal(t)
			certification := allowCertifiedStructure(t)
			test.mutate(&candidate, certification)
			got := CertifiedStructureMaterializable(candidate, certifiedAutoPolicy(), certification, 10*time.Second)
			if got.Reject != test.want || len(got.Confirm) != 0 {
				t.Fatalf("partition = %+v, want reject %q", got, test.want)
			}
		})
	}
}

func TestCertifiedStructureMaterializableRefusesDuplicateAndShortCertifiedCandidates(t *testing.T) {
	proposal := certifiedStructureProposal(t)
	proposal.Segments[0].DupOf = "old/ad.mp4"
	partition := CertifiedStructureMaterializable(proposal, certifiedAutoPolicy(), allowCertifiedStructure(t), 40*time.Second)
	if partition.Reject != RejectDuplicate || len(partition.Confirm) != 0 || len(partition.Hold) != 2 {
		t.Fatalf("partition = %+v, want duplicate and short certified candidates held", partition)
	}
	if got := partition.Hold[0].HoldReason; got != string(RejectDuplicate) {
		t.Fatalf("duplicate hold reason = %q, want %q", got, RejectDuplicate)
	}
	if got := partition.Hold[1].HoldReason; got != string(RejectTooShort) {
		t.Fatalf("short hold reason = %q, want %q", got, RejectTooShort)
	}
}

func TestCertifiedStructureMaterializableUsesDistinctWindowAuthority(t *testing.T) {
	proposal := certifiedStructureProposal(t)
	proposal.StructureDecision = passingWindowStructureDecision(t, *proposal.StructureDecision)
	projected, err := ProjectConfirmedStructureDecision(proposal.Source, nil, *proposal.StructureDecision)
	if err != nil {
		t.Fatal(err)
	}
	proposal.Structure = &projected
	withoutWindowAuthority := CertifiedStructureMaterializable(proposal, certifiedAutoPolicy(), allowCertifiedStructure(t), 10*time.Second)
	if withoutWindowAuthority.Reject != RejectStructureUncertified || len(withoutWindowAuthority.Confirm) != 0 {
		t.Fatalf("complete-video authority admitted window decision: %+v", withoutWindowAuthority)
	}
	withWindowAuthority := CertifiedStructureMaterializable(proposal, certifiedAutoPolicy(), allowCertifiedWindowStructure(t, *proposal.StructureDecision), 10*time.Second)
	if withWindowAuthority.Reject != AutoSplitOK || len(withWindowAuthority.Confirm) != 2 || len(withWindowAuthority.Hold) != 0 {
		t.Fatalf("window authority did not admit certified held children: %+v", withWindowAuthority)
	}
}

func TestSplitStageAppliesCertifiedWindowPlanWithoutChangingLegacyShadowInput(t *testing.T) {
	proposal := certifiedStructureProposal(t)
	proposal.StructureDecision = passingWindowStructureDecision(t, *proposal.StructureDecision)
	projected, err := ProjectConfirmedStructureDecision(proposal.Source, nil, *proposal.StructureDecision)
	if err != nil {
		t.Fatal(err)
	}
	proposal.Structure = &projected
	for index := range proposal.Segments {
		proposal.Segments[index].Category = ""
		proposal.Segments[index].Audience = ""
	}
	stage := NewSplitStage(nil, nil).
		WithAutoConfirm(*certifiedAutoPolicy(), func() time.Duration { return 10 * time.Second }).
		WithStructureMaterialization(allowCertifiedWindowStructure(t, *proposal.StructureDecision))
	legacy, applied := stage.splitPartitions(proposal)
	if legacy.Verdict() != RejectUntagged || len(legacy.Confirm) != 0 {
		t.Fatalf("legacy shadow input=%+v", legacy)
	}
	if applied.Verdict() != AutoSplitOK || len(applied.Confirm) != 2 || len(applied.Hold) != 0 {
		t.Fatalf("certified application decision=%+v", applied)
	}
}

func TestSplitStageNeverAppliesCompatibilityPartitionWithoutCertifiedAuthority(t *testing.T) {
	proposal := certifiedStructureProposal(t)
	stage := NewSplitStage(nil, nil).
		WithAutoConfirm(*certifiedAutoPolicy(), func() time.Duration { return 10 * time.Second })

	compatibility, application := stage.splitPartitions(proposal)
	if compatibility.Verdict() != AutoSplitOK || len(compatibility.Confirm) != 2 {
		t.Fatalf("compatibility comparison = %+v", compatibility)
	}
	if application.Reject != RejectStructureUncertified || len(application.Confirm) != 0 || len(application.Hold) != 2 {
		t.Fatalf("application partition without authority = %+v", application)
	}
}

func TestSplitStageNeverAppliesCompatibilityPartitionWithoutDecision(t *testing.T) {
	proposal := certifiedStructureProposal(t)
	proposal.StructureDecision = nil
	stage := NewSplitStage(nil, nil).
		WithAutoConfirm(*certifiedAutoPolicy(), func() time.Duration { return 10 * time.Second }).
		WithStructureMaterialization(allowCertifiedStructure(t))

	compatibility, application := stage.splitPartitions(proposal)
	if compatibility.Verdict() != AutoSplitOK || len(compatibility.Confirm) != 2 {
		t.Fatalf("compatibility comparison = %+v", compatibility)
	}
	if application.Reject != RejectStructureInvalid || len(application.Confirm) != 0 || len(application.Hold) != 2 {
		t.Fatalf("application partition without decision = %+v", application)
	}
}

func passingWindowStructureDecision(t *testing.T, complete fillerstructure.Artifact) *fillerstructure.Artifact {
	t.Helper()
	plan, err := fillerstructurewindow.NewPlan(complete.Decision.Source)
	if err != nil {
		t.Fatal(err)
	}
	items := make([]fillerstructure.AssessmentMedia, len(plan.Windows))
	for index, window := range plan.Windows {
		items[index] = fillerstructure.AssessmentMedia{
			SHA256: strings.Repeat(string(rune('4'+index)), 64), Bytes: 1_024,
			DurationMS:    window.MediaEndMS - window.MediaStartMS,
			ProfileSHA256: plan.Profile.AssessmentMediaProfileSHA256,
			LineageSHA256: strings.Repeat(string(rune('7'+index)), 64),
		}
	}
	input, err := fillerstructure.NewWindowMediaSetInput(complete.Decision.Source, plan.SHA256, items)
	if err != nil {
		t.Fatal(err)
	}
	candidates := append([]fillerstructure.Candidate(nil), complete.Decision.Candidates...)
	for index := range candidates {
		candidates[index].InputSHA256 = input.SHA256
		candidates[index].Assessor.PromptVersion = fillerstructurewindow.DirectVideoPromptVersion
		candidates[index].Assessor.EvidenceContract = fillerstructurewindow.CallRecordContractVersion
	}
	artifact, err := fillerstructure.NewArtifact(fillerstructure.Request{
		Source: complete.Decision.Source, Input: input, BoundaryToleranceMS: complete.BoundaryToleranceMS, Candidates: candidates,
	}, complete.DecidedAt)
	if err != nil {
		t.Fatal(err)
	}
	return &artifact
}

func allowCertifiedWindowStructure(t *testing.T, artifact fillerstructure.Artifact) *StructureMaterializationPolicy {
	t.Helper()
	maximumBytes := int64(0)
	for _, item := range artifact.Decision.Input.Items {
		maximumBytes = max(maximumBytes, item.Bytes)
	}
	authority := fillerstructurewindow.MaterializationAuthority{
		SchemaVersion:             fillerstructurewindow.MaterializationAuthoritySchemaVersion,
		ContractVersion:           fillerstructurewindow.MaterializationAuthorityContractVersion,
		WindowCertificationSHA256: strings.Repeat("d", 64), ShortLongShadowSHA256: strings.Repeat("e", 64),
		WindowProfileSHA256:          fillerstructurewindow.CanonicalProfile().SHA256,
		AssessmentMediaProfileSHA256: artifact.Decision.Input.ProfileSHA256,
		MinimumSourceDurationMS:      1, MaximumSourceDurationMS: artifact.Decision.Source.DurationMS,
		MaximumWindowBytes: maximumBytes, MaximumWindows: len(artifact.Decision.Input.Items),
		ReducerVersion: artifact.ReducerVersion, BoundaryToleranceMS: artifact.BoundaryToleranceMS,
		AllowedUnits: []fillerstructure.Unit{artifact.Decision.Unit},
		AllowedRoles: []fillerstructure.Role{fillerstructure.RoleCommercial, fillerstructure.RolePromo},
		ReviewerID:   "fixture-reviewer", ReviewedAt: time.Date(2026, 9, 14, 2, 0, 0, 0, time.UTC),
		AutomaticMaterializationAllowed: true,
	}
	for _, candidate := range artifact.Decision.Candidates {
		authority.Assessors = append(authority.Assessors, fillerstructure.Profile(candidate.Assessor))
	}
	authority.SHA256 = fillerstructurewindow.MaterializationAuthoritySHA256(authority)
	return &StructureMaterializationPolicy{WindowAuthority: &authority}
}

func TestCertifiedStructureMaterializableDoesNotRequirePreChildEnrichment(t *testing.T) {
	proposal := certifiedStructureProposal(t)
	proposal.Segments[1].Category = ""
	proposal.Segments[1].Audience = ""
	proposal.Segments[1].SuggestedEra = 1980
	partition := CertifiedStructureMaterializable(proposal, certifiedAutoPolicy(), allowCertifiedStructure(t), 10*time.Second)
	if partition.Reject != AutoSplitOK || len(partition.Confirm) != 2 || len(partition.Hold) != 0 {
		t.Fatalf("child-only metadata blocked materialization: %+v", partition)
	}
}

func TestReassessProposalStructureReplaysPairedProjection(t *testing.T) {
	proposal := certifiedStructureProposal(t)
	assessment, err := reassessProposalStructure(proposal, proposal.Structure.AssessedAt.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateStructureDecisionProjection(assessment, *proposal.StructureDecision); err != nil {
		t.Fatalf("reassessed paired projection did not replay: %v", err)
	}
}

func TestCertifiedStructureMaterializableDoesNotInventLegacyBoundaryConfidence(t *testing.T) {
	proposal := certifiedStructureProposal(t)
	for index := range proposal.Segments {
		proposal.Segments[index].BoundaryConfidence = 0
		proposal.Segments[index].Unsplittable = true
	}
	partition := CertifiedStructureMaterializable(proposal, certifiedAutoPolicy(), allowCertifiedStructure(t), 10*time.Second)
	if partition.Reject != AutoSplitOK || len(partition.Confirm) != 2 || len(partition.Hold) != 0 {
		t.Fatalf("certified decision was converted back into detector confidence: %+v", partition)
	}
}

func TestCertifiedStructureMaterializableSeparatesProgrammeDiscardFromFiller(t *testing.T) {
	observations := []StructureObservation{
		structureObservation("chapter-left", ObservationChapterEdge, ObservationProposesBoundary, 20_000, 20_000),
		structureObservation("chapter-right", ObservationChapterEdge, ObservationProposesBoundary, 50_000, 50_000),
		structureRoleObservation(t, 70_000, 0, 20_000, SegmentRoleProgrammeFragment, "programme-left"),
		structureRoleObservation(t, 70_000, 20_000, 50_000, SegmentRoleCommercial, "commercial"),
		structureRoleObservation(t, 70_000, 50_000, 70_000, SegmentRoleProgrammeFragment, "programme-right"),
	}
	assessment := assessStructure(t, 70_000, observations, []StructureRoleClaim{
		structureClaim(0, 20_000, SegmentRoleProgrammeFragment, "programme-left"),
		structureClaim(20_000, 50_000, SegmentRoleCommercial, "commercial"),
		structureClaim(50_000, 70_000, SegmentRoleProgrammeFragment, "programme-right"),
	})
	proposal := SplitProposal{Source: assessment.Source, Structure: &assessment, Segments: []SplitSegment{
		{StartMs: 20_000, EndMs: 50_000, Category: "toys"},
	}}
	proposal.StructureDecision = passingStructureDecision(t, assessment)
	projected, err := ProjectConfirmedStructureDecision(proposal.Source, &assessment, *proposal.StructureDecision)
	if err != nil {
		t.Fatal(err)
	}
	proposal.Structure = &projected
	partition := CertifiedStructureMaterializable(proposal, certifiedAutoPolicy(), allowCertifiedStructure(t), 10*time.Second)
	if partition.Reject != AutoSplitOK || len(partition.Confirm) != 1 || partition.Confirm[0].StartMs != 20_000 || len(partition.Discard) != 2 || len(partition.Hold) != 0 {
		t.Fatalf("programme/filler partition = %+v", partition)
	}
}
