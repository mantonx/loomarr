package fillerreference

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/filleradmission"
	"github.com/loomarr/loomarr/internal/fillerbakeoff"
	"github.com/loomarr/loomarr/internal/fillercorpus"
	"github.com/loomarr/loomarr/internal/fillereval"
)

const (
	referenceMaxPacketFacts   = 256
	referenceMaxPacketSignals = 64
)

// validatePacketReferenceBinding verifies the immutable packet identity and
// deterministic fact namespace without reopening external media derivatives.
// Gate A screens bound evidence references; it does not authorize inference.
func validatePacketReferenceBinding(item fillereval.Case, packet fillerbakeoff.Packet, evidenceVersion string) error {
	if packet.SchemaVersion != fillerbakeoff.PacketSchemaVersion || packet.CaseID != item.ID || packet.EvidenceVersion != evidenceVersion || packet.ContentSHA256 != item.ContentSHA256 {
		return fmt.Errorf("case %q evidence packet identity does not match run and manifest", item.ID)
	}
	if len(packet.Facts) > referenceMaxPacketFacts || len(packet.Signals) > referenceMaxPacketSignals {
		return fmt.Errorf("case %q evidence packet exceeds bounded item counts", item.ID)
	}
	seen := make(map[string]struct{}, len(packet.Facts))
	for _, fact := range packet.Facts {
		validDeterministic := fact.Claim == filleradmission.ClaimMediaUsability && fact.Kind == filleradmission.KindDecoder ||
			fact.Claim == filleradmission.ClaimSourceLicense && fact.Kind == filleradmission.KindSourcePolicy
		if fact.ID == "" || !validDeterministic || fact.EvaluationID != "" {
			return fmt.Errorf("case %q packet facts may contain only named deterministic decoder and source-policy evidence", item.ID)
		}
		if _, duplicate := seen[fact.ID]; duplicate {
			return fmt.Errorf("case %q packet contains duplicate fact id %q", item.ID, fact.ID)
		}
		seen[fact.ID] = struct{}{}
	}
	if got := fillerbakeoff.PacketSHA256(packet); got != item.EvidenceSHA256 {
		return fmt.Errorf("case %q evidence packet digest %s does not match manifest", item.ID, got)
	}
	return nil
}

func validatePacketCaseSet(manifest fillereval.Manifest, packets map[string]fillerbakeoff.Packet) error {
	if len(packets) != len(manifest.Cases) {
		return fmt.Errorf("reference audit has %d packets, want %d", len(packets), len(manifest.Cases))
	}
	for _, item := range manifest.Cases {
		if _, ok := packets[item.ID]; !ok {
			return fmt.Errorf("reference audit packet set is missing case %q", item.ID)
		}
	}
	return nil
}

func validateContentReview(manifest fillereval.Manifest, review ContentReviewArtifact, manifestSHA256 string, generatedAt time.Time) (map[string]*AppliedContentFinding, error) {
	if review.SchemaVersion != 1 || review.Kind != ContentReviewKind || review.ContractVersion != ContractVersion || strings.TrimSpace(review.ReviewerID) == "" || review.ReviewedAt.IsZero() || review.ReviewedAt.After(generatedAt) || review.SourceManifestSHA256 != manifestSHA256 || len(review.Findings) == 0 {
		return nil, fmt.Errorf("content review identity, authority, or time is invalid")
	}
	byContent := make(map[string]fillereval.Case, len(manifest.Cases))
	for _, item := range manifest.Cases {
		if _, duplicate := byContent[item.ContentSHA256]; duplicate {
			return nil, fmt.Errorf("manifest repeats content identity %q", item.ContentSHA256)
		}
		byContent[item.ContentSHA256] = item
	}
	result := make(map[string]*AppliedContentFinding, len(review.Findings))
	for _, finding := range review.Findings {
		item, ok := byContent[finding.ContentSHA256]
		if !ok || !validSHA256(finding.ContentSHA256) {
			return nil, fmt.Errorf("content review names unknown content identity %q", finding.ContentSHA256)
		}
		if _, duplicate := result[finding.ContentSHA256]; duplicate {
			return nil, fmt.Errorf("content review repeats content identity %q", finding.ContentSHA256)
		}
		if finding.Disposition != string(DispositionExclude) || finding.ReasonCode != ReasonNonBroadcastMaterial || strings.TrimSpace(finding.Detail) == "" || len(finding.Detail) > 2000 || len(finding.EvidenceRefs) < 2 || !slices.IsSorted(finding.EvidenceRefs) || len(slices.Compact(slices.Clone(finding.EvidenceRefs))) != len(finding.EvidenceRefs) {
			return nil, fmt.Errorf("content review finding %q has unsupported authority or evidence", finding.ContentSHA256)
		}
		evidenceByID := make(map[string]fillereval.Evidence, len(item.Evidence))
		for _, evidence := range item.Evidence {
			if _, duplicate := evidenceByID[evidence.ID]; duplicate {
				return nil, fmt.Errorf("content review case %q has ambiguous evidence id %q", finding.ContentSHA256, evidence.ID)
			}
			evidenceByID[evidence.ID] = evidence
		}
		for _, ref := range finding.EvidenceRefs {
			evidence, exists := evidenceByID[ref]
			if !exists || !contentEvidenceKind(evidence.Kind) {
				return nil, fmt.Errorf("content review finding %q cites missing or unsupported evidence %q", finding.ContentSHA256, ref)
			}
		}
		result[finding.ContentSHA256] = &AppliedContentFinding{
			ReviewerID: review.ReviewerID, ReviewedAt: review.ReviewedAt.UTC(), Disposition: finding.Disposition,
			ReasonCode: finding.ReasonCode, Detail: finding.Detail, EvidenceRefs: slices.Clone(finding.EvidenceRefs),
		}
	}
	return result, nil
}

func contentEvidenceKind(kind string) bool {
	switch kind {
	case string(filleradmission.KindFrame), string(filleradmission.KindTranscript), string(filleradmission.KindOCR), string(filleradmission.KindAudio), string(filleradmission.KindVideo):
		return true
	default:
		return false
	}
}

func validateDownloads(manifest fillereval.Manifest, ledger DownloadLedger) (map[string]DownloadCase, error) {
	validProfile := (ledger.SchemaVersion == fillercorpus.DownloadLedgerLegacySchemaVersion && ledger.Profile == "") ||
		(ledger.SchemaVersion == fillercorpus.DownloadLedgerSchemaVersion && ledger.Profile == fillercorpus.RightsProfileDevelopment)
	if !validProfile || !validSHA256(ledger.InventorySHA256) || ledger.GeneratedAt.IsZero() || ledger.MaxRequests <= 0 || ledger.RequestsUsed < 0 || ledger.RequestsUsed > ledger.MaxRequests || ledger.MaxItems <= 0 || ledger.MaxBytes <= 0 || ledger.Bytes < 0 || ledger.Bytes > ledger.MaxBytes || len(ledger.Cases) != len(manifest.Cases) {
		return nil, fmt.Errorf("download ledger has invalid identity, ceilings, or case count")
	}
	manifestByID := make(map[string]fillereval.Case, len(manifest.Cases))
	for _, item := range manifest.Cases {
		manifestByID[item.ID] = item
	}
	byID := make(map[string]DownloadCase, len(ledger.Cases))
	localFiles := make(map[string]struct{}, len(ledger.Cases))
	contentHashes := make(map[string]struct{}, len(ledger.Cases))
	var bytes int64
	for _, item := range ledger.Cases {
		caseItem, exists := manifestByID[item.CaseID]
		if !exists {
			return nil, fmt.Errorf("download ledger contains extra case %q", item.CaseID)
		}
		if err := validateDownloadCase(caseItem, ledger.InventorySHA256, item); err != nil {
			return nil, err
		}
		if bytes > ledger.MaxBytes-item.Representation.Bytes {
			return nil, fmt.Errorf("download ledger bytes overflow")
		}
		bytes += item.Representation.Bytes
		if item.CaseID == "" || item.LocalFile == "" || !validSHA256(item.ContentSHA256) || item.Representation.Bytes <= 0 {
			return nil, fmt.Errorf("download ledger contains an incomplete case")
		}
		if _, duplicate := byID[item.CaseID]; duplicate {
			return nil, fmt.Errorf("download ledger repeats case %q", item.CaseID)
		}
		if _, duplicate := localFiles[item.LocalFile]; duplicate {
			return nil, fmt.Errorf("download ledger repeats local file %q", item.LocalFile)
		}
		if _, duplicate := contentHashes[item.ContentSHA256]; duplicate {
			return nil, fmt.Errorf("download ledger repeats content identity %q", item.ContentSHA256)
		}
		byID[item.CaseID] = item
		localFiles[item.LocalFile] = struct{}{}
		contentHashes[item.ContentSHA256] = struct{}{}
	}
	if bytes != ledger.Bytes {
		return nil, fmt.Errorf("download ledger byte total differs from its acquired source rows")
	}
	for _, item := range manifest.Cases {
		if _, ok := byID[item.ID]; !ok {
			return nil, fmt.Errorf("download ledger is missing case %q", item.ID)
		}
	}
	return byID, nil
}

func validateDownloadCase(item fillereval.Case, inventorySHA256 string, download DownloadCase) error {
	p := item.Provenance
	if download.ContentSHA256 != item.ContentSHA256 || download.Authority != p.Authority || download.ItemID != p.ItemID || download.LicenseURL != p.LicenseURL || download.ItemURL != p.ItemRef || download.MetadataRetrievedAt != p.MetadataRetrievedAt || download.MetadataSHA256 != p.MetadataSHA256 || download.Representation.Name != p.SourceFilename || download.Representation.URL != p.SourceRef || download.Representation.Bytes != p.SourceBytes {
		return fmt.Errorf("download case %q does not match manifest provenance or acquired source bytes", item.ID)
	}
	approval := download.Approval
	if approval.InventorySHA256 != inventorySHA256 || approval.CaseID != item.ID || approval.Authority != p.Authority || approval.ItemID != p.ItemID || approval.MetadataSHA256 != p.MetadataSHA256 || approval.Decision != "approved" || !approval.Redistributable || approval.ReviewerID != p.RightsReviewerID || approval.ReviewedAt != p.RightsReviewedAt || approval.RequiredCredit != p.RequiredCredit || !slices.Equal(approval.Restrictions, p.Restrictions) || strings.TrimSpace(approval.Basis) == "" || p.RightsDecision != "approved" || !p.Redistributable {
		return fmt.Errorf("download case %q has no matching approved rights decision", item.ID)
	}
	return nil
}

func validateMapping(manifest fillereval.Manifest, mapping MappingArtifact) (map[string]ProductMapping, error) {
	byLabel := make(map[string]ProductMapping, len(mapping.Mappings))
	for _, entry := range mapping.Mappings {
		if strings.TrimSpace(entry.ReviewerLabel) == "" || entry.Occurrences <= 0 || len(entry.ProductionCategories) == 0 || strings.TrimSpace(entry.Basis) == "" || !slices.IsSorted(entry.ProductionCategories) || len(slices.Compact(slices.Clone(entry.ProductionCategories))) != len(entry.ProductionCategories) {
			return nil, fmt.Errorf("invalid product mapping for %q", entry.ReviewerLabel)
		}
		if _, duplicate := byLabel[entry.ReviewerLabel]; duplicate {
			return nil, fmt.Errorf("duplicate product mapping for %q", entry.ReviewerLabel)
		}
		byLabel[entry.ReviewerLabel] = entry
	}
	counts := map[string]int{}
	assignments := 0
	for _, item := range manifest.Cases {
		for _, product := range item.Taxonomy["product"] {
			counts[product]++
			assignments++
		}
	}
	if assignments != mapping.SourceProductAssignments || assignments != mapping.MappedAssignments || len(counts) != mapping.UniqueReviewerLabels || len(counts) != len(byLabel) {
		return nil, fmt.Errorf("product mapping does not cover the manifest assignment set")
	}
	for label, count := range counts {
		entry, ok := byLabel[label]
		if !ok || entry.Occurrences != count {
			return nil, fmt.Errorf("product mapping occurrence mismatch for %q", label)
		}
	}
	return byLabel, nil
}
