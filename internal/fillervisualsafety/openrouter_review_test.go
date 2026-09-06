package fillervisualsafety_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillerbakeoff"
	"github.com/loomarr/loomarr/internal/fillervisualsafety"
)

func TestRunCandidateBlindOpenRouterReviewBindsCompleteBlindInputAndOneRoute(t *testing.T) {
	t.Parallel()

	bundle, packageSHA, ownerSHA, carrierFFmpeg := candidateBlindOpenRouterFixture(t)
	now := time.Date(2026, time.September, 4, 20, 0, 0, 0, time.UTC)
	var requestRaw []byte
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var err error
		requestRaw, err = io.ReadAll(request.Body)
		if err != nil {
			t.Error(err)
			return
		}
		if request.Header.Get("X-OpenRouter-Metadata") != "enabled" {
			t.Errorf("metadata header = %q", request.Header.Get("X-OpenRouter-Metadata"))
		}
		_, _ = io.WriteString(writer, `{"id":"generation-1","model":"vendor/model","choices":[{"message":{"content":"{\"coverageAssessment\":\"completed\",\"matches\":[]}","reasoning":"checked"}}],"usage":{"prompt_tokens":100,"completion_tokens":10,"cost":0.001},"openrouter_metadata":{"attempt":1,"attempts":[{"provider":"Pinned Provider","model":"vendor/model-20260901","status":200}],"endpoints":{"available":[{"provider":"Pinned Provider","model":"vendor/model-20260901","selected":true}]}}}`)
	}))
	defer server.Close()
	output := filepath.Join(t.TempDir(), "hosted-review")
	result, err := fillervisualsafety.RunCandidateBlindOpenRouterReview(context.Background(), fillervisualsafety.CandidateBlindOpenRouterConfig{
		BundlePath: bundle, ExpectedPackageSHA256: packageSHA, ExpectedOwnerMapSHA256: ownerSHA,
		ExpectedSelectionOrigin: fillervisualsafety.ReviewSelectionTargetedDiagnostic,
		OutputDir:               output, FFmpegPath: carrierFFmpeg, BaseURL: server.URL, APIKey: "secret",
		Snapshot: candidateBlindOpenRouterSnapshot(now.Add(-time.Hour)),
		Model:    "vendor/model", ModelFamily: "vendor-family", UpstreamProvider: "Pinned Provider",
		UpstreamProviderSlug: "pinned-provider/fp8", ReviewerID: "reviewer-001",
		PerRequestTimeout: time.Minute, MaxChargeNanoUSD: 2_000_000,
		AllowInsecureTestURL: true, Client: server.Client(), Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("RunCandidateBlindOpenRouterReview() error = %v", err)
	}
	if result.Assessment.Outcome != fillervisualsafety.CandidateBlindOutcomeNoSignal ||
		result.Attempt.State != fillervisualsafety.CandidateBlindAttemptAccepted || result.Attempt.ChargedNanoUSD != 1_000_000 ||
		result.Input.Carrier.SHA256 == "" || len(result.Input.ContactSheets) != 1 || result.TruthAuthorityCreated ||
		result.TrainingAllowed || result.ProductionAdmissionAllowed {
		t.Fatalf("result = %+v", result)
	}
	opened, err := fillervisualsafety.OpenCandidateBlindOpenRouterReview(output)
	if err != nil || opened.SHA256 != result.SHA256 {
		t.Fatalf("OpenCandidateBlindOpenRouterReview() = %+v, %v", opened, err)
	}
	var request struct {
		Messages []struct {
			Content []struct {
				Type     string `json:"type"`
				Text     string `json:"text"`
				ImageURL *struct {
					URL string `json:"url"`
				} `json:"image_url"`
				VideoURL *struct {
					URL string `json:"url"`
				} `json:"video_url"`
			} `json:"content"`
		} `json:"messages"`
		Provider struct {
			Order             []string `json:"order"`
			AllowFallbacks    bool     `json:"allow_fallbacks"`
			RequireParameters bool     `json:"require_parameters"`
			DataCollection    string   `json:"data_collection"`
			ZDR               bool     `json:"zdr"`
		} `json:"provider"`
	}
	if err := json.Unmarshal(requestRaw, &request); err != nil {
		t.Fatal(err)
	}
	parts := request.Messages[1].Content
	images, videos := 0, 0
	for _, part := range parts {
		if part.ImageURL != nil && strings.HasPrefix(part.ImageURL.URL, "data:image/jpeg;base64,") {
			images++
		}
		if part.VideoURL != nil && strings.HasPrefix(part.VideoURL.URL, "data:video/mp4;base64,") {
			videos++
		}
	}
	if images != 1 || videos != 1 || len(request.Provider.Order) != 1 || request.Provider.Order[0] != "pinned-provider/fp8" ||
		request.Provider.AllowFallbacks || !request.Provider.RequireParameters || request.Provider.DataCollection != "deny" || !request.Provider.ZDR {
		t.Fatalf("request routing or media = %+v", request)
	}
	if bytes.Contains(requestRaw, []byte("real-decoder-source")) || bytes.Contains(requestRaw, []byte(bundle)) ||
		!bytes.Contains(requestRaw, []byte("explicit_nudity_v1")) || !bytes.Contains(requestRaw, []byte("every attached contact sheet")) {
		t.Fatalf("request lost blindness or policy binding")
	}
}

func TestRunCandidateBlindOpenRouterReviewPreservesUnsettledStatusFailure(t *testing.T) {
	t.Parallel()

	bundle, packageSHA, ownerSHA, carrierFFmpeg := candidateBlindOpenRouterFixture(t)
	now := time.Date(2026, time.September, 4, 20, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(writer, `{"error":"temporary"}`)
	}))
	defer server.Close()
	output := filepath.Join(t.TempDir(), "hosted-review")
	_, err := fillervisualsafety.RunCandidateBlindOpenRouterReview(context.Background(), fillervisualsafety.CandidateBlindOpenRouterConfig{
		BundlePath: bundle, ExpectedPackageSHA256: packageSHA, ExpectedOwnerMapSHA256: ownerSHA,
		ExpectedSelectionOrigin: fillervisualsafety.ReviewSelectionTargetedDiagnostic,
		OutputDir:               output, FFmpegPath: carrierFFmpeg, BaseURL: server.URL, APIKey: "secret",
		Snapshot: candidateBlindOpenRouterSnapshot(now.Add(-time.Hour)),
		Model:    "vendor/model", ModelFamily: "vendor-family", UpstreamProvider: "Pinned Provider",
		UpstreamProviderSlug: "pinned-provider/fp8", ReviewerID: "reviewer-001",
		PerRequestTimeout: time.Minute, MaxChargeNanoUSD: 2_000_000,
		AllowInsecureTestURL: true, Client: server.Client(), Now: func() time.Time { return now },
	})
	if err == nil {
		t.Fatal("expected provider status failure")
	}
	checkpoint, readErr := os.ReadFile(filepath.Join(output, "attempt.json"))
	if readErr != nil || !bytes.Contains(checkpoint, []byte(`"state": "unsettled"`)) ||
		!bytes.Contains(checkpoint, []byte(`"operationalFailure": "provider_status"`)) {
		t.Fatalf("checkpoint = %s, %v", checkpoint, readErr)
	}
	for _, path := range []string{filepath.Join(output, ".incomplete"), filepath.Join(output, "raw-response.json")} {
		if _, statErr := os.Stat(path); statErr != nil {
			t.Fatalf("reserved failure did not preserve %s: %v", path, statErr)
		}
	}
	if _, statErr := os.Stat(filepath.Join(output, "result.json")); !os.IsNotExist(statErr) {
		t.Fatalf("failed call published a result: %v", statErr)
	}
}

func TestRunCandidateBlindOpenRouterReviewNamesUnknownResponseAccounting(t *testing.T) {
	t.Parallel()

	bundle, packageSHA, ownerSHA, carrierFFmpeg := candidateBlindOpenRouterFixture(t)
	now := time.Date(2026, time.September, 4, 20, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(writer, `{"id":"generation","model":"vendor/model","choices":[{"message":{"content":null}}]}`)
	}))
	defer server.Close()
	output := filepath.Join(t.TempDir(), "hosted-review")
	_, err := fillervisualsafety.RunCandidateBlindOpenRouterReview(context.Background(), fillervisualsafety.CandidateBlindOpenRouterConfig{
		BundlePath: bundle, ExpectedPackageSHA256: packageSHA, ExpectedOwnerMapSHA256: ownerSHA,
		ExpectedSelectionOrigin: fillervisualsafety.ReviewSelectionTargetedDiagnostic,
		OutputDir:               output, FFmpegPath: carrierFFmpeg, BaseURL: server.URL, APIKey: "secret",
		Snapshot: candidateBlindOpenRouterSnapshot(now.Add(-time.Hour)),
		Model:    "vendor/model", ModelFamily: "vendor-family", UpstreamProvider: "Pinned Provider",
		UpstreamProviderSlug: "pinned-provider/fp8", ReviewerID: "reviewer-001",
		PerRequestTimeout: time.Minute, MaxChargeNanoUSD: 2_000_000,
		AllowInsecureTestURL: true, Client: server.Client(), Now: func() time.Time { return now },
	})
	if err == nil {
		t.Fatal("expected missing accounting failure")
	}
	checkpoint, readErr := os.ReadFile(filepath.Join(output, "attempt.json"))
	if readErr != nil || !bytes.Contains(checkpoint, []byte(`"state": "unsettled"`)) ||
		!bytes.Contains(checkpoint, []byte(`"operationalFailure": "unsettled_accounting"`)) {
		t.Fatalf("checkpoint = %s, %v", checkpoint, readErr)
	}
}

func candidateBlindOpenRouterFixture(t *testing.T) (string, string, string, string) {
	t.Helper()
	prepared, ffmpeg := realDecoderSource(t, realDecoderOptions{
		rate: "10", rateNumerator: 10, rateDenominator: 1, durationMS: 3_000, lastFrameMS: 2_900, driftMS: 1,
		container: "mkv", codec: "ffv1", timeBaseDenominator: 1_000,
	})
	policyPath := attachReviewPolicy(t, prepared)
	bundle := filepath.Join(t.TempDir(), "bundle")
	result, err := fillervisualsafety.BuildCandidateBlindReviewBundle(context.Background(), fillervisualsafety.CandidateBlindReviewConfig{
		Alias: "visual-case-001", SourceFamilyID: "source-family-001", RightsSHA256: repeatedDigest("e"),
		SelectionOrigin: fillervisualsafety.ReviewSelectionTargetedDiagnostic,
		Source:          fillervisualsafety.SourceRequest{Authority: prepared.Authority, Path: prepared.SnapshotPath},
		Profile:         prepared.Plan.Profile, PolicyPath: policyPath, FFmpegPath: ffmpeg, OutputDir: bundle,
		PreparedAt: time.Date(2026, time.September, 4, 18, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	carrierFFmpeg := filepath.Join(t.TempDir(), "ffmpeg")
	script := "#!/bin/sh\nif [ \"$1\" = \"-version\" ]; then printf '%s\\n' 'ffmpeg version candidate-blind-test'; exit 0; fi\nfor last do :; done\nprintf '%s' 'private-test-carrier' > \"$last\"\n"
	if err := os.WriteFile(carrierFFmpeg, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return bundle, result.PackageSHA256, result.OwnerMapSHA256, carrierFFmpeg
}

func candidateBlindOpenRouterSnapshot(retrieved time.Time) fillerbakeoff.OpenRouterSnapshot {
	return fillerbakeoff.OpenRouterSnapshot{
		SchemaVersion: fillerbakeoff.OpenRouterSnapshotSchemaVersion,
		SourceBaseURL: fillerbakeoff.OpenRouterBaseURL, RetrievedAt: retrieved.UTC(), Requests: 3, ResponseBytes: 1,
		Models: []fillerbakeoff.OpenRouterModelSnapshot{{
			ID: "vendor/model", CanonicalSlug: "vendor/model-20260901", Name: "Model", Created: 1,
			InputModalities: []string{"image", "text", "video"}, OutputModalities: []string{"text"},
			Endpoints: []fillerbakeoff.OpenRouterEndpointSnapshot{{
				Name: "Pinned", ModelID: "vendor/model", ProviderName: "Pinned Provider", ProviderSlug: "pinned-provider/fp8",
				Quantization: "fp8", ContextLength: 262_144, MaxCompletionTokens: 8_192,
				SupportedParameters: []string{"reasoning", "response_format", "structured_outputs"},
				Pricing:             map[string]string{"completion": "0.000001", "prompt": "0.000001"}, Status: 0, ZDR: true,
			}},
		}},
	}
}
