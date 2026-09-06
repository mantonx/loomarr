package fillerreview

import (
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
	reference := readStrictTestJSON[fillerreference.Audit](t, fixture.referenceAudit)
	if _, digest, err := loadTemporalStructureHoldoutProgrammeInventory(fixture.inventory, fixture.root, reference, fixture.plannedAt); err != nil || !reviewSHA256(digest) {
		t.Fatalf("load valid programme inventory = digest %q, error %v", digest, err)
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
	reference := readStrictTestJSON[fillerreference.Audit](t, fixture.referenceAudit)
	reference.Cases[len(reference.Cases)-1].ContentSHA256 = seventh.SHA256
	inventoryPath := writeTemporalHumanJSON(t, t.TempDir(), "programme-inventory.json", inventory)
	if _, _, err := loadTemporalStructureHoldoutProgrammeInventory(inventoryPath, fixture.root, reference, fixture.plannedAt); err == nil || !strings.Contains(err.Error(), "repeats bounded filler lineage") {
		t.Fatalf("seventh programme source matching unselected filler error = %v", err)
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
	reference := readStrictTestJSON[fillerreference.Audit](t, fixture.referenceAudit)
	if _, _, err := loadTemporalStructureHoldoutProgrammeInventory(writeTemporalHumanJSON(t, t.TempDir(), "programme-inventory.json", inventory), fixture.root, reference, fixture.plannedAt); err == nil {
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
