package quality_test

import (
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/quality"
)

func TestObservationValidationUsesClosedStageOutcomePairs(t *testing.T) {
	valid := quality.Observation{
		IdempotencyKey: "proposal-job-1:retrieval",
		At:             time.Unix(1_700_000_000, 0).UTC(),
		Stage:          quality.StageRetrieval,
		Outcome:        quality.OutcomeEmpty,
		Duration:       250 * time.Millisecond,
		ToolCalls:      2,
		CandidateCount: 12,
		CostNanos:      9,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid observation: %v", err)
	}

	invalid := valid
	invalid.Outcome = quality.OutcomeDeclined
	if err := invalid.Validate(); err == nil {
		t.Fatal("retrieval accepted approval-only declined outcome")
	}

	invalid = valid
	invalid.IdempotencyKey = ""
	if err := invalid.Validate(); err == nil {
		t.Fatal("observation accepted empty idempotency key")
	}

	invalid = valid
	invalid.CandidateCount = -1
	if err := invalid.Validate(); err == nil {
		t.Fatal("observation accepted negative quantity")
	}
}

func TestRunSnapshotValidationBoundsCallerAuthoredFacts(t *testing.T) {
	valid := quality.RunSnapshot{
		ID:                  "cert-2026-09",
		SchemaVersion:       1,
		CorpusVersion:       "2026-08-27.8",
		RequestedModel:      "openai/gpt-5-mini",
		ResolvedModel:       "openai/gpt-5-mini-2026-08-07",
		Provider:            quality.ProviderOpenRouter,
		BudgetProfile:       "certification-default",
		ApplicationVersion:  "0.1.0-beta.10",
		AccountingAvailable: true,
		CreatedAt:           time.Unix(1_700_000_000, 0).UTC(),
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid snapshot: %v", err)
	}

	invalid := valid
	invalid.Provider = "https://provider.example"
	if err := invalid.Validate(); err == nil {
		t.Fatal("snapshot accepted caller-authored provider")
	}

	invalid = valid
	invalid.RequestedModel = strings.Repeat("x", quality.MaxFactLength+1)
	if err := invalid.Validate(); err == nil {
		t.Fatal("snapshot accepted over-bound model fact")
	}

	invalid = valid
	invalid.RequestedModel = "https://provider.example/model"
	if err := invalid.Validate(); err == nil {
		t.Fatal("snapshot accepted a URL-shaped model fact")
	}
}

func TestRunSnapshotIDIsStableAtStorePrecision(t *testing.T) {
	snapshot := quality.RunSnapshot{
		SchemaVersion: quality.RunSnapshotSchemaVersion,
		CorpusVersion: "planner-certification-v5", RequestedModel: "qwen/qwen3.5-27b",
		ResolvedModel: "qwen/qwen3.5-27b-20260901", Provider: quality.ProviderOpenRouter,
		BudgetProfile: "hosted-bounded-v1", ApplicationVersion: "v0.1.0+abc123",
		AccountingAvailable: true, CreatedAt: time.Unix(1_800_000_000, 123).UTC(),
	}
	first := quality.RunSnapshotID(snapshot)
	snapshot.CreatedAt = snapshot.CreatedAt.Truncate(time.Second)
	if second := quality.RunSnapshotID(snapshot); first != second || len(first) != len("eval-")+64 {
		t.Fatalf("run snapshot ids = %q and %q", first, second)
	}
}
