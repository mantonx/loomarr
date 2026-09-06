package fillerreview

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillereval"
)

func TestRunOpenRouterTemporalSuitabilityBindsFullVideoFlagsAndRawResponse(t *testing.T) {
	now := time.Date(2026, 9, 2, 4, 0, 0, 0, time.UTC)
	fixture := newTemporalHumanReviewFixture(t)
	manifest, _, err := LoadTemporalTruthEvidence(fixture.manifest)
	if err != nil {
		t.Fatal(err)
	}
	alias := manifest.Cases[0].Alias
	var request openRouterStructuredRequest
	server := newTemporalSuitabilityServer(t, &request, `{"visualAssessment":"completed","spokenLanguageAssessment":"completed","flags":[{"kind":"explicit_nudity","startMs":100,"endMs":200,"modality":"video"},{"kind":"hateful_or_degrading_slur","startMs":300,"endMs":400,"modality":"audio"}]}`)
	defer server.Close()
	checkpointDir := filepath.Join(t.TempDir(), "private")
	result, err := RunOpenRouterTemporalSuitability(t.Context(), temporalSuitabilityTestConfig(fixture.manifest, checkpointDir, alias, server, now))
	if err != nil {
		t.Fatal(err)
	}
	if result.ProductionAdmissionAllowed || result.Requests != 1 || result.ChargedNanoUSD != 1_000_000 || len(result.Assessments) != 1 || result.Assessments[0].Outcome != SuitabilityOutcomeProhibitedSignal || len(result.Assessments[0].Flags) != 2 {
		t.Fatalf("result = %+v", result)
	}
	parts := request.Messages[1].Content
	if len(parts) != 2 || parts[1].Type != "video_url" || parts[1].VideoURL == nil || !strings.HasPrefix(parts[1].VideoURL.URL, "data:video/mp4;base64,") {
		t.Fatalf("video request parts = %+v", parts)
	}
	if request.MaxTokens != temporalSuitabilityMaxTokens || result.Assessor.PromptVersion != TemporalSuitabilityPromptVersion || result.PromptSHA256 != temporalSuitabilityPromptSHA256() {
		t.Fatalf("request max tokens=%d prompt version=%q prompt sha=%q", request.MaxTokens, result.Assessor.PromptVersion, result.PromptSHA256)
	}
	attempt := result.Attempts[0]
	rawPath := filepath.Join(checkpointDir, filepath.FromSlash(attempt.RawResponsePath))
	info, err := os.Stat(rawPath)
	if err != nil || info.Mode().Perm() != 0o600 || attempt.ResponseSHA256 != result.Assessments[0].RawResponseSHA256 {
		t.Fatalf("raw response mode=%v attempt=%+v err=%v", info.Mode(), attempt, err)
	}
	if _, err := loadTemporalSuitabilityCheckpoint(checkpointDir, buildTemporalSuitabilityCheckpointIdentity(temporalSuitabilityTestConfig(fixture.manifest, checkpointDir, alias, server, now), hashPath(t, fixture.manifest), temporalTruthJSONSHA([]string{alias}), server.URL), []TemporalTruthEvidenceCase{manifest.Cases[0]}); err != nil {
		t.Fatal(err)
	}
}

func TestRunOpenRouterTemporalSuitabilityHoldsInsufficientCoverage(t *testing.T) {
	now := time.Date(2026, 9, 2, 4, 0, 0, 0, time.UTC)
	fixture := newTemporalHumanReviewFixture(t)
	manifest, _, err := LoadTemporalTruthEvidence(fixture.manifest)
	if err != nil {
		t.Fatal(err)
	}
	server := newTemporalSuitabilityServer(t, nil, `{"visualAssessment":"completed","spokenLanguageAssessment":"insufficient","flags":[]}`)
	defer server.Close()
	result, err := RunOpenRouterTemporalSuitability(t.Context(), temporalSuitabilityTestConfig(fixture.manifest, filepath.Join(t.TempDir(), "private"), manifest.Cases[0].Alias, server, now))
	if err != nil {
		t.Fatal(err)
	}
	if result.Assessments[0].Outcome != SuitabilityOutcomeCoverageHold || result.ProductionAdmissionAllowed {
		t.Fatalf("result = %+v", result)
	}
}

func TestRunOpenRouterTemporalSuitabilityTurnsInvalidSemanticsIntoFailure(t *testing.T) {
	now := time.Date(2026, 9, 2, 4, 0, 0, 0, time.UTC)
	fixture := newTemporalHumanReviewFixture(t)
	manifest, _, err := LoadTemporalTruthEvidence(fixture.manifest)
	if err != nil {
		t.Fatal(err)
	}
	server := newTemporalSuitabilityServer(t, nil, `{"visualAssessment":"completed","spokenLanguageAssessment":"completed","flags":[{"kind":"explicit_nudity","startMs":100,"endMs":200,"modality":"audio"}]}`)
	defer server.Close()
	result, err := RunOpenRouterTemporalSuitability(t.Context(), temporalSuitabilityTestConfig(fixture.manifest, filepath.Join(t.TempDir(), "private"), manifest.Cases[0].Alias, server, now))
	if err != nil {
		t.Fatal(err)
	}
	assessment := result.Assessments[0]
	if assessment.OperationalFailure == nil || assessment.OperationalFailure.Code != fillereval.TemporalFailureInvalidResponse || assessment.Outcome != "" || result.Attempts[0].State != temporalOpenRouterAttemptFailed {
		t.Fatalf("assessment=%+v attempt=%+v", assessment, result.Attempts[0])
	}
}

func TestRunOpenRouterTemporalSuitabilityRequiresSnapshotVideoModality(t *testing.T) {
	now := time.Date(2026, 9, 2, 4, 0, 0, 0, time.UTC)
	fixture := newTemporalHumanReviewFixture(t)
	server := newTemporalSuitabilityServer(t, nil, `{}`)
	defer server.Close()
	config := temporalSuitabilityTestConfig(fixture.manifest, filepath.Join(t.TempDir(), "private"), "evidence-00-private", server, now)
	config.Snapshot.Models[0].InputModalities = []string{"image", "text"}
	if _, err := RunOpenRouterTemporalSuitability(t.Context(), config); err == nil || !strings.Contains(err.Error(), "text/video") {
		t.Fatalf("err = %v", err)
	}
}

func newTemporalSuitabilityServer(t *testing.T, captured *openRouterStructuredRequest, content string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request openRouterStructuredRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		if captured != nil {
			*captured = request
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "generation", "model": "review/vendor-model",
			"choices": []any{map[string]any{"message": map[string]any{"content": content, "reasoning": ""}}},
			"usage":   map[string]any{"prompt_tokens": 100, "completion_tokens": 20, "cost": 0.001},
			"openrouter_metadata": map[string]any{
				"attempt":   1,
				"attempts":  []any{map[string]any{"provider": "Provider Route", "model": "review/vendor-model", "status": 200}},
				"endpoints": map[string]any{"available": []any{map[string]any{"provider": "Provider Route", "model": "review/vendor-model", "selected": true}}},
			},
		})
	}))
}

func temporalSuitabilityTestConfig(manifestPath, checkpointDir, alias string, server *httptest.Server, now time.Time) TemporalSuitabilityConfig {
	snapshot := openRouterReviewSnapshot(server.URL, now)
	snapshot.Models[0].InputModalities = append(snapshot.Models[0].InputModalities, "video")
	return TemporalSuitabilityConfig{
		EvidenceManifestPath: manifestPath, CaseAliases: []string{alias}, CheckpointDir: checkpointDir,
		BaseURL: server.URL, APIKey: "test-key", Snapshot: snapshot, Model: "review/vendor-model", ModelFamily: "video-family",
		UpstreamProvider: "Provider Route", UpstreamProviderSlug: "provider/route", AssessorID: "suitability-assessor",
		ReasoningMode: TemporalSuitabilityReasoningDisabled,
		ExpectedCases: 1, PerCaseTimeout: time.Second, MaxRequests: 1, MaxSpendNanoUSD: 2_000_000, MaxChargeNanoUSD: 2_000_000,
		AllowInsecureTestURL: true, Client: server.Client(), Now: func() time.Time { return now },
	}
}

func hashPath(t *testing.T, path string) string {
	t.Helper()
	digest, err := hashFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}
