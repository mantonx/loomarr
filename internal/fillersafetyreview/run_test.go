package fillersafetyreview

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillersafetycert"
)

func TestRunOpenRouterRejectsNilContext(t *testing.T) {
	t.Parallel()
	var requests atomic.Int64
	endpoint := newReviewTestEndpoint(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	fixture := newReviewFixture(t, endpoint.baseURL)
	if _, err := runOpenRouter(nil, fixture.config, fixture.runtime(endpoint.client, endpoint.baseURL)); err == nil || !strings.Contains(err.Error(), "active context") {
		t.Fatalf("err=%v", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("provider requests=%d", requests.Load())
	}
	if _, err := os.Stat(fixture.checkpointDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("checkpoint stat err=%v", err)
	}
	if _, err := os.Stat(fixture.outputPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("output stat err=%v", err)
	}
}

func TestRunOpenRouterPublishesExhaustiveEvidenceBoundReview(t *testing.T) {
	t.Parallel()
	var calls, active, maximumActive atomic.Int64
	endpoint := newReviewTestEndpoint(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		current := active.Add(1)
		defer active.Add(-1)
		for previous := maximumActive.Load(); current > previous && !maximumActive.CompareAndSwap(previous, current); previous = maximumActive.Load() {
		}
		call := calls.Add(1)
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Error(err)
			return
		}
		assertLockedRequest(t, request, body)
		positive := strings.Contains(string(body), "positive_candidate")
		observation := verifiedObservation(positive)
		if call == fillersafetycert.MinimumPositiveFamilies+1 {
			observation = modelObservation{
				Verdict: "rejected", Audibility: "clear", MatchedRuleIDs: []string{testRuleID},
				ConfirmedIntervalIndexes: []int{},
			}
		}
		writeReviewResponse(t, writer, call, observation)
	}))
	fixture := newReviewFixture(t, endpoint.baseURL)
	runtime := fixture.runtime(endpoint.client, endpoint.baseURL)

	first, err := runOpenRouter(t.Context(), fixture.config, runtime)
	if err != nil {
		t.Fatal(err)
	}
	if first.Cases != fillersafetycert.MinimumPositiveFamilies+fillersafetycert.MinimumCleanFamilies ||
		first.Requests != first.Cases || first.Rejected != 1 || first.ChargedNanoUSD != int64(first.Requests)*1_000_000 ||
		len(first.ReviewSHA256) != 64 || calls.Load() != int64(first.Cases) || maximumActive.Load() != 1 {
		t.Fatalf("result=%+v calls=%d maximum_active=%d", first, calls.Load(), maximumActive.Load())
	}
	review, raw, err := readPrivateJSON[fillersafetycert.AuthorityReview](fixture.outputPath, maximumDocumentBytes)
	if err != nil {
		t.Fatal(err)
	}
	if review.Assessments[fillersafetycert.MinimumPositiveFamilies].Decision != fillersafetycert.ReviewDecisionRejected ||
		review.ModelEvidence == nil || len(review.ModelEvidence.Attempts) != first.Requests ||
		review.ModelEvidence.SnapshotSHA256 == "" || review.EvidenceSHA256 == "" {
		t.Fatalf("review=%+v", review)
	}
	for _, secret := range []string{"restricted token", "secret", "cases/case-001/source.mp4"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("published review leaked %q", secret)
		}
	}
	info, err := os.Stat(fixture.outputPath)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("output info=%v err=%v", info, err)
	}

	second, err := runOpenRouter(t.Context(), fixture.config, runtime)
	if err != nil {
		t.Fatal(err)
	}
	if second != first || calls.Load() != int64(first.Cases) {
		t.Fatalf("completed replay changed result or repeated HTTP: first=%+v second=%+v calls=%d", first, second, calls.Load())
	}
}

func TestIndependentModelReviewsFeedAuthorityLock(t *testing.T) {
	t.Parallel()
	endpoint := newReviewTestEndpoint(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Error(err)
			return
		}
		positive := strings.Contains(string(body), "positive_candidate")
		requestedModel := "vendor/reviewer-model"
		resolvedModel := "vendor/reviewer-model-2026"
		if strings.Contains(string(body), `"model":"vendor/reviewer-model-two"`) {
			requestedModel = "vendor/reviewer-model-two"
			resolvedModel = "vendor/reviewer-model-two-2026"
		}
		writeReviewResponseForModel(t, writer, 1, requestedModel, resolvedModel, verifiedObservation(positive))
	}))
	first := newReviewFixture(t, endpoint.baseURL)
	if _, err := runOpenRouter(t.Context(), first.config, first.runtime(endpoint.client, endpoint.baseURL)); err != nil {
		t.Fatal(err)
	}

	plan, _, err := readPrivateJSON[Plan](first.planPath, maximumPlanBytes)
	if err != nil {
		t.Fatal(err)
	}
	secondSnapshot := fixtureSnapshotForModel(
		endpoint.baseURL, first.now.Add(-time.Hour), "vendor/reviewer-model-two", "vendor/reviewer-model-two-2026",
	)
	secondSnapshotPath := filepath.Join(first.root, "reviewer-two-snapshot.json")
	secondSnapshotRaw := marshalPrivateJSON(t, secondSnapshotPath, secondSnapshot)
	plan.ReviewerID = "primary-model-two"
	plan.ModelFamily = "independent-review-family-two"
	plan.Model = "vendor/reviewer-model-two"
	plan.ResolvedModel = "vendor/reviewer-model-two-2026"
	plan.Snapshot = testAuthority("reviewer-two-snapshot.json", secondSnapshotRaw)
	secondPlanPath := filepath.Join(filepath.Dir(first.planPath), "review-plan-two.json")
	marshalPrivateJSON(t, secondPlanPath, plan)
	second := first
	second.planPath = secondPlanPath
	second.checkpointDir = filepath.Join(filepath.Dir(first.checkpointDir), "checkpoint-two")
	second.outputPath = filepath.Join(filepath.Dir(first.outputPath), "review-two.json")
	second.config.PlanPath = second.planPath
	second.config.CheckpointDirectory = second.checkpointDir
	second.config.OutputPath = second.outputPath
	if _, err := runOpenRouter(t.Context(), second.config, second.runtime(endpoint.client, endpoint.baseURL)); err != nil {
		t.Fatal(err)
	}

	seedPath := filepath.Join(filepath.Dir(first.root), "authority-seed.bin")
	writePrivateTestFile(t, seedPath, []byte("0123456789abcdef0123456789abcdef"))
	authorityPath := filepath.Join(filepath.Dir(first.root), "authority.json")
	result, err := fillersafetycert.BuildAuthority(t.Context(), fillersafetycert.AuthorityBuildConfig{
		DraftPath: filepath.Join(first.root, "draft.json"), FirstReviewPath: first.outputPath,
		SecondReviewPath: second.outputPath, SeedPath: seedPath, SourceRoot: first.root,
		AuthoredAt: first.now.Add(time.Hour), ExpectedCases: plan.ExpectedCases,
		MaximumSourceBytes: 1 << 20,
		ValidateEvidence: func(_, _ []byte, item fillersafetycert.AuthorityDraftCase, _ time.Time) error {
			if item.SourceAuthority.SourceID != item.CaseID {
				return fmt.Errorf("fixture source is unbound")
			}
			return nil
		},
		OutputPath: authorityPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Cases != plan.ExpectedCases || len(result.AuthoritySHA256) != 64 {
		t.Fatalf("authority result=%+v", result)
	}
}

func TestRunOpenRouterResumesSettledFailureWithoutReplayingAcceptedCases(t *testing.T) {
	t.Parallel()
	var calls atomic.Int64
	endpoint := newReviewTestEndpoint(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		call := calls.Add(1)
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Error(err)
			return
		}
		positive := strings.Contains(string(body), "positive_candidate")
		observation := verifiedObservation(positive)
		if call == 2 {
			observation = modelObservation{
				Verdict: "unclear", Audibility: "degraded",
				MatchedRuleIDs: []string{}, ConfirmedIntervalIndexes: []int{},
			}
		}
		writeReviewResponse(t, writer, call, observation)
	}))
	fixture := newReviewFixture(t, endpoint.baseURL)
	runtime := fixture.runtime(endpoint.client, endpoint.baseURL)

	if _, err := runOpenRouter(t.Context(), fixture.config, runtime); err == nil ||
		!strings.Contains(err.Error(), "unclear") {
		t.Fatalf("first run err=%v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("first run calls=%d", calls.Load())
	}
	result, err := runOpenRouter(t.Context(), fixture.config, runtime)
	if err != nil {
		t.Fatal(err)
	}
	if result.Requests != result.Cases+1 || calls.Load() != int64(result.Requests) {
		t.Fatalf("result=%+v calls=%d", result, calls.Load())
	}
	state, _, err := readPrivateJSON[checkpoint](fixture.checkpointDir+"/"+checkpointFilename, maximumDocumentBytes)
	if err != nil {
		t.Fatal(err)
	}
	if state.Attempts[0].CaseID != "case-001" || state.Attempts[1].CaseID != "case-002" ||
		state.Attempts[1].State != attemptFailed || state.Attempts[2].CaseID != "case-002" ||
		state.Attempts[2].Attempt != 2 || state.Attempts[2].State != attemptAccepted {
		t.Fatalf("attempts=%+v", state.Attempts[:3])
	}
}

func TestRunOpenRouterRefusesUnsettledRequestWithoutRetry(t *testing.T) {
	t.Parallel()
	var calls atomic.Int64
	endpoint := newReviewTestEndpoint(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writer.WriteHeader(http.StatusBadGateway)
		_, _ = writer.Write([]byte(`{"error":"unavailable"}`))
	}))
	fixture := newReviewFixture(t, endpoint.baseURL)
	runtime := fixture.runtime(endpoint.client, endpoint.baseURL)

	if _, err := runOpenRouter(t.Context(), fixture.config, runtime); err == nil {
		t.Fatal("first run unexpectedly succeeded")
	}
	if _, err := runOpenRouter(t.Context(), fixture.config, runtime); err == nil ||
		!strings.Contains(err.Error(), "unsettled prior request") {
		t.Fatalf("second run err=%v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("unsettled request was replayed: calls=%d", calls.Load())
	}
}

func TestRunOpenRouterRejectsSourceDriftBeforeHTTP(t *testing.T) {
	t.Parallel()
	var calls atomic.Int64
	endpoint := newReviewTestEndpoint(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	fixture := newReviewFixture(t, endpoint.baseURL)
	path := fixture.root + "/cases/case-001/source.mp4"
	if err := os.WriteFile(path, []byte("changed-media-001"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := runOpenRouter(t.Context(), fixture.config, fixture.runtime(endpoint.client, endpoint.baseURL)); err == nil ||
		!strings.Contains(err.Error(), "source") {
		t.Fatalf("err=%v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("provider called before source verification: %d", calls.Load())
	}
}

func TestRunOpenRouterRejectsExistingOutputBeforeHTTP(t *testing.T) {
	t.Parallel()
	var calls atomic.Int64
	endpoint := newReviewTestEndpoint(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	fixture := newReviewFixture(t, endpoint.baseURL)
	writePrivateTestFile(t, fixture.outputPath, []byte("prior output"))

	if _, err := runOpenRouter(t.Context(), fixture.config, fixture.runtime(endpoint.client, endpoint.baseURL)); err == nil ||
		!strings.Contains(err.Error(), "already exists") {
		t.Fatalf("err=%v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("provider called before output validation: %d", calls.Load())
	}
}

func verifiedObservation(positive bool) modelObservation {
	if positive {
		return modelObservation{
			Verdict: "verified", Audibility: "clear", MatchedRuleIDs: []string{testRuleID},
			ConfirmedIntervalIndexes: []int{0},
		}
	}
	return modelObservation{
		Verdict: "verified", Audibility: "no_speech",
		MatchedRuleIDs: []string{}, ConfirmedIntervalIndexes: []int{},
	}
}

func assertLockedRequest(t *testing.T, request *http.Request, body []byte) {
	t.Helper()
	if request.Header.Get("Authorization") != "Bearer secret" ||
		request.Header.Get("X-OpenRouter-Metadata") != "enabled" ||
		!strings.Contains(string(body), `"allow_fallbacks":false`) ||
		!strings.Contains(string(body), `"data_collection":"deny"`) ||
		!strings.Contains(string(body), `"zdr":true`) ||
		!strings.Contains(string(body), `"type":"input_audio"`) ||
		!strings.Contains(string(body), `"strict":true`) {
		t.Errorf("request is not route-locked: headers=%v body=%s", request.Header, body)
	}
}

func writeReviewResponse(t *testing.T, writer http.ResponseWriter, call int64, observation modelObservation) {
	writeReviewResponseForModel(t, writer, call, "vendor/reviewer-model", "vendor/reviewer-model-2026", observation)
}

func writeReviewResponseForModel(
	t *testing.T,
	writer http.ResponseWriter,
	call int64,
	requestedModel string,
	resolvedModel string,
	observation modelObservation,
) {
	t.Helper()
	structured, err := json.Marshal(observation)
	if err != nil {
		t.Fatal(err)
	}
	response := map[string]any{
		"id": "generation-" + fmt.Sprint(call), "model": requestedModel,
		"choices": []any{map[string]any{"message": map[string]any{"content": string(structured)}}},
		"usage":   map[string]any{"prompt_tokens": 10, "completion_tokens": 2, "cost": 0.001},
		"openrouter_metadata": map[string]any{
			"attempt": 1,
			"endpoints": map[string]any{"available": []any{map[string]any{
				"provider": "Pinned Provider", "model": resolvedModel, "selected": true,
			}}},
		},
	}
	if err := json.NewEncoder(writer).Encode(response); err != nil {
		t.Error(err)
	}
}
