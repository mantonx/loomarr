package fillerreview

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/loomarr/loomarr/internal/fillerstructurewindowcert"
)

func validateTemporalStructureWindowMeasuredEvidence(evidence TemporalStructureWindowMeasuredEvidence, loaded temporalStructureWindowSuiteLoaded) error {
	if evidence.SchemaVersion != TemporalStructureWindowEvidenceSchemaVersion || evidence.ContractVersion != TemporalStructureWindowEvidenceContractVersion ||
		evidence.MeasuredAt.IsZero() || evidence.MeasuredAt != evidence.MeasuredAt.UTC() || evidence.SHA256 != temporalStructureWindowMeasuredEvidenceSHA256(evidence) ||
		evidence.WindowSetManifestSHA256 != loaded.windowSetManifestSHA || evidence.WindowSetAuthoritySHA256 != loaded.windowSetAuthoritySHA ||
		evidence.CorpusManifestSHA256 != loaded.corpusManifestSHA || evidence.CorpusAuthoritySHA256 != loaded.corpusAuthoritySHA ||
		evidence.HoldoutAuthoringSHA256 != loaded.authoringSHA || evidence.HoldoutReceiptSHA256 != loaded.receiptSHA ||
		evidence.EvidenceManifestSHA256 != loaded.evidenceManifestSHA || evidence.EvidencePrivateMapSHA256 != loaded.evidencePrivateMapSHA ||
		evidence.MotionScale != TemporalStructureWindowMotionScale || evidence.MinimumHighMotionMeanMicroluma <= 0 ||
		strings.TrimSpace(evidence.MotionTool.Path) == "" || strings.TrimSpace(evidence.MotionTool.Version) == "" ||
		!reviewSHA256(evidence.MotionTool.BinarySHA256) || evidence.TrainingAllowed || evidence.ProductionAdmissionAllowed {
		return errors.New("window measured evidence identity or disposition is invalid")
	}
	wantWordless, err := temporalStructureWindowWordlessEvidence(loaded)
	if err != nil || !reflect.DeepEqual(evidence.WordlessJoins, wantWordless) {
		return errors.New("window measured wordless evidence does not reproduce")
	}
	if err := validateTemporalStructureWindowMotionRecords(evidence, loaded); err != nil {
		return err
	}
	return nil
}

func validateTemporalStructureWindowMotionRecords(evidence TemporalStructureWindowMeasuredEvidence, loaded temporalStructureWindowSuiteLoaded) error {
	privateByAlias := make(map[string]TemporalStructureWindowSetAuthorityCase, len(loaded.windowSetAuthority.Cases))
	for _, item := range loaded.windowSetAuthority.Cases {
		privateByAlias[item.Alias] = item
	}
	expected := 0
	best := make(map[string]int, len(loaded.windowSetManifest.Cases))
	for _, item := range loaded.windowSetManifest.Cases {
		private := privateByAlias[item.Alias]
		for ordinal, media := range item.MediaSet.Windows {
			if expected >= len(evidence.MotionWindows) {
				return errors.New("window measured motion evidence is incomplete")
			}
			measured := evidence.MotionWindows[expected]
			if measured.CaseID != private.CaseID || measured.Alias != item.Alias || measured.WindowOrdinal != ordinal ||
				measured.MediaSHA256 != media.Media.SHA256 || measured.MediaDurationMS != media.Media.DurationMS ||
				measured.MeanMicroluma != roundedMotionMean(measured.SumMicroluma, measured.Frames) ||
				!validTemporalStructureWindowMotionSample(TemporalStructureWindowMotionSample{
					Frames: measured.Frames, SumMicroluma: measured.SumMicroluma,
					P95Microluma: measured.P95Microluma, MaximumMicroluma: measured.MaximumMicroluma,
				}) || measured.Frames < media.Media.DurationMS*20/1_000 || measured.Frames > media.Media.DurationMS*40/1_000+1 {
				return fmt.Errorf("window measured motion record %d drifted", expected)
			}
			if current, found := best[measured.CaseID]; !found || motionEvidenceGreater(measured, evidence.MotionWindows[current]) {
				best[measured.CaseID] = expected
			}
			expected++
		}
	}
	if expected != len(evidence.MotionWindows) || len(best) != len(loaded.windowSetManifest.Cases) {
		return errors.New("window measured motion evidence count drifted")
	}
	ranked := make([]int, 0, len(best))
	for _, index := range best {
		ranked = append(ranked, index)
	}
	sort.Slice(ranked, func(i, j int) bool {
		left, right := evidence.MotionWindows[ranked[i]], evidence.MotionWindows[ranked[j]]
		if motionEvidenceGreater(left, right) {
			return true
		}
		if motionEvidenceGreater(right, left) {
			return false
		}
		if left.CaseID != right.CaseID {
			return left.CaseID < right.CaseID
		}
		return left.WindowOrdinal < right.WindowOrdinal
	})
	selected := make(map[int]struct{}, fillerstructurewindowcert.MinimumSliceCases)
	for _, index := range ranked[:fillerstructurewindowcert.MinimumSliceCases] {
		selected[index] = struct{}{}
	}
	for index, item := range evidence.MotionWindows {
		_, want := selected[index]
		if item.Selected != want {
			return errors.New("window measured high-motion selection does not reproduce")
		}
	}
	minimum := evidence.MotionWindows[ranked[fillerstructurewindowcert.MinimumSliceCases-1]].MeanMicroluma
	if minimum <= 0 || evidence.MinimumHighMotionMeanMicroluma != minimum {
		return errors.New("window measured high-motion threshold does not reproduce")
	}
	return nil
}
