package fillersafety

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/loomarr/loomarr/internal/mediatools"
)

const (
	// SourceAuthoritySchemaVersion identifies the immutable complete-source contract.
	SourceAuthoritySchemaVersion = 2
	maxAuthorityIDBytes          = 128
)

// ToolIdentity binds a media tool to the executable bytes that measured or
// will derive evidence from the source.
type ToolIdentity struct {
	Version      string `json:"version"`
	BinarySHA256 string `json:"binarySha256"`
}

// SourceAuthority is the certification-independent, path-independent identity
// and measured coverage of one complete filler source. A caller joins it to a
// certification authority in EvaluationRequest, avoiding a circular digest.
type SourceAuthority struct {
	SchemaVersion  int          `json:"schemaVersion"`
	PolicySHA256   string       `json:"policySha256"`
	Implementation string       `json:"implementation"`
	SourceID       string       `json:"sourceId"`
	SourceSHA256   string       `json:"sourceSha256"`
	SourceBytes    int64        `json:"sourceBytes"`
	DurationMS     int64        `json:"durationMs"`
	HasAudio       bool         `json:"hasAudio"`
	HasVideo       bool         `json:"hasVideo"`
	MeasuredAt     time.Time    `json:"measuredAt"`
	FFmpeg         ToolIdentity `json:"ffmpeg"`
	FFprobe        ToolIdentity `json:"ffprobe"`
}

// AuthorityCode is a non-sensitive reason that source planning failed.
type AuthorityCode string

const (
	AuthoritySchemaInvalid   AuthorityCode = "schema_invalid"
	AuthorityIdentityInvalid AuthorityCode = "identity_invalid"
	AuthoritySourceInvalid   AuthorityCode = "source_invalid"
	AuthorityCoverageMissing AuthorityCode = "coverage_missing"
)

// AuthorityError deliberately omits source identifiers, paths, and hashes.
type AuthorityError struct {
	Code AuthorityCode
}

func (e *AuthorityError) Error() string {
	return fmt.Sprintf("spoken-safety source authority is invalid: %s", e.Code)
}

func validateSourceAuthority(authority SourceAuthority) error {
	if authority.SchemaVersion != SourceAuthoritySchemaVersion {
		return &AuthorityError{Code: AuthoritySchemaInvalid}
	}
	if !validSHA256(authority.PolicySHA256) || !boundedAuthorityID(authority.Implementation) || !boundedAuthorityID(authority.SourceID) || !validToolIdentity(authority.FFmpeg) || !validToolIdentity(authority.FFprobe) {
		return &AuthorityError{Code: AuthorityIdentityInvalid}
	}
	if !validSHA256(authority.SourceSHA256) || authority.SourceBytes <= 0 || authority.SourceBytes > mediatools.ConditioningMaxSnapshotBytes || authority.DurationMS <= 0 || authority.MeasuredAt.IsZero() {
		return &AuthorityError{Code: AuthoritySourceInvalid}
	}
	if !authority.HasAudio || !authority.HasVideo {
		return &AuthorityError{Code: AuthorityCoverageMissing}
	}
	return nil
}

func sourceAuthoritySHA256(authority SourceAuthority) (string, error) {
	raw, err := json.Marshal(authority)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// SourceAuthoritySHA256 validates and hashes the complete path-independent
// source identity so a certification authority can bind it before evaluation.
func SourceAuthoritySHA256(authority SourceAuthority) (string, error) {
	if err := validateSourceAuthority(authority); err != nil {
		return "", err
	}
	return sourceAuthoritySHA256(authority)
}

func validToolIdentity(identity ToolIdentity) bool {
	return boundedAuthorityID(identity.Version) && validSHA256(identity.BinarySHA256)
}

func boundedAuthorityID(value string) bool {
	return value == strings.TrimSpace(value) && value != "" && utf8.ValidString(value) && len(value) <= maxAuthorityIDBytes
}

func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
