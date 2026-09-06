package fillerreview

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillerstructure"
	"github.com/loomarr/loomarr/internal/fillerstructurewindow"
)

func TestPublishTemporalStructureWindowAuthorityDerivesOnlyPassingObservedEnvelope(t *testing.T) {
	fixture := temporalStructureWindowAuthorityFixture(t)
	authority, fileSHA, err := PublishTemporalStructureWindowAuthority(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if len(authority.Assessors) != 2 || len(authority.AllowedUnits) != 1 || authority.AllowedUnits[0] != fillerstructure.UnitCompilation ||
		len(authority.AllowedRoles) != 2 || authority.MinimumSourceDurationMS <= 0 ||
		authority.MaximumSourceDurationMS < authority.MinimumSourceDurationMS || authority.MaximumWindowBytes <= 0 || authority.MaximumWindows <= 0 ||
		authority.TrainingAllowed || authority.ProductionAdmissionAllowed || !authority.AutomaticMaterializationAllowed ||
		authority.SHA256 != fillerstructurewindow.MaterializationAuthoritySHA256(authority) || !reviewSHA256(fileSHA) {
		t.Fatalf("authority=%+v fileSHA=%q", authority, fileSHA)
	}
	if err := fillerstructurewindow.ValidateMaterializationAuthority(authority); err != nil {
		t.Fatal(err)
	}
}

func TestPublishTemporalStructureWindowAuthorityRejectsMissingReviewOrShadowDrift(t *testing.T) {
	fixture := temporalStructureWindowAuthorityFixture(t)
	fixture.AutomaticMaterializationAllowed = false
	if _, _, err := PublishTemporalStructureWindowAuthority(fixture); err == nil || !strings.Contains(err.Error(), "explicit permission") {
		t.Fatalf("permission error=%v", err)
	}
	fixture = temporalStructureWindowAuthorityFixture(t)
	shadow := readStrictTestJSON[TemporalStructureShortLongShadowArtifact](t, fixture.ShortLongShadowPath)
	shadow.WindowDecisionSetFileSHA256 = strings.Repeat("f", 64)
	shadow.SHA256 = temporalStructureShortLongShadowSHA256(shadow)
	writeTestJSON(t, fixture.ShortLongShadowPath, shadow)
	fixture.OutputPath = filepath.Join(t.TempDir(), "drifted-authority.json")
	if _, _, err := PublishTemporalStructureWindowAuthority(fixture); err == nil || !strings.Contains(err.Error(), "exact complete passing shadow lineage") {
		t.Fatalf("shadow drift error=%v", err)
	}
}

func temporalStructureWindowAuthorityFixture(t *testing.T) TemporalStructureWindowAuthorityConfig {
	t.Helper()
	shadowFixture := temporalStructureShortLongShadowFixture(t)
	rewriteTemporalStructureDecisionSetAsCompilation(t, shadowFixture.config.CompleteDecisionSetPath)
	rewriteTemporalStructureDecisionSetAsCompilation(t, shadowFixture.config.WindowDecisionSetPath)
	shadowFixture.config.OutputPath = filepath.Join(t.TempDir(), "passing-shadow.json")
	if _, _, err := PublishTemporalStructureShortLongShadow(shadowFixture.config); err != nil {
		t.Fatal(err)
	}
	return TemporalStructureWindowAuthorityConfig{
		WindowSetManifestPath:   shadowFixture.config.WindowSetManifestPath,
		WindowCertificationPath: shadowFixture.config.WindowCertificationPath,
		CompleteDecisionSetPath: shadowFixture.config.CompleteDecisionSetPath,
		WindowDecisionSetPath:   shadowFixture.config.WindowDecisionSetPath,
		ShortLongShadowPath:     shadowFixture.config.OutputPath,
		ReviewerID:              "maintainer", ReviewedAt: time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC),
		AutomaticMaterializationAllowed: true, OutputPath: filepath.Join(t.TempDir(), "window-authority.json"),
	}
}

func rewriteTemporalStructureDecisionSetAsCompilation(t *testing.T, path string) {
	t.Helper()
	set := readStrictTestJSON[TemporalStructureShadowDecisionSet](t, path)
	for index, item := range set.Cases {
		old := item.Artifact
		middle := old.Decision.Source.DurationMS / 2
		segments := []fillerstructure.Segment{
			{StartMS: 0, EndMS: middle, Role: fillerstructure.RoleCommercial},
			{StartMS: middle, EndMS: old.Decision.Source.DurationMS, Role: fillerstructure.RolePromo},
		}
		candidates := make([]fillerstructure.Candidate, 0, len(old.Decision.Candidates))
		for _, prior := range old.Decision.Candidates {
			candidate, err := fillerstructure.NewCandidate(old.Decision.Source, old.Decision.Input.SHA256, prior.Assessor, "", segments)
			if err != nil {
				t.Fatal(err)
			}
			candidates = append(candidates, candidate)
		}
		artifact, err := fillerstructure.NewArtifact(fillerstructure.Request{
			Source: old.Decision.Source, Input: old.Decision.Input,
			BoundaryToleranceMS: old.BoundaryToleranceMS, Candidates: candidates,
		}, old.DecidedAt)
		if err != nil {
			t.Fatal(err)
		}
		set.Cases[index].Artifact = artifact
	}
	set.SHA256 = temporalStructureShadowDecisionSetSHA256(set)
	writeTestJSON(t, path, set)
}
