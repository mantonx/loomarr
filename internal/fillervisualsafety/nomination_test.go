package fillervisualsafety

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillercorpus"
)

func TestVisualCorpusNominationWorkflowPublishesDraftCompatiblePrivateInputs(t *testing.T) {
	t.Parallel()
	fixture := newVisualNominationFixture(t)
	worksheet, err := PrepareVisualCorpusNominationWorksheet(context.Background(), fixture.prepare)
	if err != nil {
		t.Fatal(err)
	}
	if worksheet.CandidateModelOutput || worksheet.TruthAuthorityCreated || worksheet.TrainingAllowed || worksheet.ProductionUseAllowed || len(worksheet.Cases) != 1 {
		t.Fatalf("worksheet grants authority or has wrong cases: %+v", worksheet)
	}
	records := completedNominationRecords(worksheet, VisualCorpusNominationPositive, VisualCorpusSubjectHistoricalAdult)
	result, err := LockVisualCorpusNominations(context.Background(), VisualCorpusNominationLockConfig{
		Prepare: fixture.prepare, Worksheet: worksheet, CompletedCSV: records,
		ReviewedBy: "visual-reviewer", ReviewedAt: fixture.reviewedAt, OutputDir: fixture.output,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ReviewedCount != 1 || result.CandidateCount != 1 || result.ExcludedCount != 0 || !validDigest(result.SetSHA256) {
		t.Fatalf("result = %+v", result)
	}
	set, err := OpenVisualCorpusNominationSet(fixture.output)
	if err != nil {
		t.Fatal(err)
	}
	if set.SHA256 != result.SetSHA256 || set.TruthAuthorityCreated || set.CandidateModelOutput || set.TrainingAllowed || set.ProductionUseAllowed {
		t.Fatalf("set = %+v", set)
	}
	if set.ReviewedCaseCount != 1 || set.ExcludedCaseCount != 0 || set.ReviewDecisionsSHA256 != digestJSON(records) {
		t.Fatalf("set review binding = %+v", set)
	}
	candidate := set.Candidates[0]
	if candidate.CandidateID != worksheet.Cases[0].CaseID || candidate.Nomination != VisualCorpusNominationPositive ||
		candidate.SubjectStatus != VisualCorpusSubjectHistoricalAdult || candidate.GeneratedStatus != VisualCorpusGeneratedNo ||
		validateVisualCorpusDraftCandidate(candidate) != nil {
		t.Fatalf("candidate = %+v", candidate)
	}
	for _, relative := range []string{candidate.AssetRelativePath, candidate.RightsRelativePath, visualCorpusNominationSetFilename} {
		info, statErr := os.Lstat(filepath.Join(fixture.output, filepath.FromSlash(relative)))
		if statErr != nil || info.Mode().Perm() != 0o600 || !info.Mode().IsRegular() {
			t.Fatalf("private output %s = %v, %v", relative, info, statErr)
		}
	}
}

func TestVisualCorpusNominationWorkflowRejectsImmutableEditsAndContradictions(t *testing.T) {
	t.Parallel()
	t.Run("immutable", func(t *testing.T) {
		fixture := newVisualNominationFixture(t)
		worksheet, err := PrepareVisualCorpusNominationWorksheet(context.Background(), fixture.prepare)
		if err != nil {
			t.Fatal(err)
		}
		records := completedNominationRecords(worksheet, VisualCorpusNominationPositive, VisualCorpusSubjectHistoricalAdult)
		records[1][3] = "changed-case"
		_, err = LockVisualCorpusNominations(context.Background(), VisualCorpusNominationLockConfig{
			Prepare: fixture.prepare, Worksheet: worksheet, CompletedCSV: records,
			ReviewedBy: "visual-reviewer", ReviewedAt: fixture.reviewedAt, OutputDir: fixture.output,
		})
		if err == nil {
			t.Fatal("LockVisualCorpusNominations accepted an immutable CSV edit")
		}
	})
	t.Run("contradiction", func(t *testing.T) {
		fixture := newVisualNominationFixture(t)
		worksheet, err := PrepareVisualCorpusNominationWorksheet(context.Background(), fixture.prepare)
		if err != nil {
			t.Fatal(err)
		}
		records := completedNominationRecords(worksheet, VisualCorpusNominationPositive, VisualCorpusSubjectNoRiskFound)
		_, err = LockVisualCorpusNominations(context.Background(), VisualCorpusNominationLockConfig{
			Prepare: fixture.prepare, Worksheet: worksheet, CompletedCSV: records,
			ReviewedBy: "visual-reviewer", ReviewedAt: fixture.reviewedAt, OutputDir: fixture.output,
		})
		if err == nil {
			t.Fatal("LockVisualCorpusNominations accepted contradictory visual judgments")
		}
	})
	t.Run("media drift", func(t *testing.T) {
		fixture := newVisualNominationFixture(t)
		worksheet, err := PrepareVisualCorpusNominationWorksheet(context.Background(), fixture.prepare)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(fixture.prepare.MediaRoot, worksheet.Cases[0].LocalFile), []byte("changed"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err = LockVisualCorpusNominations(context.Background(), VisualCorpusNominationLockConfig{
			Prepare: fixture.prepare, Worksheet: worksheet,
			CompletedCSV: completedNominationRecords(worksheet, VisualCorpusNominationPositive, VisualCorpusSubjectHistoricalAdult),
			ReviewedBy:   "visual-reviewer", ReviewedAt: fixture.reviewedAt, OutputDir: fixture.output,
		})
		if err == nil {
			t.Fatal("LockVisualCorpusNominations accepted changed media")
		}
	})
	t.Run("symlink", func(t *testing.T) {
		fixture := newVisualNominationFixture(t)
		mediaPath := filepath.Join(fixture.prepare.MediaRoot, "materialized.jpg")
		outsidePath := filepath.Join(filepath.Dir(fixture.prepare.MediaRoot), "outside.jpg")
		raw, err := os.ReadFile(mediaPath)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(outsidePath, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(mediaPath); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outsidePath, mediaPath); err != nil {
			t.Fatal(err)
		}
		if _, err := PrepareVisualCorpusNominationWorksheet(context.Background(), fixture.prepare); err == nil {
			t.Fatal("PrepareVisualCorpusNominationWorksheet accepted symlinked media")
		}
	})
	t.Run("unstructured rights basis", func(t *testing.T) {
		fixture := newVisualNominationFixture(t)
		var ledger fillercorpus.MaterializationLedger
		if err := json.Unmarshal(fixture.prepare.MaterializationJSON, &ledger); err != nil {
			t.Fatal(err)
		}
		ledger.Cases[0].Approval.Basis = "not CC0"
		var err error
		fixture.prepare.MaterializationJSON, err = json.Marshal(ledger)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := PrepareVisualCorpusNominationWorksheet(context.Background(), fixture.prepare); err == nil {
			t.Fatal("PrepareVisualCorpusNominationWorksheet accepted an unstructured rights basis")
		}
	})
	t.Run("verification after worksheet time", func(t *testing.T) {
		fixture := newVisualNominationFixture(t)
		var ledger fillercorpus.MaterializationLedger
		if err := json.Unmarshal(fixture.prepare.MaterializationJSON, &ledger); err != nil {
			t.Fatal(err)
		}
		ledger.Cases[0].VerifiedAt = fixture.prepare.PreparedAt.Add(time.Minute)
		var err error
		fixture.prepare.MaterializationJSON, err = json.Marshal(ledger)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := PrepareVisualCorpusNominationWorksheet(context.Background(), fixture.prepare); err == nil {
			t.Fatal("PrepareVisualCorpusNominationWorksheet accepted a worksheet predating media verification")
		}
	})
	t.Run("source family", func(t *testing.T) {
		fixture := newVisualNominationFixture(t)
		addDuplicateFamilyNominationCase(t, &fixture)
		worksheet, err := PrepareVisualCorpusNominationWorksheet(context.Background(), fixture.prepare)
		if err != nil {
			t.Fatal(err)
		}
		_, err = LockVisualCorpusNominations(context.Background(), VisualCorpusNominationLockConfig{
			Prepare: fixture.prepare, Worksheet: worksheet,
			CompletedCSV: completedNominationRecords(worksheet, VisualCorpusNominationPositive, VisualCorpusSubjectHistoricalAdult),
			ReviewedBy:   "visual-reviewer", ReviewedAt: fixture.reviewedAt, OutputDir: fixture.output,
		})
		if err == nil {
			t.Fatal("LockVisualCorpusNominations accepted a repeated source family")
		}
		if _, statErr := os.Lstat(fixture.output); !os.IsNotExist(statErr) {
			t.Fatalf("failed nomination output remains: %v", statErr)
		}
	})
}

type visualNominationFixture struct {
	prepare    VisualCorpusNominationPrepareConfig
	reviewedAt time.Time
	output     string
}

func newVisualNominationFixture(t *testing.T) visualNominationFixture {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	mediaRoot := filepath.Join(root, "media")
	if err := os.Mkdir(mediaRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	imageRaw := visualNominationJPEG(t)
	localFile := "materialized.jpg"
	if err := os.WriteFile(filepath.Join(mediaRoot, localFile), imageRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	role := "visual-positive"
	collection := "selection-sha256:" + strings.Repeat("d", 64)
	captureID := fillercorpus.NewCaptureID(fillercorpus.MetAuthority, collection, role)
	caseID := fillercorpus.CaseID(fillercorpus.MetAuthority, "392067")
	representation := fillercorpus.InventoryRepresentation{
		Transport: fillercorpus.TransportHTTPS, Name: "DP-392067.jpg",
		URL: "https://images.metmuseum.org/CRDImages/ep/original/DP-392067.jpg", MIMEType: "image/jpeg", Bytes: int64(len(imageRaw)),
	}
	inventory := fillercorpus.Inventory{
		SchemaVersion: fillercorpus.InventorySchemaVersion, SnapshotAt: snapshot,
		Captures: []fillercorpus.Capture{{
			CaptureID: captureID, Transport: fillercorpus.TransportHTTPS, Authority: fillercorpus.MetAuthority,
			Collection: collection, RoleHint: role, SnapshotAt: snapshot, MaxRequests: 2, RequestsUsed: 2,
			MaxResponseBytes: 4096, ResponseBytes: 1024, MaxPredictedMediaBytes: int64(len(imageRaw)),
			PredictedMediaBytes: int64(len(imageRaw)), MaxWallTimeMS: 1000, WallTimeMS: 10,
		}},
		Cases: []fillercorpus.InventoryCase{{
			CaseID: caseID, CaptureIDs: []string{captureID}, Authority: fillercorpus.MetAuthority, ItemID: "392067",
			Title: "Woman Sitting Half-Dressed", RoleHints: []string{role}, Creator: []string{"Rembrandt"},
			SubjectTerms: []string{"Female Nudes"}, SourceFamily: "met-object:392067",
			RightsAssertions:    []string{"Met object record isPublicDomain=true."},
			ItemURL:             "https://www.metmuseum.org/art/collection/search/392067",
			MetadataURL:         "https://collectionapi.metmuseum.org/public/collection/v1/objects/392067",
			MetadataRetrievedAt: snapshot, MetadataSHA256: strings.Repeat("a", 64),
			AllowedMediaHosts: []string{"images.metmuseum.org"}, Representation: representation,
		}},
	}
	inventoryJSON, err := json.Marshal(inventory)
	if err != nil {
		t.Fatal(err)
	}
	inventorySHA256 := digestBytes(inventoryJSON)
	approval := fillercorpus.RightsDecision{
		InventorySHA256: inventorySHA256, CaseID: caseID, CaptureIDs: []string{captureID},
		Authority: fillercorpus.MetAuthority, ItemID: "392067", MetadataSHA256: strings.Repeat("a", 64),
		ReviewerID: "rights-reviewer", ReviewedAt: snapshot.Add(time.Minute), Decision: "approved",
		Basis: VisualCorpusMetCC0ApprovalBasisPrefix + "object page carried the Open Access mark", Redistributable: true,
	}
	materializedAt := snapshot.Add(2 * time.Minute)
	ledger := fillercorpus.MaterializationLedger{
		SchemaVersion: fillercorpus.MaterializationLedgerSchemaVersion, Profile: fillercorpus.RightsProfileDevelopment,
		InventorySHA256: inventorySHA256, GeneratedAt: materializedAt, MaxRequests: 1, RequestsUsed: 1,
		MaxItems: 1, MaxBytes: int64(len(imageRaw)), Bytes: int64(len(imageRaw)),
		MaxImagePixels: fillercorpus.MaximumMaterializedImagePixels,
		Cases: []fillercorpus.MaterializedCase{{
			CaseID: caseID, CaptureIDs: []string{captureID}, Authority: fillercorpus.MetAuthority, ItemID: "392067",
			RoleHints: []string{role}, Creator: []string{"Rembrandt"}, SubjectTerms: []string{"Female Nudes"},
			SourceFamily: "met-object:392067", ItemURL: inventory.Cases[0].ItemURL, MetadataURL: inventory.Cases[0].MetadataURL,
			MetadataRetrievedAt: snapshot, MetadataSHA256: strings.Repeat("a", 64), Representation: representation,
			LocalFile: localFile, ContentSHA256: digestBytes(imageRaw), VerifiedMediaType: "image/jpeg", Width: 16, Height: 12,
			Approval: approval, VerifiedAt: materializedAt,
		}},
	}
	materializationJSON, err := json.Marshal(ledger)
	if err != nil {
		t.Fatal(err)
	}
	preparedAt := materializedAt.Add(time.Minute)
	return visualNominationFixture{
		prepare: VisualCorpusNominationPrepareConfig{
			InventoryJSON: inventoryJSON, MaterializationJSON: materializationJSON,
			MediaRoot: mediaRoot, PreparedAt: preparedAt,
		},
		reviewedAt: preparedAt.Add(time.Minute), output: filepath.Join(root, "locked"),
	}
}

func completedNominationRecords(worksheet VisualCorpusNominationWorksheet, nomination, subject string) [][]string {
	records := [][]string{VisualCorpusNominationCSVHeader()}
	for _, row := range worksheet.Cases {
		record := ImmutableVisualCorpusNominationCSVRecord(worksheet, row)
		record = append(record, nomination, subject, VisualCorpusGeneratedNo, `["historical_graphics"]`)
		records = append(records, record)
	}
	return records
}

func addDuplicateFamilyNominationCase(t *testing.T, fixture *visualNominationFixture) {
	t.Helper()
	var inventory fillercorpus.Inventory
	if err := json.Unmarshal(fixture.prepare.InventoryJSON, &inventory); err != nil {
		t.Fatal(err)
	}
	var ledger fillercorpus.MaterializationLedger
	if err := json.Unmarshal(fixture.prepare.MaterializationJSON, &ledger); err != nil {
		t.Fatal(err)
	}
	secondRaw := visualNominationJPEGReversed(t, 15, 12)
	secondFile := "second.jpg"
	if err := os.WriteFile(filepath.Join(fixture.prepare.MediaRoot, secondFile), secondRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	secondInventory := inventory.Cases[0]
	secondInventory.ItemID = "492067"
	secondInventory.CaseID = fillercorpus.CaseID(fillercorpus.MetAuthority, secondInventory.ItemID)
	secondInventory.Title = "Second work"
	secondInventory.Creator = []string{"Second Creator"}
	secondInventory.ItemURL = "https://www.metmuseum.org/art/collection/search/492067"
	secondInventory.MetadataURL = "https://collectionapi.metmuseum.org/public/collection/v1/objects/492067"
	secondInventory.MetadataSHA256 = strings.Repeat("b", 64)
	secondInventory.Representation.Name = "DP-492067.jpg"
	secondInventory.Representation.URL = "https://images.metmuseum.org/CRDImages/ep/original/DP-492067.jpg"
	secondInventory.Representation.Bytes = int64(len(secondRaw))
	inventory.Cases = append(inventory.Cases, secondInventory)
	inventory.Captures[0].PredictedMediaBytes += int64(len(secondRaw))
	inventory.Captures[0].MaxPredictedMediaBytes = inventory.Captures[0].PredictedMediaBytes
	inventoryJSON, err := json.Marshal(inventory)
	if err != nil {
		t.Fatal(err)
	}
	inventorySHA256 := digestBytes(inventoryJSON)
	ledger.InventorySHA256 = inventorySHA256
	ledger.MaxItems = 2
	ledger.MaxBytes += int64(len(secondRaw))
	ledger.Bytes += int64(len(secondRaw))
	ledger.Cases[0].Approval.InventorySHA256 = inventorySHA256
	secondMaterialized := ledger.Cases[0]
	secondMaterialized.CaseID = secondInventory.CaseID
	secondMaterialized.ItemID = secondInventory.ItemID
	secondMaterialized.Creator = secondInventory.Creator
	secondMaterialized.ItemURL = secondInventory.ItemURL
	secondMaterialized.MetadataURL = secondInventory.MetadataURL
	secondMaterialized.MetadataSHA256 = secondInventory.MetadataSHA256
	secondMaterialized.Representation = secondInventory.Representation
	secondMaterialized.LocalFile = secondFile
	secondMaterialized.ContentSHA256 = digestBytes(secondRaw)
	secondMaterialized.Width = 15
	secondMaterialized.Approval.CaseID = secondInventory.CaseID
	secondMaterialized.Approval.ItemID = secondInventory.ItemID
	secondMaterialized.Approval.MetadataSHA256 = secondInventory.MetadataSHA256
	ledger.Cases = append(ledger.Cases, secondMaterialized)
	materializationJSON, err := json.Marshal(ledger)
	if err != nil {
		t.Fatal(err)
	}
	fixture.prepare.InventoryJSON = inventoryJSON
	fixture.prepare.MaterializationJSON = materializationJSON
}

func visualNominationJPEG(t *testing.T) []byte {
	t.Helper()
	return visualNominationJPEGWithSize(t, 16, 12)
}

func visualNominationJPEGWithSize(t *testing.T, width, height int) []byte {
	t.Helper()
	value := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			value.Set(x, y, color.NRGBA{R: uint8(x * 11), G: uint8(y * 17), B: uint8((x + y) * 7), A: 0xff})
		}
	}
	var output bytes.Buffer
	if err := jpeg.Encode(&output, value, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func visualNominationJPEGReversed(t *testing.T, width, height int) []byte {
	t.Helper()
	value := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			value.Set(x, y, color.NRGBA{R: uint8((width - x) * 11), G: uint8((height - y) * 17), B: uint8((x + y) * 3), A: 0xff})
		}
	}
	var output bytes.Buffer
	if err := jpeg.Encode(&output, value, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}
