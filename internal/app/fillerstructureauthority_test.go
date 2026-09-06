package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillerstructure"
	"github.com/loomarr/loomarr/internal/fillerstructurewindow"
)

func TestLoadWindowStructureAuthorityAcceptsOnlyReplayValidRegularFile(t *testing.T) {
	authority := appWindowStructureAuthorityFixture()
	raw, err := json.Marshal(authority)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "authority.json")
	if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadWindowStructureAuthority(path)
	if err != nil || loaded == nil || loaded.SHA256 != authority.SHA256 {
		t.Fatalf("loaded=%+v error=%v", loaded, err)
	}
	if empty, err := loadWindowStructureAuthority(""); err != nil || empty != nil {
		t.Fatalf("empty=%+v error=%v", empty, err)
	}
}

func TestLoadWindowStructureAuthorityRejectsUnknownTrailingAndSymlinkedInput(t *testing.T) {
	root := t.TempDir()
	unknown := filepath.Join(root, "unknown.json")
	if err := os.WriteFile(unknown, []byte(`{"unknown":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadWindowStructureAuthority(unknown); err == nil {
		t.Fatal("unknown authority fields were accepted")
	}
	authority := appWindowStructureAuthorityFixture()
	raw, err := json.Marshal(authority)
	if err != nil {
		t.Fatal(err)
	}
	trailing := filepath.Join(root, "trailing.json")
	if err := os.WriteFile(trailing, append(raw, []byte("\n{}")...), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadWindowStructureAuthority(trailing); err == nil || !strings.Contains(err.Error(), "trailing") {
		t.Fatalf("trailing error=%v", err)
	}
	link := filepath.Join(root, "authority-link.json")
	if err := os.Symlink(trailing, link); err != nil {
		t.Fatal(err)
	}
	if _, err := loadWindowStructureAuthority(link); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("symlink error=%v", err)
	}
}

func appWindowStructureAuthorityFixture() fillerstructurewindow.MaterializationAuthority {
	profile := fillerstructurewindow.CanonicalProfile()
	authority := fillerstructurewindow.MaterializationAuthority{
		SchemaVersion:             fillerstructurewindow.MaterializationAuthoritySchemaVersion,
		ContractVersion:           fillerstructurewindow.MaterializationAuthorityContractVersion,
		WindowCertificationSHA256: strings.Repeat("a", 64), ShortLongShadowSHA256: strings.Repeat("b", 64),
		WindowProfileSHA256: profile.SHA256, AssessmentMediaProfileSHA256: profile.AssessmentMediaProfileSHA256,
		MinimumSourceDurationMS: 120_001, MaximumSourceDurationMS: 300_000,
		MaximumWindowBytes: 16 << 20, MaximumWindows: 3,
		ReducerVersion: fillerstructure.ReducerContractVersion, BoundaryToleranceMS: 2_000,
		AllowedUnits: []fillerstructure.Unit{fillerstructure.UnitCompilation},
		AllowedRoles: []fillerstructure.Role{fillerstructure.RoleCommercial, fillerstructure.RolePromo},
		ReviewerID:   "maintainer", ReviewedAt: time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC),
		AutomaticMaterializationAllowed: true,
	}
	for _, pair := range [][2]string{{"assessor-a", "family-a"}, {"assessor-b", "family-b"}} {
		authority.Assessors = append(authority.Assessors, fillerstructure.AssessorProfile{
			ID: pair[0], ModelFamily: pair[1], Provider: "openrouter", Model: "provider/model-" + pair[0],
			ModelDigest: strings.Repeat("c", 64), CapabilitySHA256: strings.Repeat("d", 64),
			PromptVersion:    fillerstructurewindow.DirectVideoPromptVersion,
			EvidenceContract: fillerstructurewindow.CallRecordContractVersion,
		})
	}
	authority.SHA256 = fillerstructurewindow.MaterializationAuthoritySHA256(authority)
	return authority
}
