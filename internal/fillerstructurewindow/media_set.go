package fillerstructurewindow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"

	"github.com/loomarr/loomarr/internal/fillerstructure"
)

const (
	MediaSetSchemaVersion   = 1
	MediaSetContractVersion = "filler-structure-window-media-set-v1"
)

// WindowMedia binds one planned ordinal to the exact normalized bytes supplied to assessors.
// The plan remains the source-relative geometry authority.
type WindowMedia struct {
	Ordinal int                             `json:"ordinal"`
	Media   fillerstructure.AssessmentMedia `json:"media"`
}

// MediaSet is the path-free, content-addressed input authority shared by every assessor family.
// Machine-local paths deliberately remain outside this artifact.
type MediaSet struct {
	SchemaVersion   int           `json:"schemaVersion"`
	ContractVersion string        `json:"contractVersion"`
	Plan            Plan          `json:"plan"`
	Windows         []WindowMedia `json:"windows"`
	SHA256          string        `json:"sha256"`
}

// NewMediaSet closes an ordered set of normalized window identities over one exact plan.
func NewMediaSet(plan Plan, media []fillerstructure.AssessmentMedia) (MediaSet, error) {
	windows := make([]WindowMedia, len(media))
	for ordinal := range media {
		windows[ordinal] = WindowMedia{Ordinal: ordinal, Media: media[ordinal]}
	}
	set := MediaSet{
		SchemaVersion: MediaSetSchemaVersion, ContractVersion: MediaSetContractVersion,
		Plan: plan, Windows: windows,
	}
	set.SHA256 = MediaSetSHA256(set)
	return set, ValidateMediaSet(set)
}

// MediaSetSHA256 returns the media-set identity with its self-digest excluded.
func MediaSetSHA256(set MediaSet) string {
	set.SHA256 = ""
	set.Plan.Windows = slices.Clone(set.Plan.Windows)
	set.Windows = slices.Clone(set.Windows)
	raw, err := json.Marshal(set)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}
