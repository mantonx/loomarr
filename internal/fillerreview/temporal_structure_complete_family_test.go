package fillerreview

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/filler"
	"github.com/loomarr/loomarr/internal/fillerstructure"
)

const temporalStructureFamilySnapshotSHA256 = "9999999999999999999999999999999999999999999999999999999999999999"

type fakeTemporalStructureCompletePreparer struct {
	root       string
	profileSHA string
	calls      int
}

func (p *fakeTemporalStructureCompletePreparer) Prepare(_ context.Context, input filler.StructureAssessmentSource) (filler.StructureAssessmentMedia, error) {
	p.calls++
	return filler.StructureAssessmentMedia{
		Source: input.Source,
		Assessment: fillerstructure.AssessmentMedia{
			SHA256: input.Source.ClipHash, Bytes: min(input.Source.Bytes, int64(32<<20)), DurationMS: input.Source.DurationMs,
			ProfileSHA256: p.profileSHA, LineageSHA256: input.Source.SHA256,
		},
		FullPath: filepath.Join(p.root, input.Source.ClipHash+".mp4"),
	}, nil
}

type fakeTemporalStructureCompleteFamily struct {
	profile fillerstructure.AssessorProfile
	calls   int
	failAt  int
}

func (f *fakeTemporalStructureCompleteFamily) Profile() fillerstructure.AssessorProfile {
	return f.profile
}

func (f *fakeTemporalStructureCompleteFamily) AssessWithEvidence(_ context.Context, media filler.StructureAssessmentMedia) (fillerstructure.RecordedAssessment, error) {
	f.calls++
	if f.failAt > 0 && f.calls == f.failAt {
		return fillerstructure.RecordedAssessment{}, fmt.Errorf("fixture complete family failure")
	}
	duration := media.Source.DurationMs
	source := fillerstructure.Source{SHA256: media.Source.SHA256, Bytes: media.Source.Bytes, DurationMS: duration}
	structured := fmt.Sprintf(`{"segments":[{"endMs":%d,"role":"commercial","decisiveAtMs":[1],"reason":"fixture"}]}`, duration)
	return fillerstructure.NewAssessmentRecord(fillerstructure.AssessmentRecordInput{
		Source: source, Media: media.Assessment, Assessor: f.profile,
		MetadataSnapshotSHA256: temporalStructureFamilySnapshotSHA256,
		PromptSHA256:           fillerstructure.DirectVideoPromptSHA256(duration), SchemaSHA256: fillerstructure.DirectVideoSchemaSHA256(duration),
		RequestSHA256: media.Source.SHA256, RawResponse: []byte(`{"id":"fixture"}`), StructuredOutput: structured,
		ResolvedProvider: "openrouter", ResolvedModel: "provider/model-2026",
		UpstreamProvider: "Provider", UpstreamProviderSlug: "provider", GenerationID: "fixture-generation",
		Tokens:           fillerstructure.AssessmentTokenUsage{Prompt: 100, Completion: 20, Video: 80},
		RequestedNanoUSD: 1_000, ReservedNanoUSD: 1_000, ChargedAmountUSD: "0.0000005",
		ChargedNanoUSD: 500, AccountedNanoUSD: 500, ChargeKnown: true,
		State: fillerstructure.AssessmentRecordAccepted, AssessedAt: time.Date(2026, 9, 13, 4, 0, f.calls, 0, time.UTC),
	})
}

func TestRunTemporalStructureCompleteFamilyPublishesCompleteTruthBlindEvidence(t *testing.T) {
	config, _ := temporalStructureWindowSuiteFixture(t, filepath.Join(t.TempDir(), "suite"))
	manifest := readStrictTestJSON[TemporalStructureWindowSetManifest](t, config.WindowSetManifestPath)
	preparer := &fakeTemporalStructureCompletePreparer{root: t.TempDir(), profileSHA: manifest.AssessmentMediaProfileSHA256}
	family := &fakeTemporalStructureCompleteFamily{profile: temporalStructureCompleteFamilyProfile("family-a")}
	completedAt := time.Date(2026, 9, 13, 5, 0, 0, 0, time.UTC)
	result, err := RunTemporalStructureCompleteFamily(t.Context(), TemporalStructureCompleteFamilyConfig{
		WindowSetManifestPath: config.WindowSetManifestPath, ExpectedCases: TemporalStructureWindowCorpusCases,
		CapabilitySnapshotSHA256: temporalStructureFamilySnapshotSHA256,
		Preparer:                 preparer, Family: family, Now: func() time.Time { return completedAt },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Cases) != TemporalStructureWindowCorpusCases || result.CallRecords != TemporalStructureWindowCorpusCases ||
		result.CapabilitySnapshotSHA256 != temporalStructureFamilySnapshotSHA256 ||
		result.ProviderRequests != TemporalStructureWindowCorpusCases || result.ChargedNanoUSD != 500*TemporalStructureWindowCorpusCases ||
		result.AccountedNanoUSD != result.ChargedNanoUSD || result.UnknownChargeReservations != 0 ||
		preparer.calls != TemporalStructureWindowCorpusCases || family.calls != TemporalStructureWindowCorpusCases ||
		result.TrainingAllowed || result.ProductionAdmissionAllowed || result.SHA256 != temporalStructureCompleteFamilySHA256(result) {
		t.Fatalf("result=%+v prepares=%d calls=%d", result, preparer.calls, family.calls)
	}
	output := filepath.Join(t.TempDir(), "complete-family.json")
	fileSHA, err := PublishTemporalStructureCompleteFamilyResult(output, config.WindowSetManifestPath, result)
	if err != nil {
		t.Fatal(err)
	}
	loaded, loadedFileSHA, err := LoadTemporalStructureCompleteFamilyResult(output, config.WindowSetManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded, result) || loadedFileSHA != fileSHA || !reviewSHA256(fileSHA) {
		t.Fatalf("loaded result or file digest drifted")
	}
}

func TestRunTemporalStructureCompleteFamilyReturnsNoPartialResult(t *testing.T) {
	config, _ := temporalStructureWindowSuiteFixture(t, filepath.Join(t.TempDir(), "suite"))
	manifest := readStrictTestJSON[TemporalStructureWindowSetManifest](t, config.WindowSetManifestPath)
	preparer := &fakeTemporalStructureCompletePreparer{root: t.TempDir(), profileSHA: manifest.AssessmentMediaProfileSHA256}
	family := &fakeTemporalStructureCompleteFamily{profile: temporalStructureCompleteFamilyProfile("family-a"), failAt: 3}
	result, err := RunTemporalStructureCompleteFamily(t.Context(), TemporalStructureCompleteFamilyConfig{
		WindowSetManifestPath: config.WindowSetManifestPath, ExpectedCases: TemporalStructureWindowCorpusCases,
		CapabilitySnapshotSHA256: temporalStructureFamilySnapshotSHA256,
		Preparer:                 preparer, Family: family, Now: time.Now,
	})
	if err == nil || !strings.Contains(err.Error(), "fixture complete family failure") ||
		!reflect.DeepEqual(result, TemporalStructureCompleteFamilyResult{}) || preparer.calls != 3 || family.calls != 3 {
		t.Fatalf("result=%+v error=%v prepares=%d calls=%d", result, err, preparer.calls, family.calls)
	}
}

func TestValidateTemporalStructureCompleteFamilyResultRejectsAccountingDrift(t *testing.T) {
	config, _ := temporalStructureWindowSuiteFixture(t, filepath.Join(t.TempDir(), "suite"))
	manifest := readStrictTestJSON[TemporalStructureWindowSetManifest](t, config.WindowSetManifestPath)
	result, err := RunTemporalStructureCompleteFamily(t.Context(), TemporalStructureCompleteFamilyConfig{
		WindowSetManifestPath: config.WindowSetManifestPath, ExpectedCases: TemporalStructureWindowCorpusCases,
		CapabilitySnapshotSHA256: temporalStructureFamilySnapshotSHA256,
		Preparer:                 &fakeTemporalStructureCompletePreparer{root: t.TempDir(), profileSHA: manifest.AssessmentMediaProfileSHA256},
		Family:                   &fakeTemporalStructureCompleteFamily{profile: temporalStructureCompleteFamilyProfile("family-a")},
		Now:                      func() time.Time { return time.Date(2026, 9, 13, 5, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	result.AccountedNanoUSD++
	result.SHA256 = temporalStructureCompleteFamilySHA256(result)
	if err := ValidateTemporalStructureCompleteFamilyResult(result); err == nil {
		t.Fatal("complete family result accepted accounting drift")
	}
}

func temporalStructureCompleteFamilyProfile(id string) fillerstructure.AssessorProfile {
	return fillerstructure.AssessorProfile{
		ID: id, Provider: "openrouter", Model: "provider/model", ModelFamily: id,
		ModelDigest: strings.Repeat("a", 64), CapabilitySHA256: strings.Repeat("b", 64),
		PromptVersion: fillerstructure.DirectVideoPromptVersion, EvidenceContract: fillerstructure.AssessmentRecordContractVersion,
	}
}
