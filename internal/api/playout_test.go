package api_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/api"
	"github.com/loomarr/loomarr/internal/playout"
	"github.com/loomarr/loomarr/internal/schedule"
	"github.com/loomarr/loomarr/internal/store"
)

const (
	playoutToken   = "playout-token-abcdefghijklmnop"
	playoutTestURL = "http://example.com"
)

// fakePlayoutSessions stands in for playout.Manager. The session lifecycle is tested in
// internal/playout; here the question is what the HTTP layer does with it.
type fakePlayoutSessions struct {
	mu sync.Mutex
	// attached records each Attach as (channel, target) so a test can assert the tuner attaches as
	// PlanFull (§9.1 V47) — the identity that keeps HEVC a copy for a media server.
	attached []attachRecord
	err      error
	chunks   chan []byte
	streams  map[playout.EncodePlan]chan []byte
	detached int
	// V16 telemetry: what the handlers reported, and what Stats hands back.
	stats        []playout.SessionStat
	capacity     int
	reported     []reportedProgram
	asset        playout.Asset
	assetOK      bool
	opened       string
	tunes        int
	stopped      []string
	admitErr     error
	denyProgram  bool
	programCosts []bool
}

// attachRecord is one Attach call — its channel and codec target.
type attachRecord struct {
	channelID string
	target    playout.EncodePlan
}

// reportedProgram is one ReportProgram call, so a test can assert the per-program encode path
// actually reports its telemetry rather than silently dropping it — to the right (channel, target).
type reportedProgram struct {
	channelID   string
	target      playout.EncodePlan
	encoder     playout.Encoder
	transcoding bool
	progress    playout.Progress
}

func (f *fakePlayoutSessions) Stats(time.Time) []playout.SessionStat {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.stats
}

func (f *fakePlayoutSessions) Capacity() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.capacity
}

func (f *fakePlayoutSessions) ReportProgram(channelID string, target playout.EncodePlan, enc playout.Encoder, transcoding bool, p playout.Progress) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reported = append(f.reported, reportedProgram{channelID: channelID, target: target, encoder: enc, transcoding: transcoding, progress: p})
}

func (f *fakePlayoutSessions) AdmitProgram(_ string, _ playout.EncodePlan, transcoding bool) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.programCosts = append(f.programCosts, transcoding)
	return !f.denyProgram
}

func (f *fakePlayoutSessions) Attach(_ context.Context, channelID string, target playout.EncodePlan) (<-chan []byte, func(), error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, nil, f.err
	}
	f.attached = append(f.attached, attachRecord{channelID: channelID, target: target})
	if chunks := f.streams[target]; chunks != nil {
		return chunks, func() {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.detached++
		}, nil
	}
	if f.chunks == nil {
		f.chunks = make(chan []byte, 8)
	}
	return f.chunks, func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.detached++
	}, nil
}

func (f *fakePlayoutSessions) attachments() []attachRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]attachRecord(nil), f.attached...)
}

func (f *fakePlayoutSessions) Tune(ctx context.Context, request playout.TuneRequest) (playout.Presentation, error) {
	f.mu.Lock()
	f.tunes++
	f.mu.Unlock()
	chunks, release, err := f.Attach(ctx, request.ChannelID, request.Plan)
	return playout.Presentation{Stream: chunks, Release: release}, err
}

func (f *fakePlayoutSessions) OpenAsset(_ context.Context, _ string, _ playout.EncodePlan, rel string) (playout.Asset, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.opened = rel
	return f.asset, f.assetOK, nil
}

func (f *fakePlayoutSessions) AcquireAdmission(ctx context.Context, _ string) (playout.Admission, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return playout.Admission{Context: ctx}, f.admitErr
}

func (f *fakePlayoutSessions) StopChannel(channelID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = append(f.stopped, channelID)
}

// reports returns the ReportProgram calls seen so far.
func (f *fakePlayoutSessions) reports() []reportedProgram {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]reportedProgram(nil), f.reported...)
}

func (f *fakePlayoutSessions) detachCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.detached
}

func (f *fakePlayoutSessions) tuneCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.tunes
}

type playoutOpts struct {
	sessions   *fakePlayoutSessions
	token      string
	publicURL  string
	backend    string
	noSecret   bool
	skipConfig bool
}

func newPlayoutServer(t *testing.T, o playoutOpts) (*httptest.Server, store.Store) {
	t.Helper()
	st := openTestStore(t, t.TempDir()+"/playout.db")
	t.Cleanup(func() { _ = st.Close() })

	if o.token == "" {
		o.token = playoutToken
	}
	if o.publicURL == "" {
		o.publicURL = "http://loomarr.local:8080"
	}
	if o.backend == "" {
		o.backend = "internal"
	}

	opts := api.Options{
		Store: st,
		Auth:  api.NewTokenAuthorizer(adminToken),
		Log:   slog.New(slog.DiscardHandler),
	}
	if o.sessions != nil {
		opts.Playout = o.sessions
		opts.PlayoutObserver = o.sessions
	}
	if !o.noSecret {
		opts.PlayoutSecret = func() string { return o.token }
	}
	if !o.skipConfig {
		cfg := map[string]string{
			"server.public_url": o.publicURL,
			"playout.backend":   o.backend,
		}
		opts.LiveConfig = func(k string) string { return cfg[k] }
	}

	srv := httptest.NewTestServer(t, api.Router(slog.New(slog.DiscardHandler), opts))
	return srv, st
}

func getPlayout(t *testing.T, srv *httptest.Server, path string) *http.Response {
	t.Helper()
	resp, err := srv.Client().Get(playoutTestURL + path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func seedChannel(t *testing.T, st store.Store, id, name string, number int, backend string) {
	t.Helper()
	ch := store.Channel{Channel: schedule.Channel{ID: id, Name: name, Number: number, Status: schedule.StatusLive}}
	if backend != "" {
		ch.Policy.Playout = &schedule.PlayoutPolicy{Backend: backend}
	}
	if _, err := st.SaveChannel(context.Background(), ch); err != nil {
		t.Fatal(err)
	}
}

func setChannelStatus(t *testing.T, st store.Store, id string, status schedule.ChannelStatus) {
	t.Helper()
	ch, err := st.GetChannel(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	ch.Status = status
	if _, err := st.SaveChannel(context.Background(), ch); err != nil {
		t.Fatal(err)
	}
}

// --- Device auth (§11). These are the negative cases AGENTS.md §19 requires. ---

// EVERY playout route must reject a missing or wrong token. A television is the client, so
// there is no session to fall back on — the token is the only auth these routes have.
func TestPlayout_RejectsMissingOrWrongToken(t *testing.T) {
	srv, st := newPlayoutServer(t, playoutOpts{sessions: &fakePlayoutSessions{}})
	seedChannel(t, st, "ch1", "Channel One", 1, "internal")

	for _, path := range []string{
		"/v1/playout/tuner.m3u",
		"/v1/playout/stream/ch1",
		"/v1/playout/program/ch1",
		// The in-app HLS surface (§9.1 Watch, V46) authenticates exactly like the rest.
		"/v1/playout/hls/ch1/master.m3u8",
		"/v1/playout/hls/ch1/seg-0.ts",
	} {
		for name, q := range map[string]string{
			"no token":    "",
			"empty token": "?token=",
			"wrong token": "?token=nope",
			// A prefix of the real token must not pass — it would if the comparison were
			// truncating or prefix-based.
			"token prefix": "?token=" + playoutToken[:10],
		} {
			resp := getPlayout(t, srv, path+q)
			if resp.StatusCode != http.StatusNotFound {
				t.Errorf("%s with %s: status %d, want 404", path, name, resp.StatusCode)
			}
		}
	}
}

// A wrong token gets 404, NOT 401/403. These URLs are pasted into a media server's config and
// leak into logs and screenshots; an enumerable "real channel, wrong password" tells an
// attacker where to aim.
func TestPlayout_WrongTokenIsIndistinguishableFromNoRoute(t *testing.T) {
	srv, st := newPlayoutServer(t, playoutOpts{sessions: &fakePlayoutSessions{}})
	seedChannel(t, st, "ch1", "Channel One", 1, "internal")

	real := getPlayout(t, srv, "/v1/playout/stream/ch1?token=wrong")
	fake := getPlayout(t, srv, "/v1/playout/stream/does-not-exist?token=wrong")

	if real.StatusCode != fake.StatusCode {
		t.Errorf("a real channel with a bad token (%d) is distinguishable from a "+
			"nonexistent one (%d) — that is an enumeration oracle",
			real.StatusCode, fake.StatusCode)
	}
}

// No token configured ⇒ fail CLOSED. Serving streams unauthenticated because a secret failed
// to mint would silently remove the only auth these routes have.
func TestPlayout_NoConfiguredTokenRefusesEverything(t *testing.T) {
	srv, st := newPlayoutServer(t, playoutOpts{sessions: &fakePlayoutSessions{}, noSecret: true})
	seedChannel(t, st, "ch1", "Channel One", 1, "internal")

	for _, path := range []string{"/v1/playout/tuner.m3u", "/v1/playout/stream/ch1", "/v1/playout/program/ch1"} {
		// Even presenting an empty token — which would "match" an unset secret under a naive
		// equality check — must be refused.
		for _, q := range []string{"", "?token=", "?token=" + playoutToken} {
			if resp := getPlayout(t, srv, path+q); resp.StatusCode != http.StatusNotFound {
				t.Errorf("%s%s: status %d with no token configured, want 404",
					path, q, resp.StatusCode)
			}
		}
	}
}

// The admin API token must NOT work on playout routes, and vice versa. §11: they are separate
// secrets with opposite authority — playout_token grants no API access, api_token is
// break-glass admin.
func TestPlayout_AdminTokenIsNotAPlayoutToken(t *testing.T) {
	srv, st := newPlayoutServer(t, playoutOpts{sessions: &fakePlayoutSessions{}})
	seedChannel(t, st, "ch1", "Channel One", 1, "internal")

	if resp := getPlayout(t, srv, "/v1/playout/stream/ch1?token="+adminToken); resp.StatusCode != http.StatusNotFound {
		t.Errorf("the admin token authorized a playout route: status %d", resp.StatusCode)
	}
	// And the playout token grants no PRIVILEGED API access. POST /v1/channels is admin-only
	// (requireAdmin); GET /v1/channels is deliberately NOT, so asserting against a read route
	// would prove only that the route is public — which an earlier version of this test did.
	req, _ := http.NewRequest(http.MethodPost, playoutTestURL+"/v1/channels",
		strings.NewReader(`{"name":"Sneaky","number":99}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+playoutToken)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
		t.Errorf("the playout token created a channel (status %d) — §11 says it grants no "+
			"write of any kind", resp.StatusCode)
	}
}

// A playout path that matches no route must 404, not fall through to the SPA. Without the
// /v1/ prefix guard, ffmpeg would read index.html as a transport stream and report a
// corrupt stream naming neither the URL nor the typo.
func TestPlayout_UnknownPathDoesNotServeTheSPA(t *testing.T) {
	srv, _ := newPlayoutServer(t, playoutOpts{sessions: &fakePlayoutSessions{}})

	resp := getPlayout(t, srv, "/v1/playout/not-a-route?token="+playoutToken)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status %d, want 404", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if strings.Contains(strings.ToLower(string(body)), "<!doctype html") {
		t.Error("an unknown playout path served the SPA; ffmpeg would read HTML as MPEG-TS")
	}
}

// Prepared publications identify an immutable asset with one opaque path segment. Exercise the
// real router here: a handler-only test can inject a slash into PathValue manually even though the
// registered `{asset}` route would never match that URL, which is how the black-screen bug escaped.
func TestPlayoutHLS_PreparedAssetMatchesTheRegisteredRoute(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "prepared-*.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("prepared init"); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	f := &fakePlayoutSessions{
		asset:   playout.Asset{Content: file, Modified: time.Unix(1_000, 0), Immutable: true},
		assetOK: true,
	}
	srv, _ := newPlayoutServer(t, playoutOpts{sessions: f})
	const token = "p-aGVsbG8.mp4"

	resp := getPlayout(t, srv, "/v1/playout/hls/ch1/"+token+"?token="+playoutToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 from the registered single-segment asset route", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "video/mp4" {
		t.Fatalf("Content-Type = %q, want video/mp4 retained by the opaque token suffix", got)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil || string(body) != "prepared init" {
		t.Fatalf("body = %q, err=%v", body, err)
	}
	f.mu.Lock()
	opened := f.opened
	f.mu.Unlock()
	if opened != token {
		t.Fatalf("OpenAsset rel = %q, want %q", opened, token)
	}
}

// --- The stream endpoint ---

// The response must look like a LIVE stream, not a file. A Content-Length promises an end that
// never comes, and advertising ranges invites a seek that is meaningless here.
func TestPlayoutStream_LooksLikeALiveStreamNotAFile(t *testing.T) {
	f := &fakePlayoutSessions{}
	srv, st := newPlayoutServer(t, playoutOpts{sessions: f})
	seedChannel(t, st, "ch1", "Channel One", 1, "internal")

	// A real request would never end, so drive it with a cancellable context and stop after
	// the headers arrive.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		playoutTestURL+"/v1/playout/stream/ch1?token="+playoutToken, nil)

	// Feed one chunk so the handler writes something and we know it is streaming.
	f.chunks = make(chan []byte, 1)
	f.chunks <- []byte("ts-bytes")

	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "video/mp2t" {
		t.Errorf("Content-Type = %q, want video/mp2t", ct)
	}
	if cl := resp.Header.Get("Content-Length"); cl != "" {
		t.Errorf("Content-Length = %q — a live stream has no length", cl)
	}
	if ar := resp.Header.Get("Accept-Ranges"); ar != "none" {
		t.Errorf("Accept-Ranges = %q, want none (a seek is meaningless on a live stream)", ar)
	}
	if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}

	// The bytes must actually arrive — proving the handler flushes rather than buffering.
	buf := make([]byte, len("ts-bytes"))
	if _, err := io.ReadFull(resp.Body, buf); err != nil {
		t.Fatalf("no stream bytes: %v", err)
	}
	if string(buf) != "ts-bytes" {
		t.Errorf("got %q, want the session's chunk", buf)
	}
}

// A session can start its parent process successfully and still close before producing transport.
// The raw tuner route must not commit 200 until it has one real chunk, or Emby waits on a stream
// that has already failed and the response status lies about the channel being playable.
func TestPlayoutStream_SessionClosingBeforeFirstChunkFailsBeforeCommitting(t *testing.T) {
	f := &fakePlayoutSessions{chunks: make(chan []byte)}
	close(f.chunks)
	srv, st := newPlayoutServer(t, playoutOpts{sessions: f})
	seedChannel(t, st, "ch1", "Channel One", 1, "internal")

	resp, err := srv.Client().Get(playoutTestURL + "/v1/playout/stream/ch1?token=" + playoutToken)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 before a streaming 200 is committed", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); strings.Contains(ct, "video/mp2t") {
		t.Fatalf("failed startup advertised transport Content-Type %q", ct)
	}
}

// The full tuner plan may select a native HEVC broadcast that this machine cannot encode. A
// zero-byte full presentation is not terminal when the known-playable H.264/AAC baseline can
// produce transport: release the failed presentation, tune the baseline session, and commit 200
// only after its first real chunk arrives.
func TestPlayoutStream_FullPlanWithoutTransportFallsBackToBaseline(t *testing.T) {
	full := make(chan []byte)
	close(full)
	baseline := make(chan []byte, 1)
	baseline <- []byte("baseline-ts")
	close(baseline)
	f := &fakePlayoutSessions{streams: map[playout.EncodePlan]chan []byte{
		playout.PlanFull: full, playout.PlanBaseline: baseline,
	}}
	srv, st := newPlayoutServer(t, playoutOpts{sessions: f})
	seedChannel(t, st, "ch1", "Channel One", 1, "internal")

	resp := getPlayout(t, srv, "/v1/playout/stream/ch1?token="+playoutToken)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want baseline transport 200", resp.StatusCode)
	}
	if string(body) != "baseline-ts" {
		t.Fatalf("body = %q, want baseline transport", body)
	}
	if got, want := f.attachments(), []attachRecord{
		{channelID: "ch1", target: playout.PlanFull},
		{channelID: "ch1", target: playout.PlanBaseline},
	}; !slices.Equal(got, want) {
		t.Fatalf("tunes = %+v, want %+v", got, want)
	}
	if got := f.detachCount(); got != 2 {
		t.Fatalf("released presentations = %d, want 2", got)
	}
}

// A viewer disconnecting MUST detach. Nothing else reports it — the tuner path never
// re-requests — and a leaked viewer keeps the channel encoding forever.
func TestPlayoutStream_ClientDisconnectDetaches(t *testing.T) {
	f := &fakePlayoutSessions{chunks: make(chan []byte, 1)}
	srv, st := newPlayoutServer(t, playoutOpts{sessions: f})
	seedChannel(t, st, "ch1", "Channel One", 1, "internal")

	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		playoutTestURL+"/v1/playout/stream/ch1?token="+playoutToken, nil)
	f.chunks <- []byte("x")

	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1)
	if _, err := io.ReadFull(resp.Body, buf); err != nil {
		t.Fatalf("stream did not start: %v", err)
	}

	cancel() // the television goes away
	_ = resp.Body.Close()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if f.detachCount() > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("a disconnected viewer was never detached — the channel would encode forever")
}

// The session ending (encoder exited, channel stopped, viewer dropped for falling behind) must
// end the response rather than hanging the client.
func TestPlayoutStream_SessionEndEndsTheResponse(t *testing.T) {
	f := &fakePlayoutSessions{chunks: make(chan []byte, 1)}
	srv, st := newPlayoutServer(t, playoutOpts{sessions: f})
	seedChannel(t, st, "ch1", "Channel One", 1, "internal")

	f.chunks <- []byte("y")
	resp := getPlayout(t, srv, "/v1/playout/stream/ch1?token="+playoutToken)
	buf := make([]byte, 1)
	if _, err := io.ReadFull(resp.Body, buf); err != nil {
		t.Fatalf("stream did not start: %v", err)
	}

	close(f.chunks) // the encoder died

	done := make(chan struct{})
	go func() { _, _ = io.ReadAll(resp.Body); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Error("the response did not end when the session ended — the client hangs")
	}
}

// At capacity must be a 503 with Retry-After, not a generic error. The measured capacity is a
// safety boundary, so the response may suggest waiting or lowering quality but must never claim
// that raising a configured cap creates hardware headroom.
func TestPlayoutStream_AtCapacityIsActionable(t *testing.T) {
	srv, st := newPlayoutServer(t, playoutOpts{
		sessions: &fakePlayoutSessions{err: playout.ErrAtCapacity},
	})
	seedChannel(t, st, "ch1", "Channel One", 1, "internal")

	resp := getPlayout(t, srv, "/v1/playout/stream/ch1?token="+playoutToken)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status %d, want 503", resp.StatusCode)
	}
	if resp.Header.Get("Retry-After") == "" {
		t.Error("no Retry-After — a media server would retry immediately and hammer")
	}
	body, _ := io.ReadAll(resp.Body)
	detail := string(body)
	if !strings.Contains(detail, "wait") || !strings.Contains(detail, "lower quality") {
		t.Errorf("the 503 body does not give safe recovery choices: %s", body)
	}
	if strings.Contains(detail, "Raise") {
		t.Errorf("the 503 body promises capacity can be raised past measurement: %s", body)
	}
}

// Playout not running is a 501 with an explanation, not a 404 that looks like a wiring bug.
func TestPlayoutStream_NotRunningExplainsItself(t *testing.T) {
	srv, st := newPlayoutServer(t, playoutOpts{sessions: nil})
	seedChannel(t, st, "ch1", "Channel One", 1, "internal")

	resp := getPlayout(t, srv, "/v1/playout/stream/ch1?token="+playoutToken)
	if resp.StatusCode != http.StatusNotImplemented {
		t.Errorf("status %d, want 501", resp.StatusCode)
	}
}

// --- The tuner M3U ---

// tvg-id must match the guide's channel id, tvg-chno carries the operator's numbering, and the
// stream URL must be absolute + tokenized.
func TestPlayoutTuner_CarriesGuideCorrelationAttributes(t *testing.T) {
	srv, st := newPlayoutServer(t, playoutOpts{sessions: &fakePlayoutSessions{}})
	seedChannel(t, st, "ch1", "Channel One", 3, "internal")

	resp := getPlayout(t, srv, "/v1/playout/tuner.m3u?token="+playoutToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "mpegurl") {
		t.Errorf("Content-Type = %q, want an m3u type", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	got := string(body)

	if !strings.HasPrefix(got, "#EXTM3U\n") {
		t.Errorf("missing the M3U header: %q", got)
	}
	// tvg-id ties the entry to its XMLTV <channel id>; a mismatch means the channel plays
	// with an EMPTY guide, which is silent.
	if !strings.Contains(got, `tvg-id="ch1"`) {
		t.Errorf("no tvg-id — the channel would appear with no listings: %q", got)
	}
	if !strings.Contains(got, `tvg-chno="3"`) {
		t.Errorf("no tvg-chno — the media server would impose its own numbering: %q", got)
	}
	if !strings.Contains(got, "http://loomarr.local:8080/v1/playout/stream/ch1?token="+playoutToken) {
		t.Errorf("stream URL is not absolute + tokenized: %q", got)
	}
}

// A channel on the TUNARR backend must not appear in Loomarr's tuner, or the media server has
// two tuners offering the same channel and picks unpredictably — presenting as a channel that
// plays sometimes and not others.
func TestPlayoutTuner_ExcludesTunarrBackedChannels(t *testing.T) {
	srv, st := newPlayoutServer(t, playoutOpts{sessions: &fakePlayoutSessions{}})
	seedChannel(t, st, "mine", "Internal Channel", 1, "internal")
	seedChannel(t, st, "theirs", "Tunarr Channel", 2, "tunarr")

	resp := getPlayout(t, srv, "/v1/playout/tuner.m3u?token="+playoutToken)
	body, _ := io.ReadAll(resp.Body)
	got := string(body)

	if !strings.Contains(got, "Internal Channel") {
		t.Errorf("the internal channel is missing: %q", got)
	}
	if strings.Contains(got, "Tunarr Channel") {
		t.Errorf("a Tunarr-backed channel appeared in Loomarr's tuner — two tuners would "+
			"offer the same channel: %q", got)
	}
}

func TestPlayoutTuner_ExcludesChannelsThatAreOffAirOrNotManaged(t *testing.T) {
	srv, st := newPlayoutServer(t, playoutOpts{sessions: &fakePlayoutSessions{}})
	seedChannel(t, st, "live", "Live Channel", 1, "internal")
	seedChannel(t, st, "paused", "Paused Channel", 2, "internal")
	seedChannel(t, st, "detached", "Detached Channel", 3, "internal")
	seedChannel(t, st, "empty", "Empty Channel", 4, "internal")
	setChannelStatus(t, st, "paused", schedule.StatusPaused)
	setChannelStatus(t, st, "detached", schedule.StatusDetached)
	setChannelStatus(t, st, "empty", schedule.StatusEmpty)

	resp := getPlayout(t, srv, "/v1/playout/tuner.m3u?token="+playoutToken)
	body, _ := io.ReadAll(resp.Body)
	got := string(body)
	if !strings.Contains(got, "Live Channel") {
		t.Fatalf("live internal channel is missing: %q", got)
	}
	for _, name := range []string{"Paused Channel", "Detached Channel", "Empty Channel"} {
		if strings.Contains(got, name) {
			t.Errorf("%s was advertised by the internal tuner: %q", name, got)
		}
	}
}

func TestPlayoutTune_RejectsChannelsOutsideTheSurfableCatalog(t *testing.T) {
	sessions := &fakePlayoutSessions{err: errors.New("Tune must not be called")}
	srv, st := newPlayoutServer(t, playoutOpts{sessions: sessions})
	for i, tc := range []struct {
		id     string
		status schedule.ChannelStatus
	}{
		{id: "paused", status: schedule.StatusPaused},
		{id: "detached", status: schedule.StatusDetached},
		{id: "empty", status: schedule.StatusEmpty},
	} {
		seedChannel(t, st, tc.id, tc.id, i+1, "internal")
		setChannelStatus(t, st, tc.id, tc.status)
		for _, path := range []string{
			"/v1/playout/stream/" + tc.id,
			"/v1/playout/hls/" + tc.id + "/master.m3u8",
		} {
			resp := getPlayout(t, srv, path+"?token="+playoutToken)
			if resp.StatusCode != http.StatusNotFound {
				t.Errorf("%s status = %d, want 404", path, resp.StatusCode)
			}
		}
	}
	if got := sessions.tuneCount(); got != 0 {
		t.Fatalf("Tune called %d times for channels that cannot be played", got)
	}
}

// A channel with no explicit backend INHERITS the global (§15 nil-means-inherit). With the
// global set to tunarr, an unconfigured channel must not be served.
func TestPlayoutTuner_InheritsTheGlobalBackend(t *testing.T) {
	srv, st := newPlayoutServer(t, playoutOpts{sessions: &fakePlayoutSessions{}, backend: "tunarr"})
	seedChannel(t, st, "inherits", "Inherits Global", 1, "")  // no policy
	seedChannel(t, st, "opted-in", "Opted In", 2, "internal") // explicit override

	resp := getPlayout(t, srv, "/v1/playout/tuner.m3u?token="+playoutToken)
	body, _ := io.ReadAll(resp.Body)
	got := string(body)

	if strings.Contains(got, "Inherits Global") {
		t.Errorf("a channel with no backend set was served while the global is tunarr: %q", got)
	}
	// …but an explicit per-channel override still wins over the global.
	if !strings.Contains(got, "Opted In") {
		t.Errorf("an explicitly-internal channel was not served: %q", got)
	}
}

// A channel name is operator text and lands in an M3U attribute. A quote must not escape it.
func TestPlayoutTuner_QuotesInChannelNamesDoNotBreakTheM3U(t *testing.T) {
	srv, st := newPlayoutServer(t, playoutOpts{sessions: &fakePlayoutSessions{}})
	seedChannel(t, st, "ch1", `Bob's "Best" Movies`, 1, "internal")

	resp := getPlayout(t, srv, "/v1/playout/tuner.m3u?token="+playoutToken)
	body, _ := io.ReadAll(resp.Body)
	got := string(body)

	// Each attribute must remain a single well-formed key="value" pair: an unescaped quote
	// would terminate tvg-name early and the rest would parse as new attributes.
	for _, attr := range []string{"tvg-id=", "tvg-name=", "tvg-chno="} {
		if !strings.Contains(got, attr) {
			t.Errorf("missing %s after a name with quotes: %q", attr, got)
		}
	}
	if strings.Contains(got, `tvg-name="Bob's "Best" Movies"`) {
		t.Errorf("the quotes in the name were not escaped: %q", got)
	}
}
