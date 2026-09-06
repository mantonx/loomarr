package filler

import "time"

// AcquisitionTrigger says who started a filler download. It is intentionally about the
// initiating policy, not the transport: a scheduled source refresh and an operator's explicit
// fetch may both use the same downloader while remaining different operational stories.
type AcquisitionTrigger string

const (
	AcquisitionScheduled AcquisitionTrigger = "scheduled"
	AcquisitionSource    AcquisitionTrigger = "source"
	AcquisitionPull      AcquisitionTrigger = "pull"
	AcquisitionManual    AcquisitionTrigger = "manual"
)

// AcquisitionStatus is the durable execution state of one bounded download run.
type AcquisitionStatus string

const (
	AcquisitionQueued  AcquisitionStatus = "queued"
	AcquisitionRunning AcquisitionStatus = "running"
	AcquisitionSuccess AcquisitionStatus = "success"
	AcquisitionError   AcquisitionStatus = "error"
)

// AcquisitionRun connects a source/pull decision to downloader facts and the pipeline rows that
// resulted from it. Download counts and pipeline outcomes are deliberately separate: one fetched
// compilation may produce many clips, while a skipped remote item produces no pipeline row.
type AcquisitionRun struct {
	ID       string
	Trigger  AcquisitionTrigger
	SourceID string
	PullID   string
	Status   AcquisitionStatus

	Requested int
	Fetched   int
	Skipped   int
	Failed    int
	Empty     int
	Error     string

	StartedAt   time.Time
	CompletedAt time.Time
	UpdatedAt   time.Time

	Outcome   AcquisitionOutcome
	Artifacts AcquisitionArtifactOutcome
}

// AcquisitionTarget is one URL inside an approved acquisition plan. SourceID stays per-target
// because one approved pull may deliberately draw from several registered sources.
type AcquisitionTarget struct {
	SourceID string
	// Kind is the registered source provider. Empty is reserved for a one-off URL an admin typed,
	// where the ingest boundary must infer the downloader because no source policy exists.
	Kind string
	URL  string
}

// AcquisitionOutcome is the current lifecycle distribution of every clip enrolled from one run.
// Enrolled is the non-overlapping total; the remaining fields partition it by operator ownership.
type AcquisitionOutcome struct {
	Enrolled      int
	Preparing     int
	NeedsDecision int
	Admitted      int
	Rejected      int
	Dismissed     int
}

// AcquisitionArtifactOutcome is the bounded operator-facing projection of manifest state. Only
// the newest repair reason is retained; the durable rows remain the full audit.
type AcquisitionArtifactOutcome struct {
	Staged       int
	Published    int
	Consumed     int
	Repair       int
	RepairReason string
}

func AcquisitionArtifactOutcomeFrom(artifacts []AcquisitionArtifact) AcquisitionArtifactOutcome {
	var out AcquisitionArtifactOutcome
	for _, artifact := range artifacts {
		switch artifact.State {
		case ArtifactStaged:
			out.Staged++
		case ArtifactPublished:
			out.Published++
		case ArtifactConsumed:
			out.Consumed++
		case ArtifactRepair:
			out.Repair++
			if out.RepairReason == "" {
				out.RepairReason = artifact.RepairReason
			}
		}
	}
	return out
}

// AcquisitionOutcomeFrom projects pipeline rows through the same Lifecycle classifier used by
// Incoming and run telemetry. This is the only ownership mapping for acquisition results.
func AcquisitionOutcomeFrom(rows []ClipPipeline, at time.Time) AcquisitionOutcome {
	var out AcquisitionOutcome
	for _, row := range rows {
		out.Enrolled++
		switch row.Lifecycle(at).State {
		case LifecycleRunnable, LifecycleInProgress, LifecycleScheduled:
			out.Preparing++
		case LifecycleNeedsDecision:
			out.NeedsDecision++
		case LifecycleAdmitted:
			out.Admitted++
		case LifecycleRejected:
			out.Rejected++
		case LifecycleDismissed:
			out.Dismissed++
		}
	}
	return out
}
