// Package fillerreference owns the deterministic pre-screen for the production-ready
// filler reference cohort. It never edits media or claims human editorial acceptance.
package fillerreference

import (
	"time"

	"github.com/loomarr/loomarr/internal/fillercorpus"
	"github.com/loomarr/loomarr/internal/fillereval"
)

const (
	AuditSchemaVersion    = 3
	ContractVersion       = "filler-reference-cohort-2026-08-31-v3"
	PacketEvidenceVersion = "filler-evidence-v1"
	ContentReviewKind     = "filler_reference_content_review"
)

type Disposition string

const (
	DispositionCandidate Disposition = "candidate"
	DispositionHold      Disposition = "hold"
	DispositionExclude   Disposition = "exclude"
)

type ReasonCode string

const (
	ReasonSemanticInvalid          ReasonCode = "semantic_invalid"
	ReasonSemanticAmbiguous        ReasonCode = "semantic_ambiguous"
	ReasonRoleNotFiller            ReasonCode = "role_not_filler"
	ReasonRoleMissing              ReasonCode = "role_missing"
	ReasonMediaUnusable            ReasonCode = "media_unusable"
	ReasonDurationTooShort         ReasonCode = "duration_too_short"
	ReasonMediaEvidenceMissing     ReasonCode = "media_evidence_missing"
	ReasonSourceIneligible         ReasonCode = "source_ineligible"
	ReasonSourceEvidenceMissing    ReasonCode = "source_evidence_missing"
	ReasonSourceCoverageIncomplete ReasonCode = "source_coverage_incomplete"
	ReasonCommercialProductMissing ReasonCode = "commercial_product_missing"
	ReasonNonBroadcastMaterial     ReasonCode = "non_broadcast_material"
	ReasonTaxonomyUnresolved       ReasonCode = "taxonomy_unresolved"
)

type ContentReviewArtifact struct {
	SchemaVersion        int              `json:"schemaVersion"`
	Kind                 string           `json:"kind"`
	ContractVersion      string           `json:"contractVersion"`
	ReviewerID           string           `json:"reviewerId"`
	ReviewedAt           time.Time        `json:"reviewedAt"`
	SourceManifestSHA256 string           `json:"sourceManifestSha256"`
	Findings             []ContentFinding `json:"findings"`
}

type ContentFinding struct {
	ContentSHA256 string     `json:"contentSha256"`
	Disposition   string     `json:"disposition"`
	ReasonCode    ReasonCode `json:"reasonCode"`
	Detail        string     `json:"detail"`
	EvidenceRefs  []string   `json:"evidenceRefs"`
}

type AppliedContentFinding struct {
	ReviewerID   string     `json:"reviewerId"`
	ReviewedAt   time.Time  `json:"reviewedAt"`
	Disposition  string     `json:"disposition"`
	ReasonCode   ReasonCode `json:"reasonCode"`
	Detail       string     `json:"detail"`
	EvidenceRefs []string   `json:"evidenceRefs"`
}

type MappingArtifact struct {
	SchemaVersion            int              `json:"schemaVersion"`
	MappingVersion           string           `json:"mappingVersion"`
	SourceManifestSHA256     string           `json:"sourceManifestSha256"`
	SourceProductAssignments int              `json:"sourceProductAssignments"`
	UniqueReviewerLabels     int              `json:"uniqueReviewerLabels"`
	MappedAssignments        int              `json:"mappedAssignments"`
	UnmappedAssignments      int              `json:"unmappedAssignments"`
	ProductionCategories     []string         `json:"productionCategories"`
	Mappings                 []ProductMapping `json:"mappings"`
}

type ProductMapping struct {
	ReviewerLabel        string   `json:"reviewerLabel"`
	Occurrences          int      `json:"occurrences"`
	ProductionCategories []string `json:"productionCategories"`
	Basis                string   `json:"basis"`
}

type InputIdentity struct {
	ManifestSHA256       string `json:"manifestSha256"`
	PacketsSHA256        string `json:"packetsSha256"`
	MappingSHA256        string `json:"mappingSha256"`
	DownloadLedgerSHA256 string `json:"downloadLedgerSha256"`
	ContentReviewSHA256  string `json:"contentReviewSha256"`
}

// RawAuditInputs are the exact artifact bytes bound into an audit. BuildAudit
// clones, strictly decodes, and hashes these bytes itself; callers cannot pair
// typed values from one artifact with asserted identities from another.
type RawAuditInputs struct {
	Manifest       []byte
	Packets        []byte
	Mapping        []byte
	DownloadLedger []byte
	ContentReview  []byte
}

type DownloadLedger = fillercorpus.DownloadLedger
type DownloadCase = fillercorpus.DownloadCase

type Summary struct {
	Cases      int            `json:"cases"`
	Candidates int            `json:"candidates"`
	Holds      int            `json:"holds"`
	Excluded   int            `json:"excluded"`
	ByRole     map[string]int `json:"byRole"`
	ByReason   map[string]int `json:"byReason"`
	BySource   map[string]int `json:"candidateBySource"`
	Unresolved map[string]int `json:"unresolvedTaxonomyValues"`
	Mapping    string         `json:"mappingVersion"`
	Contract   string         `json:"contractVersion"`
}

type Audit struct {
	SchemaVersion int           `json:"schemaVersion"`
	Contract      string        `json:"contractVersion"`
	GeneratedAt   time.Time     `json:"generatedAt"`
	Inputs        InputIdentity `json:"inputs"`
	Summary       Summary       `json:"summary"`
	Cases         []Case        `json:"cases"`
}

type Case struct {
	CaseID                   string                 `json:"caseId"`
	ContentSHA256            string                 `json:"contentSha256"`
	Source                   string                 `json:"source"`
	SourceItemID             string                 `json:"sourceItemId"`
	SourceFilename           string                 `json:"sourceFilename"`
	SourceLocalFile          string                 `json:"sourceLocalFile"`
	SemanticTruth            fillereval.Truth       `json:"semanticTruth"`
	ContentRole              string                 `json:"contentRole,omitempty"`
	Disposition              Disposition            `json:"disposition"`
	ReasonCodes              []ReasonCode           `json:"reasonCodes,omitempty"`
	ReadinessGrade           string                 `json:"readinessGrade"`
	NeedsFullVideoInspection bool                   `json:"needsFullVideoInspection"`
	Media                    MediaSummary           `json:"media"`
	ProposedProductionTags   []string               `json:"proposedProductionTags,omitempty"`
	UnresolvedTaxonomyValues []string               `json:"unresolvedTaxonomyValues,omitempty"`
	ProposedBrandValues      []string               `json:"proposedBrandValues,omitempty"`
	ReviewerTaxonomy         map[string][]string    `json:"reviewerTaxonomy,omitempty"`
	ContentFinding           *AppliedContentFinding `json:"contentFinding,omitempty"`
}

type MediaSummary struct {
	SourceDurationMS        int64  `json:"sourceDurationMs"`
	SegmentStartMS          int64  `json:"segmentStartMs"`
	SegmentDurationMS       int64  `json:"segmentDurationMs"`
	EvidenceVideoDurationMS int64  `json:"evidenceVideoDurationMs,omitempty"`
	Width                   int    `json:"width,omitempty"`
	Height                  int    `json:"height,omitempty"`
	BlackPercent            int    `json:"blackPercent"`
	SilencePercent          int    `json:"silencePercent"`
	NoVideo                 bool   `json:"noVideo"`
	NoAudio                 bool   `json:"noAudio"`
	EvidenceVideoPath       string `json:"evidenceVideoPath,omitempty"`
	EvidenceVideoSHA256     string `json:"evidenceVideoSha256,omitempty"`
	EvidenceVideoBytes      int64  `json:"evidenceVideoBytes,omitempty"`
	evidenceVideoValid      bool
}

// BuildAudit creates a stable, read-only screen. Candidate means only that no deterministic
