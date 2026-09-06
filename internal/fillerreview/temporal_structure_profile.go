package fillerreview

import (
	"fmt"
	"math/big"
)

func validateTemporalStructureVideoProfile(info TemporalTruthVideoInfo) error {
	profile := info.Profile
	rate, validRate := new(big.Rat).SetString(profile.FrameRate)
	if info.Width != 960 || info.Height != 720 || !info.HasAudio || profile.VideoCodec != "h264" || profile.PixelFormat != "yuv420p" || !validRate || rate.Cmp(big.NewRat(30, 1)) != 0 || profile.AudioStreams != 1 || profile.AudioCodec != "aac" || profile.SampleRate != 48_000 || profile.Channels != 2 {
		return fmt.Errorf("rendered structure video does not match the required measured profile")
	}
	return nil
}
