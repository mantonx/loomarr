package fillerdecision

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/loomarr/loomarr/internal/filleradmission"
)

type Service struct{ repo Repository }

func New(repo Repository) (*Service, error) {
	if repo == nil {
		return nil, fmt.Errorf("%w: repository is required", ErrInvalid)
	}
	return &Service{repo: repo}, nil
}

func (s *Service) Record(ctx context.Context, record Record) error {
	if err := ValidateRecord(record); err != nil {
		return err
	}
	return s.repo.PutFillerDecision(ctx, record)
}

func (s *Service) Act(ctx context.Context, action Action) error {
	if err := ValidateAction(action); err != nil {
		return err
	}
	return s.repo.CommitFillerDecisionAction(ctx, action)
}

func (s *Service) Overview(ctx context.Context) (Overview, error) {
	counts, err := s.repo.FillerDecisionCounts(ctx)
	if err != nil {
		return Overview{}, err
	}
	overview := Overview{Healthy: true, Next: NextNone, Counts: counts}
	switch {
	case counts.Operational > counts.Retryable:
		overview.Healthy, overview.Next = false, NextRepair
		overview.ActionCount = counts.Operational - counts.Retryable
	case counts.Retryable > 0:
		overview.Healthy, overview.Next = false, NextRetry
		overview.ActionCount = counts.Retryable
	case counts.UnresolvedReviews > 0:
		overview.Healthy, overview.Next = false, NextReview
		overview.ActionCount = counts.UnresolvedReviews
	}
	return overview, nil
}

func (s *Service) Reviews(ctx context.Context, cursor Cursor, limit int) (ReviewPage, error) {
	limit, err := validLimit(limit)
	if err != nil {
		return ReviewPage{}, err
	}
	page, err := s.repo.ListFillerDecisions(ctx, DecisionFilter{
		Kind: OutcomeSemantic, Verdict: filleradmission.VerdictReview,
		CurrentOnly: true, UnresolvedOnly: true, Cursor: cursor, Limit: limit,
	})
	if err != nil {
		return ReviewPage{}, err
	}
	out := ReviewPage{Rows: make([]ReviewItem, 0, len(page.Rows)), Total: page.Total}
	for _, record := range page.Rows {
		decision := record.Result.Decision
		out.Rows = append(out.Rows, ReviewItem{
			ID: record.ID, ClipHash: record.ClipHash, Question: decision.ReviewQuestion,
			ApplicationMode: record.ApplicationMode,
			ReasonCodes:     append([]filleradmission.ReasonCode{}, decision.ReasonCodes...),
			EvidenceRefs:    append([]string{}, decision.EvidenceRefs...),
			Conflicts:       append([]filleradmission.Conflict{}, decision.Conflicts...), CreatedAt: record.CreatedAt,
		})
	}
	return out, nil
}

func (s *Service) Diagnostics(ctx context.Context, cursor Cursor, limit int) (DiagnosticPage, error) {
	limit, err := validLimit(limit)
	if err != nil {
		return DiagnosticPage{}, err
	}
	page, err := s.repo.ListFillerDecisions(ctx, DecisionFilter{
		Kind: OutcomeOperational, CurrentOnly: true, Cursor: cursor, Limit: limit,
	})
	if err != nil {
		return DiagnosticPage{}, err
	}
	out := DiagnosticPage{Rows: make([]DiagnosticItem, 0, len(page.Rows)), Total: page.Total}
	for _, record := range page.Rows {
		hold := record.Result.Hold
		out.Rows = append(out.Rows, DiagnosticItem{
			ID: record.ID, ClipHash: record.ClipHash, Code: hold.Code,
			Retryable: hold.Retryable, Recovery: recoveryFor(hold.Code, hold.Retryable), CreatedAt: record.CreatedAt,
		})
	}
	return out, nil
}

func (s *Service) Activity(ctx context.Context, cursor Cursor, limit int) (ActivityPage, error) {
	limit, err := validLimit(limit)
	if err != nil {
		return ActivityPage{}, err
	}
	return s.repo.ListFillerDecisionActivity(ctx, cursor, limit)
}

func validLimit(limit int) (int, error) {
	if limit == 0 {
		return MaxPageSize, nil
	}
	if limit < 1 || limit > MaxPageSize {
		return 0, fmt.Errorf("%w: limit must be between 1 and %d", ErrInvalid, MaxPageSize)
	}
	return limit, nil
}

func ValidateRecord(record Record) error {
	if record.ApplicationMode != ApplicationModeShadow && record.ApplicationMode != ApplicationModeApplied {
		return fmt.Errorf("%w: application mode must be shadow or applied", ErrInvalid)
	}
	for name, value := range map[string]string{
		"id": record.ID, "clip hash": record.ClipHash, "evidence hash": record.EvidenceHash,
		"evidence version": record.EvidenceVersion, "policy version": record.PolicyVersion,
		"taxonomy version": record.TaxonomyVersion,
	} {
		if !boundedRequired(value, MaxIDBytes) {
			return fmt.Errorf("%w: %s is required and bounded", ErrInvalid, name)
		}
	}
	if record.SchemaVersion < 1 || record.CreatedAt.IsZero() || record.Kind() == "" {
		return fmt.Errorf("%w: schema, timestamp, and exactly one outcome are required", ErrInvalid)
	}
	payload, err := json.Marshal(record.Result)
	if err != nil || len(payload) > MaxResultBytes {
		return fmt.Errorf("%w: canonical result exceeds its bound", ErrInvalid)
	}
	if decision := record.Result.Decision; decision != nil {
		if len(decision.ReasonCodes) == 0 || !knownVerdict(decision.Verdict) {
			return fmt.Errorf("%w: semantic decision is incomplete", ErrInvalid)
		}
		question := strings.TrimSpace(decision.ReviewQuestion)
		if (decision.Verdict == filleradmission.VerdictReview) != (question != "") || !bounded(decision.ReviewQuestion, MaxTextBytes) {
			return fmt.Errorf("%w: review decisions require exactly one bounded question", ErrInvalid)
		}
	}
	if hold := record.Result.Hold; hold != nil {
		if hold.Code == "" || !bounded(hold.Detail, MaxTextBytes) {
			return fmt.Errorf("%w: operational hold is incomplete", ErrInvalid)
		}
	}
	return nil
}

func recoveryFor(code filleradmission.OperationalCode, retryable bool) RecoveryAction {
	switch code {
	case filleradmission.HoldProviderUnavailable, filleradmission.HoldRouteUnavailable:
		return RecoveryConfigureProvider
	case filleradmission.HoldBudgetExhausted:
		return RecoveryAdjustBudget
	case filleradmission.HoldExtractionFailed:
		if retryable {
			return RecoveryRetryExtraction
		}
		return RecoveryInspectMedia
	default:
		return RecoveryUpdatePolicy
	}
}

func ValidateAction(action Action) error {
	for name, value := range map[string]string{
		"id": action.ID, "decision id": action.DecisionID, "actor id": action.ActorID,
	} {
		if !boundedRequired(value, MaxIDBytes) {
			return fmt.Errorf("%w: %s is required and bounded", ErrInvalid, name)
		}
	}
	if action.CreatedAt.IsZero() || !knownAction(action.Kind) ||
		!bounded(action.Reason, MaxTextBytes) || !bounded(action.Answer, MaxTextBytes) ||
		!bounded(action.SupersedesID, MaxIDBytes) {
		return fmt.Errorf("%w: action is incomplete or outside its bounds", ErrInvalid)
	}
	if action.Kind == ActionCorrect {
		if strings.TrimSpace(action.Answer) == "" ||
			(action.CorrectedVerdict != filleradmission.VerdictAdmit && action.CorrectedVerdict != filleradmission.VerdictReject) {
			return fmt.Errorf("%w: correction requires an answer and corrected verdict", ErrInvalid)
		}
	} else if action.CorrectedVerdict != "" {
		return fmt.Errorf("%w: only a correction carries a corrected verdict", ErrInvalid)
	}
	if (action.Kind == ActionRestore || action.Kind == ActionReverse) && strings.TrimSpace(action.Reason) == "" {
		return fmt.Errorf("%w: restore and reverse require an audit reason", ErrInvalid)
	}
	return nil
}

func knownVerdict(verdict filleradmission.Verdict) bool {
	return verdict == filleradmission.VerdictAdmit || verdict == filleradmission.VerdictReject || verdict == filleradmission.VerdictReview
}

func knownAction(kind ActionKind) bool {
	return kind == ActionAdmit || kind == ActionReject || kind == ActionCorrect ||
		kind == ActionAbandon || kind == ActionRestore || kind == ActionReverse
}

func boundedRequired(value string, limit int) bool {
	return strings.TrimSpace(value) != "" && bounded(value, limit)
}

func bounded(value string, limit int) bool {
	return len(value) <= limit && utf8.ValidString(value)
}
