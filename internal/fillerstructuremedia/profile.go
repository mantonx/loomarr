// Package fillerstructuremedia owns the exact media contract shared by
// complete-timeline structure qualification and production assessment.
package fillerstructuremedia

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/loomarr/loomarr/internal/fillerstructure"
)

const (
	ProfileSchemaVersion   = 2
	ProfileContractVersion = "filler-structure-assessment-media-v2"
	MaximumVideoBytes      = fillerstructure.AssessmentMediaMaximumBytes
)

// Profile is the path-free identity of the one supported assessment-media
// recipe. Media-tool binary identities are bound separately by the caller.
type Profile struct {
	SchemaVersion          int    `json:"schemaVersion"`
	ContractVersion        string `json:"contractVersion"`
	MIMEType               string `json:"mimeType"`
	Container              string `json:"container"`
	VideoCodec             string `json:"videoCodec"`
	PixelFormat            string `json:"pixelFormat"`
	Width                  int    `json:"width"`
	Height                 int    `json:"height"`
	FrameRate              string `json:"frameRate"`
	SampleAspectRatio      string `json:"sampleAspectRatio"`
	AudioCodec             string `json:"audioCodec"`
	AudioSampleRate        int    `json:"audioSampleRate"`
	AudioChannels          int    `json:"audioChannels"`
	VideoTrackTimescale    int    `json:"videoTrackTimescale"`
	MaximumVideoBytes      int64  `json:"maximumVideoBytes"`
	MaximumTimelineDriftMS int64  `json:"maximumTimelineDriftMs"`
	PartRecipeSHA256       string `json:"partRecipeSha256"`
	ConcatRecipeSHA256     string `json:"concatRecipeSha256"`
	SHA256                 string `json:"sha256"`
}

// CanonicalProfile returns a fresh value so callers cannot mutate shared
// process state and silently change the recipe identity.
func CanonicalProfile() Profile {
	profile := Profile{
		SchemaVersion: ProfileSchemaVersion, ContractVersion: ProfileContractVersion,
		MIMEType: "video/mp4", Container: "mp4", VideoCodec: "h264", PixelFormat: "yuv420p",
		Width: 960, Height: 720, FrameRate: "30/1", SampleAspectRatio: "1/1",
		AudioCodec: "aac", AudioSampleRate: 48_000, AudioChannels: 2,
		VideoTrackTimescale: 90_000, MaximumVideoBytes: MaximumVideoBytes,
		MaximumTimelineDriftMS: fillerstructure.AssessmentMediaMaximumTimelineDriftMS,
		PartRecipeSHA256:       recipeSHA256(partArgumentTemplate()),
		ConcatRecipeSHA256:     recipeSHA256(concatArgumentTemplate()),
	}
	profile.SHA256 = ProfileSHA256(profile)
	return profile
}

func ProfileSHA256(profile Profile) string {
	profile.SHA256 = ""
	raw, err := json.Marshal(profile)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func recipeSHA256(arguments []string) string {
	raw, err := json.Marshal(arguments)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
