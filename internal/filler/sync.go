package filler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// This file is the catalog sync (§10, revised by §9.1): loomarr scans FILLER_DIR
// itself and probes each clip with ffprobe. Path + name + DURATION come from that
// scan; loomarr owns the match metadata (era/audience/category) too, and PRESERVES
// it across syncs so a re-sync never clobbers hand-edited or AI-assigned tags.
// `/v1/filler/sync` triggers it; a periodic sync runs alongside the reconciler
// (FILLER_SYNC_EVERY, §15).
//
// It previously synced FROM the Tunarr `local` filler source, with clip identity
// being the Tunarr program uuid. See Clip for why that could not serve internal
// playout (a uuid is not a playable input) and why the dependency ran the wrong
// way (no Tunarr ⇒ no catalog ⇒ no commercials, on a service §9.1 makes optional).

// RawClip is one clip as discovered by a scan, before catalog metadata is merged.
type RawClip struct {
	// ID is the identity: the clip's sparse content hash (§10 V38c).
	//
	// ⚠ Separate from Path since V38c. Identity used to BE the path, which broke the moment many
	// watched folders were allowed — two folders each holding `ads/coke.mp4` produced one id and
	// silently overwrote each other. Hashing the bytes also answers the question a path cannot:
	// *is this the same advert?*, which is what lets intake skip duplicates.
	ID string
	// Path is the clip's LOCATION under the clip folder — `a3/f9/<hash>.mp4` for anything intake
	// has filed. Data, not identity.
	Path string
	// Source is the registered source id restored from Loomarr's sidecar. Empty is a hand-copied
	// or legacy clip and resolves through the folder source policy.
	Source string
	// ParentHash is restored only from Loomarr's conditioning lineage sidecar. It reconnects a
	// reviewed child to its composite after the rebuildable clip catalog is empty.
	ParentHash string
	// LineageInvalid keeps a damaged child fail-closed during catalog reconstruction. ParentHash
	// may still name the retained parent, but the child remains held until its sidecar is repaired.
	LineageInvalid bool
	// ConditioningHold keeps a valid lineage-only child and malformed conditioning state out of
	// rotation across an empty-catalog rebuild until complete post-rewrite evidence exists.
	ConditioningHold bool
	// SidecarInvalid prevents catalog repair from overwriting or laundering unreadable durable
	// metadata. The clip is held until an operator repairs the sidecar.
	SidecarInvalid bool
	// IsComposite is derived for retained parents from valid child lineage before any rows are
	// written, so filesystem traversal order cannot briefly make the parent airable.
	IsComposite bool
	// TunarrProgramID is set only when the clip was ALSO seen through Tunarr's local
	// source, so Tunarr-backed channels can still build filler-lists. Empty on an
	// install with no Tunarr, which is a supported configuration, not a degraded one.
	TunarrProgramID string
	Name            string
	DurationMs      int64
	Kind            Kind
	Era             int // initial era from filename; 0 if none
	GeographicScope GeographicScope
	Country         string
	Market          string
	Network         string
	Station         string
	AirDate         string
	GeoEvidence     string
	// Quality is the resolution label ("1080p", "480p") derived from the probed video
	// height; "" when the file has no video stream or was never re-probed. Display-only —
	// the guide's pod hover card shows it so a grainy 240p advert is explicable rather
	// than surprising. It never affects selection (V17c adds an opt-in floor).
	Quality string
	// License is the licence URL the source declared, read from the clip's info-JSON sidecar
	// at scan time (V33). "" means UNKNOWN — the common case, ~92% of archive.org items — and
	// never "public domain".
	License string
	// Thumbnail is the extracted frame's path relative to the thumbnail cache dir; "" when
	// extraction failed or has not run. Filled by a SEPARATE pass (GenerateThumbnails), not
	// by the probe — see thumbnail.go for why it cannot ride along with ffprobe.
	Thumbnail string
	// Preview is the animated hover preview's path relative to the preview cache dir; "" when
	// the render failed or has not run (V39). A third pass again (GeneratePreviews) for the same
	// reason as the still: it is a distinct ffmpeg invocation, and a failed render must cost a
	// preview rather than a clip.
	Preview string
}

// FillerSource discovers the clips in FILLER_DIR.
//
// Implemented by DirSource (the local scan, §9.1) and satisfied in tests by a double. It was
// previously implemented by the Tunarr client — EnsureLocalSource registered the drop-folder
// as a Tunarr `local` media source and ListLocalClips read back what Tunarr had scanned.
//
// EnsureLocalSource is retained because Tunarr-backed channels still need that registration
// (their filler-lists reference Tunarr program ids). It is now BEST-EFFORT: an install with no
// Tunarr fails it harmlessly and still gets a full catalog from the local scan, which is the
// whole point of the change.
type FillerSource interface {
	EnsureLocalSource(ctx context.Context, dir string) error
	ListLocalClips(ctx context.Context) ([]RawClip, error)
}

// Store is the slice of the store the sync needs.
type Store interface {
	UpsertClip(ctx context.Context, c StoreClip) error
	GetClip(ctx context.Context, id string) (StoreClip, bool, error)
	DeleteClipsNotIn(ctx context.Context, keepIDs []string) (int, error)
}

// AcquisitionAuthority is the durable held/filed authority for Loomarr downloads. Filesystem
// validation stays in Syncer because it owns the applied clip root; persistence only resolves and
// advances manifest values.
type AcquisitionAuthority interface {
	AcquisitionArtifactForClip(ctx context.Context, mediaPath, clipHash string) (AcquisitionArtifact, bool, error)
	UpsertAcquisitionArtifacts(ctx context.Context, artifacts []AcquisitionArtifact) error
}

// StoreClip is the persistence view the sync round-trips (mirrors store.Clip;
// declared here so filler doesn't import store — the adapter in main bridges them).
type StoreClip struct {
	Clip
	UpdatedAt time.Time
}

// wasFetchedByUs reports whether Loomarr DOWNLOADED this clip, rather than an operator putting it
// there (§10 V38c) — the held/filed fork.
//
// ⚠ **Reads a FIELD inside the sidecar, not the sidecar's existence** (changed from V38b). The old
// test was "does `<name>.info.json` exist", which worked only while Loomarr never wrote sidecars.
// V38c writes tags back for hand-dropped clips too, so existence now says nothing — every tagged
// clip would look downloaded, and a hand-dropped one would start being held for review.
//
// The field is also the better signal, not merely a repair: an operator who copies a clip TOGETHER
// with its sidecar gets the honest answer, and one who tidies sidecars away no longer flips a
// clip's lifecycle by accident. Explicit beats inferred.
//
// A missing drop-folder path answers false, which files the clip. That is the right failure: an
// install whose FILLER_DIR we cannot read should behave as it did before this phase rather than
// holding every clip it finds.
func (s *Syncer) wasFetchedByUs(clipPath string) bool {
	if s.dir == "" || clipPath == "" {
		return false
	}
	return SidecarFetchedByUs(filepath.Join(s.dir, clipPath))
}

// ErrSourceDisabled reports that the drop-folder is switched off on the Sources tab (§10 V35).
//
// ⚠ A distinct error rather than a silent zero result, and that is the difference between the
// switch working and the switch lying. A no-op success makes "Fetch now" a button that appears
// to run and changes nothing, which reads as a broken sync rather than a disabled source; the
// caller turns this into an answer that names the switch to flip.
var ErrSourceDisabled = errors.New("filler: the drop-folder source is switched off")

// Syncer reconciles the clip catalog against the Tunarr `local` filler source.
type Syncer struct {
	source FillerSource
	store  Store
	layout Layout
	dir    string // resolved local traversal root for this application generation
	watch  string // immutable arrival folder paired with dir for this application generation
	log    *slog.Logger
	now    func() time.Time
	// enabled reports whether the drop-folder source is switched on (§10 V35). Read on every
	// sync rather than captured, because the setting hot-applies (config-design §3) — an
	// operator switching the folder off expects the NEXT scheduled pass to stop, not a restart
	// to be required. nil means always on, which keeps every existing construction unchanged.
	enabled func() bool
	// scanSources lists the registered folders and libraries to drain (§10 V38c). nil ⇒ only the
	// configured clip folder is scanned, which is exactly the pre-V38c behaviour — so every
	// existing construction keeps working without opting in.
	scanSources ScanSourceStore
	// libraries copies clips out of a media-server library. nil ⇒ library rows do no work, which
	// is the honest state for an install with no media server configured.
	libraries    *LibraryScanner
	acquisitions AcquisitionAuthority
}

// drainScanSources reads every registered folder and library into the watch folder (§10 V38c),
// leaving the actual filing to the one intake that runs immediately afterwards.
//
// ⚠ **Every source is isolated from every other one.** A media server that is down, a folder whose
// mount has gone, a library name that no longer exists — each is logged against the row an
// operator can see, and the loop continues. Returning an error from here would mean one
// unreachable source costs a channel every commercial it already had on disk, which is the
// dependency §9.1 removed reappearing one level down.
//
// ⚠ Nothing is returned at all, deliberately. There is no aggregate outcome worth reporting: the
// clips that arrived are counted by the intake that follows, and a per-source failure belongs on
// that source's row rather than in a number the caller would have to interpret.
func (s *Syncer) drainScanSources(ctx context.Context) {
	if s.scanSources == nil {
		return // no per-source scanning wired (the single-folder path still runs)
	}
	srcs, err := s.scanSources.ListScanSources(ctx)
	if err != nil {
		if s.log != nil {
			s.log.Warn("filler: could not list scan sources; scanning the configured folder only",
				"err", err)
		}
		return
	}
	watch := s.watch
	if watch == "" {
		return
	}

	for _, src := range srcs {
		select {
		case <-ctx.Done():
			return
		default:
		}
		switch src.Kind {
		case "folder":
			// ⚠ A registered folder is drained into the WATCH folder rather than scanned in
			// place, so its clips are hashed, deduplicated and filed exactly like everything
			// else. Scanning it in place would make it a second catalog location — the divergent
			// path §10 forbids — and two folders holding the same advert would be two clips.
			//
			// The configured clip folder is skipped: it is where clips already live, and draining
			// it into the watch folder would move the whole catalog back through intake.
			if src.URI == "" {
				continue
			}
			layout := s.layout
			if layout.ClipDir() == "" {
				layout, err = NewLayout(s.dir, s.watch)
				if err != nil {
					s.warnSource("filler: could not validate a watched folder", src, err)
					continue
				}
			}
			sourceDir, err := layout.intakeSource(src.URI)
			if err != nil {
				s.warnSource("filler: skipped a watched folder that overlaps the clip library", src, err)
				continue
			}
			if res, err := TakeInFrom(sourceDir, s.dir, true, src.ID, s.logAttrs); err != nil {
				s.warnSource("filler: could not drain a watched folder", src, err)
			} else if s.log != nil && res.Taken > 0 {
				s.log.Info("filler: filed clips from a watched folder",
					"source", src.ID, "folder", src.URI, "taken", res.Taken)
			}
		case "library":
			if s.libraries == nil {
				continue // no media server configured; the row simply does no work
			}
			res, err := s.libraries.Scan(ctx, src.URI, watch)
			if err != nil {
				// ⚠ ErrLibraryUnreachableStorage is logged like any other failure HERE, but it
				// stays a distinct error so the Sources row can say "mount the volume" rather
				// than "this library is empty" — the two look identical to an operator and have
				// completely different remedies.
				s.warnSource("filler: could not scan a media-server library", src, err)
				continue
			}
			if s.log != nil && res.Copied > 0 {
				s.log.Info("filler: copied clips from a media-server library",
					"source", src.ID, "library", src.URI, "copied", res.Copied)
			}
			if res.Copied > 0 {
				if _, err := TakeInFrom(watch, s.dir, true, src.ID, s.logAttrs); err != nil {
					s.warnSource("filler: could not file media-server library clips", src, err)
				}
			}
		}
	}
}

// warnSource logs a per-source failure against the row an operator can see.
func (s *Syncer) warnSource(msg string, src ScanSource, err error) {
	if s.log != nil {
		s.log.Warn(msg, "source", src.ID, "kind", src.Kind, "uri", src.URI, "err", err)
	}
}

// logAttrs adapts the Syncer's logger to the plain function intake takes.
//
// ⚠ Intake deliberately does NOT take an *slog.Logger. It is filesystem plumbing with no other
// dependency, and a nil-able function keeps it callable from a test with one argument rather than
// a constructed logger.
func (s *Syncer) logAttrs(msg string, args ...any) {
	if s.log != nil {
		s.log.Warn(msg, args...)
	}
}

// NewSyncer builds a catalog syncer over one immutable storage layout. The clip root is
// registered with Tunarr as a `local` media source and the paired watch folder drains into it.
func NewSyncer(source FillerSource, store Store, layout Layout, now func() time.Time, log *slog.Logger) *Syncer {
	if now == nil {
		now = time.Now
	}
	return &Syncer{
		source: source, store: store,
		layout: layout, dir: layout.ClipDir(), watch: layout.WatchDir(),
		log: log, now: now,
	}
}

// WithEnabled gates the syncer on the drop-folder's on/off switch.
//
// ⚠ The gate lives HERE rather than at each caller, because there are three (the manual sync
// route, the Sources tab's "Fetch now", and the scheduled job) and a switch that only some of
// them honour is not a switch. A test asserts the disabled sync neither scans nor writes.
func (s *Syncer) WithEnabled(enabled func() bool) *Syncer {
	s.enabled = enabled
	return s
}

// WithScanSources gives the syncer the registered folders and libraries to drain (§10 V38c).
//
// ⚠ `libraries` may be nil on an install with no media server, and that is a supported
// configuration rather than a degraded one: folder rows still drain, and library rows simply do
// no work. This is the same rule EnsureLocalSource follows for Tunarr — an optional service is
// never allowed to become a precondition for having a catalog.
func (s *Syncer) WithScanSources(srcs ScanSourceStore, libraries *LibraryScanner) *Syncer {
	s.scanSources = srcs
	s.libraries = libraries
	return s
}

// WithAcquisitionAuthority makes durable manifests authoritative for newly discovered ownership.
func (s *Syncer) WithAcquisitionAuthority(authority AcquisitionAuthority) *Syncer {
	s.acquisitions = authority
	return s
}

// SyncResult reports what a sync did (for the API + logs).
type SyncResult struct {
	Total    int // clips in the Tunarr local filler source
	Added    int // new clips
	Updated  int // existing clips whose server-derived fields changed
	Repaired int // legacy stale-hash paths moved to the bytes' canonical identity
	Pruned   int // clips removed (gone from the source)
}

// Sync ensures the Tunarr local source exists, then reconciles the catalog (§10):
//   - upsert every scanned clip, PRESERVING loomarr-owned tags (era/audience/
//     category/ai) on clips we already know — Tunarr only owns id/name/duration/
//     kind-hint;
//   - prune clips no longer in the source (identity = Tunarr program uuid).
//
// Duration always comes from Tunarr's scan. Idempotent: a no-change re-sync makes
// no tag edits.
func (s *Syncer) Sync(ctx context.Context) (SyncResult, error) {
	// Checked BEFORE the FILLER_DIR check and before any filesystem work: a switched-off
	// source must not scan, and must not report a configuration problem it does not have.
	if s.enabled != nil && !s.enabled() {
		return SyncResult{}, ErrSourceDisabled
	}
	if s.dir == "" {
		return SyncResult{}, fmt.Errorf("filler sync: no FILLER_DIR configured")
	}
	// Register the drop-folder with Tunarr, when there is one (idempotent, §10).
	//
	// BEST-EFFORT since §9.1, and the distinction is the whole point of that change: this
	// registration exists so TUNARR-backed channels can reference clips in a filler-list, and
	// it has nothing to do with discovering the files — Loomarr scans FILLER_DIR itself. An
	// install with no Tunarr, or one whose Tunarr is momentarily down, must still get a full
	// catalog; failing here would restore exactly the dependency §9.1 removed.
	if err := s.source.EnsureLocalSource(ctx, s.layout.ConfiguredClipDir()); err != nil && s.log != nil {
		s.log.Warn("filler: could not register the drop-folder with Tunarr; "+
			"scanning locally anyway (Tunarr-backed channels may lack filler until it returns)",
			"dir", s.dir, "err", err)
	}

	// Every registered folder and library drains into the watch folder FIRST, so the intake below
	// files what they brought in on this same pass (§10 V38c).
	s.drainScanSources(ctx)

	// ⚠ INTAKE RUNS BEFORE THE LISTING, and the order is the whole difference between "I dropped
	// a file in and it appeared" and "I dropped a file in and nothing happened". Draining after
	// the scan would file clips that this pass has already finished looking for, so every arrival
	// would wait a full sync interval — up to 15 minutes by default — to be catalogued.
	//
	// Failures are logged, not returned: a watch folder that cannot be drained (a permissions
	// problem, a full disk) must not take the catalog down with it. The clips already filed are
	// still there, and the arrivals stay put for the next pass.
	if taken, err := TakeInWithAcquisitionBinding(s.watch, s.dir, false, s.logAttrs, s.bindAcquisitionArtifact(ctx)); err != nil {
		if s.log != nil {
			s.log.Warn("filler: could not drain the watch folder; scanning what is already filed",
				"watch", s.watch, "err", err)
		}
	} else if s.log != nil && (taken.Taken > 0 || taken.Duplicates > 0) {
		s.log.Info("filler: filed new clips from the watch folder",
			"taken", taken.Taken, "duplicates", taken.Duplicates, "skipped", taken.Skipped)
	}

	raw, err := s.source.ListLocalClips(ctx)
	if err != nil {
		return SyncResult{}, fmt.Errorf("list filler clips: %w", err)
	}

	rawByID := make(map[string]int, len(raw))
	for i := range raw {
		rawByID[raw[i].ID] = i
	}
	for i := range raw {
		rc := &raw[i]
		if rc.ParentHash == "" || rc.LineageInvalid {
			continue
		}
		parentIndex, found := rawByID[rc.ParentHash]
		if !found || rc.ParentHash == rc.ID {
			rc.ConditioningHold = true
			continue
		}
		parent := &raw[parentIndex]
		if parent.ParentHash != "" || parent.LineageInvalid || parent.ConditioningHold || parent.SidecarInvalid {
			rc.ConditioningHold = true
			continue
		}
		// A clean retained top-level parent is expected to arrive as non-composite in an empty
		// catalog. The valid child lineage is the durable authority that derives this marker;
		// requiring it beforehand would make reconstruction impossible.
		parent.IsComposite = true
	}

	res := SyncResult{Total: len(raw)}
	keep := make([]string, 0, len(raw))
	for _, rc := range raw {
		// ⚠ IDENTITY IS rc.ID (the content hash), NOT rc.Path — since V38c. Keying on the path
		// here survived the re-key unnoticed because `DirSource` fills both and today they agree
		// for every clip under one folder. The moment two watched folders each hold `ads/coke.mp4`
		// the path stops being unique, one clip overwrites the other, and `keep` prunes a live
		// row — which is the exact failure V38c moved identity off the path to prevent.
		existing, found, err := s.store.GetClip(ctx, rc.ID)
		if err != nil {
			return res, fmt.Errorf("get clip %s: %w", rc.ID, err)
		}
		artifact, acquired, err := s.authorizeAcquisition(ctx, rc)
		if err != nil {
			// A claimed file whose exact bytes or portable provenance cannot be proved stays out
			// of the catalog. Preserve an existing row so repair cannot look like deletion.
			if found {
				keep = append(keep, rc.ID)
			}
			if s.log != nil {
				s.log.Warn("filler acquisition artifact remains quarantined", "clip", rc.Path, "err", err)
			}
			continue
		}
		if acquired && strings.TrimSpace(rc.Source) == "" {
			rc.Source = artifact.SourceID
		}
		rc, nameRepaired, err := s.repairOpaqueDisplayName(rc, existing, found)
		if err != nil {
			return res, fmt.Errorf("repair clip %s name: %w", rc.ID, err)
		}
		rc, pathRepaired, err := s.repairLegacyContentPath(rc, existing, found)
		if err != nil {
			return res, fmt.Errorf("repair clip %s path: %w", rc.ID, err)
		}
		if nameRepaired || pathRepaired {
			res.Repaired++
		}
		keep = append(keep, rc.ID)

		merged := StoreClip{UpdatedAt: s.now()}
		merged.Hash = rc.ID
		merged.Path = rc.Path
		merged.ParentHash = rc.ParentHash
		merged.IsComposite = rc.IsComposite
		// Scan-owned fields (always taken fresh — the filesystem is source of truth).
		merged.Name = rc.Name
		merged.DurationMs = rc.DurationMs
		merged.Kind = rc.Kind
		merged.GeographicScope = rc.GeographicScope
		merged.Country, merged.Market = rc.Country, rc.Market
		merged.Network, merged.Station, merged.AirDate, merged.GeoEvidence = rc.Network, rc.Station, rc.AirDate, rc.GeoEvidence
		// Quality is scan-owned like duration: it is a property of the FILE, so a re-encode
		// that changes the resolution should be reflected, and there is no hand-edited value
		// to protect. (Contrast the match tags below, which a human or the AI may have set.)
		merged.Quality = rc.Quality
		// Thumbnail is scan-owned for the same reason: it is derived from the FILE, and the
		// generator adopts an existing image rather than re-extracting, so this is already
		// the previous value whenever one exists.
		merged.Thumbnail = rc.Thumbnail
		// Preview, identically: derived from the FILE, and GeneratePreviews adopts an existing
		// animation rather than re-rendering, so this is already the previous value whenever one
		// exists (V39).
		merged.Preview = rc.Preview
		// Licence is scan-owned (it comes from the source's sidecar, never from a human), but
		// ⚠ a BLANK scan value must not erase a known one. The sidecar can go missing for
		// reasons that say nothing about the licence — an operator tidying `.info.json` files
		// out of the drop-folder, or a clip moved in by hand beside one that has one — and
		// "we stopped being able to see it" is not "it became unknown". Losing the record
		// silently is the failure worth preventing; a re-fetch restores it either way.
		merged.License = rc.License
		if merged.License == "" {
			merged.License = existing.License
		}
		// Carry the Tunarr uuid when the scan found one. Taken fresh rather than preserved:
		// a re-registered Tunarr local source mints new program ids, and a stale uuid would
		// build a filler-list referencing programs Tunarr no longer has.
		merged.TunarrProgramID = rc.TunarrProgramID
		if found {
			// PRESERVE loomarr-owned match tags across syncs (§10) — never clobber a
			// hand-edited or AI-assigned era/audience/category.
			merged.Era = existing.Era
			merged.Audience = existing.Audience
			merged.Category = existing.Category
			merged.AITagged = existing.AITagged
			merged.Rating = existing.Rating
			merged.GeographicScope = existing.GeographicScope
			merged.Country, merged.Market = existing.Country, existing.Market
			merged.Network, merged.Station, merged.AirDate, merged.GeoEvidence = existing.Network, existing.Station, existing.AirDate, existing.GeoEvidence
			merged.Source = existing.Source
			// The era suggestion (V34) is loomarr-owned too — a scan knows nothing
			// about it, so it survives like the tags above.
			merged.SuggestedEra = existing.SuggestedEra
			// ⚠ The lifecycle fields (§10 V38) are loomarr-owned and a scan knows nothing about
			// any of them. `Held` is the sharpest: this scan sets it false for every file it
			// finds on disk, so without this line a single pass would FILE every held clip —
			// emptying the review queue into live channels with no operator action.
			//
			// Belt and braces with UpsertClip's ON CONFLICT list, which omits all three. Either
			// alone would do; the file's own comment about the play counters argues for both,
			// because a future edit to one that forgot the other fails silently.
			merged.Held = existing.Held
			merged.Confidence = existing.Confidence
			merged.AutoFiled = existing.AutoFiled
			merged.IsComposite = existing.IsComposite || rc.IsComposite
			// ⚠ Play counters are PRESERVED, not re-derived: a scan knows nothing about what
			// aired. Belt and braces with UpsertClip's ON CONFLICT list, which also omits them
			// — either one alone would be enough, but a future edit to either that forgot the
			// other would silently reset every counter, and "usage never goes up" is a bug
			// nobody reports.
			merged.PlayCount = existing.PlayCount
			merged.LastPlayedAt = existing.LastPlayedAt
			if existing.TunarrProgramID != "" && rc.TunarrProgramID == "" {
				// Keep a known uuid when THIS scan could not see Tunarr (it is offline, or
				// this install has none). Losing it would silently strip filler from a
				// Tunarr-backed channel on the next reconcile.
				merged.TunarrProgramID = existing.TunarrProgramID
			}
			if serverFieldsUnchanged(existing.Clip, merged.Clip) {
				continue // idempotent: nothing changed, skip the write
			}
			res.Updated++
		} else {
			// New clip: seed era from the filename hint; leave audience/category
			// untagged for AI/manual tagging.
			merged.Era = rc.Era
			merged.Source = strings.TrimSpace(rc.Source)
			if merged.Source == "" {
				merged.Source = "filler-dir"
			}
			// ⚠ The lifecycle fork (§10 V38), and it is decided ONLY for a clip this scan has
			// never seen — an existing clip's `Held` is preserved above, so re-scanning can
			// never re-hold something a human already filed.
			//
			// Ingest downloads into this same folder, so at catalogue time a downloaded file and
			// a hand-copied one are both just files on disk. The sidecar's `fetchedBy` field is
			// what tells them apart: Loomarr FETCHED this ⇒ HOLD it for review; a person put it
			// here ⇒ file it on sight. Holding a hand-copied clip would mean a file you placed
			// yourself sits invisible until you approve it, which is the ceremony §7 warns
			// teaches people to click through gates.
			//
			// ⚠ V38c moved this from "a sidecar EXISTS" to the field. Existence stopped working
			// the moment Loomarr began writing tags back for hand-dropped clips too — every
			// tagged clip would have looked downloaded, and would have started being held.
			merged.Held = acquired || s.wasFetchedByUs(rc.Path)
			res.Added++
		}
		if rc.LineageInvalid || rc.ConditioningHold {
			merged.Held = true
		}
		if err := s.store.UpsertClip(ctx, merged); err != nil {
			return res, fmt.Errorf("upsert clip %s: %w", rc.ID, err)
		}
		if acquired && artifact.State != ArtifactConsumed {
			artifact.State = ArtifactConsumed
			artifact.MediaPath = rc.Path
			artifact.SidecarPath = strings.TrimSuffix(rc.Path, filepath.Ext(rc.Path)) + ".info.json"
			artifact.ClipHash = rc.ID
			artifact.RepairReason = ""
			artifact.UpdatedAt = s.now().UTC()
			if err := s.acquisitions.UpsertAcquisitionArtifacts(ctx, []AcquisitionArtifact{artifact}); err != nil {
				return res, fmt.Errorf("consume acquisition artifact %s: %w", artifact.ID, err)
			}
		}
	}

	pruned, err := s.store.DeleteClipsNotIn(ctx, keep)
	if err != nil {
		return res, fmt.Errorf("prune clips: %w", err)
	}
	res.Pruned = pruned
	if s.log != nil {
		s.log.Info("filler catalog synced", "total", res.Total, "added", res.Added, "updated", res.Updated, "repaired", res.Repaired, "pruned", res.Pruned)
	}
	return res, nil
}

func (s *Syncer) bindAcquisitionArtifact(ctx context.Context) func(sourcePath, destinationPath, previousPath, filedPath, clipHash string) error {
	if s.acquisitions == nil {
		return nil
	}
	return func(sourcePath, destinationPath, previousPath, filedPath, clipHash string) error {
		artifact, found, err := s.acquisitions.AcquisitionArtifactForClip(ctx, previousPath, clipHash)
		if err != nil || !found {
			return err
		}
		fail := func(reason string) error {
			artifact.State = ArtifactRepair
			artifact.RepairReason = reason
			artifact.UpdatedAt = s.now().UTC()
			if persistErr := s.acquisitions.UpsertAcquisitionArtifacts(ctx, []AcquisitionArtifact{artifact}); persistErr != nil {
				return fmt.Errorf("%s; record repair: %w", reason, persistErr)
			}
			return errors.New(reason)
		}
		if artifact.State == ArtifactRepair {
			return errors.New(artifact.RepairReason)
		}
		verify := func(path, observedHash string) error {
			info, statErr := os.Lstat(path)
			if statErr != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
				return errors.New("manifested media is missing, symlinked, or not a regular file")
			}
			digest, size, digestErr := FileSHA256(path)
			if digestErr != nil {
				return errors.New("manifested media cannot be hashed: " + digestErr.Error())
			}
			if observedHash == "" {
				observedHash, digestErr = ClipID(path)
				if digestErr != nil {
					return errors.New("manifested media cannot be identified: " + digestErr.Error())
				}
			}
			if digest != artifact.MediaSHA256 || size != artifact.MediaBytes || observedHash != artifact.ClipHash {
				return errors.New("manifested media bytes do not match the recorded digest, size, and clip identity")
			}
			return nil
		}
		if verifyErr := verify(sourcePath, clipHash); verifyErr != nil {
			return fail(verifyErr.Error())
		}
		if _, statErr := os.Lstat(destinationPath); statErr == nil {
			if verifyErr := verify(destinationPath, ""); verifyErr != nil {
				return fail(verifyErr.Error())
			}
		} else if !os.IsNotExist(statErr) {
			return fmt.Errorf("inspect manifested destination: %w", statErr)
		}
		if artifact.MediaPath == filedPath {
			return nil
		}
		artifact.MediaPath = filedPath
		artifact.UpdatedAt = s.now().UTC()
		if err := s.acquisitions.UpsertAcquisitionArtifacts(ctx, []AcquisitionArtifact{artifact}); err != nil {
			return fmt.Errorf("bind manifested watch arrival: %w", err)
		}
		return nil
	}
}

func (s *Syncer) authorizeAcquisition(ctx context.Context, rc RawClip) (AcquisitionArtifact, bool, error) {
	if s.acquisitions == nil {
		return AcquisitionArtifact{}, false, nil
	}
	artifact, found, err := s.acquisitions.AcquisitionArtifactForClip(ctx, rc.Path, rc.ID)
	if err != nil || !found {
		return artifact, found, err
	}
	fail := func(reason string) (AcquisitionArtifact, bool, error) {
		artifact.State = ArtifactRepair
		artifact.RepairReason = reason
		artifact.UpdatedAt = s.now().UTC()
		if persistErr := s.acquisitions.UpsertAcquisitionArtifacts(ctx, []AcquisitionArtifact{artifact}); persistErr != nil {
			return artifact, true, fmt.Errorf("%s; record repair: %w", reason, persistErr)
		}
		return artifact, true, errors.New(reason)
	}
	if artifact.State == ArtifactRepair {
		return artifact, true, errors.New(artifact.RepairReason)
	}
	path := filepath.Join(s.dir, filepath.FromSlash(rc.Path))
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fail("manifested media is missing, symlinked, or not a regular file")
	}
	digest, size, err := FileSHA256(path)
	if err != nil {
		return fail("manifested media cannot be hashed: " + err.Error())
	}
	if digest != artifact.MediaSHA256 || size != artifact.MediaBytes || rc.ID != artifact.ClipHash {
		return fail("manifested media bytes do not match the recorded digest, size, and clip identity")
	}
	tags, state := ReadSidecarTagsState(path)
	if state == SidecarInvalid {
		return fail("manifested media has malformed portable provenance")
	}
	if state == SidecarAbsent || tags.SourceID != artifact.SourceID || tags.AcquisitionID != artifact.AcquisitionID || !SidecarFetchedByUs(path) {
		if err := WriteSidecarTags(path, SidecarTags{
			SourceID: artifact.SourceID, AcquisitionID: artifact.AcquisitionID,
		}, true); err != nil {
			return fail("manifested media portable provenance cannot be repaired: " + err.Error())
		}
	}
	return artifact, true, nil
}

// repairOpaqueDisplayName removes implementation identifiers from the user-facing title even when
// the media is already at its canonical path. Both the former 40-character digest and the current
// 64-character content id are recognised; arbitrary hex-like operator names are left alone.
func (s *Syncer) repairOpaqueDisplayName(rc RawClip, existing StoreClip, found bool) (RawClip, bool, error) {
	if !isHashDisplayName(rc.Name) {
		return rc, false, nil
	}
	grounded := Clip{Kind: rc.Kind}
	if found {
		grounded = existing.Clip
	}
	rc.Name = groundedRepairName(grounded)
	if rc.SidecarInvalid {
		return rc, true, nil
	}
	full := filepath.Join(s.dir, filepath.FromSlash(rc.Path))
	tags, _ := ReadSidecarTags(full)
	if tags.OriginalName == "" || isHashDisplayName(tags.OriginalName) {
		tags.OriginalName = rc.Name
		if found {
			tags.Kind = string(existing.Kind)
			tags.Era = existing.Era
			tags.Audience = string(existing.Audience)
			tags.Category = existing.Category
			tags.Brand = existing.Brand
			tags.Transcript = existing.Transcript
			tags.Confidence = existing.Confidence
			tags.SuggestedEra = existing.SuggestedEra
		}
		if err := WriteSidecarTags(full, tags, false); err != nil {
			return rc, false, err
		}
	}
	return rc, true, nil
}

// repairLegacyContentPath recognises one old Loomarr state: a full hash-shaped filename whose
// stem does not match the full hash-shaped identity computed from the bytes. Arbitrary operator
// filenames are deliberately untouched. The canonical media and sidecar are published without
// replacement before the stale links are removed, so interruption can only leave a duplicate.
func (s *Syncer) repairLegacyContentPath(rc RawClip, existing StoreClip, found bool) (RawClip, bool, error) {
	stem := strings.TrimSuffix(filepath.Base(filepath.FromSlash(rc.Path)), filepath.Ext(rc.Path))
	if !isContentHash(rc.ID) || !isOpaqueHash(stem) || stem == rc.ID {
		return rc, false, nil
	}
	ext := filepath.Ext(rc.Path)
	canonical := filepath.ToSlash(ClipRelPath(rc.ID, ext))
	if rc.Path == canonical {
		return rc, false, nil
	}
	if rc.SidecarInvalid {
		return rc, false, nil
	}
	oldFull := filepath.Join(s.dir, filepath.FromSlash(rc.Path))
	newFull := filepath.Join(s.dir, filepath.FromSlash(canonical))

	// If the durable row still has a useful title, put it beside the bytes before moving. This is
	// the last automatic chance to prevent the next scan from replacing it with the stale hash.
	repairName := ""
	if found {
		repairName = existing.Name
		if isHashDisplayName(repairName) {
			repairName = groundedRepairName(existing.Clip)
		}
	}
	if repairName != "" {
		tags, _ := ReadSidecarTags(oldFull)
		if tags.OriginalName == "" || isHashDisplayName(tags.OriginalName) {
			tags.OriginalName = repairName
			tags.Kind = string(existing.Kind)
			tags.Era = existing.Era
			tags.Audience = string(existing.Audience)
			tags.Category = existing.Category
			tags.Brand = existing.Brand
			tags.Transcript = existing.Transcript
			tags.Confidence = existing.Confidence
			tags.SuggestedEra = existing.SuggestedEra
			if err := WriteSidecarTags(oldFull, tags, false); err != nil {
				return rc, false, err
			}
			rc.Name = repairName
		}
	}
	if err := os.MkdirAll(filepath.Dir(newFull), 0o755); err != nil {
		return rc, false, err
	}
	targetExists := false
	if _, err := os.Stat(newFull); err == nil {
		id, hashErr := ClipID(newFull)
		if hashErr != nil || id != rc.ID {
			return rc, false, fmt.Errorf("canonical target exists with different content")
		}
		targetExists = true
	} else if os.IsNotExist(err) {
	} else {
		return rc, false, err
	}

	oldSidecar, newSidecar := sidecarPathFor(oldFull), sidecarPathFor(newFull)
	if _, err := os.Stat(oldSidecar); err == nil {
		if _, targetErr := os.Stat(newSidecar); os.IsNotExist(targetErr) {
			if err := os.Link(oldSidecar, newSidecar); err != nil {
				return rc, false, err
			}
		} else if targetErr == nil {
			// A prior interrupted repair may already have the canonical media/sidecar. Merge our
			// namespaced facts rather than choosing one file and silently losing the other.
			if oldTags, ok := ReadSidecarTags(oldFull); ok {
				if err := WriteSidecarTags(newFull, oldTags, false); err != nil {
					return rc, false, err
				}
			}
		} else if targetErr != nil {
			return rc, false, targetErr
		}
	} else if !os.IsNotExist(err) {
		return rc, false, err
	}
	// Sidecar first: without media the canonical sidecar is invisible to the scan. Publishing the
	// media second prevents even a concurrent scan from observing a hash title between the two.
	if !targetExists {
		if err := os.Link(oldFull, newFull); err != nil {
			return rc, false, err
		}
	}

	if err := os.Remove(oldFull); err != nil && !os.IsNotExist(err) {
		return rc, false, err
	}
	_ = os.Remove(oldSidecar)
	rc.Path = canonical
	return rc, true, nil
}

// groundedRepairName replaces an opaque title from facts a person or grounded classifier supplied.
// When there are none, it uses a neutral kind label rather than exposing an implementation id as
// if that were a title.
func groundedRepairName(c Clip) string {
	base := strings.TrimSpace(c.Brand)
	if base == "" && c.Category != "" {
		base = strings.ReplaceAll(c.Category, "_", " ")
		if base != "" {
			base = strings.ToUpper(base[:1]) + base[1:] + " commercial"
		}
	}
	if base == "" {
		if c.Era > 0 && c.Kind != "" {
			return fmt.Sprintf("%d %s", c.Era, strings.ReplaceAll(string(c.Kind), "_", " "))
		}
		kind := strings.TrimSpace(strings.ReplaceAll(string(c.Kind), "_", " "))
		if kind == "" {
			kind = "filler clip"
		}
		return "Untitled " + kind
	}
	if c.Era > 0 {
		return fmt.Sprintf("%s — %d", base, c.Era)
	}
	return base
}

func isHashDisplayName(value string) bool {
	base := filepath.Base(value)
	return isOpaqueHash(strings.TrimSuffix(base, filepath.Ext(base)))
}

func isContentHash(value string) bool {
	return len(value) == 64 && isLowerHex(value)
}

func isOpaqueHash(value string) bool {
	return (len(value) == 40 || len(value) == 64) && isLowerHex(value)
}

func isLowerHex(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return false
		}
	}
	return true
}

// serverFieldsUnchanged reports whether the SCAN-owned fields match (so a re-sync is a no-op
// write). Tags aren't compared — they're loomarr-owned and preserved, not synced.
//
// ⚠ **Every scan-owned field must be listed here, or it can never reach the database.** This
// gates the write: a field the scan freshly computed but that this function ignores makes the
// sync decide "nothing changed" and skip the row entirely, so the merge assigning it runs and is
// then thrown away.
//
// Found live (V39): artwork rendered to disk for all 13 clips, `merged.Preview = rc.Preview` ran,
// and the column stayed empty on every row — sync reported `updated: 0` because Name, DurationMs
// and Kind were identical. `Thumbnail` had the same latent bug and had simply never been exercised,
// because a clip's still was always generated on the same pass that first inserted it.
//
// The rule for anything added later: if `syncClips` assigns it from `rc`, compare it here.
func serverFieldsUnchanged(a, b Clip) bool {
	return a.Path == b.Path && a.TunarrProgramID == b.TunarrProgramID &&
		a.Name == b.Name && a.DurationMs == b.DurationMs && a.Kind == b.Kind &&
		a.Quality == b.Quality && a.License == b.License &&
		a.Thumbnail == b.Thumbnail && a.Preview == b.Preview
}
