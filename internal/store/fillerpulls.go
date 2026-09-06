package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/loomarr/loomarr/internal/filler"
)

// Persisted filler pulls (§10 V35) — the approval gate for filler acquisition.
//
// ⚠ **A decided pull is KEPT, never deleted.** The queue's History tab answers "what did we
// agree to download, and when, and who said so", and a delete erases exactly that. This is the
// same reason §7 keeps deny reasons on title proposals rather than dropping refused rows.

const fillerPullSelect = `SELECT id, title, reason, proposed_by, status, note, plan_json,
	created_at, decided_at, decided_by FROM filler_pulls`

type fillerPullPlanDocument struct {
	Version  string                             `json:"version"`
	Intent   filler.AcquisitionIntent           `json:"intent"`
	Selected []filler.PullPlanRow               `json:"selected"`
	Rejected []filler.AcquisitionDecision       `json:"rejected"`
	Sources  []filler.AcquisitionSourceDecision `json:"sources"`
}

// GetPull reads one pull. ErrNotFound for an unknown id.
func (s *sqlStore) GetPull(ctx context.Context, id string) (filler.Pull, error) {
	row := s.db.QueryRowContext(ctx, s.ph(fillerPullSelect+` WHERE id = ?`), id)
	p, err := scanPull(row)
	if errors.Is(err, sql.ErrNoRows) {
		return filler.Pull{}, ErrNotFound
	}
	return p, err
}

// ListPulls returns pulls with the given status, NEWEST FIRST — the queue shows the most recent
// decision first, and an empty status means every pull.
//
// Ordering is explicit rather than left to the engine: an unordered list reshuffles between
// reads on Postgres, and an approval queue whose rows move under the pointer is its own bug.
func (s *sqlStore) ListPulls(ctx context.Context, status filler.PullStatus) ([]filler.Pull, error) {
	q := fillerPullSelect
	args := []any{}
	if status != "" {
		q += ` WHERE status = ?`
		args = append(args, string(status))
	}
	q += ` ORDER BY created_at DESC, id DESC`

	rows, err := s.db.QueryContext(ctx, s.ph(q), args...)
	if err != nil {
		return nil, fmt.Errorf("list filler pulls: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []filler.Pull
	for rows.Next() {
		p, err := scanPull(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// UpsertPull writes a pull by id.
//
// Every field is in the DO UPDATE list, unlike filler_sources: a pull has no
// independently-owned column: the composer writes it once and the approval path rewrites it
// wholesale with the operator's edits. There is no second writer to protect a field from.
func (s *sqlStore) UpsertPull(ctx context.Context, p filler.Pull) error {
	plan, err := json.Marshal(fillerPullPlanDocument{
		Version: filler.AcquisitionIntentVersion, Intent: p.Intent,
		Selected: p.Plan, Rejected: p.Rejected, Sources: p.Sources,
	})
	if err != nil {
		return fmt.Errorf("encode pull plan %s: %w", p.ID, err)
	}
	_, err = s.db.ExecContext(ctx, s.ph(
		`INSERT INTO filler_pulls (id, title, reason, proposed_by, status, note, plan_json,
		   created_at, decided_at, decided_by)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   title=excluded.title, reason=excluded.reason, proposed_by=excluded.proposed_by,
		   status=excluded.status, note=excluded.note, plan_json=excluded.plan_json,
		   decided_at=excluded.decided_at, decided_by=excluded.decided_by`),
		p.ID, p.Title, p.Reason, p.ProposedBy, string(p.Status), p.Note, string(plan),
		epoch(p.CreatedAt), epoch(p.DecidedAt), p.DecidedBy)
	if err != nil {
		return fmt.Errorf("upsert filler pull %s: %w", p.ID, err)
	}
	return nil
}

func scanPull(sc scannable) (filler.Pull, error) {
	var (
		p                    filler.Pull
		status, plan         string
		createdAt, decidedAt int64
	)
	if err := sc.Scan(&p.ID, &p.Title, &p.Reason, &p.ProposedBy, &status, &p.Note, &plan,
		&createdAt, &decidedAt, &p.DecidedBy); err != nil {
		return filler.Pull{}, err
	}
	p.Status = filler.PullStatus(status)
	p.CreatedAt = fromEpoch(createdAt)
	if decidedAt != 0 {
		p.DecidedAt = fromEpoch(decidedAt)
	}
	// A malformed plan is reported rather than silently read as an empty one: an approval that
	// committed zero rows because the JSON did not parse would look like a successful no-op.
	if plan != "" {
		if len(plan) > 0 && plan[0] == '[' {
			// V35-V65 persisted a bare source-row array. Keep those approvals legible and
			// executable; the V66 writer always upgrades the record to the document form.
			if err := json.Unmarshal([]byte(plan), &p.Plan); err != nil {
				return filler.Pull{}, fmt.Errorf("decode legacy pull plan %s: %w", p.ID, err)
			}
		} else {
			var doc fillerPullPlanDocument
			if err := json.Unmarshal([]byte(plan), &doc); err != nil {
				return filler.Pull{}, fmt.Errorf("decode pull plan %s: %w", p.ID, err)
			}
			if doc.Version != filler.AcquisitionIntentVersion {
				return filler.Pull{}, fmt.Errorf("decode pull plan %s: unsupported version %q", p.ID, doc.Version)
			}
			p.Intent, p.Plan, p.Rejected, p.Sources = doc.Intent, doc.Selected, doc.Rejected, doc.Sources
		}
	}
	return p, nil
}
