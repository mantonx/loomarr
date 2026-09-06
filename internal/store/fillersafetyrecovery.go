package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/loomarr/loomarr/internal/fillersafety"
)

// RecoverInterruptedSpokenSafetyRuns closes attempts owned by a previous
// process. Unknown provider settlement keeps the full reservation accounted.
func (s *sqlStore) RecoverInterruptedSpokenSafetyRuns(ctx context.Context, at time.Time) (int, error) {
	if at.IsZero() {
		return 0, fillersafety.ErrLedgerInvalid
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM filler_spoken_safety_runs r
		WHERE NOT EXISTS (SELECT 1 FROM filler_spoken_safety_events e
			WHERE e.run_id = r.id AND e.kind = 'terminal')
		ORDER BY created_at ASC, id ASC`)
	if err != nil {
		return 0, fmt.Errorf("list interrupted spoken-safety runs: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}

	recovered := 0
	for _, id := range ids {
		didRecover, err := s.recoverSpokenSafetyRun(ctx, id, at)
		if err != nil {
			return recovered, err
		}
		if didRecover {
			recovered++
		}
	}
	return recovered, nil
}

func (s *sqlStore) recoverSpokenSafetyRun(ctx context.Context, runID string, at time.Time) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	query := spokenSafetyRunSelect + ` WHERE id = ?`
	if s.dialect == DialectPostgres {
		query += ` FOR UPDATE`
	}
	run, err := scanSpokenSafetyRun(tx.QueryRowContext(ctx, s.ph(query), runID))
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	events, err := listSpokenSafetyEvents(ctx, tx, s.ph, runID)
	if err != nil {
		return false, err
	}
	if len(events) > 0 && events[len(events)-1].Kind == fillersafety.LedgerTerminal {
		return false, nil
	}

	if len(events) == 0 {
		event := fillersafety.LedgerEvent{
			ID: recoveryLedgerID(runID, "source"), RunID: runID, Ordinal: 0,
			Kind: fillersafety.LedgerSourcePlanned,
			Source: &fillersafety.SourcePlanned{
				Audio: fillersafety.Span{EndMS: run.DurationMS}, Video: fillersafety.Span{EndMS: run.DurationMS},
			},
			CreatedAt: at,
		}
		if err := s.appendSpokenSafetyEvent(ctx, tx, event); err != nil {
			return false, err
		}
		events = append(events, event)
	}
	if len(events) == 1 {
		event := fillersafety.LedgerEvent{
			ID: recoveryLedgerID(runID, "proposal"), RunID: runID, Ordinal: 1,
			Kind: fillersafety.LedgerProposalCompleted,
			Proposal: &fillersafety.ProposalCompleted{
				State: fillersafety.ProposalFailed, ProposerSHA256: run.ProposerSHA256, Candidates: []fillersafety.Candidate{},
			},
			CreatedAt: at,
		}
		if err := s.appendSpokenSafetyEvent(ctx, tx, event); err != nil {
			return false, err
		}
		events = append(events, event)
	}

	settled := map[string]struct{}{}
	for _, event := range events {
		if event.Settle != nil {
			settled[event.Settle.ReservationEventID] = struct{}{}
		}
	}
	for _, reservation := range slices.Clone(events) {
		if reservation.Reserve == nil || reservation.Reserve.State != fillersafety.ReservationAccepted {
			continue
		}
		if _, ok := settled[reservation.ID]; ok {
			continue
		}
		stored, err := scanInferenceEvaluation(tx.QueryRowContext(ctx,
			s.ph(inferenceEvaluationSelect+` WHERE id = ?`), reservation.Reserve.EvaluationID))
		if err != nil {
			return false, fmt.Errorf("read interrupted spoken-safety inference: %w", err)
		}
		if stored.State == InferenceReserved {
			stored, _, err = s.settleInferenceEvaluation(ctx, tx, stored.ID, InferenceSettlement{
				State: InferenceFailed, FailureReason: "process interrupted before settlement",
				RetainReservation: true, UpdatedAt: at,
			})
			if err != nil {
				return false, err
			}
		}
		event := fillersafety.LedgerEvent{
			ID: recoveryLedgerID(runID, reservation.ID), RunID: runID, Ordinal: len(events),
			Kind: fillersafety.LedgerInferenceSettled,
			Settle: &fillersafety.InferenceSettled{
				ReservationEventID: reservation.ID, EvaluationID: stored.ID,
				State: fillersafety.SettlementUnknown, Failure: fillersafety.FailureInterrupted,
				AccountedNanoUSD: stored.ReservedNanoUSD,
			},
			CreatedAt: at,
		}
		if err := s.appendSpokenSafetyEvent(ctx, tx, event); err != nil {
			return false, err
		}
		events = append(events, event)
	}

	evidence := recoveredSpokenSafetyEvidence(events)
	ids := make([]string, 0, len(events))
	for _, event := range events {
		ids = append(ids, event.ID)
	}
	terminal := fillersafety.LedgerEvent{
		ID: recoveryLedgerID(runID, "terminal"), RunID: runID, Ordinal: len(events),
		Kind: fillersafety.LedgerTerminal,
		Terminal: &fillersafety.TerminalResult{
			Evidence: evidence, Result: fillersafety.Reduce(evidence), EventIDs: ids,
		},
		CreatedAt: at,
	}
	if err := s.appendSpokenSafetyEvent(ctx, tx, terminal); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func recoveredSpokenSafetyEvidence(events []fillersafety.LedgerEvent) fillersafety.Evidence {
	failed := fillersafety.Evidence{
		ProposalState: fillersafety.ProposalFailed, Candidates: []fillersafety.Candidate{},
		Audio: []fillersafety.AudioAssessment{}, Video: fillersafety.VideoNotRun,
	}
	var proposal *fillersafety.ProposalCompleted
	for _, event := range events {
		if event.Proposal != nil {
			proposal = event.Proposal
			break
		}
	}
	if proposal == nil || proposal.State != fillersafety.ProposalComplete {
		return failed
	}
	evidence := fillersafety.Evidence{
		ProposalState: fillersafety.ProposalComplete, Candidates: slices.Clone(proposal.Candidates),
		Audio: make([]fillersafety.AudioAssessment, 0, len(proposal.Candidates)), Video: fillersafety.VideoNotRun,
	}
	for _, candidate := range proposal.Candidates {
		evidence.Audio = append(evidence.Audio, fillersafety.AudioAssessment{
			CandidateID: candidate.ID, State: recoveredAudioState(events, candidate.ID),
		})
	}
	allAbsent := true
	for _, assessment := range evidence.Audio {
		if assessment.State != fillersafety.AudioAbsent {
			allAbsent = false
			break
		}
	}
	if allAbsent {
		evidence.Video = recoveredVideoState(events)
	}
	return evidence
}

func recoveredAudioState(events []fillersafety.LedgerEvent, candidateID string) fillersafety.AudioState {
	states := []fillersafety.AudioState{}
	for _, event := range events {
		if event.Reserve == nil || event.Reserve.CandidateID != candidateID {
			continue
		}
		state := fillersafety.AudioFailed
		for _, later := range events {
			if later.Settle != nil && later.Settle.ReservationEventID == event.ID && later.Settle.State == fillersafety.SettlementCompleted {
				state = fillersafety.AudioState(later.Settle.Outcome)
			}
		}
		states = append(states, state)
	}
	if len(states) == 1 {
		return states[0]
	}
	return fillersafety.AudioFailed
}

func recoveredVideoState(events []fillersafety.LedgerEvent) fillersafety.VideoState {
	states := []fillersafety.VideoState{}
	for _, event := range events {
		if event.Reserve == nil || event.Reserve.CandidateID != "" {
			continue
		}
		state := fillersafety.VideoFailed
		for _, later := range events {
			if later.Settle != nil && later.Settle.ReservationEventID == event.ID && later.Settle.State == fillersafety.SettlementCompleted {
				state = fillersafety.VideoState(later.Settle.Outcome)
			}
		}
		states = append(states, state)
	}
	if len(states) == 1 {
		return states[0]
	}
	return fillersafety.VideoFailed
}

func recoveryLedgerID(runID, part string) string {
	digest := sha256.Sum256([]byte("spoken-safety-recovery\x00" + runID + "\x00" + part))
	return fmt.Sprintf("recovery-%x", digest[:12])
}
