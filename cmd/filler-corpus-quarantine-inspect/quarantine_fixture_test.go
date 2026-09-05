//go:build ffmpeg

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillercorpus"
	"github.com/loomarr/loomarr/internal/fillereval"
	"github.com/loomarr/loomarr/internal/fillerquarantine"
	"github.com/loomarr/loomarr/internal/fillerreview"
	"github.com/loomarr/loomarr/internal/testkit"
)

type quarantineInspectionFixture struct {
	inventoryPath, ledgerPath, mediaRoot                   string
	priorManifestPath, priorAuthorityPath, priorSourceName string
	generatedAt                                            time.Time
	media                                                  fillerquarantine.Media
}

func (fixture quarantineInspectionFixture) config(maxMediaWallTime time.Duration) fillerquarantine.Config {
	return fillerquarantine.Config{
		InventoryPath: fixture.inventoryPath, DownloadLedgerPath: fixture.ledgerPath, DownloadRoot: fixture.mediaRoot,
		PriorPublicManifestPath: fixture.priorManifestPath, PriorAuthorityPath: fixture.priorAuthorityPath, PriorSourceRoot: fixture.mediaRoot,
		ExpectedPriorCases: 1, MaxMediaWallTime: maxMediaWallTime, GeneratedAt: fixture.generatedAt, Media: fixture.media,
	}
}

func newQuarantineInspectionFixture(t *testing.T) quarantineInspectionFixture {
	t.Helper()
	root := t.TempDir()
	mediaRoot := filepath.Join(root, "media")
	if err := os.MkdirAll(mediaRoot, 0o750); err != nil {
		t.Fatal(err)
	}
	mediaFiles := testkit.FillerQuarantineMedia(t, mediaRoot)
	candidateBytes := readFixtureFile(t, mediaFiles.Candidate)
	priorBytes := readFixtureFile(t, mediaFiles.Prior)
	candidateName, priorName := filepath.Base(mediaFiles.Candidate), filepath.Base(mediaFiles.Prior)

	generatedAt := time.Date(2026, 9, 5, 16, 0, 0, 0, time.UTC)
	inventory := quarantineFixtureInventory(generatedAt.Add(-4*time.Hour), candidateName, candidateBytes)
	inventoryPath := filepath.Join(root, "inventory.json")
	inventoryRaw := writeQuarantineFixtureJSON(t, inventoryPath, inventory)
	ledger := quarantineFixtureLedger(inventory, quarantineHash(inventoryRaw), generatedAt.Add(-time.Hour), candidateName, candidateBytes)
	ledgerPath := filepath.Join(root, "ledger.json")
	writeQuarantineFixtureJSON(t, ledgerPath, ledger)

	ffmpeg, err := newExecMedia(t.Context(), "ffmpeg")
	if err != nil {
		t.Fatal(err)
	}
	manifestPath, authorityPath := writePriorChallengeFixture(t, root, generatedAt.Add(-2*time.Hour), priorName, priorBytes, ffmpeg.Identity())
	return quarantineInspectionFixture{
		inventoryPath: inventoryPath, ledgerPath: ledgerPath, mediaRoot: mediaRoot,
		priorManifestPath: manifestPath, priorAuthorityPath: authorityPath, priorSourceName: priorName,
		generatedAt: generatedAt, media: ffmpeg,
	}
}

func writePriorChallengeFixture(t *testing.T, root string, generatedAt time.Time, sourceName string, sourceBytes []byte, identity fillerreview.TemporalTruthMediaIdentity) (string, string) {
	t.Helper()
	alias := "case-0123456789abcdef01234567"
	publicRoot := filepath.Join(root, "challenge", "public")
	videoPath := filepath.Join(publicRoot, "cases", alias, "video.mp4")
	writeQuarantineFixtureFile(t, videoPath, sourceBytes)
	manifest := fillerreview.TemporalStructureChallengeManifest{
		SchemaVersion: fillerreview.TemporalStructureChallengeSchemaVersion, ContractVersion: fillerreview.TemporalStructureChallengeContractVersion,
		ChallengeID: "quarantine-fixture", GeneratedAt: generatedAt, ProductionAdmissionAllowed: false,
		Cases: []fillerreview.TemporalStructureChallengePublicCase{{Alias: alias, Video: fillerreview.TemporalTruthEvidenceFile{
			Path: filepath.ToSlash(filepath.Join("cases", alias, "video.mp4")), SHA256: quarantineHash(sourceBytes), Bytes: int64(len(sourceBytes)), DurationMS: 6_000, Width: 320, Height: 180,
		}}},
	}
	manifestPath := filepath.Join(publicRoot, "manifest.json")
	manifestRaw := writeQuarantineFixtureJSON(t, manifestPath, manifest)
	authority := fillerreview.TemporalStructureChallengeAuthority{
		SchemaVersion: manifest.SchemaVersion, ContractVersion: manifest.ContractVersion, ChallengeID: manifest.ChallengeID, GeneratedAt: manifest.GeneratedAt,
		AuthoringSHA256: strings.Repeat("a", 64), PlanContractVersion: fillerreview.TemporalStructureHoldoutContractVersion,
		PlanReceiptSHA256: strings.Repeat("b", 64), SeedSHA256: strings.Repeat("c", 64), PublicManifestSHA256: quarantineHash(manifestRaw), MediaTools: identity,
		Cases: []fillerreview.TemporalStructureChallengeAuthorityCase{{
			Alias: alias, CaseID: "prior-case", Unit: fillereval.UnitStandalone, Role: fillereval.TemporalRoleCommercial, VideoSHA256: quarantineHash(sourceBytes),
			Segments: []fillerreview.TemporalStructureChallengeAuthorityPart{{
				Ordinal: 0, SourceID: "prior-source", SourcePath: sourceName, SourceSHA256: quarantineHash(sourceBytes), SourceDurationMS: 6_000,
				SourceRole: fillereval.TemporalRoleCommercial, RequestedMS: 6_000, RenderedMS: 6_000, OutputEndMS: 6_000,
				Provenance: fillerreview.TemporalStructureSourceProvenance{
					Kind: fillerreview.TemporalStructureSourceBoundedItem, Authority: "fixture", Reference: "fixture:prior", MetadataSHA256: strings.Repeat("d", 64), RetrievedAt: generatedAt.Add(-time.Hour),
				},
			}},
		}},
	}
	authorityPath := filepath.Join(root, "challenge", "private", "authority.json")
	writeQuarantineFixtureJSON(t, authorityPath, authority)
	return manifestPath, authorityPath
}

func quarantineFixtureInventory(snapshot time.Time, name string, data []byte) fillercorpus.Inventory {
	authority, itemID, role := "loc.gov/national-screening-room", "candidate", "commercial"
	captureID := fillercorpus.NewCaptureID(authority, "fixture", role)
	return fillercorpus.Inventory{
		SchemaVersion: fillercorpus.InventorySchemaVersion, SnapshotAt: snapshot,
		Captures: []fillercorpus.Capture{{
			CaptureID: captureID, Transport: fillercorpus.TransportHTTPS, Authority: authority, Collection: "fixture", RoleHint: role, SnapshotAt: snapshot,
			MaxRequests: 1, RequestsUsed: 1, MaxResponseBytes: 1_000, ResponseBytes: 100, MaxPredictedMediaBytes: int64(len(data)), PredictedMediaBytes: int64(len(data)), MaxWallTimeMS: 1_000, WallTimeMS: 10,
		}},
		Cases: []fillercorpus.InventoryCase{{
			CaseID: fillercorpus.CaseID(authority, itemID), CaptureIDs: []string{captureID}, Authority: authority, ItemID: itemID, Title: "Candidate", RoleHints: []string{role},
			RightsAssertions: []string{"local quarantine review required"}, ItemURL: "https://www.loc.gov/item/candidate/", MetadataURL: "https://www.loc.gov/item/candidate/?fo=json",
			MetadataRetrievedAt: snapshot.Add(-time.Hour), MetadataSHA256: strings.Repeat("e", 64), AllowedMediaHosts: []string{"tile.loc.gov"},
			Representation: fillercorpus.InventoryRepresentation{Transport: fillercorpus.TransportHTTPS, Name: name, URL: "https://tile.loc.gov/candidate.mp4", MIMEType: "video/mp4", Bytes: int64(len(data)), SHA256: quarantineHash(data), DurationMS: 6_000, Width: 320, Height: 180},
		}},
	}
}

func quarantineFixtureLedger(inventory fillercorpus.Inventory, inventorySHA string, generatedAt time.Time, name string, data []byte) fillercorpus.DownloadLedger {
	item := inventory.Cases[0]
	approval := fillercorpus.RightsDecision{
		InventorySHA256: inventorySHA, CaseID: item.CaseID, CaptureIDs: append([]string(nil), item.CaptureIDs...), Authority: item.Authority, ItemID: item.ItemID, MetadataSHA256: item.MetadataSHA256,
		ReviewerID: "fixture-reviewer", ReviewedAt: generatedAt.Add(-time.Hour), Decision: "approved", Basis: "fixture local inspection", QuarantineContract: &fillercorpus.QuarantineAcquisitionContract{
			SchemaVersion: fillercorpus.QuarantineAcquisitionContractSchemaVersion, Purpose: fillercorpus.QuarantinePurposeLocalInspection, CopyAndStorage: true, LocalTechnicalInspection: true,
		},
	}
	return fillercorpus.DownloadLedger{
		SchemaVersion: fillercorpus.DownloadLedgerSchemaVersion, Profile: fillercorpus.RightsProfileQuarantine, InventorySHA256: inventorySHA, GeneratedAt: generatedAt,
		MaxRequests: 1, RequestsUsed: 1, MaxItems: 1, MaxBytes: int64(len(data)), Bytes: int64(len(data)),
		Cases: []fillercorpus.DownloadCase{{
			CaseID: item.CaseID, Authority: item.Authority, ItemID: item.ItemID, ItemURL: item.ItemURL, MetadataURL: item.MetadataURL,
			MetadataRetrievedAt: item.MetadataRetrievedAt, MetadataSHA256: item.MetadataSHA256, Representation: item.Representation, LocalFile: name,
			ContentSHA256: quarantineHash(data), Approval: approval, VerifiedAt: generatedAt,
		}},
	}
}

func readFixtureFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func writeQuarantineFixtureFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeQuarantineFixtureJSON(t *testing.T, path string, value any) []byte {
	t.Helper()
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, '\n')
	writeQuarantineFixtureFile(t, path, raw)
	return raw
}

func quarantineHash(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
