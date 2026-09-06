package fillersafetyreview

import (
	"context"
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

	"github.com/loomarr/loomarr/internal/fillersafety"
)

func TestFFmpegAudioExtractorRejectsReplacedExecutable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ffmpeg")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho 'ffmpeg version 7.1'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	expected, _, err := identifyFFmpeg(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho 'ffmpeg version 7.2'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	_, err = (ffmpegAudioExtractor{}).Extract(t.Context(), path, expected, &fillersafety.CompleteMediaPlan{}, 12)
	if err == nil || !strings.Contains(err.Error(), "identity changed") || strings.Contains(err.Error(), path) {
		t.Fatalf("err=%v", err)
	}
}

func TestRunOpenRouterCancelsMaterialPreflightBeforeToolIdentity(t *testing.T) {
	fixture := newReviewFixture(t, testReviewBaseURL)
	runtime := fixture.runtime(&http.Client{}, testReviewBaseURL)
	called := false
	runtime.identify = func(context.Context, string) (fillersafety.ToolIdentity, string, error) {
		called = true
		return fillersafety.ToolIdentity{}, "", nil
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := runOpenRouter(ctx, fixture.config, runtime)
	if err == nil || called {
		t.Fatalf("err=%v identify=%t", err, called)
	}
}

func TestRunOpenRouterRejectsMissingReasoningAuthorityBeforeSideEffects(t *testing.T) {
	var requests atomic.Int64
	endpoint := newReviewTestEndpoint(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	fixture := newReviewFixture(t, endpoint.baseURL)
	plan, _, err := readPrivateJSON[Plan](fixture.planPath, maximumPlanBytes)
	if err != nil {
		t.Fatal(err)
	}
	plan.DisableReasoning = false
	marshalPrivateJSON(t, fixture.planPath, plan)
	runtime := fixture.runtime(endpoint.client, endpoint.baseURL)
	identified := false
	runtime.identify = func(context.Context, string) (fillersafety.ToolIdentity, string, error) {
		identified = true
		return fillersafety.ToolIdentity{}, "", nil
	}
	_, err = runOpenRouter(t.Context(), fixture.config, runtime)
	if err == nil || !strings.Contains(err.Error(), "route authority") || identified || requests.Load() != 0 {
		t.Fatalf("err=%v identified=%t requests=%d", err, identified, requests.Load())
	}
	if _, statErr := os.Stat(fixture.checkpointDir); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("checkpoint stat err=%v", statErr)
	}
	if _, statErr := os.Stat(fixture.outputPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("output stat err=%v", statErr)
	}
}

func TestRunOpenRouterWholeDeadlineBoundsToolIdentityBeforeReview(t *testing.T) {
	var requests atomic.Int64
	endpoint := newReviewTestEndpoint(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	fixture := newReviewFixture(t, endpoint.baseURL)
	plan, _, err := readPrivateJSON[Plan](fixture.planPath, maximumPlanBytes)
	if err != nil {
		t.Fatal(err)
	}
	plan.MaximumWallTimeMS = 5_000
	plan.PerCaseTimeoutMS = 5_000
	marshalPrivateJSON(t, fixture.planPath, plan)
	runtime := fixture.runtime(endpoint.client, endpoint.baseURL)
	var identityDeadline time.Time
	runtime.identify = func(ctx context.Context, _ string) (fillersafety.ToolIdentity, string, error) {
		var ok bool
		identityDeadline, ok = ctx.Deadline()
		if !ok {
			return fillersafety.ToolIdentity{}, "", fmt.Errorf("tool identity lacks whole-run deadline")
		}
		<-ctx.Done()
		return fillersafety.ToolIdentity{}, "", ctx.Err()
	}
	started := time.Now()
	_, err = runOpenRouter(t.Context(), fixture.config, runtime)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v", err)
	}
	if identityDeadline.IsZero() || identityDeadline.Sub(started) > 5100*time.Millisecond {
		t.Fatalf("identity deadline=%v started=%v", identityDeadline, started)
	}
	if requests.Load() != 0 {
		t.Fatalf("review requests=%d", requests.Load())
	}
	if _, statErr := os.Stat(fixture.checkpointDir); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("checkpoint stat err=%v", statErr)
	}
	if _, statErr := os.Stat(fixture.outputPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("output stat err=%v", statErr)
	}
}

func TestRunOpenRouterBindsCaseDeadlineAndToolIdentityToExtraction(t *testing.T) {
	endpoint := newReviewTestEndpoint(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		writeReviewResponse(t, writer, 1, verifiedObservation(strings.Contains(string(body), "positive_candidate")))
	}))
	fixture := newReviewFixture(t, endpoint.baseURL)
	plan, _, err := readPrivateJSON[Plan](fixture.planPath, maximumPlanBytes)
	if err != nil {
		t.Fatal(err)
	}
	plan.PerCaseTimeoutMS = 1_000
	marshalPrivateJSON(t, fixture.planPath, plan)
	runtime := fixture.runtime(endpoint.client, endpoint.baseURL)
	var calls int
	runtime.extract = audioExtractFunc(func(ctx context.Context, _ string, identity fillersafety.ToolIdentity, _ *fillersafety.CompleteMediaPlan, _ int64) ([]byte, error) {
		calls++
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 2*time.Second || identity.BinarySHA256 != testFixtureSHA(90) {
			return nil, fmt.Errorf("missing short case deadline or tool identity")
		}
		return []byte("RIFF0000WAVE"), nil
	})
	if _, err := runOpenRouter(t.Context(), fixture.config, runtime); err != nil {
		t.Fatal(err)
	}
	if calls == 0 {
		t.Fatal("extractor was not called")
	}
}
