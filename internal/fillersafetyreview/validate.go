package fillersafetyreview

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/loomarr/loomarr/internal/fillerbakeoff"
	"github.com/loomarr/loomarr/internal/fillersafety"
	"github.com/loomarr/loomarr/internal/fillersafetycert"
	"github.com/loomarr/loomarr/internal/fillersafetycorpus"
)

func validateInputs(ctx context.Context, config Config, loaded *loadedInputs, runtime reviewRuntime) (fillersafety.ToolIdentity, string, error) {
	if ctx == nil || ctx.Err() != nil || loaded == nil || strings.TrimSpace(config.APIKey) == "" ||
		config.APIKey != strings.TrimSpace(config.APIKey) || strings.ContainsAny(config.APIKey, "\r\n\x00") ||
		strings.TrimSpace(config.FFmpegPath) == "" || strings.TrimSpace(config.CheckpointDirectory) == "" ||
		strings.TrimSpace(config.OutputPath) == "" || runtime.client == nil || runtime.now == nil ||
		runtime.call == nil || runtime.extract == nil || runtime.identify == nil {
		return fillersafety.ToolIdentity{}, "", fmt.Errorf("spoken-safety model review requires exact inputs and an active runtime")
	}
	if err := validatePlan(loaded.plan); err != nil {
		return fillersafety.ToolIdentity{}, "", err
	}
	if loaded.plan.Draft.SHA256 != loaded.draftSHA256 || loaded.plan.Worklist.SHA256 != loaded.worklistSHA256 ||
		loaded.plan.Snapshot.SHA256 != loaded.snapshotSHA256 || loaded.inputBytes > loaded.plan.MaximumInputBytes {
		return fillersafety.ToolIdentity{}, "", fmt.Errorf("model review plan inputs or byte ceiling do not match")
	}
	canonicalDraft, canonicalDraftSHA256, err := fillersafetycert.MarshalCertificationDraft(loaded.draft)
	if err != nil || int64(len(canonicalDraft)) != loaded.plan.Draft.Bytes || canonicalDraftSHA256 != loaded.draftSHA256 {
		return fillersafety.ToolIdentity{}, "", fmt.Errorf("model review requires the canonical certification draft")
	}
	if err := fillersafety.ValidatePolicy(loaded.policy); err != nil || loaded.policySHA256 != loaded.draft.PolicySHA256 ||
		loaded.policySHA256 != loaded.worklist.PolicySHA256 {
		return fillersafety.ToolIdentity{}, "", fmt.Errorf("model review policy does not bind the draft and worklist")
	}
	now := runtime.now().UTC()
	if now.IsZero() || now.Before(loaded.worklist.AssembledAt) {
		return fillersafety.ToolIdentity{}, "", fmt.Errorf("model review time predates assembly")
	}
	if err := validateSnapshot(loaded, runtime.baseURL, now); err != nil {
		return fillersafety.ToolIdentity{}, "", err
	}
	if _, err := reviewRouteAuthority(*loaded, runtime); err != nil {
		return fillersafety.ToolIdentity{}, "", fmt.Errorf("model review route authority is invalid: %w", err)
	}
	if loaded.plan.ModelFamily == loaded.draft.ProposerFamily || loaded.plan.ModelFamily == loaded.draft.AudioRoute.ModelFamily ||
		loaded.plan.ModelFamily == loaded.draft.VideoRoute.ModelFamily {
		return fillersafety.ToolIdentity{}, "", fmt.Errorf("model reviewer family is not independent from the evaluated cascade")
	}
	processor := plannedKnownScriptProcessor(loaded.plan, runtime.baseURL)
	if err := verifyWorklist(ctx, loaded, now, processor); err != nil {
		return fillersafety.ToolIdentity{}, "", err
	}
	if err := ctx.Err(); err != nil {
		return fillersafety.ToolIdentity{}, "", err
	}
	ffmpeg, resolved, err := runtime.identify(ctx, config.FFmpegPath)
	if err != nil {
		return fillersafety.ToolIdentity{}, "", fmt.Errorf("identify model review ffmpeg: %w", err)
	}
	if err := validateOutputLocations(config, loaded.root); err != nil {
		return fillersafety.ToolIdentity{}, "", err
	}
	return ffmpeg, resolved, nil
}

func plannedKnownScriptProcessor(plan Plan, baseURL string) fillersafetycorpus.KnownScriptHostedProcessor {
	return fillersafetycorpus.KnownScriptHostedProcessor{
		Kind: fillersafetycorpus.KnownScriptProcessorOpenRouter, SourceBaseURL: baseURL,
		RequestedModel: plan.Model, ResolvedModel: plan.ResolvedModel,
		UpstreamProvider: plan.UpstreamProvider, UpstreamProviderSlug: plan.UpstreamProviderSlug,
		ZDR: true,
	}
}

func validatePlan(plan Plan) error {
	if plan.SchemaVersion != PlanSchemaVersion || plan.ContractVersion != PlanContractVersion ||
		!validAuthority(plan.Draft) || !validAuthority(plan.Worklist) || !validAuthority(plan.Snapshot) ||
		!boundedID(plan.ReviewerID) || !boundedID(plan.ModelFamily) || !boundedID(plan.Model) ||
		!boundedID(plan.ResolvedModel) || !boundedText(plan.UpstreamProvider) ||
		!boundedID(plan.UpstreamProviderSlug) || plan.ExpectedCases <= 0 ||
		plan.MaximumRequests < plan.ExpectedCases || plan.MaximumRequests > plan.ExpectedCases+16 ||
		plan.MaximumChargeNanoUSD <= 0 || plan.MaximumSpendNanoUSD <= 0 || plan.MaximumInputBytes <= 0 ||
		plan.MaximumAudioBytes < 12 || plan.MaximumAudioBytes > 64<<20 ||
		plan.PerCaseTimeoutMS < 1_000 || plan.PerCaseTimeoutMS > int64((10*time.Minute)/time.Millisecond) ||
		plan.MaximumWallTimeMS < plan.PerCaseTimeoutMS || plan.MaximumWallTimeMS > int64((24*time.Hour)/time.Millisecond) {
		return fmt.Errorf("spoken-safety model review plan identity, route, counts, or ceilings are invalid")
	}
	if int64(plan.MaximumRequests) > math.MaxInt64/plan.MaximumChargeNanoUSD ||
		int64(plan.MaximumRequests)*plan.MaximumChargeNanoUSD > plan.MaximumSpendNanoUSD {
		return fmt.Errorf("model review spend ceiling cannot reserve every allowed request")
	}
	return nil
}

func validateSnapshot(loaded *loadedInputs, baseURL string, now time.Time) error {
	if err := fillerbakeoff.ValidateOpenRouterSnapshot(loaded.snapshot); err != nil {
		return fmt.Errorf("validate model review route snapshot: %w", err)
	}
	age := now.Sub(loaded.snapshot.RetrievedAt)
	if loaded.snapshot.SourceBaseURL != baseURL || age < 0 || age > 24*time.Hour {
		return fmt.Errorf("model review route snapshot is stale or does not bind the request base")
	}
	for _, model := range loaded.snapshot.Models {
		if model.ID != loaded.plan.Model {
			continue
		}
		if model.CanonicalSlug != loaded.plan.ResolvedModel || !slices.Contains(model.InputModalities, "text") ||
			!slices.Contains(model.InputModalities, "audio") || !slices.Contains(model.OutputModalities, "text") {
			return fmt.Errorf("model review route lacks exact audio and structured-text capability")
		}
		for _, endpoint := range model.Endpoints {
			if endpoint.ProviderName == loaded.plan.UpstreamProvider && endpoint.ProviderSlug == loaded.plan.UpstreamProviderSlug &&
				endpoint.ZDR && endpoint.Status == 0 && endpoint.MaxCompletionTokens >= reviewMaxTokens &&
				slices.Contains(endpoint.SupportedParameters, "response_format") &&
				slices.Contains(endpoint.SupportedParameters, "structured_outputs") {
				return nil
			}
		}
		return fmt.Errorf("model review endpoint is not live, ZDR, fallback-free, and strict-output capable")
	}
	return fmt.Errorf("model review route snapshot omits the requested model")
}

func validateOutputLocations(config Config, root string) error {
	checkpoint, err := filepath.Abs(filepath.Clean(config.CheckpointDirectory))
	if err != nil {
		return err
	}
	output, err := filepath.Abs(filepath.Clean(config.OutputPath))
	if err != nil {
		return err
	}
	plan, err := filepath.Abs(filepath.Clean(config.PlanPath))
	if err != nil || checkpoint == output || output == plan || pathWithin(root, checkpoint) || pathWithin(root, output) ||
		pathWithin(checkpoint, output) || pathWithin(checkpoint, plan) {
		return fmt.Errorf("model review checkpoint and output must be distinct and outside the assembled input root")
	}
	return nil
}

func requirePrivateRegular(root, relative string, expectedBytes int64) error {
	path, err := resolveRootPath(root, relative)
	if err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || info.Size() != expectedBytes {
		return fmt.Errorf("private source must be a regular file at mode 0600 or stricter")
	}
	return nil
}

func pathWithin(root, target string) bool {
	relative, err := filepath.Rel(root, target)
	return err == nil && (relative == "." || filepath.IsLocal(relative) && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

func validAuthority(value fillersafetycorpus.FileAuthority) bool {
	return validRelative(value.Path) && validSHA256(value.SHA256) && value.Bytes > 0 && value.Bytes <= maximumDocumentBytes
}

func boundedID(value string) bool {
	return value != "" && value == strings.TrimSpace(value) && len(value) <= 128 && utf8.ValidString(value) &&
		!strings.HasPrefix(value, "/") && !strings.Contains(value, "\\") &&
		!strings.ContainsFunc(value, func(char rune) bool { return char <= ' ' || char == 0x7f })
}

func boundedText(value string) bool {
	return value != "" && value == strings.TrimSpace(value) && len(value) <= 128 && utf8.ValidString(value) &&
		!strings.ContainsFunc(value, func(char rune) bool { return char < ' ' || char == 0x7f })
}
