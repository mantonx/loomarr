package fillerreview

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/filler"
	"github.com/loomarr/loomarr/internal/fillerstructure"
	"github.com/loomarr/loomarr/internal/fillerstructurewindow"
)

type fakeTemporalStructureWindowFamily struct {
	profile fillerstructure.AssessorProfile
	calls   []string
	paths   [][]string
	failAt  int
}

func (f *fakeTemporalStructureWindowFamily) Profile() fillerstructure.AssessorProfile {
	return f.profile
}

func (f *fakeTemporalStructureWindowFamily) AssessWithEvidence(_ context.Context, prepared filler.StructureAssessmentWindowMediaSet) (filler.StructureWindowFamilyEvidence, error) {
	f.calls = append(f.calls, prepared.Source.SHA256)
	paths := make([]string, len(prepared.Windows))
	for index, window := range prepared.Windows {
		paths[index] = window.FullPath
	}
	f.paths = append(f.paths, paths)
	if f.failAt > 0 && len(f.calls) == f.failAt {
		return filler.StructureWindowFamilyEvidence{}, errors.New("fixture family failure")
	}
	assessments := make([]fillerstructurewindow.Assessment, 0, len(prepared.Windows))
	recorded := make([]fillerstructurewindow.RecordedAssessment, 0, len(prepared.Windows))
	for _, window := range prepared.Windows {
		durationMS := window.Window.MediaEndMS - window.Window.MediaStartMS
		structured := fmt.Sprintf(`{"segments":[{"endMs":%d,"role":"commercial","decisiveAtMs":[1000],"reason":"fixture offer"}]}`, durationMS)
		item, err := fillerstructurewindow.NewRecordedAssessment(fillerstructurewindow.CallRecordInput{
			MediaSet: prepared.Authority, WindowOrdinal: window.Window.Ordinal, Assessor: f.profile,
			MetadataSnapshotSHA256: temporalStructureFamilySnapshotSHA256,
			PromptSHA256:           fillerstructurewindow.DirectVideoPromptSHA256(durationMS),
			SchemaSHA256:           fillerstructurewindow.DirectVideoSchemaSHA256(durationMS),
			RequestSHA256:          hashBytes([]byte(fmt.Sprintf("%s:%s:%d", prepared.Source.SHA256, f.profile.ID, window.Window.Ordinal))),
			RawResponse:            []byte("fixture provider response"), StructuredOutput: structured,
			ResolvedProvider: "openrouter", ResolvedModel: "provider/model", UpstreamProvider: "Fixture Provider",
			UpstreamProviderSlug: "fixture/provider", GenerationID: fmt.Sprintf("generation-%d", window.Window.Ordinal),
			Tokens:           fillerstructure.AssessmentTokenUsage{Prompt: 100, Completion: 20, Video: 80},
			RequestedNanoUSD: 1_000, ReservedNanoUSD: 1_000, ChargedAmountUSD: "0.0000001",
			ChargedNanoUSD: 100, AccountedNanoUSD: 100, ChargeKnown: true,
			State:      fillerstructure.AssessmentRecordAccepted,
			AssessedAt: time.Date(2026, time.September, 13, 1, 0, window.Window.Ordinal, 0, time.UTC),
		})
		if err != nil {
			return filler.StructureWindowFamilyEvidence{}, err
		}
		recorded = append(recorded, item)
		assessments = append(assessments, item.Assessment)
	}
	stitched, err := fillerstructurewindow.Stitch(prepared.Authority, assessments, 2_000)
	if err != nil {
		return filler.StructureWindowFamilyEvidence{}, err
	}
	return filler.NewStructureWindowFamilyEvidence(recorded, stitched)
}

func TestRunTemporalStructureWindowFamilyUsesOnlyCompletePublicMediaSets(t *testing.T) {
	suiteConfig, _ := temporalStructureWindowSuiteFixture(t, filepath.Join(t.TempDir(), "suite"))
	manifest := readStrictTestJSON[TemporalStructureWindowSetManifest](t, suiteConfig.WindowSetManifestPath)
	windows := 0
	for _, item := range manifest.Cases {
		windows += len(item.Windows)
	}
	family := &fakeTemporalStructureWindowFamily{profile: temporalStructureWindowFamilyProfile("family-a")}
	completedAt := time.Date(2026, time.September, 13, 2, 0, 0, 0, time.UTC)
	result, err := RunTemporalStructureWindowFamily(t.Context(), TemporalStructureWindowFamilyConfig{
		WindowSetManifestPath:    suiteConfig.WindowSetManifestPath,
		ExpectedCases:            TemporalStructureWindowCorpusCases,
		CapabilitySnapshotSHA256: temporalStructureFamilySnapshotSHA256,
		Family:                   family,
		Now:                      func() time.Time { return completedAt },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateTemporalStructureWindowFamilyResult(result); err != nil {
		t.Fatal(err)
	}
	if result.CompletedAt != completedAt || result.Assessor != family.profile ||
		result.CapabilitySnapshotSHA256 != temporalStructureFamilySnapshotSHA256 ||
		len(result.Cases) != TemporalStructureWindowCorpusCases || len(family.calls) != len(result.Cases) ||
		result.CallRecords != windows || result.ProviderRequests != windows ||
		result.ChargedNanoUSD != int64(windows*100) || result.AccountedNanoUSD != int64(windows*100) ||
		result.ProductionAdmissionAllowed || result.TrainingAllowed || !reviewSHA256(result.SHA256) {
		t.Fatalf("cases=%d records=%d requests=%d charged=%d accounted=%d calls=%d", len(result.Cases), result.CallRecords, result.ProviderRequests, result.ChargedNanoUSD, result.AccountedNanoUSD, len(family.calls))
	}
	for index, item := range result.Cases {
		if item.Alias != manifest.Cases[index].Alias || item.Evidence.Stitch.MediaSet.SHA256 != manifest.Cases[index].MediaSet.SHA256 ||
			item.Evidence.Stitch.Assessor != family.profile || filler.ValidateStructureWindowFamilyEvidence(item.Evidence) != nil ||
			family.calls[index] != manifest.Cases[index].Source.SHA256 {
			t.Fatalf("case %d=%+v call=%s", index, item, family.calls[index])
		}
		for ordinal, path := range family.paths[index] {
			want := filepath.Join(filepath.Dir(suiteConfig.WindowSetManifestPath), filepath.FromSlash(manifest.Cases[index].Windows[ordinal].Path))
			if path != want {
				t.Fatalf("case %d window %d path=%q want=%q", index, ordinal, path, want)
			}
		}
	}
}

func TestTemporalStructureWindowFamilyArtifactRoundTripsAgainstManifest(t *testing.T) {
	suiteConfig, _ := temporalStructureWindowSuiteFixture(t, filepath.Join(t.TempDir(), "suite"))
	family := &fakeTemporalStructureWindowFamily{profile: temporalStructureWindowFamilyProfile("family-a")}
	result, err := RunTemporalStructureWindowFamily(t.Context(), TemporalStructureWindowFamilyConfig{
		WindowSetManifestPath:    suiteConfig.WindowSetManifestPath,
		ExpectedCases:            TemporalStructureWindowCorpusCases,
		CapabilitySnapshotSHA256: temporalStructureFamilySnapshotSHA256,
		Family:                   family,
		Now:                      func() time.Time { return time.Date(2026, time.September, 13, 2, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "family.json")
	digest, err := PublishTemporalStructureWindowFamilyResult(path, suiteConfig.WindowSetManifestPath, result)
	if err != nil {
		t.Fatal(err)
	}
	loaded, fileDigest, err := LoadTemporalStructureWindowFamilyResult(path, suiteConfig.WindowSetManifestPath)
	if err != nil || !reflect.DeepEqual(loaded, result) || digest != fileDigest || !reviewSHA256(digest) {
		t.Fatalf("loaded=%+v digest=%q fileDigest=%q error=%v", loaded, digest, fileDigest, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("artifact mode=%v", info.Mode())
	}
	if _, err := PublishTemporalStructureWindowFamilyResult(path, suiteConfig.WindowSetManifestPath, result); err == nil {
		t.Fatal("expected immutable publication failure")
	}
}

func TestValidateTemporalStructureWindowFamilyResultRejectsDrift(t *testing.T) {
	suiteConfig, _ := temporalStructureWindowSuiteFixture(t, filepath.Join(t.TempDir(), "suite"))
	manifest, manifestSHA, err := LoadTemporalStructureWindowSetPublic(suiteConfig.WindowSetManifestPath, TemporalStructureWindowCorpusCases)
	if err != nil {
		t.Fatal(err)
	}
	result, err := RunTemporalStructureWindowFamily(t.Context(), TemporalStructureWindowFamilyConfig{
		WindowSetManifestPath:    suiteConfig.WindowSetManifestPath,
		ExpectedCases:            TemporalStructureWindowCorpusCases,
		CapabilitySnapshotSHA256: temporalStructureFamilySnapshotSHA256,
		Family:                   &fakeTemporalStructureWindowFamily{profile: temporalStructureWindowFamilyProfile("family-a")},
		Now:                      func() time.Time { return time.Date(2026, time.September, 13, 2, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	mutations := map[string]func(*TemporalStructureWindowFamilyResult){
		"manifest": func(value *TemporalStructureWindowFamilyResult) {
			value.WindowSetManifestSHA256 = strings.Repeat("f", 64)
		},
		"snapshot": func(value *TemporalStructureWindowFamilyResult) { value.CapabilitySnapshotSHA256 = "" },
		"alias":    func(value *TemporalStructureWindowFamilyResult) { value.Cases[0].Alias = value.Cases[1].Alias },
		"assessor": func(value *TemporalStructureWindowFamilyResult) {
			value.Cases[0].Evidence.Stitch.Assessor = temporalStructureWindowFamilyProfile("family-b")
		},
		"training": func(value *TemporalStructureWindowFamilyResult) { value.TrainingAllowed = true },
		"accounting": func(value *TemporalStructureWindowFamilyResult) {
			value.AccountedNanoUSD++
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			changed := result
			changed.Cases = append([]TemporalStructureWindowFamilyCase(nil), result.Cases...)
			mutate(&changed)
			changed.SHA256 = temporalStructureWindowFamilySHA256(changed)
			if err := validateTemporalStructureWindowFamilyResultAgainstManifest(changed, manifest, manifestSHA); err == nil {
				t.Fatal("expected validation failure")
			}
		})
	}
}

func TestRunTemporalStructureWindowFamilyReturnsNoPartialResult(t *testing.T) {
	suiteConfig, _ := temporalStructureWindowSuiteFixture(t, filepath.Join(t.TempDir(), "suite"))
	family := &fakeTemporalStructureWindowFamily{profile: temporalStructureWindowFamilyProfile("family-a"), failAt: 3}
	result, err := RunTemporalStructureWindowFamily(t.Context(), TemporalStructureWindowFamilyConfig{
		WindowSetManifestPath:    suiteConfig.WindowSetManifestPath,
		ExpectedCases:            TemporalStructureWindowCorpusCases,
		CapabilitySnapshotSHA256: temporalStructureFamilySnapshotSHA256,
		Family:                   family,
		Now:                      func() time.Time { return time.Date(2026, time.September, 13, 2, 0, 0, 0, time.UTC) },
	})
	if err == nil || !strings.Contains(err.Error(), "fixture family failure") ||
		!reflect.DeepEqual(result, TemporalStructureWindowFamilyResult{}) || len(family.calls) != 3 {
		t.Fatalf("result=%+v error=%v calls=%d", result, err, len(family.calls))
	}
}

func temporalStructureWindowFamilyProfile(id string) fillerstructure.AssessorProfile {
	return fillerstructure.AssessorProfile{
		ID: id, Provider: "openrouter", Model: "provider/model", ModelFamily: id,
		ModelDigest: strings.Repeat("a", 64), CapabilitySHA256: strings.Repeat("b", 64),
		PromptVersion: fillerstructurewindow.DirectVideoPromptVersion, EvidenceContract: fillerstructurewindow.CallRecordContractVersion,
	}
}
