package fillersafetycorpus

import (
	"fmt"
	"math"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/loomarr/loomarr/internal/fillercorpus"
	"github.com/loomarr/loomarr/internal/fillersafetycert"
)

func validateKnownScriptConfig(config PrepareKnownScriptConfig) error {
	if strings.TrimSpace(config.AuthorityPath) == "" || strings.TrimSpace(config.SourceRoot) == "" ||
		strings.TrimSpace(config.SeedPath) == "" || strings.TrimSpace(config.FFmpegPath) == "" ||
		strings.TrimSpace(config.FFprobePath) == "" || strings.TrimSpace(config.OutputDirectory) == "" ||
		config.PreparedAt.IsZero() || config.ExpectedSpeakers < fillersafetycert.MinimumPositiveFamilies ||
		config.MaximumInputBytes <= 0 || config.MaximumOutputBytes <= 0 || config.MaximumWallTime <= 0 ||
		config.MaximumWallTime > 24*time.Hour {
		return fmt.Errorf("known-script preparation requires exact private inputs, at least %d speakers, and positive resource ceilings", fillersafetycert.MinimumPositiveFamilies)
	}
	root, rootErr := filepath.Abs(filepath.Clean(config.SourceRoot))
	output, outputErr := filepath.Abs(filepath.Clean(config.OutputDirectory))
	if rootErr != nil || outputErr != nil || pathInside(root, output) {
		return fmt.Errorf("known-script output must be outside the private source root")
	}
	return nil
}

func validateKnownScriptAuthority(authority KnownScriptAuthority, config PrepareKnownScriptConfig) error {
	if authority.SchemaVersion != KnownScriptAuthoritySchemaVersion ||
		authority.ContractVersion != KnownScriptAuthorityContractVersion || authority.Dataset != KnownScriptDatasetID ||
		authority.AuthoredAt.IsZero() || authority.AuthoredAt.After(config.PreparedAt) ||
		!validSHA256(authority.PolicySHA256) || !boundedID(authority.Implementation) ||
		len(authority.Members) != config.ExpectedSpeakers {
		return fmt.Errorf("known-script authority identity or exact speaker count is invalid")
	}
	previous := ""
	participants, sessions, masters, outputs, consents := map[string]struct{}{}, map[string]struct{}{}, map[string]struct{}{}, map[string]struct{}{}, map[string]struct{}{}
	covered := map[string]struct{}{}
	for index, member := range authority.Members {
		if member.ParticipantID <= previous || !boundedID(member.ParticipantID) || !boundedID(member.SessionID) ||
			!boundedID(member.TakeID) || !vctkLocale.MatchString(member.Locale) || !boundedID(member.Accent) ||
			!boundedID(member.ScriptID) || !validFileAuthority(member.Script) || !validFileAuthority(member.PolicyMapping) ||
			!validFileAuthority(member.MasterAudio) || !validFileAuthority(member.SelectedAudio) ||
			len(member.Slices) == 0 || len(member.Slices) > len(knownScriptPositiveSlices()) ||
			!strictlySortedStrings(member.Slices) || len(member.PositiveIntervals) == 0 || len(member.PositiveIntervals) > 256 ||
			!validPreparedIntervals(member.PositiveIntervals, math.MaxInt64) {
			return fmt.Errorf("known-script member %d identity, source, slices, or intervals are invalid", index+1)
		}
		for _, slice := range member.Slices {
			if !slices.Contains(knownScriptPositiveSlices(), slice) {
				return fmt.Errorf("known-script member %d names an unknown positive slice", index+1)
			}
			covered[slice] = struct{}{}
		}
		if err := validateKnownScriptConsent(member.Consent, member.ParticipantID, authority.AuthoredAt, config.PreparedAt); err != nil {
			return fmt.Errorf("known-script member %d consent is invalid: %w", index+1, err)
		}
		if err := validateKnownScriptTransformation(member, authority.AuthoredAt, config.PreparedAt); err != nil {
			return fmt.Errorf("known-script member %d transformation is invalid: %w", index+1, err)
		}
		keys := []struct {
			value string
			seen  map[string]struct{}
			name  string
		}{
			{member.ParticipantID, participants, "participant"},
			{member.SessionID + "\x00" + member.TakeID, sessions, "session/take"},
			{member.MasterAudio.SHA256, masters, "master audio"},
			{member.SelectedAudio.SHA256, outputs, "selected audio"},
			{member.Consent.Document.SHA256, consents, "consent document"},
		}
		for _, key := range keys {
			if _, duplicate := key.seen[key.value]; duplicate {
				return fmt.Errorf("known-script authority repeats a %s", key.name)
			}
			key.seen[key.value] = struct{}{}
		}
		previous = member.ParticipantID
	}
	for _, required := range knownScriptPositiveSlices() {
		if _, ok := covered[required]; !ok {
			return fmt.Errorf("known-script authority does not cover every required positive slice")
		}
	}
	return nil
}

func validateKnownScriptConsent(consent KnownScriptConsent, participantID string, authoredAt, preparedAt time.Time) error {
	grants := consent.Grants
	if consent.SchemaVersion != KnownScriptConsentSchemaVersion || consent.ContractVersion != KnownScriptConsentContractVersion ||
		consent.ParticipantID != participantID || !validFileAuthority(consent.Document) ||
		!validFileAuthority(consent.SignerAuthorityEvidence) || !validFileAuthority(consent.ProcessorSchedule) ||
		!validFileAuthority(consent.WithdrawalInstructions) || consent.SignedAt.IsZero() ||
		consent.RightsReviewedAt.IsZero() || consent.RightsReviewedAt.Before(consent.SignedAt) ||
		consent.RightsReviewedAt.After(authoredAt) || !boundedID(consent.RightsReviewerID) ||
		(consent.ExpiresAt != nil && !consent.ExpiresAt.After(preparedAt)) || consent.WithdrawnAt != nil ||
		(consent.RedistributionScope != KnownScriptRedistributionPrivate && consent.RedistributionScope != KnownScriptRedistributionAllowed) ||
		consent.RetentionPolicy != KnownScriptRetentionWithdrawal || !consent.WithdrawalSupported || !consent.NoEndorsement ||
		!grants.Collection || !grants.PrivateStorage || !grants.TechnicalModification || !grants.EvidenceExtraction ||
		!grants.IndependentReview || !grants.HostedModelEvaluation {
		return fmt.Errorf("participant grant is incomplete, expired, withdrawn, or mismatched")
	}
	return nil
}

func validateKnownScriptTransformation(member KnownScriptMember, authoredAt, preparedAt time.Time) error {
	value := member.Transformation
	if !boundedID(value.RecipeID) || !validSHA256(value.RecipeSHA256) || value.RenderedAt.IsZero() ||
		value.RenderedAt.Before(member.Consent.SignedAt) || value.RenderedAt.After(authoredAt) || !validTool(value.Tool) ||
		value.MasterSHA256 != member.MasterAudio.SHA256 || value.OutputSHA256 != member.SelectedAudio.SHA256 {
		return fmt.Errorf("recipe, tool, time, master, or output binding is invalid")
	}
	hasMusic, err := validateKnownScriptAssets(value.Assets, preparedAt)
	if err != nil {
		return err
	}
	if slices.Contains(member.Slices, fillersafetycert.SliceMusicOverlap) && !hasMusic {
		return fmt.Errorf("music-overlap slice lacks a rights-cleared music asset")
	}
	return nil
}

func validateKnownScriptAssets(assets []KnownScriptAsset, at time.Time) (bool, error) {
	previous, hasMusic := "", false
	for _, asset := range assets {
		key := asset.Role + "\x00" + asset.Media.SHA256
		if (asset.Role != KnownScriptAssetMusic && asset.Role != KnownScriptAssetNoise) || key <= previous ||
			!validFileAuthority(asset.Media) || !validFileAuthority(asset.RightsEvidence) ||
			len(fillercorpus.HoldoutRightsHoldReasons(&asset.RightsContract, at)) != 0 {
			return false, fmt.Errorf("asset identity, order, or rights are invalid")
		}
		hasMusic = hasMusic || asset.Role == KnownScriptAssetMusic
		previous = key
	}
	return hasMusic, nil
}

func validateKnownScriptMapping(mapping KnownScriptPolicyMapping, member KnownScriptMember, policySHA256 string) error {
	if mapping.SchemaVersion != KnownScriptMappingSchemaVersion || mapping.ContractVersion != KnownScriptMappingContractVersion ||
		mapping.ScriptID != member.ScriptID || mapping.ScriptSHA256 != member.Script.SHA256 ||
		mapping.PolicySHA256 != policySHA256 || !slices.Equal(mapping.Intervals, member.PositiveIntervals) {
		return fmt.Errorf("known-script policy mapping does not bind the script, policy, and intervals")
	}
	return nil
}

func validateKnownScriptProcessorSchedule(schedule KnownScriptProcessorSchedule) error {
	if schedule.SchemaVersion != KnownScriptProcessorSchemaVersion ||
		schedule.ContractVersion != KnownScriptProcessorContractVersion || len(schedule.Processors) == 0 ||
		len(schedule.Processors) > 16 {
		return fmt.Errorf("known-script hosted processor schedule identity or count is invalid")
	}
	previous := ""
	for _, processor := range schedule.Processors {
		key := strings.Join([]string{
			processor.Kind, processor.SourceBaseURL, processor.RequestedModel, processor.ResolvedModel,
			processor.UpstreamProvider, processor.UpstreamProviderSlug,
		}, "\x00")
		if !validKnownScriptProcessor(processor) ||
			key <= previous {
			return fmt.Errorf("known-script hosted processor schedule contains an invalid, unsafe, or unsorted route")
		}
		previous = key
	}
	return nil
}

func validKnownScriptProcessor(processor KnownScriptHostedProcessor) bool {
	return processor.Kind == KnownScriptProcessorOpenRouter && validKnownScriptProcessorURL(processor.SourceBaseURL) &&
		validKnownScriptModel(processor.RequestedModel) && validKnownScriptModel(processor.ResolvedModel) &&
		boundedProcessorName(processor.UpstreamProvider) && boundedID(processor.UpstreamProviderSlug) && processor.ZDR
}

func validKnownScriptProcessorURL(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil &&
		parsed.Opaque == "" && parsed.RawQuery == "" && parsed.Fragment == "" && parsed.RawPath == "" &&
		!parsed.ForceQuery && parsed.String() == value
}

func validKnownScriptModel(value string) bool {
	provider, model, ok := strings.Cut(value, "/")
	return ok && provider != "" && model != "" && value == strings.TrimSpace(value) && len(value) <= 256 &&
		utf8.ValidString(value) && !strings.Contains(value, "\\") &&
		!strings.ContainsFunc(value, func(char rune) bool { return char <= ' ' || char == 0x7f }) &&
		!strings.Contains(strings.ToLower(value), "latest")
}

func boundedProcessorName(value string) bool {
	return value != "" && value == strings.TrimSpace(value) && len(value) <= 256 && utf8.ValidString(value) &&
		!strings.HasPrefix(value, "/") && !strings.Contains(value, "\\") &&
		!strings.ContainsFunc(value, func(char rune) bool { return char < ' ' || char == 0x7f })
}

func validateKnownScriptOwnerMap(value KnownScriptOwnerMap, cohort PreparedCohort, cohortSHA256 string) error {
	if value.SchemaVersion != PreparedCohortSchemaVersion || value.ContractVersion != KnownScriptOwnerMapContractVersion ||
		!value.PreparedAt.Equal(cohort.PreparedAt) || value.CohortSHA256 != cohortSHA256 ||
		len(value.Entries) != len(cohort.Cases) {
		return fmt.Errorf("known-script owner map identity or exact case count is invalid")
	}
	for index, entry := range value.Entries {
		item := cohort.Cases[index]
		if entry.CaseID != item.CaseID || entry.SourceFamily != item.SourceFamily || !boundedID(entry.ParticipantID) ||
			!boundedID(entry.SessionID) || !boundedID(entry.TakeID) || !boundedID(entry.ScriptID) ||
			!validRelative(entry.MasterAudioPath) || !validRelative(entry.SelectedAudioPath) {
			return fmt.Errorf("known-script owner map contains an invalid entry")
		}
	}
	return nil
}

func knownScriptPositiveSlices() []string {
	return []string{
		fillersafetycert.SliceAccentLocale,
		fillersafetycert.SliceClipping,
		fillersafetycert.SliceCodecTransform,
		fillersafetycert.SliceDerivativeCompilation,
		fillersafetycert.SliceMusicOverlap,
		fillersafetycert.SliceNoise,
		fillersafetycert.SlicePartialToken,
		fillersafetycert.SlicePhoneticConfusable,
		fillersafetycert.SlicePlacement,
		fillersafetycert.SliceQuietSpeech,
		fillersafetycert.SliceSpeedPitch,
	}
}

func pathInside(root, target string) bool {
	relative, err := filepath.Rel(root, target)
	return err == nil && (relative == "." || filepath.IsLocal(relative) && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}
