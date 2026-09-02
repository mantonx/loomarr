package fillersafetycert

import (
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/loomarr/loomarr/internal/fillersafety"
)

func validateAuthority(authority Authority) error {
	if authority.SchemaVersion != SchemaVersion || authority.ContractVersion != ContractVersion ||
		authority.AuthoredAt.IsZero() || !validSHA256(authority.CorpusManifestSHA256) ||
		!validSHA256(authority.PolicySHA256) || !validSHA256(authority.ProposerSHA256) ||
		!boundedID(authority.ProposerFamily) || !boundedID(authority.Implementation) ||
		(authority.ChallengeKind != ChallengeDevelopment && authority.ChallengeKind != ChallengeCertification) ||
		len(authority.Cases) == 0 {
		return fmt.Errorf("authority identity, time, or challenge kind is invalid")
	}
	if err := validateRoute(authority.AudioRoute, "spoken-safety", "native-audio", []string{"audio"}); err != nil {
		return fmt.Errorf("audio route: %w", err)
	}
	if err := validateRoute(authority.VideoRoute, "spoken-safety", "complete-video", []string{"audio", "video"}); err != nil {
		return fmt.Errorf("video route: %w", err)
	}
	excludedFamilies := map[string]struct{}{
		authority.ProposerFamily: {}, authority.AudioRoute.ModelFamily: {}, authority.VideoRoute.ModelFamily: {},
	}
	return validateAuthorityCases(authority.Cases, excludedFamilies)
}

func validateRoute(route RouteAuthority, role, rung string, modalities []string) error {
	if route.Role != role || route.Rung != rung || !slices.Equal(route.Modalities, modalities) ||
		!boundedID(route.RequestedProvider) || !boundedID(route.RequestedModel) ||
		!boundedID(route.ResolvedProvider) || !boundedID(route.ResolvedModel) ||
		!boundedID(route.UpstreamProvider) || !boundedID(route.ModelFamily) ||
		!validSHA256(route.CapabilitySHA256) || !validSHA256(route.PromptSHA256) || !validSHA256(route.SchemaSHA256) {
		return fmt.Errorf("route identity is incomplete or malformed")
	}
	return nil
}

func validateAuthorityCases(cases []AuthorityCase, excludedFamilies map[string]struct{}) error {
	aliases, sources, families, attestations := map[string]struct{}{}, map[string]struct{}{}, map[string]struct{}{}, map[string]struct{}{}
	positiveFamilies := 0
	positiveSlices, cleanSlices := map[string]struct{}{}, map[string]struct{}{}
	previousAlias := ""
	cleanSources := 0
	for _, item := range cases {
		if !validOpaqueID(item.Alias, "sc-") || item.Alias <= previousAlias || !validSHA256(item.SourceSHA256) ||
			!validSHA256(item.SourceAuthoritySHA256) || !validOpaqueID(item.SourceFamilyID, "family-") ||
			item.SourceBytes <= 0 || item.DurationMS <= 0 || !validSHA256(item.TruthProvenanceSHA256) ||
			!validSHA256(item.RightsSHA256) || !validLocale(item.Locale) || len(item.Slices) == 0 ||
			len(item.Slices) > 8 || !strictlySorted(item.Slices) {
			return fmt.Errorf("case identity, provenance, rights, locale, slices, or order is invalid")
		}
		previousAlias = item.Alias
		if _, duplicate := aliases[item.Alias]; duplicate {
			return fmt.Errorf("authority repeats a case alias")
		}
		if _, duplicate := sources[item.SourceSHA256]; duplicate {
			return fmt.Errorf("authority repeats source content")
		}
		if _, duplicate := families[item.SourceFamilyID]; duplicate {
			return fmt.Errorf("authority is not source-family-disjoint")
		}
		aliases[item.Alias], sources[item.SourceSHA256], families[item.SourceFamilyID] = struct{}{}, struct{}{}, struct{}{}
		if err := validateReviewers(item, excludedFamilies, attestations); err != nil {
			return fmt.Errorf("case %s reviewers: %w", item.Alias, err)
		}
		switch item.Label {
		case LabelPositive:
			positiveFamilies++
			if !validPositiveIntervals(item.PositiveIntervals, item.DurationMS) {
				return fmt.Errorf("positive case has invalid expected intervals")
			}
			for _, slice := range item.Slices {
				if !slices.Contains(requiredPositiveSlices(), slice) {
					return fmt.Errorf("positive case has an unknown slice")
				}
				positiveSlices[slice] = struct{}{}
			}
		case LabelClean:
			cleanSources++
			if len(item.PositiveIntervals) != 0 {
				return fmt.Errorf("clean case contains positive intervals")
			}
			for _, slice := range item.Slices {
				if !slices.Contains(requiredCleanSlices(), slice) {
					return fmt.Errorf("clean case has an unknown slice")
				}
				cleanSlices[slice] = struct{}{}
			}
		default:
			return fmt.Errorf("case label is invalid")
		}
	}
	if positiveFamilies < MinimumPositiveFamilies || cleanSources == 0 ||
		!coversExactly(positiveSlices, requiredPositiveSlices()) || !coversExactly(cleanSlices, requiredCleanSlices()) {
		return fmt.Errorf("authority requires at least %d positive families, clean sources, and complete declared slice coverage", MinimumPositiveFamilies)
	}
	return nil
}

func validateReviewers(item AuthorityCase, excludedFamilies, attestations map[string]struct{}) error {
	if len(item.Reviewers) < 2 || len(item.Reviewers) > 3 {
		return fmt.Errorf("two primary reviewers and at most one adjudicator are required")
	}
	ids := map[string]struct{}{}
	primary := make([]string, 0, 2)
	adjudicator := ""
	for _, reviewer := range item.Reviewers {
		if !validOpaqueID(reviewer.ReviewerID, "reviewer-") || !validSHA256(reviewer.AttestationSHA256) ||
			(reviewer.Decision != LabelPositive && reviewer.Decision != LabelClean) {
			return fmt.Errorf("reviewer identity, attestation, or decision is invalid")
		}
		if _, duplicate := ids[reviewer.ReviewerID]; duplicate {
			return fmt.Errorf("reviewer identity is repeated within a case")
		}
		if _, duplicate := attestations[reviewer.AttestationSHA256]; duplicate {
			return fmt.Errorf("attestation digest is reused")
		}
		ids[reviewer.ReviewerID], attestations[reviewer.AttestationSHA256] = struct{}{}, struct{}{}
		switch reviewer.Method {
		case ReviewerHuman:
			if reviewer.ModelFamily != "" {
				return fmt.Errorf("human reviewer declares a model family")
			}
		case ReviewerModel:
			if !boundedID(reviewer.ModelFamily) {
				return fmt.Errorf("model reviewer lacks a bounded family")
			}
			if _, excluded := excludedFamilies[reviewer.ModelFamily]; excluded {
				return fmt.Errorf("model reviewer is not family-independent")
			}
		default:
			return fmt.Errorf("reviewer method is invalid")
		}
		switch reviewer.Role {
		case ReviewerPrimary:
			primary = append(primary, reviewer.Decision)
		case ReviewerAdjudicator:
			if adjudicator != "" {
				return fmt.Errorf("multiple adjudicators are not allowed")
			}
			adjudicator = reviewer.Decision
		default:
			return fmt.Errorf("reviewer role is invalid")
		}
	}
	if len(primary) != 2 {
		return fmt.Errorf("exactly two primary reviewers are required")
	}
	if primary[0] == primary[1] {
		if adjudicator != "" || primary[0] != item.Label {
			return fmt.Errorf("agreeing primaries must establish the label without an adjudicator")
		}
		return nil
	}
	if adjudicator == "" || adjudicator != item.Label {
		return fmt.Errorf("disagreeing primaries require an adjudicator establishing the label")
	}
	return nil
}

func validPositiveIntervals(intervals []PositiveInterval, durationMS int64) bool {
	if len(intervals) == 0 {
		return false
	}
	var previousEnd int64
	for index, interval := range intervals {
		if !fillersafety.ValidPolicyRuleID(interval.RuleID) || interval.StartMS < 0 || interval.EndMS <= interval.StartMS ||
			interval.EndMS > durationMS || index > 0 && interval.StartMS < previousEnd {
			return false
		}
		previousEnd = interval.EndMS
	}
	return true
}

func requiredPositiveSlices() []string {
	return []string{SliceAccentLocale, SliceClipping, SliceCodecTransform, SliceDerivativeCompilation, SliceMusicOverlap, SlicePartialToken, SlicePhoneticConfusable, SliceQuietSpeech, SliceSpeedPitch}
}

func requiredCleanSlices() []string {
	return []string{SliceMusicOnly, SliceNearMatch, SliceTargetLocale, SliceWordless}
}

func coversExactly(observed map[string]struct{}, required []string) bool {
	if len(observed) != len(required) {
		return false
	}
	for _, value := range required {
		if _, ok := observed[value]; !ok {
			return false
		}
	}
	return true
}

func strictlySorted(values []string) bool {
	return slices.IsSorted(values) && len(slices.Compact(slices.Clone(values))) == len(values)
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && strings.ToLower(value) == value
}

func validOpaqueID(value, prefix string) bool {
	return len(value) == len(prefix)+24 && strings.HasPrefix(value, prefix) && validLowerHex(value[len(prefix):])
}

func validLowerHex(value string) bool {
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return value != ""
}

func boundedID(value string) bool {
	if value == "" || value != strings.TrimSpace(value) || len(value) > 128 || !utf8.ValidString(value) ||
		strings.HasPrefix(value, "/") || strings.Contains(value, "\\") {
		return false
	}
	return !strings.ContainsFunc(value, func(char rune) bool { return char <= ' ' || char == 0x7f })
}

func validLocale(value string) bool {
	if len(value) < 2 || len(value) > 35 || value[0] == '-' || value[len(value)-1] == '-' {
		return false
	}
	for _, char := range value {
		if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (char < '0' || char > '9') && char != '-' {
			return false
		}
	}
	return true
}
