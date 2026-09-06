package fillersafety

import (
	"strings"
	"testing"
	"time"
)

func TestValidatePolicyAcceptsCanonicalPrivateAuthority(t *testing.T) {
	t.Parallel()
	policy := validPolicyFixture()
	if err := ValidatePolicy(policy); err != nil {
		t.Fatal(err)
	}
	if got := CanonicalWords("Ａ Private—Term"); len(got) != 3 || got[0] != "a" || got[1] != "private" || got[2] != "term" {
		t.Fatalf("canonical words=%q", got)
	}
}

func TestValidatePolicyRejectsNormalizedDuplicateWithoutLeakingIt(t *testing.T) {
	t.Parallel()
	policy := validPolicyFixture()
	secret := "PRIVATE TERM"
	policy.Rules = append(policy.Rules, PolicyRule{
		ID:        "rule-abcdefabcdefabcdefabcdef",
		Class:     PolicyClassAmbiguous,
		MatchMode: PolicyModeExactWords,
		Variants:  []string{secret},
	})
	err := ValidatePolicy(policy)
	if err == nil || !strings.Contains(err.Error(), "normalized") || strings.Contains(err.Error(), secret) {
		t.Fatalf("err=%v", err)
	}
}

func TestValidatePolicyRejectsUnknownModesAndIdentifiers(t *testing.T) {
	t.Parallel()
	tests := []func(*Policy){
		func(policy *Policy) { policy.PolicyID = "private value" },
		func(policy *Policy) { policy.Rules[0].ID = "rule-private-value" },
		func(policy *Policy) { policy.Rules[0].Class = "future" },
		func(policy *Policy) { policy.Rules[0].MatchMode = "future" },
	}
	for _, mutate := range tests {
		policy := validPolicyFixture()
		mutate(&policy)
		if err := ValidatePolicy(policy); err == nil {
			t.Fatalf("invalid policy accepted: %+v", policy)
		}
	}
}

func validPolicyFixture() Policy {
	return Policy{
		SchemaVersion: PolicySchemaVersion, ContractVersion: PolicyContractVersion,
		PolicyID: "policy-private-v1", GeneratedAt: time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC), MaximumInterSegmentGapMS: 500,
		Rules: []PolicyRule{{
			ID: "rule-0123456789abcdef01234567", Class: PolicyClassProhibited,
			MatchMode: PolicyModeExactWords, Variants: []string{"private term"},
		}},
	}
}
