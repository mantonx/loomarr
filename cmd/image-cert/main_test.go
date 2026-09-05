package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/loomarr/loomarr/internal/images"
	"github.com/loomarr/loomarr/internal/testkit"
)

func TestRunWritesTheRepositoryCertificationReport(t *testing.T) {
	worker, err := testkit.RustImageWorker()
	if err != nil {
		t.Fatal(err)
	}
	reportPath := filepath.Join(t.TempDir(), "image-certification.json")
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{
		"--worker", worker,
		"--report", reportPath,
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run = %d; stderr = %s", code, stderr.String())
	}
	data, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var report images.CertificationReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if !report.Passed || report.Summary.Passed != 7 || report.Summary.Refused != 11 {
		t.Errorf("report = %+v", report.Summary)
	}
	if !strings.Contains(stdout.String(), reportPath) || !strings.Contains(stdout.String(), "7 passed, 11 refused") {
		t.Errorf("stdout = %q", stdout.String())
	}
}

func TestRepositoryCertificationCorpusCoversStaticAnimationAndLimits(t *testing.T) {
	corpus := t.TempDir()
	manifest, err := writeCertificationCorpus(corpus)
	if err != nil {
		t.Fatalf("writeCertificationCorpus: %v", err)
	}
	report, err := images.Certify(context.Background(), images.CertificationOptions{
		CorpusDir: corpus, Renderer: testkit.RustImageRenderer(t),
		Limits: images.DefaultCertificationLimits(), ExpectedRefusals: manifest.ExpectedRefusals,
		BoundaryCases: manifest.BoundaryCases,
	})
	if err != nil {
		t.Fatalf("repository corpus: %v; report = %+v", err, report)
	}
	if report.Summary.Passed != 7 || report.Summary.Refused != 11 || report.Summary.Failed != 0 {
		t.Fatalf("summary = %+v, want 7 accepted and 11 stable refusals", report.Summary)
	}
	byPath := map[string]images.CertificationCase{}
	for _, result := range report.Cases {
		byPath[result.Path] = result
	}
	if got := byPath["fractional-zero-delay.apng"]; !got.Animated || got.FrameCount != 2 || got.DurationMS != 27 {
		t.Errorf("APNG timeline = %+v", got)
	}
	if got := byPath["finite-loop.gif"]; !got.Animated || got.LoopCount == nil || *got.LoopCount != 2 {
		t.Errorf("finite GIF loop = %+v", got)
	}
	if got := byPath["one-frame.gif"]; got.Animated || got.FrameCount != 1 || got.LoopCount != nil {
		t.Errorf("one-frame GIF should be static: %+v", got)
	}
	if got := byPath["static.webp"]; got.MIME != "image/webp" || got.Animated {
		t.Errorf("static WebP = %+v", got)
	}
	for path, code := range manifest.ExpectedRefusals {
		if got := byPath[path]; got.Outcome != "refused" || got.ErrorCode != code {
			t.Errorf("%s refusal = %+v, want %s", path, got, code)
		}
	}
	for _, boundary := range manifest.BoundaryCases {
		if got := byPath["@limits/"+boundary.Name]; got.Outcome != "refused" || got.ErrorCode != boundary.ExpectedCode {
			t.Errorf("%s boundary = %+v, want %s", boundary.Name, got, boundary.ExpectedCode)
		}
	}
}
