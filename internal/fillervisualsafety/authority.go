// Package fillervisualsafety owns complete-source visual-sensitive-content evidence.
// Its results may quarantine or hold filler, but never grant ingestion, scheduling,
// training, or broadcast authority.
package fillervisualsafety

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	SourceAuthoritySchemaVersion   = 1
	SourceAuthorityContractVersion = "filler-visual-source-authority-v1"
	MaximumSourceBytes             = int64(1 << 30)
	MaximumSourceDurationMS        = int64(30 * 60 * 1_000)
	maximumIdentityBytes           = 256
)

// ToolIdentity binds a measurement or extraction result to exact executable bytes.
type ToolIdentity struct {
	Name             string `json:"name"`
	Version          string `json:"version"`
	ExecutableSHA256 string `json:"executableSha256"`
}

// VideoStreamIdentity is the measured stream used for complete visual coverage.
type VideoStreamIdentity struct {
	Index                int    `json:"index"`
	Codec                string `json:"codec"`
	Width                int    `json:"width"`
	Height               int    `json:"height"`
	FirstFrameMS         int64  `json:"firstFrameMs"`
	LastFrameMS          int64  `json:"lastFrameMs"`
	FrameRateNumerator   int64  `json:"frameRateNumerator"`
	FrameRateDenominator int64  `json:"frameRateDenominator"`
	TimeBaseNumerator    int64  `json:"timeBaseNumerator"`
	TimeBaseDenominator  int64  `json:"timeBaseDenominator"`
	DurationMS           int64  `json:"durationMs"`
}

// SourceAuthority is the path-free identity that must reproduce before visual work begins.
type SourceAuthority struct {
	SchemaVersion   int                 `json:"schemaVersion"`
	ContractVersion string              `json:"contractVersion"`
	SourceID        string              `json:"sourceId"`
	SourceSHA256    string              `json:"sourceSha256"`
	SourceBytes     int64               `json:"sourceBytes"`
	DurationMS      int64               `json:"durationMs"`
	Video           VideoStreamIdentity `json:"video"`
	PolicySHA256    string              `json:"policySha256"`
	Implementation  string              `json:"implementation"`
	Probe           ToolIdentity        `json:"probe"`
	MeasuredAt      time.Time           `json:"measuredAt"`
	SHA256          string              `json:"sha256"`
}

// SealSourceAuthority fills canonical protocol fields and its content identity.
func SealSourceAuthority(authority SourceAuthority) (SourceAuthority, error) {
	authority.SchemaVersion = SourceAuthoritySchemaVersion
	authority.ContractVersion = SourceAuthorityContractVersion
	authority.MeasuredAt = authority.MeasuredAt.UTC()
	authority.SHA256 = SourceAuthoritySHA256(authority)
	if err := ValidateSourceAuthority(authority); err != nil {
		return SourceAuthority{}, err
	}
	return authority, nil
}

// ValidateSourceAuthority rejects incomplete or non-canonical source identity.
func ValidateSourceAuthority(authority SourceAuthority) error {
	if authority.SchemaVersion != SourceAuthoritySchemaVersion || authority.ContractVersion != SourceAuthorityContractVersion ||
		!validIdentity(authority.SourceID) || !validDigest(authority.SourceSHA256) || authority.SourceBytes <= 0 ||
		authority.SourceBytes > MaximumSourceBytes || authority.DurationMS <= 0 || authority.DurationMS > MaximumSourceDurationMS ||
		!validDigest(authority.PolicySHA256) || !validIdentity(authority.Implementation) || !validTool(authority.Probe) ||
		authority.MeasuredAt.IsZero() || authority.MeasuredAt.Location() != time.UTC {
		return errors.New("visual-safety source authority identity is invalid")
	}
	video := authority.Video
	if video.Index < 0 || !validIdentity(video.Codec) || video.Width <= 0 || video.Height <= 0 ||
		video.FirstFrameMS < 0 || video.LastFrameMS < video.FirstFrameMS || video.LastFrameMS >= authority.DurationMS ||
		video.FrameRateNumerator <= 0 || video.FrameRateDenominator <= 0 || video.TimeBaseNumerator <= 0 ||
		video.TimeBaseDenominator <= 0 || video.DurationMS != authority.DurationMS {
		return errors.New("visual-safety source authority video stream is invalid")
	}
	if authority.SHA256 == "" || authority.SHA256 != SourceAuthoritySHA256(authority) {
		return errors.New("visual-safety source authority digest is invalid")
	}
	return nil
}

// SourceAuthoritySHA256 returns the identity with its self-digest excluded.
func SourceAuthoritySHA256(authority SourceAuthority) string {
	authority.SHA256 = ""
	return digestJSON(authority)
}

func digestJSON(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func validDigest(value string) bool {
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validIdentity(value string) bool {
	return value != "" && value == strings.TrimSpace(value) && utf8.ValidString(value) && len(value) <= maximumIdentityBytes &&
		!strings.ContainsFunc(value, unicode.IsControl)
}

func validTool(tool ToolIdentity) bool {
	return validIdentity(tool.Name) && validIdentity(tool.Version) && validDigest(tool.ExecutableSHA256)
}
