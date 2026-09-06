package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	"github.com/loomarr/loomarr/internal/fillervisualsafety"
)

func TestRightsApprovedMetImageFlowsIntoVisualNominationWorksheet(t *testing.T) {
	t.Parallel()
	snapshot := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	imageRaw := testNominationJPEG(t, 4, 3)
	collection := "selection-sha256:" + strings.Repeat("1", 64)
	role := "policy-positive-nomination"
	captureID := fillercorpus.NewCaptureID(fillercorpus.MetAuthority, collection, role)
	item := fillercorpus.InventoryCase{
		CaseID: fillercorpus.CaseID(fillercorpus.MetAuthority, "195733"), CaptureIDs: []string{captureID},
		Authority: fillercorpus.MetAuthority, ItemID: "195733", Title: "Venus", RoleHints: []string{role},
		Collection: []string{"Metropolitan Museum of Art", "search-term:venus"}, Creator: []string{"Example Artist"},
		SubjectTerms: []string{"Female Nudes"}, SourceFamily: "met-object:195733", Date: "1900",
		RightsAssertions: []string{"Met object record isPublicDomain=true."},
		ItemURL:          "https://www.metmuseum.org/art/collection/search/195733", MetadataURL: "https://collectionapi.metmuseum.org/public/collection/v1/objects/195733",
		MetadataCache: "objects/example.json", MetadataRetrievedAt: snapshot.Add(-time.Minute), MetadataSHA256: strings.Repeat("a", 64),
		AllowedMediaHosts: []string{"images.metmuseum.org"},
		Representation:    fillercorpus.InventoryRepresentation{Transport: fillercorpus.TransportHTTPS, Name: "image.jpg", URL: "https://images.metmuseum.org/image.jpg", MIMEType: "image/jpeg", Bytes: int64(len(imageRaw))},
	}
	inventory := fillercorpus.Inventory{
		SchemaVersion: fillercorpus.InventorySchemaVersion, SnapshotAt: snapshot,
		Captures: []fillercorpus.Capture{{
			CaptureID: captureID, Transport: fillercorpus.TransportHTTPS, Authority: fillercorpus.MetAuthority, Collection: collection, RoleHint: role,
			SnapshotAt: snapshot, MaxRequests: 3, RequestsUsed: 3, MaxResponseBytes: 4096, ResponseBytes: 100,
			MaxPredictedMediaBytes: int64(len(imageRaw)), PredictedMediaBytes: int64(len(imageRaw)), MaxWallTimeMS: 1000, WallTimeMS: 10,
		}},
		Cases: []fillercorpus.InventoryCase{item},
	}
	inventoryRaw, err := json.Marshal(inventory)
	if err != nil {
		t.Fatal(err)
	}
	inventorySHA256 := testNominationDigest(inventoryRaw)
	approval := fillercorpus.RightsDecision{
		InventorySHA256: inventorySHA256, CaseID: item.CaseID, CaptureIDs: item.CaptureIDs,
		Authority: item.Authority, ItemID: item.ItemID, MetadataSHA256: item.MetadataSHA256,
		ReviewerID: "maintainer", ReviewedAt: snapshot.Add(time.Minute), Decision: "approved",
		Basis:           fillervisualsafety.VisualCorpusMetCC0ApprovalBasisPrefix + "exact batch attestation and item evidence reviewed.",
		Redistributable: true, Restrictions: []string{},
	}
	mediaRoot := filepath.Join(t.TempDir(), "media")
	opts := options{
		profile: fillercorpus.RightsProfileDevelopment, inventorySHA256: inventorySHA256,
		generatedAt: snapshot.Add(2 * time.Minute), maxRequests: 1, maxItems: 1,
		maxBytes: int64(len(imageRaw)), maxImagePixels: fillercorpus.MaximumMaterializedImagePixels,
		outputDir: mediaRoot,
	}
	plan, err := planDownloads(inventory, []fillercorpus.RightsDecision{approval}, opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := ensurePrivateDirectory(mediaRoot); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plan[0].path, imageRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	ledger, err := executeDownloads(context.Background(), nil, plan, opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := fillercorpus.ValidateMaterializationLedger(ledger, inventory, inventorySHA256); err != nil {
		t.Fatal(err)
	}
	ledgerRaw, err := json.Marshal(ledger)
	if err != nil {
		t.Fatal(err)
	}
	worksheet, err := fillervisualsafety.PrepareVisualCorpusNominationWorksheet(context.Background(), fillervisualsafety.VisualCorpusNominationPrepareConfig{
		InventoryJSON: inventoryRaw, MaterializationJSON: ledgerRaw, MediaRoot: mediaRoot,
		PreparedAt: snapshot.Add(3 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(worksheet.Cases) != 1 {
		t.Fatalf("worksheet cases = %d", len(worksheet.Cases))
	}
	row := worksheet.Cases[0]
	if row.CaseID != item.CaseID || row.SourceWorkID != item.SourceFamily || row.SourceFamilyID != item.SourceFamily ||
		row.Creator[0] != item.Creator[0] || row.RightsReviewerID != approval.ReviewerID ||
		row.RightsReviewBasis != approval.Basis || row.Asset.SHA256 != testNominationDigest(imageRaw) ||
		row.Asset.Bytes != int64(len(imageRaw)) || row.Width != 4 || row.Height != 3 ||
		worksheet.TruthAuthorityCreated || worksheet.TrainingAllowed || worksheet.ProductionUseAllowed {
		t.Fatalf("nomination row = %+v, worksheet = %+v", row, worksheet)
	}
}

func testNominationJPEG(t *testing.T, width, height int) []byte {
	t.Helper()
	value := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			value.Set(x, y, color.RGBA{R: uint8(40 + x*10), G: uint8(50 + y*10), B: 100, A: 255})
		}
	}
	var raw bytes.Buffer
	if err := jpeg.Encode(&raw, value, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	return raw.Bytes()
}

func testNominationDigest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
