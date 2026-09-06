package filler

// ReadinessAction is the highest-impact operator action for the filler system. The server owns
// this ordering so every client presents the same diagnosis instead of reverse-engineering it
// from counters.
type ReadinessAction string

const (
	ReadinessNone             ReadinessAction = "none"
	ReadinessEnableFetch      ReadinessAction = "enable_fetch"
	ReadinessFreeCatalog      ReadinessAction = "free_catalog_capacity"
	ReadinessFreeDisk         ReadinessAction = "free_disk_capacity"
	ReadinessRetryAcquisition ReadinessAction = "retry_acquisition"
	ReadinessRetryWork        ReadinessAction = "retry_failed_work"
	ReadinessReviewIncoming   ReadinessAction = "review_incoming"
	ReadinessAddFiller        ReadinessAction = "add_filler"
	ReadinessImproveCoverage  ReadinessAction = "improve_channel_coverage"
)

// ReadinessInput contains only authoritative domain projections. No raw storage status reaches
// this classifier: pipeline ownership, fetch limits, and channel matching have already crossed
// their respective single sources of truth.
type ReadinessInput struct {
	Fetch    FetchStatus
	Pipeline PipelineOverview
	Pool     PoolReport
	Runs     []AcquisitionRun
}

// Readiness is the server-owned summary shown at the simple Filler entry point. The detailed
// views retain every count; this projection answers what matters now and where to go next.
type Readiness struct {
	Ready bool
	Next  ReadinessAction

	ChannelID string
	Count     int

	Fetch    FetchStatus
	Pipeline PipelineOverview
	Pool     PoolReport
	Runs     []AcquisitionRun
}

// ProjectReadiness chooses one next action in impact order. Machine blockage precedes operator
// decisions, and an empty airable pool precedes quality improvements to a pool that already works.
func ProjectReadiness(in ReadinessInput) Readiness {
	out := Readiness{Fetch: in.Fetch, Pipeline: in.Pipeline, Pool: in.Pool, Runs: in.Runs}
	switch {
	case !in.Fetch.Enabled:
		out.Next = ReadinessEnableFetch
	case in.Fetch.StoppedBy == "catalog":
		out.Next, out.Count = ReadinessFreeCatalog, in.Fetch.CatalogClips
	case in.Fetch.StoppedBy == "disk":
		out.Next = ReadinessFreeDisk
	case len(in.Runs) > 0 && (in.Runs[0].Status == AcquisitionError || in.Runs[0].Artifacts.Repair > 0):
		out.Next, out.Count = ReadinessRetryAcquisition, in.Runs[0].Failed+in.Runs[0].Artifacts.Repair
	case in.Pipeline.Recoverable > 0:
		out.Next, out.Count = ReadinessRetryWork, in.Pipeline.Recoverable
	case in.Pipeline.NeedsDecision > 0:
		out.Next, out.Count = ReadinessReviewIncoming, in.Pipeline.NeedsDecision
	case in.Pool.Eligible == 0:
		out.Next = ReadinessAddFiller
	default:
		if weakest := in.Pool.Weakest(); weakest != nil && weakest.Report.Level != MatchExact {
			out.Next, out.ChannelID = ReadinessImproveCoverage, weakest.ChannelID
			out.Count = weakest.Report.Total
		} else {
			out.Ready, out.Next = true, ReadinessNone
		}
	}
	return out
}
