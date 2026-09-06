package fillersafetyruntime

import (
	"context"
	"fmt"
	"reflect"

	"github.com/loomarr/loomarr/internal/filler"
	"github.com/loomarr/loomarr/internal/fillersafety"
	"github.com/loomarr/loomarr/internal/mediatools"
)

type sourceInspection struct {
	Probe   filler.Probed
	FFmpeg  mediatools.MediaToolIdentity
	FFprobe mediatools.MediaToolIdentity
}

type sourceInspector interface {
	Inspect(context.Context, string, string, mediatools.MediaToolIdentity) (sourceInspection, error)
}

type realSourceInspector struct{}

func (realSourceInspector) Inspect(ctx context.Context, path, ffmpegPath string, expected mediatools.MediaToolIdentity) (sourceInspection, error) {
	ffmpeg, err := mediatools.IdentifyFFmpeg(ctx, ffmpegPath)
	if err != nil || !reflect.DeepEqual(ffmpeg, expected) {
		return sourceInspection{}, fmt.Errorf("spoken-safety evidence tool identity drifted")
	}
	ffprobe, err := mediatools.IdentifyFFprobe(ctx, ffmpegPath)
	if err != nil {
		return sourceInspection{}, fmt.Errorf("spoken-safety probe tool is unavailable")
	}
	probe, err := filler.FFprobeNextTo(ffmpegPath)(ctx, path)
	if err != nil {
		return sourceInspection{}, fmt.Errorf("spoken-safety evidence cannot be measured")
	}
	return sourceInspection{Probe: probe, FFmpeg: ffmpeg, FFprobe: ffprobe}, nil
}

func sourceToolIdentity(identity mediatools.MediaToolIdentity, name string) (fillersafety.ToolIdentity, error) {
	if err := identity.ValidateName(name); err != nil {
		return fillersafety.ToolIdentity{}, err
	}
	return fillersafety.ToolIdentity{Version: identity.Version, BinarySHA256: identity.ExecutableSHA256}, nil
}
