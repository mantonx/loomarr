package fillerreview

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillerbakeoff"
)

func TestLockTemporalStructureAssessmentBindsRawResultSnapshotAndPrivateTruth(t *testing.T) {
	now := time.Date(2026, 9, 2, 4, 0, 0, 0, time.UTC)
	fixture := newTemporalStructureFixture(t)
	root, challenge := fixture.build(t, "lock-result")
	manifestPath := filepath.Join(root, "public", "manifest.json")
	authorityPath := filepath.Join(root, "private", "authority.json")
	manifest := readStrictTestJSON[TemporalStructureChallengeManifest](t, manifestPath)
	aliases := temporalStructureAliases(manifest)
	resultPath := filepath.Join(t.TempDir(), "result.json")
	server := newTemporalSuitabilityServer(t, nil, temporalStructureStandaloneResponse)
	defer server.Close()
	config := temporalStructureOpenRouterTestConfig(manifestPath, resultPath+".private", aliases, server, now)
	result, err := RunOpenRouterTemporalStructure(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	writeTemporalStructureResult(t, resultPath, result)
	snapshotPath := writeTemporalStructureSnapshot(t, config.Snapshot)
	outputPath := filepath.Join(t.TempDir(), "assessment-set.json")
	locked, err := LockTemporalStructureAssessment(TemporalStructureAssessmentLockConfig{
		PublicManifestPath: manifestPath, PrivateAuthorityPath: authorityPath, ResultPath: resultPath,
		SnapshotPath: snapshotPath, ExpectedCases: len(aliases), LockedAt: now.Add(time.Hour), OutputPath: outputPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if locked.Assessments != len(aliases) || !reviewSHA256(locked.AssessmentSHA256) || !reviewSHA256(locked.RawResultSHA256) || !reviewSHA256(locked.SnapshotFileSHA256) {
		t.Fatalf("lock result = %+v", locked)
	}
	set := readStrictTestJSON[TemporalStructureAssessmentSet](t, outputPath)
	if set.PublicManifestSHA256 != challenge.PublicManifestSHA256 || set.PrivateAuthoritySHA256 != challenge.AuthoritySHA256 || set.RawResultSHA256 != locked.RawResultSHA256 || set.SnapshotFileSHA256 != locked.SnapshotFileSHA256 || set.CapabilitySnapshotSHA256 != fillerbakeoff.OpenRouterSnapshotSHA256(config.Snapshot) || !set.LockedAt.Equal(now.Add(time.Hour)) || set.ProductionAdmissionAllowed {
		t.Fatalf("locked set = %+v", set)
	}
	if _, err := loadTemporalStructureAssessment(outputPath, manifest, challenge.PublicManifestSHA256, challenge.AuthoritySHA256); err != nil {
		t.Fatal(err)
	}
	if _, err := LockTemporalStructureAssessment(TemporalStructureAssessmentLockConfig{
		PublicManifestPath: manifestPath, PrivateAuthorityPath: authorityPath, ResultPath: resultPath,
		SnapshotPath: snapshotPath, ExpectedCases: len(aliases), LockedAt: now.Add(time.Hour), OutputPath: outputPath,
	}); err == nil {
		t.Fatal("immutable lock output was overwritten")
	}
}

func TestLockTemporalStructureAssessmentRejectsRawResponseTamper(t *testing.T) {
	now := time.Date(2026, 9, 2, 4, 0, 0, 0, time.UTC)
	fixture := newTemporalStructureFixture(t)
	root, _ := fixture.build(t, "lock-tamper")
	manifestPath := filepath.Join(root, "public", "manifest.json")
	authorityPath := filepath.Join(root, "private", "authority.json")
	manifest := readStrictTestJSON[TemporalStructureChallengeManifest](t, manifestPath)
	aliases := temporalStructureAliases(manifest)
	resultPath := filepath.Join(t.TempDir(), "result.json")
	server := newTemporalSuitabilityServer(t, nil, temporalStructureStandaloneResponse)
	defer server.Close()
	config := temporalStructureOpenRouterTestConfig(manifestPath, resultPath+".private", aliases, server, now)
	result, err := RunOpenRouterTemporalStructure(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	writeTemporalStructureResult(t, resultPath, result)
	snapshotPath := writeTemporalStructureSnapshot(t, config.Snapshot)
	rawPath := filepath.Join(resultPath+".private", filepath.FromSlash(result.Attempts[0].RawResponsePath))
	if err := os.WriteFile(rawPath, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = LockTemporalStructureAssessment(TemporalStructureAssessmentLockConfig{
		PublicManifestPath: manifestPath, PrivateAuthorityPath: authorityPath, ResultPath: resultPath,
		SnapshotPath: snapshotPath, ExpectedCases: len(aliases), LockedAt: now.Add(time.Hour), OutputPath: filepath.Join(t.TempDir(), "lock.json"),
	})
	if err == nil || !strings.Contains(err.Error(), "raw-response binding failed") {
		t.Fatalf("tamper error = %v", err)
	}
}

func TestLockTemporalStructureAssessmentRejectsSnapshotRouteDrift(t *testing.T) {
	now := time.Date(2026, 9, 2, 4, 0, 0, 0, time.UTC)
	fixture := newTemporalStructureFixture(t)
	root, _ := fixture.build(t, "lock-route-drift")
	manifestPath := filepath.Join(root, "public", "manifest.json")
	authorityPath := filepath.Join(root, "private", "authority.json")
	manifest := readStrictTestJSON[TemporalStructureChallengeManifest](t, manifestPath)
	aliases := temporalStructureAliases(manifest)
	resultPath := filepath.Join(t.TempDir(), "result.json")
	server := newTemporalSuitabilityServer(t, nil, temporalStructureStandaloneResponse)
	defer server.Close()
	config := temporalStructureOpenRouterTestConfig(manifestPath, resultPath+".private", aliases, server, now)
	result, err := RunOpenRouterTemporalStructure(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	result.UpstreamProvider = "unbound-provider"
	writeTemporalStructureResult(t, resultPath, result)
	snapshotPath := writeTemporalStructureSnapshot(t, config.Snapshot)
	_, err = LockTemporalStructureAssessment(TemporalStructureAssessmentLockConfig{
		PublicManifestPath: manifestPath, PrivateAuthorityPath: authorityPath, ResultPath: resultPath,
		SnapshotPath: snapshotPath, ExpectedCases: len(aliases), LockedAt: now.Add(time.Hour), OutputPath: filepath.Join(t.TempDir(), "lock.json"),
	})
	if err == nil || !strings.Contains(err.Error(), "route") {
		t.Fatalf("route drift error = %v", err)
	}
}

func TestLockTemporalStructureAssessmentReproducesSnapshotPriceBound(t *testing.T) {
	now := time.Date(2026, 9, 2, 4, 0, 0, 0, time.UTC)
	fixture := newTemporalStructureFixture(t)
	root, _ := fixture.build(t, "lock-price-drift")
	manifestPath := filepath.Join(root, "public", "manifest.json")
	authorityPath := filepath.Join(root, "private", "authority.json")
	manifest := readStrictTestJSON[TemporalStructureChallengeManifest](t, manifestPath)
	aliases := temporalStructureAliases(manifest)
	resultPath := filepath.Join(t.TempDir(), "result.json")
	server := newTemporalSuitabilityServer(t, nil, temporalStructureStandaloneResponse)
	defer server.Close()
	config := temporalStructureOpenRouterTestConfig(manifestPath, resultPath+".private", aliases, server, now)
	result, err := RunOpenRouterTemporalStructure(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	result.EstimatedMaximumChargeNanoUSD++
	writeTemporalStructureResult(t, resultPath, result)
	_, err = LockTemporalStructureAssessment(TemporalStructureAssessmentLockConfig{
		PublicManifestPath: manifestPath, PrivateAuthorityPath: authorityPath, ResultPath: resultPath,
		SnapshotPath: writeTemporalStructureSnapshot(t, config.Snapshot), ExpectedCases: len(aliases),
		LockedAt: now.Add(time.Hour), OutputPath: filepath.Join(t.TempDir(), "lock.json"),
	})
	if err == nil || !strings.Contains(err.Error(), "price bound") {
		t.Fatalf("price drift error=%v", err)
	}
}

func writeTemporalStructureSnapshot(t *testing.T, snapshot fillerbakeoff.OpenRouterSnapshot) string {
	t.Helper()
	raw, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "snapshot.json")
	if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
