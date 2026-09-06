package filler

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

const legacySplitObservationProducer = "v34-split-shadow-v1"

// assessCurrentSplitStructure projects the V34 detector's durable facts into the V67 contract.
// It deliberately supplies no role claims, so this shadow assessment cannot authorize publication.
func assessCurrentSplitStructure(source SplitSourceAsset, progress SplitDetectionProgress, segments []SplitSegment, assessedAt time.Time) (SourceStructureAssessment, error) {
	observations := splitStructureObservations(source, progress, segments)
	discards, err := splitStructureDiscards(progress.Discarded, observations)
	if err != nil {
		return SourceStructureAssessment{}, err
	}
	return AssessSourceStructure(SourceStructureInput{
		Source: source, Observations: observations, DiscardClaims: discards, AssessedAt: assessedAt,
	})
}

func chapterEdges(chapters []Chapter) []int64 {
	var edges []int64
	for _, chapter := range chapters {
		edges = append(edges, chapter.StartMs, chapter.EndMs)
	}
	slices.Sort(edges)
	return slices.Compact(edges)
}

func splitStructureObservations(source SplitSourceAsset, progress SplitDetectionProgress, segments []SplitSegment) []StructureObservation {
	var observations []StructureObservation
	appendObservation := func(kind StructureObservationKind, effect StructureObservationEffect, startMs, endMs int64, evidence string) {
		if startMs <= 0 || endMs >= source.DurationMs {
			return
		}
		observations = append(observations, StructureObservation{
			Kind: kind, Effect: effect, StartMs: startMs, EndMs: endMs,
			Producer:       legacySplitObservationProducer,
			EvidenceSHA256: structureDerivedEvidenceSHA256(source.SHA256, string(kind), strconv.FormatInt(startMs, 10), strconv.FormatInt(endMs, 10), evidence),
		})
	}
	for _, edge := range progress.ChapterEdges {
		appendObservation(ObservationChapterEdge, ObservationProposesBoundary, edge, edge, "declared-container-edge")
	}
	for _, interval := range progress.Black {
		appendObservation(ObservationBlackInterval, ObservationProposesBoundary, interval.StartMs, interval.EndMs, "ffmpeg-blackdetect")
	}
	for _, interval := range progress.Silence {
		appendObservation(ObservationSilenceInterval, ObservationProposesBoundary, interval.StartMs, interval.EndMs, "ffmpeg-silencedetect")
	}
	seenTranscriptEdges := map[int64]struct{}{}
	for _, segment := range segments {
		for _, edge := range []struct {
			at  int64
			src boundarySource
		}{{segment.StartMs, segment.startSrc}, {segment.EndMs, segment.endSrc}} {
			if edge.src&srcTranscript == 0 || edge.at <= 0 || edge.at >= source.DurationMs {
				continue
			}
			if _, exists := seenTranscriptEdges[edge.at]; exists {
				continue
			}
			seenTranscriptEdges[edge.at] = struct{}{}
			appendObservation(ObservationTranscriptChange, ObservationProposesBoundary, edge.at, edge.at, segment.Transcript)
		}
	}
	sort.Slice(observations, func(i, j int) bool {
		if observations[i].StartMs != observations[j].StartMs {
			return observations[i].StartMs < observations[j].StartMs
		}
		if observations[i].Kind != observations[j].Kind {
			return observations[i].Kind < observations[j].Kind
		}
		return observations[i].EvidenceSHA256 < observations[j].EvidenceSHA256
	})
	for i := range observations {
		observations[i].ID = fmt.Sprintf("obs-%04d", i+1)
	}
	return observations
}

func splitStructureDiscards(discarded []Interval, observations []StructureObservation) ([]StructureDiscardClaim, error) {
	if len(discarded) == 0 {
		return nil, nil
	}
	var claims []StructureDiscardClaim
	for _, interval := range discarded {
		ids := structureEvidenceAtEdges(interval, observations)
		if len(ids) == 0 {
			return nil, fmt.Errorf("discarded interval %d..%d has no retained boundary evidence", interval.StartMs, interval.EndMs)
		}
		claims = append(claims, StructureDiscardClaim{
			StartMs: interval.StartMs, EndMs: interval.EndMs,
			Reason: DiscardBelowClipFloor, EvidenceIDs: ids,
		})
	}
	return claims, nil
}

func structureEvidenceAtEdges(interval Interval, observations []StructureObservation) []string {
	var ids []string
	for _, observation := range observations {
		midpoint := (observation.StartMs + observation.EndMs) / 2
		if midpoint == interval.StartMs || midpoint == interval.EndMs || observation.StartMs <= interval.StartMs && interval.StartMs <= observation.EndMs || observation.StartMs <= interval.EndMs && interval.EndMs <= observation.EndMs {
			ids = append(ids, observation.ID)
		}
	}
	slices.Sort(ids)
	return slices.Compact(ids)
}

func structureDerivedEvidenceSHA256(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}
