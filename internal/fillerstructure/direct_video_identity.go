package fillerstructure

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

func DirectVideoPromptSHA256(durationMS int64) string {
	raw, err := json.Marshal(struct {
		Version string `json:"version"`
		System  string `json:"system"`
		Content string `json:"content"`
	}{DirectVideoPromptVersion, DirectVideoSystemPrompt, DirectVideoContent(durationMS)})
	if err != nil {
		return ""
	}
	return directVideoIdentitySHA256(raw)
}

func DirectVideoSchemaSHA256(durationMS int64) string {
	raw, err := json.Marshal(DirectVideoSchema(durationMS))
	if err != nil {
		return ""
	}
	return directVideoIdentitySHA256(raw)
}

func directVideoIdentitySHA256(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
