package fillerstructurewindow

import (
	"fmt"

	"github.com/loomarr/loomarr/internal/fillerstructure"
)

// ParseDirectVideoResponse strictly decodes window-local provider output and projects its complete
// timeline onto the planned source-relative media interval. Planned geometry, not encoder drift,
// remains the coverage authority.
func ParseDirectVideoResponse(set MediaSet, ordinal int, raw string) ([]fillerstructure.Segment, error) {
	if err := ValidateMediaSet(set); err != nil {
		return nil, err
	}
	if ordinal < 0 || ordinal >= len(set.Plan.Windows) {
		return nil, fmt.Errorf("structure window direct-video ordinal is invalid")
	}
	window := set.Plan.Windows[ordinal]
	durationMS := window.MediaEndMS - window.MediaStartMS
	_, local, err := fillerstructure.ParseDirectVideoResponse(raw, durationMS)
	if err != nil {
		return nil, err
	}
	segments := make([]fillerstructure.Segment, 0, len(local.Segments))
	for _, segment := range local.Segments {
		segments = append(segments, fillerstructure.Segment{
			StartMS: window.MediaStartMS + segment.StartMS,
			EndMS:   window.MediaStartMS + segment.EndMS,
			Role:    segment.Role,
		})
	}
	return segments, nil
}
