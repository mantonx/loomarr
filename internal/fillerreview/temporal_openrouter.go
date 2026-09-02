package fillerreview

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/fillerbakeoff"
	"github.com/loomarr/loomarr/internal/fillereval"
	"github.com/loomarr/loomarr/internal/openroutermedia"
)

const (
	OpenRouterTemporalPromptVersion       = "filler-temporal-unit-role-openrouter-v7"
	OpenRouterTemporalResultSchemaVersion = 1
	OpenRouterTemporalResultContract      = "filler-temporal-openrouter-calibration-v1"
)

type temporalHostedUnitWire struct {
	Kind              string   `json:"kind"`
	DecisiveSignalIDs []string `json:"decisiveSignalIds"`
}

type temporalHostedRoleWire struct {
	Kind              string   `json:"kind"`
	DecisiveSignalIDs []string `json:"decisiveSignalIds"`
}

var errTemporalOpenRouterBudget = errors.New("OpenRouter temporal reservation exhausted")

type OpenRouterTemporalConfig struct {
	PackagePath              string
	SelectionPath            string
	CheckpointDir            string
	BaseURL                  string
	APIKey                   string
	Snapshot                 fillerbakeoff.OpenRouterSnapshot
	Model                    string
	ModelFamily              string
	UpstreamProvider         string
	UpstreamProviderSlug     string
	AssessorID               string
	ExpectedPackageCases     int
	ExpectedCalibrationCases int
	PerCaseTimeout           time.Duration
	MaxRequests              int
	MaxSpendNanoUSD          int64
	MaxChargeNanoUSD         int64
	AllowInsecureTestURL     bool
	Client                   *http.Client
	Now                      func() time.Time
}

type OpenRouterTemporalResult struct {
	SchemaVersion             int                              `json:"schemaVersion"`
	ContractVersion           string                           `json:"contractVersion"`
	SelectionSHA256           string                           `json:"selectionSha256"`
	CapabilitySnapshotSHA256  string                           `json:"capabilitySnapshotSha256"`
	ResolvedModel             string                           `json:"resolvedModel"`
	UpstreamProvider          string                           `json:"upstreamProvider"`
	UpstreamProviderSlug      string                           `json:"upstreamProviderSlug"`
	PromptSHA256              string                           `json:"promptSha256"`
	MaxRequests               int                              `json:"maxRequests"`
	MaxSpendNanoUSD           int64                            `json:"maxSpendNanoUsd"`
	MaxChargeNanoUSD          int64                            `json:"maxChargeNanoUsd"`
	Requests                  int                              `json:"requests"`
	ChargedNanoUSD            int64                            `json:"chargedNanoUsd"`
	ConsumedNanoUSD           int64                            `json:"consumedNanoUsd"`
	UnknownChargeReservations int                              `json:"unknownChargeReservations"`
	CompletedAt               time.Time                        `json:"completedAt"`
	AssessmentSet             fillereval.TemporalAssessmentSet `json:"assessmentSet"`
	Attempts                  []TemporalOpenRouterAttempt      `json:"attempts"`
}

// RunOpenRouterTemporalAssessment executes only the aliases in the immutable
// calibration selection. It is serial, makes at most one provider attempt per
// axis, reserves before HTTP, and preserves semantic versus operational state.
func RunOpenRouterTemporalAssessment(ctx context.Context, config OpenRouterTemporalConfig) (OpenRouterTemporalResult, error) {
	baseURL, client, now, err := validateOpenRouterTemporalConfig(config, true)
	if err != nil {
		return OpenRouterTemporalResult{}, err
	}
	loaded, err := LoadTemporalCalibrationPackage(config.PackagePath, config.SelectionPath, config.ExpectedPackageCases, config.ExpectedCalibrationCases)
	if err != nil {
		return OpenRouterTemporalResult{}, err
	}
	return runOpenRouterTemporalInference(ctx, config, loaded.inferencePackage(), baseURL, client, now)
}

// RunOpenRouterTemporalModelAssessment executes one complete freshly blinded
// model-panel package. It shares all paid transport, durable reservation,
// checkpoint, validation, and accounting behaviour with legacy calibration;
// only the verified package adapter differs.
func RunOpenRouterTemporalModelAssessment(ctx context.Context, config OpenRouterTemporalConfig) (OpenRouterTemporalResult, error) {
	baseURL, client, now, err := validateOpenRouterTemporalConfig(config, false)
	if err != nil {
		return OpenRouterTemporalResult{}, err
	}
	if config.SelectionPath != "" || config.ExpectedPackageCases != config.ExpectedCalibrationCases {
		return OpenRouterTemporalResult{}, fmt.Errorf("OpenRouter temporal model panel requires one complete package and no calibration selection")
	}
	loaded, err := loadTemporalModelInferencePackage(config.PackagePath, config.ExpectedPackageCases)
	if err != nil {
		return OpenRouterTemporalResult{}, err
	}
	return runOpenRouterTemporalInference(ctx, config, loaded, baseURL, client, now)
}

func runOpenRouterTemporalInference(ctx context.Context, config OpenRouterTemporalConfig, loaded temporalInferencePackage, baseURL string, client *http.Client, now func() time.Time) (result OpenRouterTemporalResult, err error) {
	identity := buildTemporalOpenRouterCheckpointIdentity(config, loaded, baseURL)
	activeLock, err := acquireOpenRouterActiveRunLock(config.CheckpointDir, identity, now, nil)
	if err != nil {
		return OpenRouterTemporalResult{}, err
	}
	defer func() {
		if releaseErr := activeLock.release(); releaseErr != nil {
			result = OpenRouterTemporalResult{}
			err = errors.Join(err, releaseErr)
		}
	}()
	checkpoint, err := loadTemporalOpenRouterCheckpoint(config.CheckpointDir, identity)
	if err != nil {
		return OpenRouterTemporalResult{}, err
	}
	if err := validateTemporalOpenRouterCheckpointAgainstSelection(checkpoint, loaded); err != nil {
		return OpenRouterTemporalResult{}, err
	}
	for _, attempt := range checkpoint.Attempts {
		if attempt.State == temporalOpenRouterAttemptReserved {
			return OpenRouterTemporalResult{}, fmt.Errorf("OpenRouter temporal checkpoint has an unsettled crash reservation for alias %q axis %q", attempt.Alias, attempt.Axis)
		}
	}
	completed := make(map[string]struct{}, len(checkpoint.Assessments))
	for _, assessment := range checkpoint.Assessments {
		completed[assessment.Alias] = struct{}{}
	}
	root := filepath.Dir(config.PackagePath)
	for index, item := range loaded.Cases {
		if _, exists := completed[item.Alias]; exists {
			continue
		}
		assessment, err := assessOpenRouterTemporalCase(ctx, client, baseURL, root, config, loaded.Signals[index], item, &checkpoint, now)
		if err != nil {
			return OpenRouterTemporalResult{}, err
		}
		checkpoint.Assessments = append(checkpoint.Assessments, assessment)
		if err := persistTemporalOpenRouterCheckpoint(config.CheckpointDir, checkpoint); err != nil {
			return OpenRouterTemporalResult{}, fmt.Errorf("persist completed OpenRouter temporal case: %w", err)
		}
		completed[item.Alias] = struct{}{}
	}
	set := fillereval.TemporalAssessmentSet{
		SchemaVersion: fillereval.TemporalAssessmentSchemaVersion, ContractVersion: fillereval.TemporalAssessmentContractVersion,
		BatchID: loaded.BatchID, PackageSHA256: loaded.PackageSHA256,
		Assessor: fillereval.TemporalAssessorIdentity{
			ID: config.AssessorID, Provider: "openrouter", Model: config.Model, ModelFamily: config.ModelFamily,
			ModelDigest: identity.CapabilitySnapshotSHA256, PromptVersion: OpenRouterTemporalPromptVersion,
		},
		Assessments: slices.Clone(checkpoint.Assessments),
	}
	if err := fillereval.ValidateTemporalAssessmentSet(set, loaded.BatchID, loaded.PackageSHA256, loaded.Signals); err != nil {
		return OpenRouterTemporalResult{}, fmt.Errorf("validate OpenRouter temporal assessment set: %w", err)
	}
	consumed, err := temporalOpenRouterCheckpointSpend(checkpoint)
	if err != nil {
		return OpenRouterTemporalResult{}, err
	}
	result = OpenRouterTemporalResult{
		SchemaVersion: OpenRouterTemporalResultSchemaVersion, ContractVersion: OpenRouterTemporalResultContract,
		SelectionSHA256: loaded.SelectionSHA256, CapabilitySnapshotSHA256: identity.CapabilitySnapshotSHA256,
		ResolvedModel: identity.ResolvedModel, UpstreamProvider: config.UpstreamProvider, UpstreamProviderSlug: config.UpstreamProviderSlug,
		PromptSHA256: identity.PromptSHA256, MaxRequests: config.MaxRequests, MaxSpendNanoUSD: config.MaxSpendNanoUSD,
		MaxChargeNanoUSD: config.MaxChargeNanoUSD, Requests: len(checkpoint.Attempts), ConsumedNanoUSD: consumed,
		CompletedAt: now().UTC(), AssessmentSet: set, Attempts: slices.Clone(checkpoint.Attempts),
	}
	for _, attempt := range checkpoint.Attempts {
		result.ChargedNanoUSD += attempt.ChargedNanoUSD
		if attempt.State == temporalOpenRouterAttemptUnsettled {
			result.UnknownChargeReservations++
		}
	}
	if err := validateOpenRouterTemporalInferenceResult(result, loaded); err != nil {
		return OpenRouterTemporalResult{}, err
	}
	return result, nil
}

func assessOpenRouterTemporalCase(ctx context.Context, client *http.Client, baseURL, root string, config OpenRouterTemporalConfig, signals fillereval.TemporalCaseSignals, item TemporalReviewCase, checkpoint *temporalOpenRouterCheckpoint, now func() time.Time) (fillereval.TemporalAssessment, error) {
	caseCtx, cancel := context.WithTimeout(ctx, config.PerCaseTimeout)
	defer cancel()
	content, images, err := temporalReviewerContent(root, item)
	if err != nil {
		failure := &temporalCallError{code: fillereval.TemporalFailureEvidence, detail: err.Error()}
		return temporalFailedAssessment(item.Alias, now().UTC(), []fillereval.TemporalInferenceCall{{Axis: "unit", Attempt: 1, OperationalFailure: failure.code}}, failure), nil
	}
	calls := make([]fillereval.TemporalInferenceCall, 0, 2)
	var unit temporalHostedUnitWire
	call, failure, err := callOpenRouterTemporalClaim(caseCtx, client, baseURL, config, item, "unit", content, images, &unit, checkpoint, now)
	if err != nil {
		return fillereval.TemporalAssessment{}, err
	}
	calls = append(calls, call)
	if failure != nil {
		return temporalFailedAssessment(item.Alias, now().UTC(), calls, failure), nil
	}
	unitSignalIDs := temporalHostedSignalIDs(unit.DecisiveSignalIDs)
	assessment := fillereval.TemporalAssessment{Alias: item.Alias, Unit: &fillereval.UnitAssessment{Kind: fillereval.UnitKind(unit.Kind), DecisiveSignalIDs: unitSignalIDs, Reason: temporalHostedReason("unit", unit.Kind, unitSignalIDs)}}
	if assessment.Unit.Kind == fillereval.UnitStandalone {
		var role temporalHostedRoleWire
		roleContent := content + "\nThe independent unit pass classified this span as standalone. Classify only its semantic role."
		call, failure, err = callOpenRouterTemporalClaim(caseCtx, client, baseURL, config, item, "role", roleContent, images, &role, checkpoint, now)
		if err != nil {
			return fillereval.TemporalAssessment{}, err
		}
		calls = append(calls, call)
		if failure != nil {
			return temporalFailedAssessment(item.Alias, now().UTC(), calls, failure), nil
		}
		roleSignalIDs := temporalHostedSignalIDs(role.DecisiveSignalIDs)
		assessment.Role = &fillereval.RoleAssessment{Kind: fillereval.TemporalRole(role.Kind), DecisiveSignalIDs: roleSignalIDs, Reason: temporalHostedReason("role", role.Kind, roleSignalIDs)}
	}
	assessment = fillereval.NormalizeTemporalAssessment(assessment)
	assessment.Inference = temporalInferenceFromCalls(now().UTC(), calls)
	one := fillereval.TemporalAssessmentSet{
		SchemaVersion: fillereval.TemporalAssessmentSchemaVersion, ContractVersion: fillereval.TemporalAssessmentContractVersion,
		BatchID: checkpoint.Identity.BatchID, PackageSHA256: checkpoint.Identity.PackageSHA256,
		Assessor:    fillereval.TemporalAssessorIdentity{ID: checkpoint.Identity.AssessorID, Provider: "openrouter", Model: checkpoint.Identity.Model, ModelFamily: checkpoint.Identity.ModelFamily, ModelDigest: checkpoint.Identity.CapabilitySnapshotSHA256, PromptVersion: checkpoint.Identity.PromptVersion},
		Assessments: []fillereval.TemporalAssessment{assessment},
	}
	if err := fillereval.ValidateTemporalAssessmentSet(one, checkpoint.Identity.BatchID, checkpoint.Identity.PackageSHA256, []fillereval.TemporalCaseSignals{signals}); err != nil {
		failure := &temporalCallError{code: fillereval.TemporalFailureInvalidResponse, detail: err.Error()}
		calls[len(calls)-1].OperationalFailure = failure.code
		markLastTemporalOpenRouterAttemptFailed(checkpoint, failure.code)
		if err := persistTemporalOpenRouterCheckpoint(config.CheckpointDir, *checkpoint); err != nil {
			return fillereval.TemporalAssessment{}, fmt.Errorf("persist invalid OpenRouter temporal claim: %w", err)
		}
		return temporalFailedAssessment(item.Alias, now().UTC(), calls, failure), nil
	}
	return assessment, nil
}

func callOpenRouterTemporalClaim(ctx context.Context, client *http.Client, baseURL string, config OpenRouterTemporalConfig, item TemporalReviewCase, axis, content string, images []string, target any, checkpoint *temporalOpenRouterCheckpoint, now func() time.Time) (call fillereval.TemporalInferenceCall, failure *temporalCallError, terminalErr error) {
	attemptNumber := 1
	schema, prompt, schemaName := temporalHostedUnitSchema(item), temporalHostedUnitSystemPrompt, "filler_temporal_unit"
	if axis == "role" {
		attemptNumber, schema, prompt, schemaName = 2, temporalHostedRoleSchema(item), temporalHostedRoleSystemPrompt, "filler_temporal_role"
	}
	call.Axis, call.Attempt = axis, attemptNumber
	started := time.Now()
	result, err := openroutermedia.Call(ctx, client, baseURL, openroutermedia.Config{
		APIKey: config.APIKey, Model: config.Model, ResolvedModel: openRouterTemporalModel(config.Snapshot, config.Model).CanonicalSlug,
		UpstreamProvider: config.UpstreamProvider, ProviderSlug: config.UpstreamProviderSlug,
		SchemaName: schemaName, Schema: schema, SystemPrompt: prompt, Content: content, Images: images,
		MaxTokens: 1024, MaxChargeNanoUSD: config.MaxChargeNanoUSD, DisableReasoning: true,
		Title: "Loomarr filler temporal calibration",
		Reserve: func(requestSHA256 string) error {
			spent, spendErr := temporalOpenRouterCheckpointSpend(*checkpoint)
			if spendErr != nil {
				return spendErr
			}
			if len(checkpoint.Attempts) >= config.MaxRequests || spent > config.MaxSpendNanoUSD-config.MaxChargeNanoUSD {
				return fmt.Errorf("%w before alias %q axis %q", errTemporalOpenRouterBudget, item.Alias, axis)
			}
			checkpoint.Attempts = append(checkpoint.Attempts, TemporalOpenRouterAttempt{
				Alias: item.Alias, Axis: axis, Attempt: attemptNumber, RequestedAt: now().UTC(), RequestSHA256: requestSHA256,
				State: temporalOpenRouterAttemptReserved, ReservedNanoUSD: config.MaxChargeNanoUSD,
			})
			return persistTemporalOpenRouterCheckpoint(config.CheckpointDir, *checkpoint)
		},
	})
	call.LatencyMS = max(int64(0), time.Since(started).Milliseconds())
	call.ResponseSHA256 = result.ResponseSHA256
	call.PromptTokens, call.CompletionTokens = result.PromptTokens, result.CompletionTokens
	if err == nil {
		if decodeErr := decodeStrictReviewJSON([]byte(result.StructuredOutput), target); decodeErr != nil {
			err = fmt.Errorf("%s assessment JSON is invalid: %w", axis, decodeErr)
		}
	}
	if err != nil {
		failure = classifyTemporalOpenRouterFailure(ctx, result, err)
		call.OperationalFailure = failure.code
	}
	if result.RequestSHA256 == "" || len(checkpoint.Attempts) == 0 || checkpoint.Attempts[len(checkpoint.Attempts)-1].RequestSHA256 != result.RequestSHA256 {
		if errors.Is(err, errTemporalOpenRouterBudget) {
			return call, failure, nil
		}
		return call, failure, fmt.Errorf("OpenRouter temporal call for alias %q axis %q did not acquire a durable reservation: %w", item.Alias, axis, err)
	}
	attempt := &checkpoint.Attempts[len(checkpoint.Attempts)-1]
	attempt.ResponseSHA256, attempt.GenerationID = result.ResponseSHA256, result.GenerationID
	attempt.LatencyMS, attempt.PromptTokens, attempt.CompletionTokens = call.LatencyMS, call.PromptTokens, call.CompletionTokens
	if result.ChargeKnown {
		attempt.ChargedAmountUSD, attempt.ChargedNanoUSD = result.ChargedAmountUSD, result.ChargedNanoUSD
	}
	if failure == nil {
		attempt.State = temporalOpenRouterAttemptAccepted
	} else {
		attempt.OperationalFailure = failure.code
		if result.ChargeKnown {
			attempt.State = temporalOpenRouterAttemptFailed
		} else {
			attempt.State = temporalOpenRouterAttemptUnsettled
		}
	}
	if persistErr := persistTemporalOpenRouterCheckpoint(config.CheckpointDir, *checkpoint); persistErr != nil {
		return call, failure, fmt.Errorf("persist OpenRouter temporal settlement for alias %q axis %q: %w", item.Alias, axis, persistErr)
	}
	return call, failure, nil
}

func temporalHostedReason(axis, kind string, signalIDs []string) string {
	return fmt.Sprintf("Hosted %s class %s selected from decisive signals %s.", axis, kind, strings.Join(signalIDs, ", "))
}

func temporalHostedSignalIDs(signalIDs []string) []string {
	normalized := slices.Clone(signalIDs)
	slices.Sort(normalized)
	return slices.Compact(normalized)
}

func temporalFailedAssessment(alias string, assessedAt time.Time, calls []fillereval.TemporalInferenceCall, failure *temporalCallError) fillereval.TemporalAssessment {
	return fillereval.TemporalAssessment{
		Alias: alias, OperationalFailure: &fillereval.TemporalOperationalFailure{Code: failure.code, Detail: boundedTemporalDetail(failure.detail), Retryable: failure.retryable},
		Inference: temporalInferenceFromCalls(assessedAt, calls),
	}
}

func classifyTemporalOpenRouterFailure(ctx context.Context, result openroutermedia.Result, err error) *temporalCallError {
	if errors.Is(err, errTemporalOpenRouterBudget) {
		return &temporalCallError{code: fillereval.TemporalFailureContextExhausted, detail: err.Error()}
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return &temporalCallError{code: fillereval.TemporalFailureTimeout, detail: "per-case inference deadline exceeded", retryable: true}
	}
	var statusErr *openroutermedia.StatusError
	if errors.As(err, &statusErr) {
		retryable := statusErr.StatusCode == http.StatusRequestTimeout || statusErr.StatusCode == http.StatusTooManyRequests || statusErr.StatusCode >= 500
		return &temporalCallError{code: fillereval.TemporalFailureProvider, detail: err.Error(), retryable: retryable}
	}
	if result.ResponseSHA256 != "" {
		return &temporalCallError{code: fillereval.TemporalFailureInvalidResponse, detail: err.Error()}
	}
	return &temporalCallError{code: fillereval.TemporalFailureProvider, detail: err.Error(), retryable: true}
}

func markLastTemporalOpenRouterAttemptFailed(checkpoint *temporalOpenRouterCheckpoint, code fillereval.TemporalFailureCode) {
	if len(checkpoint.Attempts) == 0 {
		return
	}
	attempt := &checkpoint.Attempts[len(checkpoint.Attempts)-1]
	attempt.State, attempt.OperationalFailure = temporalOpenRouterAttemptFailed, code
}
