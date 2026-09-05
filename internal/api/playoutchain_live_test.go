//go:build ffmpeg

package api_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/api"
	"github.com/loomarr/loomarr/internal/playout"
	"github.com/loomarr/loomarr/internal/schedule"
	"github.com/loomarr/loomarr/internal/store"
	"github.com/loomarr/loomarr/internal/testkit"
)

// This is the complete production transport shape: a real Go block supervisor repeatedly opens
// the authenticated finite-program endpoint, real child encoders end at their Airing boundaries,
// and one real copy mux keeps emitting. It guards the boundary handoff independently of browser HLS.
func TestLiveChain_BlockSupervisorAdvancesThroughPrograms(t *testing.T) {
	bin, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("no ffmpeg")
	}

	st := openTestStore(t, t.TempDir()+"/chain.db")
	t.Cleanup(func() { _ = st.Close() })
	var requests atomic.Int64
	profile := playout.DefaultProfile()
	profile.Width, profile.Height = 320, 180

	srcFile := buildLiveSourceClip(t, bin)

	opts := api.Options{
		Store: st, Auth: api.NewTokenAuthorizer(adminToken), Log: slog.New(slog.DiscardHandler),
		PlayoutSecret: func() string { return playoutToken }, Playout: &testkit.Playout{},
		PlayoutResolver: &chainResolver{profile: profile, requests: &requests, src: srcFile},
		PlayoutEncoder: func(ctx context.Context, args []string, progress func(playout.Progress)) (*playout.Process, error) {
			return playout.Start(ctx, bin, args, nil, progress)
		},
	}
	srv := httptest.NewServer(api.Router(slog.New(slog.DiscardHandler), opts))
	t.Cleanup(srv.Close)

	ch := store.Channel{Channel: schedule.Channel{ID: "ch1", Name: "Chain", Number: 1}}
	ch.Policy.Playout = &schedule.PlayoutPolicy{Backend: "internal"}
	if _, err := st.SaveChannel(context.Background(), ch); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	channel, err := playout.BlockSpawner(bin, liveHTTPBlockSource(srv), nil)(ctx, "ch1", playout.PlanBaseline)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	outPath := t.TempDir() + "/channel.ts"
	out, err := os.Create(outPath)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	copyDone := make(chan error, 1)
	go func() {
		_, copyErr := io.Copy(out, channel.Stdout)
		copyDone <- copyErr
	}()

	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for requests.Load() < 5 && ctx.Err() == nil {
		<-ticker.C
	}
	cancel()
	_ = channel.Wait()
	if err := <-copyDone; err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	if got := requests.Load(); got < 5 {
		t.Fatalf("program requests = %d, want programme/commercial/card/return to finish", got)
	}
	if info, err := os.Stat(outPath); err != nil || info.Size() == 0 {
		t.Fatalf("continuous mux produced no MPEG-TS: info=%v err=%v", info, err)
	}
}

// A native HEVC session pins one decoder format for its lifetime. If the preferred hardware HEVC
// child starts but emits no bytes, every fallback that does produce a block must still match that
// advertised HEVC format; returning H.264 under an HEVC header creates a black frame at the fMP4
// decoder and makes the next genuinely-HEVC block an in-stream codec switch.
func TestLiveProgram_HEVCNoOutputFallbackMatchesBroadcastFormat(t *testing.T) {
	bin, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("no ffmpeg")
	}
	probe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("no ffprobe")
	}

	srcFile := buildLiveSourceClip(t, bin)

	profile := playout.DefaultProfile()
	profile.Width, profile.Height = 320, 180
	profile.Encoder = playout.EncoderVideoToolbox
	var attempts []playout.Encoder
	srv := newProgramServer(t, programOpts{
		resolver: &fakeResolver{
			airing: playableAiring(0, 2*time.Second), url: srcFile,
			profile: profile, channelCodec: "hevc",
		},
		encoder: func(ctx context.Context, args []string, progress func(playout.Progress)) (*playout.Process, error) {
			enc := playout.Encoder("")
			for _, candidate := range []playout.Encoder{
				playout.EncoderVTHEVC, playout.EncoderSoftwareHEVC,
				playout.EncoderVideoToolbox, playout.EncoderSoftware,
			} {
				if slices.Contains(args, string(candidate)) {
					enc = candidate
					break
				}
			}
			attempts = append(attempts, enc)
			if enc == playout.EncoderVTHEVC {
				// A real FFmpeg child that starts successfully and deterministically closes stdout
				// without transport bytes, reproducing the silent hardware-output failure.
				return playout.Start(ctx, bin, []string{
					"-hide_banner", "-loglevel", "error",
					"-f", "lavfi", "-i", "testsrc=size=16x16:rate=1:duration=0.01",
					"-t", "0", "-f", "null", "pipe:1",
				}, nil, progress)
			}
			return playout.Start(ctx, bin, args, nil, progress)
		},
		reclaimVRAM: func(context.Context) {},
	})

	resp := getPlayout(t, srv, "/v1/playout/program/ch1?token="+playoutToken+"&plan=full")
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || len(body) == 0 {
		t.Fatalf("HEVC fallback produced no transport: status=%d bytes=%d attempts=%v", resp.StatusCode, len(body), attempts)
	}
	format, ok := playout.ParseBroadcastFormat(resp.Header.Get(api.PlayoutBroadcastFormatHeader))
	if !ok {
		t.Fatalf("invalid broadcast format header %q", resp.Header.Get(api.PlayoutBroadcastFormatHeader))
	}

	outPath := t.TempDir() + "/fallback.ts"
	if err := os.WriteFile(outPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(probe, "-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=codec_name", "-of", "default=nw=1:nk=1", outPath).CombinedOutput()
	if err != nil {
		t.Fatalf("probe fallback transport: %v\n%s", err, output)
	}
	actualCodec := strings.Fields(string(output))[0]
	if actualCodec != format.VideoCodec {
		t.Fatalf("fallback transport codec=%q but advertised broadcast codec=%q; attempts=%v",
			actualCodec, format.VideoCodec, attempts)
	}
}

// The program-level test above proves the silent HEVC attempt. This route-level companion proves
// the final recovery contract with real media bytes: a no-output FFmpeg presentation is released,
// the baseline presentation is selected, and the raw response contains probeable H.264 + AAC.
func TestLiveRaw_NoOutputFullPlanFallsBackToPlayableBaseline(t *testing.T) {
	bin, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("no ffmpeg")
	}
	probe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("no ffprobe")
	}

	// Start a real FFmpeg child whose output contract is intentionally empty, matching the silent
	// preferred-encoder failure used above rather than substituting a pre-closed fake channel.
	silent, err := exec.Command(bin, "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc=size=16x16:rate=1:duration=0.01",
		"-t", "0", "-f", "mpegts", "pipe:1").Output()
	if err != nil {
		t.Fatalf("run silent FFmpeg presentation: %v", err)
	}
	if len(silent) != 0 {
		t.Fatalf("silent FFmpeg presentation produced %d bytes", len(silent))
	}

	baseline, err := exec.Command(bin, "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc=size=320x180:rate=25:duration=1",
		"-f", "lavfi", "-i", "anullsrc=channel_layout=stereo:sample_rate=48000",
		"-shortest", "-c:v", "libx264", "-preset", "ultrafast", "-c:a", "aac",
		"-f", "mpegts", "pipe:1").Output()
	if err != nil {
		t.Fatalf("build baseline transport: %v", err)
	}

	fullStream := make(chan []byte)
	close(fullStream)
	baselineStream := make(chan []byte, 1)
	baselineStream <- baseline
	close(baselineStream)
	sessions := &fakePlayoutSessions{streams: map[playout.EncodePlan]chan []byte{
		playout.PlanFull: fullStream, playout.PlanBaseline: baselineStream,
	}}
	srv, st := newPlayoutServer(t, playoutOpts{sessions: sessions})
	seedChannel(t, st, "ch1", "Channel One", 1, "internal")

	resp := getPlayout(t, srv, "/v1/playout/stream/ch1?token="+playoutToken)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || len(body) == 0 {
		t.Fatalf("raw fallback status=%d bytes=%d", resp.StatusCode, len(body))
	}

	outPath := t.TempDir() + "/raw-fallback.ts"
	if err := os.WriteFile(outPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	streams, err := exec.Command(probe, "-v", "error", "-show_entries",
		"stream=codec_type,codec_name", "-of", "csv=p=0", outPath).CombinedOutput()
	if err != nil {
		t.Fatalf("probe raw fallback transport: %v\n%s", err, streams)
	}
	got := string(streams)
	if !strings.Contains(got, "h264") || !strings.Contains(got, "aac") {
		t.Fatalf("raw fallback streams = %q, want H.264 video and AAC audio", got)
	}
	if got := sessions.attachments(); len(got) != 2 ||
		got[0].target != playout.PlanFull || got[1].target != playout.PlanBaseline {
		t.Fatalf("tunes = %+v, want full then baseline", got)
	}
}

func buildLiveSourceClip(t *testing.T, bin string) string {
	t.Helper()
	srcFile := t.TempDir() + "/src.mp4"
	if output, err := exec.Command(bin, "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc=size=320x180:rate=25:duration=2",
		"-f", "lavfi", "-i", "anullsrc=channel_layout=stereo:sample_rate=48000",
		"-shortest", "-c:v", "libx264", "-preset", "ultrafast", "-c:a", "aac",
		srcFile).CombinedOutput(); err != nil {
		t.Fatalf("build source clip: %v\n%s", err, output)
	}
	return srcFile
}

func liveHTTPBlockSource(srv *httptest.Server) playout.BlockSource {
	var broadcast string
	return func(ctx context.Context, channel string, plan playout.EncodePlan) (playout.Block, error) {
		query := url.Values{
			"token": []string{playoutToken},
			"plan":  []string{plan.String()},
		}
		if broadcast != "" {
			query.Set(api.PlayoutBroadcastFormatQuery, broadcast)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet,
			srv.URL+"/v1/playout/program/"+url.PathEscape(channel)+"?"+query.Encode(), nil)
		if err != nil {
			return playout.Block{}, err
		}
		resp, err := srv.Client().Do(req)
		if err != nil {
			return playout.Block{}, err
		}
		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			return playout.Block{}, fmt.Errorf("program status %s", resp.Status)
		}
		format, ok := playout.ParseBroadcastFormat(resp.Header.Get(api.PlayoutBroadcastFormatHeader))
		if !ok {
			_ = resp.Body.Close()
			return playout.Block{}, fmt.Errorf("invalid broadcast format")
		}
		if broadcast == "" {
			broadcast = format.String()
		}
		identity, ok := api.ParsePlayoutAiringIdentity(resp.Header)
		if !ok {
			_ = resp.Body.Close()
			return playout.Block{}, fmt.Errorf("invalid airing identity")
		}
		return playout.Block{Content: resp.Body, Identity: identity}, nil
	}
}

type chainResolver struct {
	profile  playout.Profile
	requests *atomic.Int64
	src      string
}

func (c *chainResolver) AiringNow(context.Context, string) (playout.Airing, string, error) {
	n := c.requests.Add(1)
	airing := playout.Airing{
		StartedAt: time.Unix(n, 0).UTC(), Identity: fmt.Sprintf("block-%d", n),
		Kind: schedule.SlotProgram, LibraryItemID: "local", Title: "Short",
		Remaining: 2 * time.Second,
	}
	switch n % 4 {
	case 2:
		airing.Kind, airing.LibraryItemID = schedule.SlotFiller, ""
		airing.Source, airing.Title = c.src, "Commercial"
	case 3:
		airing.Kind, airing.LibraryItemID = schedule.SlotFiller, ""
		airing.Title = "Filler card"
		return airing, "", nil
	}
	return airing, c.src, nil
}

func (c *chainResolver) Profile(context.Context) playout.Profile                   { return c.profile }
func (c *chainResolver) AudioTrackFor(context.Context, string, string, string) int { return 0 }
func (c *chainResolver) Tracks(context.Context, string) (playout.MediaTracks, error) {
	return playout.MediaTracks{}, nil
}
func (c *chainResolver) PlanFor(context.Context, string, playout.EncodePlan) (playout.CopyPlan, playout.MediaFormat) {
	return playout.CopyPlan{}, playout.MediaFormat{}
}
func (c *chainResolver) ChannelCodec(context.Context, string) string { return "h264" }
