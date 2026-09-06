package filler

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/fillerstructure"
)

const (
	StructureSplitShadowSchemaVersion   = 2
	StructureSplitShadowContractVersion = "filler-structure-materialization-shadow-v2"
)

var ErrStructureSplitShadowConflict = errors.New("filler: structure split shadow decision conflicts with its content identity")

// StructureSplitShadowRepository is the append-only persistence seam. Production uses the shared
// store; tests use an in-memory adapter at the same seam.
type StructureSplitShadowRepository interface {
	PutStructureSplitShadowDecision(context.Context, StructureSplitShadowDecision) error
	GetStructureSplitShadowDecision(context.Context, string) (StructureSplitShadowDecision, bool, error)
}

// StructureSplitShadowObserver is the split stage's complete shadow interface. The stage supplies
// only the exact proposal and compatibility outcome; the module owns complete-plan evaluation,
// versioning, content identity, validation, and persistence.
type StructureSplitShadowObserver interface {
	NeedsStructureSplitObservation(context.Context, SplitProposal) (bool, error)
	ObserveStructureSplit(context.Context, SplitProposal, SplitPartition) error
}

type StructureSplitShadowSpan struct {
	StartMs    int64  `json:"startMs"`
	EndMs      int64  `json:"endMs"`
	HoldReason string `json:"holdReason,omitempty"`
}

type StructureSplitShadowOutcome struct {
	Verdict AutoSplitReject            `json:"verdict,omitempty"`
	Confirm []StructureSplitShadowSpan `json:"confirm,omitempty"`
	Hold    []StructureSplitShadowSpan `json:"hold,omitempty"`
	Discard []StructureSplitShadowSpan `json:"discard,omitempty"`
}

// StructureSplitShadowDecision preserves both decisions after the proposal is consumed. SHA256
// addresses the canonical record with ID and SHA256 empty; ID is derived from that digest.
type StructureSplitShadowDecision struct {
	SchemaVersion            int                         `json:"schemaVersion"`
	ContractVersion          string                      `json:"contractVersion"`
	ID                       string                      `json:"id"`
	ProposalID               string                      `json:"proposalId"`
	ClipHash                 string                      `json:"clipHash"`
	SourceSHA256             string                      `json:"sourceSha256,omitempty"`
	AssessmentSHA256         string                      `json:"assessmentSha256,omitempty"`
	StructureDecisionSHA256  string                      `json:"structureDecisionSha256,omitempty"`
	StructureAuthoritySHA256 string                      `json:"structureAuthoritySha256,omitempty"`
	PolicyVersion            string                      `json:"policyVersion"`
	Legacy                   StructureSplitShadowOutcome `json:"legacy"`
	Certified                StructureSplitShadowOutcome `json:"certified"`
	ObservedAt               time.Time                   `json:"observedAt"`
	SHA256                   string                      `json:"sha256"`
}

// StructureSplitShadow is the deep dual-evaluation module used during rollout.
type StructureSplitShadow struct {
	repository      StructureSplitShadowRepository
	auto            *AutoSplitPolicy
	materialization *StructureMaterializationPolicy
	minClipDuration func() time.Duration
	policyVersion   string
}

func NewStructureSplitShadow(repository StructureSplitShadowRepository, auto *AutoSplitPolicy, materialization *StructureMaterializationPolicy, minClipDuration func() time.Duration, policyVersion string) (*StructureSplitShadow, error) {
	policyVersion = strings.TrimSpace(policyVersion)
	if repository == nil || auto == nil || materialization == nil || minClipDuration == nil || policyVersion == "" || len(policyVersion) > 128 {
		return nil, fmt.Errorf("structure split shadow requires repository, complete policies, clip floor, and bounded policy identity")
	}
	return &StructureSplitShadow{
		repository: repository, auto: auto, materialization: materialization,
		minClipDuration: minClipDuration, policyVersion: policyVersion,
	}, nil
}

func (s *StructureSplitShadow) ObserveStructureSplit(ctx context.Context, proposal SplitProposal, legacy SplitPartition) error {
	if s == nil || s.repository == nil {
		return fmt.Errorf("structure split shadow is unavailable")
	}
	certified := CertifiedStructureMaterializable(proposal, s.auto, s.materialization, s.minClipDuration())
	decision, err := newStructureSplitShadowDecision(proposal, legacy, certified, s.materialization, s.policyVersion)
	if err != nil {
		return err
	}
	if err := s.repository.PutStructureSplitShadowDecision(ctx, decision); err != nil {
		return fmt.Errorf("record structure split shadow: %w", err)
	}
	return nil
}

// NeedsStructureSplitObservation lets startup requeue an older review proposal exactly when its
// current proposal/policy decision is absent. The full document is compared when the id exists so
// corrupt or conflicting evidence fails closed instead of suppressing a fresh observation.
func (s *StructureSplitShadow) NeedsStructureSplitObservation(ctx context.Context, proposal SplitProposal) (bool, error) {
	if s == nil || s.repository == nil {
		return false, fmt.Errorf("structure split shadow is unavailable")
	}
	legacy := AutoConfirmable(proposal, s.auto, s.minClipDuration())
	certified := CertifiedStructureMaterializable(proposal, s.auto, s.materialization, s.minClipDuration())
	expected, err := newStructureSplitShadowDecision(proposal, legacy, certified, s.materialization, s.policyVersion)
	if err != nil {
		return false, err
	}
	existing, found, err := s.repository.GetStructureSplitShadowDecision(ctx, expected.ID)
	if err != nil {
		return false, fmt.Errorf("read structure split shadow: %w", err)
	}
	if !found {
		return true, nil
	}
	if !reflect.DeepEqual(existing, expected) {
		return false, ErrStructureSplitShadowConflict
	}
	return false, nil
}

func newStructureSplitShadowDecision(proposal SplitProposal, legacy, certified SplitPartition, materialization *StructureMaterializationPolicy, policyVersion string) (StructureSplitShadowDecision, error) {
	if strings.TrimSpace(proposal.ID) == "" || strings.TrimSpace(proposal.ClipHash) == "" || proposal.CreatedAt.IsZero() || len(proposal.Segments) == 0 {
		return StructureSplitShadowDecision{}, fmt.Errorf("structure split shadow requires a complete proposal identity and segments")
	}
	legacyOutcome := structureSplitShadowOutcome(legacy)
	certifiedOutcome := structureSplitShadowOutcome(certified)
	if err := structureSplitShadowOutcomeCoversProposal(legacyOutcome, proposal.Segments); err != nil {
		return StructureSplitShadowDecision{}, fmt.Errorf("legacy structure split outcome: %w", err)
	}
	certifiedBasis := proposal.Segments
	if projected, ok := certifiedStructureShadowBasis(proposal); ok {
		certifiedBasis = projected
	}
	if err := structureSplitShadowOutcomeCoversProposal(certifiedOutcome, certifiedBasis); err != nil {
		return StructureSplitShadowDecision{}, fmt.Errorf("certified structure split outcome: %w", err)
	}
	observedAt := proposal.CreatedAt.UTC()
	sourceSHA, assessmentSHA, structureDecisionSHA := proposal.Source.SHA256, "", ""
	if proposal.Structure != nil {
		if err := validateSourceStructureAssessmentOrProjection(*proposal.Structure, proposal.StructureDecision); err != nil || proposal.Structure.Source != proposal.Source {
			return StructureSplitShadowDecision{}, fmt.Errorf("structure split shadow proposal assessment is invalid")
		}
		assessmentSHA = proposal.Structure.SHA256
		observedAt = proposal.Structure.AssessedAt.UTC()
	}
	if proposal.StructureDecision != nil {
		artifact := *proposal.StructureDecision
		if err := fillerstructure.ValidateArtifact(artifact); err != nil ||
			artifact.Decision.Source.SHA256 != proposal.Source.SHA256 ||
			artifact.Decision.Source.Bytes != proposal.Source.Bytes ||
			artifact.Decision.Source.DurationMS != proposal.Source.DurationMs {
			return StructureSplitShadowDecision{}, fmt.Errorf("structure split shadow proposal decision is invalid")
		}
		structureDecisionSHA = artifact.SHA256
		if artifact.DecidedAt.After(observedAt) {
			observedAt = artifact.DecidedAt
		}
	}
	structureAuthoritySHA := structureMaterializationAuthorityIdentity(materialization, proposal.StructureDecision)
	decision := StructureSplitShadowDecision{
		SchemaVersion: StructureSplitShadowSchemaVersion, ContractVersion: StructureSplitShadowContractVersion,
		ProposalID: proposal.ID, ClipHash: proposal.ClipHash, SourceSHA256: sourceSHA,
		AssessmentSHA256: assessmentSHA, StructureDecisionSHA256: structureDecisionSHA,
		StructureAuthoritySHA256: structureAuthoritySHA,
		PolicyVersion:            strings.TrimSpace(policyVersion),
		Legacy:                   legacyOutcome, Certified: certifiedOutcome, ObservedAt: observedAt,
	}
	decision.SHA256 = StructureSplitShadowDecisionSHA256(decision)
	decision.ID = "split-shadow-" + decision.SHA256
	if err := ValidateStructureSplitShadowDecision(decision); err != nil {
		return StructureSplitShadowDecision{}, err
	}
	return decision, nil
}

func certifiedStructureShadowBasis(proposal SplitProposal) ([]SplitSegment, bool) {
	if proposal.Structure == nil || len(proposal.Spawned) > 0 {
		return nil, false
	}
	assessment := *proposal.Structure
	if validateSourceStructureAssessmentOrProjection(assessment, proposal.StructureDecision) != nil || assessment.Source != proposal.Source ||
		assessment.Kind != StructureCompilationBreak && assessment.Kind != StructureProgrammeSpots {
		return nil, false
	}
	keep, discard, err := projectCertifiedStructureSegments(proposal.Segments, assessment.Plan)
	if err != nil {
		return nil, false
	}
	return append(keep, discard...), true
}

func structureSplitShadowOutcome(partition SplitPartition) StructureSplitShadowOutcome {
	outcome := StructureSplitShadowOutcome{Verdict: partition.Verdict()}
	outcome.Confirm = structureSplitShadowSpans(partition.Confirm, "", false)
	outcome.Hold = structureSplitShadowSpans(partition.Hold, string(outcome.Verdict), partition.Reject != AutoSplitOK)
	outcome.Discard = structureSplitShadowSpans(partition.Discard, "", false)
	return outcome
}

func structureSplitShadowSpans(segments []SplitSegment, fallbackReason string, overrideReason bool) []StructureSplitShadowSpan {
	spans := make([]StructureSplitShadowSpan, 0, len(segments))
	for _, segment := range segments {
		reason := strings.TrimSpace(segment.HoldReason)
		if overrideReason || reason == "" {
			reason = fallbackReason
		}
		spans = append(spans, StructureSplitShadowSpan{StartMs: segment.StartMs, EndMs: segment.EndMs, HoldReason: reason})
	}
	sort.Slice(spans, func(i, j int) bool {
		if spans[i].StartMs != spans[j].StartMs {
			return spans[i].StartMs < spans[j].StartMs
		}
		return spans[i].EndMs < spans[j].EndMs
	})
	return spans
}

func structureSplitShadowOutcomeCoversProposal(outcome StructureSplitShadowOutcome, segments []SplitSegment) error {
	expected := make(map[[2]int64]struct{}, len(segments))
	for _, segment := range segments {
		key := [2]int64{segment.StartMs, segment.EndMs}
		if _, duplicate := expected[key]; duplicate {
			return fmt.Errorf("proposal repeats span %d..%d", segment.StartMs, segment.EndMs)
		}
		expected[key] = struct{}{}
	}
	seen := make(map[[2]int64]struct{}, len(segments))
	for _, group := range [][]StructureSplitShadowSpan{outcome.Confirm, outcome.Hold, outcome.Discard} {
		for _, span := range group {
			key := [2]int64{span.StartMs, span.EndMs}
			if _, exists := expected[key]; !exists {
				return fmt.Errorf("span %d..%d is absent from the proposal", span.StartMs, span.EndMs)
			}
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("span %d..%d appears more than once", span.StartMs, span.EndMs)
			}
			seen[key] = struct{}{}
		}
	}
	if len(seen) != len(expected) {
		return fmt.Errorf("outcome covers %d of %d proposal spans", len(seen), len(expected))
	}
	return nil
}

var _ StructureSplitShadowObserver = (*StructureSplitShadow)(nil)
