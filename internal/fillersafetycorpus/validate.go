package fillersafetycorpus

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/loomarr/loomarr/internal/fillercorpus"
	"github.com/loomarr/loomarr/internal/fillersafety"
)

var (
	vctkSpeakerID   = regexp.MustCompile(`^p[0-9]{3}$`)
	vctkUtteranceID = regexp.MustCompile(`^p[0-9]{3}_[0-9]{3}$`)
	vctkLocale      = regexp.MustCompile(`^[a-z]{2,3}(?:-[A-Z]{2})?$`)
)

func validatePrepareConfig(config PrepareVCTKConfig) error {
	if strings.TrimSpace(config.ReleaseAuthorityPath) == "" || strings.TrimSpace(config.ReleaseRoot) == "" ||
		strings.TrimSpace(config.SeedPath) == "" || strings.TrimSpace(config.FFmpegPath) == "" ||
		strings.TrimSpace(config.FFprobePath) == "" || !validSHA256(config.PolicySHA256) ||
		!boundedID(config.Implementation) || config.PreparedAt.IsZero() || config.ExpectedSpeakers != 100 ||
		config.MaximumInputBytes <= 0 || config.MaximumOutputBytes <= 0 || config.MaximumWallTime <= 0 ||
		strings.TrimSpace(config.OutputDirectory) == "" {
		return fmt.Errorf("VCTK preparation requires exact inputs, identities, 100 speakers, positive ceilings, and output")
	}
	return nil
}

func validateRelease(authority VCTKReleaseAuthority, preparedAt time.Time) error {
	if authority.SchemaVersion != VCTKReleaseSchemaVersion || authority.ContractVersion != VCTKReleaseContractVersion ||
		authority.ReleaseID != VCTKReleaseID || authority.ReleaseRecordURL != VCTKReleaseRecordURL ||
		!validSHA256(authority.ArchiveSHA256) || authority.ArchiveBytes <= 0 || authority.LicenseID != VCTKLicenseID ||
		!boundedID(authority.RightsReviewerID) || authority.RightsReviewedAt.IsZero() ||
		authority.RightsReviewedAt.After(preparedAt) || len(authority.Members) < 100 {
		return fmt.Errorf("VCTK release identity, rights review, or member population is invalid")
	}
	if reasons := fillercorpus.HoldoutRightsHoldReasons(&authority.RightsContract, preparedAt); len(reasons) != 0 {
		return fmt.Errorf("VCTK hosted-evaluation rights contract is incomplete: %s", strings.Join(reasons, ","))
	}
	for _, document := range []FileAuthority{authority.License, authority.Readme, authority.RightsReviewEvidence} {
		if !validRelative(document.Path) || !validSHA256(document.SHA256) || document.Bytes <= 0 {
			return fmt.Errorf("VCTK release evidence authority is invalid")
		}
	}
	previous := ""
	speakers, audio := map[string]struct{}{}, map[string]struct{}{}
	for _, member := range authority.Members {
		key := member.SpeakerID + "\x00" + member.UtteranceID + "\x00" + member.Microphone
		if !vctkSpeakerID.MatchString(member.SpeakerID) || member.SpeakerID == "p315" ||
			!vctkUtteranceID.MatchString(member.UtteranceID) || !strings.HasPrefix(member.UtteranceID, member.SpeakerID+"_") ||
			(member.Microphone != "mic1" && member.Microphone != "mic2") || !vctkLocale.MatchString(member.Locale) ||
			key <= previous || !validFileAuthority(member.Audio) || !validFileAuthority(member.Transcript) ||
			!validFileAuthority(member.ScreeningEvidence) {
			return fmt.Errorf("VCTK member identity, order, locale, or evidence is invalid")
		}
		if _, duplicate := audio[member.Audio.SHA256]; duplicate {
			return fmt.Errorf("VCTK release repeats audio content")
		}
		audio[member.Audio.SHA256], speakers[member.SpeakerID] = struct{}{}, struct{}{}
		previous = key
	}
	if len(speakers) < 100 {
		return fmt.Errorf("VCTK release has fewer than 100 eligible speakers")
	}
	return nil
}

func validFileAuthority(value FileAuthority) bool {
	return validRelative(value.Path) && validSHA256(value.SHA256) && value.Bytes > 0
}

func boundedID(value string) bool {
	return value != "" && value == strings.TrimSpace(value) && utf8.ValidString(value) && len(value) <= 128 &&
		!strings.ContainsAny(value, "/\\")
}

func validatePreparedCohort(value PreparedCohort, expected int) error {
	if err := validatePreparedCandidateCohort(value, expected, PreparedCohortKindCleanCandidate, VCTKDatasetID, value.PreparedAt); err != nil {
		return fmt.Errorf("prepared VCTK cohort: %w", err)
	}
	for _, item := range value.Cases {
		expectedRoot := "cases/" + item.CaseID
		if item.TranscriptPath != expectedRoot+"/transcript.txt" || !validSHA256(item.TranscriptSHA256) ||
			item.TranscriptBytes <= 0 || item.RightsPath != "evidence/release-authority.json" ||
			!item.SourceAuthority.MeasuredAt.Equal(value.PreparedAt) ||
			!slices.Equal(item.Slices, []string{VCTKTargetLocaleSlice}) {
			return fmt.Errorf("prepared VCTK cohort contains an invalid case")
		}
	}
	return nil
}

func validatePreparedCandidateCohort(value PreparedCohort, expected int, kind, dataset string, latest time.Time) error {
	if value.SchemaVersion != PreparedCohortSchemaVersion || value.ContractVersion != PreparedCohortContractVersion ||
		value.PreparedAt.IsZero() || value.PreparedAt.After(latest) || value.Kind != kind || value.Dataset != dataset ||
		(kind != PreparedCohortKindCleanCandidate && kind != PreparedCohortKindPositiveCandidate) || !boundedID(dataset) ||
		!validSHA256(value.ReleaseAuthoritySHA256) || !validSHA256(value.RecipeSHA256) || len(value.Cases) != expected ||
		!validTool(value.FFmpeg) || !validTool(value.FFprobe) {
		return fmt.Errorf("prepared candidate cohort identity or exact case count is invalid")
	}
	previous := ""
	families, sources := map[string]struct{}{}, map[string]struct{}{}
	policy, implementation := "", ""
	for _, item := range value.Cases {
		expectedRoot := "cases/" + item.CaseID
		validTranscript := item.TranscriptPath == "" && item.TranscriptSHA256 == "" && item.TranscriptBytes == 0
		validTranscript = validTranscript || (item.TranscriptPath == expectedRoot+"/transcript.txt" &&
			validSHA256(item.TranscriptSHA256) && item.TranscriptBytes > 0 && item.TranscriptBytes <= maximumTranscriptBytes)
		if !boundedID(item.CaseID) || item.CaseID <= previous || !boundedID(item.SourceFamily) ||
			item.SourceAuthority.SourceID != item.CaseID || item.SourceAuthority.PolicySHA256 == "" ||
			item.SourceAuthority.FFmpeg != value.FFmpeg || item.SourceAuthority.FFprobe != value.FFprobe ||
			item.SourceAuthority.MeasuredAt.After(value.PreparedAt) || item.SourcePath != expectedRoot+"/source.mp4" ||
			!validTranscript || item.TruthProvenancePath != expectedRoot+"/provenance.json" ||
			!validSHA256(item.TruthProvenanceSHA256) || item.TruthProvenanceBytes <= 0 ||
			item.TruthProvenanceBytes > maximumReleaseAuthorityBytes || !validRelative(item.RightsPath) ||
			!validSHA256(item.RightsSHA256) || item.RightsBytes <= 0 || item.RightsBytes > maximumReleaseAuthorityBytes ||
			item.Claim != kind || !vctkLocale.MatchString(item.Locale) || len(item.Slices) == 0 || len(item.Slices) > 8 ||
			!strictlySortedStrings(item.Slices) || !validPreparedIntervals(item.PositiveIntervals, item.SourceAuthority.DurationMS) ||
			(kind == PreparedCohortKindCleanCandidate && len(item.PositiveIntervals) != 0) ||
			(kind == PreparedCohortKindPositiveCandidate && len(item.PositiveIntervals) == 0) {
			return fmt.Errorf("prepared candidate cohort contains an invalid case")
		}
		if _, duplicate := families[item.SourceFamily]; duplicate {
			return fmt.Errorf("prepared candidate cohort repeats a source family")
		}
		if _, duplicate := sources[item.SourceAuthority.SourceSHA256]; duplicate {
			return fmt.Errorf("prepared candidate cohort repeats source content")
		}
		if _, err := fillersafety.SourceAuthoritySHA256(item.SourceAuthority); err != nil {
			return fmt.Errorf("prepared candidate cohort contains an invalid source authority")
		}
		if policy == "" {
			policy, implementation = item.SourceAuthority.PolicySHA256, item.SourceAuthority.Implementation
		} else if item.SourceAuthority.PolicySHA256 != policy || item.SourceAuthority.Implementation != implementation {
			return fmt.Errorf("prepared candidate cohort mixes policy or implementation identity")
		}
		families[item.SourceFamily], sources[item.SourceAuthority.SourceSHA256] = struct{}{}, struct{}{}
		previous = item.CaseID
	}
	return nil
}

func validPreparedIntervals(intervals []PreparedPositiveInterval, durationMS int64) bool {
	previousEnd := int64(0)
	for index, interval := range intervals {
		if !fillersafety.ValidPolicyRuleID(interval.RuleID) || interval.StartMS < 0 || interval.EndMS <= interval.StartMS ||
			interval.EndMS > durationMS || (index > 0 && interval.StartMS < previousEnd) {
			return false
		}
		previousEnd = interval.EndMS
	}
	return true
}

func strictlySortedStrings(values []string) bool {
	return slices.IsSorted(values) && len(slices.Compact(slices.Clone(values))) == len(values)
}

func validateOwnerMap(value VCTKOwnerMap, cohort PreparedCohort, cohortSHA string) error {
	if value.SchemaVersion != PreparedCohortSchemaVersion || value.ContractVersion != VCTKOwnerMapContractVersion ||
		!value.PreparedAt.Equal(cohort.PreparedAt) || value.CohortSHA256 != cohortSHA || len(value.Entries) != len(cohort.Cases) {
		return fmt.Errorf("VCTK owner map identity or exact case count is invalid")
	}
	for index, entry := range value.Entries {
		prepared := cohort.Cases[index]
		if entry.CaseID != prepared.CaseID || entry.SourceFamily != prepared.SourceFamily ||
			!vctkSpeakerID.MatchString(entry.SpeakerID) || entry.SpeakerID == "p315" ||
			!vctkUtteranceID.MatchString(entry.UtteranceID) || !strings.HasPrefix(entry.UtteranceID, entry.SpeakerID+"_") ||
			(entry.Microphone != "mic1" && entry.Microphone != "mic2") ||
			!validRelative(entry.AudioPath) || !validRelative(entry.TranscriptPath) {
			return fmt.Errorf("VCTK owner map contains an invalid or mismatched entry")
		}
	}
	return nil
}

func validTool(value fillersafety.ToolIdentity) bool {
	return value.Version != "" && value.Version == strings.TrimSpace(value.Version) && len(value.Version) <= 128 &&
		!strings.HasPrefix(value.Version, "/") && !strings.Contains(value.Version, "\\") && validSHA256(value.BinarySHA256)
}
