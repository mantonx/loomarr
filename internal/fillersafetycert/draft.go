package fillersafetycert

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
)

// MarshalCertificationDraft validates the complete pre-review certification
// corpus and returns its canonical private bytes and digest. It establishes no
// truth; BuildAuthority still requires independent reviews over these bytes.
func MarshalCertificationDraft(draft AuthorityDraft) ([]byte, string, error) {
	if draft.ChallengeKind != ChallengeCertification {
		return nil, "", fmt.Errorf("reviewable authority draft must be a certification challenge")
	}
	if err := validateAuthorityDraft(draft, len(draft.Cases)); err != nil {
		return nil, "", err
	}
	if err := validateCertificationDraftCoverage(draft.Cases); err != nil {
		return nil, "", err
	}
	raw, err := json.MarshalIndent(draft, "", "  ")
	if err != nil {
		return nil, "", err
	}
	raw = append(raw, '\n')
	sum := sha256.Sum256(raw)
	return raw, hex.EncodeToString(sum[:]), nil
}

func validateCertificationDraftCoverage(cases []AuthorityDraftCase) error {
	positiveFamilies, cleanFamilies := 0, 0
	positiveSlices, cleanSlices := map[string]struct{}{}, map[string]struct{}{}
	for _, item := range cases {
		switch item.Label {
		case LabelPositive:
			positiveFamilies++
			for _, slice := range item.Slices {
				if !slices.Contains(requiredPositiveSlices(), slice) {
					return fmt.Errorf("certification draft positive case has an unknown slice")
				}
				positiveSlices[slice] = struct{}{}
			}
		case LabelClean:
			cleanFamilies++
			for _, slice := range item.Slices {
				if !slices.Contains(requiredCleanSlices(), slice) {
					return fmt.Errorf("certification draft clean case has an unknown slice")
				}
				cleanSlices[slice] = struct{}{}
			}
		default:
			return fmt.Errorf("certification draft case has an invalid label")
		}
	}
	if positiveFamilies < MinimumPositiveFamilies || cleanFamilies < MinimumCleanFamilies ||
		!coversExactly(positiveSlices, requiredPositiveSlices()) || !coversExactly(cleanSlices, requiredCleanSlices()) {
		return fmt.Errorf("certification draft requires at least %d positive families, at least %d clean families, and complete declared slice coverage", MinimumPositiveFamilies, MinimumCleanFamilies)
	}
	return nil
}
