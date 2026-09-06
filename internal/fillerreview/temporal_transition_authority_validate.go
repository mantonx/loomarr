package fillerreview

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/mediatools"
)

func loadTemporalTransitionAuthority(path string, manifest TemporalTruthEvidenceManifest, manifestSHA string, privateMap TemporalTruthEvidencePrivateMap, privateMapSHA string, plannedAt time.Time) (TemporalTransitionAuthority, string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return TemporalTransitionAuthority{}, "", err
	}
	authority, err := readStrictJSON[TemporalTransitionAuthority](path)
	if err != nil {
		return TemporalTransitionAuthority{}, "", err
	}
	if err := validateTemporalTransitionAuthority(authority, manifest, manifestSHA, privateMap, privateMapSHA, plannedAt); err != nil {
		return TemporalTransitionAuthority{}, "", err
	}
	return authority, hashBytes(raw), nil
}

func validateTemporalTransitionAuthority(authority TemporalTransitionAuthority, manifest TemporalTruthEvidenceManifest, manifestSHA string, privateMap TemporalTruthEvidencePrivateMap, privateMapSHA string, latest time.Time) error {
	if authority.SchemaVersion != TemporalTransitionAuthoritySchemaVersion || authority.ContractVersion != TemporalTransitionAuthorityContractVersion || authority.GeneratedAt.IsZero() || latest.Before(authority.GeneratedAt) || authority.EvidenceManifestSHA256 != manifestSHA || authority.EvidencePrivateMapSHA256 != privateMapSHA || authority.Policy != temporalTransitionPolicy() || authority.TrainingAllowed || authority.ProductionAdmissionAllowed || strings.TrimSpace(authority.FFmpeg.Path) == "" || strings.TrimSpace(authority.FFmpeg.Version) == "" || !reviewSHA256(authority.FFmpeg.BinarySHA256) || len(authority.Cases) != len(manifest.Cases) || !sort.SliceIsSorted(authority.Cases, func(i, j int) bool { return authority.Cases[i].EvidenceAlias < authority.Cases[j].EvidenceAlias }) {
		return fmt.Errorf("temporal transition authority identity, policy, or case count is invalid")
	}
	evidence := make(map[string]TemporalTruthEvidenceCase, len(manifest.Cases))
	for _, item := range manifest.Cases {
		evidence[item.Alias] = item
	}
	mapping := make(map[string]TemporalTruthEvidencePrivateEntry, len(privateMap.Entries))
	for _, item := range privateMap.Entries {
		mapping[item.Alias] = item
	}
	seenAliases, seenCases, seenSources := map[string]struct{}{}, map[string]struct{}{}, map[string]struct{}{}
	for _, item := range authority.Cases {
		source, exists := evidence[item.EvidenceAlias]
		mapped, mappedExists := mapping[item.EvidenceAlias]
		if !exists || !mappedExists || item.CaseID != mapped.CaseID || item.SourceSHA256 != source.Video.SHA256 || item.DurationMS != source.Video.DurationMS || item.DurationMS < TemporalTransitionEdgeWindowMS {
			return fmt.Errorf("temporal transition authority contains an unbound case")
		}
		for label, value := range map[string]string{"alias": item.EvidenceAlias, "case": item.CaseID, "source": item.SourceSHA256} {
			seen := map[string]map[string]struct{}{"alias": seenAliases, "case": seenCases, "source": seenSources}[label]
			if _, duplicate := seen[value]; duplicate {
				return fmt.Errorf("temporal transition authority repeats a %s", label)
			}
			seen[value] = struct{}{}
		}
		if err := validateTemporalTransitionEdge(item.Head, 0, TemporalTransitionEdgeWindowMS); err != nil {
			return fmt.Errorf("temporal transition head for %q: %w", item.EvidenceAlias, err)
		}
		tailStart := item.DurationMS - TemporalTransitionEdgeWindowMS
		if err := validateTemporalTransitionEdge(item.Tail, tailStart, item.DurationMS); err != nil {
			return fmt.Errorf("temporal transition tail for %q: %w", item.EvidenceAlias, err)
		}
	}
	return nil
}

func validateTemporalTransitionEdge(edge TemporalTransitionEdge, wantStart, wantEnd int64) error {
	if edge.StartMS != wantStart || edge.EndMS != wantEnd || edge.RMSMilliDBFS < -120_000 || edge.RMSMilliDBFS > 0 || edge.PeakMilliDBFS < -120_000 || edge.PeakMilliDBFS > 0 || edge.PeakMilliDBFS < edge.RMSMilliDBFS {
		return fmt.Errorf("window or level support is invalid")
	}
	for name, intervals := range map[string][]mediatools.Interval{"black": edge.Black, "silence": edge.Silence} {
		lastEnd := wantStart
		for _, interval := range intervals {
			if interval.StartMs < wantStart || interval.EndMs <= interval.StartMs || interval.EndMs > wantEnd || interval.StartMs < lastEnd {
				return fmt.Errorf("%s intervals are invalid or unordered", name)
			}
			lastEnd = interval.EndMs
		}
	}
	return nil
}

func temporalTransitionStratum(first, second TemporalTransitionAuthorityCase) (TemporalTransitionStratum, bool) {
	if temporalTransitionTouchesTail(first.Tail, first.Tail.Black) || temporalTransitionTouchesHead(second.Head, second.Head.Black) {
		return TemporalTransitionBlackBoundary, true
	}
	if !temporalTransitionTouchesTail(first.Tail, first.Tail.Silence) && !temporalTransitionTouchesHead(second.Head, second.Head.Silence) {
		return TemporalTransitionAudibleNonblackCut, true
	}
	return TemporalTransitionSilenceTouchedNonblackCut, true
}

func temporalTransitionTouchesHead(edge TemporalTransitionEdge, intervals []mediatools.Interval) bool {
	for _, interval := range intervals {
		if interval.StartMs <= edge.StartMS+TemporalTransitionBoundaryToleranceMS {
			return true
		}
	}
	return false
}

func temporalTransitionTouchesTail(edge TemporalTransitionEdge, intervals []mediatools.Interval) bool {
	for _, interval := range intervals {
		if interval.EndMs >= edge.EndMS-TemporalTransitionBoundaryToleranceMS {
			return true
		}
	}
	return false
}
