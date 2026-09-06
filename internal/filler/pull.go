package filler

import "time"

// A PULL is filler acquisition's approval gate (§10 V35).
//
// §10 has stated the principle since the starter pack shipped — *"the machine proposes, a human
// commits"* — with nothing to hang it on. A pull is that object: a plan Loomarr composed across
// sources, persisted, shown in the approval queue beside title proposals, and **downloading
// nothing until an operator approves it**.
//
// ⚠ **What the gate binds is composed plans, not an admin's own hands.** An admin searching one
// source and queueing one clip stays direct — the §7 shape, where an admin may `POST /v1/titles`
// because the admin *is* the gate. Requiring a proposal for a single deliberate click would make
// the gate ceremony, and ceremony is what teaches people to click through it. What the gate
// exists for is what happens when nobody is looking, which is exactly what a pull is.

// PullStatus is where a pull sits in the gate.
type PullStatus string

const (
	// PullPending is awaiting a human. Nothing has been enqueued.
	PullPending PullStatus = "pending"
	// PullApproved means an operator committed it and the work was enqueued.
	PullApproved PullStatus = "approved"
	// PullDismissed means an operator declined it. ⚠ Kept, not deleted — see Pull.
	PullDismissed PullStatus = "dismissed"
)

// PullPlanRow is one exact provider item the pull would acquire.
type PullPlanRow struct {
	// SourceID is the registered collection this row fetches from. A row whose source has
	// since been removed or switched off is refused at approval rather than skipped.
	SourceID string `json:"sourceId"`
	Provider string `json:"provider,omitempty"`
	RemoteID string `json:"remoteId,omitempty"`
	// URL is the exact item payload approved for ingest. Collection URLs exist only on legacy
	// pre-V66 pulls and are resolved by the approval compatibility path.
	URL          string    `json:"url,omitempty"`
	License      string    `json:"license,omitempty"`
	ObservedYear int       `json:"observedYear,omitempty"`
	PublishedAt  string    `json:"publishedAt,omitempty"`
	DurationMS   int       `json:"durationMs,omitempty"`
	Height       int       `json:"height,omitempty"`
	Geography    Geography `json:"geography,omitempty"`
	// Tag is a short label for the row's coloured chip ("1990s", "kids").
	Tag string `json:"tag"`
	// Name is the collection's operator-facing name.
	Name string `json:"name"`
	// Why is why THIS source is in the plan — the per-row half of the pull's reason.
	Why string `json:"why"`
	// EstimateClips is how many clips this row is expected to bring in.
	//
	// ⚠ An ESTIMATE, and it must never be rendered as a promise. What a source actually yields
	// depends on what is still there, what deduplicates against the catalog, and what the
	// splitter makes of a compilation. A number presented as exact here becomes "Loomarr said
	// 40 and downloaded 12" — a bug report about a forecast.
	EstimateClips int `json:"estimateClips"`
	// Dropped records that the operator excluded this row before approving.
	//
	// ⚠ Retained with a flag rather than removed from the slice, and that is a property of the
	// gate rather than bookkeeping: the record must show what was PROPOSED as well as what was
	// AGREED TO, or "we approved this" loses the half that matters.
	Dropped bool `json:"dropped"`
}

func (r PullPlanRow) Identity() RemoteIdentity {
	return RemoteIdentity{Provider: r.Provider, SourceID: r.SourceID, RemoteID: r.RemoteID}
}

func (r PullPlanRow) CandidateID() string {
	if r.Identity().Validate() != nil {
		return ""
	}
	return r.Identity().Token()
}

// Pull is a proposed acquisition across filler sources, awaiting a human.
type Pull struct {
	ID    string
	Title string
	// Reason is the gap this pull is trying to close, rendered above the plan. "Approve this"
	// without a reason is a button, not a decision.
	Reason string
	// ProposedBy is who or what composed it — a user id today, a job name when a schedule does.
	ProposedBy string
	Status     PullStatus
	// Note is the operator's narrowing instruction, captured at approval.
	Note string
	// Intent and Rejected were added in V66. Their zero values preserve readability of historical
	// V35 source-level pull JSON without pretending those records had candidate evidence.
	Intent   AcquisitionIntent           `json:"intent,omitempty"`
	Plan     []PullPlanRow               `json:"plan"`
	Rejected []AcquisitionDecision       `json:"rejected,omitempty"`
	Sources  []AcquisitionSourceDecision `json:"sources,omitempty"`

	CreatedAt time.Time
	// DecidedAt is zero while pending.
	DecidedAt time.Time
	DecidedBy string
}

// Committed returns the plan rows an approval would actually fetch — everything the operator
// did not drop.
//
// The one place "what did they agree to" is computed, so no caller re-derives it from the flag
// and gets the polarity backwards. A pull with every row dropped commits nothing, which the
// approval path refuses rather than treating as success.
func (p Pull) Committed() []PullPlanRow {
	out := make([]PullPlanRow, 0, len(p.Plan))
	for _, r := range p.Plan {
		if !r.Dropped {
			out = append(out, r)
		}
	}
	return out
}

// EstimatedClips totals the estimate across committed rows, for the pull's summary line.
func (p Pull) EstimatedClips() int {
	n := 0
	for _, r := range p.Committed() {
		n += r.EstimateClips
	}
	return n
}
