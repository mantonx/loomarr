//go:build ffmpeg

package playout

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/prepared"
	"github.com/loomarr/loomarr/internal/schedule"
)

type wallClockPreparedResolver struct {
	boundary time.Time
	first    PreparedAiring
	second   PreparedAiring
}

func (r wallClockPreparedResolver) ResolvePrepared(context.Context, TuneRequest) (PreparedWindow, bool, error) {
	now := time.Now().UTC()
	airing := r.first
	if !now.Before(r.boundary) {
		airing = r.second
	}
	airing.Offset = now.Sub(airing.StartedAt)
	return PreparedWindow{Current: airing}, true, nil
}

func TestLive_PreparedMPEGTSBlockProducesTransportUnderOneSecond(t *testing.T) {
	bin := ffmpegBin(t)
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "source.mp4")
	generate := []string{
		"-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=duration=24:size=320x180:rate=25",
		"-f", "lavfi", "-i", "anullsrc=channel_layout=stereo:sample_rate=48000",
		"-map", "0:v:0", "-map", "1:a:0", "-shortest", "-t", "24",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
		"-g", "50", "-sc_threshold", "0", "-c:a", "aac", "-ar", "48000", "-ac", "2",
		sourcePath,
	}
	if output, err := exec.Command(bin, generate...).CombinedOutput(); err != nil {
		t.Fatalf("generate source: %v\n%s", err, output)
	}

	lib, err := prepared.NewLibrary(filepath.Join(dir, "prepared"))
	if err != nil {
		t.Fatal(err)
	}
	spec := prepared.Specification{
		SourceFingerprint: "live-prepared-source",
		Rendition: prepared.RenditionContract{
			VideoCodec: "h264", VideoProfile: "high", VideoLevel: "4.1", PixelFormat: "yuv420p",
			HDR: "sdr", AudioCodec: "aac", AudioLayout: "stereo", Width: 320, Height: 180,
			FrameRate: 25, VideoBitrateKbps: 800, AudioBitrateKbps: 96,
			SegmentDurationMS: 2000, PackagingVersion: prepared.CurrentPackagingVersion,
		},
	}
	packager := prepared.NewFFmpegPackager(bin)
	if _, err := lib.Publish(t.Context(), spec, func(ctx context.Context, workspace string) (prepared.Output, error) {
		return packager.Package(ctx, workspace, prepared.LocalInput(sourcePath), 0, spec.Rendition)
	}); err != nil {
		t.Fatal(err)
	}

	const hostileOffset = 12*time.Second + 250*time.Millisecond
	startedAt := time.Now().UTC().Add(-hostileOffset)
	origin := newPreparedOrigin(lib, fixedPreparedResolver{ok: true, window: PreparedWindow{
		Current: PreparedAiring{
			Specification: spec, StartedAt: startedAt, Offset: hostileOffset,
			Identity: AiringIdentity{
				StartedAt: startedAt, EndsAt: startedAt.Add(24 * time.Second), Kind: schedule.SlotProgram,
				ContentID: "live-source", ScheduleBlockID: "block-live-source",
			},
		},
	}})

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	begin := time.Now()
	block, err := origin.MPEGTSBlockSource(bin, nil, nil)(ctx, "channel", PlanFull)
	if err != nil {
		t.Fatal(err)
	}
	packet := make([]byte, 188)
	n, err := block.Content.Read(packet)
	firstByte := time.Since(begin)
	if err != nil || n == 0 {
		t.Fatalf("first transport read = %d bytes, err=%v", n, err)
	}
	if packet[0] != 0x47 {
		t.Fatalf("first byte = %#x, want MPEG-TS sync byte 0x47", packet[0])
	}
	if firstByte >= time.Second {
		t.Fatalf("prepared first transport byte = %s, want under 1s", firstByte)
	}
	_ = block.Content.Close()
	t.Logf("prepared first transport byte: %s", firstByte)

	decodeCtx, decodeCancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer decodeCancel()
	decodeBegin := time.Now()
	channel, err := BlockSpawner(bin, origin.MPEGTSBlockSource(bin, nil, nil), nil)(
		decodeCtx, "channel", PlanFull,
	)
	if err != nil {
		t.Fatal(err)
	}
	decoder := exec.CommandContext(decodeCtx, bin,
		"-hide_banner", "-loglevel", "error", "-probesize", "256k", "-analyzeduration", "500000",
		"-f", "mpegts", "-i", "pipe:0", "-map", "0:v:0", "-frames:v", "1", "-f", "null", "-",
	)
	decoder.Stdin = channel.Stdout
	if output, err := decoder.CombinedOutput(); err != nil {
		channel.Stop()
		t.Fatalf("decode first frame: %v\n%s", err, output)
	}
	firstFrame := time.Since(decodeBegin)
	channel.Stop()
	if firstFrame >= time.Second {
		t.Fatalf("prepared first decoded frame = %s, want under 1s", firstFrame)
	}
	t.Logf("prepared first decoded frame: %s", firstFrame)
}

func TestLive_PreparedMPEGTSRolloverIsContinuousAndDoesNotReplayOutgoingAiring(t *testing.T) {
	bin := ffmpegBin(t)
	dir := t.TempDir()
	lib, err := prepared.NewLibrary(filepath.Join(dir, "prepared"))
	if err != nil {
		t.Fatal(err)
	}
	rendition := prepared.RenditionContract{
		VideoCodec: "h264", VideoProfile: "high", VideoLevel: "4.1", PixelFormat: "yuv420p",
		HDR: "sdr", AudioCodec: "aac", AudioLayout: "stereo", Width: 320, Height: 180,
		FrameRate: 25, VideoBitrateKbps: 800, AudioBitrateKbps: 96,
		SegmentDurationMS: 1000, PackagingVersion: prepared.CurrentPackagingVersion,
	}
	firstSpec := publishPreparedColour(t, bin, lib, dir, "red", rendition)
	secondSpec := publishPreparedColour(t, bin, lib, dir, "blue", rendition)

	boundary := time.Now().UTC().Add(2 * time.Second)
	resolver := wallClockPreparedResolver{
		boundary: boundary,
		first: PreparedAiring{Specification: firstSpec, StartedAt: boundary.Add(-4 * time.Second), Identity: AiringIdentity{
			StartedAt: boundary.Add(-4 * time.Second), EndsAt: boundary, Kind: schedule.SlotProgram,
			ContentID: "red-airing", ScheduleBlockID: "red-block",
		}},
		second: PreparedAiring{Specification: secondSpec, StartedAt: boundary, Identity: AiringIdentity{
			StartedAt: boundary, EndsAt: boundary.Add(6 * time.Second), Kind: schedule.SlotProgram,
			ContentID: "blue-airing", ScheduleBlockID: "blue-block",
		}},
	}
	origin := newPreparedOrigin(lib, resolver)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	channel, err := BlockSpawner(bin, origin.MPEGTSBlockSource(bin, nil, nil), nil)(ctx, "channel", PlanFull)
	if err != nil {
		t.Fatal(err)
	}
	decoder := exec.CommandContext(ctx, bin,
		"-hide_banner", "-loglevel", "error", "-probesize", "256k", "-analyzeduration", "500000",
		"-f", "mpegts", "-i", "pipe:0", "-t", "4", "-vf", "fps=5,scale=1:1:flags=neighbor,format=rgb24",
		"-f", "rawvideo", "pipe:1",
	)
	decoder.Stdin = channel.Stdout
	pixels, err := decoder.Output()
	channel.Stop()
	if err != nil {
		t.Fatalf("decode prepared rollover: %v", err)
	}

	last, transitions, redFrames, blueFrames := "", 0, 0, 0
	for index := 0; index+2 < len(pixels); index += 3 {
		r, _, b := pixels[index], pixels[index+1], pixels[index+2]
		colour := ""
		switch {
		case r > 160 && b < 80:
			colour, redFrames = "red", redFrames+1
		case b > 160 && r < 80:
			colour, blueFrames = "blue", blueFrames+1
		default:
			t.Fatalf("unexpected prepared rollover frame rgb(%d,%d,%d)", r, pixels[index+1], b)
		}
		if last != "" && colour != last {
			transitions++
		}
		if last == "blue" && colour == "red" {
			t.Fatal("outgoing red prepared Airing replayed after blue rollover")
		}
		last = colour
	}
	if redFrames == 0 || blueFrames == 0 || transitions != 1 {
		t.Fatalf("prepared rollover frames red=%d blue=%d transitions=%d, want both Airings and one handoff",
			redFrames, blueFrames, transitions)
	}
}

func publishPreparedColour(
	t *testing.T, bin string, lib *prepared.Library, dir, colour string, rendition prepared.RenditionContract,
) prepared.Specification {
	t.Helper()
	source := filepath.Join(dir, colour+".mp4")
	args := []string{
		"-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", fmt.Sprintf("color=c=%s:s=320x180:r=25:d=6", colour),
		"-f", "lavfi", "-i", "anullsrc=channel_layout=stereo:sample_rate=48000",
		"-map", "0:v:0", "-map", "1:a:0", "-shortest", "-t", "6",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
		"-g", "25", "-sc_threshold", "0", "-c:a", "aac", "-ar", "48000", "-ac", "2", source,
	}
	if output, err := exec.Command(bin, args...).CombinedOutput(); err != nil {
		t.Fatalf("generate %s prepared source: %v\n%s", colour, err, output)
	}
	spec := prepared.Specification{SourceFingerprint: "rollover-" + colour, Rendition: rendition}
	packager := prepared.NewFFmpegPackager(bin)
	if _, err := lib.Publish(t.Context(), spec, func(ctx context.Context, workspace string) (prepared.Output, error) {
		return packager.Package(ctx, workspace, prepared.LocalInput(source), 0, rendition)
	}); err != nil {
		t.Fatalf("publish %s prepared source: %v", colour, err)
	}
	return spec
}
