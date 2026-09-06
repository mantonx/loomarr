package filler

// AcquisitionRepairSummary is the bounded readiness projection of durable repair artifacts.
// It is deliberately independent of the paged acquisition history: repairs remain actionable
// until their manifest rows leave ArtifactRepair.
type AcquisitionRepairSummary struct {
	Count        int
	LatestReason string
}
