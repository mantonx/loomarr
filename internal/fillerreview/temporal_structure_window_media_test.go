package fillerreview

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/filler"
	"github.com/loomarr/loomarr/internal/fillerstructuremedia"
	"github.com/loomarr/loomarr/internal/fillerstructurewindow"
)

func TestBuildTemporalStructureWindowCorpusMediaPublishesExactBlindedSourcesAndTruth(t *testing.T) {
	config, media := temporalStructureWindowMediaFixture(t, filepath.Join(t.TempDir(), "rendered"))
	result, err := BuildTemporalStructureWindowCorpusMedia(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	if result.Cases != TemporalStructureWindowCorpusCases || !reviewSHA256(result.PublicManifestSHA256) || !reviewSHA256(result.PrivateAuthoritySHA256) {
		t.Fatalf("result = %+v", result)
	}
	if media.decodeCalls != TemporalStructureWindowCorpusCases {
		t.Fatalf("decode calls = %d", media.decodeCalls)
	}
	manifestPath := filepath.Join(config.OutputDir, "public", "manifest.json")
	authorityPath := filepath.Join(config.OutputDir, "private", "authority.json")
	manifest, authority, manifestSHA, authoritySHA, err := LoadTemporalStructureWindowCorpusMedia(manifestPath, authorityPath, TemporalStructureWindowCorpusCases)
	if err != nil {
		t.Fatal(err)
	}
	if manifestSHA != result.PublicManifestSHA256 || authoritySHA != result.PrivateAuthoritySHA256 ||
		manifest.TrainingAllowed || manifest.ProductionAdmissionAllowed || authority.TrainingAllowed || authority.ProductionAllowed {
		t.Fatalf("published identities or dispositions drifted: manifest=%+v authority=%+v", manifest, authority)
	}
	planByID := make(map[string]TemporalStructureWindowCorpusCase, len(authority.CorpusPlan.Cases))
	for _, item := range authority.CorpusPlan.Cases {
		planByID[item.ID] = item
	}
	publicByAlias := make(map[string]TemporalStructureWindowMediaPublicCase, len(manifest.Cases))
	for _, item := range manifest.Cases {
		publicByAlias[item.Alias] = item
		fullPath := filepath.Join(config.OutputDir, "public", filepath.FromSlash(item.Source.Path))
		digest, size, err := filler.FileSHA256(fullPath)
		if err != nil {
			t.Fatal(err)
		}
		clipHash, err := filler.ClipID(fullPath)
		if err != nil {
			t.Fatal(err)
		}
		if digest != item.Source.SHA256 || size != item.Source.Bytes || clipHash != item.Source.ClipHash ||
			item.Source.DurationMs != item.Plan.Source.DurationMS || item.Plan.Profile != fillerstructurewindow.CanonicalProfile() {
			t.Fatalf("public source or plan drifted: %+v", item)
		}
	}
	patterns := make(map[string]int)
	for _, item := range authority.Cases {
		publicCase := publicByAlias[item.Alias]
		planCase := planByID[item.CaseID]
		patterns[planCase.Pattern]++
		if item.Truth[len(item.Truth)-1].EndMS != publicCase.Source.DurationMs {
			t.Fatalf("case truth does not cover source: %+v", item)
		}
		if planCase.Pattern != TemporalStructureWindowPatternCrossingSeam && item.ObservedTargetBoundaryMS != planCase.TargetBoundaryMS {
			t.Fatalf("observed boundary = %d, planned = %d", item.ObservedTargetBoundaryMS, planCase.TargetBoundaryMS)
		}
	}
	for _, count := range patterns {
		if count != TemporalStructureWindowCorpusCasesPerPattern && count != TemporalStructureWindowCorpusEdgeCases {
			t.Fatalf("pattern counts = %v", patterns)
		}
	}
	publicRaw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	for caseID := range planByID {
		if strings.Contains(string(publicRaw), caseID) {
			t.Fatalf("public manifest leaks case id %q", caseID)
		}
	}

	repeatConfig, _ := temporalStructureWindowMediaFixtureFromExisting(t, config, filepath.Join(t.TempDir(), "repeat"))
	repeat, err := BuildTemporalStructureWindowCorpusMedia(t.Context(), repeatConfig)
	if err != nil {
		t.Fatal(err)
	}
	if repeat != result {
		t.Fatalf("repeat result differs: first=%+v repeat=%+v", result, repeat)
	}
	for _, relative := range []string{"public/manifest.json", "private/authority.json"} {
		first, err := os.ReadFile(filepath.Join(config.OutputDir, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatal(err)
		}
		second, err := os.ReadFile(filepath.Join(repeatConfig.OutputDir, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(first, second) {
			t.Fatalf("repeated %s bytes differ", relative)
		}
	}
}

func TestBuildTemporalStructureWindowCorpusMediaFailsAtomicallyAndRejectsTamper(t *testing.T) {
	t.Run("decode failure", func(t *testing.T) {
		output := filepath.Join(t.TempDir(), "rendered")
		config, media := temporalStructureWindowMediaFixture(t, output)
		media.decodeErrAt = 3
		if _, err := BuildTemporalStructureWindowCorpusMedia(t.Context(), config); err == nil || !strings.Contains(err.Error(), "fixture decode failure") {
			t.Fatalf("decode error = %v", err)
		}
		if _, err := os.Lstat(output); !os.IsNotExist(err) {
			t.Fatalf("failed build published output: %v", err)
		}
	})

	t.Run("rendered source tamper", func(t *testing.T) {
		config, _ := temporalStructureWindowMediaFixture(t, filepath.Join(t.TempDir(), "rendered"))
		if _, err := BuildTemporalStructureWindowCorpusMedia(t.Context(), config); err != nil {
			t.Fatal(err)
		}
		manifest := readStrictTestJSON[TemporalStructureWindowMediaManifest](t, filepath.Join(config.OutputDir, "public", "manifest.json"))
		path := filepath.Join(config.OutputDir, "public", filepath.FromSlash(manifest.Cases[0].Source.Path))
		if err := os.WriteFile(path, []byte("tampered source"), 0o640); err != nil {
			t.Fatal(err)
		}
		_, _, _, _, err := LoadTemporalStructureWindowCorpusMedia(
			filepath.Join(config.OutputDir, "public", "manifest.json"),
			filepath.Join(config.OutputDir, "private", "authority.json"),
			TemporalStructureWindowCorpusCases,
		)
		if err == nil || !strings.Contains(err.Error(), "source is not the declared regular file") {
			t.Fatalf("tamper error = %v", err)
		}
	})
}

type fakeTemporalStructureWindowCorpusMedia struct {
	base        *fakeTemporalStructureMedia
	decodeCalls int
	decodeErrAt int
}

func (media *fakeTemporalStructureWindowCorpusMedia) Identity() TemporalTruthMediaIdentity {
	return media.base.Identity()
}

func (media *fakeTemporalStructureWindowCorpusMedia) Probe(ctx context.Context, path string) (TemporalTruthVideoInfo, error) {
	return media.base.Probe(ctx, path)
}

func (media *fakeTemporalStructureWindowCorpusMedia) Render(ctx context.Context, segments []TemporalStructureRenderSegment, output string) (TemporalStructureRenderResult, error) {
	result, err := media.base.Render(ctx, segments, output)
	if err != nil {
		return TemporalStructureRenderResult{}, err
	}
	profile := fillerstructuremedia.CanonicalProfile()
	result.Video.Width, result.Video.Height = profile.Width, profile.Height
	return result, nil
}

func (media *fakeTemporalStructureWindowCorpusMedia) Decode(_ context.Context, path string) error {
	media.decodeCalls++
	if _, err := os.Stat(path); err != nil {
		return err
	}
	if media.decodeErrAt > 0 && media.decodeCalls == media.decodeErrAt {
		return errors.New("fixture decode failure")
	}
	return nil
}

func temporalStructureWindowMediaFixture(t *testing.T, output string) (TemporalStructureWindowMediaConfig, *fakeTemporalStructureWindowCorpusMedia) {
	t.Helper()
	fixture := newTemporalStructureHoldoutFixture(t)
	holdout := filepath.Join(t.TempDir(), "holdout")
	if _, err := BuildTemporalStructureHoldoutPlan(fixture.config(holdout)); err != nil {
		t.Fatal(err)
	}
	planRoot := filepath.Join(t.TempDir(), "plan")
	seed := "window-media-seed"
	if _, err := BuildTemporalStructureWindowCorpusPlan(TemporalStructureWindowCorpusConfig{
		HoldoutAuthoringPath: filepath.Join(holdout, "authoring.json"), HoldoutReceiptPath: filepath.Join(holdout, "receipt.json"),
		Seed: seed, PlannedAt: fixture.plannedAt.Add(time.Hour), OutputDir: planRoot,
	}); err != nil {
		t.Fatal(err)
	}
	config := TemporalStructureWindowMediaConfig{
		PlanPath: filepath.Join(planRoot, "plan.json"), HoldoutAuthoringPath: filepath.Join(holdout, "authoring.json"),
		HoldoutReceiptPath: filepath.Join(holdout, "receipt.json"), SourceRoot: fixture.root, Seed: seed,
		RenderedAt: fixture.plannedAt.Add(2 * time.Hour), OutputDir: output,
	}
	return temporalStructureWindowMediaFixtureFromExisting(t, config, output)
}

func temporalStructureWindowMediaFixtureFromExisting(t *testing.T, config TemporalStructureWindowMediaConfig, output string) (TemporalStructureWindowMediaConfig, *fakeTemporalStructureWindowCorpusMedia) {
	t.Helper()
	authoring := readStrictTestJSON[TemporalStructureChallengeAuthoring](t, config.HoldoutAuthoringPath)
	base := &fakeTemporalStructureMedia{durationByPath: make(map[string]int64)}
	for _, source := range authoring.Sources {
		base.durationByPath[filepath.Join(config.SourceRoot, filepath.FromSlash(source.Path))] = source.DurationMS
	}
	media := &fakeTemporalStructureWindowCorpusMedia{base: base}
	config.OutputDir, config.Media = output, media
	return config, media
}
