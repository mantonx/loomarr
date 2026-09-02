package fillersafety

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	acousticKeywordAuthoritySchemaVersion   = 1
	acousticKeywordAuthorityContractVersion = "filler-spoken-safety-sherpa-keywords-v1"
	maxAcousticKeywordAuthorityBytes        = 256 << 10
	maxAcousticVariantsPerRule              = 16
	maxAcousticTokensPerVariant             = 64
)

type acousticKeywordAuthority struct {
	SchemaVersion   int                   `json:"schemaVersion"`
	ContractVersion string                `json:"contractVersion"`
	PolicySHA256    string                `json:"policySha256"`
	ModelSHA256     string                `json:"modelSha256"`
	BPEModelSHA256  string                `json:"bpeModelSha256"`
	Rules           []acousticKeywordRule `json:"rules"`
}

type acousticKeywordRule struct {
	ID       string     `json:"id"`
	Variants [][]string `json:"variants"`
}

type loadedAcousticAuthority struct {
	authority acousticKeywordAuthority
	sha256    string
	keywords  []byte
	variants  map[string][][]string
}

func loadAcousticKeywordAuthority(path string, vocabulary map[string]struct{}, modelSHA256, bpeModelSHA256 string) (loadedAcousticAuthority, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxAcousticKeywordAuthorityBytes || runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		return loadedAcousticAuthority{}, fmt.Errorf("spoken-safety acoustic keyword authority is unavailable")
	}
	file, err := os.Open(path)
	if err != nil {
		return loadedAcousticAuthority{}, fmt.Errorf("spoken-safety acoustic keyword authority is unavailable")
	}
	defer func() { _ = file.Close() }()
	raw, err := io.ReadAll(io.LimitReader(file, maxAcousticKeywordAuthorityBytes+1))
	if err != nil || len(raw) == 0 || len(raw) > maxAcousticKeywordAuthorityBytes {
		return loadedAcousticAuthority{}, fmt.Errorf("spoken-safety acoustic keyword authority is invalid")
	}
	var authority acousticKeywordAuthority
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&authority); err != nil {
		return loadedAcousticAuthority{}, fmt.Errorf("spoken-safety acoustic keyword authority is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return loadedAcousticAuthority{}, fmt.Errorf("spoken-safety acoustic keyword authority is invalid")
	}
	keywords, variants, err := validateAcousticKeywordAuthority(authority, vocabulary, modelSHA256, bpeModelSHA256)
	if err != nil {
		return loadedAcousticAuthority{}, err
	}
	sum := sha256.Sum256(raw)
	return loadedAcousticAuthority{authority: authority, sha256: hex.EncodeToString(sum[:]), keywords: keywords, variants: variants}, nil
}

func validateAcousticKeywordAuthority(authority acousticKeywordAuthority, vocabulary map[string]struct{}, modelSHA256, bpeModelSHA256 string) ([]byte, map[string][][]string, error) {
	if authority.SchemaVersion != acousticKeywordAuthoritySchemaVersion || authority.ContractVersion != acousticKeywordAuthorityContractVersion || !validSHA256(authority.PolicySHA256) || authority.ModelSHA256 != modelSHA256 || authority.BPEModelSHA256 != bpeModelSHA256 || len(authority.Rules) == 0 || len(authority.Rules) > 256 || len(vocabulary) == 0 {
		return nil, nil, fmt.Errorf("spoken-safety acoustic keyword authority is invalid")
	}
	seenIDs := make(map[string]struct{}, len(authority.Rules))
	seenVariants := make(map[string]struct{})
	variants := make(map[string][][]string, len(authority.Rules))
	var keywords strings.Builder
	for _, rule := range authority.Rules {
		if !ValidPolicyRuleID(rule.ID) || len(rule.Variants) == 0 || len(rule.Variants) > maxAcousticVariantsPerRule {
			return nil, nil, fmt.Errorf("spoken-safety acoustic keyword authority is invalid")
		}
		if _, duplicate := seenIDs[rule.ID]; duplicate {
			return nil, nil, fmt.Errorf("spoken-safety acoustic keyword authority is invalid")
		}
		seenIDs[rule.ID] = struct{}{}
		for _, variant := range rule.Variants {
			if len(variant) == 0 || len(variant) > maxAcousticTokensPerVariant {
				return nil, nil, fmt.Errorf("spoken-safety acoustic keyword authority is invalid")
			}
			for _, token := range variant {
				if !validAcousticToken(token) {
					return nil, nil, fmt.Errorf("spoken-safety acoustic keyword authority is invalid")
				}
				if _, known := vocabulary[token]; !known {
					return nil, nil, fmt.Errorf("spoken-safety acoustic keyword authority is invalid")
				}
			}
			key := strings.Join(variant, "\x00")
			if _, duplicate := seenVariants[key]; duplicate {
				return nil, nil, fmt.Errorf("spoken-safety acoustic keyword authority is invalid")
			}
			seenVariants[key] = struct{}{}
			variants[rule.ID] = append(variants[rule.ID], slices.Clone(variant))
			keywords.WriteString(strings.Join(variant, " "))
			keywords.WriteString(" :")
			keywords.WriteString(strconv.Itoa(sherpaKeywordScore))
			keywords.WriteString(" #")
			keywords.WriteString(sherpaKeywordThreshold)
			keywords.WriteString(" @")
			keywords.WriteString(rule.ID)
			keywords.WriteByte('\n')
		}
	}
	return []byte(keywords.String()), variants, nil
}

func validAcousticToken(token string) bool {
	if token == "" || !utf8.ValidString(token) || len(token) > 128 || strings.ContainsAny(token, ":#@") {
		return false
	}
	for _, char := range token {
		if unicode.IsSpace(char) || unicode.IsControl(char) {
			return false
		}
	}
	return true
}
