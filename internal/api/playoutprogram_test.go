package api_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/api"
	"github.com/loomarr/loomarr/internal/diagnostics"
	"github.com/loomarr/loomarr/internal/playout"
	"github.com/loomarr/loomarr/internal/schedule"
	"github.com/loomarr/loomarr/internal/store"
	"github.com/loomarr/loomarr/internal/testkit"
)

// fakeResolver answers "what's on" without a store or a media server.
type fakeResolver struct {
	airing playout.Airing
	url    string
	err    error
	// audioTrack is the index AudioTrackFor returns — zero (the file's first track) unless a
	// test is specifically about audio selection.
	audioTrack int
	// tracks is what Tracks returns (the Watch pickers' media-derived options) — empty unless a
	// test is specifically about track discovery.
	tracks playout.MediaTracks
	// plan is what PlanFor returns — zero value (transcode both) unless a test is about direct play.
	plan playout.CopyPlan
	// sourceFormat is the probe PlanFor returns alongside the plan. Zero unless a test is about
	// something the probe learned about the SOURCE (as opposed to the copy decision derived from
	// it) — HDR is the first such thing.
	sourceFormat playout.MediaFormat
	profile      playout.Profile
	// channelCodec is the persisted codec of the live Channel. Empty keeps the historical h264
	// default used by the program-path tests; HEVC fallback tests set it explicitly.
	channelCodec string
	calls        int
	mu           sync.Mutex
	// airingEntered/airingRelease form a deterministic barrier for lifecycle races. When both
	// are set, AiringNow announces that resolution began and waits until either the request is
	// cancelled or the test releases it.
	airingEntered chan struct{}
	airingRelease <-chan struct{}
}

func (f *fakeResolver) AiringNow(ctx context.Context, _ string) (playout.Airing, string, error) {
	f.mu.Lock()
	f.calls++
	entered, release := f.airingEntered, f.airingRelease
	f.mu.Unlock()
	if entered != nil && release != nil {
		close(entered)
		select {
		case <-ctx.Done():
			return playout.Airing{}, "", ctx.Err()
		case <-release:
		}
	}
	return f.airing, f.url, f.err
}

func (f *fakeResolver) Profile(context.Context) playout.Profile {
	if f.profile.Width == 0 {
		return playout.DefaultProfile()
	}
	return f.profile
}

// AudioTrackFor returns whatever the test set, defaulting to the file's first track — the same
// answer the real resolver gives when no language preference is configured.
func (f *fakeResolver) AudioTrackFor(context.Context, string, string, string) int {
	return f.audioTrack
}

// Tracks returns whatever the test set (empty by default) — the Watch pickers' media-derived
// options. These tests exercise the program/stream path, not the pickers, so empty is the right
// default.
func (f *fakeResolver) Tracks(context.Context, string) (playout.MediaTracks, error) {
	return f.tracks, nil
}

// PlanFor returns whatever plan the test set — zero value (transcode both) by default, which is
// what the existing program-path assertions expect (they check the transcode args) — plus the
// source format it was derived from. `sourceFormat` is zero unless a test is about something the
// probe learned (HDR is the first), which reads as "not probed": SDR, unknown geometry.
func (f *fakeResolver) PlanFor(context.Context, string, playout.EncodePlan) (playout.CopyPlan, playout.MediaFormat) {
	return f.plan, f.sourceFormat
}

func (f *fakeResolver) ChannelCodec(context.Context, string) string {
	if f.channelCodec == "" {
		return "h264"
	}
	return f.channelCodec
}

func (f *fakeResolver) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// fakeEncoder captures the args it was asked to run and serves canned bytes as the encoder's
// output, so the handler can be exercised without executing ffmpeg.
type fakeEncoder struct {
	mu      sync.Mutex
	gotArgs []string
	spec    diagnostics.ProcessSpec
	output  string
	failErr error
	// progressScript emits ffmpeg-shaped progress on fd 3 (`>&3`) so the parser + the V16
	// reporting path can be tested end to end with `sh` standing in for ffmpeg.
	progressScript string
}

func (f *fakeEncoder) start(
	ctx context.Context, args []string, onProgress func(playout.Progress),
) (*playout.Process, error) {
	f.mu.Lock()
	f.gotArgs = args
	f.spec, _ = diagnostics.ProcessSpecFromContext(ctx)
	f.mu.Unlock()
	if f.failErr != nil {
		return nil, f.failErr
	}
	// The REAL playout.Start, driven with `sh` instead of ffmpeg. That keeps the process
	// supervision under test (stdout piping, the context binding, Wait) while producing
	// deterministic bytes — and it needs no test-only export in the production package.
	//
	// printf then EXIT: a child's exit is the behaviour under test, since that EOF is what
	// advances the channel.
	// `progressScript` (when set) writes ffmpeg-shaped key=value lines to fd 3 before the
	// output, so the REAL parser and the reporting path are exercised without ffmpeg.
	script := "printf %s " + shellQuote(f.output)
	if f.progressScript != "" {
		script = f.progressScript + "; " + script
	}
	return playout.Start(ctx, "sh", []string{"-c", script}, nil, onProgress)
}

// shellQuote wraps a string in single quotes for `sh -c`.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func (f *fakeEncoder) args() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.gotArgs
}

func (f *fakeEncoder) processSpec() diagnostics.ProcessSpec {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.spec
}

type programOpts struct {
	resolver api.PlayoutResolver
	encoder  api.PlayoutEncoder
	logger   *slog.Logger
	font     string
	playout  api.Playout
	noToken  bool
	sessions api.PlayoutObserver
	// config overlays LiveConfig, for tests about a setting the handler reads live —
	// `filler.target_lufs` (§10 V40) is the first.
	config map[string]string
	// reclaimVRAM is the LLM-eviction seam the retry ladder calls (§9.1 V47) — set by ladder tests
	// to observe that eviction fired; nil for every other test (the ladder then skips that step).
	reclaimVRAM func(ctx context.Context)
}

func newProgramServer(t *testing.T, o programOpts) *httptest.Server {
	t.Helper()
	st := openTestStore(t, t.TempDir()+"/prog.db")
	t.Cleanup(func() { _ = st.Close() })

	cfg := map[string]string{
		"server.public_url": "http://loomarr.local:8080",
		"playout.backend":   "internal",
	}
	for k, v := range o.config {
		cfg[k] = v
	}
	if o.playout == nil {
		o.playout = &testkit.Playout{}
	}
	if o.logger == nil {
		o.logger = slog.New(slog.DiscardHandler)
	}
	opts := api.Options{
		Store:           st,
		Auth:            api.NewTokenAuthorizer(adminToken),
		Log:             o.logger,
		PlayoutResolver: o.resolver,
		PlayoutEncoder:  o.encoder,
		PlayoutObserver: o.sessions,
		PlayoutFont:     func() string { return o.font },
		Playout:         o.playout,
		LiveConfig:      func(k string) string { return cfg[k] },
		ReclaimVRAM:     o.reclaimVRAM,
	}
	if !o.noToken {
		opts.PlayoutSecret = func() string { return playoutToken }
	}
	srv := httptest.NewServer(api.Router(o.logger, opts))
	t.Cleanup(srv.Close)
	return srv
}

func TestPlayoutProgramAdmissionFailureDoesNoResolverOrEncoderWork(t *testing.T) {
	for _, tc := range []struct {
		name      string
		admission api.Playout
		status    int
	}{
		{
			name:      "channel outside transport catalog",
			admission: &testkit.Playout{AdmissionErr: playout.ErrIneligible},
			status:    http.StatusNotFound,
		},
		{
			name: "listener gate closed",
			admission: playout.NewOrigin(playout.OriginDependencies{
				Available: func() bool { return false },
			}),
			status: http.StatusServiceUnavailable,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resolver := &fakeResolver{airing: playableAiring(0, time.Hour), url: "http://emby/v/1"}
			encoder := &fakeEncoder{output: "must-not-run"}
			srv := newProgramServer(t, programOpts{
				resolver: resolver,
				encoder:  encoder.start,
				playout:  tc.admission,
			})

			resp := getPlayout(t, srv, "/v1/playout/program/ch1?token="+playoutToken)
			if resp.StatusCode != tc.status {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.status)
			}
			if got := resolver.callCount(); got != 0 {
				t.Fatalf("resolver calls = %d, want 0", got)
			}
			if got := encoder.args(); got != nil {
				t.Fatalf("encoder started with %v", got)
			}
		})
	}
}

func TestPlayoutProgram_DoesNotSpawnWhenPreparedFallbackCannotClaimCapacity(t *testing.T) {
	encoder := &fakeEncoder{output: "must-not-run"}
	sessions := &fakePlayoutSessions{denyProgram: true}
	srv := newProgramServer(t, programOpts{
		resolver: &fakeResolver{airing: playableAiring(0, time.Hour), url: "http://emby/v/1"},
		encoder:  encoder.start, sessions: sessions,
	})

	resp := getPlayout(t, srv, "/v1/playout/program/ch1?token="+playoutToken)
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 while the block supervisor waits for capacity", resp.StatusCode)
	}
	if got := encoder.args(); got != nil {
		t.Fatalf("encoder started without transcode capacity: %v", got)
	}
	if len(sessions.programCosts) == 0 {
		t.Fatal("program did not ask the session manager for its real cost")
	}
}

func TestPlayoutProgramAdmissionCannotEscapeConcurrentStopAll(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	resolver := &fakeResolver{
		airing: playableAiring(0, time.Hour), url: "http://emby/v/1",
		airingEntered: entered, airingRelease: release,
	}
	encoder := &fakeEncoder{failErr: errors.New("must not start")}
	origin := playout.NewOrigin(playout.OriginDependencies{
		Eligible:     func(context.Context, string) (bool, error) { return true, nil },
		LiveSessions: &playout.Manager{},
		LiveHLS:      &playout.HLSManager{},
	})
	srv := newProgramServer(t, programOpts{
		resolver: resolver,
		encoder:  encoder.start,
		playout:  origin,
	})

	done := make(chan *http.Response, 1)
	go func() {
		resp, _ := srv.Client().Get(srv.URL + "/v1/playout/program/ch1?token=" + playoutToken)
		done <- resp
	}()
	<-entered
	origin.StopAll()
	close(release)
	resp := <-done
	if resp != nil {
		_ = resp.Body.Close()
	}
	if got := encoder.args(); got != nil {
		t.Fatalf("encoder started after StopAll with %v", got)
	}
}

func playableAiring(offset, remaining time.Duration) playout.Airing {
	return playout.Airing{
		Kind: schedule.SlotProgram, LibraryItemID: "item-1", Title: "Heat",
		Offset: offset, Remaining: remaining,
	}
}

// The device token gates this route like every other playout route (§11).
func TestPlayoutProgram_RequiresTheDeviceToken(t *testing.T) {
	srv := newProgramServer(t, programOpts{
		resolver: &fakeResolver{airing: playableAiring(0, time.Hour), url: "http://emby/v/1"},
		encoder:  (&fakeEncoder{output: "ts"}).start,
	})
	for _, q := range []string{"", "?token=wrong", "?token=" + playoutToken[:8]} {
		resp := getPlayout(t, srv, "/v1/playout/program/ch1"+q)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("token %q: status %d, want 404", q, resp.StatusCode)
		}
	}
}

// THE CORE BEHAVIOUR: one program's bytes, then the response ENDS. That EOF is what makes the
// block supervisor advance to the next program — a response that never ended would pin the
// channel to one program forever.
func TestPlayoutProgram_StreamsOneProgramThenEnds(t *testing.T) {
	enc := &fakeEncoder{output: "program-bytes"}
	srv := newProgramServer(t, programOpts{
		resolver: &fakeResolver{airing: playableAiring(0, time.Hour), url: "http://emby/v/1"},
		encoder:  enc.start,
	})

	resp := getPlayout(t, srv, "/v1/playout/program/ch1?token="+playoutToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "video/mp2t" {
		t.Errorf("Content-Type = %q, want video/mp2t", ct)
	}
	identity, ok := api.ParsePlayoutAiringIdentity(resp.Header)
	if !ok {
		t.Fatal("program response has no valid Airing identity")
	}
	if got := identity.EndsAt.Sub(identity.StartedAt); got != time.Hour {
		t.Fatalf("Airing boundary = %s after start, want 1h so the supervisor cannot replay an early EOF", got)
	}

	// The body must terminate on its own — read it fully with a deadline.
	done := make(chan []byte, 1)
	go func() { b, _ := io.ReadAll(resp.Body); done <- b }()
	select {
	case body := <-done:
		if string(body) != "program-bytes" {
			t.Errorf("body = %q, want the encoder's output", body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the response never ended — the channel would be stuck on one program")
	}
}

// The seek offset and the slot's remaining time must reach ffmpeg, or a mid-program tune-in
// restarts the show and a program overruns its slot.
func TestPlayoutProgram_PassesTheSeekAndTheSlotBound(t *testing.T) {
	enc := &fakeEncoder{output: "x"}
	srv := newProgramServer(t, programOpts{
		resolver: &fakeResolver{
			airing: playableAiring(40*time.Minute, 20*time.Minute),
			url:    "http://emby/Videos/abc/stream?static=true",
		},
		encoder: enc.start,
	})

	resp := getPlayout(t, srv, "/v1/playout/program/ch1?token="+playoutToken)
	_, _ = io.ReadAll(resp.Body)

	got := strings.Join(enc.args(), " ")
	if !strings.Contains(got, "-ss 2400.000") {
		t.Errorf("no 40-minute seek — the joiner would restart the show: %q", got)
	}
	if !strings.Contains(got, "-t 1200.000") {
		t.Errorf("no slot bound — the program would overrun its slot: %q", got)
	}
	if !strings.Contains(got, "http://emby/Videos/abc/stream") {
		t.Errorf("the resolved stream URL did not reach ffmpeg: %q", got)
	}
}

// Nothing airing ⇒ the offline CARD, not an empty 200. An empty body EOFs the demuxer
// instantly and it re-requests in a tight loop, spinning a core on an empty channel.
func TestPlayoutProgram_NothingAiringServesABoundedCard(t *testing.T) {
	enc := &fakeEncoder{output: "card"}
	srv := newProgramServer(t, programOpts{
		// A flex Airing with no item: "nothing is on".
		resolver: &fakeResolver{airing: playout.Airing{Kind: schedule.SlotFlex}},
		encoder:  enc.start,
	})

	resp := getPlayout(t, srv, "/v1/playout/program/ch1?token="+playoutToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200 — an unplayable channel still gets a card", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if len(body) == 0 {
		t.Fatal("empty body — the demuxer would re-request in a tight loop")
	}

	got := strings.Join(enc.args(), " ")
	if !strings.Contains(got, "color=c=black") {
		t.Errorf("not the synthetic card: %q", got)
	}
	// BOUNDED is the point: the card args loop forever by design, so without -t the channel
	// could never pick up content that later lands.
	if !strings.Contains(got, "-t 30.000") {
		t.Errorf("the card is not bounded — the channel could never pick up new content: %q", got)
	}
	// And the silent audio track must survive: a video-only MPEG-TS is a classic cause of a
	// player refusing to play.
	if !strings.Contains(got, "anullsrc") {
		t.Errorf("the card lost its silent audio track: %q", got)
	}
}

// A scheduled filler gap is not an empty Channel. If its pod cannot supply a playable clip,
// the fallback card must keep that distinction and end at the real programme boundary. A fixed
// 30-second card started ten seconds before the boundary would otherwise cover the first twenty
// seconds of the next episode and tell the viewer, incorrectly, that nothing was scheduled.
func TestPlayoutProgram_UnfilledBreakStopsAtTheProgrammeBoundary(t *testing.T) {
	enc := &fakeEncoder{output: "card"}
	srv := newProgramServer(t, programOpts{
		resolver: &fakeResolver{airing: playout.Airing{
			Kind: schedule.SlotFiller, Remaining: 10 * time.Second,
		}},
		encoder: enc.start,
		font:    "/font.ttf",
	})

	resp := getPlayout(t, srv, "/v1/playout/program/ch1?token="+playoutToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200 — an unfilled break still gets a card", resp.StatusCode)
	}
	_, _ = io.ReadAll(resp.Body)

	got := strings.Join(enc.args(), " ")
	if !strings.Contains(got, "right back") {
		t.Errorf("scheduled break was mislabeled as an empty Channel: %q", got)
	}
	if !strings.Contains(got, "-t 10.000") {
		t.Errorf("break card crossed the programme boundary: %q", got)
	}
	if strings.Contains(got, "ch1") {
		t.Errorf("commercial-break card exposes the internal Channel ID: %q", got)
	}
}

// A resolver failure shows the CARD, not a 502.
//
// The usual cause of a resolver failure is the media server being briefly unreachable — the single
// most likely failure this handler sees. Trading the whole broadcast for it is the wrong trade, so
// the handler renders the card and the supervisor re-asks 30s later, by which time the outage has
// usually passed.
func TestPlayoutProgram_ResolverFailureShowsTheCard(t *testing.T) {
	enc := &fakeEncoder{output: "card-bytes"}
	srv := newProgramServer(t, programOpts{
		resolver: &fakeResolver{err: errors.New("emby unreachable")},
		encoder:  enc.start,
	})
	resp := getPlayout(t, srv, "/v1/playout/program/ch1?token="+playoutToken)
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200 — a 5xx here kills the parent demuxer and the whole channel", resp.StatusCode)
	}
	if string(body) != "card-bytes" {
		t.Errorf("body = %q, want the card's bytes", body)
	}
	// It must be the CARD, not a half-built program: the resolver failed, so there is no source
	// to read and the args have to come from the synthetic lavfi generator.
	if got := strings.Join(enc.args(), " "); !strings.Contains(got, "color=c=black") {
		t.Errorf("did not render the offline card on a resolver failure: %q", got)
	}
	identity, ok := api.ParsePlayoutAiringIdentity(resp.Header)
	if !ok {
		t.Fatal("resolver-failure card has no complete airing identity")
	}
	if got := enc.processSpec().ScheduleBlockID; got == "" || got != identity.ScheduleBlockID {
		t.Errorf("card Process schedule block = %q, response identity = %q", got, identity.ScheduleBlockID)
	}
}

// A channel that does not exist is a 404, so the demuxer stops rather than retrying forever.
func TestPlayoutProgram_UnknownChannelIs404(t *testing.T) {
	srv := newProgramServer(t, programOpts{
		resolver: &fakeResolver{err: store.ErrNotFound},
		encoder:  (&fakeEncoder{}).start,
	})
	resp := getPlayout(t, srv, "/v1/playout/program/nope?token="+playoutToken)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status %d, want 404", resp.StatusCode)
	}
}

// An encoder that fails to start must report it, not hang or serve a truncated 200.
func TestPlayoutProgram_EncoderStartFailureIsReported(t *testing.T) {
	srv := newProgramServer(t, programOpts{
		resolver: &fakeResolver{airing: playableAiring(0, time.Hour), url: "http://emby/v/1"},
		encoder:  (&fakeEncoder{failErr: errors.New("no such binary")}).start,
	})
	resp := getPlayout(t, srv, "/v1/playout/program/ch1?token="+playoutToken)
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status %d, want 502", resp.StatusCode)
	}
}

// The audio-language preference must reach the ENCODER, not just the resolver (§9.1). This is
// the seam the Russian-audio bug lived on: the selection can be perfectly correct and still not
// be applied, because the handler builds the args.
func TestPlayoutProgram_MapsTheResolvedAudioTrack(t *testing.T) {
	res := &fakeResolver{
		airing:     playableAiring(0, time.Minute),
		url:        "http://emby/v/1",
		audioTrack: 2,
	}
	enc := &fakeEncoder{output: "chunk"}
	srv := newProgramServer(t, programOpts{resolver: res, encoder: enc.start})

	resp := getPlayout(t, srv, "/v1/playout/program/ch1?token="+playoutToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	_, _ = io.ReadAll(resp.Body)

	args := strings.Join(enc.args(), " ")
	if !strings.Contains(args, "-map 0:a:2") {
		t.Errorf("args did not map the resolved audio track 2:\n%s", args)
	}
	if strings.Contains(args, "-map 0:a:0") {
		t.Errorf("args still map track 0 — the preference was ignored or double-mapped:\n%s", args)
	}
}

// Playout not running is a 501 that explains itself, not a 404 that reads as a wiring mistake.
func TestPlayoutProgram_NotRunningExplainsItself(t *testing.T) {
	srv := newProgramServer(t, programOpts{resolver: nil, encoder: nil})
	resp := getPlayout(t, srv, "/v1/playout/program/ch1?token="+playoutToken)
	if resp.StatusCode != http.StatusNotImplemented {
		t.Errorf("status %d, want 501", resp.StatusCode)
	}
}

// REPEATED requests are the normal case — the demuxer opens this once per program, forever. Each
// must resolve independently, and none may leak state into the next.
func TestPlayoutProgram_IsCalledRepeatedlyAndStaysConsistent(t *testing.T) {
	res := &fakeResolver{airing: playableAiring(10*time.Second, time.Minute), url: "http://emby/v/1"}
	enc := &fakeEncoder{output: "chunk"}
	srv := newProgramServer(t, programOpts{resolver: res, encoder: enc.start})

	for i := 0; i < 5; i++ {
		resp := getPlayout(t, srv, "/v1/playout/program/ch1?token="+playoutToken)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("request %d: status %d", i, resp.StatusCode)
		}
		body, _ := io.ReadAll(resp.Body)
		if string(body) != "chunk" {
			t.Errorf("request %d: body = %q", i, body)
		}
	}
	if got := res.callCount(); got != 5 {
		t.Errorf("resolver called %d times for 5 requests — the handler is caching or skipping", got)
	}
}

func TestPlayoutProgram_PinsTheFirstBlocksBroadcastFormat(t *testing.T) {
	res := &fakeResolver{
		airing: playableAiring(0, time.Minute), url: "http://emby/v/1",
		profile: playout.Profile{
			Width: 1280, Height: 720, Framerate: 25, VideoBitrate: 2500,
			AudioBitrate: 128, Encoder: playout.EncoderSoftware,
		},
	}
	enc := &fakeEncoder{output: "chunk"}
	srv := newProgramServer(t, programOpts{resolver: res, encoder: enc.start})

	first := getPlayout(t, srv, "/v1/playout/program/ch1?token="+playoutToken)
	_, _ = io.Copy(io.Discard, first.Body)
	format := first.Header.Get(api.PlayoutBroadcastFormatHeader)
	if _, ok := playout.ParseBroadcastFormat(format); !ok {
		t.Fatalf("first response broadcast format = %q, want a valid token", format)
	}

	// Simulate another channel starting between blocks and moving the live quality ladder. The
	// session token, not this newly resolved profile, must still govern the next child.
	res.profile = playout.Profile{
		Width: 1920, Height: 1080, Framerate: 30, VideoBitrate: 5000,
		AudioBitrate: 160, Encoder: playout.EncoderSoftware,
	}
	second := getPlayout(t, srv, "/v1/playout/program/ch1?token="+playoutToken+"&"+
		api.PlayoutBroadcastFormatQuery+"="+format)
	_, _ = io.Copy(io.Discard, second.Body)
	if got := second.Header.Get(api.PlayoutBroadcastFormatHeader); got != format {
		t.Fatalf("second response broadcast format = %q, want pinned %q", got, format)
	}
	args := strings.Join(enc.args(), " ")
	if !strings.Contains(args, "scale=1280:720") || !strings.Contains(args, "fps=25") ||
		!strings.Contains(args, "-b:v 2500k") || !strings.Contains(args, "-b:a 128k") {
		t.Fatalf("second block did not retain first block profile:\n%s", args)
	}
}

// A client disconnecting mid-program must not leave the encoder running. The child's lifetime is
// bound to the request, and a leaked child is a core burned until the process dies.
func TestPlayoutProgram_DisconnectStopsTheEncoder(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	// An encoder that would run for a long time, so the disconnect is what ends it.
	marker := t.TempDir() + "/alive"
	enc := api.PlayoutEncoder(func(
		ctx context.Context, _ []string, _ func(playout.Progress),
	) (*playout.Process, error) {
		// Removes the marker on SIGTERM, so the assertion below observes the real teardown
		// path (Stop signals the process GROUP) rather than just the process disappearing.
		script := "trap 'rm -f " + marker + "; exit 0' TERM; touch " + marker +
			"; while :; do printf x; sleep 0.05; done"
		return playout.Start(ctx, "sh", []string{"-c", script}, nil, nil)
	})
	srv := newProgramServer(t, programOpts{
		resolver: &fakeResolver{airing: playableAiring(0, time.Hour), url: "http://emby/v/1"},
		encoder:  enc,
	})

	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		srv.URL+"/v1/playout/program/ch1?token="+playoutToken, nil)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1)
	if _, err := io.ReadFull(resp.Body, buf); err != nil {
		t.Fatalf("stream did not start: %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the encoder never started: %v", err)
	}

	cancel()
	_ = resp.Body.Close()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(marker); os.IsNotExist(err) {
			return // the child was signalled and cleaned up
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Error("the encoder survived the client disconnect — an orphan burning a core")
}

// A channel that produces NO BYTES must say so loudly.
//
// This is the bug that took a live channel down silently: a misconfigured hardware encoder died
// at startup, which closes stdout — so the copy saw a clean EOF, copyErr was nil, and the old
// `copyErr != nil && n == 0` guard never fired. The viewer's player buffered forever with not
// one line in the log at INFO, because ffmpeg's stderr goes to DEBUG.
func TestPlayoutProgram_ZeroBytesIsLoggedAsAWarning(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	st := openTestStore(t, t.TempDir()+"/zero.db")
	t.Cleanup(func() { _ = st.Close() })

	cfg := map[string]string{"server.public_url": "http://loomarr.local:8080", "playout.backend": "internal"}
	srv := httptest.NewServer(api.Router(logger, api.Options{
		Store: st, Auth: api.NewTokenAuthorizer(adminToken), Log: logger,
		PlayoutSecret:   func() string { return playoutToken },
		Playout:         &testkit.Playout{},
		PlayoutResolver: &fakeResolver{airing: playableAiring(0, time.Hour), url: "http://emby/v/1"},
		// An "encoder" that exits immediately without writing anything — exactly what a
		// hardware encoder does when its device is missing.
		PlayoutEncoder: func(ctx context.Context, _ []string, _ func(playout.Progress)) (*playout.Process, error) {
			return playout.Start(ctx, "sh", []string{"-c", "exit 0"}, nil, nil)
		},
		LiveConfig: func(k string) string { return cfg[k] },
	}))
	t.Cleanup(srv.Close)

	resp := getPlayout(t, srv, "/v1/playout/program/ch1?token="+playoutToken)
	_, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	got := buf.String()
	if !strings.Contains(got, "NO OUTPUT") {
		t.Errorf("a channel that produced zero bytes logged nothing at INFO — the operator "+
			"sees a buffering player and an empty log. Got:\n%s", got)
	}
}

// --- V47: the transcode retry ladder (hardware → evict+retry → software) --------

// ladderEncoder is a fakeEncoder that FAILS (zero output) whenever the args ask for the hardware
// encoder, and SUCCEEDS (real bytes) for libx264 — modelling a GPU that cannot fit the encode
// (VRAM full) while software always works. It records every encoder it was asked to run so a test
// can assert the ladder's path.
type ladderEncoder struct {
	mu     sync.Mutex
	hwEnc  string   // the hardware encoder token that should FAIL, e.g. "h264_vulkan"
	tried  []string // "hardware" or "software", in call order — PROGRAM attempts only
	cards  []string // the same, for OFFLINE CARD renders
	output string
}

// start classifies each invocation on two independent axes, because the ladder's invariants are
// about the two SEPARATELY and a single counter cannot express either.
//
// The card renders through this same encoder — it is a real encode of a synthetic source — so a
// bare "how many times were you called?" conflates "the handler retried the PROGRAM" (which a copy
// failure must never do) with "the handler fell back to the CARD" (which every failure now should).
// Discriminating on the lavfi source is not a test convenience: `color=c=black` is what makes the
// card a card, so the classification tracks the thing itself rather than a label we chose.
func (e *ladderEncoder) start(ctx context.Context, args []string, _ func(playout.Progress)) (*playout.Process, error) {
	software, card := false, false
	for _, a := range args {
		if a == "libx264" || a == "libx265" {
			software = true
		}
		if strings.Contains(a, "color=c=black") {
			card = true
		}
	}
	step := "hardware"
	if software {
		step = "software"
	}
	e.mu.Lock()
	if card {
		e.cards = append(e.cards, step)
	} else {
		e.tried = append(e.tried, step)
	}
	e.mu.Unlock()

	// Hardware writes nothing (device won't allocate); software writes real bytes.
	script := "exit 0"
	if software {
		script = "printf %s " + shellQuote(e.output)
	}
	return playout.Start(ctx, "sh", []string{"-c", script}, nil, nil)
}

func (e *ladderEncoder) path() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.tried...)
}

// cardPath is the ladder the OFFLINE CARD climbed, empty when no card was rendered.
func (e *ladderEncoder) cardPath() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.cards...)
}

// THE LADDER, END TO END: a hardware encode that produces nothing must not black the channel. The
// handler reclaims VRAM (evicts the LLM), retries hardware, and — since the fake's hardware still
// fails — falls back to libx264, which plays. The channel serves bytes; the operator never sees a
// black frame. This is the fix for the Ollama-vs-encoder VRAM contention (§9.1 V47).
func TestPlayoutProgram_TranscodeFallsBackToSoftware(t *testing.T) {
	enc := &ladderEncoder{hwEnc: "h264_vulkan", output: "software-bytes"}
	var evicted int
	srv := newProgramServer(t, programOpts{
		// A hardware profile + the default transcode plan (CopyVideo=false) → the ladder applies.
		resolver: &fakeResolver{
			airing:  playableAiring(0, time.Hour),
			url:     "http://emby/v/1",
			profile: playout.Profile{Width: 1280, Height: 720, Framerate: 30, Encoder: "h264_vulkan", VideoBitrate: 4000, AudioBitrate: 128},
		},
		encoder:     enc.start,
		reclaimVRAM: func(context.Context) { evicted++ },
	})

	resp := getPlayout(t, srv, "/v1/playout/program/ch1?token="+playoutToken)
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	// The channel PLAYS — the whole point. It served the software encoder's bytes, not a black frame.
	if resp.StatusCode != http.StatusOK || string(body) != "software-bytes" {
		t.Fatalf("channel did not play: status %d body %q (want 200 + software bytes)", resp.StatusCode, body)
	}
	// The ladder was climbed in order: hardware, (evict), hardware retry, software.
	path := enc.path()
	if len(path) != 3 || path[0] != "hardware" || path[1] != "hardware" || path[2] != "software" {
		t.Errorf("ladder path = %v, want [hardware hardware software]", path)
	}
	// VRAM was reclaimed exactly once (before the hardware retry), not speculatively.
	if evicted != 1 {
		t.Errorf("reclaimVRAM called %d times, want 1 (once, before the hardware retry)", evicted)
	}
}

func TestPlayoutProgram_FallbackLogNamesEncodersWithoutSourceCredentials(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo}))
	enc := &ladderEncoder{hwEnc: "hevc_videotoolbox", output: "software-bytes"}
	srv := newProgramServer(t, programOpts{
		logger: logger,
		resolver: &fakeResolver{
			airing: playableAiring(0, time.Hour),
			url:    "http://library-user:do-not-log@emby.invalid/video?api_key=do-not-log",
			profile: playout.Profile{
				Width: 1280, Height: 720, Framerate: 30, Encoder: playout.EncoderVideoToolbox,
				VideoBitrate: 4000, AudioBitrate: 128,
			},
			channelCodec: "hevc",
		},
		encoder:     enc.start,
		reclaimVRAM: func(context.Context) {},
	})

	resp := getPlayout(t, srv, "/v1/playout/program/ch1?token="+playoutToken+"&plan=full")
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	got := logs.String()
	for _, encoder := range []string{"hevc_videotoolbox", "libx265"} {
		if !strings.Contains(got, encoder) {
			t.Errorf("fallback log does not identify attempted encoder %q:\n%s", encoder, got)
		}
	}
	for _, secret := range []string{"library-user", "do-not-log", "api_key"} {
		if strings.Contains(got, secret) {
			t.Errorf("fallback log exposed source credential fragment %q:\n%s", secret, got)
		}
	}
}

// A COPY plan is NOT laddered: a `-c copy` that produces nothing is a bad source file, which no
// encoder swap fixes. So a failing copy must NOT evict the LLM or try software — it fails straight
// through. This keeps the ladder scoped to transcodes, where it can actually help.
func TestPlayoutProgram_CopyPlanIsNotLaddered(t *testing.T) {
	// An encoder that always produces nothing, and a resolver whose plan COPIES the video.
	enc := &ladderEncoder{hwEnc: "h264_vulkan", output: "never"}
	var evicted int
	srv := newProgramServer(t, programOpts{
		resolver: &fakeResolver{
			airing: playableAiring(0, time.Hour),
			url:    "http://emby/v/1",
			plan:   playout.CopyPlan{CopyVideo: true, CopyAudio: true}, // direct play
			sourceFormat: playout.MediaFormat{
				VideoCodec: "h264", Width: 1280, Height: 720, FrameRate: 25, PixelFormat: "yuv420p",
				AudioCodec: "aac", AudioChannels: 2, AudioSampleRate: 48000,
			},
		},
		encoder:     enc.start,
		reclaimVRAM: func(context.Context) { evicted++ },
	})

	resp := getPlayout(t, srv, "/v1/playout/program/ch1?token="+playoutToken)
	_, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	// One attempt at the PROGRAM only — no evict, no software retry of the bad file.
	if path := enc.path(); len(path) != 1 {
		t.Errorf("copy plan made %d program encode attempts, want 1 (no ladder for a copy): %v", len(path), path)
	}
	if evicted != 0 {
		t.Errorf("a failing COPY evicted the LLM %d times; a bad file is not a VRAM problem", evicted)
	}
	// ...but the CHANNEL survives it. The card is a separate encode of a synthetic source, so it
	// carries its own hardware→software fallback; what matters is that one was rendered at all.
	if cards := enc.cardPath(); len(cards) == 0 {
		t.Error("a failing copy produced no offline card — the channel is left with nothing to play")
	}
	// The status is the point: a terminal 5xx leaves the supervisor without a transport block and
	// parent ffmpeg, taking every viewer with it. One bad file must not end the broadcast.
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200: a 5xx on a concat entry kills the parent and drops every viewer", resp.StatusCode)
	}
}

// --- V16: the per-program encoder's progress reaches the session ---------------

// END TO END through the REAL parser: `sh` writes ffmpeg-shaped `key=value` lines to fd 3,
// exactly as ffmpeg's `-progress pipe:3` does, and the assertion is what arrived at the session.
//
// This is the wiring V16 exists to fix. `playout.Start` has accepted an `onProgress` callback
// since the supervisor was written, and every caller passed nil — so each sample was parsed and
// discarded. Nothing downstream could tell the difference between "the encoder is at 12× and
// healthy" and "no telemetry exists", which is why the dashboard had no real numbers to show.
func TestProgramEncoder_ReportsProgressToTheSession(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	sessions := &fakePlayoutSessions{}
	enc := &fakeEncoder{
		output: "ts-bytes",
		// A whole ffmpeg progress block. `progress=continue` is the terminator the parser
		// emits on, so anything before it must arrive as ONE sample, never half-updated.
		progressScript: `{ printf 'frame=120\nspeed=12.4x\nout_time_ms=4000000\nprogress=continue\n' >&3; }`,
	}
	srv := newProgramServer(t, programOpts{
		resolver: &fakeResolver{airing: playableAiring(0, time.Hour), url: "http://emby/v/1"},
		encoder:  enc.start,
		sessions: sessions,
	})

	resp := getPlayout(t, srv, "/v1/playout/program/ch1?token="+playoutToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("program → %d, want 200", resp.StatusCode)
	}
	_, _ = io.Copy(io.Discard, resp.Body)

	// ⚠ POLLED, not read once. `sh` writes to fd 3 on its own schedule, and draining the
	// response body does not wait for it — so a single read asserts that a subprocess won a
	// race. It usually does, which is worse than never: this test passed locally and on the
	// PR, then failed on main's post-merge run with "no progress reached the session", which
	// reads as the wiring bug V16 fixed rather than as the scheduling artefact it was.
	//
	// A deadline rather than a sleep: the fast path stays fast (it typically lands on the
	// first pass) and a real regression still fails, just a second later.
	var got []reportedProgram
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		if got = sessions.reports(); len(got) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(got) == 0 {
		t.Fatal("no progress reached the session — the callback is nil somewhere in the chain")
	}
	last := got[len(got)-1]
	if last.channelID != "ch1" {
		t.Errorf("reported channel %q, want ch1 — telemetry keyed to the wrong channel is worse than none", last.channelID)
	}
	if last.progress.Speed != 12.4 {
		t.Errorf("speed = %v, want 12.4 parsed from the progress stream", last.progress.Speed)
	}
	// ffmpeg reports out_time_ms in MICROseconds despite the name; the parser divides.
	if last.progress.OutTimeMS != 4000 {
		t.Errorf("outTimeMs = %d, want 4000 (4s) — ffmpeg's out_time_ms is microseconds", last.progress.OutTimeMS)
	}
	if last.progress.Frame != 120 {
		t.Errorf("frame = %d, want 120", last.progress.Frame)
	}
}

// The encoder the program RESOLVED is what gets reported — not the session's own `-c copy`
// parent, which never encodes anything and whose "encoder" would be copy.
func TestProgramEncoder_ReportsTheResolvedEncoder(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	sessions := &fakePlayoutSessions{}
	enc := &fakeEncoder{
		output:         "ts",
		progressScript: `{ printf 'speed=1.0x\nprogress=continue\n' >&3; }`,
	}
	srv := newProgramServer(t, programOpts{
		resolver: &fakeResolver{airing: playableAiring(0, time.Hour), url: "http://emby/v/1"},
		encoder:  enc.start,
		sessions: sessions,
	})

	resp := getPlayout(t, srv, "/v1/playout/program/ch1?token="+playoutToken)
	_, _ = io.Copy(io.Discard, resp.Body)

	got := sessions.reports()
	if len(got) == 0 {
		t.Fatal("no report reached the session")
	}
	// fakeResolver's Profile resolves software; the point is that SOME resolved encoder is
	// carried, not the copy-parent's absence of one.
	if got[len(got)-1].encoder == "" {
		t.Error("reported an empty encoder — the dashboard's hardware/software badge would be blank")
	}
}

// fillerAiring is a resolved commercial clip. ⚠ `Source` is what marks it as filler — set for a
// clip resolved to a local file under FILLER_DIR, empty for a library title (see Airing.Source).
// That existing field is the discriminator the loudness gate reads, so it needed no new plumbing.
func fillerAiring(remaining time.Duration) playout.Airing {
	return playout.Airing{
		Kind: schedule.SlotFiller, Source: "/filler/14/36/abc.mp4", Title: "Frosted Flakes",
		Remaining: remaining,
	}
}

// Loudness normalisation, FILLER ONLY (§10 V40).
//
// Measured across real fetched clips the spread was -21.8 to -32.6 LUFS — about 11 dB of
// clip-to-clip jump, which is what an operator hears as "some of these are too quiet".
func TestPlayoutProgram_NormalisesFillerLoudness(t *testing.T) {
	enc := &fakeEncoder{output: "ts"}
	srv := newProgramServer(t, programOpts{
		resolver: &fakeResolver{airing: fillerAiring(30 * time.Second), url: "http://emby/v/1"},
		encoder:  enc.start,
		config:   map[string]string{"filler.target_lufs": "-23"},
	})

	if resp := getPlayout(t, srv, "/v1/playout/program/ch1?token="+playoutToken); resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if joined := strings.Join(enc.args(), " "); !strings.Contains(joined, "loudnorm=I=-23") {
		t.Errorf("filler was not normalised; args = %v", enc.args())
	}
}

// ⚠ **THE guard.** A feature film normalised to advert loudness loses its dynamic range — the
// quiet scenes come up and the loud ones come down, which is the opposite of what a film wants.
// The problem being solved is adverts recorded a decade apart; a library title must be untouched
// even with the setting on.
func TestPlayoutProgram_LeavesLibraryProgramsAlone(t *testing.T) {
	enc := &fakeEncoder{output: "ts"}
	srv := newProgramServer(t, programOpts{
		// A library title: LibraryItemID set, Source empty.
		resolver: &fakeResolver{airing: playableAiring(0, time.Hour), url: "http://emby/v/1"},
		encoder:  enc.start,
		config:   map[string]string{"filler.target_lufs": "-23"},
	})

	if resp := getPlayout(t, srv, "/v1/playout/program/ch1?token="+playoutToken); resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if joined := strings.Join(enc.args(), " "); strings.Contains(joined, "loudnorm") {
		t.Errorf("a library program was normalised; args = %v", enc.args())
	}
}

// Empty target ⇒ no filter, even for filler. That is what "set it empty to disable" means in §15,
// and it keeps the pre-V40 behaviour reachable.
func TestPlayoutProgram_EmptyTargetDisablesNormalisation(t *testing.T) {
	enc := &fakeEncoder{output: "ts"}
	srv := newProgramServer(t, programOpts{
		resolver: &fakeResolver{airing: fillerAiring(30 * time.Second), url: "http://emby/v/1"},
		encoder:  enc.start,
		config:   map[string]string{"filler.target_lufs": ""},
	})

	if resp := getPlayout(t, srv, "/v1/playout/program/ch1?token="+playoutToken); resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if joined := strings.Join(enc.args(), " "); strings.Contains(joined, "loudnorm") {
		t.Errorf("normalisation ran with the setting disabled; args = %v", enc.args())
	}
}
