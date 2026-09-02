package fillerreview

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillerbakeoff"
	"github.com/loomarr/loomarr/internal/fillereval"
	"github.com/loomarr/loomarr/internal/testkit/httpfixture"
)

func TestRunOpenRouterReviewRejectsStaleSnapshotBeforeTransport(t *testing.T) {
	retrievedAt := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	var requests atomic.Int32
	client := &http.Client{Transport: httpfixture.RoundTripperFunc(func(*http.Request) (*http.Response, error) {
		requests.Add(1)
		return nil, fmt.Errorf("transport must not be reached")
	})}

	_, _, err := RunOpenRouterReview(context.Background(), OpenRouterReviewConfig{
		PackageDir: t.TempDir(), CheckpointDir: filepath.Join(t.TempDir(), "private-review-state"),
		BaseURL: fillerbakeoff.OpenRouterBaseURL, APIKey: "test-only", Snapshot: openRouterReviewSnapshot(fillerbakeoff.OpenRouterBaseURL, retrievedAt),
		Model: "review/vendor-model", UpstreamProvider: "Provider Route", UpstreamProviderSlug: "provider/route", ReviewerID: "hosted-reviewer-b",
		ExpectedCases: 1, PerCaseTimeout: time.Second, MaxRequests: 2, MaxSpendNanoUSD: 4_000_000, MaxChargeNanoUSD: 2_000_000,
		Client: client, Now: func() time.Time { return retrievedAt.Add(25 * time.Hour) },
	})
	if err == nil || !strings.Contains(err.Error(), "24-hour window") || requests.Load() != 0 {
		t.Fatalf("error=%v requests=%d", err, requests.Load())
	}
}

func TestRunOpenRouterReviewAllowsOnlyOneConcurrentWriter(t *testing.T) {
	packageDir, transcript := reviewPackageFixture(t)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		generation := requests.Add(1)
		content := `{"disposition":"eligible","contentRole":"commercial","taxonomy":{"product":["cola"]},"policyFlags":[],"slices":["commercial"],"evidence":[{"id":"frame-01","kind":"frame","claim":"content_role","value":"commercial","provenance":"cases/review-one/frame-01.jpg","atMs":1000}],"reviewQuestion":""}`
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": fmt.Sprintf("generation-%d", generation), "model": "review/vendor-model",
			"choices":             []any{map[string]any{"message": map[string]string{"content": content}}},
			"usage":               map[string]any{"prompt_tokens": 200, "completion_tokens": 50, "cost": 0.001},
			"openrouter_metadata": map[string]any{"attempt": 1, "endpoints": map[string]any{"available": []any{map[string]any{"provider": "Provider Route", "model": "review/vendor-model", "selected": true}}}},
		})
	}))
	defer server.Close()

	fixedNow := time.Date(2026, 8, 27, 22, 0, 0, 0, time.UTC)
	var clockCalls atomic.Int32
	var checkpointDirCreateCalls atomic.Int32
	snapshotBarrier := make(chan struct{})
	reservationBarrier := make(chan struct{})
	checkpointDirCreateBarrier := make(chan struct{})
	beforeCheckpointDirCreate := func() {
		switch checkpointDirCreateCalls.Add(1) {
		case 1:
			select {
			case <-checkpointDirCreateBarrier:
			case <-time.After(time.Second):
			}
		case 2:
			close(checkpointDirCreateBarrier)
		}
	}
	now := func() time.Time {
		switch clockCalls.Add(1) {
		case 1:
			select {
			case <-snapshotBarrier:
			case <-time.After(time.Second):
			}
		case 2:
			close(snapshotBarrier)
		case 3:
			select {
			case <-reservationBarrier:
			case <-time.After(100 * time.Millisecond):
			}
		case 4:
			close(reservationBarrier)
		}
		return fixedNow
	}
	config := OpenRouterReviewConfig{
		PackageDir: packageDir, CheckpointDir: filepath.Join(t.TempDir(), "private-review-state"), Transcripts: []fillerbakeoff.TranscriptArtifact{transcript},
		BaseURL: server.URL, APIKey: "test-key", Snapshot: openRouterReviewSnapshot(server.URL, fixedNow),
		Model: "review/vendor-model", UpstreamProvider: "Provider Route", UpstreamProviderSlug: "provider/route", ReviewerID: "hosted-reviewer-b",
		ExpectedCases: 1, PerCaseTimeout: time.Second, MaxRequests: 2, MaxSpendNanoUSD: 4_000_000, MaxChargeNanoUSD: 2_000_000,
		AllowInsecureTestURL: true, Now: now,
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			_, _, err := runOpenRouterReview(context.Background(), config, beforeCheckpointDirCreate)
			results <- err
		}()
	}
	close(start)
	errors := []error{<-results, <-results}
	successes := 0
	lockFailures := 0
	for _, err := range errors {
		if err == nil {
			successes++
		} else if strings.Contains(err.Error(), "active run lock") {
			lockFailures++
		}
	}
	if got := requests.Load(); got != 1 || successes != 1 || lockFailures != 1 {
		t.Fatalf("requests=%d successes=%d lockFailures=%d errors=%v", got, successes, lockFailures, errors)
	}
	if _, _, err := RunOpenRouterReview(context.Background(), config); err != nil {
		t.Fatalf("normal return retained its active run lock: %v", err)
	}
	if requests.Load() != 1 {
		t.Fatalf("completed checkpoint was requested again: %d", requests.Load())
	}
}

func TestEnsureOpenRouterCheckpointDirRevalidatesConcurrentExistingPath(t *testing.T) {
	tests := map[string]func(*testing.T, string){
		"symlink": func(t *testing.T, path string) {
			t.Helper()
			target := filepath.Join(t.TempDir(), "target")
			if err := os.Mkdir(target, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}
		},
		"regular file": func(t *testing.T, path string) {
			t.Helper()
			if err := os.WriteFile(path, []byte("not a directory"), 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"permissive directory": func(t *testing.T, path string) {
			t.Helper()
			if err := os.Mkdir(path, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, 0o755); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, create := range tests {
		t.Run(name, func(t *testing.T) {
			checkpointDir := filepath.Join(t.TempDir(), "private-review-state")
			err := ensureOpenRouterCheckpointDirBeforeCreate(checkpointDir, func() { create(t, checkpointDir) })
			if err == nil || !strings.Contains(err.Error(), "must be private and not a symlink") {
				t.Fatalf("checkpoint directory error = %v", err)
			}
		})
	}
}

func TestRunOpenRouterReviewCrashStaleLockRequiresExplicitRecovery(t *testing.T) {
	packageDir, transcript := reviewPackageFixture(t)
	checkpointDir := filepath.Join(t.TempDir(), "private-review-state")
	if err := os.Mkdir(checkpointDir, 0o700); err != nil {
		t.Fatal(err)
	}
	stale := []byte("crash-stale-active-run\n")
	lockPath := filepath.Join(checkpointDir, openRouterActiveRunLockFilename)
	if err := os.WriteFile(lockPath, stale, 0o600); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		content := `{"disposition":"eligible","contentRole":"commercial","taxonomy":{"product":["cola"]},"policyFlags":[],"slices":["commercial"],"evidence":[{"id":"frame-01","kind":"frame","claim":"content_role","value":"commercial","provenance":"cases/review-one/frame-01.jpg","atMs":1000}],"reviewQuestion":""}`
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "generation-1", "model": "review/vendor-model",
			"choices":             []any{map[string]any{"message": map[string]string{"content": content}}},
			"usage":               map[string]any{"prompt_tokens": 200, "completion_tokens": 50, "cost": 0.001},
			"openrouter_metadata": map[string]any{"attempt": 1, "endpoints": map[string]any{"available": []any{map[string]any{"provider": "Provider Route", "model": "review/vendor-model", "selected": true}}}},
		})
	}))
	defer server.Close()
	now := time.Date(2026, 8, 27, 22, 0, 0, 0, time.UTC)
	config := OpenRouterReviewConfig{
		PackageDir: packageDir, CheckpointDir: checkpointDir, Transcripts: []fillerbakeoff.TranscriptArtifact{transcript},
		BaseURL: server.URL, APIKey: "test-key", Snapshot: openRouterReviewSnapshot(server.URL, now),
		Model: "review/vendor-model", UpstreamProvider: "Provider Route", UpstreamProviderSlug: "provider/route", ReviewerID: "hosted-reviewer-b",
		ExpectedCases: 1, PerCaseTimeout: time.Second, MaxRequests: 2, MaxSpendNanoUSD: 4_000_000, MaxChargeNanoUSD: 2_000_000,
		AllowInsecureTestURL: true, Now: func() time.Time { return now },
	}
	if _, _, err := RunOpenRouterReview(context.Background(), config); err == nil || !strings.Contains(err.Error(), "explicit operator recovery") {
		t.Fatalf("stale lock error = %v", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("stale lock allowed %d HTTP requests", requests.Load())
	}
	if _, err := RecoverOpenRouterReviewLock(checkpointDir, strings.Repeat("0", 64)); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("wrong recovery digest error = %v", err)
	}
	recovered, err := RecoverOpenRouterReviewLock(checkpointDir, hashBytes(stale))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(recovered); err != nil {
		t.Fatalf("recovered lock audit = %s: %v", recovered, err)
	}
	if _, _, err := RunOpenRouterReview(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 {
		t.Fatalf("explicitly recovered runner requests = %d", requests.Load())
	}
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("normal return retained active lock: %v", err)
	}
}

func TestRunOpenRouterReviewResumesOnlyFailedAlias(t *testing.T) {
	packageDir, transcript := reviewPackageFixture(t)
	checkpointDir := filepath.Join(t.TempDir(), "private-review-state")
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		content := `{"disposition":"eligible"`
		if requests == 2 {
			content = `{"disposition":"eligible","contentRole":"commercial","taxonomy":{"product":["cola"]},"policyFlags":[],"slices":["commercial"],"evidence":[{"id":"frame-01","kind":"frame","claim":"content_role","value":"commercial","provenance":"cases/review-one/frame-01.jpg","atMs":1000}],"reviewQuestion":""}`
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "generation-" + string(rune('0'+requests)), "model": "review/vendor-model",
			"choices":             []any{map[string]any{"message": map[string]string{"content": content}}},
			"usage":               map[string]any{"prompt_tokens": 200, "completion_tokens": 50, "cost": 0.001},
			"openrouter_metadata": map[string]any{"attempt": 1, "endpoints": map[string]any{"available": []any{map[string]any{"provider": "Provider Route", "model": "review/vendor-model", "selected": true}}}},
		})
	}))
	defer server.Close()

	now := time.Date(2026, 8, 27, 22, 0, 0, 0, time.UTC)
	config := OpenRouterReviewConfig{
		PackageDir: packageDir, CheckpointDir: checkpointDir, Transcripts: []fillerbakeoff.TranscriptArtifact{transcript},
		BaseURL: server.URL, APIKey: "test-key", Snapshot: openRouterReviewSnapshot(server.URL, now),
		Model: "review/vendor-model", UpstreamProvider: "Provider Route", UpstreamProviderSlug: "provider/route", ReviewerID: "hosted-reviewer-a",
		ExpectedCases: 1, PerCaseTimeout: time.Second, MaxRequests: 2, MaxSpendNanoUSD: 4_000_000, MaxChargeNanoUSD: 2_000_000,
		AllowInsecureTestURL: true, Now: func() time.Time { return now },
	}
	if _, _, err := RunOpenRouterReview(context.Background(), config); err == nil || !strings.Contains(err.Error(), "unexpected EOF") {
		t.Fatalf("first run error = %v", err)
	}
	info, err := os.Stat(checkpointDir)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("private checkpoint = %v, %v", info, err)
	}

	run, submissions, err := RunOpenRouterReview(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	if requests != 2 || run.Requests != 2 || run.ChargedNanoUSD != 2_000_000 || len(run.Attempts) != 2 || len(submissions) != 1 {
		t.Fatalf("requests=%d run=%+v submissions=%+v", requests, run, submissions)
	}
}

func TestRunOpenRouterReviewRejectsCheckpointResultHashDrift(t *testing.T) {
	packageDir, transcript := reviewPackageFixture(t)
	checkpointDir := filepath.Join(t.TempDir(), "private-review-state")
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		content := `{"disposition":"eligible","contentRole":"commercial","taxonomy":{"product":["cola"]},"policyFlags":[],"slices":["commercial"],"evidence":[{"id":"frame-01","kind":"frame","claim":"content_role","value":"commercial","provenance":"cases/review-one/frame-01.jpg","atMs":1000}],"reviewQuestion":""}`
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "generation-1", "model": "review/vendor-model",
			"choices":             []any{map[string]any{"message": map[string]string{"content": content}}},
			"usage":               map[string]any{"prompt_tokens": 200, "completion_tokens": 50, "cost": 0.001},
			"openrouter_metadata": map[string]any{"attempt": 1, "endpoints": map[string]any{"available": []any{map[string]any{"provider": "Provider Route", "model": "review/vendor-model", "selected": true}}}},
		})
	}))
	defer server.Close()

	now := time.Date(2026, 8, 27, 22, 0, 0, 0, time.UTC)
	config := OpenRouterReviewConfig{
		PackageDir: packageDir, CheckpointDir: checkpointDir, Transcripts: []fillerbakeoff.TranscriptArtifact{transcript},
		BaseURL: server.URL, APIKey: "test-key", Snapshot: openRouterReviewSnapshot(server.URL, now),
		Model: "review/vendor-model", UpstreamProvider: "Provider Route", UpstreamProviderSlug: "provider/route", ReviewerID: "hosted-reviewer-a",
		ExpectedCases: 1, PerCaseTimeout: time.Second, MaxRequests: 2, MaxSpendNanoUSD: 4_000_000, MaxChargeNanoUSD: 2_000_000,
		AllowInsecureTestURL: true, Now: func() time.Time { return now },
	}
	if _, _, err := RunOpenRouterReview(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	checkpointPath := filepath.Join(checkpointDir, openRouterCheckpointFilename)
	checkpoint, err := readStrictJSON[openRouterCheckpoint](checkpointPath)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint.Submissions[0].Labels.ContentRole = "promo"
	raw, err := json.MarshalIndent(checkpoint, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(checkpointPath, append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, _, err := RunOpenRouterReview(context.Background(), config); err == nil || !strings.Contains(err.Error(), "hash") {
		t.Fatalf("checkpoint drift error = %v", err)
	}
	if requests != 1 {
		t.Fatalf("identity-drifted checkpoint caused %d paid requests", requests)
	}
}

func TestRunOpenRouterReviewPreservesAcceptedCasesAcrossFailedAliasRetry(t *testing.T) {
	packageDir, transcripts := twoCaseOpenRouterReviewFixture(t)
	checkpointDir := filepath.Join(t.TempDir(), "private-review-state")
	requestAliases := make([]string, 0, 3)
	requestSHA256s := make([]string, 0, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		alias := "review-one"
		if strings.Contains(string(raw), `\"alias\":\"review-two\"`) {
			alias = "review-two"
		}
		requestAliases = append(requestAliases, alias)
		requestSHA256s = append(requestSHA256s, hashBytes(raw))
		content := `{"disposition":"eligible"`
		if len(requestAliases) != 2 {
			content = fmt.Sprintf(`{"disposition":"eligible","contentRole":"commercial","taxonomy":{"product":["cola"]},"policyFlags":[],"slices":["commercial"],"evidence":[{"id":"frame-01","kind":"frame","claim":"content_role","value":"commercial","provenance":"cases/%s/frame-01.jpg","atMs":1000}],"reviewQuestion":""}`, alias)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": fmt.Sprintf("generation-%d", len(requestAliases)), "model": "review/vendor-model",
			"choices":             []any{map[string]any{"message": map[string]string{"content": content}}},
			"usage":               map[string]any{"prompt_tokens": 200, "completion_tokens": 50, "cost": 0.001},
			"openrouter_metadata": map[string]any{"attempt": 1, "endpoints": map[string]any{"available": []any{map[string]any{"provider": "Provider Route", "model": "review/vendor-model", "selected": true}}}},
		})
	}))
	defer server.Close()

	now := time.Date(2026, 8, 27, 22, 0, 0, 0, time.UTC)
	config := OpenRouterReviewConfig{
		PackageDir: packageDir, CheckpointDir: checkpointDir, Transcripts: transcripts,
		BaseURL: server.URL, APIKey: "test-key", Snapshot: openRouterReviewSnapshot(server.URL, now),
		Model: "review/vendor-model", UpstreamProvider: "Provider Route", UpstreamProviderSlug: "provider/route", ReviewerID: "hosted-reviewer-b",
		ExpectedCases: 2, PerCaseTimeout: time.Second, MaxRequests: 3, MaxSpendNanoUSD: 6_000_000, MaxChargeNanoUSD: 2_000_000,
		AllowInsecureTestURL: true, Now: func() time.Time { return now },
	}
	if _, _, err := RunOpenRouterReview(context.Background(), config); err == nil || !strings.Contains(err.Error(), "unexpected EOF") {
		t.Fatalf("first run error = %v", err)
	}
	checkpointPath := filepath.Join(checkpointDir, openRouterCheckpointFilename)
	checkpoint, err := readStrictJSON[openRouterCheckpoint](checkpointPath)
	if err != nil {
		t.Fatal(err)
	}
	checkpointInfo, err := os.Stat(checkpointPath)
	if err != nil || checkpointInfo.Mode().Perm() != 0o600 || len(checkpoint.Submissions) != 1 || len(checkpoint.Attempts) != 2 || checkpoint.Attempts[0].RequestSHA256 != requestSHA256s[0] || checkpoint.Attempts[1].RequestSHA256 != requestSHA256s[1] || checkpoint.Identity.PackageManifestSHA256 == "" || checkpoint.Identity.CapabilitySnapshotSHA256 == "" || checkpoint.Identity.PromptSHA256 == "" || checkpoint.Identity.Model != config.Model || checkpoint.Identity.UpstreamProvider != config.UpstreamProvider || checkpoint.Identity.ReviewerID != config.ReviewerID {
		t.Fatalf("checkpoint=%+v mode=%v err=%v", checkpoint, checkpointInfo, err)
	}
	drifted := config
	drifted.ReviewerID = "different-reviewer"
	if _, _, err := RunOpenRouterReview(context.Background(), drifted); err == nil || !strings.Contains(err.Error(), "identity drift") {
		t.Fatalf("identity drift error = %v", err)
	}
	if len(requestAliases) != 2 {
		t.Fatalf("identity drift made a paid request: %v", requestAliases)
	}
	run, submissions, err := RunOpenRouterReview(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(requestAliases, ","); got != "review-one,review-two,review-two" {
		t.Fatalf("requested aliases = %s", got)
	}
	if len(submissions) != 2 || submissions[0].Alias != "review-one" || submissions[1].Alias != "review-two" || run.Requests != 3 || run.ChargedNanoUSD != 3_000_000 || len(run.Attempts) != 3 || run.Attempts[0].State != openRouterAttemptAccepted || run.Attempts[1].State != openRouterAttemptFailed || run.Attempts[2].State != openRouterAttemptAccepted {
		t.Fatalf("run=%+v submissions=%+v", run, submissions)
	}
	completed := filepath.Join(t.TempDir(), "completed-review")
	if err := PublishReview(completed, run, submissions); err != nil {
		t.Fatal(err)
	}
	labels, err := os.ReadFile(filepath.Join(completed, "labels.jsonl"))
	if err != nil || strings.Count(string(labels), "\n") != 2 {
		t.Fatalf("completed labels=%q err=%v", labels, err)
	}
	checkpoint, err = readStrictJSON[openRouterCheckpoint](checkpointPath)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint.Submissions = append(checkpoint.Submissions, checkpoint.Submissions[0])
	checkpoint.Calls = append(checkpoint.Calls, checkpoint.Calls[0])
	raw, err := json.MarshalIndent(checkpoint, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(checkpointPath, append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := RunOpenRouterReview(context.Background(), config); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate checkpoint error = %v", err)
	}
	if len(requestAliases) != 3 {
		t.Fatalf("duplicate checkpoint caused a paid request: %v", requestAliases)
	}
}

func TestPublishReviewRejectsHostedAttemptLedgerHashMismatch(t *testing.T) {
	out := filepath.Join(t.TempDir(), "completed-review")
	reviewedAt := time.Date(2026, 8, 27, 22, 0, 0, 0, time.UTC)
	submissions := []fillereval.LabelSubmission{{
		Alias: "review-one", ReviewerID: "hosted-reviewer-b", BatchID: "blind-a", ReviewedAt: reviewedAt,
		Labels: fillereval.Labels{Truth: fillereval.TruthEligible, ContentRole: "commercial", Slices: []string{"commercial"}, Evidence: []fillereval.Evidence{{ID: "frame-01", Kind: "frame", Claim: "content_role", Value: "commercial", Provenance: "cases/review-one/frame-01.jpg", AtMS: 1000}}},
	}}
	requestOne := strings.Repeat("1", 64)
	requestTwo := strings.Repeat("2", 64)
	run := ReviewRun{
		SchemaVersion: ReviewRunSchemaVersion, BatchID: "blind-a", ReviewerID: "hosted-reviewer-b", Provider: "openrouter", ResolvedModel: "review/vendor-model",
		PackageManifestSHA256: strings.Repeat("4", 64), CapabilitySnapshotSHA256: strings.Repeat("5", 64), PromptVersion: OpenRouterReviewPromptVersion, PromptSHA256: strings.Repeat("6", 64),
		Model: "review/vendor-model", UpstreamProvider: "Provider Route", UpstreamProviderSlug: "provider/route", CompletedAt: reviewedAt,
		Cases: 1, Requests: 2, MaxRequests: 2, PromptTokens: 400, CompletionTokens: 100, ChargedNanoUSD: 2_000_000, MaxSpendNanoUSD: 4_000_000, MaxChargeNanoUSD: 2_000_000,
		Calls: []ReviewCall{{Alias: "review-one", ReviewedAt: reviewedAt, GenerationID: "generation-2", PromptTokens: 200, CompletionTokens: 50, ChargedAmountUSD: "0.001", ChargedNanoUSD: 1_000_000, RequestSHA256: requestTwo, Attempt: 2}},
		Attempts: []ReviewAttempt{
			{Alias: "review-one", Attempt: 1, RequestedAt: reviewedAt, RequestSHA256: requestOne, State: openRouterAttemptFailed, GenerationID: "generation-1", PromptTokens: 200, CompletionTokens: 50, ChargedAmountUSD: "0.001", ChargedNanoUSD: 1_000_000},
			{Alias: "review-one", Attempt: 2, RequestedAt: reviewedAt, RequestSHA256: requestTwo, State: openRouterAttemptAccepted, GenerationID: "generation-2", PromptTokens: 200, CompletionTokens: 50, ChargedAmountUSD: "0.001", ChargedNanoUSD: 1_000_000, SubmissionSHA256: strings.Repeat("3", 64)},
		},
		SubmissionSHA256: submissionSHA256(submissions),
	}

	if err := PublishReview(out, run, submissions); err == nil || !strings.Contains(err.Error(), "attempt") {
		t.Fatalf("publication error = %v", err)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("invalid hosted review was published: %v", err)
	}
}

func TestPublishReviewRejectsAttemptsOutsideSerialSubmissionOrder(t *testing.T) {
	out := filepath.Join(t.TempDir(), "completed-review")
	reviewedAt := time.Date(2026, 8, 27, 22, 0, 0, 0, time.UTC)
	submissions := []fillereval.LabelSubmission{
		{
			Alias: "review-one", ReviewerID: "hosted-reviewer-b", BatchID: "blind-a", ReviewedAt: reviewedAt,
			Labels: fillereval.Labels{Truth: fillereval.TruthEligible, ContentRole: "commercial", Slices: []string{"commercial"}, Evidence: []fillereval.Evidence{{ID: "frame-01", Kind: "frame", Claim: "content_role", Value: "commercial", Provenance: "cases/review-one/frame-01.jpg", AtMS: 1000}}},
		},
		{
			Alias: "review-two", ReviewerID: "hosted-reviewer-b", BatchID: "blind-a", ReviewedAt: reviewedAt,
			Labels: fillereval.Labels{Truth: fillereval.TruthEligible, ContentRole: "commercial", Slices: []string{"commercial"}, Evidence: []fillereval.Evidence{{ID: "frame-01", Kind: "frame", Claim: "content_role", Value: "commercial", Provenance: "cases/review-two/frame-01.jpg", AtMS: 1000}}},
		},
	}
	requestOne := strings.Repeat("1", 64)
	requestTwo := strings.Repeat("2", 64)
	requestThree := strings.Repeat("3", 64)
	run := ReviewRun{
		SchemaVersion: ReviewRunSchemaVersion, BatchID: "blind-a", ReviewerID: "hosted-reviewer-b", Provider: "openrouter",
		PackageManifestSHA256: strings.Repeat("4", 64), CapabilitySnapshotSHA256: strings.Repeat("5", 64), PromptVersion: OpenRouterReviewPromptVersion, PromptSHA256: strings.Repeat("6", 64),
		Model: "review/vendor-model", ResolvedModel: "review/vendor-model", UpstreamProvider: "Provider Route", UpstreamProviderSlug: "provider/route", CompletedAt: reviewedAt,
		Cases: 2, Requests: 3, MaxRequests: 3, PromptTokens: 600, CompletionTokens: 150, ChargedNanoUSD: 3_000_000, MaxSpendNanoUSD: 6_000_000, MaxChargeNanoUSD: 2_000_000,
		Calls: []ReviewCall{
			{Alias: "review-one", ReviewedAt: reviewedAt, GenerationID: "generation-3", PromptTokens: 200, CompletionTokens: 50, ChargedAmountUSD: "0.001", ChargedNanoUSD: 1_000_000, RequestSHA256: requestThree, Attempt: 2},
			{Alias: "review-two", ReviewedAt: reviewedAt, GenerationID: "generation-2", PromptTokens: 200, CompletionTokens: 50, ChargedAmountUSD: "0.001", ChargedNanoUSD: 1_000_000, RequestSHA256: requestTwo, Attempt: 1},
		},
		Attempts: []ReviewAttempt{
			{Alias: "review-one", Attempt: 1, RequestedAt: reviewedAt, RequestSHA256: requestOne, State: openRouterAttemptFailed, GenerationID: "generation-1", PromptTokens: 200, CompletionTokens: 50, ChargedAmountUSD: "0.001", ChargedNanoUSD: 1_000_000},
			{Alias: "review-two", Attempt: 1, RequestedAt: reviewedAt, RequestSHA256: requestTwo, State: openRouterAttemptAccepted, GenerationID: "generation-2", PromptTokens: 200, CompletionTokens: 50, ChargedAmountUSD: "0.001", ChargedNanoUSD: 1_000_000, SubmissionSHA256: submissionSHA256([]fillereval.LabelSubmission{submissions[1]})},
			{Alias: "review-one", Attempt: 2, RequestedAt: reviewedAt, RequestSHA256: requestThree, State: openRouterAttemptAccepted, GenerationID: "generation-3", PromptTokens: 200, CompletionTokens: 50, ChargedAmountUSD: "0.001", ChargedNanoUSD: 1_000_000, SubmissionSHA256: submissionSHA256([]fillereval.LabelSubmission{submissions[0]})},
		},
		SubmissionSHA256: submissionSHA256(submissions),
	}

	if err := PublishReview(out, run, submissions); err == nil || !strings.Contains(err.Error(), "serial order") {
		t.Fatalf("publication error = %v", err)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("out-of-order hosted review was published: %v", err)
	}
}

func TestRunOpenRouterReviewResumeKeepsOriginalCeilings(t *testing.T) {
	t.Run("requests", func(t *testing.T) {
		assertOpenRouterResumeCeiling(t, 2, 6_000_000, 2_000_000)
	})
	t.Run("spend", func(t *testing.T) {
		assertOpenRouterResumeCeiling(t, 3, 2_500_000, 1_500_000)
	})
}

func assertOpenRouterResumeCeiling(t *testing.T, maxRequests int, maxSpendNanoUSD, maxChargeNanoUSD int64) {
	t.Helper()
	packageDir, transcripts := twoCaseOpenRouterReviewFixture(t)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		alias := "review-one"
		content := `{"disposition":"eligible","contentRole":"commercial","taxonomy":{"product":["cola"]},"policyFlags":[],"slices":["commercial"],"evidence":[{"id":"frame-01","kind":"frame","claim":"content_role","value":"commercial","provenance":"cases/review-one/frame-01.jpg","atMs":1000}],"reviewQuestion":""}`
		if requests > 1 {
			alias = "review-two"
			content = `{"disposition":"eligible"`
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": fmt.Sprintf("generation-%d", requests), "model": "review/vendor-model", "alias": alias,
			"choices":             []any{map[string]any{"message": map[string]string{"content": content}}},
			"usage":               map[string]any{"prompt_tokens": 200, "completion_tokens": 50, "cost": 0.001},
			"openrouter_metadata": map[string]any{"attempt": 1, "endpoints": map[string]any{"available": []any{map[string]any{"provider": "Provider Route", "model": "review/vendor-model", "selected": true}}}},
		})
	}))
	defer server.Close()
	now := time.Date(2026, 8, 27, 22, 0, 0, 0, time.UTC)
	config := OpenRouterReviewConfig{
		PackageDir: packageDir, CheckpointDir: filepath.Join(t.TempDir(), "private-review-state"), Transcripts: transcripts,
		BaseURL: server.URL, APIKey: "test-key", Snapshot: openRouterReviewSnapshot(server.URL, now),
		Model: "review/vendor-model", UpstreamProvider: "Provider Route", UpstreamProviderSlug: "provider/route", ReviewerID: "hosted-reviewer-b",
		ExpectedCases: 2, PerCaseTimeout: time.Second, MaxRequests: maxRequests, MaxSpendNanoUSD: maxSpendNanoUSD, MaxChargeNanoUSD: maxChargeNanoUSD,
		AllowInsecureTestURL: true, Now: func() time.Time { return now },
	}
	if _, _, err := RunOpenRouterReview(context.Background(), config); err == nil || !strings.Contains(err.Error(), "unexpected EOF") {
		t.Fatalf("first run error = %v", err)
	}
	if _, _, err := RunOpenRouterReview(context.Background(), config); err == nil || !strings.Contains(err.Error(), "reservation exhausted") {
		t.Fatalf("resume ceiling error = %v", err)
	}
	if requests != 2 {
		t.Fatalf("resume exceeded its original ceiling with %d requests", requests)
	}
}

func twoCaseOpenRouterReviewFixture(t *testing.T) (string, []fillerbakeoff.TranscriptArtifact) {
	t.Helper()
	root, firstTranscript := reviewPackageFixture(t)
	manifest, err := readStrictJSON[Package](filepath.Join(root, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	second := manifest.Cases[0]
	second.Signals = append([]Signal(nil), second.Signals...)
	second.Alias = "review-two"
	second.ContentSHA256 = strings.Repeat("6", 64)
	second.EvidenceSHA256 = strings.Repeat("7", 64)
	secondDir := filepath.Join(root, "cases", second.Alias)
	if err := os.MkdirAll(secondDir, 0o750); err != nil {
		t.Fatal(err)
	}
	secondTranscript := firstTranscript
	secondTranscript.CaseID = "second-hidden-case"
	secondTranscript.PacketSHA256 = strings.Repeat("8", 64)
	for index := range second.Signals {
		signal := &second.Signals[index]
		data, err := os.ReadFile(filepath.Join(root, signal.Path))
		if err != nil {
			t.Fatal(err)
		}
		if signal.Kind == "audio" {
			data = append(data, []byte("-two")...)
			signal.SHA256 = sha(data)
			signal.Bytes = int64(len(data))
			secondTranscript.AudioSHA256 = signal.SHA256
			secondTranscript.AudioBytes = signal.Bytes
		}
		signal.Path = filepath.Join("cases", second.Alias, filepath.Base(signal.Path))
		if err := os.WriteFile(filepath.Join(root, signal.Path), data, 0o640); err != nil {
			t.Fatal(err)
		}
	}
	manifest.Cases = append(manifest.Cases, second)
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "manifest.json"), append(raw, '\n'), 0o640); err != nil {
		t.Fatal(err)
	}
	return root, []fillerbakeoff.TranscriptArtifact{firstTranscript, secondTranscript}
}

func openRouterReviewSnapshot(baseURL string, now time.Time) fillerbakeoff.OpenRouterSnapshot {
	return fillerbakeoff.OpenRouterSnapshot{SchemaVersion: fillerbakeoff.OpenRouterSnapshotSchemaVersion, SourceBaseURL: baseURL, RetrievedAt: now.Add(-time.Hour), Requests: 3, ResponseBytes: 100, Models: []fillerbakeoff.OpenRouterModelSnapshot{{ID: "review/vendor-model", CanonicalSlug: "review/vendor-model", Name: "Reviewer", Created: 1, InputModalities: []string{"image", "text"}, OutputModalities: []string{"text"}, Endpoints: []fillerbakeoff.OpenRouterEndpointSnapshot{{Name: "Route", ModelID: "review/vendor-model", ProviderName: "Provider Route", ProviderSlug: "provider/route", Quantization: "unknown", ContextLength: 32768, MaxCompletionTokens: 4096, SupportedParameters: []string{"reasoning", "response_format", "structured_outputs"}, Pricing: map[string]string{"completion": "0.000001", "prompt": "0.000001"}, Status: 0, ZDR: true}}}}}
}

func TestRunOpenRouterReviewPinsZDRRouteAndPaidAccounting(t *testing.T) {
	packageDir, transcript := reviewPackageFixture(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatal("missing API key")
		}
		if r.Header.Get("X-OpenRouter-Metadata") != "enabled" {
			t.Fatal("missing metadata request")
		}
		var request struct {
			Model    string `json:"model"`
			Provider struct {
				Order             []string `json:"order"`
				AllowFallbacks    bool     `json:"allow_fallbacks"`
				RequireParameters bool     `json:"require_parameters"`
				DataCollection    string   `json:"data_collection"`
				ZDR               bool     `json:"zdr"`
			} `json:"provider"`
			Messages []struct {
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"messages"`
			ResponseFormat struct {
				JSONSchema struct {
					Strict bool `json:"strict"`
				} `json:"json_schema"`
			} `json:"response_format"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(request)
		if request.Model != "review/vendor-model" || len(request.Provider.Order) != 1 || request.Provider.Order[0] != "provider/route" || request.Provider.AllowFallbacks || !request.Provider.RequireParameters || request.Provider.DataCollection != "deny" || !request.Provider.ZDR || !request.ResponseFormat.JSONSchema.Strict || strings.Contains(string(encoded), "case-secret") {
			t.Fatalf("request = %s", encoded)
		}
		labels := `{"disposition":"eligible","contentRole":"commercial","taxonomy":{"product":["cola"]},"policyFlags":[],"slices":["commercial"],"evidence":[{"id":"frame-01","kind":"frame","claim":"content_role","value":"commercial","provenance":"cases/review-one/frame-01.jpg","atMs":1000}],"reviewQuestion":""}`
		response := map[string]any{
			"id": "generation-1", "model": "review/vendor-model",
			"choices":             []any{map[string]any{"message": map[string]string{"content": labels}}},
			"usage":               map[string]any{"prompt_tokens": 200, "completion_tokens": 50, "cost": 0.001},
			"openrouter_metadata": map[string]any{"attempt": 1, "attempts": []any{map[string]any{"provider": "Provider Route", "model": "review/vendor-model", "status": 200}}, "endpoints": map[string]any{"available": []any{map[string]any{"provider": "Provider Route", "model": "review/vendor-model", "selected": true}}}},
		}
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	now := time.Date(2026, 8, 27, 22, 0, 0, 0, time.UTC)
	snapshot := openRouterReviewSnapshot(server.URL, now)
	run, submissions, err := RunOpenRouterReview(context.Background(), OpenRouterReviewConfig{PackageDir: packageDir, CheckpointDir: filepath.Join(t.TempDir(), "private-review-state"), Transcripts: []fillerbakeoff.TranscriptArtifact{transcript}, BaseURL: server.URL, APIKey: "test-key", Snapshot: snapshot, Model: "review/vendor-model", UpstreamProvider: "Provider Route", UpstreamProviderSlug: "provider/route", ReviewerID: "hosted-reviewer-a", ExpectedCases: 1, PerCaseTimeout: time.Second, MaxRequests: 1, MaxSpendNanoUSD: 2_000_000, MaxChargeNanoUSD: 2_000_000, AllowInsecureTestURL: true, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if len(submissions) != 1 || run.Requests != 1 || run.PromptTokens != 200 || run.CompletionTokens != 50 || run.ChargedNanoUSD != 1_000_000 || run.CapabilitySnapshotSHA256 == "" || run.UpstreamProvider != "Provider Route" {
		t.Fatalf("run=%+v submissions=%+v", run, submissions)
	}
}
