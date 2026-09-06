package fillerstructure

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
)

const (
	AssessmentInputSchemaVersion   = 1
	AssessmentInputContractVersion = "filler-structure-assessment-input-v1"
	AssessmentInputMaximumItems    = 128
)

type AssessmentInputKind string

const (
	AssessmentInputCompleteVideo  AssessmentInputKind = "complete_video"
	AssessmentInputWindowMediaSet AssessmentInputKind = "window_media_set"
)

// AssessmentInput is the exact path-free media authority shared by independent candidates.
// Window geometry stays in the plan named by PlanSHA256; Items retain every submitted derivative.
type AssessmentInput struct {
	SchemaVersion   int                 `json:"schemaVersion"`
	ContractVersion string              `json:"contractVersion"`
	Kind            AssessmentInputKind `json:"kind"`
	Source          Source              `json:"source"`
	ProfileSHA256   string              `json:"profileSha256"`
	PlanSHA256      string              `json:"planSha256,omitempty"`
	Items           []AssessmentMedia   `json:"items"`
	SHA256          string              `json:"sha256"`
}

// NewCompleteVideoInput closes the existing one-video protocol as an explicit input manifest.
func NewCompleteVideoInput(source Source, media AssessmentMedia) (AssessmentInput, error) {
	return newAssessmentInput(AssessmentInputCompleteVideo, source, "", []AssessmentMedia{media})
}

// NewWindowMediaSetInput closes the media identities of one separately validated window plan.
func NewWindowMediaSetInput(source Source, planSHA256 string, media []AssessmentMedia) (AssessmentInput, error) {
	return newAssessmentInput(AssessmentInputWindowMediaSet, source, planSHA256, media)
}

func newAssessmentInput(kind AssessmentInputKind, source Source, planSHA256 string, media []AssessmentMedia) (AssessmentInput, error) {
	profileSHA256 := ""
	if len(media) != 0 {
		profileSHA256 = media[0].ProfileSHA256
	}
	input := AssessmentInput{
		SchemaVersion: AssessmentInputSchemaVersion, ContractVersion: AssessmentInputContractVersion,
		Kind: kind, Source: source, ProfileSHA256: profileSHA256, PlanSHA256: planSHA256,
		Items: slices.Clone(media),
	}
	input.SHA256 = AssessmentInputSHA256(input)
	return input, ValidateAssessmentInput(input)
}

// ValidateAssessmentInput reproduces the manifest and its kind-specific media invariants.
func ValidateAssessmentInput(input AssessmentInput) error {
	if input.SchemaVersion != AssessmentInputSchemaVersion || input.ContractVersion != AssessmentInputContractVersion ||
		!validSource(input.Source) || !digest(input.ProfileSHA256) || !digest(input.SHA256) ||
		input.SHA256 != AssessmentInputSHA256(input) || len(input.Items) == 0 || len(input.Items) > AssessmentInputMaximumItems {
		return errors.New("filler structure assessment input identity is invalid")
	}
	lineages := make(map[string]struct{}, len(input.Items))
	for _, media := range input.Items {
		if !digest(media.SHA256) || media.Bytes <= 0 || media.Bytes > AssessmentMediaMaximumBytes ||
			media.DurationMS <= 0 || media.ProfileSHA256 != input.ProfileSHA256 || !digest(media.LineageSHA256) {
			return errors.New("filler structure assessment input item is invalid")
		}
		if _, duplicate := lineages[media.LineageSHA256]; duplicate {
			return errors.New("filler structure assessment input repeats media lineage")
		}
		lineages[media.LineageSHA256] = struct{}{}
	}
	switch input.Kind {
	case AssessmentInputCompleteVideo:
		if input.PlanSHA256 != "" || len(input.Items) != 1 || !validAssessmentMedia(input.Items[0], input.Source) {
			return errors.New("filler structure complete-video input is invalid")
		}
	case AssessmentInputWindowMediaSet:
		if !digest(input.PlanSHA256) {
			return errors.New("filler structure window-media-set input is invalid")
		}
	default:
		return errors.New("filler structure assessment input kind is invalid")
	}
	return nil
}

// AssessmentInputSHA256 returns the manifest identity with its self-digest excluded.
func AssessmentInputSHA256(input AssessmentInput) string {
	input.SHA256 = ""
	input.Items = slices.Clone(input.Items)
	raw, err := json.Marshal(input)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}
