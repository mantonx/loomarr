// Package fillerdecision owns the durable lifecycle and operator projections
// for filler-admission results. The pure evidence policy remains in
// filleradmission; this package records what it decided and what happened next.
package fillerdecision

import (
	"context"
	"errors"
	"time"

	"github.com/loomarr/loomarr/internal/filleradmission"
)

const (
	MaxPageSize    = 100
	MaxIDBytes     = 128
	MaxTextBytes   = 512
	MaxResultBytes = 256 << 10
)

type OutcomeKind string

const (
	OutcomeSemantic    OutcomeKind = "semantic"
	OutcomeOperational OutcomeKind = "operational"
)

type ApplicationMode string

const (
	ApplicationModeShadow  ApplicationMode = "shadow"
	ApplicationModeApplied ApplicationMode = "applied"
)

type Record struct {
	ID, ClipHash, EvidenceHash                      string
	EvidenceVersion, PolicyVersion, TaxonomyVersion string
	SchemaVersion                                   int
	ApplicationMode                                 ApplicationMode
	Result                                          filleradmission.Result
	CreatedAt                                       time.Time
}

func (r Record) Kind() OutcomeKind {
	if r.Result.Decision != nil && r.Result.Hold == nil {
		return OutcomeSemantic
	}
	if r.Result.Hold != nil && r.Result.Decision == nil {
		return OutcomeOperational
	}
	return ""
}

type ActionKind string

const (
	ActionAdmit   ActionKind = "admit"
	ActionReject  ActionKind = "reject"
	ActionCorrect ActionKind = "correct"
	ActionAbandon ActionKind = "abandon"
	ActionRestore ActionKind = "restore"
	ActionReverse ActionKind = "reverse"
)

type Action struct {
	ID, DecisionID, ActorID, Reason, Answer, SupersedesID string
	Kind                                                  ActionKind
	CorrectedVerdict                                      filleradmission.Verdict
	CreatedAt                                             time.Time
}

type Cursor struct {
	BeforeCreatedAt time.Time
	BeforeID        string
}

type DecisionFilter struct {
	Kind           OutcomeKind
	Verdict        filleradmission.Verdict
	CurrentOnly    bool
	UnresolvedOnly bool
	RetryableOnly  bool
	Cursor         Cursor
	Limit          int
}

type ActionFilter struct {
	DecisionID string
	Cursor     Cursor
	Limit      int
}

type DecisionPage struct {
	Rows  []Record
	Total int
}

type ActionPage struct {
	Rows  []Action
	Total int
}

type Counts struct {
	Admitted, Rejected, Reviews, Operational, Retryable, UnresolvedReviews int
}

// Repository is the narrow persistence port. Implementations must preserve
// canonical result bytes and validate action transitions transactionally.
type Repository interface {
	PutFillerDecision(context.Context, Record) error
	GetFillerDecision(context.Context, string) (Record, error)
	ListFillerDecisions(context.Context, DecisionFilter) (DecisionPage, error)
	FillerDecisionCounts(context.Context) (Counts, error)
	CommitFillerDecisionAction(context.Context, Action) error
	ListFillerDecisionActions(context.Context, ActionFilter) (ActionPage, error)
	ListFillerDecisionActivity(context.Context, Cursor, int) (ActivityPage, error)
}

var (
	ErrInvalid          = errors.New("filler decision: invalid")
	ErrConflict         = errors.New("filler decision: conflicting immutable record")
	ErrActionStale      = errors.New("filler decision: stale action")
	ErrActionNotAllowed = errors.New("filler decision: action not allowed")
)

type NextAction string

const (
	NextNone   NextAction = "none"
	NextRepair NextAction = "repair_processing"
	NextRetry  NextAction = "retry_processing"
	NextReview NextAction = "review_decisions"
)

type Overview struct {
	Healthy     bool
	Next        NextAction
	ActionCount int
	Counts      Counts
}

type ReviewItem struct {
	ID, ClipHash, Question string
	ApplicationMode        ApplicationMode
	ReasonCodes            []filleradmission.ReasonCode
	EvidenceRefs           []string
	Conflicts              []filleradmission.Conflict
	CreatedAt              time.Time
}

type ReviewPage struct {
	Rows  []ReviewItem
	Total int
}

type RecoveryAction string

const (
	RecoveryConfigureProvider RecoveryAction = "configure_provider"
	RecoveryAdjustBudget      RecoveryAction = "adjust_budget"
	RecoveryRetryExtraction   RecoveryAction = "retry_extraction"
	RecoveryInspectMedia      RecoveryAction = "inspect_media"
	RecoveryUpdatePolicy      RecoveryAction = "update_policy"
)

type DiagnosticItem struct {
	ID, ClipHash string
	Code         filleradmission.OperationalCode
	Retryable    bool
	Recovery     RecoveryAction
	CreatedAt    time.Time
}

type DiagnosticPage struct {
	Rows  []DiagnosticItem
	Total int
}

type ActivityKind string

const (
	ActivityAutomaticAdmit  ActivityKind = "automatic_admit"
	ActivityAutomaticReject ActivityKind = "automatic_reject"
	ActivityReviewRequested ActivityKind = "review_requested"
	ActivityActionAdmit     ActivityKind = "review_admit"
	ActivityActionReject    ActivityKind = "review_reject"
	ActivityCorrection      ActivityKind = "correction"
	ActivityReviewAbandoned ActivityKind = "review_abandoned"
	ActivityRestore         ActivityKind = "restore"
	ActivityReversal        ActivityKind = "reversal"
)

type ActivityItem struct {
	ID, ActionID, DecisionID, ClipHash, ActorID, Reason string
	Kind                                                ActivityKind
	ApplicationMode                                     ApplicationMode
	CreatedAt                                           time.Time
}

type ActivityPage struct {
	Rows  []ActivityItem
	Total int
}
