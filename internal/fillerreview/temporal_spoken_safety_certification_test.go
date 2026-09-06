package fillerreview

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillerbakeoff"
)

func TestPublishTemporalSpokenSafetyCertificationPassesLockedFamilyAndCleanGates(t *testing.T) {
	fixture := newTemporalSpokenSafetyCertificationFixture(t)
	output := filepath.Join(t.TempDir(), "certification.json")
	report, digest, err := PublishTemporalSpokenSafetyCertification(fixture.config(output))
	if err != nil {
		t.Fatal(err)
	}
	if report.CertificationStatus != TemporalSpokenSafetyCertificationPassed || report.PositiveFamilies != 59 || report.MissedPositiveSources != 0 || report.SourceRecall != 1 || report.SourceRecallExactLower95 < 0.95 || report.CleanSources != 4 || report.CleanFalsePositiveSources != 0 || report.CoverageHolds != 0 || !reviewSHA256(digest) || report.ProductionAdmissionAllowed {
		t.Fatalf("certification = %+v", report)
	}
	for _, metric := range report.CleanSlices {
		if !metric.Passed || metric.FalsePositiveRate != 0 {
			t.Fatalf("clean metric = %+v", metric)
		}
	}
	raw, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{fixture.authority.Cases[0].SourceSHA256, fixture.authority.Cases[0].SourceFamilyID} {
		if strings.Contains(string(raw), private) {
			t.Fatalf("certification leaked private source authority")
		}
	}
	repeat := filepath.Join(t.TempDir(), "repeat.json")
	_, repeatedDigest, err := PublishTemporalSpokenSafetyCertification(fixture.config(repeat))
	if err != nil {
		t.Fatal(err)
	}
	if repeatedDigest != digest {
		t.Fatalf("repeat digest = %s, want %s", repeatedDigest, digest)
	}
}

func TestPublishTemporalSpokenSafetyCertificationFailsMissAndCleanFalsePositive(t *testing.T) {
	t.Run("positive interval miss", func(t *testing.T) {
		fixture := newTemporalSpokenSafetyCertificationFixture(t)
		fixture.projection.SourceDispositions[0].Matches = nil
		fixture.projection.SourceDispositions[0].Disposition = TemporalSpokenSafetyDispositionNoSignal
		fixture.projection.ProhibitedSources--
		fixture.projection.NoSignalObservedSources++
		fixture.rewriteProjection(t)
		report, _, err := PublishTemporalSpokenSafetyCertification(fixture.config(filepath.Join(t.TempDir(), "score.json")))
		if err != nil {
			t.Fatal(err)
		}
		if report.CertificationStatus != TemporalSpokenSafetyCertificationFailed || report.MissedPositiveSources != 1 || report.SourceRecallExactLower95 <= 0 || report.SourceRecallExactLower95 >= 0.95 {
			t.Fatalf("miss report = %+v", report)
		}
	})
	t.Run("clean false positive", func(t *testing.T) {
		fixture := newTemporalSpokenSafetyCertificationFixture(t)
		index := temporalSpokenSafetyMinimumPositiveFamilies
		fixture.projection.SourceDispositions[index].Matches = []TemporalSpokenSafetyMatch{{RuleID: "rule-000102030405060708090a0b", Class: TemporalSpokenSafetyMatchProhibited, StartMS: 100, EndMS: 900}}
		fixture.projection.SourceDispositions[index].Disposition = TemporalSpokenSafetyDispositionProhibited
		fixture.projection.NoSignalObservedSources--
		fixture.projection.ProhibitedSources++
		fixture.rewriteProjection(t)
		report, _, err := PublishTemporalSpokenSafetyCertification(fixture.config(filepath.Join(t.TempDir(), "score.json")))
		if err != nil {
			t.Fatal(err)
		}
		if report.CertificationStatus != TemporalSpokenSafetyCertificationFailed || report.CleanFalsePositiveSources != 1 {
			t.Fatalf("false-positive report = %+v", report)
		}
	})
}

func TestTemporalSpokenSafetyExactLower95ReportsPartialRecall(t *testing.T) {
	if got := temporalSpokenSafetyExactLower95(17, 59); math.Abs(got-0.192640999722) > 1e-12 {
		t.Fatalf("17/59 exact lower = %.12f, want 0.192640999722", got)
	}
	if got := temporalSpokenSafetyExactLower95(59, 59); math.Abs(got-math.Pow(0.05, 1.0/59.0)) > 1e-12 {
		t.Fatalf("59/59 exact lower = %.12f", got)
	}
	if got := temporalSpokenSafetyExactLower95(0, 59); got != 0 {
		t.Fatalf("0/59 exact lower = %.12f, want zero", got)
	}
	if got := temporalSpokenSafetyExactLower95(500, 1_000); math.IsNaN(got) || got <= 0 || got >= 0.5 {
		t.Fatalf("500/1000 exact lower = %.12f, want a finite lower bound", got)
	}
}

func TestPublishTemporalSpokenSafetyCertificationRejectsPostProjectionAuthority(t *testing.T) {
	fixture := newTemporalSpokenSafetyCertificationFixture(t)
	fixture.authority.AuthoredAt = fixture.projection.ProjectedAt.Add(time.Second)
	writeTemporalSpokenSafetyJSON(t, fixture.authorityPath, fixture.authority)
	_, _, err := PublishTemporalSpokenSafetyCertification(fixture.config(filepath.Join(t.TempDir(), "score.json")))
	if err == nil || !strings.Contains(err.Error(), "projection, policy, and time") {
		t.Fatalf("post-run authority error = %v", err)
	}
}

func TestPublishTemporalSpokenSafetyCertificationCannotPromoteDevelopmentControls(t *testing.T) {
	fixture := newTemporalSpokenSafetyCertificationFixture(t)
	fixture.authority.ChallengeKind = TemporalSpokenSafetyChallengeDevelopment
	writeTemporalSpokenSafetyJSON(t, fixture.authorityPath, fixture.authority)
	report, _, err := PublishTemporalSpokenSafetyCertification(fixture.config(filepath.Join(t.TempDir(), "score.json")))
	if err != nil {
		t.Fatal(err)
	}
	if report.CertificationStatus != TemporalSpokenSafetyDiagnosticPassed || report.ProductionAdmissionAllowed {
		t.Fatalf("development report = %+v", report)
	}
}

type temporalSpokenSafetyCertificationFixture struct {
	projection     TemporalSpokenSafetyReport
	authority      TemporalSpokenSafetyChallengeAuthority
	projectionPath string
	authorityPath  string
	scoredAt       time.Time
}

func newTemporalSpokenSafetyCertificationFixture(t *testing.T) *temporalSpokenSafetyCertificationFixture {
	t.Helper()
	root := t.TempDir()
	projectedAt := time.Date(2026, 9, 2, 16, 0, 0, 0, time.UTC)
	projection := TemporalSpokenSafetyReport{
		SchemaVersion: TemporalSpokenSafetySchemaVersion, ContractVersion: TemporalSpokenSafetyContractVersion, ProjectedAt: projectedAt,
		CorpusManifestSHA256: strings.Repeat("1", 64), PacketsSHA256: strings.Repeat("2", 64),
		EvidenceManifestSHA256: strings.Repeat("3", 64), EvidencePrivateMapSHA256: strings.Repeat("4", 64),
		TranscriptSetSHA256: strings.Repeat("5", 64), TranscriptFileSHA256: strings.Repeat("6", 64),
		StructureManifestSHA256: strings.Repeat("7", 64), StructureAuthoritySHA256: strings.Repeat("8", 64),
		PolicySHA256: strings.Repeat("9", 64), PolicyID: "policy-fixture-v1",
		Engine:        fillerbakeoff.TranscriptEngineIdentity{Provider: "whisper.cpp", ImplementationVersion: "fixture-v1", BinarySHA256: strings.Repeat("a", 64), Model: "model.bin", ModelSHA256: strings.Repeat("b", 64)},
		CorpusSources: 63, Sources: 63, CompleteTranscriptSources: 63, ProhibitedSources: 59, NoSignalObservedSources: 4,
		StructureCases: 1, NoSignalObservedCases: 1, CertificationStatus: temporalSpokenSafetyCertificationNotRun,
		NextAction: "run_source_disjoint_spoken_safety_certification_before_admission",
	}
	sourceSHA := make([]string, 0, 63)
	for index := 0; index < 63; index++ {
		id := fmt.Sprintf("source-%024x", index)
		contentSHA := hashBytes([]byte(fmt.Sprintf("certification-source-%d", index)))
		sourceSHA = append(sourceSHA, contentSHA)
		source := TemporalSpokenSafetySourceDisposition{
			SourceID: id, AuthorityKind: TemporalSpokenSafetySourceCorpus, SourceSHA256: contentSHA,
			PacketSHA256: hashBytes([]byte(fmt.Sprintf("packet-%d", index))), SourceDurationMS: 10_000,
			TranscriptSHA256: hashBytes([]byte(fmt.Sprintf("transcript-%d", index))), AudioSHA256: hashBytes([]byte(fmt.Sprintf("audio-%d", index))), AudioDurationMS: 10_000,
			Disposition: TemporalSpokenSafetyDispositionNoSignal,
		}
		if index < temporalSpokenSafetyMinimumPositiveFamilies {
			source.Disposition = TemporalSpokenSafetyDispositionProhibited
			source.Matches = []TemporalSpokenSafetyMatch{{RuleID: "rule-000102030405060708090a0b", Class: TemporalSpokenSafetyMatchProhibited, StartMS: 100, EndMS: 900}}
		}
		projection.SourceDispositions = append(projection.SourceDispositions, source)
	}
	projection.CaseDispositions = []TemporalSpokenSafetyCaseDisposition{{EvidenceAlias: "case-a", SourceIDs: []string{projection.SourceDispositions[62].SourceID}, Disposition: TemporalSpokenSafetyDispositionNoSignal}}
	projectionPath := filepath.Join(root, "projection.json")
	writeTemporalSpokenSafetyJSON(t, projectionPath, projection)
	authority := TemporalSpokenSafetyChallengeAuthority{
		SchemaVersion: TemporalSpokenSafetyCertificationSchemaVersion, ContractVersion: TemporalSpokenSafetyCertificationContractVersion,
		AuthoredAt: projectedAt.Add(-time.Hour), ChallengeKind: TemporalSpokenSafetyChallengeCertification,
		CorpusManifestSHA256: projection.CorpusManifestSHA256, PolicySHA256: projection.PolicySHA256,
	}
	positiveSlices := temporalSpokenSafetyRequiredPositiveSlices()
	for index := 0; index < temporalSpokenSafetyMinimumPositiveFamilies; index++ {
		authority.Cases = append(authority.Cases, TemporalSpokenSafetyChallengeAuthorityCase{
			Alias: fmt.Sprintf("sc-%024x", index), SourceSHA256: sourceSHA[index], SourceFamilyID: fmt.Sprintf("family-%024x", index),
			Label: TemporalSpokenSafetyChallengePositive, Locale: "en-US", Slices: []string{positiveSlices[index%len(positiveSlices)]},
			PositiveIntervals: []TemporalSpokenSafetyPositiveInterval{{RuleID: "rule-000102030405060708090a0b", StartMS: 100, EndMS: 900}},
		})
	}
	cleanSlices := []string{TemporalSpokenSafetySliceMusicOnly, TemporalSpokenSafetySliceNearMatch, TemporalSpokenSafetySliceTargetLocale, TemporalSpokenSafetySliceWordless}
	for offset, slice := range cleanSlices {
		index := temporalSpokenSafetyMinimumPositiveFamilies + offset
		authority.Cases = append(authority.Cases, TemporalSpokenSafetyChallengeAuthorityCase{
			Alias: fmt.Sprintf("sc-%024x", index), SourceSHA256: sourceSHA[index], SourceFamilyID: fmt.Sprintf("family-%024x", index),
			Label: TemporalSpokenSafetyChallengeClean, Locale: "en-US", Slices: []string{slice},
		})
	}
	authorityPath := filepath.Join(root, "authority.json")
	writeTemporalSpokenSafetyJSON(t, authorityPath, authority)
	return &temporalSpokenSafetyCertificationFixture{projection: projection, authority: authority, projectionPath: projectionPath, authorityPath: authorityPath, scoredAt: projectedAt.Add(time.Hour)}
}

func (fixture *temporalSpokenSafetyCertificationFixture) config(output string) TemporalSpokenSafetyCertificationConfig {
	return TemporalSpokenSafetyCertificationConfig{AuthorityPath: fixture.authorityPath, SpokenSafetyReportPath: fixture.projectionPath, ScoredAt: fixture.scoredAt, OutputPath: output}
}

func (fixture *temporalSpokenSafetyCertificationFixture) rewriteProjection(t *testing.T) {
	t.Helper()
	if err := os.Remove(fixture.projectionPath); err != nil {
		t.Fatal(err)
	}
	writeTemporalSpokenSafetyJSON(t, fixture.projectionPath, fixture.projection)
}
