package fillerreference

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"math/bits"
	"slices"
	"strings"
	"time"
)

const (
	// DuplicateAlgorithm versions the decode cadence, center crop, dHash layout,
	// and ordered-sequence comparison used by filler-reference-families.
	DuplicateAlgorithm = "duplicate-v3-visual-dhash-2fps-fixedoffset-or-audio-rms100ms-corr90"

	duplicateFrameDistance = 12
	duplicateMinFrames     = 12
	duplicateMinCoverage   = 0.70
	duplicateAudioMinBins  = 50
	duplicateAudioCorr     = 0.90
)

// DuplicateComparison is evidence that two captures contain the same moving-
// image unit. Related is deliberately conservative: callers must not collapse
// a pair based on a title, filename, or a handful of matching black frames.
type DuplicateComparison struct {
	Related         bool    `json:"related"`
	MatchedFrames   int     `json:"matchedFrames"`
	ComparedFramesA int     `json:"comparedFramesA"`
	ComparedFramesB int     `json:"comparedFramesB"`
	Coverage        float64 `json:"coverage"`
	MeanDistance    float64 `json:"meanDistance,omitempty"`
	MaximumDistance int     `json:"maximumDistance,omitempty"`
}

type FamilyFingerprint struct {
	CaseID        string   `json:"caseId"`
	ContentSHA256 string   `json:"contentSha256"`
	LocalFile     string   `json:"localFile"`
	FrameHashes   []uint64 `json:"frameHashes"`
	AudioRMS      []uint32 `json:"audioRms100ms"`
}

type FamilyPair struct {
	CaseA      string              `json:"caseA"`
	CaseB      string              `json:"caseB"`
	Basis      []string            `json:"basis,omitempty"`
	Comparison DuplicateComparison `json:"visualComparison"`
	Audio      AudioComparison     `json:"audioComparison"`
}

type AudioComparison struct {
	Related      bool    `json:"related"`
	Correlation  float64 `json:"correlation,omitempty"`
	ComparedBins int     `json:"comparedBins"`
	Coverage     float64 `json:"coverage"`
	OffsetBins   int     `json:"offsetBins"`
}

type DuplicateFamily struct {
	FamilyID       string   `json:"familyId"`
	Members        []string `json:"members"`
	CompleteClique bool     `json:"completeClique"`
	PreferredCase  string   `json:"preferredCase,omitempty"`
}

type FamilySummary struct {
	Cases             int `json:"cases"`
	RelatedPairs      int `json:"relatedPairs"`
	ClosestNonMatches int `json:"closestNonMatches"`
	DuplicateFamilies int `json:"duplicateFamilies"`
	NonCliqueFamilies int `json:"nonCliqueFamilies"`
}

type FamilyAudit struct {
	SchemaVersion int                 `json:"schemaVersion"`
	Algorithm     string              `json:"algorithm"`
	GeneratedAt   time.Time           `json:"generatedAt"`
	SourceAudit   string              `json:"sourceAuditSha256"`
	Summary       FamilySummary       `json:"summary"`
	Fingerprints  []FamilyFingerprint `json:"fingerprints"`
	Pairs         []FamilyPair        `json:"relatedPairs"`
	ClosestPairs  []FamilyPair        `json:"closestNonMatches"`
	Families      []DuplicateFamily   `json:"families"`
}

// BuildFamilyAudit compares fingerprints bound to the exact raw Gate A audit.
// It reports connected components but leaves PreferredCase empty: choosing a
// rendition requires full playback and cannot be inferred from measurements.
func BuildFamilyAudit(sourceAuditRaw []byte, fingerprints []FamilyFingerprint, generatedAt time.Time) (FamilyAudit, error) {
	sourceAudit, err := decodeStrictJSON[Audit](sourceAuditRaw)
	if err != nil {
		return FamilyAudit{}, fmt.Errorf("source audit: %w", err)
	}
	if err := validateFamilyInputs(sourceAudit, fingerprints, generatedAt); err != nil {
		return FamilyAudit{}, err
	}
	fingerprints = cloneFingerprints(fingerprints)
	slices.SortFunc(fingerprints, func(a, b FamilyFingerprint) int { return strings.Compare(a.CaseID, b.CaseID) })
	parents := make([]int, len(fingerprints))
	for i := range parents {
		parents[i] = i
	}
	var find func(int) int
	find = func(index int) int {
		if parents[index] != index {
			parents[index] = find(parents[index])
		}
		return parents[index]
	}
	var pairs, nonMatches []FamilyPair
	pairSet := map[string]struct{}{}
	for i := range fingerprints {
		for j := i + 1; j < len(fingerprints); j++ {
			visual := CompareDuplicateSequences(fingerprints[i].FrameHashes, fingerprints[j].FrameHashes)
			audio := CompareAudioEnvelopes(fingerprints[i].AudioRMS, fingerprints[j].AudioRMS)
			pair := FamilyPair{CaseA: fingerprints[i].CaseID, CaseB: fingerprints[j].CaseID, Comparison: visual, Audio: audio}
			if visual.Related {
				pair.Basis = append(pair.Basis, "visual")
			}
			if audio.Related {
				pair.Basis = append(pair.Basis, "audio")
			}
			if len(pair.Basis) == 0 {
				nonMatches = append(nonMatches, pair)
				continue
			}
			pairs = append(pairs, pair)
			pairSet[fingerprints[i].CaseID+"\x00"+fingerprints[j].CaseID] = struct{}{}
			left, right := find(i), find(j)
			if left != right {
				parents[right] = left
			}
		}
	}
	slices.SortFunc(nonMatches, func(a, b FamilyPair) int {
		if a.Audio.Correlation > b.Audio.Correlation {
			return -1
		}
		if a.Audio.Correlation < b.Audio.Correlation {
			return 1
		}
		if a.Comparison.Coverage > b.Comparison.Coverage {
			return -1
		}
		if a.Comparison.Coverage < b.Comparison.Coverage {
			return 1
		}
		if a.Comparison.MeanDistance < b.Comparison.MeanDistance {
			return -1
		}
		if a.Comparison.MeanDistance > b.Comparison.MeanDistance {
			return 1
		}
		return strings.Compare(a.CaseA+"\x00"+a.CaseB, b.CaseA+"\x00"+b.CaseB)
	})
	if len(nonMatches) > 20 {
		nonMatches = nonMatches[:20]
	}
	components := map[int][]string{}
	for i, fingerprint := range fingerprints {
		components[find(i)] = append(components[find(i)], fingerprint.CaseID)
	}
	var families []DuplicateFamily
	nonClique := 0
	for _, members := range components {
		if len(members) < 2 {
			continue
		}
		slices.Sort(members)
		clique := true
		for i := range members {
			for j := i + 1; j < len(members); j++ {
				if _, ok := pairSet[members[i]+"\x00"+members[j]]; !ok {
					clique = false
				}
			}
		}
		if !clique {
			nonClique++
		}
		families = append(families, DuplicateFamily{FamilyID: duplicateFamilyID(members), Members: members, CompleteClique: clique})
	}
	slices.SortFunc(families, func(a, b DuplicateFamily) int { return strings.Compare(a.FamilyID, b.FamilyID) })
	return FamilyAudit{
		SchemaVersion: 3, Algorithm: DuplicateAlgorithm, GeneratedAt: generatedAt.UTC(), SourceAudit: SHA256(sourceAuditRaw),
		Summary:      FamilySummary{Cases: len(fingerprints), RelatedPairs: len(pairs), ClosestNonMatches: len(nonMatches), DuplicateFamilies: len(families), NonCliqueFamilies: nonClique},
		Fingerprints: fingerprints, Pairs: pairs, ClosestPairs: nonMatches, Families: families,
	}, nil
}

func duplicateFamilyID(members []string) string {
	digest := sha256.Sum256([]byte(strings.Join(members, "\n")))
	return "duplicate-family-" + hex.EncodeToString(digest[:12])
}

func validateFamilyInputs(source Audit, fingerprints []FamilyFingerprint, generatedAt time.Time) error {
	if source.SchemaVersion != AuditSchemaVersion || source.Contract != ContractVersion || source.GeneratedAt.IsZero() || generatedAt.IsZero() || generatedAt.Before(source.GeneratedAt) || source.Summary.Cases != 300 || len(source.Cases) != source.Summary.Cases {
		return fmt.Errorf("source audit identity, time, or case count is invalid")
	}
	if !validSHA256(source.Inputs.ManifestSHA256) || !validSHA256(source.Inputs.PacketsSHA256) || !validSHA256(source.Inputs.MappingSHA256) || !validSHA256(source.Inputs.DownloadLedgerSHA256) || !validSHA256(source.Inputs.ContentReviewSHA256) {
		return fmt.Errorf("source audit input identities are invalid")
	}
	expected := make(map[string]Case, len(source.Cases))
	candidates, holds, excluded := 0, 0, 0
	for _, item := range source.Cases {
		if item.CaseID == "" || !validSHA256(item.ContentSHA256) || item.SourceLocalFile == "" {
			return fmt.Errorf("source audit contains an incomplete case")
		}
		if _, duplicate := expected[item.CaseID]; duplicate {
			return fmt.Errorf("source audit repeats case %q", item.CaseID)
		}
		switch item.Disposition {
		case DispositionCandidate:
			candidates++
		case DispositionHold:
			holds++
		case DispositionExclude:
			excluded++
		default:
			return fmt.Errorf("source audit case %q has invalid disposition %q", item.CaseID, item.Disposition)
		}
		expected[item.CaseID] = item
	}
	if candidates != source.Summary.Candidates || holds != source.Summary.Holds || excluded != source.Summary.Excluded || candidates+holds+excluded != source.Summary.Cases {
		return fmt.Errorf("source audit disposition summary is invalid")
	}
	if len(fingerprints) != candidates+holds {
		return fmt.Errorf("family inventory has %d fingerprints, want %d non-excluded cases", len(fingerprints), candidates+holds)
	}
	seen := make(map[string]struct{}, len(fingerprints))
	seenContent := make(map[string]struct{}, len(fingerprints))
	seenFiles := make(map[string]struct{}, len(fingerprints))
	for _, fingerprint := range fingerprints {
		item, ok := expected[fingerprint.CaseID]
		if !ok || item.Disposition == DispositionExclude {
			return fmt.Errorf("family inventory contains extra or excluded case %q", fingerprint.CaseID)
		}
		if _, duplicate := seen[fingerprint.CaseID]; duplicate {
			return fmt.Errorf("family inventory repeats case %q", fingerprint.CaseID)
		}
		if fingerprint.ContentSHA256 != item.ContentSHA256 || fingerprint.LocalFile != item.SourceLocalFile || len(fingerprint.FrameHashes) == 0 || len(fingerprint.AudioRMS) == 0 {
			return fmt.Errorf("family inventory case %q is not content-bound to the source audit", fingerprint.CaseID)
		}
		if _, duplicate := seenContent[fingerprint.ContentSHA256]; duplicate {
			return fmt.Errorf("family inventory repeats content identity %q", fingerprint.ContentSHA256)
		}
		if _, duplicate := seenFiles[fingerprint.LocalFile]; duplicate {
			return fmt.Errorf("family inventory repeats local file %q", fingerprint.LocalFile)
		}
		seen[fingerprint.CaseID] = struct{}{}
		seenContent[fingerprint.ContentSHA256] = struct{}{}
		seenFiles[fingerprint.LocalFile] = struct{}{}
	}
	for id, item := range expected {
		if item.Disposition == DispositionExclude {
			continue
		}
		if _, ok := seen[id]; !ok {
			return fmt.Errorf("family inventory is missing case %q", id)
		}
	}
	return nil
}

func cloneFingerprints(input []FamilyFingerprint) []FamilyFingerprint {
	result := make([]FamilyFingerprint, len(input))
	for i, fingerprint := range input {
		result[i] = fingerprint
		result[i].FrameHashes = slices.Clone(fingerprint.FrameHashes)
		result[i].AudioRMS = slices.Clone(fingerprint.AudioRMS)
	}
	return result
}

// ValidateSplit rejects missing/extra assignments and any similarity family
// whose members cross an evaluation split boundary.
func (a FamilyAudit) ValidateSplit(caseSplits map[string]string) error {
	if len(caseSplits) != len(a.Fingerprints) {
		return fmt.Errorf("split assignment has %d cases, want %d", len(caseSplits), len(a.Fingerprints))
	}
	known := make(map[string]struct{}, len(a.Fingerprints))
	for _, fingerprint := range a.Fingerprints {
		if _, duplicate := known[fingerprint.CaseID]; duplicate {
			return fmt.Errorf("family audit repeats case %q", fingerprint.CaseID)
		}
		known[fingerprint.CaseID] = struct{}{}
		if strings.TrimSpace(caseSplits[fingerprint.CaseID]) == "" {
			return fmt.Errorf("case %q has no split assignment", fingerprint.CaseID)
		}
	}
	for id := range caseSplits {
		if _, ok := known[id]; !ok {
			return fmt.Errorf("split assignment contains extra case %q", id)
		}
	}
	for _, family := range a.Families {
		var split string
		for _, id := range family.Members {
			assigned, ok := caseSplits[id]
			if !ok {
				return fmt.Errorf("family %q contains unknown case %q", family.FamilyID, id)
			}
			if split == "" {
				split = assigned
			} else if assigned != split {
				return fmt.Errorf("family %q crosses split boundary", family.FamilyID)
			}
		}
	}
	return nil
}

// CompareAudioEnvelopes aligns 100ms RMS bins at one fixed offset and reports
// normalized correlation. Constant/near-silent tracks have no useful variance
// and cannot establish a duplicate relationship.
func CompareAudioEnvelopes(a, b []uint32) AudioComparison {
	result, _ := CompareAudioEnvelopesContext(context.Background(), a, b)
	return result
}

// CompareAudioEnvelopesContext is the cancellable form used by bounded
// inspection jobs. Cancellation never produces a partial comparison.
func CompareAudioEnvelopesContext(ctx context.Context, a, b []uint32) (AudioComparison, error) {
	result := AudioComparison{}
	if len(a) < duplicateAudioMinBins || len(b) < duplicateAudioMinBins {
		return result, nil
	}
	minimum := min(len(a), len(b))
	minimumOverlap := int(float64(minimum) * duplicateMinCoverage)
	best := -2.0
	for offset := -len(a) + minimumOverlap; offset <= len(b)-minimumOverlap; offset++ {
		if offset&63 == 0 {
			select {
			case <-ctx.Done():
				return AudioComparison{}, ctx.Err()
			default:
			}
		}
		start := max(0, -offset)
		end := min(len(a), len(b)-offset)
		n := end - start
		if n < minimumOverlap {
			continue
		}
		var sumA, sumB float64
		for i := start; i < end; i++ {
			if i&1023 == 0 {
				select {
				case <-ctx.Done():
					return AudioComparison{}, ctx.Err()
				default:
				}
			}
			sumA += float64(a[i])
			sumB += float64(b[i+offset])
		}
		meanA, meanB := sumA/float64(n), sumB/float64(n)
		var numerator, varianceA, varianceB float64
		for i := start; i < end; i++ {
			if i&1023 == 0 {
				select {
				case <-ctx.Done():
					return AudioComparison{}, ctx.Err()
				default:
				}
			}
			deltaA := float64(a[i]) - meanA
			deltaB := float64(b[i+offset]) - meanB
			numerator += deltaA * deltaB
			varianceA += deltaA * deltaA
			varianceB += deltaB * deltaB
		}
		if varianceA == 0 || varianceB == 0 {
			continue
		}
		correlation := numerator / (math.Sqrt(varianceA) * math.Sqrt(varianceB))
		if correlation > best {
			best = correlation
			result.Correlation = correlation
			result.ComparedBins = n
			result.Coverage = float64(n) / float64(minimum)
			result.OffsetBins = offset
		}
	}
	result.Related = result.ComparedBins >= duplicateAudioMinBins && result.Correlation >= duplicateAudioCorr
	return result, nil
}

// AudioFingerprintComparable reports whether an audio envelope contains the
// minimum amount and variance needed to establish a duplicate relationship.
func AudioFingerprintComparable(value []uint32) bool {
	if len(value) < duplicateAudioMinBins {
		return false
	}
	first := value[0]
	for _, item := range value[1:] {
		if item != first {
			return true
		}
	}
	return false
}

// CompareDuplicateSequences performs an order-preserving match over useful
// dHash frames. Unlike the retired four-relative-frame comparison, it tolerates
// short leaders, trailers, and sample-phase shifts. A match must use one fixed
// time offset: it cannot collect convenient frames scattered across a long
// compilation. Near-flat frames do not count because black and white cards are
// common across unrelated adverts, but they retain their timeline position.
func CompareDuplicateSequences(a, b []uint64) DuplicateComparison {
	result, _ := CompareDuplicateSequencesContext(context.Background(), a, b)
	return result
}

// CompareDuplicateSequencesContext is the cancellable form used by bounded
// inspection jobs. Cancellation never produces a partial comparison.
func CompareDuplicateSequencesContext(ctx context.Context, a, b []uint64) (DuplicateComparison, error) {
	usefulA := informativeCount(a)
	usefulB := informativeCount(b)
	result := DuplicateComparison{ComparedFramesA: usefulA, ComparedFramesB: usefulB}
	if usefulA < duplicateMinFrames || usefulB < duplicateMinFrames {
		return result, nil
	}
	type alignment struct{ matches, distance, maximum int }
	best := alignment{}
	// offset is b's frame index minus a's. Every comparison along one
	// diagonal therefore represents one continuous temporal alignment.
	for offset := -len(a) + 1; offset < len(b); offset++ {
		if offset&63 == 0 {
			select {
			case <-ctx.Done():
				return DuplicateComparison{}, ctx.Err()
			default:
			}
		}
		candidate := alignment{}
		for i := max(0, -offset); i < len(a) && i+offset < len(b); i++ {
			if i&1023 == 0 {
				select {
				case <-ctx.Done():
					return DuplicateComparison{}, ctx.Err()
				default:
				}
			}
			j := i + offset
			if !informativeHash(a[i]) || !informativeHash(b[j]) {
				continue
			}
			distance := bits.OnesCount64(a[i] ^ b[j])
			if distance <= duplicateFrameDistance {
				candidate.matches++
				candidate.distance += distance
				candidate.maximum = max(candidate.maximum, distance)
			}
		}
		if candidate.matches > best.matches || (candidate.matches == best.matches && candidate.distance < best.distance) {
			best = candidate
		}
	}
	result.MatchedFrames = best.matches
	result.Coverage = float64(best.matches) / float64(min(usefulA, usefulB))
	if best.matches > 0 {
		result.MeanDistance = float64(best.distance) / float64(best.matches)
		result.MaximumDistance = best.maximum
	}
	result.Related = result.Coverage >= duplicateMinCoverage
	return result, nil
}

// VisualFingerprintComparable reports whether a visual sequence contains the
// minimum number of informative frames needed to establish a relationship.
func VisualFingerprintComparable(value []uint64) bool {
	return informativeCount(value) >= duplicateMinFrames
}

func informativeCount(in []uint64) int {
	count := 0
	for _, hash := range in {
		if informativeHash(hash) {
			count++
		}
	}
	return count
}

func informativeHash(hash uint64) bool {
	ones := bits.OnesCount64(hash)
	return ones >= 4 && ones <= 60
}
