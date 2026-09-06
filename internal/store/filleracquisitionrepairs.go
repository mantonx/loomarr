package store

import (
	"context"
	"fmt"

	"github.com/loomarr/loomarr/internal/filler"
)

// AcquisitionRepairSummary projects durable unresolved repair rows for readiness. The newest
// reason is ordered by the same stable UpdatedAt/ID key used for artifact projections.
func (s *sqlStore) AcquisitionRepairSummary(ctx context.Context) (filler.AcquisitionRepairSummary, error) {
	const query = `SELECT COUNT(*), COALESCE((
		SELECT substr(repair_reason, 1, 512) FROM filler_acquisition_artifacts
		WHERE state = ? ORDER BY updated_at DESC, id DESC LIMIT 1
	), '') FROM filler_acquisition_artifacts WHERE state = ?`
	var summary filler.AcquisitionRepairSummary
	if err := s.db.QueryRowContext(ctx, s.ph(query), string(filler.ArtifactRepair), string(filler.ArtifactRepair)).Scan(&summary.Count, &summary.LatestReason); err != nil {
		return filler.AcquisitionRepairSummary{}, fmt.Errorf("summarize filler acquisition repairs: %w", err)
	}
	return summary, nil
}
