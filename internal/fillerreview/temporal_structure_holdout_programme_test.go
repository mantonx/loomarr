package fillerreview

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillercorpus"
	"github.com/loomarr/loomarr/internal/fillerreference"
)

func TestLoadTemporalStructureHoldoutProgrammeInventoryAcceptsBoundSourceRecord(t *testing.T) {
	fixture := newTemporalStructureHoldoutFixture(t)
	ledger := fixture.downloadLedger(t)
	if _, digest, err := loadTemporalStructureHoldoutProgrammeInventory(fixture.inventory, fixture.root, ledger, fixture.plannedAt); err != nil || !reviewSHA256(digest) {
		t.Fatalf("load valid programme inventory = digest %q, error %v", digest, err)
	}
}

func TestLoadTemporalStructureHoldoutProgrammeInventoryNormalizesSourceRecordReference(t *testing.T) {
	fixture := newTemporalStructureHoldoutFixture(t)
	inventory := readStrictTestJSON[TemporalStructureHoldoutProgrammeInventory](t, fixture.inventory)
	mutateTemporalStructureProgrammeRecord(t, fixture, &inventory, func(record *fillercorpus.Inventory) {
		record.Cases[0].ItemURL = "https://EXAMPLE.invalid/items/test-programme-a"
	})
	inventoryPath := writeTemporalHumanJSON(t, t.TempDir(), "programme-inventory.json", inventory)
	if _, _, err := loadTemporalStructureHoldoutProgrammeInventory(inventoryPath, fixture.root, fixture.downloadLedger(t), fixture.plannedAt); err != nil {
		t.Fatalf("normalized source-record reference rejected: %v", err)
	}
}

func TestLoadTemporalStructureHoldoutProgrammeInventoryRejectsUppercaseHostParentReference(t *testing.T) {
	fixture := newTemporalStructureHoldoutFixture(t)
	inventory := readStrictTestJSON[TemporalStructureHoldoutProgrammeInventory](t, fixture.inventory)
	inventory.Sources[0].Provenance.Reference = strings.Replace(inventory.Sources[0].Provenance.Reference, "example.invalid", "EXAMPLE.INVALID", 1)
	assertProgrammeInventoryRejected(t, fixture, inventory)
}

func TestLoadTemporalStructureHoldoutProgrammeInventoryRejectsNormalizedLedgerParentCollision(t *testing.T) {
	fixture := newTemporalStructureHoldoutFixture(t)
	ledger := fixture.downloadLedger(t)
	parent := readStrictTestJSON[TemporalStructureHoldoutProgrammeInventory](t, fixture.inventory).Sources[0]
	ledger.Cases[0].Authority = parent.Provenance.Authority
	ledger.Cases[0].ItemURL = strings.Replace(parent.Provenance.Reference, "example.invalid", "EXAMPLE.INVALID", 1)
	raw, err := json.Marshal(ledger)
	if err != nil {
		t.Fatal(err)
	}
	ledgerPath := filepath.Join(t.TempDir(), "ledger.json")
	if err := os.WriteFile(ledgerPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	audit := readStrictTestJSON[fillerreference.Audit](t, fixture.referenceAudit)
	audit.Inputs.DownloadLedgerSHA256 = hashBytes(raw)
	_, normalized, err := loadTemporalStructureHoldoutReferenceDownloadLedger(ledgerPath, audit)
	if err != nil {
		t.Fatalf("uppercase-host ledger rejected: %v", err)
	}
	if _, _, err := loadTemporalStructureHoldoutProgrammeInventory(fixture.inventory, fixture.root, normalized, fixture.plannedAt); err == nil {
		t.Fatal("programme loader accepted normalized ledger parent collision")
	}
}

func TestLoadTemporalStructureHoldoutProgrammeInventoryRejectsHostileSourceRecords(t *testing.T) {
	tests := map[string]func(*testing.T, *temporalStructureHoldoutFixture, *TemporalStructureHoldoutProgrammeInventory){
		"missing source record": func(_ *testing.T, _ *temporalStructureHoldoutFixture, inventory *TemporalStructureHoldoutProgrammeInventory) {
			inventory.Sources[0].Provenance.SourceRecordPath = "missing-source-record.json"
		},
		"malformed source record": func(t *testing.T, fixture *temporalStructureHoldoutFixture, inventory *TemporalStructureHoldoutProgrammeInventory) {
			writeProgrammeRecordBytes(t, fixture, inventory, []byte("{"))
		},
		"unknown source record field": func(t *testing.T, fixture *temporalStructureHoldoutFixture, inventory *TemporalStructureHoldoutProgrammeInventory) {
			writeProgrammeRecordBytes(t, fixture, inventory, []byte(`{"schemaVersion":4,"snapshotAt":"2026-01-01T00:00:00Z","captures":[],"cases":[],"unknown":true}`))
		},
		"legacy source record contract": func(t *testing.T, fixture *temporalStructureHoldoutFixture, inventory *TemporalStructureHoldoutProgrammeInventory) {
			writeProgrammeRecordBytes(t, fixture, inventory, []byte(`{"schemaVersion":1,"snapshotAt":"2026-01-01T00:00:00Z","captures":[],"cases":[]}`))
		},
		"source record hash drift": func(_ *testing.T, _ *temporalStructureHoldoutFixture, inventory *TemporalStructureHoldoutProgrammeInventory) {
			inventory.Sources[0].Provenance.SourceRecordSHA256 = strings.Repeat("0", 64)
		},
		"oversize source record": func(t *testing.T, fixture *temporalStructureHoldoutFixture, inventory *TemporalStructureHoldoutProgrammeInventory) {
			writeProgrammeRecordBytes(t, fixture, inventory, make([]byte, temporalStructureProgrammeEvidenceMaxBytes+1))
		},
		"absolute source record path": func(_ *testing.T, fixture *temporalStructureHoldoutFixture, inventory *TemporalStructureHoldoutProgrammeInventory) {
			inventory.Sources[0].Provenance.SourceRecordPath = fixture.inventory
		},
		"existing escaping source record path": func(t *testing.T, fixture *temporalStructureHoldoutFixture, inventory *TemporalStructureHoldoutProgrammeInventory) {
			escape := filepath.Join(filepath.Dir(filepath.Dir(fixture.root)), "existing-escape-record.json")
			if err := os.WriteFile(escape, []byte("outside"), 0o600); err != nil {
				t.Fatal(err)
			}
			inventory.Sources[0].Provenance.SourceRecordPath = "../../existing-escape-record.json"
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			fixture := newTemporalStructureHoldoutFixture(t)
			inventory := readStrictTestJSON[TemporalStructureHoldoutProgrammeInventory](t, fixture.inventory)
			mutate(t, &fixture, &inventory)
			assertProgrammeInventoryRejected(t, fixture, inventory)
		})
	}
}

func TestLoadTemporalStructureHoldoutProgrammeInventoryRejectsHostileMetadataAndMediaPaths(t *testing.T) {
	tests := map[string]func(*testing.T, *temporalStructureHoldoutFixture, *TemporalStructureHoldoutProgrammeInventory){
		"missing metadata": func(t *testing.T, fixture *temporalStructureHoldoutFixture, inventory *TemporalStructureHoldoutProgrammeInventory) {
			mutateTemporalStructureProgrammeRecord(t, *fixture, inventory, func(record *fillercorpus.Inventory) { record.Cases[0].MetadataCache = "missing-metadata.json" })
		},
		"metadata bytes drift": func(t *testing.T, fixture *temporalStructureHoldoutFixture, inventory *TemporalStructureHoldoutProgrammeInventory) {
			record := readProgrammeRecord(t, *fixture, *inventory)
			if err := os.WriteFile(filepath.Join(fixture.root, filepath.FromSlash(record.Cases[0].MetadataCache)), []byte("drifted metadata"), 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"oversize metadata": func(t *testing.T, fixture *temporalStructureHoldoutFixture, inventory *TemporalStructureHoldoutProgrammeInventory) {
			record := readProgrammeRecord(t, *fixture, *inventory)
			if err := os.WriteFile(filepath.Join(fixture.root, filepath.FromSlash(record.Cases[0].MetadataCache)), make([]byte, temporalStructureProgrammeEvidenceMaxBytes+1), 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"absolute metadata path": func(t *testing.T, fixture *temporalStructureHoldoutFixture, inventory *TemporalStructureHoldoutProgrammeInventory) {
			mutateTemporalStructureProgrammeRecord(t, *fixture, inventory, func(record *fillercorpus.Inventory) { record.Cases[0].MetadataCache = fixture.inventory })
		},
		"existing escaping metadata path": func(t *testing.T, fixture *temporalStructureHoldoutFixture, inventory *TemporalStructureHoldoutProgrammeInventory) {
			escape := filepath.Join(filepath.Dir(filepath.Dir(fixture.root)), "existing-escape-metadata.json")
			if err := os.WriteFile(escape, []byte("outside"), 0o600); err != nil {
				t.Fatal(err)
			}
			mutateTemporalStructureProgrammeRecord(t, *fixture, inventory, func(record *fillercorpus.Inventory) {
				record.Cases[0].MetadataCache = "../../existing-escape-metadata.json"
			})
		},
		"absolute media path": func(_ *testing.T, fixture *temporalStructureHoldoutFixture, inventory *TemporalStructureHoldoutProgrammeInventory) {
			inventory.Sources[0].Path = fixture.inventory
		},
		"existing escaping media path": func(t *testing.T, fixture *temporalStructureHoldoutFixture, inventory *TemporalStructureHoldoutProgrammeInventory) {
			escape := filepath.Join(filepath.Dir(filepath.Dir(fixture.root)), "existing-escape-media.mp4")
			if err := os.WriteFile(escape, []byte("outside"), 0o600); err != nil {
				t.Fatal(err)
			}
			inventory.Sources[0].Path = "../../existing-escape-media.mp4"
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			fixture := newTemporalStructureHoldoutFixture(t)
			inventory := readStrictTestJSON[TemporalStructureHoldoutProgrammeInventory](t, fixture.inventory)
			mutate(t, &fixture, &inventory)
			assertProgrammeInventoryRejected(t, fixture, inventory)
		})
	}
}

func TestLoadTemporalStructureHoldoutProgrammeInventoryRejectsSymlinkedEvidence(t *testing.T) {
	tests := map[string]func(*testing.T, *temporalStructureHoldoutFixture, *TemporalStructureHoldoutProgrammeInventory){
		"symlink file": func(t *testing.T, fixture *temporalStructureHoldoutFixture, inventory *TemporalStructureHoldoutProgrammeInventory) {
			link := filepath.Join(fixture.root, "record-link.json")
			if err := os.Symlink(filepath.Join(fixture.root, inventory.Sources[0].Provenance.SourceRecordPath), link); err != nil {
				t.Fatal(err)
			}
			inventory.Sources[0].Provenance.SourceRecordPath = filepath.Base(link)
		},
		"symlink intermediate directory": func(t *testing.T, fixture *temporalStructureHoldoutFixture, inventory *TemporalStructureHoldoutProgrammeInventory) {
			link := filepath.Join(fixture.root, "record-directory-link")
			if err := os.Symlink(fixture.root, link); err != nil {
				t.Fatal(err)
			}
			inventory.Sources[0].Provenance.SourceRecordPath = filepath.ToSlash(filepath.Join(filepath.Base(link), inventory.Sources[0].Provenance.SourceRecordPath))
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			fixture := newTemporalStructureHoldoutFixture(t)
			inventory := readStrictTestJSON[TemporalStructureHoldoutProgrammeInventory](t, fixture.inventory)
			mutate(t, &fixture, &inventory)
			assertProgrammeInventoryRejected(t, fixture, inventory)
		})
	}
}

func TestLoadTemporalStructureHoldoutProgrammeInventoryRejectsBrokenRecordBindings(t *testing.T) {
	tests := map[string]func(*testing.T, *temporalStructureHoldoutFixture, *TemporalStructureHoldoutProgrammeInventory){
		"item identity": func(_ *testing.T, _ *temporalStructureHoldoutFixture, inventory *TemporalStructureHoldoutProgrammeInventory) {
			inventory.Sources[0].Provenance.ItemID = "wrong-item"
		},
		"authority": func(_ *testing.T, _ *temporalStructureHoldoutFixture, inventory *TemporalStructureHoldoutProgrammeInventory) {
			inventory.Sources[0].Provenance.Authority = "wrong-authority"
		},
		"reference": func(_ *testing.T, _ *temporalStructureHoldoutFixture, inventory *TemporalStructureHoldoutProgrammeInventory) {
			inventory.Sources[0].Provenance.Reference = "https://example.invalid/items/wrong-item"
		},
		"retrieval": func(_ *testing.T, _ *temporalStructureHoldoutFixture, inventory *TemporalStructureHoldoutProgrammeInventory) {
			inventory.Sources[0].Provenance.RetrievedAt = inventory.Sources[0].Provenance.RetrievedAt.Add(time.Second)
		},
		"representation path": func(t *testing.T, fixture *temporalStructureHoldoutFixture, inventory *TemporalStructureHoldoutProgrammeInventory) {
			mutateTemporalStructureProgrammeRecord(t, *fixture, inventory, func(record *fillercorpus.Inventory) {
				record.Cases[0].Representation.Path = record.Cases[0].MetadataCache
			})
		},
		"representation SHA": func(t *testing.T, fixture *temporalStructureHoldoutFixture, inventory *TemporalStructureHoldoutProgrammeInventory) {
			mutateTemporalStructureProgrammeRecord(t, *fixture, inventory, func(record *fillercorpus.Inventory) { record.Cases[0].Representation.SHA256 = strings.Repeat("f", 64) })
		},
		"representation size": func(t *testing.T, fixture *temporalStructureHoldoutFixture, inventory *TemporalStructureHoldoutProgrammeInventory) {
			mutateTemporalStructureProgrammeRecord(t, *fixture, inventory, func(record *fillercorpus.Inventory) { record.Cases[0].Representation.Bytes++ })
		},
		"representation duration": func(t *testing.T, fixture *temporalStructureHoldoutFixture, inventory *TemporalStructureHoldoutProgrammeInventory) {
			mutateTemporalStructureProgrammeRecord(t, *fixture, inventory, func(record *fillercorpus.Inventory) { record.Cases[0].Representation.DurationMS++ })
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			fixture := newTemporalStructureHoldoutFixture(t)
			inventory := readStrictTestJSON[TemporalStructureHoldoutProgrammeInventory](t, fixture.inventory)
			mutate(t, &fixture, &inventory)
			assertProgrammeInventoryRejected(t, fixture, inventory)
		})
	}
}

func TestLoadTemporalStructureHoldoutProgrammeInventoryRejectsSeventhProgrammeSourceMatchingUnselectedFiller(t *testing.T) {
	fixture := newTemporalStructureHoldoutFixture(t)
	inventory := readStrictTestJSON[TemporalStructureHoldoutProgrammeInventory](t, fixture.inventory)
	seventh := inventory.Sources[0]
	seventh.ID = "programme-g"
	seventh.Path = "programmes/parent-g.mp4"
	seventh.DurationMS = 240_000
	seventh.Provenance.ItemID = "test-programme-g"
	seventh.Provenance.Reference = "https://example.invalid/items/test-programme-g"
	seventh.Provenance.RetrievedAt = inventory.GeneratedAt.Add(-time.Hour)
	media := []byte("seventh-programme-parent")
	metadata := []byte("seventh-programme-metadata")
	if err := os.WriteFile(filepath.Join(fixture.root, filepath.FromSlash(seventh.Path)), media, 0o600); err != nil {
		t.Fatal(err)
	}
	metadataPath := "programmes/parent-g.json"
	if err := os.WriteFile(filepath.Join(fixture.root, filepath.FromSlash(metadataPath)), metadata, 0o600); err != nil {
		t.Fatal(err)
	}
	seventh.SHA256 = hashBytes(media)
	seventh.Provenance.MetadataSHA256 = hashBytes(metadata)
	mutateTemporalStructureProgrammeRecord(t, fixture, &inventory, func(record *fillercorpus.Inventory) {
		item := record.Cases[0]
		item.CaseID = fillercorpus.CaseID(seventh.Provenance.Authority, seventh.Provenance.ItemID)
		item.ItemID = seventh.Provenance.ItemID
		item.Title = seventh.Provenance.ItemID
		item.ItemURL = seventh.Provenance.Reference
		item.MetadataURL = "https://example.invalid/metadata/" + seventh.Provenance.ItemID
		item.MetadataCache = metadataPath
		item.MetadataRetrievedAt = seventh.Provenance.RetrievedAt
		item.MetadataSHA256 = seventh.Provenance.MetadataSHA256
		item.Representation.Name = filepath.Base(seventh.Path)
		item.Representation.Path = seventh.Path
		item.Representation.Bytes = int64(len(media))
		item.Representation.SHA256 = seventh.SHA256
		item.Representation.DurationMS = seventh.DurationMS
		item.Evidence[0].Path, item.Evidence[0].Bytes, item.Evidence[0].SHA256 = metadataPath, int64(len(metadata)), hashBytes(metadata)
		item.Evidence[1].Path, item.Evidence[1].Bytes, item.Evidence[1].SHA256 = metadataPath, int64(len(metadata)), hashBytes(metadata)
		record.Cases = append(record.Cases, item)
		record.Captures[0].PredictedMediaBytes += item.Representation.Bytes
		record.Captures[0].MaxPredictedMediaBytes = record.Captures[0].PredictedMediaBytes
	})
	seventh.Provenance.SourceRecordSHA256 = inventory.Sources[0].Provenance.SourceRecordSHA256
	inventory.Sources = append(inventory.Sources, seventh)
	ledger := fixture.downloadLedger(t)
	validInventoryPath := writeTemporalHumanJSON(t, t.TempDir(), "programme-inventory.json", inventory)
	if _, _, err := loadTemporalStructureHoldoutProgrammeInventory(validInventoryPath, fixture.root, ledger, fixture.plannedAt); err != nil {
		t.Fatalf("valid seventh programme inventory rejected: %v", err)
	}
	ledger.Cases[len(ledger.Cases)-1].Authority = seventh.Provenance.Authority
	ledger.Cases[len(ledger.Cases)-1].ItemURL = seventh.Provenance.Reference
	inventoryPath := writeTemporalHumanJSON(t, t.TempDir(), "programme-inventory.json", inventory)
	if _, _, err := loadTemporalStructureHoldoutProgrammeInventory(inventoryPath, fixture.root, ledger, fixture.plannedAt); err == nil || !strings.Contains(err.Error(), "repeats bounded filler lineage") {
		t.Fatalf("seventh programme source matching unselected filler error = %v", err)
	}
	for name, mutate := range map[string]func(*fillerreference.DownloadCase, *TemporalStructureHoldoutProgrammeInventory){
		"canonical URL with different authority": func(item *fillerreference.DownloadCase, _ *TemporalStructureHoldoutProgrammeInventory) {
			item.Authority = "different-authority"
			item.ItemID = "different-item"
			item.ContentSHA256 = strings.Repeat("a", 64)
			item.LocalFile = "different/path.mp4"
			item.ItemURL = seventh.Provenance.Reference
		},
		"content hash": func(item *fillerreference.DownloadCase, _ *TemporalStructureHoldoutProgrammeInventory) {
			item.ContentSHA256 = seventh.SHA256
		},
		"canonical relative local path": func(item *fillerreference.DownloadCase, _ *TemporalStructureHoldoutProgrammeInventory) {
			item.LocalFile = seventh.Path
		},
		"authority and item ID": func(item *fillerreference.DownloadCase, _ *TemporalStructureHoldoutProgrammeInventory) {
			item.Authority = seventh.Provenance.Authority
			item.ItemID = seventh.Provenance.ItemID
		},
		"padded parent authority and item ID": func(item *fillerreference.DownloadCase, candidate *TemporalStructureHoldoutProgrammeInventory) {
			candidate.Sources[len(candidate.Sources)-1].Provenance.Authority = " " + seventh.Provenance.Authority + " "
			candidate.Sources[len(candidate.Sources)-1].Provenance.ItemID = " " + seventh.Provenance.ItemID + " "
			item.Authority = seventh.Provenance.Authority
			item.ItemID = seventh.Provenance.ItemID
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := fixture.downloadLedger(t)
			candidateInventory := inventory
			candidateInventory.Sources = append([]TemporalStructureChallengeSource(nil), inventory.Sources...)
			mutate(&candidate.Cases[len(candidate.Cases)-1], &candidateInventory)
			candidatePath := inventoryPath
			if name == "padded parent authority and item ID" {
				candidatePath = writeTemporalHumanJSON(t, t.TempDir(), "programme-inventory.json", candidateInventory)
				if _, _, err := loadTemporalStructureHoldoutProgrammeInventory(candidatePath, fixture.root, fixture.downloadLedger(t), fixture.plannedAt); err != nil {
					t.Fatalf("padded seventh programme inventory rejected with untouched ledger: %v", err)
				}
			}
			if _, _, err := loadTemporalStructureHoldoutProgrammeInventory(candidatePath, fixture.root, candidate, fixture.plannedAt); err == nil || !strings.Contains(err.Error(), "repeats bounded filler lineage") {
				t.Fatalf("accepted seventh programme collision %q", name)
			}
		})
	}
}

func TestBuildTemporalStructureHoldoutPlanDoesNotPublishOnProgrammeInventoryFailure(t *testing.T) {
	fixture := newTemporalStructureHoldoutFixture(t)
	inventory := readStrictTestJSON[TemporalStructureHoldoutProgrammeInventory](t, fixture.inventory)
	inventory.Sources[0].Provenance.SourceRecordPath = "missing-source-record.json"
	fixture.inventory = writeTemporalHumanJSON(t, t.TempDir(), "programme-inventory.json", inventory)
	output := filepath.Join(t.TempDir(), "output")
	if _, err := BuildTemporalStructureHoldoutPlan(fixture.config(output)); err == nil {
		t.Fatal("BuildTemporalStructureHoldoutPlan accepted a missing source record")
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("programme inventory failure published output: stat error = %v", err)
	}
}

func assertProgrammeInventoryRejected(t *testing.T, fixture temporalStructureHoldoutFixture, inventory TemporalStructureHoldoutProgrammeInventory) {
	t.Helper()
	ledger := fixture.downloadLedger(t)
	if _, _, err := loadTemporalStructureHoldoutProgrammeInventory(writeTemporalHumanJSON(t, t.TempDir(), "programme-inventory.json", inventory), fixture.root, ledger, fixture.plannedAt); err == nil {
		t.Fatal("hostile programme inventory was accepted")
	}
}

func readProgrammeRecord(t *testing.T, fixture temporalStructureHoldoutFixture, inventory TemporalStructureHoldoutProgrammeInventory) fillercorpus.Inventory {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(fixture.root, filepath.FromSlash(inventory.Sources[0].Provenance.SourceRecordPath)))
	if err != nil {
		t.Fatal(err)
	}
	record, err := fillercorpus.DecodeInventoryBytes(raw)
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func writeProgrammeRecordBytes(t *testing.T, fixture *temporalStructureHoldoutFixture, inventory *TemporalStructureHoldoutProgrammeInventory, raw []byte) {
	t.Helper()
	path := filepath.Join(fixture.root, filepath.FromSlash(inventory.Sources[0].Provenance.SourceRecordPath))
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	for index := range inventory.Sources {
		inventory.Sources[index].Provenance.SourceRecordSHA256 = hashBytes(raw)
	}
}
