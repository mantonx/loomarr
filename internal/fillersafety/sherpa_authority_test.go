//go:build !windows

package fillersafety

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAcousticKeywordAuthorityBuildsOnlyOpaqueSherpaLabels(t *testing.T) {
	t.Parallel()
	contract := fakeSherpaContract("test/platform", map[string][]byte{})
	vocabulary := map[string]struct{}{"SAFE": {}, "TOKEN": {}}
	authority := acousticKeywordAuthority{
		SchemaVersion: acousticKeywordAuthoritySchemaVersion, ContractVersion: acousticKeywordAuthorityContractVersion,
		PolicySHA256: strings.Repeat("a", 64), ModelSHA256: sherpaModelIdentitySHA256(contract), BPEModelSHA256: contract.bpeModelSHA256,
		Rules: []acousticKeywordRule{{ID: "rule-0123456789abcdef01234567", Variants: [][]string{{"SAFE", "TOKEN"}}}},
	}
	keywords, variants, err := validateAcousticKeywordAuthority(authority, vocabulary, authority.ModelSHA256, contract.bpeModelSHA256)
	if err != nil {
		t.Fatal(err)
	}
	want := "SAFE TOKEN :4 #0.05 @rule-0123456789abcdef01234567\n"
	if string(keywords) != want || len(variants) != 1 {
		t.Fatalf("keywords=%q variants=%v", keywords, variants)
	}
}

func TestAcousticKeywordAuthorityFailsClosed(t *testing.T) {
	t.Parallel()
	contract := fakeSherpaContract("test/platform", map[string][]byte{})
	modelSHA := sherpaModelIdentitySHA256(contract)
	valid := acousticKeywordAuthority{
		SchemaVersion: acousticKeywordAuthoritySchemaVersion, ContractVersion: acousticKeywordAuthorityContractVersion,
		PolicySHA256: strings.Repeat("a", 64), ModelSHA256: modelSHA, BPEModelSHA256: contract.bpeModelSHA256,
		Rules: []acousticKeywordRule{{ID: "rule-0123456789abcdef01234567", Variants: [][]string{{"SAFE"}}}},
	}
	tests := []struct {
		name   string
		mutate func(*acousticKeywordAuthority)
		vocab  map[string]struct{}
	}{
		{name: "wrong model", mutate: func(value *acousticKeywordAuthority) { value.ModelSHA256 = strings.Repeat("b", 64) }, vocab: map[string]struct{}{"SAFE": {}}},
		{name: "unknown token", vocab: map[string]struct{}{"OTHER": {}}},
		{name: "syntax token", mutate: func(value *acousticKeywordAuthority) { value.Rules[0].Variants[0][0] = "BAD@TOKEN" }, vocab: map[string]struct{}{"BAD@TOKEN": {}}},
		{name: "non opaque rule", mutate: func(value *acousticKeywordAuthority) { value.Rules[0].ID = "plain-text" }, vocab: map[string]struct{}{"SAFE": {}}},
		{name: "duplicate variant", mutate: func(value *acousticKeywordAuthority) {
			value.Rules = append(value.Rules, acousticKeywordRule{ID: "rule-1123456789abcdef01234567", Variants: [][]string{{"SAFE"}}})
		}, vocab: map[string]struct{}{"SAFE": {}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := cloneAcousticAuthority(t, valid)
			if test.mutate != nil {
				test.mutate(&value)
			}
			if _, _, err := validateAcousticKeywordAuthority(value, test.vocab, modelSHA, contract.bpeModelSHA256); err == nil {
				t.Fatal("invalid authority accepted")
			}
		})
	}
}

func TestAcousticKeywordAuthorityRequiresPrivateStrictJSON(t *testing.T) {
	t.Parallel()
	contract := fakeSherpaContract("test/platform", map[string][]byte{})
	authority := acousticKeywordAuthority{
		SchemaVersion: acousticKeywordAuthoritySchemaVersion, ContractVersion: acousticKeywordAuthorityContractVersion,
		PolicySHA256: strings.Repeat("a", 64), ModelSHA256: sherpaModelIdentitySHA256(contract), BPEModelSHA256: contract.bpeModelSHA256,
		Rules: []acousticKeywordRule{{ID: "rule-0123456789abcdef01234567", Variants: [][]string{{"SAFE"}}}},
	}
	raw, err := json.Marshal(authority)
	if err != nil {
		t.Fatal(err)
	}
	vocabulary := map[string]struct{}{"SAFE": {}}
	privatePath := filepath.Join(t.TempDir(), "authority.json")
	if err := os.WriteFile(privatePath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadAcousticKeywordAuthority(privatePath, vocabulary, authority.ModelSHA256, contract.bpeModelSHA256); err != nil {
		t.Fatalf("private authority rejected: %v", err)
	}
	if err := os.Chmod(privatePath, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadAcousticKeywordAuthority(privatePath, vocabulary, authority.ModelSHA256, contract.bpeModelSHA256); err == nil {
		t.Fatal("world-readable authority accepted")
	}
	if err := os.Chmod(privatePath, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(privatePath, append(raw, []byte(`{"extra":true}`)...), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadAcousticKeywordAuthority(privatePath, vocabulary, authority.ModelSHA256, contract.bpeModelSHA256); err == nil {
		t.Fatal("trailing JSON accepted")
	}
}

func cloneAcousticAuthority(t *testing.T, value acousticKeywordAuthority) acousticKeywordAuthority {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var cloned acousticKeywordAuthority
	if err := json.Unmarshal(raw, &cloned); err != nil {
		t.Fatal(err)
	}
	return cloned
}
