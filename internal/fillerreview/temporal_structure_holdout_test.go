package fillerreview

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillereval"
	"github.com/loomarr/loomarr/internal/fillerreference"
)

func TestTemporalStructureHoldoutPlanReproducesCompleteBlindedChallenge(t *testing.T) {
	fixture := newTemporalStructureHoldoutFixture(t)
	planRoot := filepath.Join(t.TempDir(), "plan")
	if _, err := BuildTemporalStructureHoldoutPlan(fixture.config(planRoot)); err != nil {
		t.Fatal(err)
	}
	authoringPath := filepath.Join(planRoot, "authoring.json")
	authoring := readStrictTestJSON[TemporalStructureChallengeAuthoring](t, authoringPath)
	media := &fakeTemporalStructureMedia{durationByPath: make(map[string]int64, len(authoring.Sources))}
	for _, source := range authoring.Sources {
		media.durationByPath[filepath.Join(fixture.root, filepath.FromSlash(source.Path))] = source.DurationMS
	}
	build := func(output string) TemporalStructureChallengeResult {
		t.Helper()
		result, err := BuildTemporalStructureChallenge(context.Background(), TemporalStructureChallengeConfig{
			AuthoringPath: authoringPath, PlanReceiptPath: filepath.Join(planRoot, "receipt.json"),
			SourceRoot:  fixture.root,
			OutputDir:   output,
			ChallengeID: "complete-holdout-reproduction",
			Seed:        "complete-holdout-blinding-seed",
			GeneratedAt: fixture.plannedAt.Add(time.Hour),
			Media:       media,
		})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	firstRoot := filepath.Join(t.TempDir(), "first")
	secondRoot := filepath.Join(t.TempDir(), "second")
	first := build(firstRoot)
	second := build(secondRoot)
	if first.Cases != TemporalStructureHoldoutCases || first != second {
		t.Fatalf("complete holdout results differ: first=%+v second=%+v", first, second)
	}
	if !bytes.Equal(readTree(t, firstRoot), readTree(t, secondRoot)) {
		t.Fatal("complete 36-case plan did not reproduce byte-identical public and private trees")
	}
	authority := readStrictTestJSON[TemporalStructureChallengeAuthority](t, filepath.Join(firstRoot, "private", "authority.json"))
	receiptRaw, err := os.ReadFile(filepath.Join(planRoot, "receipt.json"))
	if err != nil {
		t.Fatal(err)
	}
	if authority.PlanContractVersion != TemporalStructureHoldoutContractVersion || authority.PlanReceiptSHA256 != hashBytes(receiptRaw) {
		t.Fatalf("challenge does not bind the validated plan receipt: %+v", authority)
	}
}

func TestBuildTemporalStructureChallengeRejectsReceiptThatDoesNotBindAuthoring(t *testing.T) {
	fixture := newTemporalStructureHoldoutFixture(t)
	planRoot := filepath.Join(t.TempDir(), "plan")
	if _, err := BuildTemporalStructureHoldoutPlan(fixture.config(planRoot)); err != nil {
		t.Fatal(err)
	}
	receipt := readStrictTestJSON[TemporalStructureHoldoutReceipt](t, filepath.Join(planRoot, "receipt.json"))
	receipt.AuthoringSHA256 = strings.Repeat("f", 64)
	receiptPath := writeTemporalHumanJSON(t, t.TempDir(), "receipt.json", receipt)
	media := &fakeTemporalStructureMedia{durationByPath: map[string]int64{}}
	_, err := BuildTemporalStructureChallenge(context.Background(), TemporalStructureChallengeConfig{
		AuthoringPath: filepath.Join(planRoot, "authoring.json"), PlanReceiptPath: receiptPath,
		SourceRoot: fixture.root, OutputDir: filepath.Join(t.TempDir(), "challenge"), ChallengeID: "receipt-tamper",
		Seed: "blinding-seed", GeneratedAt: fixture.plannedAt.Add(time.Hour), Media: media,
	})
	if err == nil || !strings.Contains(err.Error(), "does not bind authoring bytes") {
		t.Fatalf("receipt binding error = %v", err)
	}
	if media.probeCalls != 0 {
		t.Fatalf("receipt tamper reached media probing: calls=%d", media.probeCalls)
	}
}

func TestBuildTemporalStructureHoldoutPlanRequiresExplicitLineageMode(t *testing.T) {
	fixture := newTemporalStructureHoldoutFixture(t)
	config := fixture.config(filepath.Join(t.TempDir(), "plan"))
	config.Genesis = false
	if _, err := BuildTemporalStructureHoldoutPlan(config); err == nil || !strings.Contains(err.Error(), "lineage mode") {
		t.Fatalf("omitted lineage error = %v", err)
	}
	config.Genesis = true
	config.PriorAdjudicationPaths = []string{"prior.json"}
	if _, err := BuildTemporalStructureHoldoutPlan(config); err == nil || !strings.Contains(err.Error(), "lineage mode") {
		t.Fatalf("mixed lineage error = %v", err)
	}
}

func TestValidateTemporalStructureHoldoutReceiptAcceptsImmutableV3Authority(t *testing.T) {
	fixture := newTemporalStructureHoldoutFixture(t)
	root := filepath.Join(t.TempDir(), "plan")
	if _, err := BuildTemporalStructureHoldoutPlan(fixture.config(root)); err != nil {
		t.Fatal(err)
	}
	authoring := readStrictTestJSON[TemporalStructureChallengeAuthoring](t, filepath.Join(root, "authoring.json"))
	receipt := readStrictTestJSON[TemporalStructureHoldoutReceipt](t, filepath.Join(root, "receipt.json"))
	receipt.ContractVersion = TemporalStructureHoldoutLegacyContractVersion
	receipt.PlanKind = ""
	receipt.PriorExposure = TemporalStructureHoldoutTrainingExclusion{}
	if err := validateTemporalStructureHoldoutReceipt(receipt, authoring, nil); err != nil {
		t.Fatalf("immutable v3 receipt was rejected: %v", err)
	}
}

func TestBuildTemporalStructureHoldoutPlanBindsAuthoritiesAndBuildsBalancedConstructions(t *testing.T) {
	fixture := newTemporalStructureHoldoutFixture(t)
	first := filepath.Join(t.TempDir(), "first")
	second := filepath.Join(t.TempDir(), "second")
	firstConfig := fixture.config(first)
	result, err := BuildTemporalStructureHoldoutPlan(firstConfig)
	if err != nil {
		t.Fatal(err)
	}
	if result.Cases != TemporalStructureHoldoutCases || !reviewSHA256(result.AuthoringSHA256) || !reviewSHA256(result.ReceiptSHA256) {
		t.Fatalf("result = %+v", result)
	}
	secondConfig := fixture.config(second)
	repeat, err := BuildTemporalStructureHoldoutPlan(secondConfig)
	if err != nil {
		t.Fatal(err)
	}
	if repeat.AuthoringSHA256 != result.AuthoringSHA256 || repeat.ReceiptSHA256 != result.ReceiptSHA256 {
		t.Fatalf("holdout plan is not reproducible: first=%+v repeat=%+v", result, repeat)
	}
	authoring := readStrictTestJSON[TemporalStructureChallengeAuthoring](t, filepath.Join(first, "authoring.json"))
	receipt := readStrictTestJSON[TemporalStructureHoldoutReceipt](t, filepath.Join(first, "receipt.json"))
	unitCounts := map[fillereval.UnitKind]int{}
	for _, item := range authoring.Cases {
		unitCounts[item.Unit]++
	}
	if unitCounts[fillereval.UnitStandalone] != 12 || unitCounts[fillereval.UnitCompilation] != 12 || unitCounts[fillereval.UnitProgrammeExcerpt] != 12 || len(authoring.Sources) != 18 || receipt.StandaloneRoleCounts[fillereval.TemporalRoleBumper] != 2 || receipt.StandaloneRoleCounts[fillereval.TemporalRoleCommercial] != 3 || receipt.StandaloneRoleCounts[fillereval.TemporalRolePromo] != 2 || receipt.StandaloneRoleCounts[fillereval.TemporalRolePSA] != 2 || receipt.StandaloneRoleCounts[fillereval.TemporalRoleTrailer] != 3 || receipt.TrainingAllowed || receipt.ProductionAdmissionAllowed {
		t.Fatalf("authoring counts=%v sources=%d receipt=%+v", unitCounts, len(authoring.Sources), receipt)
	}
	bands := map[string]int{}
	sameRoleByBand := map[string]int{}
	strataByBand := map[string]map[TemporalTransitionStratum]int{"early": {}, "middle": {}, "late": {}}
	usedByBand := map[string]map[string]struct{}{"early": {}, "middle": {}, "late": {}}
	for _, item := range receipt.CompilationConstructions {
		bands[item.JoinBand]++
		strataByBand[item.JoinBand][item.TransitionStratum]++
		if item.Roles[0] == item.Roles[1] {
			sameRoleByBand[item.JoinBand]++
		}
		for _, sourceID := range []string{item.FirstSourceID, item.SecondSourceID} {
			if _, duplicate := usedByBand[item.JoinBand][sourceID]; duplicate {
				t.Fatalf("join band %q reused source %q", item.JoinBand, sourceID)
			}
			usedByBand[item.JoinBand][sourceID] = struct{}{}
		}
	}
	patterns := map[string]int{}
	for _, item := range receipt.ProgrammeConstructions {
		patterns[item.Pattern]++
		if item.StartMS < 10_000 || item.StartMS+item.DurationMS > item.ParentEndMS-10_000 {
			t.Fatalf("programme cut lacks parent margins: %+v", item)
		}
	}
	if bands["early"] != 4 || bands["middle"] != 4 || bands["late"] != 4 || sameRoleByBand["early"] != 2 || sameRoleByBand["middle"] != 2 || sameRoleByBand["late"] != 2 || patterns["dependent_start"] != 4 || patterns["dependent_end"] != 4 || patterns["both_edges"] != 4 {
		t.Fatalf("bands=%v same-role=%v patterns=%v", bands, sameRoleByBand, patterns)
	}
	for _, band := range []string{"early", "middle", "late"} {
		if strataByBand[band][TemporalTransitionBlackBoundary] == 0 || strataByBand[band][TemporalTransitionAudibleNonblackCut] == 0 || strataByBand[band][TemporalTransitionSilenceTouchedNonblackCut] == 0 {
			t.Fatalf("transition strata for %s = %v", band, strataByBand[band])
		}
	}
	if receipt.FutureTrainingExclusion.Split != "holdout" || len(receipt.FutureTrainingExclusion.SourceSHA256) != 18 || len(receipt.FutureTrainingExclusion.FamilyIDs) != 12 || len(receipt.FutureTrainingExclusion.ProgrammeProvenance) != 6 {
		t.Fatalf("future training exclusion = %+v", receipt.FutureTrainingExclusion)
	}
	if _, err := BuildTemporalStructureHoldoutPlan(firstConfig); err == nil {
		t.Fatal("immutable holdout output was overwritten")
	}
}

func TestBuildTemporalStructureHoldoutPlanRejectsRepeatedProgrammeProvenanceParent(t *testing.T) {
	fixture := newTemporalStructureHoldoutFixture(t)
	inventory := readStrictTestJSON[TemporalStructureHoldoutProgrammeInventory](t, fixture.inventory)
	inventory.Sources[1].Provenance.Authority = inventory.Sources[0].Provenance.Authority
	inventory.Sources[1].Provenance.Reference = inventory.Sources[0].Provenance.Reference
	fixture.inventory = writeTemporalHumanJSON(t, t.TempDir(), "programme-inventory.json", inventory)
	_, err := BuildTemporalStructureHoldoutPlan(fixture.config(filepath.Join(t.TempDir(), "output")))
	if err == nil || !strings.Contains(err.Error(), "repeats a provenance parent") {
		t.Fatalf("repeated programme provenance error = %v", err)
	}
}

func TestBuildTemporalStructureHoldoutPlanRejectsProgrammeParentDerivedFromFiller(t *testing.T) {
	tests := map[string]func(*testing.T, *temporalStructureHoldoutFixture, *TemporalStructureHoldoutProgrammeInventory){
		"same bytes": func(t *testing.T, fixture *temporalStructureHoldoutFixture, inventory *TemporalStructureHoldoutProgrammeInventory) {
			manifest := readStrictTestJSON[TemporalTruthEvidenceManifest](t, fixture.manifest)
			path, err := filepath.Rel(fixture.root, filepath.Join(filepath.Dir(fixture.manifest), manifest.Cases[0].Video.Path))
			if err != nil {
				t.Fatal(err)
			}
			inventory.Sources[0].Path = filepath.ToSlash(path)
			inventory.Sources[0].SHA256 = manifest.Cases[0].Video.SHA256
		},
		"same provenance": func(t *testing.T, fixture *temporalStructureHoldoutFixture, inventory *TemporalStructureHoldoutProgrammeInventory) {
			privateMap := readStrictTestJSON[TemporalTruthEvidencePrivateMap](t, fixture.privateMap)
			inventory.Sources[0].Provenance.Authority = "locked-temporal-human-review"
			inventory.Sources[0].Provenance.Reference = privateMap.Entries[0].CaseID
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			fixture := newTemporalStructureHoldoutFixture(t)
			inventory := readStrictTestJSON[TemporalStructureHoldoutProgrammeInventory](t, fixture.inventory)
			mutate(t, &fixture, &inventory)
			fixture.inventory = writeTemporalHumanJSON(t, t.TempDir(), "programme-inventory.json", inventory)
			_, err := BuildTemporalStructureHoldoutPlan(fixture.config(filepath.Join(t.TempDir(), "output")))
			if err == nil || !strings.Contains(err.Error(), "repeats bounded filler") {
				t.Fatalf("programme parent derived from filler error = %v", err)
			}
		})
	}
}

func TestBuildTemporalStructureHoldoutPlanRejectsProhibitedRoleCoverage(t *testing.T) {
	fixture := newTemporalStructureHoldoutFixture(t)
	report := readStrictTestJSON[TemporalSuitabilityComparisonReport](t, fixture.suitability)
	report.CaseComparisons[0].Disposition = "prohibited_hold"
	report.CaseComparisons[0].UnionFlags = []SuitabilityFlag{SuitabilityExplicitNudity}
	report.CaseComparisons[0].CorroboratedFlags = []SuitabilityFlag{SuitabilityExplicitNudity}
	report.CorroboratedProhibitedCases = 1
	report.FlaggedUnionCases = 1
	report.CoverageHoldCases--
	fixture.suitability = writeTemporalHumanJSON(t, t.TempDir(), "suitability.json", report)
	_, err := BuildTemporalStructureHoldoutPlan(fixture.config(filepath.Join(t.TempDir(), "output")))
	if err == nil || !strings.Contains(err.Error(), "insufficient eligible bumper") {
		t.Fatalf("prohibited coverage error = %v", err)
	}
}

func TestBuildTemporalStructureHoldoutPlanRejectsUnsatisfiedTransitionStrata(t *testing.T) {
	fixture := newTemporalStructureHoldoutFixture(t)
	authority := readStrictTestJSON[TemporalTransitionAuthority](t, fixture.transition)
	for index := range authority.Cases {
		authority.Cases[index].Head.Black = nil
		authority.Cases[index].Head.Silence = nil
		authority.Cases[index].Tail.Black = nil
		authority.Cases[index].Tail.Silence = nil
	}
	fixture.transition = writeTemporalHumanJSON(t, t.TempDir(), "transition-authority.json", authority)
	_, err := BuildTemporalStructureHoldoutPlan(fixture.config(filepath.Join(t.TempDir(), "output")))
	if err == nil || !strings.Contains(err.Error(), "cannot jointly satisfy") {
		t.Fatalf("transition quota error = %v", err)
	}
}

func TestBuildTemporalStructureHoldoutPlanRejectsDuplicateFamilyCoverage(t *testing.T) {
	fixture := newTemporalStructureHoldoutFixture(t)
	privateMap := readStrictTestJSON[TemporalTruthEvidencePrivateMap](t, fixture.privateMap)
	audit := readStrictTestJSON[temporalStructureHoldoutFamilyAudit](t, fixture.family)
	for index := range audit.Fingerprints {
		if audit.Fingerprints[index].CaseID == privateMap.Entries[0].CaseID || audit.Fingerprints[index].CaseID == privateMap.Entries[1].CaseID {
			audit.Fingerprints[index].FrameHashes = []uint64{
				0x0f0f0f0f0f0f0f0f, 0x0f0f0f0f0f0f0f0f, 0x0f0f0f0f0f0f0f0f, 0x0f0f0f0f0f0f0f0f,
				0x0f0f0f0f0f0f0f0f, 0x0f0f0f0f0f0f0f0f, 0x0f0f0f0f0f0f0f0f, 0x0f0f0f0f0f0f0f0f,
				0x0f0f0f0f0f0f0f0f, 0x0f0f0f0f0f0f0f0f, 0x0f0f0f0f0f0f0f0f, 0x0f0f0f0f0f0f0f0f,
			}
			audit.Fingerprints[index].AudioRMS = make([]uint32, 50)
		}
	}
	fixture.family = rebuildTemporalStructureHoldoutFamily(t, fixture.referenceAudit, audit)
	_, err := BuildTemporalStructureHoldoutPlan(fixture.config(filepath.Join(t.TempDir(), "output")))
	if err == nil || !strings.Contains(err.Error(), "cannot jointly satisfy") {
		t.Fatalf("duplicate-family coverage error = %v", err)
	}
}

func TestBuildTemporalStructureHoldoutPlanRejectsInventedFamilyGraph(t *testing.T) {
	fixture := newTemporalStructureHoldoutFixture(t)
	audit := readStrictTestJSON[temporalStructureHoldoutFamilyAudit](t, fixture.family)
	audit.Families = []temporalStructureHoldoutDuplicateFamily{{
		FamilyID: "invented-family", Members: []string{audit.Fingerprints[0].CaseID, audit.Fingerprints[1].CaseID},
		CompleteClique: false,
	}}
	audit.Summary.DuplicateFamilies = 1
	audit.Summary.NonCliqueFamilies = 1
	fixture.family = writeTemporalHumanJSON(t, t.TempDir(), "family.json", audit)
	_, err := BuildTemporalStructureHoldoutPlan(fixture.config(filepath.Join(t.TempDir(), "output")))
	if err == nil || !strings.Contains(err.Error(), "canonical duplicate graph") {
		t.Fatalf("invented family graph error = %v", err)
	}
}

func TestBuildTemporalStructureHoldoutPlanRejectsMissingReferenceFamilyFingerprint(t *testing.T) {
	fixture := newTemporalStructureHoldoutFixture(t)
	audit := readStrictTestJSON[temporalStructureHoldoutFamilyAudit](t, fixture.family)
	audit.Fingerprints = audit.Fingerprints[:len(audit.Fingerprints)-1]
	audit.Summary.Cases--
	fixture.family = writeTemporalHumanJSON(t, t.TempDir(), "family.json", audit)
	_, err := BuildTemporalStructureHoldoutPlan(fixture.config(filepath.Join(t.TempDir(), "output")))
	if err == nil || !strings.Contains(err.Error(), "does not cover the reference audit") {
		t.Fatalf("missing family fingerprint error = %v", err)
	}
}

func TestBuildTemporalStructureHoldoutPlanAllowsFamilyAuthoritySupersetOfSelection(t *testing.T) {
	fixture := newTemporalStructureHoldoutFixture(t)
	if _, err := BuildTemporalStructureHoldoutPlan(fixture.config(filepath.Join(t.TempDir(), "output"))); err != nil {
		t.Fatalf("reference family superset was rejected: %v", err)
	}
}

func TestTemporalStructureHoldoutAcceptsBoundLegacyReferenceAudit(t *testing.T) {
	fixture := newTemporalStructureHoldoutFixture(t)
	reference := readStrictTestJSON[fillerreference.Audit](t, fixture.referenceAudit)
	reference.SchemaVersion = 2
	reference.Contract = temporalStructureHoldoutLegacyReferenceContract
	reference.Summary.Contract = temporalStructureHoldoutLegacyReferenceContract
	reference.Inputs.ContentReviewSHA256 = ""
	path := writeTemporalHumanJSON(t, t.TempDir(), "legacy-reference-audit.json", reference)
	if _, _, err := loadTemporalStructureHoldoutReferenceAudit(path, fixture.plannedAt); err != nil {
		t.Fatalf("bound legacy reference audit was rejected: %v", err)
	}
}

func TestTemporalStructureHoldoutRejectsIncompleteReferenceAudit(t *testing.T) {
	fixture := newTemporalStructureHoldoutFixture(t)
	reference := readStrictTestJSON[fillerreference.Audit](t, fixture.referenceAudit)
	reference.Cases = reference.Cases[:len(reference.Cases)-1]
	reference.Summary.Cases--
	reference.Summary.Candidates--
	path := writeTemporalHumanJSON(t, t.TempDir(), "incomplete-reference-audit.json", reference)
	if _, _, err := loadTemporalStructureHoldoutReferenceAudit(path, fixture.plannedAt); err == nil || !strings.Contains(err.Error(), "reference audit is invalid") {
		t.Fatalf("incomplete reference audit error = %v", err)
	}
}

func TestTemporalStructureHoldoutAllowsSelectedReferenceExclusionWithoutFingerprint(t *testing.T) {
	fixture := newTemporalStructureHoldoutFixture(t)
	reference := readStrictTestJSON[fillerreference.Audit](t, fixture.referenceAudit)
	excludedID := reference.Cases[len(reference.Cases)-1].CaseID
	reference.Cases[len(reference.Cases)-1].Disposition = fillerreference.DispositionExclude
	reference.Summary.Candidates--
	reference.Summary.Excluded++
	referencePath := writeTemporalHumanJSON(t, t.TempDir(), "reference-audit.json", reference)
	reference, referenceSHA, err := loadTemporalStructureHoldoutReferenceAudit(referencePath, fixture.plannedAt)
	if err != nil {
		t.Fatal(err)
	}
	family := readStrictTestJSON[temporalStructureHoldoutFamilyAudit](t, fixture.family)
	family.SourceAudit = referenceSHA
	filtered := family.Fingerprints[:0]
	for _, fingerprint := range family.Fingerprints {
		if fingerprint.CaseID != excludedID {
			filtered = append(filtered, fingerprint)
		}
	}
	family.Fingerprints = filtered
	familyPath := rebuildTemporalStructureHoldoutFamily(t, referencePath, family)
	selectionRaw, err := os.ReadFile(fixture.selection)
	if err != nil {
		t.Fatal(err)
	}
	selection, err := fillereval.DecodeTemporalTruthSelection(selectionRaw)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadTemporalStructureHoldoutFamily(familyPath, selection, reference, referenceSHA, fixture.plannedAt); err != nil {
		t.Fatalf("selected reference exclusion was not kept ineligible: %v", err)
	}
}

func TestBuildTemporalStructureHoldoutPlanRejectsMediaQualitySummaryDrift(t *testing.T) {
	fixture := newTemporalStructureHoldoutFixture(t)
	report := readStrictTestJSON[TemporalMediaQualityReport](t, fixture.quality)
	report.PolicyContinueCases--
	fixture.quality = writeTemporalHumanJSON(t, t.TempDir(), "quality.json", report)
	_, err := BuildTemporalStructureHoldoutPlan(fixture.config(filepath.Join(t.TempDir(), "output")))
	if err == nil || !strings.Contains(err.Error(), "summary does not match") {
		t.Fatalf("media-quality summary error = %v", err)
	}
}
