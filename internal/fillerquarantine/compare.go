package fillerquarantine

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"

	"github.com/loomarr/loomarr/internal/fillerreference"
	"github.com/loomarr/loomarr/internal/fillerreview"
)

func compareCandidates(ctx context.Context, report *Report, fingerprints map[string]fingerprint) error {
	for i := range report.Cases {
		for j := i + 1; j < len(report.Cases); j++ {
			left, right := report.Cases[i].CaseID, report.Cases[j].CaseID
			comparison, err := compare(ctx, ComparisonCandidate, left, right, fingerprints[left], fingerprints[right])
			if err != nil {
				return fmt.Errorf("compare candidates %q and %q: %w", left, right, err)
			}
			report.Comparisons = append(report.Comparisons, comparison)
			if comparison.Related {
				report.Summary.RelatedCandidatePairs++
				report.Cases[i].HoldReasons = append(report.Cases[i].HoldReasons, "candidate_duplicate_risk")
				report.Cases[j].HoldReasons = append(report.Cases[j].HoldReasons, "candidate_duplicate_risk")
			}
		}
	}
	return nil
}

func comparePriorExposure(ctx context.Context, report *Report, candidates map[string]fingerprint, prior map[string]fingerprint, incomplete bool) error {
	for caseIndex := range report.Cases {
		item := &report.Cases[caseIndex]
		if incomplete {
			item.HoldReasons = append(item.HoldReasons, "prior_perceptual_exposure_incomplete")
		}
		for _, source := range report.PriorSources {
			fp, ok := prior[source.SourceSHA256]
			if !ok {
				continue
			}
			comparison, err := compare(ctx, ComparisonPrior, item.CaseID, source.SourceID, candidates[item.CaseID], fp)
			if err != nil {
				return fmt.Errorf("compare candidate %q to prior source %q: %w", item.CaseID, source.SourceID, err)
			}
			report.Comparisons = append(report.Comparisons, comparison)
			if comparison.Related {
				report.Summary.RelatedPriorExposurePairs++
				item.HoldReasons = append(item.HoldReasons, "prior_perceptual_exposure")
			}
		}
	}
	return nil
}

func compare(ctx context.Context, scope, left, right string, a, b fingerprint) (Comparison, error) {
	visual, err := fillerreference.CompareDuplicateSequencesContext(ctx, a.frames, b.frames)
	if err != nil {
		return Comparison{}, err
	}
	audio, err := fillerreference.CompareAudioEnvelopesContext(ctx, a.audio, b.audio)
	if err != nil {
		return Comparison{}, err
	}
	result := Comparison{Scope: scope, CaseA: left, CaseB: right, Visual: visual, Audio: audio, Related: visual.Related || audio.Related}
	if visual.Related {
		result.Basis = append(result.Basis, "visual")
	}
	if audio.Related {
		result.Basis = append(result.Basis, "audio")
	}
	return result, nil
}

func applyExactExposure(report *Report, authority fillerreview.TemporalStructureChallengeAuthority) {
	sourceHashes := make(map[string]string)
	renderedHashes := make(map[string]string)
	for _, item := range authority.Cases {
		renderedHashes[item.VideoSHA256] = item.Alias
		for _, segment := range item.Segments {
			sourceHashes[segment.SourceSHA256] = segment.SourceID
		}
	}
	candidateHashes := make(map[string][]int)
	for index := range report.Cases {
		candidateHashes[report.Cases[index].ContentSHA256] = append(candidateHashes[report.Cases[index].ContentSHA256], index)
	}
	for index := range report.Cases {
		item := &report.Cases[index]
		if id, ok := sourceHashes[item.ContentSHA256]; ok {
			item.ExactExposure = append(item.ExactExposure, ExactExposure{Scope: "prior_source", Identity: id, SHA256: item.ContentSHA256})
		}
		if id, ok := renderedHashes[item.ContentSHA256]; ok {
			item.ExactExposure = append(item.ExactExposure, ExactExposure{Scope: "prior_rendered_case", Identity: id, SHA256: item.ContentSHA256})
		}
		for _, other := range candidateHashes[item.ContentSHA256] {
			if other != index {
				item.ExactExposure = append(item.ExactExposure, ExactExposure{Scope: "candidate", Identity: report.Cases[other].CaseID, SHA256: item.ContentSHA256})
			}
		}
		if len(item.ExactExposure) != 0 {
			report.Summary.ExactExposureCollisions += len(item.ExactExposure)
			item.HoldReasons = append(item.HoldReasons, "exact_content_collision")
		}
	}
}

func finalize(report *Report) {
	for index := range report.Cases {
		item := &report.Cases[index]
		slices.Sort(item.HoldReasons)
		item.HoldReasons = slices.Compact(item.HoldReasons)
		slices.SortFunc(item.ExactExposure, func(a, b ExactExposure) int {
			return strings.Compare(a.Scope+"\x00"+a.Identity, b.Scope+"\x00"+b.Identity)
		})
		if len(item.HoldReasons) == 0 {
			item.Disposition = DispositionEligibleForRightsReview
			report.Summary.EligibleForRightsReview++
		} else {
			item.Disposition = DispositionHold
			report.Summary.Held++
		}
	}
	report.Summary.Cases = len(report.Cases)
	slices.SortFunc(report.Comparisons, func(a, b Comparison) int {
		return strings.Compare(a.Scope+"\x00"+a.CaseA+"\x00"+a.CaseB, b.Scope+"\x00"+b.CaseA+"\x00"+b.CaseB)
	})
}

func fingerprintEvidence(fp fingerprint) FingerprintEvidence {
	return FingerprintEvidence{
		FrameCount: len(fp.frames), FrameSHA256: hashUint64(fp.frames), AudioBinCount: len(fp.audio), AudioRMSSHA256: hashUint32(fp.audio),
		VisualComparable: fillerreference.VisualFingerprintComparable(fp.frames),
		AudioComparable:  fillerreference.AudioFingerprintComparable(fp.audio),
	}
}

func hashUint64(values []uint64) string {
	hash := sha256.New()
	var buffer [8]byte
	for _, value := range values {
		binary.BigEndian.PutUint64(buffer[:], value)
		_, _ = hash.Write(buffer[:])
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func hashUint32(values []uint32) string {
	hash := sha256.New()
	var buffer [4]byte
	for _, value := range values {
		binary.BigEndian.PutUint32(buffer[:], value)
		_, _ = hash.Write(buffer[:])
	}
	return hex.EncodeToString(hash.Sum(nil))
}
