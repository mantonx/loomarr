package main

import (
	"bytes"
	"context"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillercorpus"
)

func TestExecuteDownloadsPublishesSharedProvenanceCompleteLedger(t *testing.T) {
	t.Parallel()
	retrieved := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	inventory := downloadableInventory(retrieved, "shared-ledger", "")
	inventory.Cases[0].Creator = []string{"Example Creator"}
	inventory.Cases[0].SubjectTerms = []string{"Advertising"}
	inventory.Cases[0].Campaign = "Example Campaign"
	inventory.Cases[0].SourceFamily = "example-family"
	inventorySHA256 := strings.Repeat("f", 64)
	approval := approvalFor(inventory, retrieved)
	options := options{
		profile: fillercorpus.RightsProfileDevelopment, inventorySHA256: inventorySHA256,
		generatedAt: retrieved.Add(2 * time.Minute), maxRequests: 1, maxItems: 1, maxBytes: 1024,
		maxImagePixels: fillercorpus.MaximumMaterializedImagePixels, outputDir: t.TempDir(), delay: 500 * time.Millisecond,
	}
	plan, err := planDownloads(inventory, []fillercorpus.RightsDecision{approval}, options)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plan[0].path, bytes.Repeat([]byte{0x42}, 1024), 0o600); err != nil {
		t.Fatal(err)
	}
	ledger, err := executeDownloads(context.Background(), nil, plan, options)
	if err != nil {
		t.Fatal(err)
	}
	if err := fillercorpus.ValidateMaterializationLedger(ledger, inventory, inventorySHA256); err != nil {
		t.Fatal(err)
	}
	item := ledger.Cases[0]
	if ledger.SchemaVersion != fillercorpus.MaterializationLedgerSchemaVersion || item.Creator[0] != "Example Creator" ||
		item.SourceFamily != "example-family" || item.CaptureIDs[0] != inventory.Cases[0].CaptureIDs[0] {
		t.Fatalf("ledger = %+v", ledger)
	}
}

func downloadableInventory(retrieved time.Time, id, license string) fillercorpus.Inventory {
	authority := "loc.gov/national-screening-room"
	captureID := fillercorpus.NewCaptureID(authority, "", "commercial")
	return fillercorpus.Inventory{SchemaVersion: fillercorpus.InventorySchemaVersion, SnapshotAt: retrieved, Captures: []fillercorpus.Capture{{CaptureID: captureID, Transport: fillercorpus.TransportHTTPS, Authority: authority, RoleHint: "commercial", SnapshotAt: retrieved, MaxRequests: 2, RequestsUsed: 1, MaxResponseBytes: 2048, ResponseBytes: 10, MaxPredictedMediaBytes: 2048, PredictedMediaBytes: 1024, MaxWallTimeMS: 1000, WallTimeMS: 10}}, Cases: []fillercorpus.InventoryCase{{CaseID: fillercorpus.CaseID(authority, id), CaptureIDs: []string{captureID}, Authority: authority, ItemID: id, Title: "Clip", RoleHints: []string{"commercial"}, LicenseURL: license, RightsAssertions: []string{"review required"}, ItemURL: "https://www.loc.gov/item/" + id, MetadataURL: "https://www.loc.gov/item/" + id + "/?fo=json", MetadataSHA256: strings.Repeat("a", 64), MetadataRetrievedAt: retrieved, AllowedMediaHosts: []string{"tile.loc.gov"}, Representation: fillercorpus.InventoryRepresentation{Transport: fillercorpus.TransportHTTPS, Name: id + ".mp4", URL: "https://tile.loc.gov/" + id + ".mp4?download=1", MIMEType: "video/mp4", Bytes: 1024}}}}
}

func approvalFor(inv fillercorpus.Inventory, retrieved time.Time) fillercorpus.RightsDecision {
	c := inv.Cases[0]
	return fillercorpus.RightsDecision{InventorySHA256: strings.Repeat("f", 64), CaseID: c.CaseID, CaptureIDs: c.CaptureIDs, Authority: c.Authority, ItemID: c.ItemID, MetadataSHA256: c.MetadataSHA256, ReviewerID: "rights-reviewer", ReviewedAt: retrieved.Add(time.Minute), Decision: "approved", Basis: "item license and source reviewed", Redistributable: true}
}

func TestPlanDownloadsSkipsRightsApprovedLocalMedia(t *testing.T) {
	retrieved := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	inv := downloadableInventory(retrieved, "remote", "")
	local := inv.Cases[0]
	local.ItemID = "local"
	local.CaseID = fillercorpus.CaseID(local.Authority, local.ItemID)
	local.CaptureIDs = []string{fillercorpus.NewCaptureID(local.Authority, "direct", "commercial")}
	local.Representation = fillercorpus.InventoryRepresentation{Transport: fillercorpus.TransportLocal, Name: "local.mp4", Path: "media/local.mp4", MIMEType: "video/mp4", Bytes: 10, SHA256: strings.Repeat("b", 64)}
	local.AllowedMediaHosts = nil
	local.ItemURL = ""
	local.MetadataURL = ""
	local.Evidence = []fillercorpus.InventoryEvidence{{Kind: "rights", Path: "evidence/rights.txt", Bytes: 1, SHA256: strings.Repeat("c", 64)}, {Kind: "provenance", Path: "evidence/provenance.txt", Bytes: 1, SHA256: strings.Repeat("d", 64)}}
	inv.Captures = append(inv.Captures, fillercorpus.Capture{CaptureID: local.CaptureIDs[0], Transport: fillercorpus.TransportLocal, Authority: local.Authority, Collection: "direct", RoleHint: "commercial", SnapshotAt: retrieved, MaxPredictedMediaBytes: 10, PredictedMediaBytes: 10, MaxWallTimeMS: 1000})
	inv.Cases = append(inv.Cases, local)
	remoteApproval := approvalFor(inv, retrieved)
	localApproval := remoteApproval
	localApproval.CaseID, localApproval.CaptureIDs, localApproval.ItemID = local.CaseID, local.CaptureIDs, local.ItemID
	opts := options{profile: fillercorpus.RightsProfileDevelopment, inventorySHA256: strings.Repeat("f", 64), generatedAt: retrieved.Add(2 * time.Minute), maxItems: 2, maxBytes: 4096, outputDir: t.TempDir()}
	plan, err := planDownloads(inv, []fillercorpus.RightsDecision{remoteApproval, localApproval}, opts)
	if err != nil || len(plan) != 1 || plan[0].candidate.ItemID != "remote" {
		t.Fatalf("plan = %+v, %v", plan, err)
	}
}

func planOptions(retrieved time.Time) options {
	return options{profile: fillercorpus.RightsProfileDevelopment, outputDir: "/tmp/corpus", inventorySHA256: strings.Repeat("f", 64), generatedAt: retrieved.Add(2 * time.Minute), maxItems: 1, maxBytes: 1024}
}

func TestPlanDownloadsRequiresMetadataBoundRightsReview(t *testing.T) {
	retrieved := time.Date(2026, 8, 25, 8, 0, 0, 0, time.UTC)
	inv := downloadableInventory(retrieved, "soda-ad", "https://creativecommons.org/publicdomain/mark/1.0/")
	approval, opts := approvalFor(inv, retrieved), planOptions(retrieved)
	if plan, err := planDownloads(inv, []fillercorpus.RightsDecision{approval}, opts); err != nil || len(plan) != 1 {
		t.Fatalf("plan = %v, %v", plan, err)
	}
	approval.MetadataSHA256 = strings.Repeat("b", 64)
	if _, err := planDownloads(inv, []fillercorpus.RightsDecision{approval}, opts); err == nil {
		t.Fatal("stale review accepted")
	}
	approval = approvalFor(inv, retrieved)
	approval.InventorySHA256 = strings.Repeat("e", 64)
	if _, err := planDownloads(inv, []fillercorpus.RightsDecision{approval}, opts); err == nil {
		t.Fatal("foreign inventory accepted")
	}
}

func TestPlanDownloadsRequiresAttributionAndRedistribution(t *testing.T) {
	retrieved := time.Date(2026, 8, 25, 8, 0, 0, 0, time.UTC)
	inv := downloadableInventory(retrieved, "by-ad", "https://creativecommons.org/licenses/by/4.0/")
	approval, opts := approvalFor(inv, retrieved), planOptions(retrieved)
	if _, err := planDownloads(inv, []fillercorpus.RightsDecision{approval}, opts); err == nil {
		t.Fatal("attribution-free approval accepted")
	}
	approval.RequiredCredit = "Creator, CC BY 4.0"
	approval.Redistributable = false
	if _, err := planDownloads(inv, []fillercorpus.RightsDecision{approval}, opts); err == nil {
		t.Fatal("non-redistributable approval accepted")
	}
}

func TestPlanDownloadsRejectsUnallowlistedMediaHost(t *testing.T) {
	retrieved := time.Date(2026, 8, 25, 8, 0, 0, 0, time.UTC)
	inv := downloadableInventory(retrieved, "clip", "https://creativecommons.org/publicdomain/mark/1.0/")
	inv.Cases[0].Representation.URL = "https://example.com/clip.mp4"
	if _, err := planDownloads(inv, nil, planOptions(retrieved)); err == nil {
		t.Fatal("unallowlisted host accepted")
	}
}

func TestPlanDownloadsAcceptsRightsApprovedMetImageWithCanonicalExtension(t *testing.T) {
	retrieved := time.Date(2026, 9, 4, 8, 0, 0, 0, time.UTC)
	authority := fillercorpus.MetAuthority
	captureID := fillercorpus.NewCaptureID(authority, "terms-sha256:"+strings.Repeat("a", 64), "policy-positive-nomination")
	inv := fillercorpus.Inventory{SchemaVersion: fillercorpus.InventorySchemaVersion, SnapshotAt: retrieved, Captures: []fillercorpus.Capture{{
		CaptureID: captureID, Transport: fillercorpus.TransportHTTPS, Authority: authority, Collection: "terms-sha256:" + strings.Repeat("a", 64), RoleHint: "policy-positive-nomination", SnapshotAt: retrieved,
		MaxRequests: 3, RequestsUsed: 3, MaxResponseBytes: 4096, ResponseBytes: 100, MaxPredictedMediaBytes: 4096, PredictedMediaBytes: 1024, MaxWallTimeMS: 1000, WallTimeMS: 10,
	}}, Cases: []fillercorpus.InventoryCase{{
		CaseID: fillercorpus.CaseID(authority, "195733"), CaptureIDs: []string{captureID}, Authority: authority, ItemID: "195733", Title: "Venus",
		RoleHints: []string{"policy-positive-nomination"}, Creator: []string{"Artist"}, SubjectTerms: []string{"Female Nudes"}, SourceFamily: "met-object:195733",
		RightsAssertions: []string{"Met object record isPublicDomain=true."}, ItemURL: "https://www.metmuseum.org/art/collection/search/195733",
		MetadataURL: "https://collectionapi.metmuseum.org/public/collection/v1/objects/195733", MetadataRetrievedAt: retrieved, MetadataSHA256: strings.Repeat("b", 64),
		AllowedMediaHosts: []string{"images.metmuseum.org"}, Representation: fillercorpus.InventoryRepresentation{Transport: fillercorpus.TransportHTTPS, Name: "misleading.bin", URL: "https://images.metmuseum.org/object.jpg", MIMEType: "image/jpeg", Bytes: 1024},
	}}}
	approval := approvalFor(inv, retrieved)
	opts := planOptions(retrieved)
	plan, err := planDownloads(inv, []fillercorpus.RightsDecision{approval}, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 1 || !strings.HasSuffix(plan[0].path, ".jpg") {
		t.Fatalf("plan = %+v", plan)
	}
}

func TestPlanDownloadsCertificationRejectsLegacyAndProcessorDrift(t *testing.T) {
	retrieved := time.Date(2026, 9, 2, 8, 0, 0, 0, time.UTC)
	inv := downloadableInventory(retrieved, "holdout", "")
	approval := approvalFor(inv, retrieved)
	approval.Redistributable = false
	approval.HoldoutContract = downloadHoldoutContract()
	opts := planOptions(retrieved)
	opts.profile = fillercorpus.RightsProfileCertification
	opts.processorID = approval.HoldoutContract.ProcessorID
	opts.processorTermsSHA256 = approval.HoldoutContract.ProcessorTermsSHA256
	if plan, err := planDownloads(inv, []fillercorpus.RightsDecision{approval}, opts); err != nil || len(plan) != 1 {
		t.Fatalf("certification plan = %v, %v", plan, err)
	}
	legacy := approval
	legacy.HoldoutContract = nil
	if _, err := planDownloads(inv, []fillercorpus.RightsDecision{legacy}, opts); err == nil {
		t.Fatal("schema-v3 approval authorized certification acquisition")
	}
	drifted := opts
	drifted.processorTermsSHA256 = strings.Repeat("f", 64)
	if _, err := planDownloads(inv, []fillercorpus.RightsDecision{approval}, drifted); err == nil {
		t.Fatal("changed processor terms authorized certification acquisition")
	}
}

func downloadHoldoutContract() *fillercorpus.HoldoutRightsContract {
	return &fillercorpus.HoldoutRightsContract{
		SchemaVersion: fillercorpus.HoldoutRightsContractSchemaVersion,
		AgreementID:   "agreement-v1", AgreementSHA256: strings.Repeat("a", 64), ScheduleID: "schedule-v1", ScheduleSHA256: strings.Repeat("b", 64),
		SignerAuthorityStatus: fillercorpus.RightsStatusCleared, SignerAuthorityEvidenceSHA256: strings.Repeat("c", 64), ProcessorID: "openrouter/vertex", ProcessorTermsSHA256: strings.Repeat("d", 64),
		Grants:                       fillercorpus.HoldoutRightsGrants{CommercialEvaluation: true, CopyAndStorage: true, TechnicalModification: true, EvidenceExtraction: true, ProviderTransfer: true},
		EmbeddedRights:               fillercorpus.EmbeddedRightsStatus{Music: fillercorpus.RightsStatusNotPresent, PerformersAndVoices: fillercorpus.RightsStatusCleared, StockAndArtwork: fillercorpus.RightsStatusNotPresent, Trademarks: fillercorpus.RightsStatusCleared, PrivacyAndPublicity: fillercorpus.RightsStatusCleared, Locations: fillercorpus.RightsStatusNotPresent},
		EmbeddedRightsEvidenceSHA256: strings.Repeat("e", 64), RedistributionScope: fillercorpus.RedistributionExternalOnly, Territory: fillercorpus.RightsTerritoryWorldwide, Term: fillercorpus.RightsTermPerpetualIrrevocable, Withdrawal: fillercorpus.RightsWithdrawalDefectRetirement,
	}
}

func TestRedirectPolicyRejectsBeforeFollowingUnallowlistedHost(t *testing.T) {
	policy := redirectPolicy([]string{"archive.org", ".archive.org"})
	allowed, _ := url.Parse("https://ia801.example.archive.org/file.mp4")
	if err := policy(&http.Request{URL: allowed}, nil); err != nil {
		t.Fatalf("allowed redirect: %v", err)
	}
	outside, _ := url.Parse("https://attacker.invalid/file.mp4")
	if err := policy(&http.Request{URL: outside}, nil); err == nil {
		t.Fatal("outside redirect accepted")
	}
	credentialed, _ := url.Parse("https://user:secret@archive.org/file.mp4")
	if err := policy(&http.Request{URL: credentialed}, nil); err == nil {
		t.Fatal("credentialed redirect accepted")
	}
}
