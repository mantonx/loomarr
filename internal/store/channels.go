package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/loomarr/loomarr/internal/schedule"
)

// The two legal channel broadcast codecs (§9.1 V50). Kept as plain strings to match
// how codecs flow everywhere else (playout.MediaFormat.VideoCodec, the plan capability
// maps) — a fresh typed enum here would only need converting at every boundary. H264 is
// the migration default, so an un-backfilled channel broadcasts h264/TS exactly as today.
const (
	BroadcastCodecH264 = "h264"
	BroadcastCodecHEVC = "hevc"
)

// Channel is the persisted form of a scheduler channel (§9). It carries the
// domain schedule.Channel fields plus persistence concerns: the approved lineup
// and last-computed desired lineup (stored as JSON blobs, like titles.title_json)
// and the reconcile deadline that drives the sweep's ClaimDueChannels. The
// reconcile engine maps between this and the pure schedule.* types.
type Channel struct {
	schedule.Channel
	// Revision is the optimistic concurrency token for every channel-row mutation.
	// Zero is the create sentinel accepted by SaveChannel; persisted rows start at 1.
	Revision int64
	// Lineup is the approved []LineupEntry (the intent-level "what should play").
	Lineup []schedule.LineupEntry
	// Desired is the last-computed []Slot (desired lineup) — persisted so the
	// sweep can diff without recomputing when nothing changed, and so a restart
	// resumes from the known desired state.
	Desired []schedule.Slot
	// Policy is the channel's ChannelPolicy (programming-design §2): scope/audience/
	// separation/ordering/seasonal + the last reconcile's applied relaxations.
	// Stored as policy_json, sparse (omitted fields resolve to built-in defaults).
	Policy schedule.ChannelPolicy
	// BroadcastCodec is the channel's uniform broadcast video codec (§9.1 V50): the
	// codec every program on the timeline is normalized TO so the stream stays
	// single-codec (which is what makes HEVC-fMP4 legal — fMP4 can't switch codec
	// mid-stream). Derived from the majority of the library titles' codecs at curation,
	// not probed at runtime. One of BroadcastCodecH264 / BroadcastCodecHEVC; empty
	// (an un-backfilled row read before the DDL default applies) reads as H264.
	BroadcastCodec string
	// PlayoutAnchor is the immutable wall-clock origin of this channel's accepted
	// broadcast cycle. Building/empty channels may be zero until first activation.
	PlayoutAnchor time.Time
	// ReconcileDeadline: the channel is due for a sweep reconcile at/before this
	// time. Leased forward on claim (§9/§18).
	ReconcileDeadline time.Time
}

func (s *sqlStore) GetChannel(ctx context.Context, id string) (Channel, error) {
	row := s.db.QueryRowContext(ctx, s.ph(channelSelect+` WHERE id = ?`), id)
	return scanChannel(row)
}

func (s *sqlStore) GetChannelByNumber(ctx context.Context, number int) (Channel, error) {
	row := s.db.QueryRowContext(ctx, s.ph(channelSelect+` WHERE number = ?`), number)
	return scanChannel(row)
}

// GetChannelByIntentRef finds the channel bound to a suggestion job. A channel's `intent_ref` IS
// the job id that produced it, so this answers "which channel does this proposal belong to?" —
// asked once per bind and once per auto-curate consideration.
//
// ⚠ Indexed (00037), and it replaces two byte-identical `ListChannels`-then-linear-scan helpers
// that had been copy-pasted into `binder` and `recurate`. Two packages walking the whole channel
// table for one row is the shape a missing store method leaves behind.
//
// ⚠ Returns ErrNotFound rather than a zero Channel. Both former callers treated "no match" as
// benign (a job with no channel is simply not an auto-curate candidate), and they still can — but
// that has to be the CALLER's decision: a lookup that silently answers with an empty struct is one
// a caller can forget to check, and the zero Channel has an empty ID that reads as valid.
func (s *sqlStore) GetChannelByIntentRef(ctx context.Context, intentRef string) (Channel, error) {
	row := s.db.QueryRowContext(ctx, s.ph(channelSelect+` WHERE intent_ref = ?`), intentRef)
	return scanChannel(row)
}

func (s *sqlStore) SaveChannel(ctx context.Context, ch Channel) (Channel, error) {
	if (ch.Status == schedule.StatusLive || ch.Status == schedule.StatusDrifted) && ch.IntentRef != "" {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return Channel{}, fmt.Errorf("save live channel %s: begin: %w", ch.ID, err)
		}
		defer func() { _ = tx.Rollback() }()
		saved, err := s.saveChannel(ctx, tx, ch)
		if isConstraintViolation(err) {
			return Channel{}, fmt.Errorf("%w: %v", ErrChannelConflict, err)
		}
		if err != nil {
			return Channel{}, err
		}
		if _, err := tx.ExecContext(ctx, s.ph(
			`UPDATE jobs SET reached_live=TRUE WHERE id=?`), ch.IntentRef); err != nil {
			return Channel{}, fmt.Errorf("save live channel %s Journey milestone: %w", ch.ID, err)
		}
		if err := tx.Commit(); err != nil {
			return Channel{}, fmt.Errorf("save live channel %s: commit: %w", ch.ID, err)
		}
		return saved, nil
	}
	saved, err := s.saveChannel(ctx, s.db, ch)
	if isConstraintViolation(err) {
		return Channel{}, fmt.Errorf("%w: %v", ErrChannelConflict, err)
	}
	return saved, err
}

type channelDB interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// saveChannel is shared by ordinary channel writes and the proposal-approval
// transaction. Keeping the serialization and SQL in one place prevents the
// atomic path from becoming a subtly different channel adapter.
func (s *sqlStore) saveChannel(ctx context.Context, exec channelDB, ch Channel) (Channel, error) {
	lineupBlob, err := json.Marshal(orEmptyEntries(ch.Lineup))
	if err != nil {
		return Channel{}, fmt.Errorf("marshal lineup: %w", err)
	}
	desiredBlob, err := json.Marshal(orEmptySlots(ch.Desired))
	if err != nil {
		return Channel{}, fmt.Errorf("marshal desired: %w", err)
	}
	// Policy is a single object (not a slice), so it serializes to "{}" when zero —
	// the DDL default — never "null"; json.Marshal of a struct already yields "{}".
	policyBlob, err := json.Marshal(ch.Policy)
	if err != nil {
		return Channel{}, fmt.Errorf("marshal policy: %w", err)
	}
	// An empty BroadcastCodec (a Channel built without setting it) persists as the
	// DDL default h264, so the round-trip never writes "" — matching what an
	// un-backfilled row reads back as.
	broadcastCodec := ch.BroadcastCodec
	if broadcastCodec == "" {
		broadcastCodec = BroadcastCodecH264
	}
	args := []any{ch.IntentRef, ch.Name, ch.Number, ch.Group, ch.Logo, string(ch.Strategy),
		ch.FillerRef, ch.TunarrID, string(ch.Status), ch.Shuffle.Seed,
		string(lineupBlob), string(desiredBlob), string(policyBlob), broadcastCodec,
		epoch(ch.PlayoutAnchor), epoch(ch.ReconcileDeadline), ch.UpdatedAt}
	invalidation := ""
	if s.dialect == DialectPostgres {
		invalidation, err = channelInvalidation(ch)
		if err != nil {
			return Channel{}, err
		}
	}
	if ch.Revision == 0 {
		query :=
			`INSERT INTO channels
			   (id, intent_ref, name, number, grp, logo, strategy, filler_ref, tunarr_id,
			    status, shuffle_seed, lineup_json, desired_json, policy_json, broadcast_codec,
			    playout_anchor, reconcile_deadline, updated_at, revision)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1)
			 RETURNING revision`
		queryArgs := append([]any{ch.ID}, args...)
		if s.dialect == DialectPostgres {
			query += `, pg_notify('` + postgresInvalidationChannel + `', ?)`
			queryArgs = append(queryArgs, invalidation)
			var notified any
			err = exec.QueryRowContext(ctx, s.ph(query), queryArgs...).Scan(&ch.Revision, &notified)
		} else {
			err = exec.QueryRowContext(ctx, s.ph(query), queryArgs...).Scan(&ch.Revision)
		}
		if err != nil {
			return Channel{}, fmt.Errorf("insert channel %s: %w", ch.ID, err)
		}
		return ch, nil
	}
	if ch.Revision < 0 {
		return Channel{}, fmt.Errorf("save channel %s: revision must not be negative", ch.ID)
	}
	expected := ch.Revision
	query :=
		`UPDATE channels SET
		   intent_ref=?, name=?, number=?, grp=?, logo=?, strategy=?, filler_ref=?,
		   tunarr_id=?, status=?, shuffle_seed=?, lineup_json=?, desired_json=?,
		   policy_json=?, broadcast_codec=?, playout_anchor=?, reconcile_deadline=?, updated_at=?,
		   revision=revision+1
		 WHERE id=? AND revision=?
		 RETURNING revision`
	queryArgs := append(args, ch.ID, expected)
	if s.dialect == DialectPostgres {
		query += `, pg_notify('` + postgresInvalidationChannel + `', ?)`
		queryArgs = append(queryArgs, invalidation)
		var notified any
		err = exec.QueryRowContext(ctx, s.ph(query), queryArgs...).Scan(&ch.Revision, &notified)
	} else {
		err = exec.QueryRowContext(ctx, s.ph(query), queryArgs...).Scan(&ch.Revision)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return Channel{}, channelRevisionMiss(ctx, exec, s.ph, ch.ID, expected)
	}
	if err != nil {
		return Channel{}, fmt.Errorf("save channel %s: %w", ch.ID, err)
	}
	return ch, nil
}

func channelRevisionMiss(ctx context.Context, q channelDB, ph placeholder, id string, expected int64) error {
	var current int64
	err := q.QueryRowContext(ctx, ph(`SELECT revision FROM channels WHERE id = ?`), id).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("read channel %s revision: %w", id, err)
	}
	return fmt.Errorf("%w: channel %s expected %d, current %d", ErrChannelStale, id, expected, current)
}

func channelMutationMiss(ctx context.Context, q channelDB, ph placeholder, id, reason string) error {
	var revision int64
	err := q.QueryRowContext(ctx, ph(`SELECT revision FROM channels WHERE id = ?`), id).Scan(&revision)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("read channel %s revision: %w", id, err)
	}
	return fmt.Errorf("%w: channel %s %s at revision %d", ErrChannelStale, id, reason, revision)
}

func (s *sqlStore) AttachTunarrChannel(
	ctx context.Context,
	id, expectedTunarrID, newTunarrID string,
	expectedNumber, newNumber int,
) (int64, error) {
	var revision int64
	err := s.db.QueryRowContext(ctx, s.ph(
		`UPDATE channels SET tunarr_id = ?, number = ?, revision = revision + 1
		 WHERE id = ? AND tunarr_id = ? AND number = ? RETURNING revision`),
		newTunarrID, newNumber, id, expectedTunarrID, expectedNumber).Scan(&revision)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, channelMutationMiss(ctx, s.db, s.ph, id, "tunarr id or number changed")
	}
	if isConstraintViolation(err) {
		return 0, fmt.Errorf("%w: attach Tunarr channel %s: %v", ErrChannelConflict, id, err)
	}
	if err != nil {
		return 0, fmt.Errorf("attach Tunarr channel %s: %w", id, err)
	}
	return revision, nil
}

// SetChannelBroadcastCodec updates ONLY the channel's broadcast_codec column (§9.1 V50) — the
// uniform codec the timeline normalizes to, computed from the library titles' codecs at curation.
//
// A targeted single-column UPDATE rather than a read-modify-write SaveChannel: the codec is
// derived state written after the lineup is bound, and the binder is the ONE lineup writer
// (loomarr-one-lineup-writer). Reusing SaveChannel here would mean re-reading the whole row and
// racing the reconciler's own writes for no benefit — this only ever touches the one derived column.
func (s *sqlStore) SetChannelBroadcastCodec(ctx context.Context, id string, expectedRevision int64, codec string) (int64, error) {
	if codec == "" {
		codec = BroadcastCodecH264
	}
	var revision int64
	err := s.db.QueryRowContext(ctx, s.ph(
		`UPDATE channels SET broadcast_codec = ?, revision = revision + 1
		 WHERE id = ? AND revision = ? RETURNING revision`), codec, id, expectedRevision).Scan(&revision)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, channelRevisionMiss(ctx, s.db, s.ph, id, expectedRevision)
	}
	if err != nil {
		return 0, fmt.Errorf("set broadcast_codec %s: %w", id, err)
	}
	return revision, nil
}

func (s *sqlStore) ListChannels(ctx context.Context) ([]Channel, error) {
	rows, err := s.db.QueryContext(ctx, s.ph(channelSelect+` ORDER BY number`))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanChannels(rows)
}

func (s *sqlStore) DeleteChannel(ctx context.Context, id string, expectedRevision int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("delete channel %s: begin: %w", id, err)
	}
	defer func() { _ = tx.Rollback() }()
	if s.dialect == DialectPostgres {
		payload, err := marshalInvalidation(Invalidation{Kind: InvalidationChannel, ChannelID: id,
			Status: schedule.StatusDetached})
		if err != nil {
			return err
		}
		var notified any
		err = tx.QueryRowContext(ctx,
			`DELETE FROM channels WHERE id = $1 AND revision = $2
			 RETURNING pg_notify('`+postgresInvalidationChannel+`', $3)`,
			id, expectedRevision, payload).Scan(&notified)
		if errors.Is(err, sql.ErrNoRows) {
			return channelRevisionMiss(ctx, tx, s.ph, id, expectedRevision)
		}
		if err != nil {
			return err
		}
	} else {
		result, err := tx.ExecContext(ctx, s.ph(`DELETE FROM channels WHERE id = ? AND revision = ?`), id, expectedRevision)
		if err != nil {
			return err
		}
		deleted, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("delete channel %s: affected rows: %w", id, err)
		}
		if deleted == 0 {
			return channelRevisionMiss(ctx, tx, s.ph, id, expectedRevision)
		}
	}
	if _, err := tx.ExecContext(ctx, s.ph(
		`DELETE FROM discovery_feedback WHERE scope = ? AND scope_id = ?`), FeedbackChannel, id); err != nil {
		return fmt.Errorf("delete channel %s feedback: %w", id, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("delete channel %s: commit: %w", id, err)
	}
	// Best-effort: drop the channel's image refs so its icon becomes collectable.
	//
	// ⚠ **This replaced `DELETE FROM channel_icons` in V52 phase 8, and the swap fixed a LEAK retired-ok
	// rather than merely renaming a cleanup.** Phase 5 moved icon bytes to the image service but
	// left this line deleting the old blob table, so a deleted channel's `image_refs` row survived
	// it — and a ref is exactly what tells the GC an image is still in use (§22). The icon was
	// therefore never orphaned and never collected: bytes on disk, referenced by a channel that no
	// longer exists, for the life of the install. `DeleteImageRefs` had no caller at all until now.
	//
	// A failure here shouldn't fail the delete (the channel row is already gone), so it is
	// logged by the caller path, not surfaced — mirror the delete's fire-and-forget shape. The
	// cost of a miss is a retained image, which the GC's orphan sweep collects on a later pass
	// once the ref does go; the cost of failing the delete would be a channel that cannot be
	// removed.
	//
	// The literal "channel" matches api.ownerKindChannel. It is spelled out rather than imported
	// because internal/store imports no domain package — the same rule every other adapter here
	// follows.
	_ = s.DeleteImageRefs(ctx, "channel", id)
	return nil
}

// ⚠ **`PutChannelIcon`/`GetChannelIcon` were DELETED in V52 phase 8, with the `channel_icons` retired-ok
// table.** Icon bytes are image-service images (§22): content-addressed on disk, served by
// /v1/images/{hash} under an honest immutable cache. `PutChannelIcon` had already had no caller retired-ok
// since phase 5 moved uploads across — it was dead code the whole time the window was open.

// ClaimDueChannels runs the dialect-specific channel-claim statement. Selects
// non-detached channels whose reconcile_deadline is at/before now, up to limit,
// and leases each by advancing its deadline to now+lease (§9/§18).
// Placeholder order (all dialects): 1=leaseUntil, 2=now, 3=limit.
func (s *sqlStore) ClaimDueChannels(ctx context.Context, now time.Time, lease time.Duration, limit int) ([]Channel, error) {
	leaseUntil := epoch(now.Add(lease))
	rows, err := s.db.QueryContext(ctx, s.channelClaimSQL, leaseUntil, epoch(now), limit)
	if err != nil {
		return nil, fmt.Errorf("claim due channels: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanChannels(rows)
}

// channelSelect is the shared column list; claim SQL RETURNs the same columns in
// this order so scanChannel serves both paths (mirrors scanTitle).
const channelSelect = `SELECT id, intent_ref, name, number, grp, logo, strategy, filler_ref,
		tunarr_id, status, shuffle_seed, lineup_json, desired_json, policy_json, broadcast_codec,
		playout_anchor, reconcile_deadline, updated_at, revision
	FROM channels`

func scanChannel(sc scannable) (Channel, error) {
	var (
		ch                                  Channel
		strategy, status                    string
		lineupBlob, desiredBlob, policyBlob string
		seed, anchor, deadline, updatedAt   int64
	)
	err := sc.Scan(&ch.ID, &ch.IntentRef, &ch.Name, &ch.Number, &ch.Group, &ch.Logo,
		&strategy, &ch.FillerRef, &ch.TunarrID, &status, &seed,
		&lineupBlob, &desiredBlob, &policyBlob, &ch.BroadcastCodec, &anchor, &deadline, &updatedAt, &ch.Revision)
	if err == sql.ErrNoRows {
		return Channel{}, ErrNotFound
	}
	if err != nil {
		return Channel{}, err
	}
	ch.Strategy = schedule.Strategy(strategy)
	ch.Status = schedule.ChannelStatus(status)
	ch.Shuffle.Seed = seed
	ch.PlayoutAnchor = fromEpoch(anchor)
	if err := json.Unmarshal([]byte(lineupBlob), &ch.Lineup); err != nil {
		return Channel{}, fmt.Errorf("unmarshal lineup %s: %w", ch.ID, err)
	}
	if err := json.Unmarshal([]byte(desiredBlob), &ch.Desired); err != nil {
		return Channel{}, fmt.Errorf("unmarshal desired %s: %w", ch.ID, err)
	}
	if err := json.Unmarshal([]byte(policyBlob), &ch.Policy); err != nil {
		return Channel{}, fmt.Errorf("unmarshal policy %s: %w", ch.ID, err)
	}
	ch.ReconcileDeadline = fromEpoch(deadline)
	ch.UpdatedAt = updatedAt
	return ch, nil
}

func scanChannels(rows *sql.Rows) ([]Channel, error) {
	var out []Channel
	for rows.Next() {
		ch, err := scanChannel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ch)
	}
	return out, rows.Err()
}

// orEmptyEntries / orEmptySlots ensure a nil slice serializes to `[]` not `null`,
// so the stored JSON round-trips to an empty (non-nil) slice.
func orEmptyEntries(e []schedule.LineupEntry) []schedule.LineupEntry {
	if e == nil {
		return []schedule.LineupEntry{}
	}
	return e
}

func orEmptySlots(s []schedule.Slot) []schedule.Slot {
	if s == nil {
		return []schedule.Slot{}
	}
	return s
}
