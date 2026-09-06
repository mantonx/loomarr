package app

import (
	"path/filepath"
	"testing"

	"github.com/loomarr/loomarr/internal/filler"
	"github.com/loomarr/loomarr/internal/testkit"
)

func TestBuildQualificationSegmentScreeningRuntimeUsesConfiguredFillerStorage(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	layout, err := filler.NewLayout(filepath.Join(root, "clips"), filepath.Join(root, "watch"))
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := buildQualificationSegmentScreeningRuntime(testkit.MigratedSQLiteStore(t), layout)
	if err != nil || runtime == nil {
		t.Fatalf("qualification runtime = %v, error = %v", runtime, err)
	}
}

func TestBuildQualificationSegmentScreeningRuntimeLeavesUnconfiguredInstallInert(t *testing.T) {
	runtime, err := buildQualificationSegmentScreeningRuntime(nil, filler.Layout{})
	if err != nil || runtime != nil {
		t.Fatalf("unconfigured qualification runtime = %v, error = %v", runtime, err)
	}
}

func TestBuildSegmentScreeningSummaryServiceUsesTheSameAppliedEvidenceRoot(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	layout, err := filler.NewLayout(filepath.Join(root, "clips"), filepath.Join(root, "watch"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := buildSegmentScreeningSummaryService(layout)
	if err != nil || service == nil {
		t.Fatalf("screening summary service = %v, error = %v", service, err)
	}
	if got, want := segmentScreeningEvidenceRoot(layout), filepath.Join(layout.ClipDir(), ".loomarr", "segment-screening"); got != want {
		t.Fatalf("screening evidence root = %q, want %q", got, want)
	}
	service, err = buildSegmentScreeningSummaryService(filler.Layout{})
	if err != nil || service != nil {
		t.Fatalf("unconfigured summary service = %v, error = %v", service, err)
	}
}
