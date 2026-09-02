package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

type InferenceEvaluationState string

const (
	InferenceReserved   InferenceEvaluationState = "reserved"
	InferenceCompleted  InferenceEvaluationState = "completed"
	InferenceFailed     InferenceEvaluationState = "failed"
	InferenceHeldBudget InferenceEvaluationState = "held_budget"
)

type InferenceTokens struct {
	Prompt, Completion, Reasoning, Cached, CacheWrite, Image, Audio, Video int64
}

type InferenceVersions struct {
	Evidence, Extractor, Prompt, Schema, Taxonomy, AdmissionPolicy, RolePolicy, CapabilitySnapshot string
}

type InferenceEvaluation struct {
	ID, CacheKey, ClipHash, RunID, Role, Rung                                            string
	State                                                                                InferenceEvaluationState
	RequestedProvider, RequestedModel, ResolvedProvider, ResolvedModel, UpstreamProvider string
	Modalities                                                                           []string
	DerivativeBytes, DerivativeDurationMS, DerivativePixels                              int64
	Tokens                                                                               InferenceTokens
	ChargedAmount, ChargedCurrency                                                       string
	ChargedNanoUSD, EstimatedNanoUSD, ReservedNanoUSD                                    int64
	PriceSnapshot                                                                        string
	LatencyMS                                                                            int64
	Attempts                                                                             int
	GenerationID, Outcome, FailureReason                                                 string
	Versions                                                                             InferenceVersions
	CreatedAt, UpdatedAt                                                                 time.Time
}

type InferenceBudget struct {
	PerClipNanoUSD int64
	PerDayNanoUSD  int64
	PerRunNanoUSD  int64
}

type InferenceSettlement struct {
	ResolvedProvider, ResolvedModel, UpstreamProvider string
	Tokens                                            InferenceTokens
	ChargedAmount, ChargedCurrency                    string
	ChargedNanoUSD, EstimatedNanoUSD                  int64
	PriceSnapshot                                     string
	LatencyMS                                         int64
	Attempts                                          int
	GenerationID, Outcome, FailureReason              string
	State                                             InferenceEvaluationState
	RetainReservation                                 bool
	UpdatedAt                                         time.Time
}

type InferenceEvaluationFilter struct {
	ClipHash string
	RunID    string
	Limit    int
}

const inferenceEvaluationSelect = `SELECT id, cache_key, clip_hash, run_id, role, rung, state,
	requested_provider, requested_model, resolved_provider, resolved_model, upstream_provider,
	modalities_json, derivative_bytes, derivative_duration_ms, derivative_pixels,
	prompt_tokens, completion_tokens, reasoning_tokens, cached_tokens, cache_write_tokens,
	image_tokens, audio_tokens, video_tokens, charged_amount, charged_currency,
	charged_nano_usd, estimated_nano_usd, reserved_nano_usd, price_snapshot,
	latency_ms, attempts, generation_id, evidence_version, extractor_version, prompt_version,
	schema_version, taxonomy_version, admission_policy_version, role_policy_version,
	capability_snapshot, outcome, failure_reason, created_at, updated_at
	FROM filler_inference_evaluations`

func (s *sqlStore) ReserveInferenceEvaluation(ctx context.Context, e InferenceEvaluation, budget InferenceBudget) (InferenceEvaluation, error) {
	if err := validateInferenceReservation(e, budget); err != nil {
		return InferenceEvaluation{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return InferenceEvaluation{}, fmt.Errorf("begin inference reservation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	e, err = s.reserveInferenceEvaluation(ctx, tx, e, budget)
	if err != nil {
		return InferenceEvaluation{}, err
	}
	if err := tx.Commit(); err != nil {
		return InferenceEvaluation{}, fmt.Errorf("commit inference reservation: %w", err)
	}
	return e, nil
}

func (s *sqlStore) reserveInferenceEvaluation(ctx context.Context, tx *sql.Tx, e InferenceEvaluation, budget InferenceBudget) (InferenceEvaluation, error) {
	dayStart := e.CreatedAt.UTC().Truncate(24 * time.Hour)
	scopes := []string{"clip:" + e.ClipHash, "day:" + dayStart.Format("2006-01-02")}
	if e.RunID != "" {
		scopes = append(scopes, "run:"+e.RunID)
	}
	slices.Sort(scopes)
	for _, scope := range scopes {
		if _, err := tx.ExecContext(ctx, s.ph(`INSERT INTO filler_inference_budget_guards (scope) VALUES (?) ON CONFLICT(scope) DO NOTHING`), scope); err != nil {
			return InferenceEvaluation{}, fmt.Errorf("create inference budget guard: %w", err)
		}
		lockSQL := `SELECT scope FROM filler_inference_budget_guards WHERE scope = ?`
		if s.dialect == DialectPostgres {
			lockSQL += ` FOR UPDATE`
		}
		var locked string
		if err := tx.QueryRowContext(ctx, s.ph(lockSQL), scope).Scan(&locked); err != nil {
			return InferenceEvaluation{}, fmt.Errorf("lock inference budget guard: %w", err)
		}
	}

	clipUsed, err := sumInferenceBudget(ctx, tx, s.ph, `clip_hash = ?`, e.ClipHash)
	if err != nil {
		return InferenceEvaluation{}, err
	}
	dayUsed, err := sumInferenceBudget(ctx, tx, s.ph, `created_at >= ? AND created_at < ?`, epoch(dayStart), epoch(dayStart.Add(24*time.Hour)))
	if err != nil {
		return InferenceEvaluation{}, err
	}
	runUsed := int64(0)
	if e.RunID != "" {
		runUsed, err = sumInferenceBudget(ctx, tx, s.ph, `run_id = ?`, e.RunID)
		if err != nil {
			return InferenceEvaluation{}, err
		}
	}

	e.CacheKey = InferenceCacheKey(e)
	e.State = InferenceReserved
	if exceedsBudget(clipUsed, e.ReservedNanoUSD, budget.PerClipNanoUSD) ||
		exceedsBudget(dayUsed, e.ReservedNanoUSD, budget.PerDayNanoUSD) ||
		(e.RunID != "" && exceedsBudget(runUsed, e.ReservedNanoUSD, budget.PerRunNanoUSD)) {
		e.State = InferenceHeldBudget
		e.FailureReason = "inference spend ceiling reached"
		e.ReservedNanoUSD = 0
	}
	e.UpdatedAt = e.CreatedAt
	if err := insertInferenceEvaluation(ctx, tx, s.ph, e); err != nil {
		return InferenceEvaluation{}, err
	}
	return e, nil
}

// InferenceCacheKey identifies all semantic and evidence inputs that can change an
// inference answer. Operational identity (ID, run, timestamps) and accounting do not
// participate, so retries of exactly the same evaluation remain comparable.
func InferenceCacheKey(e InferenceEvaluation) string {
	modalities := append([]string(nil), e.Modalities...)
	slices.Sort(modalities)
	payload := struct {
		ClipHash, Role, Rung, Provider, Model, Upstream                          string
		Modalities                                                               []string
		DerivativeBytes, DerivativeDurationMS, DerivativePixels                  int64
		Evidence, Extractor, Prompt, Schema, Taxonomy, Admission, Policy, Compat string
	}{
		ClipHash: e.ClipHash, Role: e.Role, Rung: e.Rung,
		Provider: e.RequestedProvider, Model: e.RequestedModel, Upstream: e.UpstreamProvider,
		Modalities: modalities, DerivativeBytes: e.DerivativeBytes,
		DerivativeDurationMS: e.DerivativeDurationMS, DerivativePixels: e.DerivativePixels,
		Evidence: e.Versions.Evidence, Extractor: e.Versions.Extractor,
		Prompt: e.Versions.Prompt, Schema: e.Versions.Schema, Taxonomy: e.Versions.Taxonomy,
		Admission: e.Versions.AdmissionPolicy, Policy: e.Versions.RolePolicy,
		Compat: e.Versions.CapabilitySnapshot,
	}
	raw, _ := json.Marshal(payload)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func validateInferenceReservation(e InferenceEvaluation, budget InferenceBudget) error {
	if e.ID == "" || e.ClipHash == "" || e.Role == "" || e.RequestedProvider == "" || e.RequestedModel == "" {
		return fmt.Errorf("inference reservation requires id, clip, role, provider, and model")
	}
	if e.CreatedAt.IsZero() {
		return fmt.Errorf("inference reservation requires created time")
	}
	if e.ReservedNanoUSD < 0 || budget.PerClipNanoUSD < 0 || budget.PerDayNanoUSD < 0 || budget.PerRunNanoUSD < 0 {
		return fmt.Errorf("inference budgets cannot be negative")
	}
	versions := []string{e.Versions.Evidence, e.Versions.Extractor, e.Versions.Prompt, e.Versions.Schema,
		e.Versions.Taxonomy, e.Versions.AdmissionPolicy, e.Versions.RolePolicy, e.Versions.CapabilitySnapshot}
	for _, version := range versions {
		if strings.TrimSpace(version) == "" {
			return fmt.Errorf("inference reservation requires every version identity")
		}
	}
	return nil
}

func exceedsBudget(used, reserve, limit int64) bool {
	if reserve == 0 {
		return false
	}
	return limit == 0 || used > limit || reserve > limit-used
}

func sumInferenceBudget(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, ph placeholder, where string, args ...any) (int64, error) {
	var used int64
	if err := q.QueryRowContext(ctx, ph(`SELECT COALESCE(SUM(reserved_nano_usd), 0) FROM filler_inference_evaluations WHERE `+where), args...).Scan(&used); err != nil {
		return 0, fmt.Errorf("sum inference budget: %w", err)
	}
	return used, nil
}

func insertInferenceEvaluation(ctx context.Context, tx *sql.Tx, ph placeholder, e InferenceEvaluation) error {
	modalities, err := json.Marshal(e.Modalities)
	if err != nil {
		return fmt.Errorf("marshal inference modalities: %w", err)
	}
	_, err = tx.ExecContext(ctx, ph(`INSERT INTO filler_inference_evaluations (
		id, cache_key, clip_hash, run_id, role, rung, state, requested_provider, requested_model,
		resolved_provider, resolved_model, upstream_provider, modalities_json,
		derivative_bytes, derivative_duration_ms, derivative_pixels,
		prompt_tokens, completion_tokens, reasoning_tokens, cached_tokens, cache_write_tokens,
		image_tokens, audio_tokens, video_tokens, charged_amount, charged_currency,
		charged_nano_usd, estimated_nano_usd, reserved_nano_usd, price_snapshot,
		latency_ms, attempts, generation_id, evidence_version, extractor_version, prompt_version,
		schema_version, taxonomy_version, admission_policy_version, role_policy_version,
		capability_snapshot, outcome, failure_reason, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`),
		e.ID, e.CacheKey, e.ClipHash, e.RunID, e.Role, e.Rung, string(e.State), e.RequestedProvider, e.RequestedModel,
		e.ResolvedProvider, e.ResolvedModel, e.UpstreamProvider, string(modalities),
		e.DerivativeBytes, e.DerivativeDurationMS, e.DerivativePixels,
		e.Tokens.Prompt, e.Tokens.Completion, e.Tokens.Reasoning, e.Tokens.Cached, e.Tokens.CacheWrite,
		e.Tokens.Image, e.Tokens.Audio, e.Tokens.Video, e.ChargedAmount, e.ChargedCurrency,
		e.ChargedNanoUSD, e.EstimatedNanoUSD, e.ReservedNanoUSD, e.PriceSnapshot,
		e.LatencyMS, e.Attempts, e.GenerationID, e.Versions.Evidence, e.Versions.Extractor, e.Versions.Prompt,
		e.Versions.Schema, e.Versions.Taxonomy, e.Versions.AdmissionPolicy, e.Versions.RolePolicy,
		e.Versions.CapabilitySnapshot, e.Outcome, e.FailureReason, epoch(e.CreatedAt), epoch(e.UpdatedAt))
	if err != nil {
		return fmt.Errorf("insert inference evaluation %s: %w", e.ID, err)
	}
	return nil
}

func (s *sqlStore) SettleInferenceEvaluation(ctx context.Context, id string, settlement InferenceSettlement) (InferenceEvaluation, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return InferenceEvaluation{}, err
	}
	defer func() { _ = tx.Rollback() }()
	got, overBudget, err := s.settleInferenceEvaluation(ctx, tx, id, settlement)
	if err != nil {
		return InferenceEvaluation{}, err
	}
	if err := tx.Commit(); err != nil {
		return InferenceEvaluation{}, err
	}
	if overBudget {
		return got, ErrInferenceBudgetExceeded
	}
	return got, nil
}

func (s *sqlStore) settleInferenceEvaluation(ctx context.Context, tx *sql.Tx, id string, settlement InferenceSettlement) (InferenceEvaluation, bool, error) {
	if settlement.UpdatedAt.IsZero() || settlement.ChargedNanoUSD < 0 || settlement.EstimatedNanoUSD < 0 {
		return InferenceEvaluation{}, false, fmt.Errorf("invalid inference settlement")
	}
	if settlement.State == "" {
		settlement.State = InferenceCompleted
	}
	if settlement.State != InferenceCompleted && settlement.State != InferenceFailed {
		return InferenceEvaluation{}, false, fmt.Errorf("invalid inference settlement state %q", settlement.State)
	}
	if settlement.RetainReservation && (settlement.State != InferenceFailed || settlement.ChargedNanoUSD != 0) {
		return InferenceEvaluation{}, false, fmt.Errorf("only an uncharged failed inference may retain its reservation")
	}
	var reserved int64
	var state string
	q := `SELECT reserved_nano_usd, state FROM filler_inference_evaluations WHERE id = ?`
	if s.dialect == DialectPostgres {
		q += ` FOR UPDATE`
	}
	if err := tx.QueryRowContext(ctx, s.ph(q), id).Scan(&reserved, &state); errors.Is(err, sql.ErrNoRows) {
		return InferenceEvaluation{}, false, ErrNotFound
	} else if err != nil {
		return InferenceEvaluation{}, false, err
	}
	if InferenceEvaluationState(state) != InferenceReserved {
		return InferenceEvaluation{}, false, ErrInferenceNotReserved
	}
	overBudget := settlement.ChargedNanoUSD > reserved
	if overBudget {
		settlement.State = InferenceHeldBudget
		settlement.FailureReason = "provider charge exceeded the pre-call reservation"
	}
	accountedNanoUSD := settlement.ChargedNanoUSD
	if settlement.RetainReservation {
		accountedNanoUSD = reserved
	}
	res, err := tx.ExecContext(ctx, s.ph(`UPDATE filler_inference_evaluations SET
		state = ?, resolved_provider = ?, resolved_model = ?, upstream_provider = ?,
		prompt_tokens = ?, completion_tokens = ?, reasoning_tokens = ?, cached_tokens = ?,
		cache_write_tokens = ?, image_tokens = ?, audio_tokens = ?, video_tokens = ?,
		charged_amount = ?, charged_currency = ?, charged_nano_usd = ?, estimated_nano_usd = ?,
		reserved_nano_usd = ?, price_snapshot = ?, latency_ms = ?, attempts = ?, generation_id = ?,
		outcome = ?, failure_reason = ?, updated_at = ? WHERE id = ? AND state = ?`),
		string(settlement.State), settlement.ResolvedProvider, settlement.ResolvedModel, settlement.UpstreamProvider,
		settlement.Tokens.Prompt, settlement.Tokens.Completion, settlement.Tokens.Reasoning, settlement.Tokens.Cached,
		settlement.Tokens.CacheWrite, settlement.Tokens.Image, settlement.Tokens.Audio, settlement.Tokens.Video,
		settlement.ChargedAmount, settlement.ChargedCurrency, settlement.ChargedNanoUSD, settlement.EstimatedNanoUSD,
		accountedNanoUSD, settlement.PriceSnapshot, settlement.LatencyMS, settlement.Attempts, settlement.GenerationID,
		settlement.Outcome, settlement.FailureReason, epoch(settlement.UpdatedAt), id, string(InferenceReserved))
	if err != nil {
		return InferenceEvaluation{}, false, fmt.Errorf("settle inference evaluation %s: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return InferenceEvaluation{}, false, ErrInferenceNotReserved
	}
	got, err := scanInferenceEvaluation(tx.QueryRowContext(ctx, s.ph(inferenceEvaluationSelect+` WHERE id = ?`), id))
	if err != nil {
		return InferenceEvaluation{}, false, err
	}
	return got, overBudget, nil
}

func (s *sqlStore) GetInferenceEvaluation(ctx context.Context, id string) (InferenceEvaluation, error) {
	e, err := scanInferenceEvaluation(s.db.QueryRowContext(ctx, s.ph(inferenceEvaluationSelect+` WHERE id = ?`), id))
	if errors.Is(err, sql.ErrNoRows) {
		return InferenceEvaluation{}, ErrNotFound
	}
	if err != nil {
		return InferenceEvaluation{}, fmt.Errorf("get inference evaluation %s: %w", id, err)
	}
	return e, nil
}

func (s *sqlStore) ListInferenceEvaluations(ctx context.Context, filter InferenceEvaluationFilter) ([]InferenceEvaluation, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	var clauses []string
	var args []any
	if filter.ClipHash != "" {
		clauses = append(clauses, "clip_hash = ?")
		args = append(args, filter.ClipHash)
	}
	if filter.RunID != "" {
		clauses = append(clauses, "run_id = ?")
		args = append(args, filter.RunID)
	}
	q := inferenceEvaluationSelect
	if len(clauses) > 0 {
		q += " WHERE " + strings.Join(clauses, " AND ")
	}
	q += " ORDER BY created_at DESC, id DESC LIMIT ?"
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, s.ph(q), args...)
	if err != nil {
		return nil, fmt.Errorf("list inference evaluations: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []InferenceEvaluation
	for rows.Next() {
		e, err := scanInferenceEvaluation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func scanInferenceEvaluation(sc scannable) (InferenceEvaluation, error) {
	var e InferenceEvaluation
	var state, modalities string
	var createdAt, updatedAt int64
	err := sc.Scan(&e.ID, &e.CacheKey, &e.ClipHash, &e.RunID, &e.Role, &e.Rung, &state,
		&e.RequestedProvider, &e.RequestedModel, &e.ResolvedProvider, &e.ResolvedModel, &e.UpstreamProvider,
		&modalities, &e.DerivativeBytes, &e.DerivativeDurationMS, &e.DerivativePixels,
		&e.Tokens.Prompt, &e.Tokens.Completion, &e.Tokens.Reasoning, &e.Tokens.Cached, &e.Tokens.CacheWrite,
		&e.Tokens.Image, &e.Tokens.Audio, &e.Tokens.Video, &e.ChargedAmount, &e.ChargedCurrency,
		&e.ChargedNanoUSD, &e.EstimatedNanoUSD, &e.ReservedNanoUSD, &e.PriceSnapshot,
		&e.LatencyMS, &e.Attempts, &e.GenerationID, &e.Versions.Evidence, &e.Versions.Extractor, &e.Versions.Prompt,
		&e.Versions.Schema, &e.Versions.Taxonomy, &e.Versions.AdmissionPolicy, &e.Versions.RolePolicy,
		&e.Versions.CapabilitySnapshot, &e.Outcome, &e.FailureReason, &createdAt, &updatedAt)
	if err != nil {
		return InferenceEvaluation{}, err
	}
	e.State = InferenceEvaluationState(state)
	if err := json.Unmarshal([]byte(modalities), &e.Modalities); err != nil {
		return InferenceEvaluation{}, fmt.Errorf("decode inference modalities: %w", err)
	}
	e.CreatedAt, e.UpdatedAt = fromEpoch(createdAt), fromEpoch(updatedAt)
	return e, nil
}
