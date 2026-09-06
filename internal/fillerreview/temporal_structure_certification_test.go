package fillerreview

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillereval"
)

func TestPublishTemporalStructureCertificationBindsCompleteHoldoutLineage(t *testing.T) {
	fixture := newTemporalStructureHoldoutFixture(t)
	holdoutRoot := filepath.Join(t.TempDir(), "holdout")
	if _, err := BuildTemporalStructureHoldoutPlan(fixture.config(holdoutRoot)); err != nil {
		t.Fatal(err)
	}
	authoringPath := filepath.Join(holdoutRoot, "authoring.json")
	authoring := readStrictTestJSON[TemporalStructureChallengeAuthoring](t, authoringPath)
	media := &fakeTemporalStructureMedia{durationByPath: make(map[string]int64)}
	for _, source := range authoring.Sources {
		media.durationByPath[filepath.Join(fixture.root, filepath.FromSlash(source.Path))] = source.DurationMS
	}
	challengeRoot := filepath.Join(t.TempDir(), "challenge")
	generatedAt := fixture.plannedAt.Add(time.Hour)
	if _, err := BuildTemporalStructureChallenge(context.Background(), TemporalStructureChallengeConfig{
		AuthoringPath: authoringPath, SourceRoot: fixture.root, OutputDir: challengeRoot,
		ChallengeID: "certification-challenge", Seed: "holdout-seed", GeneratedAt: generatedAt, Media: media,
	}); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(challengeRoot, "public", "manifest.json")
	authorityPath := filepath.Join(challengeRoot, "private", "authority.json")
	manifest := readStrictTestJSON[TemporalStructureChallengeManifest](t, manifestPath)
	authority := readStrictTestJSON[TemporalStructureChallengeAuthority](t, authorityPath)
	publicSHA, err := hashFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	authoritySHA, err := hashFile(authorityPath)
	if err != nil {
		t.Fatal(err)
	}
	completedAt := generatedAt.Add(time.Hour)
	first := exactTemporalStructureAssessmentSet(manifest, authority, publicSHA, authoritySHA, completedAt, "assessor-a", "qwen")
	second := exactTemporalStructureAssessmentSet(manifest, authority, publicSHA, authoritySHA, completedAt, "assessor-b", "claude")
	firstPath := writeTemporalHumanJSON(t, t.TempDir(), "first.json", first)
	secondPath := writeTemporalHumanJSON(t, t.TempDir(), "second.json", second)
	decidedAt := completedAt.Add(time.Hour)
	decisionPath := filepath.Join(t.TempDir(), "decision.json")
	_, decisionDigest, err := PublishTemporalStructureDecisions(TemporalStructureDecisionConfig{
		PublicManifestPath: manifestPath, PrivateAuthoritySHA256: authoritySHA,
		AssessmentPaths: []string{firstPath, secondPath}, ExpectedCases: TemporalStructureHoldoutCases,
		DecidedAt: decidedAt, OutputPath: decisionPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "certification.json")
	config := TemporalStructureCertificationConfig{
		HoldoutAuthoringPath: authoringPath, HoldoutReceiptPath: filepath.Join(holdoutRoot, "receipt.json"),
		PublicManifestPath: manifestPath, PrivateAuthorityPath: authorityPath,
		DecisionPath: decisionPath, AssessmentPaths: []string{firstPath, secondPath},
		CertifiedAt: completedAt.Add(2 * time.Hour), OutputPath: output,
	}
	tampered := readStrictTestJSON[TemporalStructureDecisionReport](t, decisionPath)
	holdTemporalStructureTestDecision(&tampered.Decisions[0])
	tamperedPath := writeTemporalHumanJSON(t, t.TempDir(), "tampered-decision.json", tampered)
	tamperedConfig := config
	tamperedConfig.DecisionPath = tamperedPath
	tamperedConfig.PrivateAuthorityPath = filepath.Join(t.TempDir(), "truth-must-not-open.json")
	tamperedConfig.OutputPath = filepath.Join(t.TempDir(), "tampered-certification.json")
	if _, _, err := PublishTemporalStructureCertification(tamperedConfig); err == nil || !strings.Contains(err.Error(), "does not match deterministic reduction") {
		t.Fatalf("tampered truth-blind decision error = %v", err)
	}
	report, digest, err := PublishTemporalStructureCertification(config)
	if err != nil {
		t.Fatal(err)
	}
	if report.CertificationStatus != TemporalStructureCertificationPassed || report.DecidedCases != TemporalStructureHoldoutCases || report.HeldCases != 0 || report.WrongAutomaticDecisions != 0 || len(report.CertifiedUnits) != len(temporalStructureScoredUnits()) || len(report.CertifiedSlices) != len(temporalStructureCertificationRequiredSlices) || !reviewSHA256(report.HoldoutAuthoringSHA256) || !reviewSHA256(report.HoldoutReceiptSHA256) || !reviewSHA256(report.PublicManifestSHA256) || !reviewSHA256(report.PrivateAuthoritySHA256) || report.AssessmentMediaProfileSHA256 != authority.AssessmentMediaProfile.SHA256 || report.MinimumTimelineDurationMS <= 0 || report.MaximumTimelineDurationMS < report.MinimumTimelineDurationMS || report.MaximumAssessmentMediaBytes <= 0 || report.DecisionSHA256 != decisionDigest || !reviewSHA256(digest) || report.TrainingAllowed || report.ProductionAdmissionAllowed {
		t.Fatalf("certification report = %+v digest=%q", report, digest)
	}
	if _, err := os.Stat(output); err != nil {
		t.Fatal(err)
	}
	if _, _, err := PublishTemporalStructureCertification(config); err == nil {
		t.Fatal("immutable certification output was overwritten")
	}

	t.Run("raw assessor error becomes a passing conservative hold", func(t *testing.T) {
		heldSecond := exactTemporalStructureAssessmentSet(manifest, authority, publicSHA, authoritySHA, completedAt, "assessor-held", "claude")
		standalone := temporalStructureAssessmentByTruth(&heldSecond, fillereval.UnitStandalone)
		if standalone.Role.Kind == fillereval.TemporalRoleCommercial {
			standalone.Role.Kind = fillereval.TemporalRolePromo
			standalone.Segments[0].Role = fillereval.TemporalSegmentPromo
		} else {
			standalone.Role.Kind = fillereval.TemporalRoleCommercial
			standalone.Segments[0].Role = fillereval.TemporalSegmentCommercial
		}
		heldSecondPath := writeTemporalHumanJSON(t, t.TempDir(), "held-second.json", heldSecond)
		heldDecisionPath := filepath.Join(t.TempDir(), "held-decision.json")
		if _, _, err := PublishTemporalStructureDecisions(TemporalStructureDecisionConfig{
			PublicManifestPath: manifestPath, PrivateAuthoritySHA256: authoritySHA,
			AssessmentPaths: []string{firstPath, heldSecondPath}, ExpectedCases: TemporalStructureHoldoutCases,
			DecidedAt: decidedAt, OutputPath: heldDecisionPath,
		}); err != nil {
			t.Fatal(err)
		}
		heldConfig := config
		heldConfig.DecisionPath = heldDecisionPath
		heldConfig.AssessmentPaths = []string{firstPath, heldSecondPath}
		heldConfig.OutputPath = filepath.Join(t.TempDir(), "held-certification.json")
		heldReport, _, err := PublishTemporalStructureCertification(heldConfig)
		if err != nil {
			t.Fatal(err)
		}
		if heldReport.CertificationStatus != TemporalStructureCertificationPassed || heldReport.DecidedCases != TemporalStructureHoldoutCases-1 || heldReport.HeldCases != 1 || heldReport.WrongAutomaticDecisions != 0 {
			t.Fatalf("conservative hold certificate = %+v", heldReport)
		}
	})

	t.Run("one wrong automatic role fails globally", func(t *testing.T) {
		candidate := readStrictTestJSON[TemporalStructureDecisionReport](t, decisionPath)
		candidate.Decisions[0].Segments[0].Role = fillereval.TemporalSegmentNonFiller
		candidate.Decisions[0].Segments[0].Disposition = TemporalStructureDispositionNonFiller
		scored := scoreTemporalStructureCertification(candidate, manifest, authority, config.CertifiedAt)
		if scored.CertificationStatus != TemporalStructureCertificationFailed || scored.WrongAutomaticDecisions != 1 || !slices.Contains(scored.FailureCodes, "wrong_automatic_decision") || !slices.Contains(scored.FailureCodes, "segment_role_error") || len(scored.CertifiedSlices) != 0 {
			t.Fatalf("wrong automatic decision score = %+v", scored)
		}
	})

	t.Run("held cases stay in difficult-slice coverage", func(t *testing.T) {
		candidate := readStrictTestJSON[TemporalStructureDecisionReport](t, decisionPath)
		aliases := map[string]struct{}{}
		for _, truth := range authority.Cases {
			if slices.Contains(truth.Slices, TemporalStructureSliceMixedRoleJoins) {
				aliases[truth.Alias] = struct{}{}
			}
		}
		for index := range candidate.Decisions {
			if _, hold := aliases[candidate.Decisions[index].Alias]; hold {
				holdTemporalStructureTestDecision(&candidate.Decisions[index])
			}
		}
		scored := scoreTemporalStructureCertification(candidate, manifest, authority, config.CertifiedAt)
		for _, slice := range scored.Slices {
			if slice.Slice == TemporalStructureSliceMixedRoleJoins {
				if slice.Passed || slice.HeldCases != slice.Cases || slice.DecidedCases != 0 || !slices.Contains(slice.FailureCodes, "insufficient_slice_decisions") {
					t.Fatalf("held slice score = %+v", slice)
				}
				return
			}
		}
		t.Fatal("mixed-role slice score is absent")
	})
}

func holdTemporalStructureTestDecision(decision *TemporalStructureCaseDecision) {
	decision.Status = TemporalStructureDecisionHeld
	decision.ReasonCodes = []string{temporalStructureDecisionReasonRoleDisagreement}
	decision.Unit = ""
	decision.Role = ""
	decision.Segments = nil
}

func exactTemporalStructureAssessmentSet(manifest TemporalStructureChallengeManifest, authority TemporalStructureChallengeAuthority, publicSHA, authoritySHA string, completedAt time.Time, assessorID, family string) TemporalStructureAssessmentSet {
	durationByAlias := make(map[string]int64, len(manifest.Cases))
	for _, item := range manifest.Cases {
		durationByAlias[item.Alias] = item.Video.DurationMS
	}
	set := TemporalStructureAssessmentSet{
		SchemaVersion: TemporalStructureAssessmentSchemaVersion, ContractVersion: TemporalStructureAssessmentContractVersion,
		ChallengeID: manifest.ChallengeID, PublicManifestSHA256: publicSHA, PrivateAuthoritySHA256: authoritySHA,
		RawResultSHA256: strings.Repeat("a", 64), SnapshotFileSHA256: strings.Repeat("b", 64),
		CapabilitySnapshotSHA256: strings.Repeat("c", 64), CompletedAt: completedAt, LockedAt: completedAt.Add(time.Minute),
		Assessor: fillereval.TemporalAssessorIdentity{
			ID: assessorID, Provider: "provider", Model: family + "/model", ModelFamily: family,
			ModelDigest: strings.Repeat("d", 64), PromptVersion: "structure-v1",
		},
	}
	for _, truth := range authority.Cases {
		duration := durationByAlias[truth.Alias]
		decisive := []int64{min(int64(1_000), duration/2)}
		if len(truth.JoinTimesMS) > 0 {
			decisive = append([]int64(nil), truth.JoinTimesMS...)
		} else if truth.Unit == fillereval.UnitProgrammeExcerpt {
			decisive = []int64{0, duration}
		}
		assessment := TemporalStructureAssessment{
			Alias: truth.Alias, Unit: &TemporalStructureUnitClaim{Kind: truth.Unit, DecisiveAtMS: decisive, Reason: "closed unit"},
			Inference: temporalStructureTestInference(completedAt.Add(-time.Minute), false),
		}
		for index, part := range truth.Segments {
			endMS := part.OutputEndMS
			if index == len(truth.Segments)-1 {
				endMS = duration
			}
			role := fillereval.TemporalSegmentRole(part.SourceRole)
			if part.Provenance.Kind == TemporalStructureSourceProgrammeParent {
				role = fillereval.TemporalSegmentProgrammeFragment
			}
			atMS := part.OutputStartMS + min(int64(1_000), (endMS-part.OutputStartMS)/2)
			assessment.Segments = append(assessment.Segments, TemporalStructureSegmentClaim{
				StartMS: part.OutputStartMS, EndMS: endMS, Role: role,
				DecisiveAtMS: []int64{atMS}, Reason: "closed segment role",
			})
		}
		if truth.Unit == fillereval.UnitStandalone {
			assessment.Role = &TemporalStructureRoleClaim{Kind: truth.Role, DecisiveAtMS: decisive, Reason: "closed role"}
		}
		set.Assessments = append(set.Assessments, assessment)
	}
	return set
}
