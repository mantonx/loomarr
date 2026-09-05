package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/filler"
	"github.com/loomarr/loomarr/internal/schedule"
	"github.com/loomarr/loomarr/internal/taxonomy"
)

// Clip is the persisted form of a filler clip (§10). It embeds the domain
// filler.Clip; the store owns the persistence concerns (UpdatedAt). Identity is
// the clip's sparse content HASH (§10 V38c) — see filler.Clip.Hash for why the
// path could not stay the key once many watched folders were allowed. The Tunarr
// program uuid rides alongside, nullable, for Tunarr-backed filler-lists.
//
// ⚠ This comment said PATH until 2026-08-10, two identity changes after the fact.
// Several methods below are still path-keyed on purpose (a scan job carries a path,
// not a hash); read each one's doc rather than assuming a single key.
type Clip struct {
	filler.Clip
	UpdatedAt time.Time
	// CreatedAt is when the clip ENTERED the catalog (§10 V51d, migration 00046) — the "recently
	// added" sort order, and the only honest answer to "what arrived while I was away?".
	//
	// ⚠ A separate column rather than a reading of UpdatedAt, because a folder re-sync bumps
	// UpdatedAt on every clip it touches: an "added" order backed by it would reshuffle the whole
	// catalog after a routine scan. Written ONCE, at insert — `UpsertClip` omits it from the
	// DO UPDATE list for the same reason it omits held/removed_at/confidence and the counters.
	//
	// ⚠ A catalog fact, so it lives HERE beside UpdatedAt rather than on filler.Clip. Nothing in
	// pod assembly, matching or playout reads it; the domain has no opinion about when a row
	// appeared, only about what the clip is.
	CreatedAt time.Time
}

// ClipFilter narrows a ListClips query. Any zero-value field is a wildcard, so a
// zero ClipFilter lists everything (the pod-assembly catalog load).
type ClipFilter struct {
	Kind            filler.Kind
	Era             int
	Audience        filler.Audience
	Category        string
	GeographicScope filler.GeographicScope
	Country         string
	Market          string
	// Taxon matches the denormalised full tag set, so selecting a parent includes descendants.
	Taxon string
	// WithoutTaxonomyTags restricts to playable clips with no asserted taxonomy rows.
	// It answers the taxonomy manager's coverage gap; unlike UntaggedOnly it is not commercial-only
	// and says nothing about era/audience completeness. Derived-only rows do not count as knowledge.
	WithoutTaxonomyTags bool
	// WithoutTaxonomyAxis restricts to playable clips with no direct assertion on one axis. Axis
	// absence is a neutral browse fact, not necessarily a defect: seasonal and audience cues are
	// intentionally sparse.
	WithoutTaxonomyAxis taxonomy.Axis
	// UntaggedOnly restricts to clips missing era/audience/category — the AI
	// tagging job's work list (§10).
	UntaggedOnly bool
	// IncludeRemoved lifts the default exclusion of tombstoned clips (V35).
	//
	// ⚠ The DEFAULT is to exclude, and it has to be: pod assembly loads the catalog through
	// this same call with a zero filter, so an opt-OUT would mean a clip the operator removed
	// keeps airing until somebody remembers to pass a flag. Opt-in is the safe polarity.
	IncludeRemoved bool
	// HeldOnly restricts to clips waiting to be filed (§10 V38) — the Incoming queue's work
	// list. The inverse of the default exclusion below, not a lifting of it.
	HeldOnly bool
	// IncludeHeld lifts the default exclusion of HELD clips (§10 V38).
	//
	// ⚠ Same polarity as IncludeRemoved and for a sharper reason. Pod assembly loads the catalog
	// through this same call with a ZERO filter, so an opt-OUT would put every untagged,
	// unreviewed clip Loomarr has ever downloaded straight into a channel's breaks — the exact
	// thing holding exists to prevent. Opt-in means a new caller is safe by default and an
	// unsafe one has to say so in writing.
	IncludeHeld bool
	// Query is a case-insensitive substring match on the clip name — the `name LIKE`
	// filter §7.2 prescribes for the clip corpus. Clip search lives here rather than in
	// /v1/search because a clip is not a provisionable title (§10), so it cannot be a
	// federated Candidate without leaking a non-title into the LLM grounding path.
	Query string
	// AutoFiledOnly restricts to clips filed into the catalog WITHOUT a human (§10 V38) — the
	// Incoming tab's audit list, "what happened while nobody was looking".
	//
	// ⚠ Opt-in like every other narrowing flag here, so the zero filter keeps meaning "the whole
	// catalog". Added because the audit list was loading every clip in the install and dropping
	// all but the auto-filed ones in Go; the predicate belongs beside the column it reads.
	AutoFiledOnly bool
	// IncludeComposites lifts the default exclusion of COMPOSITE clips (§10 V45).
	//
	// ⚠ Same polarity and same reason as IncludeHeld. A composite is a recorded break, NOT airable —
	// pod assembly loads the catalog through this same call with a ZERO filter, so an opt-OUT would
	// put a 16-minute block into a channel's break as one "commercial". Its segments air, it does
	// not. Opt-in means only a caller that explicitly wants to SEE composites (the catalog listing,
	// a lineage view) passes this.
	IncludeComposites bool
	// CompositesOnly restricts to composites — the "recorded breaks awaiting split" work list and the
	// lineage parent lookup. The inverse of the default exclusion, like HeldOnly.
	CompositesOnly bool
	// ParentHash restricts to the SEGMENTS of one composite (§10 V45) — the lineage query
	// ("show me the adverts split out of this break").
	ParentHash string
	// TopLevelOnly restricts to clips with NO parent (§10 V51d) — composites and standalone clips,
	// but not the segments split out of a break. The catalog listing's filter, so a break paginates
	// as ONE container row rather than as the twenty adverts inside it.
	//
	// ⚠ Opt-in, with a sharper argument than its siblings. For Held and IncludeComposites the
	// excluded rows are the unsafe ones; here it is the reverse — **segments are the airable
	// clips**. Pod assembly loads the catalog through the zero filter, so an opt-OUT would remove
	// every advert split out of a recorded break from every channel's breaks: not a degraded pool
	// but the exact inverse of what V45 exists to achieve, and silent.
	//
	// ⚠ Ignored when ParentHash is set — see clipWhere.
	TopLevelOnly bool
	// Hashes restricts to a specific set of clip identities (§10 V51d) — the batch read behind
	// "resolve these N pinned ids to their names".
	//
	// ⚠ It exists because paging made the alternative impossible: the channel pin/exclude editor
	// used to load the WHOLE catalog and build an id→clip map in the client, which a 100-row
	// default page silently truncates. Ask for what you hold.
	//
	// ⚠ The CALLER bounds the list. One bind parameter per hash, and Postgres caps a statement at
	// 65535 — the same ceiling attachTags carries a warning about.
	Hashes []string
	// QueryTranscript widens Query to also search the persisted transcript (§10 V51d).
	//
	// ⚠ Opt-in, and NOT because of safety this time but because of cost and noise. `transcript` is
	// the one long column — a few KB per clip, so a 500-row page scans megabytes — and the one
	// noisy one: "ford" matches "afford" with no ranking available to explain why the row came
	// back. A caller that wants to search what a clip SAYS asks for it.
	QueryTranscript bool
	// Sort selects the ORDER BY column; "" keeps the historical `path` order (see clipOrderBy).
	Sort ClipSort
	// Desc reverses the sort, tie-break included, so a descending page is the exact reverse of the
	// ascending list rather than a differently-tied approximation of it.
	Desc bool
	// Limit caps the rows returned; Offset skips that many first (§10 V51d).
	//
	// ⚠ **`Limit == 0` means NO `LIMIT` CLAUSE, and the default lives in the API — never here.**
	// This is the single most important rule in the paging change: pod assembly loads the catalog
	// through the ZERO filter, so a store-side default of 100 would quietly cut every channel's
	// break pool to the first hundred clips. No error, no log line, just a channel that plays the
	// same few adverts. The API layer applies the operator-facing default; the store obeys.
	//
	// ⚠ Offset without Limit is IGNORED, not emulated: sqlite rejects a bare OFFSET, and a page
	// with no size is not a page.
	Limit  int
	Offset int
}

// ClipSort is the catalog's sort vocabulary (§10 V51d) — a CLOSED set, mapped to a column by a
// fixed switch in clipOrderBy.
//
// ⚠ A typed string rather than a raw one so the column can never be concatenated from client
// input, and an unrecognised value is an error rather than a silent fall-back to the default. A
// silent fall-back turns "the sort control does nothing" into a bug that looks like a UI glitch.
type ClipSort string

const (
	ClipSortName       ClipSort = "name"
	ClipSortDuration   ClipSort = "duration"
	ClipSortAdded      ClipSort = "added"
	ClipSortPlays      ClipSort = "plays"
	ClipSortConfidence ClipSort = "confidence"
)

// ErrUnknownClipSort is returned by ListClips for a Sort value outside the closed set above. The
// API maps it to a 422; nothing falls back.
var ErrUnknownClipSort = errors.New("unknown clip sort")

// clipOrderBy renders the ORDER BY for a filter (§10 V51d).
//
// ⚠ **Every ordering appends `hash` as a tie-break, and that is a correctness requirement, not
// tidiness.** Without a total order, `ORDER BY duration_ms` under LIMIT/OFFSET may return the same
// row on two pages and skip another entirely — SQL promises nothing about the relative order of
// tied rows between statements, and duration/plays/confidence tie constantly.
//
// ⚠ **`name` sorts as `LOWER(name)` on BOTH dialects.** SQLite's default BINARY collation puts
// 'Z' before 'a'; Postgres's locale collation typically does not. Without the LOWER() this one
// function produces two different orders — the per-dialect fork §5's store rules forbid — and the
// conformance fixture carries case-mixed names so a regression fails on exactly one backend.
func clipOrderBy(f ClipFilter) (string, error) {
	var col string
	switch f.Sort {
	case "":
		// The historical order, preserved exactly so the zero filter (pod assembly, coverage, the
		// filler-list builder) is byte-for-byte unchanged by this phase. `hash` still rides along:
		// paths are unique in practice, and "in practice" is not a total order.
		col = "path"
	case ClipSortName:
		col = "LOWER(name)"
	case ClipSortDuration:
		col = "duration_ms"
	case ClipSortAdded:
		col = "created_at"
	case ClipSortPlays:
		col = "play_count"
	case ClipSortConfidence:
		col = "confidence"
	default:
		return "", fmt.Errorf("%w: %q", ErrUnknownClipSort, f.Sort)
	}
	dir := "ASC"
	if f.Desc {
		dir = "DESC"
	}
	// The tie-break flips WITH the sort, so a descending page is the exact reverse of the
	// ascending list. Either direction would be total; matching makes the reversal a property the
	// conformance suite can assert.
	return " ORDER BY " + col + " " + dir + ", hash " + dir, nil
}

func (s *sqlStore) UpsertClip(ctx context.Context, c Clip) error {
	_, err := s.db.ExecContext(ctx, s.ph(
		// ⚠ play_count / last_played_at are INSERTed (so a new row starts at 0) but deliberately
		// NOT in the DO UPDATE list. A re-sync knows nothing about plays, so writing
		// ⚠ `removed_at` is omitted from DO UPDATE for the same reason, and it is the thing that
		// makes "Remove from catalog" survive a re-scan. `clips` is a synced CACHE, so the next
		// pass finds the file still on disk and upserts it; if the tombstone rode along it would
		// be reset to the scan's zero and the clip would silently reappear. SetClipsRemoved is
		// its only writer, exactly like RecordClipPlay is the counters'.
		//
		// ⚠ `held` and `auto_filed` are omitted for the SAME reason, and the failure is worse
		// (V38). The folder scan re-upserts every file it finds with `held = false`; if that rode
		// along in the update list, one scan pass would file every held clip — clearing the
		// review queue and putting untagged, unreviewed clips straight into channels, with no
		// operator action and nothing in the logs. SetClipHeld is their only writer.
		//
		// `confidence` is omitted too. A scan knows nothing about tagging, and the Clip it builds
		// carries a zero score — so leaving it in the update list would blank a tagged clip's
		// confidence on the next pass, and a clip that had been trusted would start asking again
		// for no reason. `sync.go` also preserves it explicitly: belt and braces, exactly as that
		// file's own comment argues for the play counters.
		//
		// excluded.play_count would reset every counter to the scan's zero on each pass —
		// silently, and only noticeable as "usage never goes up". Tags survive re-sync because
		// sync.go merges them before calling this; the counters survive because the SQL simply
		// never touches them after insert. RecordClipPlay is their only writer.
		// ⚠ Keyed on `hash` since V38c — `path` is ordinary data now, and IS in the DO UPDATE list
		// because a clip's location can legitimately change (re-filed under a new extension, or
		// found in a different folder) while its identity does not. That is the whole point of
		// content addressing: the same bytes are the same clip wherever they sit.
		// ⚠ transcript / brand / visible_text / vision_tagged are INSERTed (a new row starts empty)
		// but deliberately OMITTED from DO UPDATE (§10 V44, migration 00038) — the same rule the
		// block above states for language/removed_at/held/counters. The folder scan knows none of
		// them; if they rode the update list, one re-sync would blank a transcribed or vision-tagged
		// clip and re-trigger ~341s of Whisper or a paid vision call over work already done. Their
		// writers are the job methods below — SetClipTranscript owns `transcript`, ApplyClipVision
		// owns `visible_text`/`vision_tagged`, and `brand` is written by whichever grounded it
		// (SetClipBrand from text, ApplyClipVision from the frame).
		//
		// ⚠ is_composite / parent_hash (§10 V45, migration 00039) are also INSERTed but OMITTED from
		// DO UPDATE. Set by intake/detection and by split Confirm; the folder scan does not know a
		// file is a composite or whose segment it is, so a re-sync must not flip a confirmed composite
		// back or blank a segment's lineage. Written by SetClipComposite / SetClipParent below.
		//
		// ⚠ `created_at` (§10 V51d, migration 00046) is the FOURTH column with this rule, and this
		// block is where people look for the list. INSERTed so a new row records when it arrived;
		// omitted from DO UPDATE because the scan supplies a fresh timestamp on every pass — riding
		// the update list would mark the entire catalog "just added" after each sync, which is the
		// exact failure the column was added to avoid. It has NO other writer: unlike its
		// neighbours here there is no Set… method, because nothing may ever change when a clip
		// arrived. See clipCreatedAt below for the insert-time value.
		//
		// ⚠ `thumb_image_hash` / `hover_image_hash` (§22 V52 phase 6, migration 00047) are the
		// FIFTH and SIXTH, and they DO have a writer — SetClipArtworkImages, after the adoption
		// job ingests the files. The scan knows nothing about image identities, so including them
		// in the update list would blank every clip's artwork on re-sync.
		`INSERT INTO clips (hash, path, tunarr_program_id, name, kind, era, audience, category, geographic_scope, country, market, network, station, air_date, geo_evidence, duration_ms, rating, source, ai_tagged, quality, license, thumbnail, preview, thumb_image_hash, hover_image_hash, language, transcript, brand, visible_text, vision_tagged, is_composite, parent_hash, play_count, last_played_at, suggested_era, removed_at, held, confidence, auto_filed, updated_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(hash) DO UPDATE SET
		   path=excluded.path,
		   tunarr_program_id=excluded.tunarr_program_id,
		   name=excluded.name, kind=excluded.kind, era=excluded.era, audience=excluded.audience,
		   category=excluded.category, duration_ms=excluded.duration_ms, rating=excluded.rating,
		   source=excluded.source, ai_tagged=excluded.ai_tagged, quality=excluded.quality,
		   license=excluded.license,
		   thumbnail=excluded.thumbnail,
		   preview=excluded.preview,
		   -- ⚠ thumb_image_hash / hover_image_hash are DELIBERATELY ABSENT from this list, and
		   -- their absence is load-bearing (§22, V52 phase 6). They are written by ONE writer —
		   -- SetClipArtworkImages, after the ingest — and the folder scan that also calls this
		   -- upsert has no idea what they are. Including them would blank a clip's artwork on
		   -- every re-sync, which is the same class as is_composite/parent_hash above.
		   language=excluded.language,
		   suggested_era=excluded.suggested_era,
		   updated_at=excluded.updated_at`),
		c.Hash, c.Path, nullIfEmpty(c.TunarrProgramID), c.Name, string(c.Kind), c.Era, string(c.Audience), c.Category,
		clipGeographicScope(c), c.Country, c.Market, c.Network, c.Station, c.AirDate, c.GeoEvidence,
		c.DurationMs, c.Rating, c.Source,
		// ⚠ Bound as real bools. `ai_tagged` used `boolToInt` until V38c, when 00033 rebuilt the
		// table and made it BOOLEAN on Postgres like `held`/`auto_filed` — so the helper that was
		// correct for it yesterday is a 42804 today. The dialect split is per COLUMN, and this
		// line is the fourth time it has bitten this session. `vision_tagged` joins them (00038).
		c.AITagged, c.Quality, c.License, c.Thumbnail, c.Preview, c.ThumbImageHash, c.HoverImageHash, c.Language,
		c.Transcript, c.Brand, c.VisibleText, c.VisionTagged,
		c.IsComposite, c.ParentHash,
		c.PlayCount, epoch(c.LastPlayedAt), c.SuggestedEra, epoch(c.RemovedAt),
		c.Held, c.Confidence, c.AutoFiled, epoch(c.UpdatedAt), epoch(clipCreatedAt(c)))
	if err != nil {
		return fmt.Errorf("upsert clip %s: %w", c.Hash, err)
	}
	return nil
}

// ReplaceClipIdentity re-keys a clip after Loomarr transforms its bytes (§10).
//
// The hash is content identity, not an intake-time label. Updating only clips.path would leave
// taxonomy, pipeline state, split lineage, artwork ownership and channel overrides attached to
// the bytes that no longer exist. Every database reference therefore moves in one transaction;
// the caller publishes the verified content-addressed file before entering this method and keeps
// the original file until it commits.
func (s *sqlStore) ReplaceClipIdentity(ctx context.Context, oldHash string, c Clip) error {
	if oldHash == "" || c.Hash == "" || c.Path == "" {
		return fmt.Errorf("replace clip identity: old hash, new hash and path are required")
	}
	// A transform can be byte-for-byte stable. In that case there is no identity graph to
	// re-key: running the reference statements below with the same value needlessly deletes the
	// still-valid fingerprint and can collide with unique sibling keys on existing databases.
	// Update only the transformed media facts. Keep the Tunarr registration when even the path is
	// unchanged; otherwise the next catalog sync must register the new location.
	if oldHash == c.Hash {
		res, err := s.db.ExecContext(ctx, s.ph(`UPDATE clips
			SET path = ?,
			    tunarr_program_id = CASE WHEN path = ? THEN tunarr_program_id ELSE NULL END,
			    duration_ms = ?, quality = ?, updated_at = ?
			WHERE hash = ?`), c.Path, c.Path, c.DurationMs, c.Quality, epoch(c.UpdatedAt), oldHash)
		if err != nil {
			return fmt.Errorf("replace clip identity %s: %w", oldHash, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("replace clip identity %s: %w", oldHash, err)
		}
		if n == 0 {
			return ErrNotFound
		}
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("replace clip identity %s: %w", oldHash, err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := s.replaceClipIdentityTx(ctx, tx, oldHash, c); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("replace clip identity %s: %w", oldHash, err)
	}
	return nil
}

func (s *sqlStore) replaceClipIdentityTx(ctx context.Context, tx *sql.Tx, oldHash string, c Clip) error {
	res, err := tx.ExecContext(ctx, s.ph(`UPDATE clips
		SET hash = ?, path = ?, tunarr_program_id = NULL, duration_ms = ?, quality = ?, updated_at = ?
		WHERE hash = ?`), c.Hash, c.Path, c.DurationMs, c.Quality, epoch(c.UpdatedAt), oldHash)
	if err != nil {
		return fmt.Errorf("replace clip identity %s: %w", oldHash, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("replace clip identity %s: %w", oldHash, err)
	}
	if n == 0 {
		return ErrNotFound
	}

	return s.rekeyClipReferencesTx(ctx, tx, oldHash, c.Hash)
}

func (s *sqlStore) rekeyClipReferencesTx(ctx context.Context, tx *sql.Tx, oldHash, newHash string) error {
	refs := []struct {
		query string
		args  []any
	}{
		// Fingerprints describe the OLD bytes. Never re-key derived evidence onto the new content
		// identity; the first de-dup pass that sees the replacement computes it again.
		{`DELETE FROM filler_clip_fingerprints WHERE clip_hash = ?`, []any{oldHash}},
		{`UPDATE clip_tags SET clip_hash = ? WHERE clip_hash = ?`, []any{newHash, oldHash}},
		{`UPDATE filler_clip_pipeline SET clip_hash = ? WHERE clip_hash = ?`, []any{newHash, oldHash}},
		{`UPDATE filler_split_proposals SET clip_hash = ? WHERE clip_hash = ?`, []any{newHash, oldHash}},
		{`UPDATE clips SET parent_hash = ? WHERE parent_hash = ?`, []any{newHash, oldHash}},
		{`UPDATE image_refs SET owner_id = ? WHERE owner_kind = ? AND owner_id = ?`, []any{newHash, "clip", oldHash}},
	}
	for _, ref := range refs {
		if _, err := tx.ExecContext(ctx, s.ph(ref.query), ref.args...); err != nil {
			return fmt.Errorf("replace clip identity %s references: %w", oldHash, err)
		}
	}
	if err := s.rekeyChannelClipRefs(ctx, tx, oldHash, newHash); err != nil {
		return fmt.Errorf("replace clip identity %s channel references: %w", oldHash, err)
	}
	return nil
}

// CommitConditioningPublication closes the catalog half of the owner-bound filesystem saga.
// The pending sidecar has already proved the exact source/target byte pair. This transaction
// permits only the three catalog shapes that proof can own: an ordinary source-only re-key, a
// source plus the held row Sync reconstructed from that sidecar, or the exact target-only state
// left by a re-key that committed before process loss.
func (s *sqlStore) CommitConditioningPublication(ctx context.Context, publication filler.ConditioningPublication, target Clip) error {
	if publication.State != "pending" || publication.Owner == "" || publication.SourceHash == "" ||
		publication.TargetHash == "" || publication.SourceHash == publication.TargetHash ||
		publication.TargetHash != target.Hash || target.Path == "" {
		return ErrConditioningPublicationMismatch
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("commit conditioning publication %s: %w", publication.TargetHash, err)
	}
	defer func() { _ = tx.Rollback() }()

	type catalogState struct {
		found       bool
		path        string
		parentHash  string
		held        bool
		removedAt   int64
		isComposite bool
	}
	states := map[string]*catalogState{
		publication.SourceHash: {},
		publication.TargetHash: {},
	}
	query := `SELECT hash, path, parent_hash, held, removed_at, is_composite
		FROM clips WHERE hash IN (?, ?)`
	if s.dialect == DialectPostgres {
		query += ` FOR UPDATE`
	}
	rows, err := tx.QueryContext(ctx, s.ph(query), publication.SourceHash, publication.TargetHash)
	if err != nil {
		return fmt.Errorf("commit conditioning publication %s classify: %w", publication.TargetHash, err)
	}
	for rows.Next() {
		var hash string
		var state catalogState
		if err := rows.Scan(&hash, &state.path, &state.parentHash, &state.held, &state.removedAt, &state.isComposite); err != nil {
			_ = rows.Close()
			return fmt.Errorf("commit conditioning publication %s classify: %w", publication.TargetHash, err)
		}
		state.found = true
		states[hash] = &state
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("commit conditioning publication %s classify: %w", publication.TargetHash, err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("commit conditioning publication %s classify: %w", publication.TargetHash, err)
	}
	sourceState := states[publication.SourceHash]
	targetState := states[publication.TargetHash]
	targetRemovedAt := epoch(target.RemovedAt)

	// The durable re-key already won. Exact filesystem evidence plus source absence and an exact
	// target row make clearing the still-owned marker idempotent; no catalog write is needed.
	if !sourceState.found {
		if targetState.found && targetState.path == target.Path && targetState.parentHash == target.ParentHash &&
			targetState.held == target.Held && targetState.removedAt == targetRemovedAt &&
			targetState.isComposite == target.IsComposite {
			return nil
		}
		return ErrConditioningPublicationMismatch
	}

	// The source row must still describe the owner whose metadata the staged target carries.
	if sourceState.parentHash != target.ParentHash || sourceState.held != target.Held ||
		sourceState.removedAt != targetRemovedAt || sourceState.isComposite != target.IsComposite {
		return ErrConditioningPublicationMismatch
	}

	if !targetState.found {
		if err := s.replaceClipIdentityTx(ctx, tx, publication.SourceHash, target); err != nil {
			return err
		}
	} else {
		// Sync may reconstruct only this exact quarantine row. Keep that row in place and adopt it;
		// there is never a delete-then-reinsert window for the content-addressed target identity.
		if targetState.path != target.Path || targetState.parentHash != target.ParentHash ||
			!targetState.held || targetState.removedAt != 0 || targetState.isComposite {
			return ErrConditioningPublicationMismatch
		}
		res, err := tx.ExecContext(ctx, s.ph(`UPDATE clips SET
			(name, kind, era, audience, category, rating, source, ai_tagged, license,
			 thumbnail, preview, thumb_image_hash, hover_image_hash, language, transcript,
			 brand, visible_text, vision_tagged, is_composite, parent_hash, play_count,
			 last_played_at, suggested_era, removed_at, held, confidence, auto_filed,
			 created_at, reaped_at) =
			(SELECT name, kind, era, audience, category, rating, source, ai_tagged, license,
			 thumbnail, preview, thumb_image_hash, hover_image_hash, language, transcript,
			 brand, visible_text, vision_tagged, is_composite, parent_hash, play_count,
			 last_played_at, suggested_era, removed_at, held, confidence, auto_filed,
			 created_at, reaped_at FROM clips WHERE hash = ?),
			path = ?, tunarr_program_id = NULL, duration_ms = ?, quality = ?, updated_at = ?
			WHERE hash = ? AND path = ? AND parent_hash = ? AND held = ? AND removed_at = ? AND is_composite = ?`),
			publication.SourceHash, target.Path, target.DurationMs, target.Quality, epoch(target.UpdatedAt),
			publication.TargetHash, target.Path, target.ParentHash, true, int64(0), false)
		if err != nil {
			return fmt.Errorf("commit conditioning publication %s adopt target: %w", publication.TargetHash, err)
		}
		if n, countErr := res.RowsAffected(); countErr != nil || n != 1 {
			if countErr != nil {
				return fmt.Errorf("commit conditioning publication %s count target: %w", publication.TargetHash, countErr)
			}
			return ErrConditioningPublicationMismatch
		}
		if err := s.rekeyClipReferencesTx(ctx, tx, publication.SourceHash, publication.TargetHash); err != nil {
			return err
		}
		res, err = tx.ExecContext(ctx, s.ph(`DELETE FROM clips WHERE hash = ?`), publication.SourceHash)
		if err != nil {
			return fmt.Errorf("commit conditioning publication %s retire source: %w", publication.TargetHash, err)
		}
		if n, countErr := res.RowsAffected(); countErr != nil || n != 1 {
			if countErr != nil {
				return fmt.Errorf("commit conditioning publication %s count source: %w", publication.TargetHash, countErr)
			}
			return ErrConditioningPublicationMismatch
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit conditioning publication %s: %w", publication.TargetHash, err)
	}
	return nil
}

func (s *sqlStore) rekeyChannelClipRefs(ctx context.Context, tx *sql.Tx, oldHash, newHash string) error {
	query := `SELECT id, policy_json FROM channels`
	if s.dialect == DialectPostgres {
		// Lock the policy snapshots before decoding them. Without this, a concurrent
		// channel CAS could commit between SELECT and UPDATE and this maintenance
		// transaction would overwrite its policy with the pre-edit JSON.
		query += ` FOR UPDATE`
	}
	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		return err
	}
	type changedPolicy struct {
		id   string
		blob []byte
	}
	var changed []changedPolicy
	for rows.Next() {
		var id, raw string
		if err := rows.Scan(&id, &raw); err != nil {
			_ = rows.Close()
			return err
		}
		var policy schedule.ChannelPolicy
		if err := json.Unmarshal([]byte(raw), &policy); err != nil {
			_ = rows.Close()
			return fmt.Errorf("channel %s policy: %w", id, err)
		}
		if policy.Filler == nil {
			continue
		}
		touched := replaceClipRef(policy.Filler.Pinned, oldHash, newHash)
		touched = replaceClipRef(policy.Filler.Excluded, oldHash, newHash) || touched
		if !touched {
			continue
		}
		blob, err := json.Marshal(policy)
		if err != nil {
			_ = rows.Close()
			return fmt.Errorf("channel %s policy: %w", id, err)
		}
		changed = append(changed, changedPolicy{id: id, blob: blob})
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, item := range changed {
		if _, err := tx.ExecContext(ctx, s.ph(
			`UPDATE channels SET policy_json = ?, revision = revision + 1 WHERE id = ?`),
			string(item.blob), item.id); err != nil {
			return err
		}
	}
	return nil
}

func replaceClipRef(refs []string, oldHash, newHash string) bool {
	changed := false
	for i := range refs {
		if refs[i] == oldHash {
			refs[i] = newHash
			changed = true
		}
	}
	return changed
}

// clipCreatedAt is the value `created_at` takes at INSERT (§10 V51d).
//
// ⚠ It falls back to UpdatedAt when the caller left CreatedAt zero, and the fallback is the point:
// every existing writer — the folder scan, intake, split confirm — builds a Clip with a fresh
// UpdatedAt and knows nothing about this column. Requiring each of them to set it would mean five
// call sites that must remember, and the one that forgets writes a 0 that sorts to the far end of
// "recently added" forever. The store answers the question it can answer correctly.
//
// ⚠ It is NOT a general default. Only the INSERT reads this; a re-sync never reaches it, because
// `created_at` is absent from the DO UPDATE list.
func clipCreatedAt(c Clip) time.Time {
	if !c.CreatedAt.IsZero() {
		return c.CreatedAt
	}
	return c.UpdatedAt
}

func clipGeographicScope(c Clip) string {
	if c.GeographicScope == "" {
		return string(filler.GeographicUnknown)
	}
	return string(c.GeographicScope)
}

const clipSelect = `SELECT hash, path, tunarr_program_id, name, kind, era, audience, category, duration_ms,
	geographic_scope, country, market, network, station, air_date, geo_evidence,
	rating, source, ai_tagged, quality, license, thumbnail, preview, thumb_image_hash, hover_image_hash, language, transcript, brand, visible_text, vision_tagged,
	is_composite, parent_hash,
	play_count, last_played_at, suggested_era,
	removed_at, held, confidence, auto_filed, updated_at, created_at FROM clips`

func (s *sqlStore) GetClip(ctx context.Context, id string) (Clip, error) {
	c, err := scanClip(s.db.QueryRowContext(ctx, s.ph(clipSelect+` WHERE hash = ?`), id))
	if err != nil {
		return Clip{}, err
	}
	return s.clipWithTags(ctx, c)
}

// clipWithTags fills a single clip's taxonomy Tags (§10 V45a). Wraps attachTags for the two
// single-row readers so they load tags the same way the listing does — one clip is a one-element
// batch, so there is no separate query shape to keep in step.
func (s *sqlStore) clipWithTags(ctx context.Context, c Clip) (Clip, error) {
	batch := []Clip{c}
	if err := s.attachTags(ctx, batch); err != nil {
		return Clip{}, err
	}
	return batch[0], nil
}

// GetClipByPath looks a clip up by its location under FILLER_DIR.
//
// ⚠ **Needed because a clip's PATH and its IDENTITY stopped being the same string in V38c.**
// Identity is now the content hash (`14365f2b…`), while the path is the sharded location
// (`14/36/14365f2b….mp4`). `GetClip` queries `WHERE hash = ?`, so handing it a path matches
// nothing — silently, as an ordinary not-found.
//
// That is not hypothetical: `/v1/filler/media` did exactly this from V38c until V39 and 404'd for
// every clip in the catalog. Nothing noticed because no client called it until the player shipped.
//
// The media route needs this one rather than the hash lookup because its URL carries the path —
// which is what `ClipDTO` exposes, and what every other clip-shaped route already keys on.
func (s *sqlStore) GetClipByPath(ctx context.Context, path string) (Clip, error) {
	c, err := scanClip(s.db.QueryRowContext(ctx, s.ph(clipSelect+` WHERE path = ?`), path))
	if err != nil {
		return Clip{}, err
	}
	return s.clipWithTags(ctx, c)
}

// ListClips applies the filter as ANDed WHERE clauses (zero fields omitted). The
// UntaggedOnly flag adds "era=0 OR audience=” OR category=”" (any missing tag).
//
// Ordering comes from clipOrderBy (always total — see its tie-break warning), and `Limit`/`Offset`
// page the result. ⚠ An unknown `Sort` is an ERROR (ErrUnknownClipSort), never a fall-back.
func (s *sqlStore) ListClips(ctx context.Context, f ClipFilter) ([]Clip, error) {
	where, args := clipWhere(f)
	q := clipSelect
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	order, err := clipOrderBy(f)
	if err != nil {
		return nil, err
	}
	q += order
	// ⚠ `Limit == 0` renders NO clause at all — see the field's own warning. Pod assembly reaches
	// here with the zero filter and must get the whole catalog.
	if f.Limit > 0 {
		q += " LIMIT ?"
		args = append(args, f.Limit)
		if f.Offset > 0 {
			q += " OFFSET ?"
			args = append(args, f.Offset)
		}
	}

	rows, err := s.db.QueryContext(ctx, s.ph(q), args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	clips, err := scanClips(rows)
	if err != nil {
		return nil, err
	}
	if err := s.attachTags(ctx, clips); err != nil {
		return nil, err
	}
	return clips, nil
}

// CountClips answers "how many match?" without materialising the rows.
//
// ⚠ Shares `clipWhere` with ListClips rather than restating the predicate. Several callers wanted
// only a number and were loading every column of every row to take `len()` of the slice — on a
// large catalog that is the dominant cost of rendering a settings page. A second hand-written
// WHERE here would be free to drift from the one the listing (and the tagging job) actually use,
// which is the failure this deliberately forecloses.
func (s *sqlStore) CountClips(ctx context.Context, f ClipFilter) (int, error) {
	where, args := clipWhere(f)
	q := `SELECT COUNT(*) FROM clips`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	var n int
	if err := s.db.QueryRowContext(ctx, s.ph(q), args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("count clips: %w", err)
	}
	return n, nil
}

// CountClipsBySource returns the clip count per source — the Sources page's whole question.
//
// ⚠ Grouped in SQL. This was every row of the catalog loaded into Go to build a map of counters;
// the answer is one small map either way, so the load was pure waste. Honours the same default
// exclusions as the listing (removed and held clips are out unless asked for) so the per-source
// numbers add up to the catalog total a caller sees elsewhere.
func (s *sqlStore) CountClipsBySource(ctx context.Context, f ClipFilter) (map[string]int, error) {
	where, args := clipWhere(f)
	q := `SELECT source, COUNT(*) FROM clips`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " GROUP BY source"

	rows, err := s.db.QueryContext(ctx, s.ph(q), args...)
	if err != nil {
		return nil, fmt.Errorf("count clips by source: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]int{}
	for rows.Next() {
		var src string
		var n int
		if err := rows.Scan(&src, &n); err != nil {
			return nil, err
		}
		out[src] = n
	}
	return out, rows.Err()
}

// clipWhere builds the ANDed predicate for a ClipFilter, shared by every query that has to mean
// the same thing by "matching clips" — the listing, the counts, and the per-source rollup.
func clipWhere(f ClipFilter) ([]string, []any) {
	var where []string
	var args []any
	if f.Kind != "" {
		where = append(where, "kind = ?")
		args = append(args, string(f.Kind))
	}
	if f.Era > 0 {
		where = append(where, "era = ?")
		args = append(args, f.Era)
	}
	if f.Audience != "" {
		where = append(where, "audience = ?")
		args = append(args, string(f.Audience))
	}
	if f.Category != "" {
		where = append(where, "category = ?")
		args = append(args, f.Category)
	}
	if f.GeographicScope != "" {
		where = append(where, "geographic_scope = ?")
		args = append(args, string(f.GeographicScope))
	}
	if f.Country != "" {
		where = append(where, "UPPER(country) = UPPER(?)")
		args = append(args, strings.TrimSpace(f.Country))
	}
	if f.Market != "" {
		where = append(where, "LOWER(market) = LOWER(?)")
		args = append(args, strings.Join(strings.Fields(f.Market), " "))
	}
	if f.Taxon != "" {
		where = append(where, "EXISTS (SELECT 1 FROM clip_tags tx WHERE tx.clip_hash = clips.hash AND tx.taxon = ?)")
		args = append(args, f.Taxon)
	}
	if f.WithoutTaxonomyTags {
		where = append(where, "NOT EXISTS (SELECT 1 FROM clip_tags tx WHERE tx.clip_hash = clips.hash AND tx.leaf = ?)")
		args = append(args, true)
	}
	if f.WithoutTaxonomyAxis != "" {
		where = append(where, `NOT EXISTS (SELECT 1 FROM clip_tags tx
			JOIN taxa tt ON tt.slug = tx.taxon
			WHERE tx.clip_hash = clips.hash AND tx.leaf = ? AND tt.axis = ?)`)
		args = append(args, true, string(f.WithoutTaxonomyAxis))
	}
	if f.Query != "" {
		// LOWER() on both sides so the match is case-insensitive on BOTH dialects:
		// SQLite's LIKE folds case for ASCII by default while Postgres's does not, and
		// a search that behaves differently per backend is exactly the dialect fork the
		// store rules forbid. The term is escaped for LIKE metacharacters so a user
		// typing "%" searches for a percent sign rather than matching everything.
		//
		// ⚠ Widened past `name` in V51d — a catalog search that could not find "Kellogg's" on a
		// clip whose brand IS Kellogg's was a search box that looked broken. The columns come from
		// a FIXED list below, never from input; only the bound term is user data.
		//
		// ⚠ No FTS, deliberately: SQLite FTS5 and Postgres tsvector are different engines with
		// different tokenizers and ranking, so adopting them forces this function to branch on
		// dialect and the conformance suite to assert equivalent-but-not-identical results per
		// backend. One suite, two backends (§5) rules that out. LIKE over four columns is slower
		// in theory and indistinguishable at household scale.
		like := "%" + escapeLike(f.Query) + "%"
		cols := []string{"name", "brand", "visible_text"}
		if f.QueryTranscript {
			cols = append(cols, "transcript")
		}
		ors := make([]string, 0, len(cols)+1)
		for _, col := range cols {
			ors = append(ors, "LOWER("+col+") LIKE LOWER(?) ESCAPE '\\'")
			args = append(args, like)
		}
		// Tags match through an EXISTS rather than a JOIN: a clip with three matching tags must be
		// ONE row, and a join would return it three times — which CountClips would then count
		// three times, so the total and the rows would disagree. `clip_tags` is indexed both ways.
		ors = append(ors, "EXISTS (SELECT 1 FROM clip_tags ct WHERE ct.clip_hash = clips.hash AND LOWER(ct.taxon) LIKE LOWER(?) ESCAPE '\\')")
		args = append(args, like)
		where = append(where, "("+strings.Join(ors, " OR ")+")")
	}
	if len(f.Hashes) > 0 {
		ph := make([]string, len(f.Hashes))
		for i, h := range f.Hashes {
			ph[i] = "?"
			args = append(args, h)
		}
		where = append(where, "hash IN ("+strings.Join(ph, ",")+")")
	}
	if !f.IncludeRemoved {
		where = append(where, "removed_at = 0")
	}
	// ⚠ THE chokepoint for the clip lifecycle (§10 V38). Held clips are excluded here, once,
	// rather than at each of the many callers — pod assembly, coverage, the filler-list builder,
	// the catalog listing, search. Filtering per call site is how one of them gets forgotten and
	// an unreviewed clip airs.
	//
	// ⚠ Compared as a BOUND PARAMETER, never a literal. `held` is INTEGER on sqlite and BOOLEAN
	// on Postgres (migration 00031), so `held = 0` is a type error on one of them — the same
	// dialect trap V37's 00029 hit with a literal `1`. Binding lets the driver render each.
	if f.HeldOnly {
		where = append(where, "held = ?")
		args = append(args, true)
	} else if !f.IncludeHeld {
		where = append(where, "held = ?")
		args = append(args, false)
	}
	// ⚠ THE composite chokepoint (§10 V45), the same shape as the held block above and for the same
	// reason: pod assembly loads the catalog here with a zero filter, so a composite excluded ONCE
	// here can never air. Bound, never a literal — `is_composite` is BOOLEAN on Postgres and INTEGER
	// on sqlite (00039), the recurring dialect trap.
	if f.CompositesOnly {
		where = append(where, "is_composite = ?")
		args = append(args, true)
	} else if !f.IncludeComposites {
		where = append(where, "is_composite = ?")
		args = append(args, false)
	}
	// ⚠ ParentHash WINS over TopLevelOnly rather than both being ANDed. They are contradictory —
	// "the segments of break X" and "clips with no parent" share no row — so ANDing them returns
	// the empty set, which renders as "this break has no segments" on a break that has twenty.
	// A lineage query is by construction not a top-level query; saying so here beats letting a
	// caller that sets both get a confident, wrong, empty answer.
	if f.ParentHash != "" {
		where = append(where, "parent_hash = ?")
		args = append(args, f.ParentHash)
	} else if f.TopLevelOnly {
		// Bound, not the literal '', for the same reason every other comparison here is bound.
		where = append(where, "parent_hash = ?")
		args = append(args, "")
	}
	if f.UntaggedOnly {
		// "Untagged" = a COMMERCIAL missing any match tag. Bumpers/station-ids/PSAs
		// serve their bookend role without era/audience/category, so the AI-tagging
		// job (§10) shouldn't spend an LLM call on them — only commercials need full
		// tags for pod matching.
		where = append(where, "kind = 'commercial' AND (era = 0 OR audience = '' OR category = '')")
	}
	if f.AutoFiledOnly {
		// ⚠ Bound, not a literal: `auto_filed` is INTEGER on sqlite and BOOLEAN on Postgres,
		// the same dialect trap the `held` comparison above documents.
		where = append(where, "auto_filed = ?")
		args = append(args, true)
	}
	return where, args
}

// ListUntaggedCommercials is the AI-tagging job's work list (§10) — sugar over the
// commercial-scoped UntaggedOnly filter.
//
// ⚠ **IncludeHeld is required, not optional** (§10 V38). Held clips are exactly the ones most in
// need of tagging — a held clip is waiting to be tagged and then filed — and the default catalog
// filter excludes them. Without this the tagger would silently skip every clip Loomarr had just
// downloaded, tag only what was already filed, and nothing would ever leave the review queue by
// itself. The auto-file threshold would appear to do nothing.
func (s *sqlStore) ListUntaggedCommercials(ctx context.Context) ([]Clip, error) {
	return s.ListClips(ctx, ClipFilter{UntaggedOnly: true, IncludeHeld: true})
}

func (s *sqlStore) UpdateClipClassification(ctx context.Context, id string, era int, audience string, suggestedEra int, aiTagged bool, updatedAt time.Time) error {
	res, err := s.db.ExecContext(ctx, s.ph(
		// ⚠ suggested_era is CONDITIONAL (§10 era grounding, V34): writing an era confirms
		// the suggestion, so it clears in the same write; a NEW suggestion overwrites; and a
		// write carrying NEITHER (an era-less tag edit) leaves the existing suggestion alone —
		// the tag job re-classifies untagged clips every run, so wiping on a no-era result
		// would make the suggestion flap run to run.
		`UPDATE clips SET era = ?, audience = ?,
		   suggested_era = CASE WHEN ? > 0 THEN 0 WHEN ? > 0 THEN ? ELSE suggested_era END,
		   ai_tagged = ?, updated_at = ? WHERE hash = ?`),
		// ⚠ A real bool, not boolToInt — `ai_tagged` became BOOLEAN on Postgres when 00033 rebuilt
		// the table (V38c). This writer and the upsert must agree, and they did not for a moment.
		era, audience, era, suggestedEra, suggestedEra, aiTagged, epoch(updatedAt), id)
	if err != nil {
		return fmt.Errorf("update clip classification %s: %w", id, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// RecordClipPlay increments a clip's play counter and stamps when it last aired (V28).
//
// ⚠ Called from PLAYOUT — when a filler item actually starts encoding — never from pod
// assembly. Assembly re-runs on every reconcile sweep and would count SCHEDULED rather than
// AIRED, inflating without bound (see migration 00017).
//
// `at` is the scheduled clip start, not the wall clock when the resolver happened to arrive.
// A finite encoder child normally requests its successor milliseconds after that boundary, and
// a viewer reconnect or retry may resolve it again. The per-channel upsert accepts only a NEWER
// scheduled start, so all callbacks for one airing are one durable write without process-local
// memory; only that accepted write increments the global catalog counter.
//
// A missing clip is NOT an error. Playout resolves a path that the catalog may have pruned
// between the schedule being built and the break airing; failing here would turn a stale
// catalog row into a playback error, and the counter is telemetry, not correctness. The row
// count is deliberately ignored for the same reason.
//
// updated_at is left alone: this is not a catalog edit, and touching it would make every clip
// look freshly re-synced in the UI's "last updated" column.
func (s *sqlStore) RecordClipPlay(ctx context.Context, channelID, id string, at time.Time) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("record clip play %s: %w", id, err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, s.ph(`INSERT INTO filler_exposures
		(channel_id, clip_hash, play_count, last_played_at, previous_played_at) VALUES (?, ?, 1, ?, 0)
		ON CONFLICT(channel_id, clip_hash) DO UPDATE SET
			play_count = filler_exposures.play_count + 1,
			previous_played_at = CASE
				WHEN excluded.last_played_at > filler_exposures.last_played_at
				THEN filler_exposures.last_played_at ELSE filler_exposures.previous_played_at END,
			last_played_at = CASE
				WHEN excluded.last_played_at > filler_exposures.last_played_at
				THEN excluded.last_played_at ELSE filler_exposures.last_played_at END
		WHERE excluded.last_played_at > filler_exposures.last_played_at`),
		channelID, id, at.UnixMilli())
	if err != nil {
		return false, fmt.Errorf("record channel clip play %s/%s: %w", channelID, id, err)
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read channel clip play result %s/%s: %w", channelID, id, err)
	}
	if changed == 0 {
		if err := tx.Commit(); err != nil {
			return false, fmt.Errorf("commit duplicate clip play %s: %w", id, err)
		}
		return false, nil
	}
	if _, err := tx.ExecContext(ctx, s.ph(
		`UPDATE clips SET play_count = play_count + 1,
		 last_played_at = CASE WHEN ? > last_played_at THEN ? ELSE last_played_at END
		 WHERE hash = ?`), epoch(at), epoch(at), id); err != nil {
		return false, fmt.Errorf("record clip play %s: %w", id, err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("record clip play %s: %w", id, err)
	}
	return true, nil
}

// FillerExposuresByChannel returns one channel's durable aggregate rotation snapshot (§10 V58).
func (s *sqlStore) FillerExposuresByChannel(ctx context.Context, channelID string, before time.Time) (map[string]filler.Exposure, error) {
	rows, err := s.db.QueryContext(ctx, s.ph(`SELECT clip_hash, play_count, last_played_at,
		previous_played_at FROM filler_exposures WHERE channel_id = ?`), channelID)
	if err != nil {
		return nil, fmt.Errorf("list filler exposures for channel %s: %w", channelID, err)
	}
	defer func() { _ = rows.Close() }()

	out := map[string]filler.Exposure{}
	for rows.Next() {
		var hash string
		var count, lastMs, previousMs int64
		if err := rows.Scan(&hash, &count, &lastMs, &previousMs); err != nil {
			return nil, fmt.Errorf("scan filler exposure for channel %s: %w", channelID, err)
		}
		// The aggregate retains one previous timestamp solely so a rebuild after a clip starts
		// can reconstruct the immutable snapshot for that active break. No-repeat means a clip
		// updates at most once inside one pod, so one predecessor is exactly the bounded state needed.
		if !before.IsZero() && lastMs >= before.UnixMilli() {
			count--
			lastMs = previousMs
		}
		if count <= 0 || lastMs <= 0 {
			continue
		}
		out[hash] = filler.Exposure{PlayCount: count, LastPlayedAt: time.UnixMilli(lastMs).UTC()}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list filler exposures for channel %s: %w", channelID, err)
	}
	return out, nil
}

// UpdateClipKind corrects a clip's kind (§10). Kind drives pod ROLE — a bumper bookends
// a pod while a commercial fills it — so a mis-detected kind produces structurally wrong
// pods, not merely a mis-tagged clip.
func (s *sqlStore) UpdateClipKind(ctx context.Context, id, kind string, updatedAt time.Time) error {
	res, err := s.db.ExecContext(ctx, s.ph(
		`UPDATE clips SET kind = ?, updated_at = ? WHERE hash = ?`),
		kind, epoch(updatedAt), id)
	if err != nil {
		return fmt.Errorf("update clip kind %s: %w", id, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *sqlStore) UpdateClipGeography(ctx context.Context, hash, scope, country, market, network, station, airDate, evidence string, updatedAt time.Time) error {
	res, err := s.db.ExecContext(ctx, s.ph(`UPDATE clips SET geographic_scope = ?, country = ?, market = ?,
		network = ?, station = ?, air_date = ?, geo_evidence = ?, updated_at = ? WHERE hash = ?`),
		scope, country, market, network, station, airDate, evidence, epoch(updatedAt), hash)
	if err != nil {
		return fmt.Errorf("update clip geography %s: %w", hash, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteClipsNotIn prunes clips absent from the given id set (the sync reconcile).
// With an empty keep set it deletes all clips. Returns the count removed.
func (s *sqlStore) DeleteClipsNotIn(ctx context.Context, keepIDs []string) (int, error) {
	// ⚠ **The pipeline rows go with the clips, in the same call** (§10 V51b). `filler_clip_pipeline`
	// is a sibling table with no foreign key — deliberately, so it survives a `clips` rebuild — and
	// the price of that independence is that nothing else will ever clean it up. An orphan row is
	// not inert either: `ListPipelineWork` would keep returning it, `advance` would fail to find
	// the clip, and it would be re-tombstoned as "no longer in the catalog" on every pass, forever.
	//
	// Pruning by "no matching clip" rather than by the keep-set means one statement covers both
	// branches below and cannot disagree with whichever DELETE ran.
	defer func() {
		_ = s.pruneOrphanPipelines(ctx)
		_ = s.pruneOrphanClipFingerprints(ctx)
	}()

	// ⚠ **And the proposals, for the same reason** (§10 V54). `filler_split_proposals` is the other
	// no-foreign-key sibling of `clips`, and it had the same hole: a wipe left 48 proposals behind,
	// which Incoming rendered as 48 "compilations to review" titled with raw content hashes, each
	// opening a review of a deleted file. Two separate defers rather than one combined closure, so
	// each table's rationale sits beside its own call; the tables are independent, so LIFO order
	// between them does not matter.
	defer func() { _ = s.pruneOrphanSplitProposals(ctx) }()

	if len(keepIDs) == 0 {
		// ⚠ Reaped composites survive an EMPTY scan too. This branch fires when the drop folder is
		// empty or unreadable, which is exactly when a swept reel looks most like a deleted one —
		// and taking the tombstones here would dangle their children just as surely.
		res, err := s.db.ExecContext(ctx, `DELETE FROM clips WHERE reaped_at IS NULL`)
		if err != nil {
			return 0, err
		}
		n, _ := res.RowsAffected()
		return int(n), nil
	}
	placeholders := make([]string, len(keepIDs))
	args := make([]any, len(keepIDs))
	for i, id := range keepIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	// ⚠ Keyed on `hash`, the clip's IDENTITY since V38c — not on `path`.
	//
	// This said `path NOT IN (…)` and was missed when identity moved off the path. The sync's
	// `keep` set carries hashes, and a hash never equals a path, so EVERY clip matched
	// "not in the keep set" and the whole catalog was deleted on every single sync. The route
	// reported "1 added, 1 pruned" forever and the catalog stayed empty — filler silently never
	// worked. Found by running the real binary; the conformance suite passed throughout because
	// its fixtures set `path` and `hash` to the same string.
	// ⚠ **A REAPED composite is exempt** (§10 V54). The split sweep deletes a spent compilation's
	// recording on purpose, so its absence from the scan is expected rather than news. Without this
	// the very next sync would prune the row — and every clip cut out of that reel carries
	// `parent_hash` pointing at it, so one sweep would dangle all 47 children and take V45's
	// lineage with it. The row is a tombstone; only the bytes are gone.
	q := s.ph(`DELETE FROM clips WHERE reaped_at IS NULL AND hash NOT IN (` + strings.Join(placeholders, ",") + `)`)
	res, err := s.db.ExecContext(ctx, q, args...)
	if err != nil {
		return 0, fmt.Errorf("prune clips: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func scanClip(sc scannable) (Clip, error) {
	var (
		c        Clip
		kind     string
		audience string
		// tunarr_program_id is NULLABLE since §9.1 — an install with no Tunarr has none,
		// which is a supported configuration. sql.NullString rather than string so a NULL
		// scans cleanly instead of erroring on a nil-to-string conversion.
		tunarrID     sql.NullString
		lastPlayedAt int64
		removedAt    int64
		// ⚠ All three are real bools since 00033 rebuilt the table. `aiTagged` was an INTEGER
		// scanned into an int until V38c — the rebuild made it BOOLEAN on Postgres like its two
		// neighbours, so the old int would now fail to scan. The dialect split is per COLUMN, and
		// a rebuild is exactly when a column can change sides.
		aiTagged  bool
		held      bool
		autoFiled bool
		// vision_tagged is BOOLEAN (00038), the same side of the dialect split as its three
		// neighbours above — scanned into a bool, never an int.
		visionTagged bool
		// is_composite is BOOLEAN (00039), the same side again.
		isComposite bool
		updatedAt   int64
		// created_at is BIGINT on Postgres and INTEGER on sqlite (00046) — the same per-column
		// dialect split every epoch value here follows, and both are 64-bit.
		createdAt int64
	)
	err := sc.Scan(&c.Hash, &c.Path, &tunarrID, &c.Name, &kind, &c.Era, &audience, &c.Category,
		&c.DurationMs, &c.GeographicScope, &c.Country, &c.Market, &c.Network, &c.Station, &c.AirDate, &c.GeoEvidence,
		&c.Rating, &c.Source, &aiTagged, &c.Quality, &c.License, &c.Thumbnail, &c.Preview,
		&c.ThumbImageHash, &c.HoverImageHash, &c.Language,
		&c.Transcript, &c.Brand, &c.VisibleText, &visionTagged,
		&isComposite, &c.ParentHash,
		&c.PlayCount, &lastPlayedAt, &c.SuggestedEra, &removedAt, &held, &c.Confidence, &autoFiled, &updatedAt, &createdAt)
	if err == sql.ErrNoRows {
		return Clip{}, ErrNotFound
	}
	if err != nil {
		return Clip{}, err
	}
	c.Kind = filler.Kind(kind)
	c.Audience = filler.Audience(audience)
	c.TunarrProgramID = tunarrID.String // "" when NULL — the no-Tunarr case
	c.AITagged = aiTagged
	c.VisionTagged = visionTagged
	c.IsComposite = isComposite
	c.LastPlayedAt = fromEpoch(lastPlayedAt)
	if removedAt != 0 {
		c.RemovedAt = fromEpoch(removedAt)
	}
	c.Held = held
	c.AutoFiled = autoFiled
	c.UpdatedAt = fromEpoch(updatedAt)
	c.CreatedAt = fromEpoch(createdAt)
	return c, nil
}

func scanClips(rows *sql.Rows) ([]Clip, error) {
	var out []Clip
	for rows.Next() {
		c, err := scanClip(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// attachTags loads the taxonomy tag set (§10 V45a) for a batch of clips and fills both the full
// Clip.Tags match set and Clip.AssertedTags, the subset an editor may safely round-trip.
//
// ⚠ ONE query for the whole batch (`WHERE clip_hash IN (…)`), never one per clip — pod assembly loads
// the entire catalog through ListClips, so a per-clip tag read would be a textbook N+1 on the hot path
// (the class [[loomarr-perf-cpu-profile-blind-to-waiting]] warns about). The full leaf+rollup set is
// loaded (leaf and rollup rows alike), because that is what curation matches against; `Category` is
// derived from it separately at write time, not here.
//
// A clip with no tag rows keeps Tags == nil, which is the honest "tagger has not reached it" state.
func (s *sqlStore) attachTags(ctx context.Context, clips []Clip) error {
	if len(clips) == 0 {
		return nil
	}
	idx := make(map[string]int, len(clips)) // hash → position in clips, so we can fill in place
	placeholders := make([]string, len(clips))
	args := make([]any, len(clips))
	for i, c := range clips {
		idx[c.Hash] = i
		placeholders[i] = "?"
		args[i] = c.Hash
	}
	q := `SELECT clip_hash, taxon, leaf FROM clip_tags WHERE clip_hash IN (` + strings.Join(placeholders, ",") + `) ORDER BY clip_hash, taxon`
	rows, err := s.db.QueryContext(ctx, s.ph(q), args...)
	if err != nil {
		return fmt.Errorf("attach clip tags: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var hash, taxon string
		var asserted bool
		if err := rows.Scan(&hash, &taxon, &asserted); err != nil {
			return err
		}
		if i, ok := idx[hash]; ok {
			clips[i].Tags = append(clips[i].Tags, taxon)
			if asserted {
				clips[i].AssertedTags = append(clips[i].AssertedTags, taxon)
			}
		}
	}
	return rows.Err()
}

// ⚠ `boolToInt` was here and is DELETED (V38c). Every bool column is BOOLEAN on Postgres now that
// 00033 rebuilt `clips`, so binding an int is a 42804 — and this helper was how that mistake kept
// happening: it sat next to correct code, looked like the local idiom, and was copied onto three
// columns where it was wrong. A helper whose only remaining use is a bug is worth deleting
// outright rather than leaving for the next person to reach for.

// nullIfEmpty writes "" as SQL NULL.
//
// For tunarr_program_id specifically: the column is nullable because an install with no Tunarr
// has no uuid for its clips. Storing "" instead would make every such clip share a value, and
// any future UNIQUE constraint on it would then reject the second clip — whereas SQL NULLs do
// not collide. Distinguishing "no Tunarr" from "the empty uuid" also keeps the sync's
// preserve-a-known-uuid branch honest.
func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// escapeLike neutralizes LIKE metacharacters so a search term is matched literally.
// Without it, a query of "%" matches every clip and "_" matches any single character —
// surprising for a plain search box, and a needless full-table scan.
func escapeLike(term string) string {
	r := strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`)
	return r.Replace(term)
}

// SetClipsHeld files clips into the catalog (held=false) or sends them back to the review queue
// (held=true) — the Incoming tab's decisions (§10 V38).
//
// ⚠ **This and RetryClipPipeline are the ONLY writers of `held` and `auto_filed`**. Recovery is
// the narrow exception because holding the clip and requeueing its failed row must be atomic.
// `UpsertClip` deliberately omits both from its DO UPDATE list, so the folder
// scan cannot file a held clip by finding its file still on disk. Route the write anywhere else
// and one scan pass empties the review queue into live channels.
//
// `autoFiled` records that no human looked at these before they became playable. It is set when
// the threshold files them and cleared whenever a person decides — because the flag's whole job
// is to answer "which of these did I never see?", and a human decision makes that answer no.
//
// Returns rows affected; unknown paths are skipped for the same reason as SetClipsRemoved — a
// bulk action races a re-scan, and failing the batch for one stale row is worse than doing the
// rest.
func (s *sqlStore) SetClipsHeld(ctx context.Context, paths []string, held, autoFiled bool, at time.Time) (int, error) {
	if len(paths) == 0 {
		return 0, nil
	}
	placeholders := make([]string, len(paths))
	args := make([]any, 0, len(paths)+3)
	args = append(args, held, autoFiled, epoch(at))
	for i, p := range paths {
		placeholders[i] = "?"
		args = append(args, p)
	}
	q := `UPDATE clips SET held = ?, auto_filed = ?, updated_at = ? WHERE path IN (` +
		strings.Join(placeholders, ",") + `)`
	res, err := s.db.ExecContext(ctx, s.ph(q), args...)
	if err != nil {
		return 0, fmt.Errorf("set clips held: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("set clips held: %w", err)
	}
	return int(n), nil
}

// SetClipsRemoved tombstones (or restores) clips by path — the Catalog tab's "Remove from
// catalog" (V35).
//
// ⚠ This, ReplaceSplitChildren, and RetryClipPipeline are the ONLY writers of `removed_at`.
// Recovery is the narrow exception because restoring the tombstone and requeueing its failed row
// must be atomic. `UpsertClip` deliberately omits the column, so the next scan
// cannot resurrect a removed clip merely by finding its file still on disk.
//
// ⚠ It does NOT touch the file. Nothing in Loomarr deletes an operator's media — the button says
// remove from the CATALOG, and the file stays where they put it.
//
// Returns the number of rows affected; unknown paths are silently skipped, because a bulk action
// over a list the operator selected minutes ago races a re-scan and failing the whole batch for
// one stale row would be worse than removing the rest.
func (s *sqlStore) SetClipsRemoved(ctx context.Context, paths []string, at time.Time) (int, error) {
	if len(paths) == 0 {
		return 0, nil
	}
	placeholders := make([]string, len(paths))
	args := make([]any, 0, len(paths)+1)
	args = append(args, epoch(at))
	for i, p := range paths {
		placeholders[i] = "?"
		args = append(args, p)
	}
	q := `UPDATE clips SET removed_at = ? WHERE path IN (` + strings.Join(placeholders, ",") + `)`
	res, err := s.db.ExecContext(ctx, s.ph(q), args...)
	if err != nil {
		return 0, fmt.Errorf("set clips removed: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("set clips removed: %w", err)
	}
	return int(n), nil
}

// ReplaceSplitChildren completes a re-split by making keepHashes the active generation for one
// composite. Old children are TOMBSTONED, never deleted; their bytes and metadata remain available
// to restore. Any clip explicitly pinned by a channel joins the keep set, because a detector
// improvement must not silently invalidate an operator override.
//
// The policy reads and clip writes share one transaction. Postgres locks channel rows while their
// pins are decoded, so a concurrent channel CAS cannot add a pin in the gap between the read and
// retirement. Current-generation hashes are restored too: a cut retired by an earlier generation
// may become byte-identical to a newly accepted cut, and UpsertClip intentionally preserves its
// tombstone on an ordinary scan.
func (s *sqlStore) ReplaceSplitChildren(ctx context.Context, parentHash string, keepHashes []string, at time.Time) (int, error) {
	if parentHash == "" {
		return 0, errors.New("replace split children: parent hash is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("replace split children %s: %w", parentHash, err)
	}
	defer func() { _ = tx.Rollback() }()
	n, err := s.replaceSplitChildrenTx(ctx, tx, parentHash, keepHashes, at)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("replace split children %s: %w", parentHash, err)
	}
	return n, nil
}

func (s *sqlStore) replaceSplitChildrenTx(ctx context.Context, tx *sql.Tx, parentHash string, keepHashes []string, at time.Time) (int, error) {
	query := `SELECT policy_json FROM channels`
	if s.dialect == DialectPostgres {
		query += ` FOR UPDATE`
	}
	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		return 0, fmt.Errorf("replace split children %s read channel pins: %w", parentHash, err)
	}
	keep := make(map[string]struct{}, len(keepHashes))
	for _, hash := range keepHashes {
		if hash != "" {
			keep[hash] = struct{}{}
		}
	}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			_ = rows.Close()
			return 0, fmt.Errorf("replace split children %s scan channel policy: %w", parentHash, err)
		}
		var policy schedule.ChannelPolicy
		if err := json.Unmarshal([]byte(raw), &policy); err != nil {
			_ = rows.Close()
			return 0, fmt.Errorf("replace split children %s decode channel policy: %w", parentHash, err)
		}
		if policy.Filler != nil {
			for _, hash := range policy.Filler.Pinned {
				if hash != "" {
					keep[hash] = struct{}{}
				}
			}
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, fmt.Errorf("replace split children %s read channel pins: %w", parentHash, err)
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("replace split children %s close channel pins: %w", parentHash, err)
	}

	hashes := make([]string, 0, len(keep))
	for hash := range keep {
		hashes = append(hashes, hash)
	}
	sort.Strings(hashes)
	placeholders := make([]string, len(hashes))
	for i := range hashes {
		placeholders[i] = "?"
	}

	if len(hashes) > 0 {
		args := make([]any, 0, len(hashes)+3)
		args = append(args, epoch(at), parentHash)
		for _, hash := range hashes {
			args = append(args, hash)
		}
		q := `UPDATE clips SET removed_at = 0, updated_at = ? WHERE parent_hash = ? AND hash IN (` + strings.Join(placeholders, ",") + `)`
		if _, err := tx.ExecContext(ctx, s.ph(q), args...); err != nil {
			return 0, fmt.Errorf("replace split children %s restore generation: %w", parentHash, err)
		}
	}

	args := []any{epoch(at), epoch(at), parentHash}
	q := `UPDATE clips SET removed_at = ?, updated_at = ? WHERE parent_hash = ? AND removed_at = 0`
	if len(hashes) > 0 {
		q += ` AND hash NOT IN (` + strings.Join(placeholders, ",") + `)`
		for _, hash := range hashes {
			args = append(args, hash)
		}
	}
	res, err := tx.ExecContext(ctx, s.ph(q), args...)
	if err != nil {
		return 0, fmt.Errorf("replace split children %s retire old generation: %w", parentHash, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("replace split children %s count retired: %w", parentHash, err)
	}
	return int(n), nil
}

// SetClipLanguage records what the detection job heard (§10 V40, migration 00036).
//
// ⚠ **The ONLY writer of `language`**, exactly like SetClipsRemoved owns the tombstone and
// RecordClipPlay owns the counters — and for the same reason: `UpsertClip` deliberately omits the
// column, so the folder scan cannot blank a detected language by finding the file still on disk.
// Without that omission every sync would reset the catalog to "not yet checked" and the job would
// re-detect everything on the next pass, which on the local backend is ~341s per clip under QEMU.
//
// Keyed by PATH rather than hash, because that is what the job carries and what every other
// clip-writing method here takes.
func (s *sqlStore) SetClipLanguage(ctx context.Context, path, language string, at time.Time) error {
	_, err := s.db.ExecContext(ctx,
		s.ph(`UPDATE clips SET language = ?, updated_at = ? WHERE path = ?`),
		language, epoch(at), path)
	if err != nil {
		return fmt.Errorf("set clip language %s: %w", path, err)
	}
	return nil
}

// SetClipConfidence records the tagger's grounding-capped score (§10 V38, migration 00030).
//
// ⚠ **The ONLY writer of `confidence`, and until V51a there was NO writer at all.**
// `TagSuggestion.Score` computed the number, `Tagger.Run` compared it against the auto-file
// threshold, and then threw it away. `UpsertClip` inserts a literal 0 and correctly omits the
// column from its DO UPDATE, so nothing ever put a score in the database: `confidence` was 0 for
// every clip that had ever existed, the Incoming tab's meter never rendered, and the field's own
// doc string ("0 = never scored") was true of the entire catalog. The store's conformance case
// passed throughout because it wrote a value by hand and asserted the round trip — a column can
// round-trip perfectly and still have no producer.
//
// ⚠ The value must come from `TagSuggestion.Score`, never from the model directly. The score is a
// ceiling set by what could be verified in the clip's own text, which the model may only lower; a
// caller passing the model's self-assessment would defeat the grounding cap that stops a
// fabricated era being auto-filed.
//
// Keyed by PATH, like SetClipLanguage and SetClipTranscript beside it — that is what the job
// carries.
func (s *sqlStore) SetClipConfidence(ctx context.Context, path string, confidence int, at time.Time) error {
	_, err := s.db.ExecContext(ctx,
		s.ph(`UPDATE clips SET confidence = ?, updated_at = ? WHERE path = ?`),
		confidence, epoch(at), path)
	if err != nil {
		return fmt.Errorf("set clip confidence %s: %w", path, err)
	}
	return nil
}

// SetClipTranscript records what the transcribe job heard (§10 V44, migration 00038).
//
// ⚠ **The ONLY writer of `transcript`**, exactly like SetClipLanguage owns `language` and for the
// identical reason: `UpsertClip` deliberately omits the column, so the folder scan cannot blank a
// transcribed clip by finding its file still on disk. Without that omission every sync would reset
// the catalog to "not yet transcribed" and the job would re-run Whisper on everything — ~341s per
// clip under QEMU.
//
// Keyed by PATH rather than hash, because that is what the job carries and what SetClipLanguage
// beside it takes. An empty transcript is a legitimate write: it records "checked, wordless", which
// is what stops the selective job from re-visiting a silent clip forever.
func (s *sqlStore) SetClipTranscript(ctx context.Context, path, transcript string, at time.Time) error {
	_, err := s.db.ExecContext(ctx,
		s.ph(`UPDATE clips SET transcript = ?, updated_at = ? WHERE path = ?`),
		transcript, epoch(at), path)
	if err != nil {
		return fmt.Errorf("set clip transcript %s: %w", path, err)
	}
	return nil
}

// SetClipBrand records a GROUNDED advertiser found by the text tagger or confirmed by an operator
// (§10 V44, migration 00038).
//
// ⚠ Writes `brand` and nothing else — deliberately narrower than ApplyClipVision, which also
// stamps `vision_tagged` and owns `visible_text`. The text tagger grounds a brand in the clip's
// text signals (filename, sidecar, or the persisted transcript); the vision pass grounds one in the
// on-screen text. Both are legitimate writers of `brand`, and each writes only what it is entitled
// to: this must NOT touch `vision_tagged`, or a text-tagged clip would masquerade as one a vision
// pass had read, and a re-run would skip the frame it never actually looked at.
//
// The caller has already applied the grounding rule (validateTags keeps a brand only when it
// appears literally in the text), so this writes what it is given. Keyed by PATH, like the transcript
// and vision writers it sits beside and unlike the hash-keyed classification update — the job carries the
// path. `UpsertClip` omits `brand` from its DO UPDATE for the same single-writer reason as every
// other job column, so a re-sync cannot blank a grounded brand and make the tagger re-derive it.
func (s *sqlStore) SetClipBrand(ctx context.Context, path, brand string, at time.Time) error {
	_, err := s.db.ExecContext(ctx,
		s.ph(`UPDATE clips SET brand = ?, updated_at = ? WHERE path = ?`),
		brand, epoch(at), path)
	if err != nil {
		return fmt.Errorf("set clip brand %s: %w", path, err)
	}
	return nil
}

// ApplyClipVision records one semantic vision pass (§10 V44/V55): the on-screen text it saw, a
// grounded brand and era, and the taxonomy assertions supported by the frames. The facts, direct
// assertions, rollups, and category compatibility shadow commit together.
//
// ⚠ **The ONLY writer of `visible_text` and `vision_tagged`.** `brand` it SHARES with SetClipBrand
// (the text tagger's grounded writer) — both write `brand` and only `brand`, and this one must not
// be confused for the sole owner or a text-grounded brand would look vision-read. Same rule as
// every job column here: `UpsertClip` omits them so a re-sync cannot undo the pass. The caller has
// already applied the grounding rule (a brand/era/category survives only if `visibleText` supports
// it), so this method writes what it is given — the validation lives in the domain, not the SQL.
//
// A blank vision brand preserves a brand grounded by the text tier. Era behaves the same way. Tags
// are additive: vision can contribute evidence without erasing an operator or the text classifier.
func (s *sqlStore) ApplyClipVision(ctx context.Context, hash, path, brand, visibleText string, era, suggestedEra int, leaves []string, at time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("apply clip vision: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if s.dialect == DialectPostgres {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock_shared(hashtext('loomarr-taxonomy'))`); err != nil {
			return fmt.Errorf("apply clip vision: lock: %w", err)
		}
	}
	result, err := tx.ExecContext(ctx, s.ph(
		`UPDATE clips SET
		   brand = CASE WHEN ? <> '' THEN ? ELSE brand END,
		   visible_text = ?, vision_tagged = ?,
		   era = CASE WHEN ? > 0 THEN ? ELSE era END,
		   suggested_era = CASE WHEN ? > 0 THEN 0 WHEN ? > 0 AND era = 0 AND suggested_era = 0 THEN ? ELSE suggested_era END,
		   updated_at = ? WHERE hash = ? AND path = ?`),
		brand, brand, visibleText, true,
		era, era,
		era, suggestedEra, suggestedEra,
		epoch(at), hash, path)
	if err != nil {
		return fmt.Errorf("apply clip vision %s: %w", path, err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}

	rows, err := tx.QueryContext(ctx, s.ph(
		`SELECT taxon FROM clip_tags WHERE clip_hash = ? AND leaf = ? ORDER BY taxon`), hash, true)
	if err != nil {
		return fmt.Errorf("apply clip vision: list asserted tags: %w", err)
	}
	merged := append([]string(nil), leaves...)
	for rows.Next() {
		var leaf string
		if err := rows.Scan(&leaf); err != nil {
			_ = rows.Close()
			return fmt.Errorf("apply clip vision: scan asserted tag: %w", err)
		}
		merged = append(merged, leaf)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("apply clip vision: close asserted tags: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("apply clip vision: list asserted tags: %w", err)
	}
	if err := s.setClipTagsTx(ctx, tx, hash, merged); err != nil {
		return err
	}
	return tx.Commit()
}

// ClearClipVisionTags removes the vision stamp so the rung will look again (§10 V51b) — the
// invalidation half of `Rewind`.
//
// ⚠ **It clears the STAMP and the text vision read; it does NOT clear brand, era or category.**
// Those three are SHARED with the text tagger — `SetClipBrand` and the classification update write the same
// columns — and nothing on the row records which tier put a value there. Blanking them would
// therefore destroy a text-grounded brand, or an era an operator confirmed by hand, in order to
// re-run a tier that may not even find one. A re-read simply overwrites what it can ground, which
// is the same additive contract `ApplyClipVision` has.
//
// ⚠ A separate narrow method rather than calling `ApplyClipVision` with empty strings: that one
// is pinned as the ONLY writer of visible_text/vision_tagged and writes what it is GIVEN, so the
// empty-argument trick works by accident today and breaks the first time it learns to gap-fill.
func (s *sqlStore) ClearClipVisionTags(ctx context.Context, path string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, s.ph(
		`UPDATE clips SET visible_text = '', vision_tagged = ?, updated_at = ? WHERE path = ?`),
		false, epoch(at), path)
	if err != nil {
		return fmt.Errorf("clear clip vision tags %s: %w", path, err)
	}
	return nil
}

// SetClipComposite marks (or unmarks) a clip as a composite — a recorded break, not airable (§10
// V45). Called by intake/detection when a 16-minute file is recognised as a break, and by split
// Confirm on the PARENT (which V45 keeps and marks composite rather than deleting).
//
// ⚠ **The ONLY writer of `is_composite`**, exactly like SetClipLanguage owns `language`: UpsertClip
// omits it, so a re-sync finding the original file on disk cannot flip a confirmed composite back to
// an airable clip. Keyed by HASH — a composite is identified by its content, and the detection/
// confirm paths hold the hash.
// ClipArtworkPending is one clip whose rendered artwork has not been adopted into the image
// service yet. Paths are RELATIVE to the artwork cache directory, exactly as the columns store
// them — the store does not know where FILLER_DIR is, and resolving that here would put a
// filesystem assumption inside a query.
type ClipArtworkPending struct {
	Hash      string
	Thumbnail string
	Preview   string
}

// ListClipsPendingArtworkAdoption returns clips that HAVE rendered artwork but no image-service
// identity for it yet (§22, V52 phase 6) — the adoption job's work list.
//
// ⚠ The predicate is per-ASSET, not per-clip: a clip whose still adopted but whose animation did
// not must come back, or the hover loop would never be adopted for any clip where the two renders
// completed in different passes. `OR` rather than `AND` is the whole correctness of this query.
//
// ⚠ Tombstoned clips are excluded. Adopting artwork for a clip the operator removed would copy
// bytes into the image store purely so the GC could collect them again later.
func (s *sqlStore) ListClipsPendingArtworkAdoption(ctx context.Context, limit int) ([]ClipArtworkPending, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, s.ph(
		`SELECT hash, thumbnail, preview FROM clips
		  WHERE removed_at = 0
		    AND (   (thumbnail <> '' AND thumb_image_hash = '')
		         OR (preview   <> '' AND hover_image_hash = ''))
		  ORDER BY updated_at
		  LIMIT ?`), limit)
	if err != nil {
		return nil, fmt.Errorf("list clips pending artwork adoption: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []ClipArtworkPending
	for rows.Next() {
		var p ClipArtworkPending
		if err := rows.Scan(&p.Hash, &p.Thumbnail, &p.Preview); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SetClipArtworkImages records the image-service identities of a clip's still and hover loop
// (§22, V52 phase 6). The ONLY writer of those two columns — which is why they are absent from
// UpsertClip's DO UPDATE list, exactly like is_composite and the play counters.
//
// ⚠ Both are written together because they come from ONE ffmpeg pass and one ingest step, but
// either may legitimately be "" — the still can succeed while the animation fails, and a caller
// that had only one would otherwise have to read-modify-write to avoid blanking the other.
func (s *sqlStore) SetClipArtworkImages(ctx context.Context, hash, thumbHash, hoverHash string, at time.Time) error {
	res, err := s.db.ExecContext(ctx,
		s.ph(`UPDATE clips SET thumb_image_hash = ?, hover_image_hash = ?, updated_at = ? WHERE hash = ?`),
		thumbHash, hoverHash, epoch(at), hash)
	if err != nil {
		return fmt.Errorf("set clip artwork images %s: %w", hash, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *sqlStore) SetClipComposite(ctx context.Context, hash string, composite bool, at time.Time) error {
	res, err := s.db.ExecContext(ctx,
		s.ph(`UPDATE clips SET is_composite = ?, updated_at = ? WHERE hash = ?`),
		composite, epoch(at), hash)
	if err != nil {
		return fmt.Errorf("set clip composite %s: %w", hash, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
