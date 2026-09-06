package fillerreview

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillercorpus"
	"github.com/loomarr/loomarr/internal/fillereval"
	"github.com/loomarr/loomarr/internal/fillerreference"
)

func TestLoadTemporalStructureHoldoutReferenceDownloadLedgerRejectsHostileInputs(t *testing.T) {
	fixture := newTemporalStructureHoldoutFixture(t)
	audit := readStrictTestJSON[fillerreference.Audit](t, fixture.referenceAudit)
	ledger := fixture.downloadLedger(t)
	marshal := func(t *testing.T, value fillerreference.DownloadLedger) []byte {
		t.Helper()
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	valid := marshal(t, ledger)
	tests := map[string][]byte{
		"digest drift":        valid,
		"unknown field":       []byte(`{"schemaVersion":1,"unknown":true}`),
		"duplicate key":       []byte(`{"schemaVersion":1,"schemaVersion":1}`),
		"trailing JSON":       append(append([]byte{}, valid...), []byte(` {}`)...),
		"oversize":            make([]byte, temporalStructureProgrammeEvidenceMaxBytes+1),
		"missing ledger path": func() []byte { v := ledger; v.Cases[0].LocalFile = ""; return marshal(t, v) }(),
	}
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "ledger.json")
			if err := os.WriteFile(path, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			testAudit := audit
			if name != "digest drift" {
				testAudit.Inputs.DownloadLedgerSHA256 = hashBytes(raw)
			}
			if _, _, err := loadTemporalStructureHoldoutReferenceDownloadLedger(path, testAudit); err == nil {
				t.Fatalf("accepted hostile reference download ledger %q", name)
			}
		})
	}
	for _, name := range []string{"missing", "extra", "duplicate", "distinct CaseID replacement", "item ID", "content hash", "local path", "schema"} {
		t.Run(name, func(t *testing.T) {
			testLedger := ledger
			testLedger.Cases = append([]fillerreference.DownloadCase(nil), ledger.Cases...)
			switch name {
			case "missing":
				testLedger.Cases = testLedger.Cases[:len(testLedger.Cases)-1]
			case "extra":
				testLedger.Cases = append(testLedger.Cases, testLedger.Cases[0])
			case "duplicate":
				testLedger.Cases[1].CaseID = testLedger.Cases[0].CaseID
			case "distinct CaseID replacement":
				testLedger.Cases[0].CaseID = "replacement-case-id"
			case "item ID":
				testLedger.Cases[0].ItemID = "wrong-item"
			case "content hash":
				testLedger.Cases[0].ContentSHA256 = strings.Repeat("0", 64)
			case "local path":
				testLedger.Cases[0].LocalFile = "wrong.mp4"
			case "schema":
				testLedger.SchemaVersion = 0
			}
			raw := marshal(t, testLedger)
			path := filepath.Join(t.TempDir(), "ledger.json")
			if err := os.WriteFile(path, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			testAudit := audit
			testAudit.Inputs.DownloadLedgerSHA256 = hashBytes(raw)
			if _, _, err := loadTemporalStructureHoldoutReferenceDownloadLedger(path, testAudit); err == nil {
				t.Fatalf("accepted invalid reference download ledger join %q", name)
			}
		})
	}
}

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

func TestBuildTemporalStructureChallengeRejectsMissingLedgerReceiptAndLegacyV5(t *testing.T) {
	fixture := newTemporalStructureHoldoutFixture(t)
	planRoot := filepath.Join(t.TempDir(), "plan")
	if _, err := BuildTemporalStructureHoldoutPlan(fixture.config(planRoot)); err != nil {
		t.Fatal(err)
	}
	receipt := readStrictTestJSON[TemporalStructureHoldoutReceipt](t, filepath.Join(planRoot, "receipt.json"))
	validReceipt := readStrictTestJSON[TemporalStructureHoldoutReceipt](t, filepath.Join(planRoot, "receipt.json"))
	for i, input := range receipt.Inputs {
		if input.Name == "reference_download_ledger" {
			receipt.Inputs = append(receipt.Inputs[:i], receipt.Inputs[i+1:]...)
			break
		}
	}
	missingReceipt := writeTemporalHumanJSON(t, t.TempDir(), "receipt.json", receipt)
	media := &fakeTemporalStructureMedia{durationByPath: map[string]int64{}}
	base := TemporalStructureChallengeConfig{AuthoringPath: filepath.Join(planRoot, "authoring.json"), PlanReceiptPath: missingReceipt, SourceRoot: fixture.root, OutputDir: filepath.Join(t.TempDir(), "missing-ledger"), ChallengeID: "missing-ledger", Seed: "seed", GeneratedAt: fixture.plannedAt.Add(time.Hour), Media: media}
	_, err := BuildTemporalStructureChallenge(context.Background(), base)
	if err == nil || media.probeCalls != 0 {
		t.Fatalf("missing ledger input error=%v probes=%d", err, media.probeCalls)
	}
	if _, statErr := os.Stat(base.OutputDir); !os.IsNotExist(statErr) {
		t.Fatalf("missing ledger input published output: %v", statErr)
	}

	validReceipt.ContractVersion = "filler-temporal-structure-holdout-plan-v5"
	legacy := writeTemporalHumanJSON(t, t.TempDir(), "receipt.json", validReceipt)
	base.PlanReceiptPath, base.OutputDir = legacy, filepath.Join(t.TempDir(), "legacy-v5")
	if _, err := BuildTemporalStructureChallenge(context.Background(), base); err == nil || media.probeCalls != 0 {
		t.Fatalf("legacy receipt error=%v probes=%d", err, media.probeCalls)
	}
	if _, statErr := os.Stat(base.OutputDir); !os.IsNotExist(statErr) {
		t.Fatalf("legacy receipt published output: %v", statErr)
	}
}

func TestBuildTemporalStructureHoldoutPlanRejectsMissingLedgerWithoutPublishing(t *testing.T) {
	fixture := newTemporalStructureHoldoutFixture(t)
	config := fixture.config(filepath.Join(t.TempDir(), "output"))
	config.ReferenceDownloadLedgerPath = filepath.Join(t.TempDir(), "missing-ledger.json")
	if _, err := BuildTemporalStructureHoldoutPlan(config); err == nil {
		t.Fatal("accepted missing reference download ledger")
	}
	if _, err := os.Stat(config.OutputDir); !os.IsNotExist(err) {
		t.Fatalf("missing ledger published output: %v", err)
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
	if unitCounts[fillereval.UnitStandalone] != 12 || unitCounts[fillereval.UnitCompilation] != 24 || unitCounts[fillereval.UnitProgrammeExcerpt] != 12 || unitCounts[fillereval.UnitProgrammeSpots] != 12 || len(authoring.Sources) != 18 || receipt.StandaloneRoleCounts[fillereval.TemporalRoleBumper] != 2 || receipt.StandaloneRoleCounts[fillereval.TemporalRoleCommercial] != 3 || receipt.StandaloneRoleCounts[fillereval.TemporalRolePromo] != 2 || receipt.StandaloneRoleCounts[fillereval.TemporalRolePSA] != 2 || receipt.StandaloneRoleCounts[fillereval.TemporalRoleTrailer] != 3 || receipt.TrainingAllowed == nil || *receipt.TrainingAllowed || receipt.ProductionAdmissionAllowed == nil || *receipt.ProductionAdmissionAllowed {
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
	spotPatterns := map[string]int{}
	spotFillers := map[string]struct{}{}
	for _, item := range receipt.ProgrammeSpotConstructions {
		spotPatterns[item.Pattern]++
		spotFillers[item.FillerSourceID] = struct{}{}
	}
	multiTraits := map[string]int{}
	multiSources := map[string]int{}
	for _, item := range receipt.MultiCompilationConstructions {
		multiTraits[item.Trait]++
		for _, sourceID := range item.SourceIDs {
			multiSources[sourceID]++
		}
	}
	if bands["early"] != 4 || bands["middle"] != 4 || bands["late"] != 4 || patterns["near_parent_start"] != 6 || patterns["near_parent_end"] != 6 || spotPatterns["early_insert"] != 6 || spotPatterns["late_insert"] != 6 || len(spotFillers) != 12 || multiTraits[temporalStructureMultiSameRoleJoin] != 6 || multiTraits[temporalStructureMultiMixedRoleJoins] != 6 || len(multiSources) != 12 {
		t.Fatalf("bands=%v patterns=%v", bands, patterns)
	}
	for sourceID, count := range multiSources {
		if count != 3 {
			t.Fatalf("multi-item source %q used %d times", sourceID, count)
		}
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
	mutateTemporalStructureProgrammeRecord(t, fixture, &inventory, func(record *fillercorpus.Inventory) {
		record.Cases[1].ItemURL = inventory.Sources[0].Provenance.Reference
	})
	fixture.inventory = writeTemporalHumanJSON(t, t.TempDir(), "programme-inventory.json", inventory)
	_, err := BuildTemporalStructureHoldoutPlan(fixture.config(filepath.Join(t.TempDir(), "output")))
	if err == nil || !strings.Contains(err.Error(), "repeats a provenance parent") {
		t.Fatalf("repeated programme provenance error = %v", err)
	}
}

func TestLoadTemporalStructureHoldoutProgrammeInventoryRejectsProgrammeParentDerivedFromFiller(t *testing.T) {
	tests := map[string]func(*testing.T, *temporalStructureHoldoutFixture, *TemporalStructureHoldoutProgrammeInventory, *fillerreference.DownloadLedger){
		"same authority item identity": func(t *testing.T, fixture *temporalStructureHoldoutFixture, inventory *TemporalStructureHoldoutProgrammeInventory, ledger *fillerreference.DownloadLedger) {
			inventory.Sources[0].Provenance.Authority = ledger.Cases[0].Authority
			inventory.Sources[0].Provenance.ItemID = ledger.Cases[0].ItemID
			mutateTemporalStructureProgrammeRecord(t, *fixture, inventory, func(record *fillercorpus.Inventory) {
				record.Cases[0].Authority = ledger.Cases[0].Authority
				record.Cases[0].ItemID = ledger.Cases[0].ItemID
				record.Cases[0].CaseID = fillercorpus.CaseID(ledger.Cases[0].Authority, ledger.Cases[0].ItemID)
				record.Captures[0].PredictedMediaBytes -= record.Cases[0].Representation.Bytes
				record.Captures[0].MaxPredictedMediaBytes = record.Captures[0].PredictedMediaBytes
				capture := record.Captures[0]
				capture.CaptureID = fillercorpus.NewCaptureID(ledger.Cases[0].Authority, "", "programme")
				capture.Authority = ledger.Cases[0].Authority
				capture.PredictedMediaBytes = record.Cases[0].Representation.Bytes
				capture.MaxPredictedMediaBytes = capture.PredictedMediaBytes
				record.Captures = append(record.Captures, capture)
				record.Cases[0].CaptureIDs = []string{capture.CaptureID}
			})
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			fixture := newTemporalStructureHoldoutFixture(t)
			inventory := readStrictTestJSON[TemporalStructureHoldoutProgrammeInventory](t, fixture.inventory)
			ledger := fixture.downloadLedger(t)
			mutate(t, &fixture, &inventory, &ledger)
			fixture.inventory = writeTemporalHumanJSON(t, t.TempDir(), "programme-inventory.json", inventory)
			_, _, err := loadTemporalStructureHoldoutProgrammeInventory(fixture.inventory, fixture.root, ledger, fixture.plannedAt)
			if err == nil || !strings.Contains(err.Error(), "repeats bounded filler") {
				t.Fatalf("programme parent derived from filler error = %v", err)
			}
		})
	}
}

func TestBuildTemporalStructureHoldoutPlanRejectsProgrammeParentWithReferenceLineage(t *testing.T) {
	fixture := newTemporalStructureHoldoutFixture(t)
	inventory := readStrictTestJSON[TemporalStructureHoldoutProgrammeInventory](t, fixture.inventory)
	ledger := fixture.downloadLedger(t)
	inventory.Sources[0].Provenance.Authority = ledger.Cases[0].Authority
	inventory.Sources[0].Provenance.ItemID = ledger.Cases[0].ItemID
	mutateTemporalStructureProgrammeRecord(t, fixture, &inventory, func(record *fillercorpus.Inventory) {
		record.Cases[0].Authority = ledger.Cases[0].Authority
		record.Cases[0].ItemID = ledger.Cases[0].ItemID
		record.Cases[0].CaseID = fillercorpus.CaseID(ledger.Cases[0].Authority, ledger.Cases[0].ItemID)
		record.Captures[0].PredictedMediaBytes -= record.Cases[0].Representation.Bytes
		record.Captures[0].MaxPredictedMediaBytes = record.Captures[0].PredictedMediaBytes
		capture := record.Captures[0]
		capture.CaptureID = fillercorpus.NewCaptureID(ledger.Cases[0].Authority, "", "programme")
		capture.Authority = ledger.Cases[0].Authority
		capture.PredictedMediaBytes = record.Cases[0].Representation.Bytes
		capture.MaxPredictedMediaBytes = capture.PredictedMediaBytes
		record.Captures = append(record.Captures, capture)
		record.Cases[0].CaptureIDs = []string{capture.CaptureID}
	})
	fixture.inventory = writeTemporalHumanJSON(t, t.TempDir(), "programme-inventory.json", inventory)
	_, err := BuildTemporalStructureHoldoutPlan(fixture.config(filepath.Join(t.TempDir(), "output")))
	if err == nil || !strings.Contains(err.Error(), "repeats bounded filler") {
		t.Fatalf("programme parent reference-lineage error = %v", err)
	}
}

func TestLoadTemporalStructureHoldoutProgrammeInventoryRejectsProgrammeParentMatchingUnselectedReference(t *testing.T) {
	fixture := newTemporalStructureHoldoutFixture(t)
	inventory := readStrictTestJSON[TemporalStructureHoldoutProgrammeInventory](t, fixture.inventory)
	ledger := fixture.downloadLedger(t)
	if len(ledger.Cases) != 300 {
		t.Fatalf("ledger cases = %d, want 300", len(ledger.Cases))
	}
	ledger.Cases[len(ledger.Cases)-1].Authority = inventory.Sources[0].Provenance.Authority
	ledger.Cases[len(ledger.Cases)-1].ItemID = inventory.Sources[0].Provenance.ItemID
	_, _, err := loadTemporalStructureHoldoutProgrammeInventory(fixture.inventory, fixture.root, ledger, fixture.plannedAt)
	if err == nil || !strings.Contains(err.Error(), "repeats bounded filler") {
		t.Fatalf("unselected reference-lineage error = %v", err)
	}
}

func mutateTemporalStructureProgrammeRecord(t *testing.T, fixture temporalStructureHoldoutFixture, inventory *TemporalStructureHoldoutProgrammeInventory, mutate func(*fillercorpus.Inventory)) {
	t.Helper()
	recordPath := filepath.Join(fixture.root, filepath.FromSlash(inventory.Sources[0].Provenance.SourceRecordPath))
	raw, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatal(err)
	}
	record, err := fillercorpus.DecodeInventoryBytes(raw)
	if err != nil {
		t.Fatal(err)
	}
	mutate(&record)
	writeTemporalHumanJSON(t, filepath.Dir(recordPath), filepath.Base(recordPath), record)
	raw, err = os.ReadFile(recordPath)
	if err != nil {
		t.Fatal(err)
	}
	for index := range inventory.Sources {
		inventory.Sources[index].Provenance.SourceRecordSHA256 = hashBytes(raw)
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

func TestTemporalStructureHoldoutReceiptRejectsProgrammeSpotTampering(t *testing.T) {
	fixture := newTemporalStructureHoldoutFixture(t)
	output := filepath.Join(t.TempDir(), "output")
	if _, err := BuildTemporalStructureHoldoutPlan(fixture.config(output)); err != nil {
		t.Fatal(err)
	}
	authoringPath := filepath.Join(output, "authoring.json")
	receiptPath := filepath.Join(output, "receipt.json")
	transition := readStrictTestJSON[TemporalTransitionAuthority](t, fixture.transition)

	t.Run("repeated filler", func(t *testing.T) {
		authoring := readStrictTestJSON[TemporalStructureChallengeAuthoring](t, authoringPath)
		receipt := readStrictTestJSON[TemporalStructureHoldoutReceipt](t, receiptPath)
		first := receipt.ProgrammeSpotConstructions[0]
		second := &receipt.ProgrammeSpotConstructions[1]
		second.FillerSourceID = first.FillerSourceID
		second.FillerDurationMS = first.FillerDurationMS
		second.FillerRole = first.FillerRole
		for caseIndex := range authoring.Cases {
			if authoring.Cases[caseIndex].ID == second.CaseID {
				authoring.Cases[caseIndex].Segments[1].SourceID = first.FillerSourceID
				authoring.Cases[caseIndex].Segments[1].DurationMS = first.FillerDurationMS
				break
			}
		}
		if err := validateTemporalStructureHoldoutReceipt(receipt, authoring, &transition); err == nil || !strings.Contains(err.Error(), "coverage is incomplete") {
			t.Fatalf("repeated filler error = %v", err)
		}
	})

	t.Run("invalid insertion bounds", func(t *testing.T) {
		authoring := readStrictTestJSON[TemporalStructureChallengeAuthoring](t, authoringPath)
		receipt := readStrictTestJSON[TemporalStructureHoldoutReceipt](t, receiptPath)
		item := &receipt.ProgrammeSpotConstructions[0]
		item.BeforeSourceStartMS++
		for caseIndex := range authoring.Cases {
			if authoring.Cases[caseIndex].ID == item.CaseID {
				authoring.Cases[caseIndex].Segments[0].StartMS = item.BeforeSourceStartMS
				break
			}
		}
		if err := validateTemporalStructureHoldoutReceipt(receipt, authoring, &transition); err == nil || !strings.Contains(err.Error(), "inserted-spot pattern drift") {
			t.Fatalf("insertion bounds error = %v", err)
		}
	})

	t.Run("missing programme spot", func(t *testing.T) {
		authoring := readStrictTestJSON[TemporalStructureChallengeAuthoring](t, authoringPath)
		receipt := readStrictTestJSON[TemporalStructureHoldoutReceipt](t, receiptPath)
		receipt.ProgrammeSpotConstructions = receipt.ProgrammeSpotConstructions[:len(receipt.ProgrammeSpotConstructions)-1]
		if err := validateTemporalStructureHoldoutReceipt(receipt, authoring, &transition); err == nil || !strings.Contains(err.Error(), "counts or disposition") {
			t.Fatalf("missing programme spot error = %v", err)
		}
	})

	t.Run("multi-item repeated source", func(t *testing.T) {
		authoring := readStrictTestJSON[TemporalStructureChallengeAuthoring](t, authoringPath)
		receipt := readStrictTestJSON[TemporalStructureHoldoutReceipt](t, receiptPath)
		item := &receipt.MultiCompilationConstructions[0]
		item.SourceIDs[1] = item.SourceIDs[0]
		item.Roles[1] = item.Roles[0]
		var firstDuration int64
		for _, anchor := range receipt.SelectedAnchors {
			if anchor.SourceID == item.SourceIDs[0] {
				firstDuration = anchor.DurationMS
				break
			}
		}
		for caseIndex := range authoring.Cases {
			if authoring.Cases[caseIndex].ID == item.CaseID {
				authoring.Cases[caseIndex].Segments[1] = TemporalStructureChallengeSegment{SourceID: item.SourceIDs[0], DurationMS: firstDuration}
				break
			}
		}
		if err := validateTemporalStructureHoldoutReceipt(receipt, authoring, &transition); err == nil || !strings.Contains(err.Error(), "repeats a source") {
			t.Fatalf("multi-item repeated source error = %v", err)
		}
	})

	t.Run("multi-item trait drift", func(t *testing.T) {
		authoring := readStrictTestJSON[TemporalStructureChallengeAuthoring](t, authoringPath)
		receipt := readStrictTestJSON[TemporalStructureHoldoutReceipt](t, receiptPath)
		receipt.MultiCompilationConstructions[0].Trait = temporalStructureMultiMixedRoleJoins
		if err := validateTemporalStructureHoldoutReceipt(receipt, authoring, &transition); err == nil || !strings.Contains(err.Error(), "invalid multi-item compilation") {
			t.Fatalf("multi-item trait error = %v", err)
		}
	})
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
