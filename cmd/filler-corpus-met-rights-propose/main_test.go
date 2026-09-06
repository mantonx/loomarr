package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillercorpus"
)

func TestRunPublishesPrivateNonAuthorizingReportAndRefusesOverwrite(t *testing.T) {
	snapshot := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	metadata := []byte(`{"objectID":195733,"isPublicDomain":true,"rightsAndReproduction":"","primaryImage":"https://images.metmuseum.org/valid.jpg","title":"Valid work","artistDisplayName":"Valid Creator","objectDate":"1900","objectURL":"https://www.metmuseum.org/art/collection/search/195733","repository":"Metropolitan Museum of Art, New York, NY","creditLine":"Gift, 1900","tags":[{"term":"Female Nudes"}]}`)
	metadataURL := "https://collectionapi.metmuseum.org/public/collection/v1/objects/195733"
	metadataDigest := sha256.Sum256(metadata)
	cacheDigest := sha256.Sum256([]byte(metadataURL))
	cacheName := hex.EncodeToString(cacheDigest[:]) + ".json"
	collection := "selection-sha256:" + strings.Repeat("a", 64)
	role := "policy-positive-nomination"
	captureID := fillercorpus.NewCaptureID(fillercorpus.MetAuthority, collection, role)
	inventory := fillercorpus.Inventory{
		SchemaVersion: fillercorpus.InventorySchemaVersion, SnapshotAt: snapshot,
		Captures: []fillercorpus.Capture{{CaptureID: captureID, Transport: fillercorpus.TransportHTTPS, Authority: fillercorpus.MetAuthority, Collection: collection, RoleHint: role, SnapshotAt: snapshot, MaxRequests: 3, RequestsUsed: 3, MaxResponseBytes: 10_000, ResponseBytes: int64(len(metadata)), MaxPredictedMediaBytes: 100, PredictedMediaBytes: 100, MaxWallTimeMS: 1_000, WallTimeMS: 100}},
		Cases: []fillercorpus.InventoryCase{{
			CaseID: fillercorpus.CaseID(fillercorpus.MetAuthority, "195733"), CaptureIDs: []string{captureID}, Authority: fillercorpus.MetAuthority, ItemID: "195733", Title: "Valid work", RoleHints: []string{role},
			Collection: []string{"Metropolitan Museum of Art", "search-term:venus"}, Creator: []string{"Valid Creator"}, SubjectTerms: []string{"Female Nudes"}, SourceFamily: "met-object:195733", Date: "1900",
			RightsAssertions: []string{"Met object record isPublicDomain=true.", "Met repository assertion: Metropolitan Museum of Art, New York, NY", "Met credit-line assertion: Gift, 1900"},
			ItemURL:          "https://www.metmuseum.org/art/collection/search/195733", MetadataURL: metadataURL, MetadataCache: cacheName, MetadataRetrievedAt: snapshot.Add(-time.Minute), MetadataSHA256: hex.EncodeToString(metadataDigest[:]), AllowedMediaHosts: []string{"images.metmuseum.org"},
			Representation: fillercorpus.InventoryRepresentation{Transport: fillercorpus.TransportHTTPS, Name: "valid.jpg", URL: "https://images.metmuseum.org/valid.jpg?loomarr=" + hex.EncodeToString(metadataDigest[:]), MIMEType: "image/jpeg", Bytes: 100},
		}},
	}
	policy := fillercorpus.MetOpenAccessPolicyEvidence{
		SchemaVersion: fillercorpus.MetOpenAccessPolicyEvidenceSchemaVersion, EvidenceID: "met-open-access-metadata-prescreen-v1", CapturedAt: snapshot,
		Sources: []fillercorpus.MetOpenAccessPolicySource{
			{Kind: "api_documentation", URL: "https://metmuseum.github.io/", SHA256: "037f875cd22180ecb31a67cb38707ce2ea88eb7087c2f81edd27a0a1aa56dd6a"},
			{Kind: "openaccess_license", URL: "https://raw.githubusercontent.com/metmuseum/openaccess/6fa206f0df6cf349d4fe558028d4c08e95f44eb6/LICENSE", SHA256: "36ffd9dc085d529a7e60e1276d73ae5a030b020313e6c5408593a6ae2af39673", Commit: "6fa206f0df6cf349d4fe558028d4c08e95f44eb6"},
			{Kind: "openaccess_readme", URL: "https://raw.githubusercontent.com/metmuseum/openaccess/6fa206f0df6cf349d4fe558028d4c08e95f44eb6/README.md", SHA256: "26f24c669b3eb888a02498113dc94feb2674ee9d007a1d470c13be36413a29c2", Commit: "6fa206f0df6cf349d4fe558028d4c08e95f44eb6"},
		},
		Limitations: []string{"cc0_does_not_resolve_non_copyright_rights", "dataset_cc0_does_not_license_images", "metadata_prescreen_is_not_rights_approval", "source_policy_pages_require_independent_review"},
	}
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	cacheRoot := filepath.Join(root, "cache")
	if err := os.Mkdir(cacheRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	paths := map[string]any{"inventory.json": inventory, "policy.json": policy}
	for name, value := range paths {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(cacheRoot, cacheName), metadata, 0o600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "report.json")
	args := []string{"--inventory", filepath.Join(root, "inventory.json"), "--metadata-cache", cacheRoot, "--policy-evidence", filepath.Join(root, "policy.json"), "--out", output, "--prepared-at", snapshot.Add(time.Hour).Format(time.RFC3339), "--min-items", "1", "--max-items", "1"}
	var stdout, stderr bytes.Buffer
	if code := run(args, &stdout, &stderr); code != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	var report fillercorpus.MetRightsPrescreen
	raw, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(output)
	if err != nil {
		t.Fatal(err)
	}
	if report.PassedCases != 1 || report.HeldCases != 0 || report.RightsApproval || report.DownloadAuthority || report.IngestionAuthority || report.SchedulingAuthority || report.BroadcastAuthority || info.Mode().Perm() != 0o600 || !strings.Contains(stdout.String(), "rightsApproval=false downloadAuthority=false") {
		t.Fatalf("report=%+v mode=%o stdout=%q", report, info.Mode().Perm(), stdout.String())
	}
	if code := run(args, ioDiscard{}, ioDiscard{}); code == 0 {
		t.Fatal("existing report was overwritten")
	}
}

type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) { return len(p), nil }
