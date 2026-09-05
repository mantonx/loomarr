package quality

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"time"

	"github.com/loomarr/loomarr/internal/provision"
)

// AcquisitionRecorder translates a committed terminal provisioning transition
// into the closed discovery-quality vocabulary. It never returns an error: the
// title row is already committed, so observability cannot revise its state.
type AcquisitionRecorder struct {
	sink ObservationRecorder
	log  *slog.Logger
}

func NewAcquisitionRecorder(sink ObservationRecorder, log *slog.Logger) *AcquisitionRecorder {
	if log == nil {
		log = slog.Default()
	}
	return &AcquisitionRecorder{sink: sink, log: log}
}

func (r *AcquisitionRecorder) AcquisitionTerminal(ctx context.Context, record provision.Record) {
	if r == nil || r.sink == nil {
		return
	}
	if record.Key == "" || record.UpdatedAt.IsZero() {
		r.log.Warn("acquisition quality observation is missing authoritative fields")
		return
	}
	outcome := OutcomeFailed
	switch record.State {
	case provision.Available:
		outcome = OutcomePlayable
	case provision.Unavailable:
	case provision.Wanted, provision.Requested, provision.Downloading:
		return
	default:
		r.log.Warn("acquisition quality observation has unknown terminal state", "state", record.State)
		return
	}

	duration := time.Duration(0)
	if !record.RequestedAt.IsZero() && !record.UpdatedAt.Before(record.RequestedAt) {
		duration = record.UpdatedAt.Sub(record.RequestedAt)
		if duration > MaxObservationDuration {
			duration = MaxObservationDuration
		}
	}
	observation := Observation{
		IdempotencyKey: lifecycleObservationKey(string(record.Key), StageAcquisition, ""),
		At:             record.UpdatedAt,
		Stage:          StageAcquisition,
		Outcome:        outcome,
		Duration:       duration,
	}
	if err := r.sink.RecordQualityObservation(ctx, observation); err != nil {
		r.log.Warn("record acquisition quality observation", "outcome", outcome, "err", err)
	}
}

// SchedulingRecorder records the first-live Proposal journey. Failure and
// eventual success intentionally use distinct keys so the quality ledger can
// retain recovery without counting repeated reconcile attempts.
type SchedulingRecorder struct {
	sink ObservationRecorder
	log  *slog.Logger
}

func NewSchedulingRecorder(sink ObservationRecorder, log *slog.Logger) *SchedulingRecorder {
	if log == nil {
		log = slog.Default()
	}
	return &SchedulingRecorder{sink: sink, log: log}
}

func (r *SchedulingRecorder) ProposalSchedulingFailed(
	ctx context.Context,
	jobID string,
	at time.Time,
	duration time.Duration,
) {
	r.recordScheduling(ctx, jobID, at, duration, OutcomeFailed)
}

func (r *SchedulingRecorder) ProposalScheduled(
	ctx context.Context,
	jobID string,
	at time.Time,
	duration time.Duration,
) {
	r.recordScheduling(ctx, jobID, at, duration, OutcomeScheduled)
}

func (r *SchedulingRecorder) recordScheduling(
	ctx context.Context,
	jobID string,
	at time.Time,
	duration time.Duration,
	outcome Outcome,
) {
	if r == nil || r.sink == nil {
		return
	}
	if jobID == "" || at.IsZero() {
		r.log.Warn("scheduling quality observation is missing authoritative fields", "outcome", outcome)
		return
	}
	if duration < 0 {
		duration = 0
	} else if duration > MaxObservationDuration {
		duration = MaxObservationDuration
	}
	observation := Observation{
		IdempotencyKey: lifecycleObservationKey(jobID, StageScheduling, outcome),
		At:             at,
		Stage:          StageScheduling,
		Outcome:        outcome,
		Duration:       duration,
	}
	if err := r.sink.RecordQualityObservation(ctx, observation); err != nil {
		r.log.Warn("record scheduling quality observation", "outcome", outcome, "err", err)
	}
}

func lifecycleObservationKey(identity string, stage Stage, outcome Outcome) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(identity))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(stage))
	if outcome != "" {
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(outcome))
	}
	return hex.EncodeToString(hash.Sum(nil))
}
