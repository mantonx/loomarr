package fillerquarantine

import (
	"fmt"
	"slices"
	"strings"

	"github.com/loomarr/loomarr/internal/fillerreview"
	"github.com/loomarr/loomarr/internal/mediatools"
)

// Validate proves the report is complete, deterministically ordered, and
// incapable of granting authority beyond local quarantine inspection.
func Validate(report Report) error {
	if report.SchemaVersion != SchemaVersion || report.ContractVersion != ContractVersion || report.GeneratedAt.IsZero() || report.Ceilings.MaxMediaWallTimeMS <= 0 || report.Algorithm == "" {
		return fmt.Errorf("report identity is invalid")
	}
	for _, digest := range []string{report.Inputs.InventorySHA256, report.Inputs.DownloadLedgerSHA256, report.Inputs.PriorPublicManifestSHA256, report.Inputs.PriorAuthoritySHA256} {
		if !validSHA256(digest) {
			return fmt.Errorf("report input identity is invalid")
		}
	}
	for name, tool := range map[string]fillerreview.TemporalTruthToolIdentity{"ffmpeg": report.MediaTools.FFmpeg, "ffprobe": report.MediaTools.FFprobe} {
		if strings.TrimSpace(tool.Path) == "" || strings.TrimSpace(tool.Version) == "" || !validSHA256(tool.BinarySHA256) {
			return fmt.Errorf("report %s identity is invalid", name)
		}
	}
	if !report.Authority.CopyAndStorage || !report.Authority.LocalTechnicalInspection || report.Authority.ProviderTransfer || report.Authority.Redistribution || report.Authority.CorpusPreparation || report.Authority.Training || report.Authority.CatalogIngestion || report.Authority.Scheduling || report.Authority.ProductionAdmission {
		return fmt.Errorf("report authority exceeds quarantine")
	}
	if report.Summary.Cases != len(report.Cases) || report.Summary.PriorSources != len(report.PriorSources) || report.Summary.Cases == 0 || report.Summary.EligibleForRightsReview+report.Summary.Held != report.Summary.Cases {
		return fmt.Errorf("report summary is inconsistent")
	}

	knownCases := make(map[string]struct{}, len(report.Cases))
	requiredReasons := make(map[string]map[string]struct{}, len(report.Cases))
	exact, eligible, held := 0, 0, 0
	for index, item := range report.Cases {
		if index > 0 && report.Cases[index-1].CaseID >= item.CaseID {
			return fmt.Errorf("report cases are not uniquely sorted")
		}
		if item.CaseID == "" || item.LocalFile == "" || !validSHA256(item.ContentSHA256) || item.Bytes <= 0 || item.ExpectedMedia.Bytes != item.Bytes || item.ExpectedMedia.DurationMS < 0 || item.ExpectedMedia.Width < 0 || item.ExpectedMedia.Height < 0 || item.Media.DurationMS <= 0 || item.Fingerprint.AudioBinCount < 0 || !slices.IsSorted(item.HoldReasons) {
			return fmt.Errorf("report case %q is incomplete", item.CaseID)
		}
		requiredReasons[item.CaseID] = map[string]struct{}{}
		if item.Media.HasVideo {
			if item.Media.Width <= 0 || item.Media.Height <= 0 || item.Media.Quality.DurationMs != item.Media.DurationMS || item.Media.Quality.EvidenceVersion != mediatools.MediaQualityEvidenceV1 || item.Media.Quality.Provenance != mediatools.MediaQualityProvenanceFFmpegDetectors || !validIntervals(item.Media.Quality.Black, item.Media.DurationMS) || !validIntervals(item.Media.Quality.Silence, item.Media.DurationMS) || !validIntervals(item.Media.Quality.Freeze, item.Media.DurationMS) || item.Fingerprint.FrameCount <= 0 || !validSHA256(item.Fingerprint.FrameSHA256) || !validSHA256(item.Fingerprint.AudioRMSSHA256) {
				return fmt.Errorf("report case %q has invalid decoded media evidence", item.CaseID)
			}
		} else if item.Media.Width < 0 || item.Media.Height < 0 || !zeroQuality(item.Media.Quality) || item.Fingerprint != (FingerprintEvidence{}) {
			return fmt.Errorf("report case %q has inconsistent missing-video evidence", item.CaseID)
		}
		addTechnicalReasons(requiredReasons[item.CaseID], item)
		if _, duplicate := knownCases[item.CaseID]; duplicate {
			return fmt.Errorf("report repeats case %q", item.CaseID)
		}
		knownCases[item.CaseID] = struct{}{}
		for _, collision := range item.ExactExposure {
			if collision.Scope == "" || collision.Identity == "" || collision.SHA256 != item.ContentSHA256 {
				return fmt.Errorf("report case %q has invalid exact exposure", item.CaseID)
			}
			exact++
		}
		if len(item.ExactExposure) != 0 {
			addReason(requiredReasons[item.CaseID], "exact_content_collision")
		}
		switch item.Disposition {
		case DispositionEligibleForRightsReview:
			if len(item.HoldReasons) != 0 {
				return fmt.Errorf("eligible case %q has hold reasons", item.CaseID)
			}
			eligible++
		case DispositionHold:
			if len(item.HoldReasons) == 0 {
				return fmt.Errorf("held case %q has no hold reasons", item.CaseID)
			}
			held++
		default:
			return fmt.Errorf("case %q has invalid disposition", item.CaseID)
		}
	}
	if exact != report.Summary.ExactExposureCollisions || eligible != report.Summary.EligibleForRightsReview || held != report.Summary.Held {
		return fmt.Errorf("report case counts do not match summary")
	}

	knownPrior := make(map[string]struct{}, len(report.PriorSources))
	unavailable := 0
	for index, source := range report.PriorSources {
		if index > 0 && report.PriorSources[index-1].SourceSHA256 >= source.SourceSHA256 {
			return fmt.Errorf("prior sources are not uniquely sorted")
		}
		if source.SourceID == "" || source.SourcePath == "" || !validSHA256(source.SourceSHA256) || source.DurationMS <= 0 {
			return fmt.Errorf("prior source is incomplete")
		}
		if source.Available {
			if source.Fingerprint.FrameCount <= 0 || !validSHA256(source.Fingerprint.FrameSHA256) || !validSHA256(source.Fingerprint.AudioRMSSHA256) {
				return fmt.Errorf("available prior source %q lacks fingerprint evidence", source.SourceID)
			}
		} else {
			unavailable++
			if source.Fingerprint != (FingerprintEvidence{}) {
				return fmt.Errorf("unavailable prior source %q has fingerprint evidence", source.SourceID)
			}
		}
		if _, duplicate := knownPrior[source.SourceID]; duplicate {
			return fmt.Errorf("report repeats prior source id %q", source.SourceID)
		}
		knownPrior[source.SourceID] = struct{}{}
	}
	if unavailable != report.Summary.UnavailablePriorSources {
		return fmt.Errorf("prior availability does not match summary")
	}
	if unavailable != 0 {
		for id := range requiredReasons {
			addReason(requiredReasons[id], "prior_perceptual_exposure_incomplete")
		}
	}

	relatedCandidates, relatedPrior := 0, 0
	seenComparisons := make(map[string]struct{}, len(report.Comparisons))
	for index, item := range report.Comparisons {
		key := item.Scope + "\x00" + item.CaseA + "\x00" + item.CaseB
		if index > 0 {
			prior := report.Comparisons[index-1]
			if prior.Scope+"\x00"+prior.CaseA+"\x00"+prior.CaseB >= key {
				return fmt.Errorf("comparisons are not uniquely sorted")
			}
		}
		if _, ok := knownCases[item.CaseA]; !ok {
			return fmt.Errorf("comparison contains unknown candidate %q", item.CaseA)
		}
		if _, duplicate := seenComparisons[key]; duplicate {
			return fmt.Errorf("report repeats comparison")
		}
		seenComparisons[key] = struct{}{}
		expectedBasis := []string{}
		if item.Visual.Related {
			expectedBasis = append(expectedBasis, "visual")
		}
		if item.Audio.Related {
			expectedBasis = append(expectedBasis, "audio")
		}
		if item.Related != (len(expectedBasis) != 0) || !slices.Equal(item.Basis, expectedBasis) {
			return fmt.Errorf("comparison relationship is inconsistent")
		}
		switch item.Scope {
		case ComparisonCandidate:
			if _, ok := knownCases[item.CaseB]; !ok || item.CaseA >= item.CaseB {
				return fmt.Errorf("candidate comparison is invalid")
			}
			if item.Related {
				relatedCandidates++
				addReason(requiredReasons[item.CaseA], "candidate_duplicate_risk")
				addReason(requiredReasons[item.CaseB], "candidate_duplicate_risk")
			}
		case ComparisonPrior:
			if _, ok := knownPrior[item.CaseB]; !ok {
				return fmt.Errorf("prior comparison is invalid")
			}
			if item.Related {
				relatedPrior++
				addReason(requiredReasons[item.CaseA], "prior_perceptual_exposure")
			}
		default:
			return fmt.Errorf("comparison has invalid scope")
		}
	}
	availablePrior := len(report.PriorSources) - unavailable
	expectedComparisons := len(report.Cases)*(len(report.Cases)-1)/2 + len(report.Cases)*availablePrior
	if len(report.Comparisons) != expectedComparisons || relatedCandidates != report.Summary.RelatedCandidatePairs || relatedPrior != report.Summary.RelatedPriorExposurePairs {
		return fmt.Errorf("comparison coverage or summary is incomplete")
	}
	for _, item := range report.Cases {
		expected := make([]string, 0, len(requiredReasons[item.CaseID]))
		for reason := range requiredReasons[item.CaseID] {
			expected = append(expected, reason)
		}
		slices.Sort(expected)
		if !slices.Equal(item.HoldReasons, expected) {
			return fmt.Errorf("case %q hold reasons do not follow from its evidence", item.CaseID)
		}
	}
	return nil
}

func addTechnicalReasons(reasons map[string]struct{}, item Case) {
	if !item.Media.HasVideo {
		addReason(reasons, "missing_video")
	}
	if !item.Media.HasAudio {
		addReason(reasons, "missing_audio")
	}
	if item.ExpectedMedia.DurationMS > 0 && absolute(item.Media.DurationMS-item.ExpectedMedia.DurationMS) > 1_000 {
		addReason(reasons, "duration_mismatch")
	}
	if item.ExpectedMedia.Width > 0 && item.Media.Width != item.ExpectedMedia.Width || item.ExpectedMedia.Height > 0 && item.Media.Height != item.ExpectedMedia.Height {
		addReason(reasons, "dimension_mismatch")
	}
	if intervalCoverage(item.Media.Quality.Black, item.Media.Quality.DurationMs) >= qualityFailureCoverage {
		addReason(reasons, "mostly_black")
	}
	if intervalCoverage(item.Media.Quality.Freeze, item.Media.Quality.DurationMs) >= qualityFailureCoverage {
		addReason(reasons, "mostly_frozen")
	}
	if item.Media.HasAudio && intervalCoverage(item.Media.Quality.Silence, item.Media.Quality.DurationMs) >= qualityFailureCoverage {
		addReason(reasons, "mostly_silent")
	}
	if !item.Fingerprint.VisualComparable && !item.Fingerprint.AudioComparable {
		addReason(reasons, "fingerprint_unusable")
	}
}

func addReason(reasons map[string]struct{}, reason string) {
	reasons[reason] = struct{}{}
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if character < '0' || (character > '9' && character < 'a') || character > 'f' {
			return false
		}
	}
	return true
}

func validIntervals(values []mediatools.Interval, duration int64) bool {
	end := int64(0)
	for _, value := range values {
		if value.StartMs < end || value.StartMs < 0 || value.EndMs <= value.StartMs || value.EndMs > duration {
			return false
		}
		end = value.EndMs
	}
	return true
}

func zeroQuality(value mediatools.MediaQuality) bool {
	return value.EvidenceVersion == 0 && value.Provenance == "" && value.DurationMs == 0 && len(value.Black) == 0 && len(value.Silence) == 0 && len(value.Freeze) == 0
}
