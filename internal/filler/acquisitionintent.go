package filler

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// AcquisitionIntentVersion is persisted with every pull so a later ranking change never
// rewrites what an operator was shown.
const AcquisitionIntentVersion = "filler-acquisition-intent/v1"

var (
	ErrInvalidAcquisitionIntent = errors.New("invalid filler acquisition intent")
	ErrNoAcquisitionSources     = errors.New("no eligible filler acquisition sources")
	ErrNoAcquisitionCandidates  = errors.New("no filler acquisition candidates satisfy the intent")
)

// RightsPreference says how declared remote rights metadata affects acquisition selection.
// A declaration is still evidence supplied by the source, not a legal conclusion.
type RightsPreference string

const (
	RightsAny             RightsPreference = "any"
	RightsPreferDeclared  RightsPreference = "prefer_declared"
	RightsRequireDeclared RightsPreference = "require_declared"
)

// AcquisitionIntent is the closed, inspectable request a deterministic planner satisfies.
// Zero values mean the operator did not request that constraint; they never mean that missing
// candidate metadata satisfies it.
type AcquisitionIntent struct {
	Version         string           `json:"version"`
	Roles           []Kind           `json:"roles,omitempty"`
	EraStart        int              `json:"eraStart,omitempty"`
	EraEnd          int              `json:"eraEnd,omitempty"`
	Audiences       []Audience       `json:"audiences,omitempty"`
	Geography       Geography        `json:"geography,omitempty"`
	MaxDurationMS   int              `json:"maxDurationMs,omitempty"`
	TaxonomyGaps    []string         `json:"taxonomyGaps,omitempty"`
	Rights          RightsPreference `json:"rights"`
	SourceAllowlist []string         `json:"sourceAllowlist,omitempty"`
	MinHeight       int              `json:"minHeight,omitempty"`
	Count           int              `json:"count"`
	CatalogReason   string           `json:"catalogReason"`
}

// Normalize supplies version/defaults and canonicalises set-like fields. It does not relax a
// requested constraint.
func (i AcquisitionIntent) Normalize() AcquisitionIntent {
	if i.Version == "" {
		i.Version = AcquisitionIntentVersion
	}
	if i.Count == 0 {
		i.Count = 12
	}
	if i.Rights == "" {
		i.Rights = RightsPreferDeclared
	}
	i.Geography = i.Geography.Normalize()
	i.CatalogReason = strings.TrimSpace(i.CatalogReason)
	i.Roles = uniqueKinds(i.Roles)
	i.Audiences = uniqueAudiences(i.Audiences)
	i.TaxonomyGaps = uniqueStrings(i.TaxonomyGaps)
	i.SourceAllowlist = uniqueStrings(i.SourceAllowlist)
	return i
}

func (i AcquisitionIntent) Validate() error {
	i = i.Normalize()
	if i.Version != AcquisitionIntentVersion {
		return fmt.Errorf("unsupported acquisition intent version %q", i.Version)
	}
	if (i.EraStart != 0 && (i.EraStart < 1800 || i.EraStart > 2200)) ||
		(i.EraEnd != 0 && (i.EraEnd < 1800 || i.EraEnd > 2200)) ||
		(i.EraStart > 0 && i.EraEnd > 0 && i.EraStart > i.EraEnd) {
		return fmt.Errorf("invalid era observation range %d-%d", i.EraStart, i.EraEnd)
	}
	if i.MaxDurationMS < 0 || i.MinHeight < 0 || i.MinHeight > 4320 {
		return fmt.Errorf("invalid duration ceiling or representation-quality floor")
	}
	if i.Count < 1 || i.Count > 50 {
		return fmt.Errorf("requested item count %d is outside 1-50", i.Count)
	}
	if err := i.Geography.Validate(); err != nil {
		return fmt.Errorf("acquisition geography: %w", err)
	}
	switch i.Rights {
	case RightsAny, RightsPreferDeclared, RightsRequireDeclared:
	default:
		return fmt.Errorf("unknown rights preference %q", i.Rights)
	}
	for _, role := range i.Roles {
		if !validKind(role) {
			return fmt.Errorf("unknown content role %q", role)
		}
	}
	for _, audience := range i.Audiences {
		if !validAudience(audience) {
			return fmt.Errorf("unknown audience %q", audience)
		}
	}
	return nil
}

// FamilyKey groups decisions made under the same semantic constraints. Presentation-only reason
// text and requested count do not change whether a previously declined item should be proposed
// again; every selection constraint does.
func (i AcquisitionIntent) FamilyKey() string {
	i = i.Normalize()
	i.Count = 0
	i.CatalogReason = ""
	b, _ := json.Marshal(i) // AcquisitionIntent contains no values json.Marshal can reject.
	return fmt.Sprintf("intent_%x", sha256.Sum256(b))
}

// RemoteIdentity is the stable identity of one provider item inside one registered source.
// URL is deliberately absent: it is mutable transport payload, not identity.
type RemoteIdentity struct {
	Provider string `json:"provider"`
	SourceID string `json:"sourceId"`
	RemoteID string `json:"remoteId"`
}

func (i RemoteIdentity) Key() string {
	return strings.ToLower(strings.TrimSpace(i.Provider)) + "\x00" +
		strings.TrimSpace(i.SourceID) + "\x00" + strings.TrimSpace(i.RemoteID)
}

// Token is a JSON/URL-safe stable handle for operator edits. The canonical key contains NUL
// separators to avoid ambiguous concatenation, so it is not exposed directly on the wire.
func (i RemoteIdentity) Token() string {
	return fmt.Sprintf("candidate_%x", sha256.Sum256([]byte(i.Key())))
}

func (i RemoteIdentity) Validate() error {
	if strings.TrimSpace(i.Provider) == "" || strings.TrimSpace(i.SourceID) == "" || strings.TrimSpace(i.RemoteID) == "" {
		return fmt.Errorf("remote identity requires provider, source, and item ids")
	}
	return nil
}

// AcquisitionCandidate contains metadata-only observations. ObservedYear and PublishedAt are
// weak remote hints and must never be copied into an admitted clip as grounded era evidence.
type AcquisitionCandidate struct {
	Identity      RemoteIdentity `json:"identity"`
	URL           string         `json:"url"`
	Title         string         `json:"title,omitempty"`
	License       string         `json:"license,omitempty"`
	ObservedYear  int            `json:"observedYear,omitempty"`
	PublishedAt   string         `json:"publishedAt,omitempty"`
	DurationMS    int            `json:"durationMs,omitempty"`
	Height        int            `json:"height,omitempty"`
	Geography     Geography      `json:"geography,omitempty"`
	ObservedRoles []Kind         `json:"observedRoles,omitempty"`
	Audiences     []Audience     `json:"audiences,omitempty"`
	Taxonomy      []string       `json:"taxonomy,omitempty"`
}

// CandidateDisposition is a stable explanation for a selected or excluded remote item.
type CandidateDisposition string

const (
	CandidateSelected           CandidateDisposition = "selected"
	CandidateAlreadyCatalogued  CandidateDisposition = "already_catalogued"
	CandidateAlreadyQueued      CandidateDisposition = "already_queued"
	CandidatePreviouslyDeclined CandidateDisposition = "previously_declined"
	CandidateDuplicateRemote    CandidateDisposition = "duplicate_remote"
	CandidateSourceNotAllowed   CandidateDisposition = "source_not_allowed"
	CandidateGeographyMismatch  CandidateDisposition = "geography_mismatch"
	CandidateRightsUnknown      CandidateDisposition = "rights_unknown"
	CandidateEraUnknown         CandidateDisposition = "era_unknown"
	CandidateEraMismatch        CandidateDisposition = "era_mismatch"
	CandidateDurationUnknown    CandidateDisposition = "duration_unknown"
	CandidateDurationExceeded   CandidateDisposition = "duration_exceeded"
	CandidateQualityUnknown     CandidateDisposition = "quality_unknown"
	CandidateQualityBelowFloor  CandidateDisposition = "quality_below_floor"
	CandidateRoleUnknown        CandidateDisposition = "role_unknown"
	CandidateRoleMismatch       CandidateDisposition = "role_mismatch"
	CandidateAudienceUnknown    CandidateDisposition = "audience_unknown"
	CandidateAudienceMismatch   CandidateDisposition = "audience_mismatch"
	CandidateTaxonomyUnknown    CandidateDisposition = "taxonomy_unknown"
	CandidateTaxonomyMismatch   CandidateDisposition = "taxonomy_mismatch"
	CandidateRankedBelowLimit   CandidateDisposition = "ranked_below_limit"
)

type ExistingRemoteState string

const (
	RemoteCatalogued ExistingRemoteState = "catalogued"
	RemoteQueued     ExistingRemoteState = "queued"
	RemoteDeclined   ExistingRemoteState = "declined"
)

type AcquisitionDecision struct {
	Candidate   AcquisitionCandidate `json:"candidate"`
	Disposition CandidateDisposition `json:"disposition"`
	Detail      string               `json:"detail"`
}

type AcquisitionPlan struct {
	Intent   AcquisitionIntent           `json:"intent"`
	Selected []AcquisitionDecision       `json:"selected"`
	Rejected []AcquisitionDecision       `json:"rejected"`
	Sources  []AcquisitionSourceDecision `json:"sources"`
}

// AcquisitionSourceDisposition explains whether and how one registered source participated in
// planning. Source decisions are separate from candidate decisions because a disabled or failed
// source has no candidate that could honestly carry the explanation.
type AcquisitionSourceDisposition string

const (
	AcquisitionSourceEnumerated        AcquisitionSourceDisposition = "enumerated"
	AcquisitionSourceDisabled          AcquisitionSourceDisposition = "disabled"
	AcquisitionSourceNotFetchable      AcquisitionSourceDisposition = "not_fetchable"
	AcquisitionSourceNotAllowed        AcquisitionSourceDisposition = "not_allowed"
	AcquisitionSourceGeographyMismatch AcquisitionSourceDisposition = "geography_mismatch"
	AcquisitionSourceLimitExceeded     AcquisitionSourceDisposition = "source_limit"
	AcquisitionSourceEnumerationFailed AcquisitionSourceDisposition = "enumeration_failed"
)

// AcquisitionSourceDecision is the durable, bounded source snapshot behind a pull proposal.
type AcquisitionSourceDecision struct {
	SourceID       string                       `json:"sourceId"`
	Provider       string                       `json:"provider"`
	Label          string                       `json:"label,omitempty"`
	Geography      Geography                    `json:"geography,omitempty"`
	Disposition    AcquisitionSourceDisposition `json:"disposition"`
	CandidateCount int                          `json:"candidateCount"`
	Detail         string                       `json:"detail"`
}

func uniqueStrings(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, value := range in {
		value = strings.TrimSpace(value)
		key := strings.ToLower(value)
		if value != "" && !seen[key] {
			seen[key] = true
			out = append(out, value)
		}
	}
	sort.Slice(out, func(a, b int) bool { return strings.ToLower(out[a]) < strings.ToLower(out[b]) })
	return out
}

func uniqueKinds(in []Kind) []Kind {
	out := make([]Kind, 0, len(in))
	seen := map[Kind]bool{}
	for _, value := range in {
		if value != "" && !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a] < out[b] })
	return out
}

func uniqueAudiences(in []Audience) []Audience {
	out := make([]Audience, 0, len(in))
	seen := map[Audience]bool{}
	for _, value := range in {
		if value != "" && !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a] < out[b] })
	return out
}

func validKind(value Kind) bool {
	switch value {
	case Commercial, Bumper, StationID, PSA, Trailer, Interstitial:
		return true
	default:
		return false
	}
}

func validAudience(value Audience) bool {
	switch value {
	case Kids, Family, General, LateNight:
		return true
	default:
		return false
	}
}
