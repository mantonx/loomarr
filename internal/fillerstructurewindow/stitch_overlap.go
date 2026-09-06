package fillerstructurewindow

import (
	"slices"

	"github.com/loomarr/loomarr/internal/fillerstructure"
)

type windowBoundary struct {
	id            int
	window, index int
	atMS          int64
	before, after fillerstructure.Role
}

type stitchedBoundary struct {
	atMS          int64
	before, after fillerstructure.Role
}

func stitchAcceptedWindows(plan Plan, assessments []Assessment, toleranceMS int64) ([]fillerstructure.Segment, string) {
	boundaries, byWindow := assessmentBoundaries(assessments)
	parents := make([]int, len(boundaries))
	for index := range parents {
		parents[index] = index
	}
	for ordinal := 0; ordinal+1 < len(plan.Windows); ordinal++ {
		left, right := plan.Windows[ordinal], plan.Windows[ordinal+1]
		overlapStart, overlapEnd := right.MediaStartMS, left.MediaEndMS
		leftIDs := boundariesInside(boundaries, byWindow[ordinal], overlapStart, overlapEnd)
		rightIDs := boundariesInside(boundaries, byWindow[ordinal+1], overlapStart, overlapEnd)
		if len(leftIDs) != len(rightIDs) || roleAt(assessments[ordinal].Segments, overlapStart) != roleAt(assessments[ordinal+1].Segments, overlapStart) {
			return nil, HoldOverlapConflict
		}
		for index := range leftIDs {
			leftBoundary, rightBoundary := boundaries[leftIDs[index]], boundaries[rightIDs[index]]
			if absolute(leftBoundary.atMS-rightBoundary.atMS) > toleranceMS ||
				leftBoundary.before != rightBoundary.before || leftBoundary.after != rightBoundary.after {
				return nil, HoldOverlapConflict
			}
			unionBoundary(parents, leftBoundary.id, rightBoundary.id)
		}
	}

	clusters := make(map[int][]windowBoundary)
	for _, boundary := range boundaries {
		root := findBoundary(parents, boundary.id)
		clusters[root] = append(clusters[root], boundary)
	}
	projected := make([]stitchedBoundary, 0, len(clusters))
	for _, observations := range clusters {
		owned := len(observations) > 1
		var total int64
		for _, observation := range observations {
			window := plan.Windows[observation.window]
			owned = owned || observation.atMS >= window.PrimaryStartMS && observation.atMS < window.PrimaryEndMS
			total += observation.atMS
		}
		if !owned {
			continue
		}
		projected = append(projected, stitchedBoundary{
			atMS: total / int64(len(observations)), before: observations[0].before, after: observations[0].after,
		})
	}
	slices.SortFunc(projected, func(left, right stitchedBoundary) int {
		if left.atMS < right.atMS {
			return -1
		}
		if left.atMS > right.atMS {
			return 1
		}
		return 0
	})
	return projectStitchedTimeline(plan.Source.DurationMS, assessments, projected)
}

func assessmentBoundaries(assessments []Assessment) ([]windowBoundary, [][]int) {
	var boundaries []windowBoundary
	byWindow := make([][]int, len(assessments))
	for window, assessment := range assessments {
		for index := 0; index+1 < len(assessment.Segments); index++ {
			boundary := windowBoundary{
				id: len(boundaries), window: window, index: index,
				atMS:   assessment.Segments[index].EndMS,
				before: assessment.Segments[index].Role, after: assessment.Segments[index+1].Role,
			}
			boundaries = append(boundaries, boundary)
			byWindow[window] = append(byWindow[window], boundary.id)
		}
	}
	return boundaries, byWindow
}

func boundariesInside(boundaries []windowBoundary, ids []int, startMS, endMS int64) []int {
	var inside []int
	for _, id := range ids {
		if boundaries[id].atMS > startMS && boundaries[id].atMS < endMS {
			inside = append(inside, id)
		}
	}
	return inside
}

func roleAt(segments []fillerstructure.Segment, atMS int64) fillerstructure.Role {
	for _, segment := range segments {
		if atMS >= segment.StartMS && atMS < segment.EndMS {
			return segment.Role
		}
	}
	return ""
}

func projectStitchedTimeline(durationMS int64, assessments []Assessment, boundaries []stitchedBoundary) ([]fillerstructure.Segment, string) {
	currentRole := roleAt(assessments[0].Segments, 0)
	if !windowRole(currentRole) {
		return nil, HoldTimelineConflict
	}
	startMS := int64(0)
	segments := make([]fillerstructure.Segment, 0, len(boundaries)+1)
	for _, boundary := range boundaries {
		if boundary.atMS <= startMS || boundary.atMS >= durationMS || boundary.before != currentRole {
			return nil, HoldTimelineConflict
		}
		segments = append(segments, fillerstructure.Segment{StartMS: startMS, EndMS: boundary.atMS, Role: currentRole})
		startMS, currentRole = boundary.atMS, boundary.after
	}
	if currentRole != roleAt(assessments[len(assessments)-1].Segments, durationMS-1) {
		return nil, HoldTimelineConflict
	}
	segments = append(segments, fillerstructure.Segment{StartMS: startMS, EndMS: durationMS, Role: currentRole})
	return segments, ""
}

func findBoundary(parents []int, value int) int {
	for parents[value] != value {
		parents[value] = parents[parents[value]]
		value = parents[value]
	}
	return value
}

func unionBoundary(parents []int, left, right int) {
	left, right = findBoundary(parents, left), findBoundary(parents, right)
	if left != right {
		parents[right] = left
	}
}
