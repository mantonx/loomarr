package filler

import (
	"fmt"
	"sort"
	"strings"
)

// PlanAcquisition applies hard constraints first, then selects a diverse stable prefix.
func PlanAcquisition(intent AcquisitionIntent, candidates []AcquisitionCandidate, existing map[string]ExistingRemoteState) (AcquisitionPlan, error) {
	intent = intent.Normalize()
	if err := intent.Validate(); err != nil {
		return AcquisitionPlan{}, fmt.Errorf("%w: %v", ErrInvalidAcquisitionIntent, err)
	}
	plan := AcquisitionPlan{Intent: intent, Selected: []AcquisitionDecision{}, Rejected: []AcquisitionDecision{}}
	eligible := make([]AcquisitionDecision, 0, len(candidates))
	seen := make(map[string]bool, len(candidates))
	for _, candidate := range candidates {
		decision := AcquisitionDecision{Candidate: normalizeCandidate(candidate)}
		key := decision.Candidate.Identity.Key()
		if err := decision.Candidate.Identity.Validate(); err != nil || strings.TrimSpace(decision.Candidate.URL) == "" {
			decision.Disposition, decision.Detail = CandidateDuplicateRemote, "candidate has no actionable registered identity or URL"
		} else if seen[key] {
			decision.Disposition, decision.Detail = CandidateDuplicateRemote, "the same provider item appeared more than once"
		} else if state := existing[key]; state != "" {
			decision.Disposition, decision.Detail = dispositionForExisting(state), "the exact registered provider item was already "+string(state)
		} else if disposition, detail := rejectByIntent(intent, decision.Candidate); disposition != "" {
			decision.Disposition, decision.Detail = disposition, detail
		} else {
			eligible = append(eligible, decision)
		}
		seen[key] = true
		if decision.Disposition != "" {
			plan.Rejected = append(plan.Rejected, decision)
		}
	}

	usedSources := map[string]bool{}
	usedYears := map[int]bool{}
	for len(eligible) > 0 && len(plan.Selected) < intent.Count {
		sort.SliceStable(eligible, func(a, b int) bool {
			return candidateBetter(intent, eligible[a].Candidate, eligible[b].Candidate, usedSources, usedYears)
		})
		decision := eligible[0]
		eligible = eligible[1:]
		decision.Disposition = CandidateSelected
		decision.Detail = "selected by deterministic rights, quality, diversity, and identity ranking"
		plan.Selected = append(plan.Selected, decision)
		usedSources[decision.Candidate.Identity.SourceID] = true
		if decision.Candidate.ObservedYear > 0 {
			usedYears[decision.Candidate.ObservedYear] = true
		}
	}
	for _, decision := range eligible {
		decision.Disposition = CandidateRankedBelowLimit
		decision.Detail = fmt.Sprintf("ranked outside the requested %d items", intent.Count)
		plan.Rejected = append(plan.Rejected, decision)
	}
	sort.SliceStable(plan.Rejected, func(a, b int) bool {
		return plan.Rejected[a].Candidate.Identity.Key() < plan.Rejected[b].Candidate.Identity.Key()
	})
	return plan, nil
}

// DefaultAcquisitionIntent derives its rationale from the same pool projection the UI reads.
func DefaultAcquisitionIntent(pool PoolReport, geography Geography) AcquisitionIntent {
	reason := "Increase the eligible filler catalog."
	if weakest := pool.Weakest(); weakest != nil {
		reason = fmt.Sprintf("Improve filler coverage for %s; its current match level is %s.", weakest.Name, weakest.Report.Level)
	}
	return AcquisitionIntent{
		Version: AcquisitionIntentVersion, Geography: geography.Normalize(),
		Rights: RightsPreferDeclared, Count: 12, CatalogReason: reason,
	}
}

func rejectByIntent(intent AcquisitionIntent, c AcquisitionCandidate) (CandidateDisposition, string) {
	if len(intent.SourceAllowlist) > 0 && !containsFold(intent.SourceAllowlist, c.Identity.SourceID) {
		return CandidateSourceNotAllowed, "registered source is outside the intent allow-list"
	}
	if intent.Geography.Country != "" && !SourceGeographicallyEligible(c.Geography, intent.Geography) {
		return CandidateGeographyMismatch, "candidate geography does not cover the target"
	}
	if intent.Rights == RightsRequireDeclared && strings.TrimSpace(c.License) == "" {
		return CandidateRightsUnknown, "the provider supplied no rights declaration"
	}
	if intent.EraStart > 0 || intent.EraEnd > 0 {
		if c.ObservedYear == 0 {
			return CandidateEraUnknown, "the provider supplied no year observation"
		}
		if (intent.EraStart > 0 && c.ObservedYear < intent.EraStart) || (intent.EraEnd > 0 && c.ObservedYear > intent.EraEnd) {
			return CandidateEraMismatch, fmt.Sprintf("observed year %d is outside the requested range", c.ObservedYear)
		}
	}
	if intent.MaxDurationMS > 0 {
		if c.DurationMS == 0 {
			return CandidateDurationUnknown, "the provider supplied no duration"
		}
		if c.DurationMS > intent.MaxDurationMS {
			return CandidateDurationExceeded, fmt.Sprintf("remote duration %dms exceeds the ceiling", c.DurationMS)
		}
	}
	if intent.MinHeight > 0 {
		if c.Height == 0 {
			return CandidateQualityUnknown, "the provider supplied no video height"
		}
		if c.Height < intent.MinHeight {
			return CandidateQualityBelowFloor, fmt.Sprintf("remote height %dp is below the floor", c.Height)
		}
	}
	if len(intent.Roles) > 0 {
		if len(c.ObservedRoles) == 0 {
			return CandidateRoleUnknown, "the provider supplied no content-role observation"
		}
		if !kindOverlap(intent.Roles, c.ObservedRoles) {
			return CandidateRoleMismatch, "observed content role does not match the intent"
		}
	}
	if len(intent.Audiences) > 0 {
		if len(c.Audiences) == 0 {
			return CandidateAudienceUnknown, "the provider supplied no audience observation"
		}
		if !audienceOverlap(intent.Audiences, c.Audiences) {
			return CandidateAudienceMismatch, "observed audience does not match the intent"
		}
	}
	if len(intent.TaxonomyGaps) > 0 {
		if len(c.Taxonomy) == 0 {
			return CandidateTaxonomyUnknown, "the provider supplied no taxonomy observation"
		}
		if !stringOverlap(intent.TaxonomyGaps, c.Taxonomy) {
			return CandidateTaxonomyMismatch, "observed taxonomy does not address the requested gap"
		}
	}
	return "", ""
}

func candidateBetter(intent AcquisitionIntent, a, b AcquisitionCandidate, usedSources map[string]bool, usedYears map[int]bool) bool {
	var av, bv bool
	if intent.Rights == RightsPreferDeclared {
		av, bv = strings.TrimSpace(a.License) != "", strings.TrimSpace(b.License) != ""
		if av != bv {
			return av
		}
	}
	if a.Height != b.Height {
		return a.Height > b.Height
	}
	av, bv = !usedSources[a.Identity.SourceID], !usedSources[b.Identity.SourceID]
	if av != bv {
		return av
	}
	av, bv = a.ObservedYear > 0 && !usedYears[a.ObservedYear], b.ObservedYear > 0 && !usedYears[b.ObservedYear]
	if av != bv {
		return av
	}
	return a.Identity.Key() < b.Identity.Key()
}

func dispositionForExisting(state ExistingRemoteState) CandidateDisposition {
	switch state {
	case RemoteCatalogued:
		return CandidateAlreadyCatalogued
	case RemoteQueued:
		return CandidateAlreadyQueued
	case RemoteDeclined:
		return CandidatePreviouslyDeclined
	default:
		return CandidateDuplicateRemote
	}
}

func normalizeCandidate(c AcquisitionCandidate) AcquisitionCandidate {
	c.Identity.Provider = strings.ToLower(strings.TrimSpace(c.Identity.Provider))
	c.Identity.SourceID = strings.TrimSpace(c.Identity.SourceID)
	c.Identity.RemoteID = strings.TrimSpace(c.Identity.RemoteID)
	c.URL = strings.TrimSpace(c.URL)
	c.Title = strings.TrimSpace(c.Title)
	c.License = strings.TrimSpace(c.License)
	c.Geography = c.Geography.Normalize()
	c.ObservedRoles = uniqueKinds(c.ObservedRoles)
	c.Audiences = uniqueAudiences(c.Audiences)
	c.Taxonomy = uniqueStrings(c.Taxonomy)
	return c
}

func containsFold(haystack []string, needle string) bool {
	for _, value := range haystack {
		if strings.EqualFold(value, needle) {
			return true
		}
	}
	return false
}

func kindOverlap(a, b []Kind) bool {
	for _, left := range a {
		for _, right := range b {
			if left == right {
				return true
			}
		}
	}
	return false
}

func audienceOverlap(a, b []Audience) bool {
	for _, left := range a {
		for _, right := range b {
			if left == right {
				return true
			}
		}
	}
	return false
}

func stringOverlap(a, b []string) bool {
	for _, left := range a {
		if containsFold(b, left) {
			return true
		}
	}
	return false
}
