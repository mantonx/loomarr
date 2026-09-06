package fillervisualsafety

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/fillerbakeoff"
	"github.com/loomarr/loomarr/internal/httpx"
	"github.com/loomarr/loomarr/internal/openroutermedia"
)

const candidateBlindOpenRouterMaximumTokens = 2048

func runCandidateBlindOpenRouterReview(ctx context.Context, config CandidateBlindOpenRouterConfig) (result CandidateBlindOpenRouterResult, err error) {
	baseURL, client, now, model, err := validateCandidateBlindOpenRouterConfig(ctx, config)
	if err != nil {
		return CandidateBlindOpenRouterResult{}, err
	}
	manifest, owner, err := OpenCandidateBlindReviewBundle(config.BundlePath)
	if err != nil {
		return CandidateBlindOpenRouterResult{}, err
	}
	if manifest.SHA256 != config.ExpectedPackageSHA256 || owner.SHA256 != config.ExpectedOwnerMapSHA256 ||
		owner.SelectionOrigin != config.ExpectedSelectionOrigin {
		return CandidateBlindOpenRouterResult{}, errors.New("candidate-blind OpenRouter review package identity drifted")
	}
	policyRaw, err := readPrivateReviewFile(filepath.Join(config.BundlePath, reviewDirectoryName, manifest.Policy.RelativePath), maximumReviewPolicyBytes)
	if err != nil {
		return CandidateBlindOpenRouterResult{}, err
	}
	policy, err := decodeCandidateBlindReviewPolicy(policyRaw)
	if err != nil {
		return CandidateBlindOpenRouterResult{}, err
	}
	parent, err := reserveReviewOutput(config.OutputDir)
	if err != nil {
		return CandidateBlindOpenRouterResult{}, err
	}
	requestReserved := false
	published := false
	defer func() {
		if !requestReserved && !published {
			_ = os.RemoveAll(config.OutputDir)
		}
	}()
	if err := writeReviewFile(filepath.Join(config.OutputDir, reviewIncompleteName), []byte("incomplete\n")); err != nil {
		return CandidateBlindOpenRouterResult{}, err
	}
	input, err := buildCandidateBlindHostedInput(ctx, config.BundlePath, config.OutputDir, config.FFmpegPath, manifest)
	if err != nil {
		return CandidateBlindOpenRouterResult{}, err
	}
	afterManifest, afterOwner, err := OpenCandidateBlindReviewBundle(config.BundlePath)
	if err != nil || afterManifest.SHA256 != manifest.SHA256 || afterOwner.SHA256 != owner.SHA256 {
		return CandidateBlindOpenRouterResult{}, errors.New("candidate-blind OpenRouter review package drifted during input preparation")
	}
	images, video, err := loadCandidateBlindHostedMedia(config.OutputDir, input)
	if err != nil {
		return CandidateBlindOpenRouterResult{}, err
	}
	promptSHA := candidateBlindOpenRouterPromptSHA256()
	schema := candidateBlindOpenRouterSchema(policy, manifest.Plan.DurationMS)
	schemaSHA := candidateBlindOpenRouterSchemaSHA256(policy, manifest.Plan.DurationMS)
	requestedAt := now().UTC()
	checkpoint := candidateBlindOpenRouterCheckpoint{
		SchemaVersion: 1, ReviewPackageSHA256: manifest.SHA256, OwnerMapSHA256: owner.SHA256,
		SelectionOrigin:          owner.SelectionOrigin,
		CapabilitySnapshotSHA256: fillerbakeoff.OpenRouterSnapshotSHA256(config.Snapshot),
		Model:                    config.Model, ModelFamily: config.ModelFamily, ResolvedModel: model.CanonicalSlug,
		UpstreamProvider: config.UpstreamProvider, UpstreamProviderSlug: config.UpstreamProviderSlug,
		ReviewerID: config.ReviewerID, PromptVersion: CandidateBlindOpenRouterPromptVersion,
		PromptSHA256: promptSHA, SchemaSHA256: schemaSHA, ReasoningEnabled: config.ReasoningEnabled,
		MaxRequests: 1, MaxChargeNanoUSD: config.MaxChargeNanoUSD, Input: input,
		Attempt: CandidateBlindOpenRouterAttempt{
			RequestedAt: requestedAt, State: CandidateBlindAttemptReserved, ReservedNanoUSD: config.MaxChargeNanoUSD,
		},
	}
	requestCtx, cancel := context.WithTimeout(ctx, config.PerRequestTimeout)
	defer cancel()
	started := time.Now()
	callResult, callErr := openroutermedia.Call(requestCtx, client, baseURL, openroutermedia.Config{
		APIKey: config.APIKey, Model: config.Model, ResolvedModel: model.CanonicalSlug,
		UpstreamProvider: config.UpstreamProvider, ProviderSlug: config.UpstreamProviderSlug,
		SchemaName: "filler_visual_policy_review", Schema: schema,
		SystemPrompt: candidateBlindOpenRouterSystemPrompt,
		Content:      candidateBlindOpenRouterContent(policyRaw, manifest, input),
		Images:       images,
		Videos:       []openroutermedia.Video{{MIMEType: "video/mp4", Base64: video}},
		MaxTokens:    candidateBlindOpenRouterMaximumTokens, ReservationNanoUSD: config.MaxChargeNanoUSD,
		DisableReasoning: !config.ReasoningEnabled, EnableReasoning: config.ReasoningEnabled,
		Title: "Loomarr candidate-blind visual policy review",
		Reserve: func(requestSHA256 string) error {
			checkpoint.Attempt.RequestSHA256 = requestSHA256
			if err := persistCandidateBlindOpenRouterCheckpoint(config.OutputDir, checkpoint); err != nil {
				return err
			}
			requestReserved = true
			return nil
		},
	})
	checkpoint.Attempt.LatencyMS = max(int64(0), time.Since(started).Milliseconds())
	checkpoint.Attempt.ResponseSHA256 = callResult.ResponseSHA256
	checkpoint.Attempt.GenerationID = callResult.GenerationID
	checkpoint.Attempt.PromptTokens = callResult.PromptTokens
	checkpoint.Attempt.CompletionTokens = callResult.CompletionTokens
	checkpoint.Attempt.ReasoningBytes = callResult.ReasoningBytes
	if callResult.ChargeKnown {
		checkpoint.Attempt.ChargedAmountUSD = callResult.ChargedAmountUSD
		checkpoint.Attempt.ChargedNanoUSD = callResult.ChargedNanoUSD
	}
	if len(callResult.RawResponse) > 0 {
		checkpoint.Attempt.RawResponsePath, err = writeCandidateBlindOpenRouterRaw(config.OutputDir, callResult.RawResponse)
		if err != nil {
			return CandidateBlindOpenRouterResult{}, err
		}
	}
	var assessment CandidateBlindOpenRouterAssessment
	if callErr == nil {
		assessment, err = decodeCandidateBlindOpenRouterAssessment(callResult.StructuredOutput, policy, manifest.Plan.DurationMS)
		if err != nil {
			callErr = err
			checkpoint.Attempt.OperationalFailure = "invalid_response"
		}
	}
	if callErr == nil {
		checkpoint.Attempt.State = CandidateBlindAttemptAccepted
	} else if callResult.ChargeKnown {
		checkpoint.Attempt.State = CandidateBlindAttemptFailed
		if checkpoint.Attempt.OperationalFailure == "" {
			checkpoint.Attempt.OperationalFailure = candidateBlindOpenRouterFailure(requestCtx, callErr)
		}
	} else {
		checkpoint.Attempt.State = CandidateBlindAttemptUnsettled
		checkpoint.Attempt.OperationalFailure = candidateBlindOpenRouterFailure(requestCtx, callErr)
		if checkpoint.Attempt.OperationalFailure == "transport_or_response" &&
			checkpoint.Attempt.ResponseSHA256 != "" && !callResult.ChargeKnown {
			checkpoint.Attempt.OperationalFailure = "unsettled_accounting"
		}
	}
	if requestReserved {
		if err := persistCandidateBlindOpenRouterCheckpoint(config.OutputDir, checkpoint); err != nil {
			return CandidateBlindOpenRouterResult{}, err
		}
	}
	if callErr != nil {
		return CandidateBlindOpenRouterResult{}, fmt.Errorf("candidate-blind OpenRouter review failed after durable reservation: %w", callErr)
	}
	result = CandidateBlindOpenRouterResult{
		SchemaVersion: CandidateBlindOpenRouterSchemaVersion, ContractVersion: CandidateBlindOpenRouterContractVersion,
		ReviewPackageSHA256: manifest.SHA256, OwnerMapSHA256: owner.SHA256, SelectionOrigin: owner.SelectionOrigin,
		CapabilitySnapshotSHA256: checkpoint.CapabilitySnapshotSHA256,
		Model:                    config.Model, ModelFamily: config.ModelFamily, ResolvedModel: model.CanonicalSlug,
		UpstreamProvider: config.UpstreamProvider, UpstreamProviderSlug: config.UpstreamProviderSlug,
		ReviewerID: config.ReviewerID, PromptVersion: CandidateBlindOpenRouterPromptVersion,
		PromptSHA256: promptSHA, SchemaSHA256: schemaSHA, ReasoningEnabled: config.ReasoningEnabled,
		MaxRequests: 1, MaxChargeNanoUSD: config.MaxChargeNanoUSD,
		Input: input, Attempt: checkpoint.Attempt, Assessment: assessment, ReviewedAt: now().UTC(),
		TruthAuthorityCreated: false, TrainingAllowed: false, ProductionAdmissionAllowed: false,
	}
	result.SHA256 = CandidateBlindOpenRouterResultSHA256(result)
	if err := validateCandidateBlindOpenRouterResult(result, checkpoint); err != nil {
		return CandidateBlindOpenRouterResult{}, err
	}
	if err := writeReviewJSON(filepath.Join(config.OutputDir, candidateBlindOpenRouterResultName), result); err != nil {
		return CandidateBlindOpenRouterResult{}, err
	}
	opened, err := openCandidateBlindOpenRouterReview(config.OutputDir, true)
	if err != nil || opened.SHA256 != result.SHA256 {
		return CandidateBlindOpenRouterResult{}, errors.New("verify candidate-blind OpenRouter review")
	}
	if err := syncReviewDirectory(filepath.Join(config.OutputDir, candidateBlindHostedInputDir)); err != nil {
		return CandidateBlindOpenRouterResult{}, err
	}
	if err := syncReviewDirectory(config.OutputDir); err != nil {
		return CandidateBlindOpenRouterResult{}, err
	}
	if err := os.Remove(filepath.Join(config.OutputDir, reviewIncompleteName)); err != nil {
		return CandidateBlindOpenRouterResult{}, errors.New("publish candidate-blind OpenRouter review")
	}
	if err := syncReviewDirectory(config.OutputDir); err != nil {
		return CandidateBlindOpenRouterResult{}, err
	}
	if err := syncReviewDirectory(parent); err != nil {
		return CandidateBlindOpenRouterResult{}, err
	}
	published = true
	return result, nil
}

func validateCandidateBlindOpenRouterConfig(ctx context.Context, config CandidateBlindOpenRouterConfig) (string, *http.Client, func() time.Time, fillerbakeoff.OpenRouterModelSnapshot, error) {
	if ctx == nil || ctx.Err() != nil {
		return "", nil, nil, fillerbakeoff.OpenRouterModelSnapshot{}, errors.New("candidate-blind OpenRouter review context is invalid")
	}
	baseURL := strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	if baseURL == "" {
		baseURL = fillerbakeoff.OpenRouterBaseURL
	}
	parsed, err := url.Parse(baseURL)
	loopback := err == nil && config.AllowInsecureTestURL && (parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "localhost" || parsed.Hostname() == "::1")
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(!loopback && (parsed.Scheme != "https" || parsed.Hostname() != "openrouter.ai" || parsed.Path != "/api/v1")) {
		return "", nil, nil, fillerbakeoff.OpenRouterModelSnapshot{}, errors.New("candidate-blind OpenRouter review requires the canonical HTTPS API base")
	}
	if strings.TrimSpace(config.APIKey) == "" || !cleanAbsoluteReviewPath(config.BundlePath) ||
		!cleanAbsoluteReviewPath(config.OutputDir) || config.BundlePath == config.OutputDir ||
		!validDigest(config.ExpectedPackageSHA256) || !validDigest(config.ExpectedOwnerMapSHA256) ||
		(config.ExpectedSelectionOrigin != ReviewSelectionIndependentCorpus && config.ExpectedSelectionOrigin != ReviewSelectionTargetedDiagnostic) ||
		strings.TrimSpace(config.FFmpegPath) == "" || !validIdentity(config.Model) || strings.Contains(strings.ToLower(config.Model), "latest") ||
		!validIdentity(config.ModelFamily) || !validIdentity(config.UpstreamProvider) || !validIdentity(config.UpstreamProviderSlug) ||
		!validIdentity(config.ReviewerID) || config.PerRequestTimeout <= 0 || config.PerRequestTimeout > 30*time.Minute ||
		config.MaxChargeNanoUSD <= 0 {
		return "", nil, nil, fillerbakeoff.OpenRouterModelSnapshot{}, errors.New("candidate-blind OpenRouter review configuration is invalid")
	}
	if reviewPathsOverlap(config.BundlePath, config.OutputDir) {
		return "", nil, nil, fillerbakeoff.OpenRouterModelSnapshot{}, errors.New("candidate-blind OpenRouter review output overlaps its source bundle")
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	model, _, err := fillerbakeoff.ValidateOpenRouterVideoRoute(config.Snapshot, config.Model, config.UpstreamProvider, config.UpstreamProviderSlug, now().UTC(), candidateBlindOpenRouterMaximumTokens)
	if err != nil || !slices.Contains(model.InputModalities, "image") {
		return "", nil, nil, fillerbakeoff.OpenRouterModelSnapshot{}, errors.New("candidate-blind OpenRouter route lacks a fresh ZDR text/image/video structured-output capability")
	}
	client := config.Client
	if client == nil {
		client = httpx.NewNamed("filler-visual-openrouter-review", httpx.TimeoutLLM)
	}
	copy := *client
	copy.Timeout = 0
	copy.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	return baseURL, &copy, now, model, nil
}

func reviewPathsOverlap(left, right string) bool {
	relative, err := filepath.Rel(left, right)
	if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return true
	}
	relative, err = filepath.Rel(right, left)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func loadCandidateBlindHostedMedia(root string, input CandidateBlindHostedInput) ([]string, string, error) {
	images := make([]string, 0, len(input.ContactSheets))
	for _, sheet := range input.ContactSheets {
		raw, err := readPrivateReviewFile(filepath.Join(root, filepath.FromSlash(sheet.RelativePath)), candidateBlindMaximumSheetBytes)
		if err != nil || int64(len(raw)) != sheet.Bytes || reviewBytesSHA256(raw) != sheet.SHA256 {
			return nil, "", errors.New("candidate-blind OpenRouter contact sheet drifted")
		}
		images = append(images, base64.StdEncoding.EncodeToString(raw))
	}
	raw, err := readPrivateReviewFile(filepath.Join(root, filepath.FromSlash(input.Carrier.RelativePath)), candidateBlindMaximumCarrierBytes)
	if err != nil || int64(len(raw)) != input.Carrier.Bytes || reviewBytesSHA256(raw) != input.Carrier.SHA256 {
		return nil, "", errors.New("candidate-blind OpenRouter video carrier drifted")
	}
	return images, base64.StdEncoding.EncodeToString(raw), nil
}

func candidateBlindOpenRouterFailure(ctx context.Context, err error) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return "canceled"
	}
	if errors.Is(err, openroutermedia.ErrRouteMismatch) {
		return "route_mismatch"
	}
	if errors.Is(err, openroutermedia.ErrChargeExceedsReservation) {
		return "charge_exceeded"
	}
	var status *openroutermedia.StatusError
	if errors.As(err, &status) {
		return "provider_status"
	}
	return "transport_or_response"
}
