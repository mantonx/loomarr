package fillerreview

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/loomarr/loomarr/internal/filler"
	"github.com/loomarr/loomarr/internal/fillerstructure"
	"github.com/loomarr/loomarr/internal/fillerstructuremedia"
	"github.com/loomarr/loomarr/internal/fillerstructurewindow"
	"github.com/loomarr/loomarr/internal/fillerstructurewindowcert"
)

func TestPublishTemporalStructureWindowPreflightReportsExactRunAndRequiresRepresentedEdges(t *testing.T) {
	root := t.TempDir()
	suiteConfig, _ := temporalStructureWindowSuiteFixture(t, filepath.Join(root, "suite"))
	if _, err := BuildTemporalStructureWindowCertificationSuite(t.Context(), suiteConfig); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "ready-preflight.json")
	report, fileSHA, err := PublishTemporalStructureWindowPreflight(TemporalStructureWindowPreflightConfig{
		WindowSetManifestPath:       suiteConfig.WindowSetManifestPath,
		SuitePath:                   filepath.Join(suiteConfig.OutputDir, "private", "suite.json"),
		ShortSourceCeilingMS:        TemporalStructureWindowFirstShortSourceCeilingMS,
		IntendedLongSourceCeilingMS: TemporalStructureWindowFirstLongSourceCeilingMS, OutputPath: output,
	})
	if err != nil {
		t.Fatal(err)
	}
	manifest, _, err := LoadTemporalStructureWindowSetPublic(suiteConfig.WindowSetManifestPath, TemporalStructureWindowCorpusCases)
	if err != nil {
		t.Fatal(err)
	}
	windows := 0
	for _, item := range manifest.Cases {
		windows += len(item.Windows)
	}
	if report.Status != TemporalStructureWindowPreflightReady || !report.ReadyForPaidCertification ||
		report.NextAction != "run_two_truth_blind_window_families" || report.Cases != TemporalStructureWindowCorpusCases ||
		report.WindowRequestsPerFamily != windows || report.CompleteVideoRequestsPerFamily != len(manifest.Cases) ||
		report.TotalProviderRequests != 2*(windows+TemporalStructureWindowCorpusCases) ||
		report.MinimumObservedSourceDurationMS != TemporalStructureWindowLowerEdgeDurationMS ||
		report.MaximumObservedSourceDurationMS < TemporalStructureWindowUpperEdgeDurationMS ||
		report.MaximumObservedSourceDurationMS > TemporalStructureWindowFirstLongSourceCeilingMS || report.TrainingAllowed ||
		report.ProductionAdmissionAllowed || report.AutomaticMaterializationAllowed || report.LowerEnvelopeEdgeCases < 2 ||
		report.UpperEnvelopeEdgeCases < 2 || !reviewSHA256(fileSHA) {
		t.Fatalf("report=%+v fileSHA=%q", report, fileSHA)
	}
	if err := ValidateTemporalStructureWindowPreflight(report); err != nil {
		t.Fatal(err)
	}
	if loaded, _, err := LoadTemporalStructureWindowPreflight(output, report.WindowSetManifestSHA256); err != nil || loaded.SHA256 != report.SHA256 {
		t.Fatalf("ready preflight load=%+v error=%v", loaded, err)
	}
	if _, _, err := PublishTemporalStructureWindowPreflight(TemporalStructureWindowPreflightConfig{
		WindowSetManifestPath:       suiteConfig.WindowSetManifestPath,
		SuitePath:                   filepath.Join(suiteConfig.OutputDir, "private", "suite.json"),
		ShortSourceCeilingMS:        TemporalStructureWindowFirstShortSourceCeilingMS,
		IntendedLongSourceCeilingMS: TemporalStructureWindowFirstLongSourceCeilingMS, OutputPath: output,
	}); err == nil {
		t.Fatal("immutable preflight output was overwritten")
	}
	blockedPath := filepath.Join(root, "blocked-preflight.json")
	blocked, _, err := PublishTemporalStructureWindowPreflight(TemporalStructureWindowPreflightConfig{
		WindowSetManifestPath:       suiteConfig.WindowSetManifestPath,
		SuitePath:                   filepath.Join(suiteConfig.OutputDir, "private", "suite.json"),
		ShortSourceCeilingMS:        TemporalStructureWindowFirstShortSourceCeilingMS,
		IntendedLongSourceCeilingMS: 300_000, OutputPath: blockedPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if blocked.Status != TemporalStructureWindowPreflightBlocked || blocked.ReadyForPaidCertification ||
		blocked.NextAction != "extend_and_rerender_sealed_window_corpus" {
		t.Fatalf("blocked report=%+v", blocked)
	}
	if _, _, err := LoadTemporalStructureWindowPreflight(blockedPath, blocked.WindowSetManifestSHA256); err == nil ||
		!strings.Contains(err.Error(), "does not authorize") {
		t.Fatalf("blocked preflight load error=%v", err)
	}
}

func TestBuildTemporalStructureWindowPreflightRecognizesRepresentedContinuousEnvelope(t *testing.T) {
	profile := fillerstructurewindow.CanonicalProfile()
	manifest := TemporalStructureWindowSetManifest{
		AssessmentMediaProfileSHA256: fillerstructuremedia.CanonicalProfile().SHA256,
		Cases:                        make([]TemporalStructureWindowSetPublicCase, TemporalStructureWindowCorpusCases),
	}
	for index := range manifest.Cases {
		duration := int64(200_000)
		switch index {
		case 0, 1:
			duration = 121_000
		case 2, 3:
			duration = 299_000
		}
		manifest.Cases[index] = TemporalStructureWindowSetPublicCase{
			Source:  filler.SplitSourceAsset{Bytes: int64(1_000 + index), DurationMs: duration},
			Windows: []TemporalStructureWindowSetWindow{{Ordinal: 0}, {Ordinal: 1}},
			MediaSet: fillerstructurewindow.MediaSet{Windows: []fillerstructurewindow.WindowMedia{
				{Media: fillerstructure.AssessmentMedia{Bytes: int64(500 + index)}},
				{Media: fillerstructure.AssessmentMedia{Bytes: int64(600 + index)}},
			}},
		}
	}
	report := buildTemporalStructureWindowPreflight(
		TemporalStructureWindowPreflightConfig{ShortSourceCeilingMS: 120_000, IntendedLongSourceCeilingMS: 300_000},
		manifest, strings.Repeat("a", 64), fillerstructurewindowcert.Suite{SHA256: strings.Repeat("b", 64)}, strings.Repeat("c", 64),
	)
	if !report.ReadyForPaidCertification || !report.ContinuousProductionEnvelope || !report.IntendedProductionCeilingRepresented ||
		report.LowerEnvelopeEdgeCases != 2 || report.UpperEnvelopeEdgeCases != 2 ||
		report.MinimumObservedSourceDurationMS != 121_000 || report.MaximumObservedSourceDurationMS != 299_000 ||
		report.MinimumObservedWindowsPerSource != 2 || report.MaximumObservedWindowsPerSource != 2 ||
		report.WindowRequestsPerFamily != 2*TemporalStructureWindowCorpusCases ||
		report.TotalProviderRequests != 6*TemporalStructureWindowCorpusCases ||
		report.ProtocolMaximumSourceDurationMS != profile.MaximumSourceDurationMS || report.Status != TemporalStructureWindowPreflightReady {
		t.Fatalf("report=%+v", report)
	}
	if err := ValidateTemporalStructureWindowPreflight(report); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "ready-preflight.json")
	writeTestJSON(t, path, report)
	loaded, fileSHA, err := LoadTemporalStructureWindowPreflight(path, report.WindowSetManifestSHA256)
	if err != nil || loaded.SHA256 != report.SHA256 || !reviewSHA256(fileSHA) {
		t.Fatalf("loaded=%+v fileSHA=%q error=%v", loaded, fileSHA, err)
	}

	tampered := report
	tampered.TotalProviderRequests++
	tampered.SHA256 = temporalStructureWindowPreflightSHA256(tampered)
	if err := ValidateTemporalStructureWindowPreflight(tampered); err == nil {
		t.Fatal("rehashed request topology drift was accepted")
	}
}
