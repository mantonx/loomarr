package fillerquarantine

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/loomarr/loomarr/internal/fillercorpus"
	"github.com/loomarr/loomarr/internal/fillerreference"
	"github.com/loomarr/loomarr/internal/fillerreview"
	"github.com/loomarr/loomarr/internal/mediatools"
)

func inspectCandidates(ctx context.Context, config Config, inventory fillercorpus.Inventory, ledger fillercorpus.DownloadLedger) ([]Case, map[string]fingerprint, error) {
	inventoryByID := make(map[string]fillercorpus.InventoryCase, len(inventory.Cases))
	for _, item := range inventory.Cases {
		inventoryByID[item.CaseID] = item
	}
	cases := make([]Case, 0, len(ledger.Cases))
	fingerprints := make(map[string]fingerprint, len(ledger.Cases))
	for _, item := range ledger.Cases {
		path, err := resolveBeneath(config.DownloadRoot, item.LocalFile)
		if err != nil {
			return nil, nil, fmt.Errorf("case %q source path: %w", item.CaseID, err)
		}
		hashes, bytes, err := hashFile(ctx, path)
		if err != nil {
			return nil, nil, fmt.Errorf("case %q source identity: %w", item.CaseID, err)
		}
		if hashes.sha256 != item.ContentSHA256 || bytes != item.Representation.Bytes ||
			(item.Representation.SHA256 != "" && hashes.sha256 != item.Representation.SHA256) ||
			(item.Representation.SHA1 != "" && hashes.sha1 != item.Representation.SHA1) ||
			(item.Representation.MD5 != "" && hashes.md5 != item.Representation.MD5) {
			return nil, nil, fmt.Errorf("case %q source bytes do not match the download ledger", item.CaseID)
		}
		probed, err := config.Media.Probe(ctx, path)
		if err != nil {
			return nil, nil, fmt.Errorf("case %q probe: %w", item.CaseID, err)
		}
		usableVideo := !probed.NoVideo && probed.Width > 0 && probed.Height > 0
		if !usableVideo {
			fp := fingerprint{}
			fingerprints[item.CaseID] = fp
			candidate := inventoryByID[item.CaseID]
			cases = append(cases, Case{
				CaseID: item.CaseID, LocalFile: item.LocalFile, ContentSHA256: hashes.sha256, Bytes: bytes,
				ExpectedMedia: mediaExpectation(candidate.Representation),
				Media:         MediaEvidence{DurationMS: probed.DurationMs, Width: probed.Width, Height: probed.Height, HasVideo: false, HasAudio: !probed.Silent},
				HoldReasons:   technicalHoldReasons(candidate.Representation, probed, mediatools.MediaQuality{}, fp),
			})
			continue
		}
		quality, err := config.Media.Quality(ctx, path, probed.DurationMs, !probed.Silent)
		if err != nil {
			return nil, nil, fmt.Errorf("case %q full decode: %w", item.CaseID, err)
		}
		frames, audio, err := config.Media.Fingerprint(ctx, path, probed.DurationMs, !probed.Silent)
		if err != nil {
			return nil, nil, fmt.Errorf("case %q fingerprint: %w", item.CaseID, err)
		}
		fp := fingerprint{frames: frames, audio: audio}
		fingerprints[item.CaseID] = fp
		candidate := inventoryByID[item.CaseID]
		cases = append(cases, Case{
			CaseID: item.CaseID, LocalFile: item.LocalFile, ContentSHA256: hashes.sha256, Bytes: bytes,
			ExpectedMedia: mediaExpectation(candidate.Representation),
			Media:         MediaEvidence{DurationMS: probed.DurationMs, Width: probed.Width, Height: probed.Height, HasVideo: !probed.NoVideo, HasAudio: !probed.Silent, Quality: quality},
			Fingerprint:   fingerprintEvidence(fp), HoldReasons: technicalHoldReasons(candidate.Representation, probed, quality, fp),
		})
	}
	slices.SortFunc(cases, func(a, b Case) int { return strings.Compare(a.CaseID, b.CaseID) })
	return cases, fingerprints, nil
}

func mediaExpectation(value fillercorpus.InventoryRepresentation) MediaExpectation {
	return MediaExpectation{Bytes: value.Bytes, DurationMS: value.DurationMS, Width: value.Width, Height: value.Height}
}

func inspectPriorSources(ctx context.Context, config Config, authority fillerreview.TemporalStructureChallengeAuthority) ([]PriorSource, map[string]fingerprint, int, error) {
	bySHA := make(map[string]PriorSource)
	byID := make(map[string]string)
	for _, item := range authority.Cases {
		for _, segment := range item.Segments {
			source := PriorSource{SourceID: segment.SourceID, SourcePath: segment.SourcePath, SourceSHA256: segment.SourceSHA256, DurationMS: segment.SourceDurationMS}
			if existingSHA, ok := byID[source.SourceID]; ok && existingSHA != source.SourceSHA256 {
				return nil, nil, 0, fmt.Errorf("prior source id %q has conflicting content authority", source.SourceID)
			}
			byID[source.SourceID] = source.SourceSHA256
			if existing, ok := bySHA[source.SourceSHA256]; ok {
				if existing.SourceID != source.SourceID || existing.SourcePath != source.SourcePath || existing.DurationMS != source.DurationMS {
					return nil, nil, 0, fmt.Errorf("prior source %q has conflicting authority", source.SourceSHA256)
				}
				continue
			}
			bySHA[source.SourceSHA256] = source
		}
	}
	prior := make([]PriorSource, 0, len(bySHA))
	fingerprints := make(map[string]fingerprint, len(bySHA))
	unavailable := 0
	for _, source := range bySHA {
		path, err := resolveBeneath(config.PriorSourceRoot, source.SourcePath)
		if err != nil {
			if os.IsNotExist(err) {
				source.Available = false
				unavailable++
				prior = append(prior, source)
				continue
			}
			return nil, nil, 0, fmt.Errorf("prior source %q path: %w", source.SourceID, err)
		}
		hashes, _, err := hashFile(ctx, path)
		if err != nil {
			return nil, nil, 0, fmt.Errorf("prior source %q identity: %w", source.SourceID, err)
		}
		if hashes.sha256 != source.SourceSHA256 {
			return nil, nil, 0, fmt.Errorf("prior source %q content identity mismatch", source.SourceID)
		}
		probed, err := config.Media.Probe(ctx, path)
		if err != nil {
			return nil, nil, 0, fmt.Errorf("prior source %q probe: %w", source.SourceID, err)
		}
		if absolute(probed.DurationMs-source.DurationMS) > 1_000 || probed.NoVideo {
			return nil, nil, 0, fmt.Errorf("prior source %q media does not match its authority", source.SourceID)
		}
		frames, audio, err := config.Media.Fingerprint(ctx, path, source.DurationMS, !probed.Silent)
		if err != nil {
			return nil, nil, 0, fmt.Errorf("prior source %q fingerprint: %w", source.SourceID, err)
		}
		fp := fingerprint{frames: frames, audio: audio}
		fingerprints[source.SourceSHA256] = fp
		source.Available = true
		source.Fingerprint = fingerprintEvidence(fp)
		prior = append(prior, source)
	}
	slices.SortFunc(prior, func(a, b PriorSource) int { return strings.Compare(a.SourceSHA256, b.SourceSHA256) })
	return prior, fingerprints, unavailable, nil
}

func technicalHoldReasons(expected fillercorpus.InventoryRepresentation, probed mediatools.Probed, quality mediatools.MediaQuality, fp fingerprint) []string {
	var reasons []string
	if probed.NoVideo || probed.Width <= 0 || probed.Height <= 0 {
		reasons = append(reasons, "missing_video")
	}
	if probed.Silent {
		reasons = append(reasons, "missing_audio")
	}
	if expected.DurationMS > 0 && absolute(probed.DurationMs-expected.DurationMS) > 1_000 {
		reasons = append(reasons, "duration_mismatch")
	}
	if expected.Width > 0 && probed.Width != expected.Width || expected.Height > 0 && probed.Height != expected.Height {
		reasons = append(reasons, "dimension_mismatch")
	}
	if intervalCoverage(quality.Black, quality.DurationMs) >= qualityFailureCoverage {
		reasons = append(reasons, "mostly_black")
	}
	if intervalCoverage(quality.Freeze, quality.DurationMs) >= qualityFailureCoverage {
		reasons = append(reasons, "mostly_frozen")
	}
	if !probed.Silent && intervalCoverage(quality.Silence, quality.DurationMs) >= qualityFailureCoverage {
		reasons = append(reasons, "mostly_silent")
	}
	visual := fillerreference.VisualFingerprintComparable(fp.frames)
	audio := fillerreference.AudioFingerprintComparable(fp.audio)
	if !visual && !audio {
		reasons = append(reasons, "fingerprint_unusable")
	}
	return reasons
}

func intervalCoverage(intervals []mediatools.Interval, duration int64) float64 {
	if duration <= 0 {
		return 0
	}
	var covered int64
	for _, interval := range intervals {
		covered += interval.EndMs - interval.StartMs
	}
	return float64(covered) / float64(duration)
}

func absolute(value int64) int64 {
	if value < 0 {
		return -value
	}
	return value
}
