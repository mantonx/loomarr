package app

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/api"
	"github.com/loomarr/loomarr/internal/inventory"
	"github.com/loomarr/loomarr/internal/library"
	"github.com/loomarr/loomarr/internal/playout"
	"github.com/loomarr/loomarr/internal/schedule"
	"github.com/loomarr/loomarr/internal/store"
	"github.com/loomarr/loomarr/internal/testkit"
)

type staticChannelReader struct{ channel store.Channel }

func (s staticChannelReader) GetChannel(context.Context, string) (store.Channel, error) {
	return s.channel, nil
}

func TestBuild_WiresMeasuredCapacityToAdmissionAndQuality(t *testing.T) {
	t.Setenv("API_TOKEN", "capacity-test-token")

	st := testkit.MigratedSQLiteStore(t)
	for key, value := range map[string]string{
		"playout.backend":      "internal",
		"playout.encoder":      "libx264",
		"playout.max_channels": "9",
	} {
		if err := st.SetSetting(context.Background(), key, value); err != nil {
			t.Fatal(err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	application, err := Build(ctx, st, slog.New(slog.DiscardHandler), Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.Shutdown(context.Background()) })
	h := application.Handler()
	r := application.playoutResolver
	if r == nil {
		t.Fatal("Build wired no playout resolver")
	}
	// Detection is lazy; install the result a real encoder trial would publish without running
	// ffmpeg in a unit test. The configured 9 is deliberately above the measured 3.
	r.maxChannels.Store(3)

	req := httptest.NewRequest(http.MethodGet, "/v1/playout/sessions", nil)
	req.Header.Set("Authorization", "Bearer capacity-test-token")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/playout/sessions = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var telemetry api.PlayoutTelemetry
	if err := json.NewDecoder(rec.Body).Decode(&telemetry); err != nil {
		t.Fatal(err)
	}
	if telemetry.Capacity != 3 {
		t.Errorf("admission capacity = %d, want measured capacity 3", telemetry.Capacity)
	}

	// Three committed transcodes on a measured-three box take Balanced's safe bottom rung.
	// If Profile reads the configured 9 instead, it incorrectly selects the top 5000 kbit/s rung.
	r.activeChannels = func() int { return 3 }
	if got := r.Profile(context.Background()).VideoBitrate; got != 1800 {
		t.Errorf("full-box profile bitrate = %d, want bottom-rung 1800", got)
	}
}

func TestPlayoutResolver_ProfileUsesMatchingEvidenceBeforeAsyncValidation(t *testing.T) {
	loadCalls := 0
	validationStarted := make(chan struct{})
	validationRelease := make(chan struct{})
	r := &playoutResolver{
		tier: func() string { return "balanced" }, encoder: func() string { return "" },
		capacity: func() int { return 4 }, activeChannels: func() int { return 0 },
		loadCapabilityEvidence: func(context.Context) (playout.Capacity, bool) {
			loadCalls++
			return playout.Capacity{Chosen: playout.EncoderNVENC, MaxChannels: 4}, true
		},
		ffmpegPath: func() string {
			close(validationStarted)
			<-validationRelease
			return "/nonexistent/ffmpeg"
		},
	}

	before := time.Now()
	profile := r.Profile(t.Context())
	if elapsed := time.Since(before); elapsed > 100*time.Millisecond {
		t.Fatalf("Profile waited %s for asynchronous capability validation", elapsed)
	}
	if profile.Encoder != playout.EncoderNVENC || loadCalls != 1 ||
		r.maxChannels.Load() != 4 || !r.detectReady.Load() {
		t.Fatalf("first Profile = %+v, load calls=%d max=%d ready=%v; want matching NVENC evidence",
			profile, loadCalls, r.maxChannels.Load(), r.detectReady.Load())
	}
	select {
	case <-validationStarted:
	case <-time.After(time.Second):
		t.Fatal("Profile did not start evidence validation in the background")
	}
	close(validationRelease)
	_ = r.detectedEncoder(t.Context())
}

func TestPlayoutResolver_AudioTrackHonoursChannelOverride(t *testing.T) {
	r := &playoutResolver{
		audioLanguage: func() string { return "eng" },
		probeSource: func(context.Context, string) (playout.SourceObservation, error) {
			return playout.SourceObservation{Streams: []playout.ObservedStream{
				{Index: 1, Kind: "audio", Language: "eng"}, {Index: 2, Kind: "audio", Language: "jpn"},
			}}, nil
		},
		channels: staticChannelReader{channel: store.Channel{Policy: schedule.ChannelPolicy{
			OperatorPolicy: schedule.OperatorPolicy{Playout: &schedule.PlayoutPolicy{AudioLanguage: "jpn"}},
		}}},
	}

	if got := r.AudioTrackFor(context.Background(), "channel-1", "", "movie.mkv"); got != 1 {
		t.Fatalf("AudioTrackFor = %d, want channel override track 1", got)
	}
}

func TestPlayoutResolver_AudioTrackWarmsInventoryThenPerformsNoIO(t *testing.T) {
	st := testkit.MigratedSQLiteStore(t)
	server := testkit.NewMediaServer(t)
	server.InventoryItems = map[string]json.RawMessage{"item-1": json.RawMessage(`{
		"Id":"item-1","Type":"Movie","DateLastSaved":"2026-09-04T12:00:00Z",
		"MediaSources":[{"Id":"source-1","ETag":"rev-1","MediaStreams":[
			{"Index":0,"Type":"Video","Codec":"h264"},
			{"Index":2,"Type":"Audio","Language":"jpn"},
			{"Index":4,"Type":"Audio","Language":"eng"}
		]}]
	}`)}
	probeCalls := 0
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	r := &playoutResolver{
		lib: newTestLibraryClient(server), inventory: inventory.New(st), now: func() time.Time { return now },
		audioLanguage: func() string { return "eng" },
		probeSource: func(context.Context, string) (playout.SourceObservation, error) {
			probeCalls++
			return playout.SourceObservation{}, nil
		},
	}
	if got := r.AudioTrackFor(context.Background(), "channel-1", "item-1", "movie.mkv"); got != 1 {
		t.Fatalf("cold metadata-first AudioTrackFor = %d, want audio ordinal 1", got)
	}
	firstRequests := len(server.Requests())
	if firstRequests != 1 || probeCalls != 0 {
		t.Fatalf("cold metadata-first calls = library %d, ffprobe %d; want 1, 0", firstRequests, probeCalls)
	}
	server.InventoryItems = nil
	if got := r.AudioTrackFor(context.Background(), "channel-1", "item-1", "movie.mkv"); got != 1 {
		t.Fatalf("warm inventory AudioTrackFor = %d, want audio ordinal 1", got)
	}
	if got := len(server.Requests()); got != firstRequests || probeCalls != 0 {
		t.Fatalf("warm inventory added calls = library %d→%d, ffprobe %d; want none", firstRequests, got, probeCalls)
	}
}

func TestPlayoutResolver_AudioTrackPersistsProbeFallback(t *testing.T) {
	st := testkit.MigratedSQLiteStore(t)
	server := testkit.NewMediaServer(t)
	server.InventoryItems = map[string]json.RawMessage{"item-1": json.RawMessage(`{
		"Id":"item-1","Type":"Movie","DateLastSaved":"2026-09-04T12:00:00Z",
		"MediaSources":[{"Id":"source-1","ETag":"rev-1","MediaStreams":[
			{"Index":2,"Type":"Audio","Language":"jpn"},
			{"Index":2,"Type":"Audio","Language":"eng"}
		]}]
	}`)}
	probeCalls := 0
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	r := &playoutResolver{
		lib: newTestLibraryClient(server), inventory: inventory.New(st), now: func() time.Time { return now },
		audioLanguage: func() string { return "eng" },
		probeSource: func(context.Context, string) (playout.SourceObservation, error) {
			probeCalls++
			return playout.SourceObservation{Container: "matroska", Streams: []playout.ObservedStream{
				{Index: 0, Kind: "video", Codec: "h264", Width: 1920, Height: 1080},
				{Index: 1, Kind: "audio", Codec: "aac", Language: "jpn", Channels: 2},
				{Index: 3, Kind: "audio", Codec: "aac", Language: "eng", Channels: 2},
			}}, nil
		},
	}
	if got := r.AudioTrackFor(context.Background(), "channel-1", "item-1", "movie.mkv"); got != 1 {
		t.Fatalf("probe fallback AudioTrackFor = %d, want ordinal 1", got)
	}
	firstRequests := len(server.Requests())
	server.InventoryItems = nil
	if got := r.AudioTrackFor(context.Background(), "channel-1", "item-1", "movie.mkv"); got != 1 {
		t.Fatalf("measured inventory AudioTrackFor = %d, want ordinal 1", got)
	}
	if len(server.Requests()) != firstRequests || probeCalls != 1 {
		t.Fatalf("measured warm calls = library %d→%d, ffprobe %d; want no second I/O", firstRequests, len(server.Requests()), probeCalls)
	}
}

func TestPlayoutResolver_AudioTrackPersistsFullProbeWhenLibraryUnavailable(t *testing.T) {
	st := testkit.MigratedSQLiteStore(t)
	server := testkit.NewMediaServer(t)
	probeCalls := 0
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	r := &playoutResolver{
		lib: newTestLibraryClient(server), inventory: inventory.New(st), now: func() time.Time { return now },
		audioLanguage: func() string { return "eng" },
		probeSource: func(context.Context, string) (playout.SourceObservation, error) {
			probeCalls++
			return playout.SourceObservation{Container: "matroska", DurationMillis: 90_000, Bitrate: 4_000_000,
				UnsafePreroll: true,
				Streams: []playout.ObservedStream{
					{Index: 0, Kind: "video", Codec: "h264", Width: 1920, Height: 1080},
					{Index: 1, Kind: "audio", Codec: "aac", Language: "jpn", Channels: 2},
					{Index: 2, Kind: "audio", Codec: "aac", Language: "eng", Channels: 2},
				}}, nil
		},
	}
	if got := r.AudioTrackFor(context.Background(), "channel-1", "item-1", server.URL+"/video"); got != 1 {
		t.Fatalf("cold fallback AudioTrackFor = %d, want ordinal 1", got)
	}
	firstRequests := len(server.Requests())
	if got := r.AudioTrackFor(context.Background(), "channel-1", "item-1", server.URL+"/video"); got != 1 {
		t.Fatalf("warm fallback AudioTrackFor = %d, want ordinal 1", got)
	}
	if len(server.Requests()) != firstRequests || probeCalls != 1 {
		t.Fatalf("warm fallback added I/O: library %d→%d, probes %d", firstRequests, len(server.Requests()), probeCalls)
	}
	origin, err := r.lib.InventoryOrigin("item-1")
	if err != nil {
		t.Fatal(err)
	}
	item, ok, err := r.inventory.Item(context.Background(), inventory.ItemRef{Origin: &origin})
	if err != nil || !ok || len(item.Sources) != 1 || item.Sources[0].Measurement == nil {
		t.Fatalf("persisted fallback = (%+v, %v, %v)", item, ok, err)
	}
	facts := item.Sources[0].Measurement.Observation.Facts
	if facts.Container != "matroska" || facts.DurationMillis != 90_000 || !facts.UnsafePreroll || len(facts.Streams) != 3 ||
		facts.Streams[0].Width != 1920 {
		t.Fatalf("persisted probe lost superset facts: %+v", facts)
	}
}

func TestPlayoutResolver_LocalFileRevisionInvalidatesMeasuredAudio(t *testing.T) {
	st := testkit.MigratedSQLiteStore(t)
	path := t.TempDir() + "/movie.mkv"
	if err := os.WriteFile(path, []byte("first revision"), 0o600); err != nil {
		t.Fatal(err)
	}
	probeCalls := 0
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	r := &playoutResolver{
		inventory: inventory.New(st), now: func() time.Time { return now },
		audioLanguage: func() string { return "eng" },
		probeSource: func(context.Context, string) (playout.SourceObservation, error) {
			probeCalls++
			return playout.SourceObservation{Streams: []playout.ObservedStream{
				{Index: 1, Kind: "audio", Language: "jpn"}, {Index: 2, Kind: "audio", Language: "eng"},
			}}, nil
		},
	}
	if got := r.AudioTrackFor(context.Background(), "channel-1", "item-1", path); got != 1 {
		t.Fatalf("cold local AudioTrackFor = %d, want ordinal 1", got)
	}
	if got := r.AudioTrackFor(context.Background(), "channel-1", "item-1", path); got != 1 || probeCalls != 1 {
		t.Fatalf("warm local = %d, probes %d; want 1, 1", got, probeCalls)
	}
	changed := now.Add(time.Hour)
	if err := os.Chtimes(path, changed, changed); err != nil {
		t.Fatal(err)
	}
	if got := r.AudioTrackFor(context.Background(), "channel-1", "item-1", path); got != 1 || probeCalls != 2 {
		t.Fatalf("changed local = %d, probes %d; want revision invalidation and second probe", got, probeCalls)
	}
}

func TestPlayoutResolver_LocalAudioProbeFeedsCopyPlanWithoutSecondProbe(t *testing.T) {
	st := testkit.MigratedSQLiteStore(t)
	path := t.TempDir() + "/movie.mkv"
	if err := os.WriteFile(path, []byte("one stable local revision"), 0o600); err != nil {
		t.Fatal(err)
	}
	sourceProbes, formatProbes := 0, 0
	r := &playoutResolver{
		inventory: inventory.New(st), now: time.Now,
		audioLanguage: func() string { return "eng" },
		probeSource: func(context.Context, string) (playout.SourceObservation, error) {
			sourceProbes++
			return playout.SourceObservation{
				Container: "matroska", DurationMillis: 90_000, Bitrate: 4_000_000,
				Streams: []playout.ObservedStream{
					{Index: 0, Kind: "video", Codec: "h264", Width: 1920, Height: 1080,
						FrameRate: "25/1", PixelFormat: "yuv420p"},
					{Index: 1, Kind: "audio", Codec: "aac", Language: "jpn", Channels: 2, SampleRate: 48_000},
					{Index: 2, Kind: "audio", Codec: "aac", Language: "eng", Channels: 2, SampleRate: 48_000},
				},
			}, nil
		},
		probeFormat: func(context.Context, string) (playout.MediaFormat, error) {
			formatProbes++
			return playout.MediaFormat{}, nil
		},
	}

	if got := r.AudioTrackFor(t.Context(), "channel-1", "item-1", path); got != 1 {
		t.Fatalf("AudioTrackFor = %d, want English audio ordinal 1", got)
	}
	plan, format := r.PlanFor(t.Context(), path, playout.PlanFull)
	if !plan.DirectPlay() || format.VideoCodec != "h264" || format.AudioCodec != "aac" ||
		format.Width != 1920 || format.Height != 1080 || format.FrameRate != 25 {
		t.Fatalf("inventory-backed PlanFor = (%+v, %+v), want direct-play 1080p25 h264/aac", plan, format)
	}
	if sourceProbes != 1 || formatProbes != 0 {
		t.Fatalf("probe calls = source %d, format %d; want one shared source probe", sourceProbes, formatProbes)
	}
}

func TestPlayoutResolver_EmptyAudioPreferenceSkipsInventoryLibraryAndProbe(t *testing.T) {
	server := testkit.NewMediaServer(t)
	probeCalls := 0
	r := &playoutResolver{
		lib: newTestLibraryClient(server), audioLanguage: func() string { return "  " },
		probeSource: func(context.Context, string) (playout.SourceObservation, error) {
			probeCalls++
			return playout.SourceObservation{}, nil
		},
	}
	if got := r.AudioTrackFor(context.Background(), "channel-1", "item-1", "movie.mkv"); got != 0 {
		t.Fatalf("AudioTrackFor = %d, want first track", got)
	}
	if len(server.Requests()) != 0 || probeCalls != 0 {
		t.Fatalf("empty preference performed I/O: requests=%d probes=%d", len(server.Requests()), probeCalls)
	}
}

func newTestLibraryClient(server *testkit.MediaServer) *library.Client {
	return library.New(library.Emby, strings.TrimRight(server.URL, "/"), server.AdminToken, "inventory-test-device")
}

// ⚠ **THE QUALITY LADDER'S DEPENDENCIES ARE CALLED UNGUARDED.**
//
// `Profile` reaches `r.tier()`, `r.encoder()`, `r.capacity()` and `r.activeChannels()` with
// no nil checks, so any one of them missing is a panic on the LIVE playout path — when a
// viewer tunes in, which is the worst place to find out.
//
// That was not hypothetical: `activeChannels` used to be back-patched onto the resolver
// after construction, and deleting the assignment broke NO test. It is now set in the
// constructor literal, and this is the test that notices if it stops being.
func TestPlayoutResolver_ProfileNeedsEveryLadderInput(t *testing.T) {
	// A resolver wired the way Build wires it — every ladder input present.
	full := func() *playoutResolver {
		return &playoutResolver{
			tier:           func() string { return "720p" },
			encoder:        func() string { return "libx264" },
			capacity:       func() int { return 4 },
			activeChannels: func() int { return 1 },
		}
	}

	// The positive case first, so the negatives below are proven to be panics rather than a
	// resolver that never works.
	if got := full().Profile(context.Background()); got.Encoder == "" {
		t.Fatalf("Profile with every input wired returned %+v, want a usable profile", got)
	}

	// ⚠ Each input removed IN TURN must panic rather than silently degrade. A zero value
	// here would be worse than a crash: the ladder would quietly pick the wrong quality and
	// nobody would know which input was missing.
	for _, tc := range []struct {
		name string
		bust func(*playoutResolver)
	}{
		{"activeChannels", func(r *playoutResolver) { r.activeChannels = nil }},
		{"capacity", func(r *playoutResolver) { r.capacity = nil }},
		{"tier", func(r *playoutResolver) { r.tier = nil }},
		{"encoder", func(r *playoutResolver) { r.encoder = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := full()
			tc.bust(r)
			defer func() {
				if recover() == nil {
					t.Errorf("Profile with %s unset did NOT panic — it is called unguarded, "+
						"so an unset input must fail loudly rather than resolve a wrong quality",
						tc.name)
				}
			}()
			_ = r.Profile(context.Background())
		})
	}
}

// ⚠ **THE ASSERTION THAT WAS MISSING.** The test above pins the invariant (an unset ladder
// input panics); this pins that Build actually satisfies it.
//
// Both are needed, and the gap between them is exactly where the original defect lived:
// `activeChannels` was back-patched after construction, and deleting the assignment left
// every test green while a viewer tuning in would panic.
//
// Reads the resolver Build really constructed rather than one assembled here, since
// a test-built resolver only proves the test knows how to fill a struct.
func TestBuild_WiresEveryLadderInput(t *testing.T) {
	st := testkit.MigratedSQLiteStore(t)
	// Playout is only wired on the internal backend; without this the resolver is nil and
	// the test would pass vacuously.
	if err := st.SetSetting(context.Background(), "playout.backend", "internal"); err != nil {
		t.Fatal(err)
	}

	application, err := Build(t.Context(), st, slog.New(slog.DiscardHandler), Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.Shutdown(context.Background()) })
	r := application.playoutResolver
	if r == nil {
		t.Fatal("Build wired no playout resolver on the internal backend — " +
			"this test can no longer see what it is meant to guard")
	}

	for _, tc := range []struct {
		name string
		set  bool
	}{
		{"tier", r.tier != nil},
		{"encoder", r.encoder != nil},
		{"capacity", r.capacity != nil},
		{"activeChannels", r.activeChannels != nil},
	} {
		if !tc.set {
			t.Errorf("Build left %s unset — Profile calls it unguarded, so a viewer "+
				"tuning in would panic", tc.name)
		}
	}
}
