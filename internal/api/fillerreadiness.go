package api

import (
	"context"
	"time"

	"github.com/loomarr/loomarr/internal/filler"
)

type AcquisitionOutcomeDTO struct {
	Enrolled      int `json:"enrolled"`
	Preparing     int `json:"preparing"`
	NeedsDecision int `json:"needsDecision"`
	Admitted      int `json:"admitted"`
	Rejected      int `json:"rejected"`
	Dismissed     int `json:"dismissed"`
}

type AcquisitionArtifactOutcomeDTO struct {
	Staged       int    `json:"staged"`
	Published    int    `json:"published"`
	Consumed     int    `json:"consumed"`
	Repair       int    `json:"repair"`
	RepairReason string `json:"repairReason,omitempty"`
}

type FillerAcquisitionRunDTO struct {
	ID       string `json:"id"`
	Trigger  string `json:"trigger" enum:"scheduled,source,pull,manual"`
	SourceID string `json:"sourceId,omitempty"`
	PullID   string `json:"pullId,omitempty"`
	Status   string `json:"status" enum:"queued,running,success,error"`

	Requested int    `json:"requested"`
	Fetched   int    `json:"fetched"`
	Skipped   int    `json:"skipped"`
	Failed    int    `json:"failed"`
	Empty     int    `json:"empty"`
	Error     string `json:"error,omitempty"`

	StartedAt   string                        `json:"startedAt" format:"date-time"`
	CompletedAt string                        `json:"completedAt,omitempty" format:"date-time"`
	UpdatedAt   string                        `json:"updatedAt" format:"date-time"`
	Outcome     AcquisitionOutcomeDTO         `json:"outcome"`
	Artifacts   AcquisitionArtifactOutcomeDTO `json:"artifacts"`
}

type FillerReadinessDTO struct {
	Ready       bool   `json:"ready"`
	NextAction  string `json:"nextAction" enum:"none,enable_fetch,free_catalog_capacity,free_disk_capacity,retry_acquisition,retry_failed_work,review_incoming,add_filler,improve_channel_coverage"`
	ChannelID   string `json:"channelId,omitempty"`
	ActionCount int    `json:"actionCount,omitempty"`

	Fetch        FillerFetchStatusDTO      `json:"fetch"`
	Pipeline     PipelineOverviewDTO       `json:"pipeline"`
	Pool         PoolDTO                   `json:"pool"`
	Acquisitions []FillerAcquisitionRunDTO `json:"acquisitions"`
}

type fillerReadinessOutput struct {
	Body FillerReadinessDTO
}

func (s *Server) fillerReadiness(ctx context.Context, _ *struct{}) (*fillerReadinessOutput, error) {
	if s.filler == nil {
		return nil, errNotImplemented("Filler isn't set up", "Set up commercials and filler before checking readiness.")
	}
	readiness, err := s.filler.Readiness(ctx)
	if err != nil {
		return nil, err
	}
	return &fillerReadinessOutput{Body: fillerReadinessDTO(readiness)}, nil
}

func fillerReadinessDTO(readiness filler.Readiness) FillerReadinessDTO {
	runs := make([]FillerAcquisitionRunDTO, 0, len(readiness.Runs))
	for _, run := range readiness.Runs {
		runs = append(runs, acquisitionRunDTO(run))
	}
	return FillerReadinessDTO{
		Ready: readiness.Ready, NextAction: string(readiness.Next),
		ChannelID: readiness.ChannelID, ActionCount: readiness.Count,
		Fetch: FillerFetchStatusDTO{
			Enabled: readiness.Fetch.Enabled, StoppedBy: readiness.Fetch.StoppedBy,
			CatalogClips: readiness.Fetch.CatalogClips, MaxCatalog: readiness.Fetch.MaxCatalog,
			DiskBytes: readiness.Fetch.DiskBytes, MaxDiskBytes: readiness.Fetch.MaxDiskBytes,
		},
		Pipeline: pipelineOverviewDTO(readiness.Pipeline), Pool: poolDTO(readiness.Pool),
		Acquisitions: runs,
	}
}

func acquisitionRunDTO(run filler.AcquisitionRun) FillerAcquisitionRunDTO {
	return FillerAcquisitionRunDTO{
		ID: run.ID, Trigger: string(run.Trigger), SourceID: run.SourceID, PullID: run.PullID,
		Status: string(run.Status), Requested: run.Requested, Fetched: run.Fetched,
		Skipped: run.Skipped, Failed: run.Failed, Empty: run.Empty, Error: run.Error,
		StartedAt: formatAcquisitionTime(run.StartedAt), CompletedAt: formatAcquisitionTime(run.CompletedAt),
		UpdatedAt: formatAcquisitionTime(run.UpdatedAt),
		Outcome: AcquisitionOutcomeDTO{
			Enrolled: run.Outcome.Enrolled, Preparing: run.Outcome.Preparing,
			NeedsDecision: run.Outcome.NeedsDecision, Admitted: run.Outcome.Admitted,
			Rejected: run.Outcome.Rejected, Dismissed: run.Outcome.Dismissed,
		},
		Artifacts: AcquisitionArtifactOutcomeDTO{
			Staged: run.Artifacts.Staged, Published: run.Artifacts.Published,
			Consumed: run.Artifacts.Consumed, Repair: run.Artifacts.Repair,
			RepairReason: run.Artifacts.RepairReason,
		},
	}
}

func formatAcquisitionTime(at time.Time) string {
	if at.IsZero() {
		return ""
	}
	return at.UTC().Format(time.RFC3339)
}
