package fillersafety

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

const (
	PolicySchemaVersion   = 1
	PolicyContractVersion = "filler-spoken-safety-policy-v1"

	PolicyClassProhibited = "prohibited"
	PolicyClassAmbiguous  = "ambiguous"
	PolicyModeExactWords  = "exact_words"
	PolicyModeTokenPrefix = "token_prefix"
)

// Policy is the private restricted-language authority shared by diagnostic
// projection and production spoken-safety evaluation.
type Policy struct {
	SchemaVersion            int          `json:"schemaVersion"`
	ContractVersion          string       `json:"contractVersion"`
	PolicyID                 string       `json:"policyId"`
	GeneratedAt              time.Time    `json:"generatedAt"`
	MaximumInterSegmentGapMS int64        `json:"maximumInterSegmentGapMs"`
	Rules                    []PolicyRule `json:"rules"`
}

// PolicyRule binds an opaque identifier to private variants and match mode.
type PolicyRule struct {
	ID        string   `json:"id"`
	Class     string   `json:"class"`
	MatchMode string   `json:"matchMode"`
	Variants  []string `json:"variants"`
}

// ValidatePolicy rejects malformed or ambiguous private rule authorities
// without reproducing any restricted value in its errors.
func ValidatePolicy(policy Policy) error {
	if policy.SchemaVersion != PolicySchemaVersion || policy.ContractVersion != PolicyContractVersion || !ValidPolicyID(policy.PolicyID) || policy.GeneratedAt.IsZero() || policy.MaximumInterSegmentGapMS < 0 || policy.MaximumInterSegmentGapMS > 5_000 || len(policy.Rules) == 0 || len(policy.Rules) > 256 {
		return fmt.Errorf("spoken-safety policy identity, timing, or rule count is invalid")
	}
	seenIDs := map[string]struct{}{}
	seenVariants := map[string]struct{}{}
	for ruleIndex, rule := range policy.Rules {
		if !ValidPolicyRuleID(rule.ID) || rule.Class != PolicyClassProhibited && rule.Class != PolicyClassAmbiguous || rule.MatchMode != PolicyModeExactWords && rule.MatchMode != PolicyModeTokenPrefix || len(rule.Variants) == 0 || len(rule.Variants) > 16 {
			return fmt.Errorf("spoken-safety policy rule %d is invalid", ruleIndex)
		}
		if _, duplicate := seenIDs[rule.ID]; duplicate {
			return fmt.Errorf("spoken-safety policy repeats a rule id")
		}
		seenIDs[rule.ID] = struct{}{}
		for variantIndex, variant := range rule.Variants {
			words := CanonicalWords(variant)
			if len(words) == 0 || len(words) > 12 || len([]rune(variant)) > 128 {
				return fmt.Errorf("spoken-safety policy rule %d variant %d is invalid", ruleIndex, variantIndex)
			}
			keyBytes, _ := json.Marshal(words)
			key := string(keyBytes)
			if _, duplicate := seenVariants[key]; duplicate {
				return fmt.Errorf("spoken-safety policy repeats a normalized variant")
			}
			seenVariants[key] = struct{}{}
		}
	}
	return nil
}

// CanonicalWords applies the policy's stable Unicode word normalization.
func CanonicalWords(value string) []string {
	value = cases.Fold().String(norm.NFKC.String(value))
	var words []string
	var current []rune
	flush := func() {
		if len(current) > 0 {
			words = append(words, string(current))
			current = current[:0]
		}
	}
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			current = append(current, r)
		} else {
			flush()
		}
	}
	flush()
	return words
}

// ValidPolicyID reports whether value is a bounded public policy identifier.
func ValidPolicyID(value string) bool {
	if len(value) < len("policy-a00") || len(value) > 71 || !strings.HasPrefix(value, "policy-") {
		return false
	}
	for _, r := range value[len("policy-"):] {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '.' && r != '_' && r != '-' {
			return false
		}
	}
	return true
}

// ValidPolicyRuleID reports whether value is an opaque 96-bit rule identifier.
func ValidPolicyRuleID(value string) bool {
	if len(value) != len("rule-")+24 || !strings.HasPrefix(value, "rule-") {
		return false
	}
	_, err := hex.DecodeString(value[len("rule-"):])
	return err == nil
}
