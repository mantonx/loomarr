package filler

import (
	"context"
	"time"
)

// The per-clip ingest pipeline's STATE (§10 V51b) — what stage a clip is at, how that stage is
// going, and what the clip's outcome was.
//
// ⚠ This lives in a table beside `clips`, never on it. `clips` is a synced cache of the
// drop-folder that has been dropped and recreated twice; this records that ~341s of Whisper and a
// paid vision call have ALREADY been spent, which is exactly the kind of fact a cache rebuild must
// not silently discard. Migration 00044 carries the full argument.

// StageID names one rung of the pipeline.
type StageID string

const (
	StageProbe      StageID = "probe"
	StageTranscode  StageID = "transcode"
	StageSplit      StageID = "split"
	StageScreen     StageID = "screen"
	StageLanguage   StageID = "language"
	StageTranscribe StageID = "transcribe"
	StageTag        StageID = "tag"
	StageVision     StageID = "vision"
	StageAdmission  StageID = "admission"
	StageScore      StageID = "score"
)

// StageOrder is the pipeline, in order. It is the ONE definition of the sequence: the runner
// advances through it, the ladder is rendered from it, and `Rewind` resets a suffix of it.
//
// ⚠ Order is not arbitrary. `probe` measures the file everything else reasons about; `transcode`
// rewrites bytes so it must precede anything that hashes or reads frames; `split` spawns new
// clips, so it runs before the per-clip metadata rungs that those children will each need;
// `screen` evaluates only those children after their final transcode and before enrichment;
// `transcribe` must precede `tag` because the transcript is one of the text signals `tag` grounds
// against (running them the other way round is what the cron schedule did, and why a clip could
// be scored low against a transcript that arrived ten minutes later); `admission` durably records
// the V61 shadow before `score`, and `score` remains last while it owns V38 compatibility filing.
var StageOrder = []StageID{
	StageProbe, StageTranscode, StageSplit, StageScreen, StageLanguage,
	StageTranscribe, StageTag, StageVision, StageAdmission, StageScore,
}

// StageIndex returns a stage's position in StageOrder, or -1 when it is not a stage.
//
// ⚠ Returns -1 rather than 0 for an unknown id. 0 is a real, meaningful position (probe), so a
// zero-value fallback would silently rewind an unknown stage to the very beginning of the
// pipeline — re-running a transcode and every expensive rung after it.
func StageIndex(id StageID) int {
	for i, s := range StageOrder {
		if s == id {
			return i
		}
	}
	return -1
}

// SegmentScreeningCompleted reports that a child advanced beyond the screening rung through its
// ordinary success path. A StatusDone record alone is insufficient because review verdicts also
// close their current rung before waiting on a person; requiring the row to be later in the ladder
// prevents a manual filing action from turning an unresolved screen into release authority.
func SegmentScreeningCompleted(row ClipPipeline) bool {
	if StageIndex(row.Stage) <= StageIndex(StageScreen) {
		return false
	}
	for _, record := range row.Stages {
		if record.Stage == StageScreen && record.Status == StatusDone {
			return true
		}
	}
	return false
}

// StageStatus is how the CURRENT stage is going.
type StageStatus string

const (
	StatusQueued  StageStatus = "queued"
	StatusRunning StageStatus = "running"
	StatusDone    StageStatus = "done"
	StatusFailed  StageStatus = "failed"
	// StatusSkipped means the stage does not APPLY to this clip in this install — vision with
	// `filler.vision.enabled` off, transcription for a clip whose description already says enough.
	//
	// ⚠ Distinct from `done`, and the difference is re-evaluation: a skipped stage is asked again
	// on every pass, so turning a setting on picks up clips that already went past it. A `done`
	// stage is never revisited. Collapsing the two would mean an operator enabling vision sees it
	// apply only to clips that arrive afterwards.
	StatusSkipped StageStatus = "skipped"
)

// Disposition is the CLIP-level outcome — what the operator sees.
type Disposition string

const (
	DispositionRunning  Disposition = "running"
	DispositionReview   Disposition = "review"
	DispositionFiled    Disposition = "filed"
	DispositionRejected Disposition = "rejected"
	// DispositionDismissed — the OPERATOR said no (§10 V54). Distinct from `rejected`, which is
	// the quality gate refusing.
	//
	// ⚠ The two are not the same fact and must not share a value. `rejected` carries a stable
	// `RejectReason` code plus the measured detail behind it, and whether it may be undone is
	// decided per reason by `Soft()`. A dismissal has no code — the reason is "a person said so" —
	// no measurement, and is always reversible. Folding it into `rejected` would need a reason
	// meaning "no reason" and a `Soft()` case that is unconditionally true: two exceptions so one
	// enumeration could carry two subjects. `reject.go` makes the same argument for keeping
	// `RejectReason` and `AutoSplitReject` apart.
	//
	// ⚠ It is deliberately absent from the refusals list ("Loomarr didn't use N clips"), which is
	// the audit of what the appliance decided WITHOUT the operator.
	DispositionDismissed Disposition = "dismissed"
)

// Terminal reports whether the pipeline is finished with this clip. A terminal clip is not picked
// up by the work list again unless `Rewind` puts it back.
//
// ⚠ `review` is TERMINAL for the pipeline even though it is not terminal for the operator. The
// pipeline has done everything it can and is waiting on a person; leaving it in the work list
// would mean re-running the whole ladder against a clip whose only missing input is a human
// decision.
func (d Disposition) Terminal() bool {
	return d == DispositionFiled || d == DispositionRejected || d == DispositionReview ||
		d == DispositionDismissed
}

// StageRecord is one finished rung on a clip's ladder — what the Incoming tab renders as history.
type StageRecord struct {
	Stage  StageID     `json:"stage"`
	Status StageStatus `json:"status"`
	// Note is the operator-facing sentence for a skip or a failure ("the description already says
	// enough"). Empty for an ordinary success — a row that needs no explanation gets none.
	Note     string    `json:"note,omitempty"`
	Attempts int       `json:"attempts,omitempty"`
	At       time.Time `json:"at"`
}

// ClipPipeline is one clip's pipeline row.
type ClipPipeline struct {
	ClipHash string
	// AcquisitionID ties this row to the durable fetch/pull run that produced its bytes. Empty is
	// honest for operator-dropped and pre-V59 clips. It survives retries and rewinds unchanged.
	AcquisitionID string
	Stage         StageID
	Status        StageStatus
	Progress      int
	Disposition   Disposition
	// RejectReason is a stable code; RejectDetail is the measured fact behind it.
	RejectReason RejectReason
	RejectDetail string
	Attempts     int
	// ForceRun bypasses the current stage's ordinary Applies check. Rewind sets it because the
	// operator explicitly asked that rung to run again; step clears it before moving on. It is
	// durable so a restart between the click and the worker does not turn the request into a skip.
	ForceRun bool
	// NextRun is when this row is next eligible. Zero means "now".
	NextRun    time.Time
	Stages     []StageRecord
	EnrolledAt time.Time
	UpdatedAt  time.Time
}

// Record appends (or replaces) this stage's entry on the ladder, and moves the row's own Status
// to match when the rung being recorded is the CURRENT one.
//
// ⚠ Replaces in place when the stage is already present, so a retry does not append a second row
// for the same rung. The ladder is a picture of the pipeline, not a log of attempts — the attempt
// COUNT carries that, and a ladder that grew a row per retry would push the useful rungs off the
// operator's screen for exactly the clips that are struggling.
//
// ⚠ **Record is the ONLY writer of `Status` once a rung resolves, and that is the fix for a defect
// the whole suite was green over.** `Status` is the CURRENT rung's state; `Disposition` is the
// clip's. The verdict paths set `Disposition` and recorded the rung, but left `Status` at the
// `running` written on entry — so a clip handed to a person persisted as `split/running, 0%`
// while its own ladder entry said `done`. One row disagreeing with itself. Nothing FUNCTIONAL
// broke, which is why no test caught it: every store predicate keys on `disposition`, never on
// `status`. It broke only the picture — `ClipPipeline.resolve` on the frontend prefers
// `row.stage`/`row.status` over the visited ladder (correctly: a rung mid-run has no entry yet),
// so the pip pulsed "in progress" forever and the reject note was never shown. Measured live
// (§10 V51g): eight reels, one of them WAGA-5, all finished within seconds and all drawn as
// though still working. Keeping the two in step HERE means the next verdict path cannot
// reintroduce it by forgetting a line.
func (p *ClipPipeline) Record(stage StageID, status StageStatus, note string, attempts int, at time.Time) {
	if stage == p.Stage {
		p.Status = status
	}
	for i := range p.Stages {
		if p.Stages[i].Stage == stage {
			p.Stages[i] = StageRecord{Stage: stage, Status: status, Note: note, Attempts: attempts, At: at}
			return
		}
	}
	p.Stages = append(p.Stages, StageRecord{Stage: stage, Status: status, Note: note, Attempts: attempts, At: at})
}

// PipelineFilter narrows a pipeline listing.
type PipelineFilter struct {
	// ConveyorOnly returns everything on the Incoming belt: clips the machine is still working on
	// (`running`) AND clips it has finished with and handed to a person (`review`).
	//
	// ⚠ **It is deliberately BOTH, and the predecessor's name is why this changed.**
	// `NonTerminalOnly` (retired-ok) meant `running` alone, which forced Incoming to answer "who
	// needs a human?" from a second, older query over tag-shape — and the two disagreed: every clip
	// is enrolled at `probe/queued` AND held-and-untagged, so the same clip appeared in both lists,
	// one saying it needed a decision while the other said nothing needed the operator at all.
	// `review` is the pipeline's own word for the handoff, so one query now answers both halves.
	ConveyorOnly bool
	// RejectedOnly returns the audit half: what was refused, and why.
	RejectedOnly bool
	// Limit caps the rows returned; 0 means unbounded.
	Limit int
}

// PipelineStore is the slice of the store the pipeline needs (the sync.go/splitjob.go pattern —
// declared here so `filler` does not import `store`; `app` bridges them).
//
// ⚠ PipelineStore is the ONLY writer of this table, which is what keeps the state machine
// in one module. Every other filler column keeps its existing owner (SetClipLanguage,
// SetClipTranscript, ApplyClipVision, SetClipsHeld, …) — this phase adds no second writer to any
// of them, which is why the 1600-line store conformance suite needs no rework to accept it.
type PipelineStore interface {
	// ListPipelineWork returns non-terminal rows due at or before `now`, oldest first.
	ListPipelineWork(ctx context.Context, now time.Time, limit int) ([]ClipPipeline, error)
	// PipelineOverview returns one lifecycle-classified snapshot for API and run telemetry.
	PipelineOverview(ctx context.Context, at time.Time) (PipelineOverview, error)
	// ListClipsWithoutPipeline returns catalogued clips that have no pipeline row yet — the
	// lazy enrolment the pipeline self-heals with, instead of a data migration.
	ListClipsWithoutPipeline(ctx context.Context, limit int) ([]StoreClip, error)
	GetClipPipeline(ctx context.Context, hash string) (ClipPipeline, bool, error)
	ListClipPipelines(ctx context.Context, f PipelineFilter) ([]ClipPipeline, error)
	UpsertClipPipeline(ctx context.Context, p ClipPipeline) error
	// RetryClipPipeline atomically returns a failed row to the conveyor. When restore is true it
	// also clears the rejection tombstone while holding the clip.
	RetryClipPipeline(ctx context.Context, failed, retry ClipPipeline, restore bool) error
}
