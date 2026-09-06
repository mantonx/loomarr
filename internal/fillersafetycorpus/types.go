// Package fillersafetycorpus prepares private real-speech cohorts for later
// spoken-safety authority assembly without assigning certification truth.
package fillersafetycorpus

import (
	"context"
	"time"

	"github.com/loomarr/loomarr/internal/fillercorpus"
	"github.com/loomarr/loomarr/internal/fillersafety"
)

const (
	VCTKReleaseSchemaVersion            = 1
	VCTKReleaseContractVersion          = "filler-spoken-vctk-release-v1"
	VCTKReleaseID                       = "VCTK-Corpus-0.92"
	VCTKReleaseRecordURL                = "https://datashare.ed.ac.uk/items/30e7453c-9ea8-48b4-8e18-f96d0dc62928"
	VCTKLicenseID                       = "CC-BY-4.0"
	PreparedCohortSchemaVersion         = 1
	PreparedCohortContractVersion       = "filler-spoken-prepared-cohort-v1"
	PreparedCohortKindCleanCandidate    = "clean_candidate"
	PreparedCohortKindPositiveCandidate = "positive_candidate"
	VCTKOwnerMapContractVersion         = "filler-spoken-vctk-owner-map-v1"
	VCTKDatasetID                       = "vctk-0.92"
	VCTKTargetLocaleSlice               = "target_locale"
	VCTKNeutralVideoRecipe              = "vctk-neutral-video-640x360-30fps-h264-aac-v1"
)

type FileAuthority struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

type VCTKReleaseAuthority struct {
	SchemaVersion        int                                `json:"schemaVersion"`
	ContractVersion      string                             `json:"contractVersion"`
	ReleaseID            string                             `json:"releaseId"`
	ReleaseRecordURL     string                             `json:"releaseRecordUrl"`
	ArchiveSHA256        string                             `json:"archiveSha256"`
	ArchiveBytes         int64                              `json:"archiveBytes"`
	LicenseID            string                             `json:"licenseId"`
	License              FileAuthority                      `json:"license"`
	Readme               FileAuthority                      `json:"readme"`
	RightsReviewEvidence FileAuthority                      `json:"rightsReviewEvidence"`
	RightsReviewerID     string                             `json:"rightsReviewerId"`
	RightsReviewedAt     time.Time                          `json:"rightsReviewedAt"`
	RightsContract       fillercorpus.HoldoutRightsContract `json:"rightsContract"`
	Members              []VCTKMember                       `json:"members"`
}

type VCTKMember struct {
	SpeakerID         string        `json:"speakerId"`
	UtteranceID       string        `json:"utteranceId"`
	Microphone        string        `json:"microphone"`
	Locale            string        `json:"locale"`
	Audio             FileAuthority `json:"audio"`
	Transcript        FileAuthority `json:"transcript"`
	ScreeningEvidence FileAuthority `json:"screeningEvidence"`
}

type PrepareVCTKConfig struct {
	ReleaseAuthorityPath string
	ReleaseRoot          string
	SeedPath             string
	FFmpegPath           string
	FFprobePath          string
	PolicySHA256         string
	Implementation       string
	PreparedAt           time.Time
	ExpectedSpeakers     int
	MaximumInputBytes    int64
	MaximumOutputBytes   int64
	MaximumWallTime      time.Duration
	OutputDirectory      string
}

type PrepareVCTKResult struct {
	Speakers       int
	CohortSHA256   string
	OwnerMapSHA256 string
	InputBytes     int64
	OutputBytes    int64
}

type PreparedCohort struct {
	SchemaVersion          int                       `json:"schemaVersion"`
	ContractVersion        string                    `json:"contractVersion"`
	PreparedAt             time.Time                 `json:"preparedAt"`
	Kind                   string                    `json:"kind"`
	Dataset                string                    `json:"dataset"`
	ReleaseAuthoritySHA256 string                    `json:"releaseAuthoritySha256"`
	RecipeSHA256           string                    `json:"recipeSha256"`
	FFmpeg                 fillersafety.ToolIdentity `json:"ffmpeg"`
	FFprobe                fillersafety.ToolIdentity `json:"ffprobe"`
	Cases                  []PreparedCohortCase      `json:"cases"`
}

type PreparedCohortCase struct {
	CaseID                string                       `json:"caseId"`
	SourcePath            string                       `json:"sourcePath"`
	SourceAuthority       fillersafety.SourceAuthority `json:"sourceAuthority"`
	SourceFamily          string                       `json:"sourceFamily"`
	TranscriptPath        string                       `json:"transcriptPath,omitempty"`
	TranscriptSHA256      string                       `json:"transcriptSha256,omitempty"`
	TranscriptBytes       int64                        `json:"transcriptBytes,omitempty"`
	TruthProvenancePath   string                       `json:"truthProvenancePath"`
	TruthProvenanceSHA256 string                       `json:"truthProvenanceSha256"`
	TruthProvenanceBytes  int64                        `json:"truthProvenanceBytes"`
	RightsPath            string                       `json:"rightsPath"`
	RightsSHA256          string                       `json:"rightsSha256"`
	RightsBytes           int64                        `json:"rightsBytes"`
	Claim                 string                       `json:"claim"`
	Locale                string                       `json:"locale"`
	Slices                []string                     `json:"slices"`
	PositiveIntervals     []PreparedPositiveInterval   `json:"positiveIntervals,omitempty"`
}

type PreparedPositiveInterval struct {
	RuleID  string `json:"ruleId"`
	StartMS int64  `json:"startMs"`
	EndMS   int64  `json:"endMs"`
}

type VCTKOwnerMap struct {
	SchemaVersion   int                 `json:"schemaVersion"`
	ContractVersion string              `json:"contractVersion"`
	PreparedAt      time.Time           `json:"preparedAt"`
	CohortSHA256    string              `json:"cohortSha256"`
	Entries         []VCTKOwnerMapEntry `json:"entries"`
}

type VCTKOwnerMapEntry struct {
	CaseID         string `json:"caseId"`
	SourceFamily   string `json:"sourceFamily"`
	SpeakerID      string `json:"speakerId"`
	UtteranceID    string `json:"utteranceId"`
	Microphone     string `json:"microphone"`
	AudioPath      string `json:"audioPath"`
	TranscriptPath string `json:"transcriptPath"`
}

type wrappedMedia struct {
	SHA256     string
	Bytes      int64
	DurationMS int64
}

type mediaWrapper interface {
	Identity(context.Context) (fillersafety.ToolIdentity, fillersafety.ToolIdentity, string, error)
	Wrap(context.Context, string, string) (wrappedMedia, error)
}

// mediaWrapperFuncs adapts the two media-wrapper operations independently.
// Keeping the operations as method values makes the production seam usable by
// isolated callers without introducing a stateful wrapper implementation.
type mediaWrapperFuncs struct {
	identity func(context.Context) (fillersafety.ToolIdentity, fillersafety.ToolIdentity, string, error)
	wrap     func(context.Context, string, string) (wrappedMedia, error)
}

func (functions mediaWrapperFuncs) Identity(ctx context.Context) (fillersafety.ToolIdentity, fillersafety.ToolIdentity, string, error) {
	return functions.identity(ctx)
}

func (functions mediaWrapperFuncs) Wrap(ctx context.Context, input, output string) (wrappedMedia, error) {
	return functions.wrap(ctx, input, output)
}
