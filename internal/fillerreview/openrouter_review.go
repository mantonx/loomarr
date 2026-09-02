package fillerreview

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/fillerbakeoff"
	"github.com/loomarr/loomarr/internal/fillereval"
	"github.com/loomarr/loomarr/internal/httpx"
	"github.com/loomarr/loomarr/internal/openroutermedia"
)

const OpenRouterReviewPromptVersion = "filler-blind-review-openrouter-v7"

type OpenRouterReviewConfig struct {
	PackageDir           string
	CheckpointDir        string
	Transcripts          []fillerbakeoff.TranscriptArtifact
	BaseURL              string
	APIKey               string
	Snapshot             fillerbakeoff.OpenRouterSnapshot
	Model                string
	UpstreamProvider     string
	UpstreamProviderSlug string
	ReviewerID           string
	ExpectedCases        int
	PerCaseTimeout       time.Duration
	MaxRequests          int
	MaxSpendNanoUSD      int64
	MaxChargeNanoUSD     int64
	AllowInsecureTestURL bool
	Client               *http.Client
	Now                  func() time.Time
}

func RunOpenRouterReview(ctx context.Context, config OpenRouterReviewConfig) (run ReviewRun, submissions []fillereval.LabelSubmission, err error) {
	return runOpenRouterReview(ctx, config, nil)
}

func runOpenRouterReview(ctx context.Context, config OpenRouterReviewConfig, beforeCheckpointDirCreate func()) (run ReviewRun, submissions []fillereval.LabelSubmission, err error) {
	baseURL, client, now, err := validateOpenRouterReviewConfig(config)
	if err != nil {
		return ReviewRun{}, nil, err
	}
	manifestPath, err := resolveWithin(config.PackageDir, "manifest.json")
	if err != nil {
		return ReviewRun{}, nil, err
	}
	manifest, err := readStrictJSON[Package](manifestPath)
	if err != nil {
		return ReviewRun{}, nil, fmt.Errorf("read review manifest: %w", err)
	}
	if err := validateReviewPackage(config.PackageDir, manifest, config.ExpectedCases); err != nil {
		return ReviewRun{}, nil, err
	}
	transcripts, transcriptSetSHA256, err := indexReviewTranscripts(config.Transcripts, manifest)
	if err != nil {
		return ReviewRun{}, nil, err
	}
	manifestSHA256, err := hashFile(manifestPath)
	if err != nil {
		return ReviewRun{}, nil, err
	}
	identity := buildOpenRouterCheckpointIdentity(config, baseURL, manifest.BatchID, manifestSHA256, transcriptSetSHA256)
	activeLock, err := acquireOpenRouterActiveRunLock(config.CheckpointDir, identity, now, beforeCheckpointDirCreate)
	if err != nil {
		return ReviewRun{}, nil, err
	}
	defer func() {
		if releaseErr := activeLock.release(); releaseErr != nil {
			run = ReviewRun{}
			submissions = nil
			err = errors.Join(err, releaseErr)
		}
	}()
	checkpoint, err := loadOpenRouterCheckpoint(config.CheckpointDir, identity)
	if err != nil {
		return ReviewRun{}, nil, err
	}
	if err := validateOpenRouterCheckpointOrder(checkpoint, manifest.Cases); err != nil {
		return ReviewRun{}, nil, err
	}
	run = ReviewRun{
		SchemaVersion: ReviewRunSchemaVersion, BatchID: manifest.BatchID, PackageManifestSHA256: manifestSHA256,
		ReviewerID: config.ReviewerID, Provider: "openrouter", Model: config.Model, ResolvedModel: openRouterReviewModel(config.Snapshot, config.Model).CanonicalSlug,
		UpstreamProvider: config.UpstreamProvider, UpstreamProviderSlug: config.UpstreamProviderSlug,
		PromptVersion:            OpenRouterReviewPromptVersion,
		PromptSHA256:             identity.PromptSHA256,
		CapabilitySnapshotSHA256: identity.CapabilitySnapshotSHA256,
		TranscriptSetSHA256:      transcriptSetSHA256, Cases: len(manifest.Cases), MaxRequests: config.MaxRequests,
		MaxSpendNanoUSD: config.MaxSpendNanoUSD, MaxChargeNanoUSD: config.MaxChargeNanoUSD,
	}
	accepted := acceptedOpenRouterAliases(checkpoint)
	for _, item := range manifest.Cases {
		if _, ok := accepted[item.Alias]; ok {
			continue
		}
		for _, prior := range checkpoint.Attempts {
			if prior.Alias == item.Alias && prior.State == openRouterAttemptReserved {
				return ReviewRun{}, nil, fmt.Errorf("openrouter review alias %q has an unsettled prior request", item.Alias)
			}
		}
		caseCtx, cancel := context.WithTimeout(ctx, config.PerCaseTimeout)
		started := time.Now()
		attemptNumber := nextOpenRouterAttempt(checkpoint, item.Alias)
		labels, callResult, reviewErr := reviewOneOpenRouter(caseCtx, client, baseURL, config, manifest, item, transcripts, func(requestSHA256 string) error {
			spent, err := openRouterCheckpointSpend(checkpoint)
			if err != nil {
				return err
			}
			if len(checkpoint.Attempts) >= config.MaxRequests || spent > config.MaxSpendNanoUSD-config.MaxChargeNanoUSD {
				return fmt.Errorf("openrouter review request or spend reservation exhausted before alias %q", item.Alias)
			}
			checkpoint.Attempts = append(checkpoint.Attempts, ReviewAttempt{Alias: item.Alias, Attempt: attemptNumber, RequestedAt: openRouterCheckpointNow(now), RequestSHA256: requestSHA256, State: openRouterAttemptReserved})
			return persistOpenRouterCheckpoint(config.CheckpointDir, checkpoint)
		})
		latencyMS := max(int64(0), time.Since(started).Milliseconds())
		cancel()
		reserved := callResult.RequestSHA256 != "" && len(checkpoint.Attempts) > 0 && checkpoint.Attempts[len(checkpoint.Attempts)-1].Alias == item.Alias && checkpoint.Attempts[len(checkpoint.Attempts)-1].RequestSHA256 == callResult.RequestSHA256
		if reserved {
			attempt := &checkpoint.Attempts[len(checkpoint.Attempts)-1]
			attempt.GenerationID = callResult.GenerationID
			attempt.LatencyMS = latencyMS
			attempt.PromptTokens = callResult.PromptTokens
			attempt.CompletionTokens = callResult.CompletionTokens
			if callResult.ChargeKnown {
				attempt.ChargedAmountUSD = callResult.ChargedAmountUSD
				attempt.ChargedNanoUSD = callResult.ChargedNanoUSD
				attempt.State = openRouterAttemptFailed
			}
		}
		if reviewErr != nil {
			if reserved {
				if err := persistOpenRouterCheckpoint(config.CheckpointDir, checkpoint); err != nil {
					return ReviewRun{}, nil, fmt.Errorf("persist failed OpenRouter review attempt: %w", err)
				}
			}
			return ReviewRun{}, nil, fmt.Errorf("review alias %q: %w", item.Alias, reviewErr)
		}
		reviewedAt := now().UTC()
		submission := fillereval.LabelSubmission{
			Alias: item.Alias, ReviewerID: config.ReviewerID, BatchID: manifest.BatchID,
			ReviewedAt: reviewedAt, Labels: fillereval.NormalizeLabels(labels),
		}
		call := ReviewCall{
			Alias: item.Alias, ReviewedAt: reviewedAt, GenerationID: callResult.GenerationID, LatencyMS: latencyMS,
			PromptTokens: callResult.PromptTokens, CompletionTokens: callResult.CompletionTokens,
			ChargedAmountUSD: callResult.ChargedAmountUSD, ChargedNanoUSD: callResult.ChargedNanoUSD, RequestSHA256: callResult.RequestSHA256, Attempt: attemptNumber,
		}
		checkpoint.Submissions = append(checkpoint.Submissions, submission)
		checkpoint.Calls = append(checkpoint.Calls, call)
		attempt := &checkpoint.Attempts[len(checkpoint.Attempts)-1]
		attempt.State = openRouterAttemptAccepted
		attempt.SubmissionSHA256 = submissionSHA256([]fillereval.LabelSubmission{submission})
		if err := persistOpenRouterCheckpoint(config.CheckpointDir, checkpoint); err != nil {
			return ReviewRun{}, nil, fmt.Errorf("persist accepted OpenRouter review result: %w", err)
		}
		accepted[item.Alias] = struct{}{}
	}
	if len(checkpoint.Submissions) != len(manifest.Cases) {
		return ReviewRun{}, nil, fmt.Errorf("OpenRouter review checkpoint has %d accepted cases; package requires %d", len(checkpoint.Submissions), len(manifest.Cases))
	}
	for _, attempt := range checkpoint.Attempts {
		if attempt.State == openRouterAttemptReserved {
			return ReviewRun{}, nil, fmt.Errorf("OpenRouter review cannot complete with an unsettled request")
		}
		run.PromptTokens += attempt.PromptTokens
		run.CompletionTokens += attempt.CompletionTokens
		run.TotalLatencyMS += attempt.LatencyMS
		run.ChargedNanoUSD += attempt.ChargedNanoUSD
	}
	run.Requests = len(checkpoint.Attempts)
	run.Attempts = slices.Clone(checkpoint.Attempts)
	run.Calls = slices.Clone(checkpoint.Calls)
	submissions = slices.Clone(checkpoint.Submissions)
	run.CompletedAt = now().UTC()
	run.SubmissionSHA256 = submissionSHA256(submissions)
	return run, submissions, nil
}

func openRouterReviewModel(snapshot fillerbakeoff.OpenRouterSnapshot, modelID string) fillerbakeoff.OpenRouterModelSnapshot {
	for _, model := range snapshot.Models {
		if model.ID == modelID {
			return model
		}
	}
	return fillerbakeoff.OpenRouterModelSnapshot{}
}

func validateOpenRouterReviewConfig(config OpenRouterReviewConfig) (string, *http.Client, func() time.Time, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	if baseURL == "" {
		baseURL = fillerbakeoff.OpenRouterBaseURL
	}
	parsed, err := url.Parse(baseURL)
	loopback := err == nil && config.AllowInsecureTestURL && reviewLoopback(parsed.Hostname())
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (!loopback && (parsed.Scheme != "https" || parsed.Hostname() != "openrouter.ai" || parsed.Path != "/api/v1")) {
		return "", nil, nil, fmt.Errorf("openrouter blind review requires the canonical HTTPS API base")
	}
	if config.APIKey == "" || strings.TrimSpace(config.CheckpointDir) == "" || config.Model == "" || config.UpstreamProvider == "" || config.UpstreamProviderSlug == "" || config.ReviewerID == "" || config.ExpectedCases <= 0 || config.MaxRequests < config.ExpectedCases || config.MaxRequests > config.ExpectedCases+1 || config.MaxSpendNanoUSD <= 0 || config.MaxChargeNanoUSD <= 0 || config.MaxChargeNanoUSD > config.MaxSpendNanoUSD || config.PerCaseTimeout <= 0 {
		return "", nil, nil, fmt.Errorf("openrouter blind review requires exact identity and positive request, charge, spend, and timeout ceilings")
	}
	if err := validateOpenRouterReviewSnapshot(config, baseURL); err != nil {
		return "", nil, nil, err
	}
	client := config.Client
	if client == nil {
		client = httpx.NewNamed("filler-review-openrouter", httpx.TimeoutLLM)
	}
	copy := *client
	copy.Timeout = 0
	copy.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	now := config.Now
	if now == nil {
		now = time.Now
	}
	return baseURL, &copy, now, nil
}

func validateOpenRouterReviewSnapshot(config OpenRouterReviewConfig, baseURL string) error {
	if err := validateOpenRouterReviewSnapshotIdentity(config, baseURL); err != nil {
		return err
	}
	now := time.Now
	if config.Now != nil {
		now = config.Now
	}
	age := now().UTC().Sub(config.Snapshot.RetrievedAt)
	if age < 0 || age > 24*time.Hour {
		return fmt.Errorf("openrouter review is outside the snapshot's 24-hour window")
	}
	return nil
}

func validateOpenRouterReviewSnapshotIdentity(config OpenRouterReviewConfig, baseURL string) error {
	if err := fillerbakeoff.ValidateOpenRouterSnapshot(config.Snapshot); err != nil {
		return err
	}
	if config.Snapshot.SourceBaseURL != baseURL {
		return fmt.Errorf("openrouter review snapshot does not bind the request base")
	}
	for _, model := range config.Snapshot.Models {
		if model.ID != config.Model {
			continue
		}
		if !slices.Contains(model.InputModalities, "text") || !slices.Contains(model.InputModalities, "image") {
			return fmt.Errorf("openrouter reviewer model lacks text or image input")
		}
		for _, endpoint := range model.Endpoints {
			if endpoint.ProviderName == config.UpstreamProvider && endpoint.ProviderSlug == config.UpstreamProviderSlug && endpoint.ZDR && endpoint.Status == 0 && endpoint.MaxCompletionTokens >= 4096 && slices.Contains(endpoint.SupportedParameters, "response_format") && slices.Contains(endpoint.SupportedParameters, "structured_outputs") {
				return nil
			}
		}
	}
	return fmt.Errorf("openrouter reviewer route is absent, non-ZDR, or lacks strict structured output")
}

func reviewOneOpenRouter(ctx context.Context, client *http.Client, baseURL string, config OpenRouterReviewConfig, manifest Package, item Case, transcripts map[string]fillerbakeoff.TranscriptArtifact, reserve func(string) error) (fillereval.Labels, openroutermedia.Result, error) {
	content, images, err := reviewerContent(config.PackageDir, manifest, item, transcripts)
	if err != nil {
		return fillereval.Labels{}, openroutermedia.Result{}, err
	}
	result, err := openroutermedia.Call(ctx, client, baseURL, openroutermedia.Config{
		APIKey: config.APIKey, Model: config.Model, ResolvedModel: openRouterReviewModel(config.Snapshot, config.Model).CanonicalSlug,
		UpstreamProvider: config.UpstreamProvider, ProviderSlug: config.UpstreamProviderSlug,
		SchemaName: "filler_blind_review", Schema: reviewLabelsSchema(item), SystemPrompt: reviewerSystemPrompt,
		Content: content, Images: images, MaxTokens: 4096, MaxChargeNanoUSD: config.MaxChargeNanoUSD,
		Title: "Loomarr filler blind review", Reserve: reserve,
	})
	if err != nil {
		return fillereval.Labels{}, result, err
	}
	labels, err := decodeReviewLabels([]byte(result.StructuredOutput))
	if err != nil {
		return fillereval.Labels{}, result, fmt.Errorf("decode openrouter review labels: %w (contentBytes=%d reasoningBytes=%d)", err, len(result.StructuredOutput), result.ReasoningBytes)
	}
	if failures := fillereval.ValidateLabels(labels); len(failures) > 0 {
		return fillereval.Labels{}, result, fmt.Errorf("invalid review labels: %s", strings.Join(failures, "; "))
	}
	if err := validateReviewEvidence(item, labels.Evidence, transcripts); err != nil {
		return fillereval.Labels{}, result, err
	}
	return labels, result, nil
}
