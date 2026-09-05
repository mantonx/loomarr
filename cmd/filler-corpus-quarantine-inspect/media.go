package main

import (
	"context"

	"github.com/loomarr/loomarr/internal/filler"
	"github.com/loomarr/loomarr/internal/fillerreference"
	"github.com/loomarr/loomarr/internal/fillerreview"
	"github.com/loomarr/loomarr/internal/mediatools"
)

type execMedia struct {
	ffmpeg   string
	probe    mediatools.Prober
	identity fillerreview.TemporalTruthMediaIdentity
}

func newExecMedia(ctx context.Context, ffmpegPath string) (*execMedia, error) {
	ffprobePath := filler.FFprobePathNextTo(ffmpegPath)
	identity, err := fillerreview.NewFFmpegTemporalTruthMedia(ctx, ffmpegPath, ffprobePath)
	if err != nil {
		return nil, err
	}
	return &execMedia{ffmpeg: identity.Identity().FFmpeg.Path, probe: filler.FFprobeNextTo(identity.Identity().FFmpeg.Path), identity: identity.Identity()}, nil
}

func (media *execMedia) Identity() fillerreview.TemporalTruthMediaIdentity { return media.identity }

func (media *execMedia) Probe(ctx context.Context, path string) (mediatools.Probed, error) {
	return media.probe(ctx, path)
}

func (media *execMedia) Quality(ctx context.Context, path string, durationMS int64, hasAudio bool) (mediatools.MediaQuality, error) {
	return mediatools.InspectQuality(ctx, media.ffmpeg, path, durationMS, hasAudio)
}

func (media *execMedia) Fingerprint(ctx context.Context, path string, durationMS int64, hasAudio bool) ([]uint64, []uint32, error) {
	return fillerreference.FingerprintMedia(ctx, media.ffmpeg, path, durationMS, hasAudio)
}
