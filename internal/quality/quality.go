// Package quality owns Loomarr's privacy-safe discovery-quality vocabulary.
// It deliberately contains aggregate workflow facts, never content, people, or
// arbitrary labels (design §17).
package quality

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	RunSnapshotSchemaVersion = 1
	MaxFactLength            = 200
	MaxIdempotencyLength     = 160
	MaxObservationDuration   = 365 * 24 * time.Hour
	MaxToolCalls             = 10_000
	MaxCandidateCount        = 1_000_000
	MaxCostNanos             = int64(1_000_000_000_000_000_000)
)

type Stage string

const (
	StageRetrieval   Stage = "retrieval"
	StageGeneration  Stage = "generation"
	StageGrounding   Stage = "grounding"
	StageApproval    Stage = "approval"
	StageAcquisition Stage = "acquisition"
	StageScheduling  Stage = "scheduling"
)

type Outcome string

const (
	OutcomeSucceeded Outcome = "succeeded"
	OutcomeEmpty     Outcome = "empty"
	OutcomeFailed    Outcome = "failed"
	OutcomeAbstained Outcome = "abstained"
	OutcomeAccepted  Outcome = "accepted"
	OutcomeRejected  Outcome = "rejected"
	OutcomeApproved  Outcome = "approved"
	OutcomeDeclined  Outcome = "declined"
	OutcomePlayable  Outcome = "playable"
	OutcomeScheduled Outcome = "scheduled"
)

var outcomesByStage = map[Stage]map[Outcome]struct{}{
	StageRetrieval:   set(OutcomeSucceeded, OutcomeEmpty, OutcomeFailed),
	StageGeneration:  set(OutcomeSucceeded, OutcomeAbstained, OutcomeFailed),
	StageGrounding:   set(OutcomeAccepted, OutcomeRejected),
	StageApproval:    set(OutcomeApproved, OutcomeDeclined),
	StageAcquisition: set(OutcomePlayable, OutcomeFailed),
	StageScheduling:  set(OutcomeScheduled, OutcomeFailed),
}

func set(values ...Outcome) map[Outcome]struct{} {
	out := make(map[Outcome]struct{}, len(values))
	for _, value := range values {
		out[value] = struct{}{}
	}
	return out
}

type Observation struct {
	IdempotencyKey string
	At             time.Time
	Stage          Stage
	Outcome        Outcome
	Duration       time.Duration
	ToolCalls      int
	CandidateCount int
	CostNanos      int64
	RunSnapshotID  string
}

func (o Observation) Validate() error {
	if o.IdempotencyKey == "" || len(o.IdempotencyKey) > MaxIdempotencyLength {
		return fmt.Errorf("quality observation: invalid idempotency key length")
	}
	if o.At.IsZero() {
		return fmt.Errorf("quality observation: event time is required")
	}
	outcomes, ok := outcomesByStage[o.Stage]
	if !ok {
		return fmt.Errorf("quality observation: unknown stage %q", o.Stage)
	}
	if _, ok := outcomes[o.Outcome]; !ok {
		return fmt.Errorf("quality observation: outcome %q is invalid for stage %q", o.Outcome, o.Stage)
	}
	if o.Duration < 0 || o.Duration > MaxObservationDuration || o.ToolCalls < 0 || o.ToolCalls > MaxToolCalls ||
		o.CandidateCount < 0 || o.CandidateCount > MaxCandidateCount || o.CostNanos < 0 || o.CostNanos > MaxCostNanos {
		return fmt.Errorf("quality observation: quantities are outside their bounds")
	}
	if len(o.RunSnapshotID) > MaxFactLength {
		return fmt.Errorf("quality observation: run snapshot id is too long")
	}
	return nil
}

type Provider string

const (
	ProviderUnknown    Provider = "unknown"
	ProviderOllama     Provider = "ollama"
	ProviderOpenRouter Provider = "openrouter"
	ProviderCustom     Provider = "custom"
)

type RunSnapshot struct {
	ID                  string    `json:"id"`
	SchemaVersion       int       `json:"schemaVersion"`
	CorpusVersion       string    `json:"corpusVersion"`
	RequestedModel      string    `json:"requestedModel"`
	ResolvedModel       string    `json:"resolvedModel,omitempty"`
	Provider            Provider  `json:"provider"`
	BudgetProfile       string    `json:"budgetProfile"`
	ApplicationVersion  string    `json:"applicationVersion"`
	AccountingAvailable bool      `json:"accountingAvailable"`
	CreatedAt           time.Time `json:"createdAt"`
}

func (s RunSnapshot) Validate() error {
	if s.ID == "" || len(s.ID) > MaxFactLength || s.SchemaVersion <= 0 || s.CreatedAt.IsZero() {
		return fmt.Errorf("quality run snapshot: identity, positive schema version, and creation time are required")
	}
	for name, value := range map[string]string{
		"corpus version":      s.CorpusVersion,
		"requested model":     s.RequestedModel,
		"resolved model":      s.ResolvedModel,
		"budget profile":      s.BudgetProfile,
		"application version": s.ApplicationVersion,
	} {
		if len(value) > MaxFactLength {
			return fmt.Errorf("quality run snapshot: %s is too long", name)
		}
		if value != "" && !factToken(value) {
			return fmt.Errorf("quality run snapshot: %s is not an identifier", name)
		}
	}
	if s.CorpusVersion == "" || s.RequestedModel == "" || s.BudgetProfile == "" || s.ApplicationVersion == "" {
		return fmt.Errorf("quality run snapshot: corpus, requested model, budget, and application versions are required")
	}
	switch s.Provider {
	case ProviderUnknown, ProviderOllama, ProviderOpenRouter, ProviderCustom:
	default:
		return fmt.Errorf("quality run snapshot: unknown provider %q", s.Provider)
	}
	return nil
}

// RunSnapshotID deterministically names the bounded snapshot facts. CreatedAt
// is normalized to whole seconds because that is the store's durable precision.
// The caller assigns the result to ID before validation or persistence.
func RunSnapshotID(s RunSnapshot) string {
	hash := sha256.New()
	for _, fact := range []string{
		strconv.Itoa(s.SchemaVersion), s.CorpusVersion, s.RequestedModel,
		s.ResolvedModel, string(s.Provider), s.BudgetProfile,
		s.ApplicationVersion, strconv.FormatBool(s.AccountingAvailable),
		s.CreatedAt.UTC().Truncate(time.Second).Format(time.RFC3339),
	} {
		_, _ = hash.Write([]byte(fact))
		_, _ = hash.Write([]byte{0})
	}
	return "eval-" + hex.EncodeToString(hash.Sum(nil))
}

func factToken(value string) bool {
	if strings.HasPrefix(value, "/") || strings.Contains(value, "..") || strings.Contains(value, "://") {
		return false
	}
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			continue
		}
		switch r {
		case '.', '_', '-', '/', ':', '+', '@':
			continue
		default:
			return false
		}
	}
	return true
}

type Aggregate struct {
	Day            string  `json:"day"`
	Stage          Stage   `json:"stage"`
	Outcome        Outcome `json:"outcome"`
	RunSnapshotID  string  `json:"runSnapshotId,omitempty"`
	Count          int64   `json:"count"`
	DurationMillis int64   `json:"durationMillis"`
	ToolCalls      int64   `json:"toolCalls"`
	CandidateCount int64   `json:"candidateCount"`
	CostNanos      int64   `json:"costNanos"`
}

type Export struct {
	SchemaVersion int           `json:"schemaVersion"`
	GeneratedAt   time.Time     `json:"generatedAt"`
	Aggregates    []Aggregate   `json:"aggregates"`
	RunSnapshots  []RunSnapshot `json:"runSnapshots"`
}
