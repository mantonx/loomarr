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

func TestBuildTemporalStructureWindowSetUsesProductionPlanAndPublishesCompleteMedia(t *testing.T) {
	corpusConfig, _ := temporalStructureWindowMediaFixture(t, filepath.Join(t.TempDir(), "corpus"))
	if _, err := BuildTemporalStructureWindowCorpusMedia(t.Context(), corpusConfig); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "window-set")
	config, factory := temporalStructureWindowSetFixture(corpusConfig, output)
	result, err := BuildTemporalStructureWindowSet(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	if result.Cases != TemporalStructureWindowCorpusCases || result.Windows <= result.Cases ||
		!reviewSHA256(result.PublicManifestSHA256) || !reviewSHA256(result.PrivateAuthoritySHA256) || factory.prepareCalls != result.Cases {
		t.Fatalf("result=%+v prepareCalls=%d", result, factory.prepareCalls)
	}
	manifestPath := filepath.Join(output, "public", "manifest.json")
	authorityPath := filepath.Join(output, "private", "authority.json")
	corpusManifestPath := filepath.Join(corpusConfig.OutputDir, "public", "manifest.json")
	corpusAuthorityPath := filepath.Join(corpusConfig.OutputDir, "private", "authority.json")
	manifest, authority, manifestSHA, authoritySHA, err := LoadTemporalStructureWindowSet(
		manifestPath, authorityPath, corpusManifestPath, corpusAuthorityPath,
	)
	if err != nil {
		t.Fatal(err)
	}
	if manifestSHA != result.PublicManifestSHA256 || authoritySHA != result.PrivateAuthoritySHA256 ||
		manifest.TrainingAllowed || manifest.ProductionAdmissionAllowed || authority.TrainingAllowed || authority.ProductionAllowed {
		t.Fatalf("window set identity or disposition drifted: manifest=%+v authority=%+v", manifest, authority)
	}
	totalWindows := 0
	for _, item := range manifest.Cases {
		if err := fillerstructurewindow.ValidateMediaSet(item.MediaSet); err != nil {
			t.Fatal(err)
		}
		if item.MediaSet.Plan.Source.SHA256 != item.Source.SHA256 || len(item.Windows) != len(item.MediaSet.Windows) {
			t.Fatalf("case drifted from source or media set: %+v", item)
		}
		for ordinal, window := range item.Windows {
			fullPath := filepath.Join(output, "public", filepath.FromSlash(window.Path))
			digest, size, err := filler.FileSHA256(fullPath)
			if err != nil {
				t.Fatal(err)
			}
			if window.Ordinal != ordinal || digest != item.MediaSet.Windows[ordinal].Media.SHA256 || size != item.MediaSet.Windows[ordinal].Media.Bytes {
				t.Fatalf("window %d does not bind its media: %+v", ordinal, window)
			}
		}
		totalWindows += len(item.Windows)
	}
	if totalWindows != result.Windows {
		t.Fatalf("total windows = %d, result = %d", totalWindows, result.Windows)
	}
	publicRaw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range authority.Cases {
		if strings.Contains(string(publicRaw), item.CaseID) {
			t.Fatalf("public window set leaks case id %q", item.CaseID)
		}
	}

	repeatOutput := filepath.Join(t.TempDir(), "repeat")
	repeatConfig, _ := temporalStructureWindowSetFixture(corpusConfig, repeatOutput)
	repeat, err := BuildTemporalStructureWindowSet(t.Context(), repeatConfig)
	if err != nil {
		t.Fatal(err)
	}
	if repeat != result {
		t.Fatalf("repeat result differs: first=%+v repeat=%+v", result, repeat)
	}
	for _, relative := range []string{"public/manifest.json", "private/authority.json"} {
		first, err := os.ReadFile(filepath.Join(output, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatal(err)
		}
		second, err := os.ReadFile(filepath.Join(repeatOutput, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(first, second) {
			t.Fatalf("repeated %s differs", relative)
		}
	}
}

func TestBuildTemporalStructureWindowSetFailsAtomicallyAndRejectsWindowTamper(t *testing.T) {
	t.Run("preparer failure", func(t *testing.T) {
		corpusConfig, _ := temporalStructureWindowMediaFixture(t, filepath.Join(t.TempDir(), "corpus"))
		if _, err := BuildTemporalStructureWindowCorpusMedia(t.Context(), corpusConfig); err != nil {
			t.Fatal(err)
		}
		output := filepath.Join(t.TempDir(), "window-set")
		config, factory := temporalStructureWindowSetFixture(corpusConfig, output)
		factory.failAt = 3
		if _, err := BuildTemporalStructureWindowSet(t.Context(), config); err == nil || !strings.Contains(err.Error(), "fixture prepare failure") {
			t.Fatalf("preparer error = %v", err)
		}
		if _, err := os.Lstat(output); !os.IsNotExist(err) {
			t.Fatalf("failed window set published output: %v", err)
		}
	})

	t.Run("window tamper", func(t *testing.T) {
		corpusConfig, _ := temporalStructureWindowMediaFixture(t, filepath.Join(t.TempDir(), "corpus"))
		if _, err := BuildTemporalStructureWindowCorpusMedia(t.Context(), corpusConfig); err != nil {
			t.Fatal(err)
		}
		output := filepath.Join(t.TempDir(), "window-set")
		config, _ := temporalStructureWindowSetFixture(corpusConfig, output)
		if _, err := BuildTemporalStructureWindowSet(t.Context(), config); err != nil {
			t.Fatal(err)
		}
		manifestPath := filepath.Join(output, "public", "manifest.json")
		manifest := readStrictTestJSON[TemporalStructureWindowSetManifest](t, manifestPath)
		windowPath := filepath.Join(output, "public", filepath.FromSlash(manifest.Cases[0].Windows[0].Path))
		if err := os.WriteFile(windowPath, []byte("tampered window"), 0o640); err != nil {
			t.Fatal(err)
		}
		_, _, _, _, err := LoadTemporalStructureWindowSet(
			manifestPath, filepath.Join(output, "private", "authority.json"),
			filepath.Join(corpusConfig.OutputDir, "public", "manifest.json"),
			filepath.Join(corpusConfig.OutputDir, "private", "authority.json"),
		)
		if err == nil || !strings.Contains(err.Error(), "window 0") {
			t.Fatalf("tamper error = %v", err)
		}
	})
}

type fakeTemporalStructureWindowSetFactory struct {
	prepareCalls int
	failAt       int
}

func (factory *fakeTemporalStructureWindowSetFactory) new(root string) (filler.StructureAssessmentWindowMediaPreparer, error) {
	return &fakeTemporalStructureWindowSetPreparer{root: root, factory: factory}, nil
}

type fakeTemporalStructureWindowSetPreparer struct {
	root    string
	factory *fakeTemporalStructureWindowSetFactory
}

func (preparer *fakeTemporalStructureWindowSetPreparer) PrepareWindows(_ context.Context, input filler.StructureAssessmentSource, plan fillerstructurewindow.Plan) (filler.StructureAssessmentWindowMediaSet, error) {
	preparer.factory.prepareCalls++
	if preparer.factory.failAt > 0 && preparer.factory.prepareCalls == preparer.factory.failAt {
		return filler.StructureAssessmentWindowMediaSet{}, errors.New("fixture prepare failure")
	}
	if input.FullPath != filepath.Join(preparer.root, filepath.FromSlash(input.Source.Path)) || plan.Source.SHA256 != input.Source.SHA256 {
		return filler.StructureAssessmentWindowMediaSet{}, errors.New("fixture source or plan drifted")
	}
	identities := make([]fillerstructure.AssessmentMedia, len(plan.Windows))
	paths := make([]string, len(plan.Windows))
	for ordinal, window := range plan.Windows {
		raw := []byte(fmt.Sprintf("%s:%d:%d", input.Source.SHA256, window.MediaStartMS, window.MediaEndMS))
		digest := hashBytes(raw)
		relative := filepath.Join(filler.MediaAssetRootName, "structure-assessment", "media", digest[:2], digest+".mp4")
		fullPath := filepath.Join(preparer.root, relative)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o750); err != nil {
			return filler.StructureAssessmentWindowMediaSet{}, err
		}
		if err := os.WriteFile(fullPath, raw, 0o640); err != nil {
			return filler.StructureAssessmentWindowMediaSet{}, err
		}
		identities[ordinal] = fillerstructure.AssessmentMedia{
			SHA256: digest, Bytes: int64(len(raw)), DurationMS: window.MediaEndMS - window.MediaStartMS,
			ProfileSHA256: plan.Profile.AssessmentMediaProfileSHA256,
			LineageSHA256: hashBytes([]byte("lineage:" + digest)),
		}
		paths[ordinal] = fullPath
	}
	set, err := fillerstructurewindow.NewMediaSet(plan, identities)
	if err != nil {
		return filler.StructureAssessmentWindowMediaSet{}, err
	}
	result := filler.StructureAssessmentWindowMediaSet{Source: input.Source, Authority: set}
	for ordinal, window := range plan.Windows {
		result.Windows = append(result.Windows, filler.StructureAssessmentWindowMedia{
			Window: window, Media: set.Windows[ordinal], FullPath: paths[ordinal],
		})
	}
	return result, nil
}

func temporalStructureWindowSetFixture(corpus TemporalStructureWindowMediaConfig, output string) (TemporalStructureWindowSetConfig, *fakeTemporalStructureWindowSetFactory) {
	factory := &fakeTemporalStructureWindowSetFactory{}
	return TemporalStructureWindowSetConfig{
		CorpusManifestPath:  filepath.Join(corpus.OutputDir, "public", "manifest.json"),
		CorpusAuthorityPath: filepath.Join(corpus.OutputDir, "private", "authority.json"),
		PreparedAt:          corpus.RenderedAt.Add(time.Hour), OutputDir: output, NewPreparer: factory.new,
	}, factory
}
