package fillerreview

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillerstructure"
	"github.com/loomarr/loomarr/internal/fillerstructurewindow"
)

func TestBuildTemporalStructureWindowCorpusPlanReproducesSeamConstructions(t *testing.T) {
	fixture := newTemporalStructureHoldoutFixture(t)
	holdout := filepath.Join(t.TempDir(), "holdout")
	if _, err := BuildTemporalStructureHoldoutPlan(fixture.config(holdout)); err != nil {
		t.Fatal(err)
	}
	plannedAt := fixture.plannedAt.Add(time.Hour)
	first := filepath.Join(t.TempDir(), "first")
	config := TemporalStructureWindowCorpusConfig{
		HoldoutAuthoringPath: filepath.Join(holdout, "authoring.json"),
		HoldoutReceiptPath:   filepath.Join(holdout, "receipt.json"),
		Seed:                 "window-corpus-seed", PlannedAt: plannedAt, OutputDir: first,
	}
	result, err := BuildTemporalStructureWindowCorpusPlan(config)
	if err != nil {
		t.Fatal(err)
	}
	if result.Cases != TemporalStructureWindowCorpusCases || !reviewSHA256(result.PlanSHA256) || !reviewSHA256(result.PlanFileSHA256) {
		t.Fatalf("result = %+v", result)
	}
	plan := readStrictTestJSON[TemporalStructureWindowCorpusPlan](t, filepath.Join(first, "plan.json"))
	if plan.SHA256 != result.PlanSHA256 || plan.TrainingAllowed || plan.ProductionAllowed {
		t.Fatalf("plan = %+v", plan)
	}
	authoring := readStrictTestJSON[TemporalStructureChallengeAuthoring](t, config.HoldoutAuthoringPath)
	receipt := readStrictTestJSON[TemporalStructureHoldoutReceipt](t, config.HoldoutReceiptPath)
	sources := make(map[string]TemporalStructureChallengeSource, len(authoring.Sources))
	for _, source := range authoring.Sources {
		sources[source.ID] = source
	}
	tampered := plan
	tampered.Cases = slices.Clone(plan.Cases)
	tampered.Cases[0].TargetBoundaryMS++
	tampered.SHA256 = temporalStructureWindowCorpusPlanSHA256(tampered)
	if err := validateTemporalStructureWindowCorpusPlan(tampered, authoring, receipt, config.Seed); err == nil ||
		!strings.Contains(err.Error(), "does not reproduce") {
		t.Fatalf("rehashed plan drift error = %v", err)
	}
	patterns := make(map[string]int)
	roles := make(map[string]struct{})
	edgeFamilies := make(map[string]map[string]struct{})
	for _, item := range plan.Cases {
		patterns[item.Pattern]++
		if item.TargetSeamMS != fillerstructurewindow.PrimarySpanMS || item.DurationMS <= fillerstructurewindow.PrimarySpanMS {
			t.Fatalf("case is not a long-reel seam case: %+v", item)
		}
		parent := sources[item.Segments[0].SourceID]
		if item.Segments[0].StartMS != parent.DurationMS/3 {
			t.Fatalf("case programme prefix does not use the measured interior start: %+v", item.Segments[0])
		}
		for index := range item.Segments {
			if item.Truth[index].EndMS-item.Truth[index].StartMS != item.Segments[index].DurationMS {
				t.Fatalf("case truth is not derived from requested parts: %+v", item)
			}
		}
		switch item.Pattern {
		case TemporalStructureWindowPatternSeamOverlap:
			if item.TargetBoundaryMS != item.TargetSeamMS-10_000 {
				t.Fatalf("overlap target = %d", item.TargetBoundaryMS)
			}
		case TemporalStructureWindowPatternSeamPrimaryLeft:
			if item.TargetBoundaryMS != item.TargetSeamMS-1_000 {
				t.Fatalf("left target = %d", item.TargetBoundaryMS)
			}
		case TemporalStructureWindowPatternSeamPrimaryRight:
			if item.TargetBoundaryMS != item.TargetSeamMS+1_000 {
				t.Fatalf("right target = %d", item.TargetBoundaryMS)
			}
		case TemporalStructureWindowPatternCrossingSeam:
			if len(item.Truth) != 3 || item.TargetBoundaryMS != 0 ||
				item.Truth[1].StartMS >= item.TargetSeamMS || item.Truth[1].EndMS <= item.TargetSeamMS {
				t.Fatalf("crossing case = %+v", item)
			}
			continue
		case TemporalStructureWindowPatternDurationLowerEdge:
			if item.DurationMS != TemporalStructureWindowLowerEdgeDurationMS || len(item.Truth) != 3 ||
				item.TargetBoundaryMS != 0 || len(item.FillerFamilyIDs) != 1 {
				t.Fatalf("lower duration-edge case = %+v", item)
			}
		case TemporalStructureWindowPatternDurationUpperEdge:
			if item.DurationMS != TemporalStructureWindowUpperEdgeDurationMS || len(item.Truth) != 3 ||
				item.TargetBoundaryMS != 0 || len(item.FillerFamilyIDs) != 1 {
				t.Fatalf("upper duration-edge case = %+v", item)
			}
		default:
			t.Fatalf("unknown pattern %q", item.Pattern)
		}
		if item.Pattern == TemporalStructureWindowPatternDurationLowerEdge || item.Pattern == TemporalStructureWindowPatternDurationUpperEdge {
			if item.Truth[0].Role != fillerstructure.RoleProgrammeFragment || item.Truth[2].Role != fillerstructure.RoleProgrammeFragment ||
				item.Truth[1].Role == fillerstructure.RoleProgrammeFragment {
				t.Fatalf("duration edge is not programme/filler/programme: %+v", item)
			}
			prefixEndMS := item.Segments[0].StartMS + item.Segments[0].DurationMS
			suffixEndMS := item.Segments[2].StartMS + item.Segments[2].DurationMS
			if item.Segments[2].StartMS != temporalStructureWindowProgrammeEdgeSuffixStart(parent) ||
				item.Segments[2].StartMS-prefixEndMS <= 15_000 || parent.DurationMS-suffixEndMS <= 15_000 {
				t.Fatalf("duration edge does not retain interior programme margins: parent=%+v case=%+v", parent, item)
			}
			if edgeFamilies[item.Pattern] == nil {
				edgeFamilies[item.Pattern] = make(map[string]struct{})
			}
			edgeFamilies[item.Pattern][item.FillerFamilyIDs[0]] = struct{}{}
			continue
		}
		if len(item.Truth) != 4 || item.Truth[1].EndMS != item.TargetBoundaryMS ||
			item.Truth[1].Role != item.Truth[2].Role || len(item.FillerFamilyIDs) != 2 ||
			item.FillerFamilyIDs[0] == item.FillerFamilyIDs[1] {
			t.Fatalf("same-role join case = %+v", item)
		}
		roles[string(item.Truth[1].Role)] = struct{}{}
	}
	if len(patterns) != 6 || len(roles) != len(temporalStructureHoldoutRoleQuotas) {
		t.Fatalf("patterns=%v roles=%v", patterns, roles)
	}
	for pattern, count := range patterns {
		want := TemporalStructureWindowCorpusCasesPerPattern
		if pattern == TemporalStructureWindowPatternDurationLowerEdge || pattern == TemporalStructureWindowPatternDurationUpperEdge {
			want = TemporalStructureWindowCorpusEdgeCases
		}
		if count != want {
			t.Fatalf("pattern %q has %d cases", pattern, count)
		}
	}
	for pattern, families := range edgeFamilies {
		if len(families) != TemporalStructureWindowCorpusEdgeCases {
			t.Fatalf("edge pattern %q families=%v", pattern, families)
		}
	}

	second := filepath.Join(t.TempDir(), "second")
	repeatConfig := config
	repeatConfig.OutputDir = second
	repeat, err := BuildTemporalStructureWindowCorpusPlan(repeatConfig)
	if err != nil {
		t.Fatal(err)
	}
	if repeat != result {
		t.Fatalf("plan is not reproducible: first=%+v repeat=%+v", result, repeat)
	}
	firstRaw, err := os.ReadFile(filepath.Join(first, "plan.json"))
	if err != nil {
		t.Fatal(err)
	}
	secondRaw, err := os.ReadFile(filepath.Join(second, "plan.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(firstRaw, secondRaw) {
		t.Fatal("repeated plan bytes differ")
	}
	if _, err := BuildTemporalStructureWindowCorpusPlan(config); err == nil {
		t.Fatal("immutable plan output was overwritten")
	}
}

func TestBuildTemporalStructureWindowCorpusPlanRejectsDriftedAuthority(t *testing.T) {
	fixture := newTemporalStructureHoldoutFixture(t)
	holdout := filepath.Join(t.TempDir(), "holdout")
	if _, err := BuildTemporalStructureHoldoutPlan(fixture.config(holdout)); err != nil {
		t.Fatal(err)
	}
	receiptPath := filepath.Join(holdout, "receipt.json")
	receipt := readStrictTestJSON[TemporalStructureHoldoutReceipt](t, receiptPath)
	receipt.SelectedAnchors[0].FamilyID = receipt.SelectedAnchors[1].FamilyID
	drifted := writeTemporalHumanJSON(t, t.TempDir(), "receipt.json", receipt)
	_, err := BuildTemporalStructureWindowCorpusPlan(TemporalStructureWindowCorpusConfig{
		HoldoutAuthoringPath: filepath.Join(holdout, "authoring.json"), HoldoutReceiptPath: drifted,
		Seed: "window-corpus-seed", PlannedAt: fixture.plannedAt.Add(time.Hour), OutputDir: filepath.Join(t.TempDir(), "output"),
	})
	if err == nil || !strings.Contains(err.Error(), "repeats an anchor family") {
		t.Fatalf("drift error = %v", err)
	}
}
