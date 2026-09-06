package fillerreview

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillereval"
)

func TestRunOpenRouterTemporalAssessmentReservesAndBindsTwoAxisCalls(t *testing.T) {
	const (
		model    = "review/vendor-model"
		provider = "Provider Route"
		slug     = "provider/route"
	)
	packagePath, selectionPath := writeTemporalCalibrationFixture(t)
	checkpointDir := filepath.Join(t.TempDir(), "private")
	now := time.Unix(10_000, 0).UTC()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		var request openRouterStructuredRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		if request.Model != model || request.Provider.AllowFallbacks || !request.Provider.RequireParameters || !request.Provider.ZDR || request.Provider.DataCollection != "deny" || len(request.Provider.Order) != 1 || request.Provider.Order[0] != slug || request.ResponseFormat.Type != "json_schema" || !request.ResponseFormat.JSONSchema.Strict || request.Reasoning == nil || request.Reasoning.Enabled {
			t.Errorf("request is not pinned and strict: %+v", request)
		}
		checkpoint, err := readStrictJSON[temporalOpenRouterCheckpoint](filepath.Join(checkpointDir, temporalOpenRouterCheckpointFilename))
		if err != nil || len(checkpoint.Attempts) == 0 || checkpoint.Attempts[len(checkpoint.Attempts)-1].State != temporalOpenRouterAttemptReserved {
			t.Errorf("HTTP occurred before durable reservation: checkpoint=%+v err=%v", checkpoint, err)
		}
		content := `{"kind":"standalone","decisiveSignalIds":["frame-01"]}`
		if request.ResponseFormat.JSONSchema.Name == "filler_temporal_role" {
			content = `{"kind":"commercial","decisiveSignalIds":["transcript-01"]}`
		}
		calls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "generation", "model": model,
			"choices": []any{map[string]any{"message": map[string]any{"content": content, "reasoning": ""}}},
			"usage":   map[string]any{"prompt_tokens": 100, "completion_tokens": 20, "cost": 0.001},
			"openrouter_metadata": map[string]any{
				"attempt":   1,
				"attempts":  []any{map[string]any{"provider": provider, "model": model, "status": 200}},
				"endpoints": map[string]any{"available": []any{map[string]any{"provider": provider, "model": model, "selected": true}}},
			},
		})
	}))
	defer server.Close()

	result, err := RunOpenRouterTemporalAssessment(t.Context(), OpenRouterTemporalConfig{
		PackagePath: packagePath, SelectionPath: selectionPath, CheckpointDir: checkpointDir,
		BaseURL: server.URL + "/api/v1", APIKey: "test-key", Snapshot: openRouterReviewSnapshot(server.URL+"/api/v1", now),
		Model: model, ModelFamily: "qwen3.8", UpstreamProvider: provider, UpstreamProviderSlug: slug, AssessorID: "hosted-calibrator",
		ExpectedPackageCases: 1, ExpectedCalibrationCases: 1, PerCaseTimeout: time.Second,
		MaxRequests: 2, MaxSpendNanoUSD: 4_000_000, MaxChargeNanoUSD: 2_000_000,
		AllowInsecureTestURL: true, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 || result.Requests != 2 || result.ChargedNanoUSD != 2_000_000 || result.ConsumedNanoUSD != 2_000_000 || result.UnknownChargeReservations != 0 {
		t.Fatalf("accounting = %+v calls=%d", result, calls.Load())
	}
	if len(result.AssessmentSet.Assessments) != 1 || result.AssessmentSet.Assessments[0].Unit.Kind != fillereval.UnitStandalone || result.AssessmentSet.Assessments[0].Role.Kind != fillereval.TemporalRoleCommercial || !strings.Contains(result.AssessmentSet.Assessments[0].Unit.Reason, "Hosted unit class standalone") {
		t.Fatalf("assessment = %+v", result.AssessmentSet.Assessments)
	}
	if len(result.Attempts) != 2 || result.Attempts[0].Axis != "unit" || result.Attempts[1].Axis != "role" || result.Attempts[0].State != temporalOpenRouterAttemptAccepted || result.AssessmentSet.Assessor.ModelDigest != result.CapabilitySnapshotSHA256 {
		t.Fatalf("attempt binding = %+v", result)
	}
	if _, err := os.Stat(filepath.Join(checkpointDir, openRouterActiveRunLockFilename)); !os.IsNotExist(err) {
		t.Fatalf("active lock survived normal return: %v", err)
	}
	info, err := os.Stat(filepath.Join(checkpointDir, temporalOpenRouterCheckpointFilename))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("checkpoint mode = %v err=%v", info.Mode(), err)
	}
}

func TestRunOpenRouterTemporalModelAssessmentUsesCompleteFreshPackage(t *testing.T) {
	const (
		model    = "review/vendor-model"
		provider = "Provider Route"
		slug     = "provider/route"
	)
	packagePath := writeTemporalModelInferenceFixture(t)
	now := time.Unix(15_000, 0).UTC()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request openRouterStructuredRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		content := `{"kind":"standalone","decisiveSignalIds":["frame-01"]}`
		if request.ResponseFormat.JSONSchema.Name == "filler_temporal_role" {
			content = `{"kind":"commercial","decisiveSignalIds":["transcript-01"]}`
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "generation", "model": model,
			"choices": []any{map[string]any{"message": map[string]any{"content": content, "reasoning": ""}}},
			"usage":   map[string]any{"prompt_tokens": 100, "completion_tokens": 20, "cost": 0.001},
			"openrouter_metadata": map[string]any{
				"attempt": 1, "attempts": []any{map[string]any{"provider": provider, "model": model, "status": 200}},
				"endpoints": map[string]any{"available": []any{map[string]any{"provider": provider, "model": model, "selected": true}}},
			},
		})
	}))
	defer server.Close()

	result, err := RunOpenRouterTemporalModelAssessment(t.Context(), OpenRouterTemporalConfig{
		PackagePath: packagePath, CheckpointDir: filepath.Join(t.TempDir(), "private"),
		BaseURL: server.URL, APIKey: "test-key", Snapshot: openRouterReviewSnapshot(server.URL, now),
		Model: model, ModelFamily: "qwen3.8", UpstreamProvider: provider, UpstreamProviderSlug: slug, AssessorID: "panel-a-model",
		ExpectedPackageCases: 1, ExpectedCalibrationCases: 1, PerCaseTimeout: time.Second,
		MaxRequests: 2, MaxSpendNanoUSD: 4_000_000, MaxChargeNanoUSD: 2_000_000,
		AllowInsecureTestURL: true, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.AssessmentSet.BatchID != "model-panel-test" || len(result.AssessmentSet.Assessments) != 1 || result.AssessmentSet.Assessments[0].Alias != "model-00000000000000000000000000000001" {
		t.Fatalf("model-panel result = %+v", result)
	}
	if err := ValidateOpenRouterTemporalModelResult(result, packagePath, 1); err != nil {
		t.Fatalf("validate model-panel result: %v", err)
	}
}

func TestTemporalClaimSchemaUsesPortableStructuredOutputSubset(t *testing.T) {
	item := TemporalReviewCase{
		Frames:             []TemporalReviewFrame{{ID: "frame-01", OCRSignalID: "ocr-01"}},
		TranscriptSegments: []TemporalReviewTranscript{{ID: "transcript-01"}},
	}
	schema := temporalHostedUnitSchema(item)
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties = %#v", schema["properties"])
	}
	if _, freeText := properties["reason"]; freeText {
		t.Fatalf("provider-facing schema exposes free-form reason: %#v", properties)
	}
	references, ok := properties["decisiveSignalIds"].(map[string]any)
	if !ok {
		t.Fatalf("decisiveSignalIds = %#v", properties["decisiveSignalIds"])
	}
	if _, unsupported := references["uniqueItems"]; unsupported {
		t.Fatalf("provider-facing schema contains unsupported uniqueItems: %#v", references)
	}
	if references["minItems"] != 1 || references["maxItems"] != 4 {
		t.Fatalf("reference bounds = %#v", references)
	}
	items, ok := references["items"].(map[string]any)
	if !ok {
		t.Fatalf("reference items = %#v", references["items"])
	}
	got, ok := items["enum"].([]string)
	if !ok || strings.Join(got, ",") != "frame-01,ocr-01,transcript-01" {
		t.Fatalf("reference enum = %#v", items["enum"])
	}
}

func TestTemporalStructureSchemaUsesPortableStructuredOutputSubset(t *testing.T) {
	schema := temporalStructureOpenRouterSchema(60_000)
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties = %#v", schema["properties"])
	}
	for _, field := range []string{"unitDecisiveAtMs", "roleDecisiveAtMs"} {
		times, ok := properties[field].(map[string]any)
		if !ok {
			t.Fatalf("%s = %#v", field, properties[field])
		}
		if _, unsupported := times["uniqueItems"]; unsupported {
			t.Fatalf("provider-facing %s contains unsupported uniqueItems: %#v", field, times)
		}
		if times["maxItems"] != temporalStructureMaximumDecisiveTimes {
			t.Fatalf("%s bounds = %#v", field, times)
		}
	}
}

func TestRunOpenRouterTemporalAssessmentTurnsSettledInvalidClaimIntoOperationalFailure(t *testing.T) {
	packagePath, selectionPath := writeTemporalCalibrationFixture(t)
	now := time.Unix(20_000, 0).UTC()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "generation", "model": "review/vendor-model",
			"choices":             []any{map[string]any{"message": map[string]any{"content": `{"kind":"invented","decisiveSignalIds":["frame-01"]}`}}},
			"usage":               map[string]any{"prompt_tokens": 100, "completion_tokens": 20, "cost": 0.001},
			"openrouter_metadata": map[string]any{"attempt": 1, "endpoints": map[string]any{"available": []any{map[string]any{"provider": "Provider Route", "model": "review/vendor-model", "selected": true}}}, "attempts": []any{map[string]any{"provider": "Provider Route", "model": "review/vendor-model", "status": 200}}},
		})
	}))
	defer server.Close()
	result, err := RunOpenRouterTemporalAssessment(t.Context(), OpenRouterTemporalConfig{
		PackagePath: packagePath, SelectionPath: selectionPath, CheckpointDir: filepath.Join(t.TempDir(), "private"),
		BaseURL: server.URL, APIKey: "test-key", Snapshot: openRouterReviewSnapshot(server.URL, now),
		Model: "review/vendor-model", ModelFamily: "qwen3.8", UpstreamProvider: "Provider Route", UpstreamProviderSlug: "provider/route", AssessorID: "hosted-calibrator",
		ExpectedPackageCases: 1, ExpectedCalibrationCases: 1, PerCaseTimeout: time.Second,
		MaxRequests: 2, MaxSpendNanoUSD: 4_000_000, MaxChargeNanoUSD: 2_000_000, AllowInsecureTestURL: true, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	assessment := result.AssessmentSet.Assessments[0]
	if assessment.OperationalFailure == nil || assessment.OperationalFailure.Code != fillereval.TemporalFailureInvalidResponse || result.Attempts[0].State != temporalOpenRouterAttemptFailed || result.Attempts[0].ChargedNanoUSD != 1_000_000 {
		t.Fatalf("invalid claim was not preserved as settled operational failure: result=%+v", result)
	}
}

func TestRunOpenRouterTemporalAssessmentClassifiesHTTP502AsRetryableProviderFailure(t *testing.T) {
	packagePath, selectionPath := writeTemporalCalibrationFixture(t)
	now := time.Unix(25_000, 0).UTC()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "upstream unavailable", http.StatusBadGateway)
	}))
	defer server.Close()
	result, err := RunOpenRouterTemporalAssessment(t.Context(), OpenRouterTemporalConfig{
		PackagePath: packagePath, SelectionPath: selectionPath, CheckpointDir: filepath.Join(t.TempDir(), "private"),
		BaseURL: server.URL, APIKey: "test-key", Snapshot: openRouterReviewSnapshot(server.URL, now),
		Model: "review/vendor-model", ModelFamily: "qwen3.8", UpstreamProvider: "Provider Route", UpstreamProviderSlug: "provider/route", AssessorID: "hosted-calibrator",
		ExpectedPackageCases: 1, ExpectedCalibrationCases: 1, PerCaseTimeout: time.Second,
		MaxRequests: 1, MaxSpendNanoUSD: 2_000_000, MaxChargeNanoUSD: 2_000_000, AllowInsecureTestURL: true, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	assessment := result.AssessmentSet.Assessments[0]
	if assessment.OperationalFailure == nil || assessment.OperationalFailure.Code != fillereval.TemporalFailureProvider || !assessment.OperationalFailure.Retryable || result.UnknownChargeReservations != 1 || result.ConsumedNanoUSD != 2_000_000 {
		t.Fatalf("502 was not retained as a retryable provider failure with conservative spend: result=%+v", result)
	}
}

func TestRunOpenRouterTemporalAssessmentRecordsPreRequestBudgetExhaustion(t *testing.T) {
	packagePath, selectionPath := writeTemporalCalibrationFixture(t)
	now := time.Unix(30_000, 0).UTC()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "generation", "model": "review/vendor-model",
			"choices":             []any{map[string]any{"message": map[string]any{"content": `{"kind":"standalone","decisiveSignalIds":["frame-01"]}`}}},
			"usage":               map[string]any{"prompt_tokens": 100, "completion_tokens": 20, "cost": 0.001},
			"openrouter_metadata": map[string]any{"attempt": 1, "endpoints": map[string]any{"available": []any{map[string]any{"provider": "Provider Route", "model": "review/vendor-model", "selected": true}}}, "attempts": []any{map[string]any{"provider": "Provider Route", "model": "review/vendor-model", "status": 200}}},
		})
	}))
	defer server.Close()
	result, err := RunOpenRouterTemporalAssessment(t.Context(), OpenRouterTemporalConfig{
		PackagePath: packagePath, SelectionPath: selectionPath, CheckpointDir: filepath.Join(t.TempDir(), "private"),
		BaseURL: server.URL, APIKey: "test-key", Snapshot: openRouterReviewSnapshot(server.URL, now),
		Model: "review/vendor-model", ModelFamily: "qwen3.8", UpstreamProvider: "Provider Route", UpstreamProviderSlug: "provider/route", AssessorID: "hosted-calibrator",
		ExpectedPackageCases: 1, ExpectedCalibrationCases: 1, PerCaseTimeout: time.Second,
		MaxRequests: 1, MaxSpendNanoUSD: 2_000_000, MaxChargeNanoUSD: 2_000_000, AllowInsecureTestURL: true, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	assessment := result.AssessmentSet.Assessments[0]
	if calls.Load() != 1 || result.Requests != 1 || assessment.OperationalFailure == nil || assessment.OperationalFailure.Code != fillereval.TemporalFailureContextExhausted || len(assessment.Inference.Calls) != 2 {
		t.Fatalf("budget exhaustion was not retained without a second provider call: calls=%d result=%+v", calls.Load(), result)
	}
}

func writeTemporalCalibrationFixture(t *testing.T) (string, string) {
	t.Helper()
	packagePath := writeTemporalTestPackage(t)
	pack, _, packageSHA256, err := LoadTemporalReviewPackage(packagePath, 1)
	if err != nil {
		t.Fatal(err)
	}
	selection := fillereval.TemporalCalibrationSelection{
		SchemaVersion: fillereval.TemporalCalibrationSelectionSchemaVersion, ContractVersion: fillereval.TemporalCalibrationSelectionContractVersion,
		BatchID: pack.BatchID, PackageSHA256: packageSHA256,
		FirstAssessmentSHA256: strings.Repeat("1", 64), SecondAssessmentSHA256: strings.Repeat("2", 64), ComparisonSHA256: strings.Repeat("3", 64),
		Cases: []fillereval.TemporalCalibrationCase{{Alias: "opaque", Reasons: []string{"agreement_control"}, Strata: []string{"agreement:standalone:commercial"}}},
	}
	raw, err := json.Marshal(selection)
	if err != nil {
		t.Fatal(err)
	}
	selectionPath := filepath.Join(filepath.Dir(packagePath), "calibration-selection.json")
	if err := os.WriteFile(selectionPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return packagePath, selectionPath
}

func writeTemporalModelInferenceFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	alias := "model-00000000000000000000000000000001"
	caseRoot := filepath.Join(root, "cases", alias)
	if err := os.MkdirAll(caseRoot, 0o750); err != nil {
		t.Fatal(err)
	}
	frame := []byte("synthetic model frame")
	if err := os.WriteFile(filepath.Join(caseRoot, "frame-01.jpg"), frame, 0o600); err != nil {
		t.Fatal(err)
	}
	pack := TemporalModelReviewPackage{
		SchemaVersion: TemporalModelReviewSchemaVersion, ContractVersion: TemporalModelReviewContractVersion,
		QuestionVersion: TemporalHumanReviewQuestionVersion, EvidenceViewVersion: TemporalModelReviewEvidenceViewVersion,
		PanelSlot: "panel-a", BatchID: "model-panel-test", PreparedAt: time.Unix(10, 0).UTC(),
		EvidenceManifestSHA256: strings.Repeat("a", 64), SelectionSHA256: strings.Repeat("b", 64), SeedSHA256: temporalTruthHash([]byte("model-seed")),
		Cases: []TemporalReviewCase{{
			Alias: alias, DurationMS: 1_000,
			Frames: []TemporalReviewFrame{{
				ID: "frame-01", Path: filepath.ToSlash(filepath.Join("cases", alias, "frame-01.jpg")),
				SHA256: hashBytes(frame), Bytes: int64(len(frame)), Width: 16, Height: 9, AtMS: 100,
			}},
			TranscriptSegments: []TemporalReviewTranscript{{ID: "transcript-01", StartMS: 200, EndMS: 400, Text: "Buy now"}},
		}},
	}
	raw, err := json.Marshal(pack)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "manifest.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
