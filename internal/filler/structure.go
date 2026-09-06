package filler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"
)

const (
	SourceStructureSchemaVersion   = 1
	SourceStructureContractVersion = "filler-source-structure-v1"
)

// SourceStructureKind answers what the complete source contains. Duration is deliberately absent:
// it can quarantine a source for assessment, but cannot establish any of these semantic claims.
type SourceStructureKind string

const (
	StructureSingleUnit       SourceStructureKind = "single_unit"
	StructureCompilationBreak SourceStructureKind = "compilation_break"
	StructureProgrammeSpots   SourceStructureKind = "programme_with_spots"
	StructureAmbiguous        SourceStructureKind = "ambiguous"
	StructureUnusable         SourceStructureKind = "unusable"
)

type StructureObservationKind string

const (
	ObservationChapterEdge      StructureObservationKind = "chapter_edge"
	ObservationBlackInterval    StructureObservationKind = "black_interval"
	ObservationSilenceInterval  StructureObservationKind = "silence_interval"
	ObservationTranscriptChange StructureObservationKind = "transcript_topic_change"
	ObservationOCRLogoChange    StructureObservationKind = "ocr_logo_change"
	ObservationAudioContinuity  StructureObservationKind = "audio_continuity"
	ObservationVisualContinuity StructureObservationKind = "visual_continuity"
	ObservationSceneChange      StructureObservationKind = "scene_change"
	ObservationStandardDuration StructureObservationKind = "standard_duration"
	ObservationSegmentRole      StructureObservationKind = "segment_role"
	// ObservationCompleteTimelineDecision projects a replayed multi-family artifact; it is not
	// another raw signal or assessor vote. Certified publication still requires that artifact.
	ObservationCompleteTimelineDecision StructureObservationKind = "complete_timeline_decision"
)

type StructureObservationEffect string

const (
	ObservationProposesBoundary    StructureObservationEffect = "proposes_boundary"
	ObservationSupportsBoundary    StructureObservationEffect = "supports_boundary"
	ObservationContradictsBoundary StructureObservationEffect = "contradicts_boundary"
	ObservationContextOnly         StructureObservationEffect = "context_only"
)

// StructureObservation is one independently retained source-relative fact. StartMs and EndMs
// form an inclusive uncertainty window; a point observation has equal bounds.
type StructureObservation struct {
	ID             string                     `json:"id"`
	Kind           StructureObservationKind   `json:"kind"`
	Effect         StructureObservationEffect `json:"effect"`
	StartMs        int64                      `json:"startMs"`
	EndMs          int64                      `json:"endMs"`
	Producer       string                     `json:"producer"`
	EvidenceSHA256 string                     `json:"evidenceSha256"`
	RoleEvidence   *StructureRoleEvidence     `json:"roleEvidence,omitempty"`
}

type StructureBoundaryStatus string

const (
	BoundaryResolved   StructureBoundaryStatus = "resolved"
	BoundaryUnresolved StructureBoundaryStatus = "unresolved"
	BoundaryConflicted StructureBoundaryStatus = "conflicted"
)

type StructureBoundary struct {
	AtMs           int64                   `json:"atMs"`
	WindowStartMs  int64                   `json:"windowStartMs"`
	WindowEndMs    int64                   `json:"windowEndMs"`
	Status         StructureBoundaryStatus `json:"status"`
	ObservationIDs []string                `json:"observationIds"`
	ConflictIDs    []string                `json:"conflictIds,omitempty"`
}

type StructureSegmentRole string

const (
	SegmentRoleCommercial        StructureSegmentRole = "commercial"
	SegmentRolePromo             StructureSegmentRole = "promo"
	SegmentRoleBumper            StructureSegmentRole = "bumper"
	SegmentRoleStationID         StructureSegmentRole = "station_id"
	SegmentRolePSA               StructureSegmentRole = "psa"
	SegmentRoleTrailer           StructureSegmentRole = "trailer"
	SegmentRoleInterstitial      StructureSegmentRole = "interstitial"
	SegmentRoleProgrammeFragment StructureSegmentRole = "programme_fragment"
	SegmentRoleNonFiller         StructureSegmentRole = "non_filler"
	SegmentRoleAmbiguous         StructureSegmentRole = "ambiguous"
	SegmentRoleUnusable          StructureSegmentRole = "unusable"
)

type StructureRoleClaim struct {
	StartMs     int64                `json:"startMs"`
	EndMs       int64                `json:"endMs"`
	Role        StructureSegmentRole `json:"role"`
	EvidenceIDs []string             `json:"evidenceIds"`
	Reason      string               `json:"reason"`
}

type StructureSegmentDisposition string

const (
	StructureKeep       StructureSegmentDisposition = "keep"
	StructureDiscard    StructureSegmentDisposition = "discard"
	StructureUnresolved StructureSegmentDisposition = "unresolved"
)

type StructureDiscardReason string

const (
	DiscardBelowClipFloor    StructureDiscardReason = "below_clip_floor"
	DiscardDuplicate         StructureDiscardReason = "duplicate"
	DiscardProgrammeMaterial StructureDiscardReason = "programme_material"
	DiscardNonFiller         StructureDiscardReason = "non_filler"
	DiscardUnusableFragment  StructureDiscardReason = "unusable_fragment"
)

type StructureDiscardClaim struct {
	StartMs     int64                  `json:"startMs"`
	EndMs       int64                  `json:"endMs"`
	Reason      StructureDiscardReason `json:"reason"`
	EvidenceIDs []string               `json:"evidenceIds"`
}

type StructurePlanSegment struct {
	Index         int                         `json:"index"`
	StartMs       int64                       `json:"startMs"`
	EndMs         int64                       `json:"endMs"`
	Disposition   StructureSegmentDisposition `json:"disposition"`
	Role          StructureSegmentRole        `json:"role,omitempty"`
	Reason        string                      `json:"reason,omitempty"`
	DiscardReason StructureDiscardReason      `json:"discardReason,omitempty"`
	EvidenceIDs   []string                    `json:"evidenceIds,omitempty"`
	StartStatus   StructureBoundaryStatus     `json:"startStatus"`
	EndStatus     StructureBoundaryStatus     `json:"endStatus"`
}

// SourceStructureAssessment is the canonical complete-timeline result. SHA256 addresses the
// normalized document with SHA256 itself empty, so persisted copies can be independently verified.
type SourceStructureAssessment struct {
	SchemaVersion   int                    `json:"schemaVersion"`
	ContractVersion string                 `json:"contractVersion"`
	Source          SplitSourceAsset       `json:"source"`
	DurationMs      int64                  `json:"durationMs"`
	Kind            SourceStructureKind    `json:"kind"`
	Observations    []StructureObservation `json:"observations"`
	Boundaries      []StructureBoundary    `json:"boundaries,omitempty"`
	Plan            []StructurePlanSegment `json:"plan"`
	AssessedAt      time.Time              `json:"assessedAt"`
	UnusableReason  string                 `json:"unusableReason,omitempty"`
	SHA256          string                 `json:"sha256"`
}

type SourceStructureInput struct {
	Source         SplitSourceAsset
	Observations   []StructureObservation
	RoleClaims     []StructureRoleClaim
	DiscardClaims  []StructureDiscardClaim
	AssessedAt     time.Time
	UnusableReason string
}

// AssessSourceStructure is the module interface: exact source plus independent observations in,
// one deterministic complete-coverage assessment out.
func AssessSourceStructure(input SourceStructureInput) (SourceStructureAssessment, error) {
	return assessSourceStructure(input, nil)
}

func assessSourceStructure(input SourceStructureInput, authority *structureDecisionProjectionAuthority) (SourceStructureAssessment, error) {
	if err := input.Source.validate(); err != nil {
		return SourceStructureAssessment{}, fmt.Errorf("source structure: %w", err)
	}
	if input.AssessedAt.IsZero() {
		return SourceStructureAssessment{}, errors.New("source structure: assessment time is required")
	}
	observations, err := normalizeStructureObservations(input.Observations, input.Source.DurationMs)
	if err != nil {
		return SourceStructureAssessment{}, err
	}
	boundaries := fuseStructureBoundaries(observations, input.Source.DurationMs)
	plan, err := buildStructurePlan(input.Source, boundaries, input.RoleClaims, input.DiscardClaims, observations, authority)
	if err != nil {
		return SourceStructureAssessment{}, err
	}
	assessment := SourceStructureAssessment{
		SchemaVersion: SourceStructureSchemaVersion, ContractVersion: SourceStructureContractVersion,
		Source: input.Source, DurationMs: input.Source.DurationMs, Observations: observations,
		Boundaries: boundaries, Plan: plan, AssessedAt: input.AssessedAt.UTC(),
		UnusableReason: strings.TrimSpace(input.UnusableReason),
	}
	assessment.Kind = reduceStructureKind(assessment)
	assessment.SHA256 = SourceStructureAssessmentSHA256(assessment)
	if err := validateSourceStructureAssessment(assessment, authority); err != nil {
		return SourceStructureAssessment{}, err
	}
	return assessment, nil
}

func ValidateSourceStructureAssessment(assessment SourceStructureAssessment) error {
	return validateSourceStructureAssessment(assessment, nil)
}

func validateSourceStructureAssessment(assessment SourceStructureAssessment, authority *structureDecisionProjectionAuthority) error {
	if assessment.SchemaVersion != SourceStructureSchemaVersion || assessment.ContractVersion != SourceStructureContractVersion {
		return errors.New("source structure: unsupported assessment contract")
	}
	if err := assessment.Source.validate(); err != nil || assessment.DurationMs != assessment.Source.DurationMs || assessment.AssessedAt.IsZero() {
		return errors.New("source structure: invalid source binding or assessment time")
	}
	if !validSourceStructureKind(assessment.Kind) {
		return errors.New("source structure: invalid source verdict")
	}
	if err := validateStructureCoverage(assessment.Plan, assessment.DurationMs); err != nil {
		return err
	}
	if assessment.Kind == StructureUnusable && strings.TrimSpace(assessment.UnusableReason) == "" {
		return errors.New("source structure: unusable verdict requires a reason")
	}
	normalized, err := normalizeStructureObservations(assessment.Observations, assessment.DurationMs)
	if err != nil || !reflect.DeepEqual(normalized, assessment.Observations) {
		return errors.New("source structure: observations are invalid or non-canonical")
	}
	if err := validateStructureRoleObservationSources(normalized, assessment.Source); err != nil {
		return err
	}
	boundaries := fuseStructureBoundaries(normalized, assessment.DurationMs)
	if !reflect.DeepEqual(boundaries, assessment.Boundaries) {
		return errors.New("source structure: boundaries do not reduce from observations")
	}
	claims := make([]StructureRoleClaim, 0, len(assessment.Plan))
	discards := make([]StructureDiscardClaim, 0, len(assessment.Plan))
	for _, segment := range assessment.Plan {
		if segment.Role != "" {
			claims = append(claims, StructureRoleClaim{
				StartMs: segment.StartMs, EndMs: segment.EndMs, Role: segment.Role,
				EvidenceIDs: segment.EvidenceIDs, Reason: segment.Reason,
			})
		}
		if segment.Disposition == StructureDiscard && segment.Role == "" {
			discards = append(discards, StructureDiscardClaim{
				StartMs: segment.StartMs, EndMs: segment.EndMs, Reason: segment.DiscardReason,
				EvidenceIDs: segment.EvidenceIDs,
			})
		}
	}
	plan, err := buildStructurePlan(assessment.Source, boundaries, claims, discards, normalized, authority)
	if err != nil || !reflect.DeepEqual(plan, assessment.Plan) {
		return errors.New("source structure: plan does not reduce from boundaries and role evidence")
	}
	if assessment.Kind != reduceStructureKind(assessment) {
		return errors.New("source structure: source verdict does not reduce from the plan")
	}
	if assessment.SHA256 == "" || assessment.SHA256 != SourceStructureAssessmentSHA256(assessment) {
		return errors.New("source structure: assessment digest does not match")
	}
	return nil
}

func SourceStructureAssessmentSHA256(assessment SourceStructureAssessment) string {
	assessment.SHA256 = ""
	raw, err := json.Marshal(assessment)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
