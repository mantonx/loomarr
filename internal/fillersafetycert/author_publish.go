package fillersafetycert

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/loomarr/loomarr/internal/fillersafety"
)

// BuildAuthority validates private source bytes, provenance, rights, and
// independent reviews, then publishes one path-free certification authority.
// It performs no transformation, inference, provider call, or evaluation.
func BuildAuthority(ctx context.Context, config AuthorityBuildConfig) (AuthorityBuildResult, error) {
	if ctx == nil || ctx.Err() != nil {
		return AuthorityBuildResult{}, fmt.Errorf("authority build requires an active context")
	}
	inputs, err := loadAuthorityInputs(config)
	if err != nil {
		return AuthorityBuildResult{}, err
	}
	if err := validateAuthorityDraft(inputs.draft, config.ExpectedCases); err != nil {
		return AuthorityBuildResult{}, err
	}
	reviews, err := validateAuthorityReviews(inputs, config.AuthoredAt)
	if err != nil {
		return AuthorityBuildResult{}, err
	}
	if err := validateAuthorityEvidence(ctx, inputs, config); err != nil {
		return AuthorityBuildResult{}, err
	}
	authority := Authority{
		SchemaVersion: SchemaVersion, ContractVersion: ContractVersion, AuthoredAt: config.AuthoredAt.UTC(),
		ChallengeKind: inputs.draft.ChallengeKind, CorpusManifestSHA256: inputs.draftSHA,
		PolicySHA256: inputs.draft.PolicySHA256, ProposerSHA256: inputs.draft.ProposerSHA256,
		ProposerFamily: inputs.draft.ProposerFamily, Implementation: inputs.draft.Implementation,
		AudioRoute: inputs.draft.AudioRoute, VideoRoute: inputs.draft.VideoRoute,
	}
	var aggregateBytes int64
	positive, clean := 0, 0
	for index, item := range inputs.draft.Cases {
		if err := ctx.Err(); err != nil {
			return AuthorityBuildResult{}, err
		}
		if item.SourceAuthority.MeasuredAt.After(config.AuthoredAt) ||
			inputs.first.SubmittedAt.Before(item.SourceAuthority.MeasuredAt) ||
			inputs.second.SubmittedAt.Before(item.SourceAuthority.MeasuredAt) ||
			(inputs.adjudicator != nil && inputs.adjudicator.SubmittedAt.Before(item.SourceAuthority.MeasuredAt)) {
			return AuthorityBuildResult{}, fmt.Errorf("authority case %d has pre-measurement review or post-authoring measurement", index+1)
		}
		if item.SourceAuthority.SourceBytes > config.MaximumSourceBytes-aggregateBytes {
			return AuthorityBuildResult{}, fmt.Errorf("authority sources exceed aggregate byte ceiling")
		}
		aggregateBytes += item.SourceAuthority.SourceBytes
		sourcePath, err := resolvePrivateRelative(inputs.sourceRoot, item.SourcePath)
		if err != nil {
			return AuthorityBuildResult{}, fmt.Errorf("authority case %d source path is invalid", index+1)
		}
		plan, err := fillersafety.PlanCompleteMedia(ctx, fillersafety.SourceRequest{Authority: item.SourceAuthority, Path: sourcePath})
		if err != nil {
			return AuthorityBuildResult{}, fmt.Errorf("authority case %d source bytes do not match authority", index+1)
		}
		sourceAuthoritySHA := plan.AuthoritySHA256
		if err := plan.Close(); err != nil {
			return AuthorityBuildResult{}, fmt.Errorf("authority case %d source snapshot cleanup failed", index+1)
		}
		truthSHA, err := hashPrivateEvidence(inputs.sourceRoot, item.TruthProvenancePath)
		if err != nil || truthSHA != item.TruthProvenanceSHA256 {
			return AuthorityBuildResult{}, fmt.Errorf("authority case %d truth provenance is invalid", index+1)
		}
		rightsSHA, err := hashPrivateEvidence(inputs.sourceRoot, item.RightsPath)
		if err != nil || rightsSHA != item.RightsSHA256 {
			return AuthorityBuildResult{}, fmt.Errorf("authority case %d rights evidence is invalid", index+1)
		}
		caseReviewers, err := bindCaseReviews(inputs, reviews, item)
		if err != nil {
			return AuthorityBuildResult{}, fmt.Errorf("authority case %d reviews do not establish declared truth", index+1)
		}
		authority.Cases = append(authority.Cases, AuthorityCase{
			Alias:        opaqueID(inputs.seed, "case", item.CaseID, "sc-"),
			SourceSHA256: item.SourceAuthority.SourceSHA256, SourceAuthoritySHA256: sourceAuthoritySHA,
			SourceFamilyID: opaqueID(inputs.seed, "family", item.SourceFamily, "family-"),
			SourceBytes:    item.SourceAuthority.SourceBytes, DurationMS: item.SourceAuthority.DurationMS,
			TruthProvenanceSHA256: truthSHA, RightsSHA256: rightsSHA, Label: item.Label,
			Locale: item.Locale, Slices: slices.Clone(item.Slices), PositiveIntervals: slices.Clone(item.PositiveIntervals),
			Reviewers: caseReviewers,
		})
		if item.Label == LabelPositive {
			positive++
		} else {
			clean++
		}
	}
	slices.SortFunc(authority.Cases, func(a, b AuthorityCase) int { return strings.Compare(a.Alias, b.Alias) })
	if err := validateAuthority(authority); err != nil {
		return AuthorityBuildResult{}, fmt.Errorf("validate built cascade certification authority: %w", err)
	}
	raw, err := json.MarshalIndent(authority, "", "  ")
	if err != nil {
		return AuthorityBuildResult{}, err
	}
	raw = append(raw, '\n')
	if err := writePrivateNew(config.OutputPath, raw); err != nil {
		return AuthorityBuildResult{}, fmt.Errorf("publish cascade certification authority: %w", err)
	}
	return AuthorityBuildResult{
		Cases: len(authority.Cases), PositiveFamilies: positive, CleanFamilies: clean,
		AuthoritySHA256: hashBytes(raw),
	}, nil
}

func bindCaseReviews(inputs loadedAuthorityInputs, reviews validatedReviews, item AuthorityDraftCase) ([]ReviewerAttestation, error) {
	first, second := reviews.first[item.CaseID], reviews.second[item.CaseID]
	result := []ReviewerAttestation{
		makeReviewerAttestation(inputs.seed, inputs.first, first),
		makeReviewerAttestation(inputs.seed, inputs.second, second),
	}
	slices.SortFunc(result, func(a, b ReviewerAttestation) int { return strings.Compare(a.ReviewerID, b.ReviewerID) })
	if first.Decision == second.Decision {
		if first.Decision != ReviewDecisionVerified {
			return nil, fmt.Errorf("agreeing primary reviews reject declared truth")
		}
		return result, nil
	}
	adjudication, ok := reviews.adjudicator[item.CaseID]
	if !ok || adjudication.Decision != ReviewDecisionVerified || inputs.adjudicator == nil {
		return nil, fmt.Errorf("adjudication does not establish declared truth")
	}
	return append(result, makeReviewerAttestation(inputs.seed, *inputs.adjudicator, adjudication)), nil
}

func makeReviewerAttestation(seed []byte, review AuthorityReview, assessment ReviewAssessment) ReviewerAttestation {
	return ReviewerAttestation{
		ReviewerID: opaqueID(seed, "reviewer", review.ReviewerID, "reviewer-"), Role: review.Role,
		Method: review.Method, ModelFamily: review.ModelFamily, Decision: assessment.Decision,
		EvidenceSHA256:    review.EvidenceSHA256,
		AttestationSHA256: reviewAttestationSHA256(review, assessment),
	}
}

func opaqueID(seed []byte, domain, value, prefix string) string {
	hash := hmac.New(sha256.New, seed)
	_, _ = hash.Write([]byte(domain))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(value))
	return prefix + hex.EncodeToString(hash.Sum(nil))[:24]
}
