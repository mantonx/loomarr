package suggest

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/loomarr/loomarr/internal/provision"
)

// Failure is the public, safe failure seam for one suggestion run. Cause is
// available only for errors.Is/As inside the worker; Code and Trace are the
// only facts suitable for persistence or Journey projection.
type Failure struct {
	Code  string
	Trace DecisionTrace
	Cause error
}

func (f *Failure) Error() string {
	message := map[string]string{
		FailureSelectionEmpty:       "no grounded titles",
		FailureCodeNoGroundedTitles: "no grounded titles",
		FailureBudgetExhausted:      "suggestion budget exhausted",
		FailureProvider:             "provider failure",
	}[f.Code]
	if message == "" {
		message = "suggestion failure"
	}
	return fmt.Sprintf("suggestion failed: %s", message)
}
func (f *Failure) Unwrap() error { return f.Cause }

func (f *Failure) TraceJSON() (string, error) {
	if err := ValidateDecisionTrace(f.Trace); err != nil {
		return "", fmt.Errorf("invalid suggestion failure trace: %w", err)
	}
	blob, err := json.Marshal(f.Trace)
	if err != nil {
		return "", fmt.Errorf("marshal bounded suggestion failure trace: %w", err)
	}
	return string(blob), nil
}

const decisionTraceMaxString = 256

// ValidateDecisionTrace is the shared typed boundary for persisted and evaluated traces.
func ValidateDecisionTrace(trace DecisionTrace) error {
	if trace.Version == 0 && len(trace.Candidates) == 0 && trace.Terminal == "" && trace.SurfacedTotal == 0 && trace.RecordedTotal == 0 {
		return nil // absent trace on pre-v1 proposals
	}
	if trace.Version != DecisionTraceVersion || len(trace.Candidates) > DecisionTraceMaxCandidates || trace.SurfacedTotal < 0 || trace.RecordedTotal < 0 || trace.SurfacedTotal > DecisionTraceMaxTotal || trace.RecordedTotal > DecisionTraceMaxTotal || trace.RecordedTotal < len(trace.Candidates) {
		return fmt.Errorf("invalid version, bounds, or totals")
	}
	if (trace.SurfacedTotal > DecisionTraceMaxCandidates || trace.RecordedTotal > DecisionTraceMaxCandidates) && !trace.Truncated {
		return fmt.Errorf("unbounded surfaced or recorded total")
	}
	if trace.Terminal != "" && !knownTerminal(trace.Terminal) {
		return fmt.Errorf("unknown terminal %q", trace.Terminal)
	}
	for _, c := range trace.Candidates {
		for name, value := range map[string]string{"key": c.Key, "name": c.Name, "source": c.Source, "ownership": c.Ownership, "disposition": c.Disposition, "reason": c.Reason, "tieKey": c.Rank.TieKey} {
			if len(value) > decisionTraceMaxString || strings.ContainsRune(value, '\x00') {
				return fmt.Errorf("invalid %s", name)
			}
		}
		if c.Disposition == DispositionValidationDropped && c.Reason == ReasonMalformedID && c.Key == "" && c.Name == "" && c.Source == "" && c.Ownership == "" && c.Rank == (RankTuple{}) && c.Constraints == (ConstraintMatches{}) {
			continue
		}
		if c.Disposition == DispositionValidationDropped && c.Reason == ReasonNotSurfaced && c.Key != "" && c.Name == "" && c.Source == "" && c.Ownership == "" && c.Rank == (RankTuple{}) && c.Constraints == (ConstraintMatches{}) {
			if _, _, _, ok := provision.ParseKey(provision.Key(c.Key)); !ok {
				return fmt.Errorf("invalid canonical key")
			}
			continue
		}
		if c.Ownership != "library" && c.Ownership != "acquisition" {
			return fmt.Errorf("invalid ownership")
		}
		if c.Key == "" || c.Rank.TieKey == "" || c.Rank.TieKey != c.Key {
			return fmt.Errorf("invalid canonical key or tie key")
		}
		if _, _, _, ok := provision.ParseKey(provision.Key(c.Key)); !ok {
			return fmt.Errorf("invalid canonical key")
		}
		if c.Rank.Relevance < 0 || c.Rank.Relevance > 1<<31-1 || c.Rank.Preference < -5 || c.Rank.Preference > 3 || c.Rank.Novelty < 0 || c.Rank.Novelty > rankNoveltyMax {
			return fmt.Errorf("rank tuple outside representation bounds")
		}
		if (c.Rank.Relevance > 0) != c.Constraints.any() {
			return fmt.Errorf("constraint matches do not explain relevance")
		}
		if !validDispositionReason(c.Disposition, c.Reason) {
			return fmt.Errorf("invalid disposition/reason")
		}
	}
	return nil
}

func (m ConstraintMatches) any() bool {
	return m.Request || m.Tone || m.Era || m.MustInclude || m.MustExclude || m.Refine
}

func knownTerminal(value string) bool {
	switch value {
	case ReasonRetrievalEmpty, FailureSelectionEmpty, FailureBudgetExhausted, TerminalProviderFailure, TerminalRetrievalFailure, TerminalGenerationFailure, TerminalMalformedExhausted:
		return true
	default:
		return false
	}
}

func validDispositionReason(disposition, reason string) bool {
	if disposition == DispositionTerminal {
		return knownTerminal(reason)
	}
	allowed := map[string]map[string]bool{
		DispositionSelected:          {"selected": true},
		DispositionAlternate:         {ReasonAcquisitionCap: true},
		DispositionNotSelected:       {ReasonNotSelected: true, ReasonNever: true, ReasonOverCeiling: true},
		DispositionRefused:           {ReasonOverCeiling: true},
		DispositionValidationDropped: {ReasonMalformedID: true, ReasonNotSurfaced: true, ReasonNoRelevanceEvidence: true, ReasonValidationDropped: true},
	}
	return allowed[disposition][reason]
}

func NewFailure(code string, trace DecisionTrace, cause error) error {
	trace = trace.Clone()
	return &Failure{Code: code, Trace: trace, Cause: cause}
}

const (
	FailureSelectionEmpty  = "selection_empty"
	FailureBudgetExhausted = "budget_exhausted"
	FailureProvider        = "provider_failure"
)
