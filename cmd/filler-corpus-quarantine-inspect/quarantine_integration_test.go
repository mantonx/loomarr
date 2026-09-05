//go:build ffmpeg

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillerquarantine"
)

func TestQuarantineInspectorWithRealMedia(t *testing.T) {
	fixture := newQuarantineInspectionFixture(t)
	report, err := fillerquarantine.Inspect(t.Context(), fixture.config(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.Cases != 1 || report.Summary.PriorSources != 1 || report.Summary.EligibleForRightsReview != 1 || report.Summary.Held != 0 {
		t.Fatalf("summary=%+v", report.Summary)
	}
	if len(report.Cases) != 1 || report.Cases[0].Disposition != fillerquarantine.DispositionEligibleForRightsReview || len(report.Cases[0].HoldReasons) != 0 || len(report.Comparisons) != 1 || report.Comparisons[0].Related {
		t.Fatalf("case=%+v comparisons=%+v", report.Cases, report.Comparisons)
	}
	if err := fillerquarantine.Validate(report); err != nil {
		t.Fatalf("report did not revalidate: %v", err)
	}

	if err := os.Remove(filepath.Join(fixture.mediaRoot, fixture.priorSourceName)); err != nil {
		t.Fatal(err)
	}
	incomplete, err := fillerquarantine.Inspect(t.Context(), fixture.config(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if incomplete.Summary.Held != 1 || incomplete.Summary.UnavailablePriorSources != 1 || incomplete.Cases[0].Disposition != fillerquarantine.DispositionHold || !slices.Contains(incomplete.Cases[0].HoldReasons, "prior_perceptual_exposure_incomplete") {
		t.Fatalf("incomplete summary=%+v case=%+v", incomplete.Summary, incomplete.Cases[0])
	}

	timedOut, err := fillerquarantine.Inspect(t.Context(), fixture.config(time.Millisecond))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout error=%v", err)
	}
	if !reflect.DeepEqual(timedOut, fillerquarantine.Report{}) {
		t.Fatalf("timeout returned partial report: %+v", timedOut)
	}
}

func TestQuarantineInspectorCommandPublishesRealMediaReport(t *testing.T) {
	fixture := newQuarantineInspectionFixture(t)
	output := filepath.Join(t.TempDir(), "inspection.json")
	args := []string{
		"-inventory", fixture.inventoryPath,
		"-ledger", fixture.ledgerPath,
		"-download-root", fixture.mediaRoot,
		"-prior-public", fixture.priorManifestPath,
		"-prior-authority", fixture.priorAuthorityPath,
		"-prior-source-root", fixture.mediaRoot,
		"-prior-cases", "1",
		"-max-media-wall-time", "1m",
		"-ffmpeg", "ffmpeg",
		"-output", output,
		"-generated-at", fixture.generatedAt.Format(time.RFC3339),
	}
	var stdout, stderr bytes.Buffer
	if code := run(args, &stdout, &stderr); code != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	raw, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var report fillerquarantine.Report
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatal(err)
	}
	if err := fillerquarantine.Validate(report); err != nil || report.Summary.EligibleForRightsReview != 1 {
		t.Fatalf("summary=%+v validation=%v", report.Summary, err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := run(args, &stdout, &stderr); code != 1 {
		t.Fatalf("immutable rerun code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if changed, err := os.ReadFile(output); err != nil || !bytes.Equal(changed, raw) {
		t.Fatalf("immutable output changed: err=%v", err)
	}
}
