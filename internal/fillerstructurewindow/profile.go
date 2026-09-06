// Package fillerstructurewindow owns the complete-coverage plan used to assess long filler reels
// without pretending that independently processed windows are independent model votes.
package fillerstructurewindow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/loomarr/loomarr/internal/fillerstructuremedia"
)

const (
	ProfileSchemaVersion   = 2
	ProfileContractVersion = "filler-structure-window-profile-v2"

	PrimarySpanMS           int64 = 120_000
	ContextOverlapMS        int64 = 15_000
	MaximumWindowDurationMS       = PrimarySpanMS + 2*ContextOverlapMS
	MaximumSourceDurationMS int64 = 30 * 60 * 1_000
	MaximumWindows                = 15
)

// Profile is the immutable geometry and media contract for one long-reel window plan. Its source
// ceiling is bounded protocol capacity; only a later certificate can authorize a production slice.
type Profile struct {
	SchemaVersion                int    `json:"schemaVersion"`
	ContractVersion              string `json:"contractVersion"`
	AssessmentMediaProfileSHA256 string `json:"assessmentMediaProfileSha256"`
	MaximumWindowBytes           int64  `json:"maximumWindowBytes"`
	MaximumTimelineDriftMS       int64  `json:"maximumTimelineDriftMs"`
	PrimarySpanMS                int64  `json:"primarySpanMs"`
	ContextOverlapMS             int64  `json:"contextOverlapMs"`
	MaximumWindowDurationMS      int64  `json:"maximumWindowDurationMs"`
	MaximumSourceDurationMS      int64  `json:"maximumSourceDurationMs"`
	MaximumWindows               int    `json:"maximumWindows"`
	SHA256                       string `json:"sha256"`
}

// CanonicalProfile returns a fresh value to prevent mutation of process-global protocol state.
func CanonicalProfile() Profile {
	profile := Profile{
		SchemaVersion: ProfileSchemaVersion, ContractVersion: ProfileContractVersion,
		AssessmentMediaProfileSHA256: fillerstructuremedia.CanonicalProfile().SHA256,
		MaximumWindowBytes:           fillerstructuremedia.CanonicalProfile().MaximumVideoBytes,
		MaximumTimelineDriftMS:       fillerstructuremedia.CanonicalProfile().MaximumTimelineDriftMS,
		PrimarySpanMS:                PrimarySpanMS, ContextOverlapMS: ContextOverlapMS,
		MaximumWindowDurationMS: MaximumWindowDurationMS,
		MaximumSourceDurationMS: MaximumSourceDurationMS, MaximumWindows: MaximumWindows,
	}
	profile.SHA256 = ProfileSHA256(profile)
	return profile
}

// ProfileSHA256 returns the content identity with the self-digest field excluded.
func ProfileSHA256(profile Profile) string {
	profile.SHA256 = ""
	raw, err := json.Marshal(profile)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}
