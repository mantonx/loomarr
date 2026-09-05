package api_test

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/loomarr/loomarr/internal/api"
	"github.com/loomarr/loomarr/internal/playout"
)

func newDashboardServer(t *testing.T, sessions api.PlayoutObserver) *httptest.Server {
	t.Helper()
	st := openTestStore(t, t.TempDir()+"/dash.db")
	t.Cleanup(func() { _ = st.Close() })

	srv := httptest.NewServer(api.Router(slog.New(slog.DiscardHandler), api.Options{
		Store:           st,
		Auth:            api.NewTokenAuthorizer(adminToken),
		Log:             slog.New(slog.DiscardHandler),
		PlayoutObserver: sessions,
	}))
	t.Cleanup(srv.Close)
	return srv
}

func telemetry(t *testing.T, resp *http.Response) api.PlayoutTelemetry {
	t.Helper()
	var body api.PlayoutTelemetry
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body
}

// §19: encoder internals — which hardware families are in use, how close the box is to its
// channel ceiling — are operational detail, so the endpoint is admin-only. The member-facing
// dashboard renders a lockout explaining that; it must never be the thing that 403s.
func TestPlayoutTelemetry_RequiresAdmin(t *testing.T) {
	srv := newDashboardServer(t, &fakePlayoutSessions{})

	for _, tok := range []string{"", "not-the-admin-token"} {
		resp := do(t, srv, http.MethodGet, "/v1/playout/sessions", tok, "")
		if resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusForbidden {
			t.Errorf("token %q → %d, want 401/403", tok, resp.StatusCode)
		}
		_ = resp.Body.Close()
	}
}

func TestPlayoutTelemetry_ReportsSessionsAndLoad(t *testing.T) {
	srv := newDashboardServer(t, &fakePlayoutSessions{
		capacity: 4,
		stats: []playout.SessionStat{
			{ChannelID: "ch1", Viewers: 2, Encoder: "h264_nvenc", Hardware: true, Speed: 12.4, BufferedMS: 96_000, TranscodeCost: 1},
			{ChannelID: "ch2", Viewers: 1, Encoder: "libx264", Hardware: false, Speed: 1.4, BufferedMS: 12_000},
		},
	})

	resp := do(t, srv, http.MethodGet, "/v1/playout/sessions", adminToken, "")
	defer func() { _ = resp.Body.Close() }()
	got := telemetry(t, resp)

	if !got.Running {
		t.Error("running = false with a wired session manager")
	}
	if got.Active != 2 || got.Capacity != 4 {
		t.Errorf("load = %d/%d, want 2/4", got.Active, got.Capacity)
	}
	if got.ViewerActiveSessions != 2 || got.GraceIdleSessions != 0 {
		t.Errorf("viewer-active/grace-idle = %d/%d, want 2/0",
			got.ViewerActiveSessions, got.GraceIdleSessions)
	}
	if got.TranscodeCost != 1 {
		t.Errorf("transcode cost = %d, want 1 transcode plus one copy session", got.TranscodeCost)
	}
	if len(got.Sessions) != 2 {
		t.Fatalf("got %d sessions, want 2", len(got.Sessions))
	}
	// The single most actionable field: hardware vs software is the difference between four
	// concurrent streams and one.
	if !got.Sessions[0].Hardware || got.Sessions[1].Hardware {
		t.Errorf("hardware flags = %v/%v, want true/false",
			got.Sessions[0].Hardware, got.Sessions[1].Hardware)
	}
	if got.Sessions[1].Speed != 1.4 {
		t.Errorf("speed = %v, want the measured 1.4", got.Sessions[1].Speed)
	}
}

func TestPlayoutTelemetry_SeparatesViewerActiveAndGraceIdleSessions(t *testing.T) {
	srv := newDashboardServer(t, &fakePlayoutSessions{
		capacity: 4,
		stats: []playout.SessionStat{
			{ChannelID: "watched", Viewers: 1},
			{ChannelID: "warm", Viewers: 0},
		},
	})

	resp := do(t, srv, http.MethodGet, "/v1/playout/sessions", adminToken, "")
	defer func() { _ = resp.Body.Close() }()
	got := telemetry(t, resp)

	if got.Active != 2 {
		t.Fatalf("active = %d, want all 2 live sessions", got.Active)
	}
	if got.ViewerActiveSessions != 1 || got.GraceIdleSessions != 1 {
		t.Fatalf("viewer-active/grace-idle = %d/%d, want 1/1",
			got.ViewerActiveSessions, got.GraceIdleSessions)
	}
}

// ⚠ `running:false` is NOT the same as "no channels playing". On a Tunarr-only install the
// session list is legitimately empty, and a panel that cannot tell the two apart renders an
// empty table that reads as "every channel just died".
func TestPlayoutTelemetry_UnwiredIsNotRunning(t *testing.T) {
	srv := newDashboardServer(t, nil)

	resp := do(t, srv, http.MethodGet, "/v1/playout/sessions", adminToken, "")
	defer func() { _ = resp.Body.Close() }()
	got := telemetry(t, resp)

	if got.Running {
		t.Error("running = true with no session manager wired")
	}
	if got.Sessions == nil {
		t.Error("sessions is null; want an empty array — `null` and `[]` mean different things here")
	}
}

// An idle-but-wired install: playout is running, nothing is being watched.
func TestPlayoutTelemetry_RunningWithNoStreams(t *testing.T) {
	srv := newDashboardServer(t, &fakePlayoutSessions{capacity: 4})

	resp := do(t, srv, http.MethodGet, "/v1/playout/sessions", adminToken, "")
	defer func() { _ = resp.Body.Close() }()
	got := telemetry(t, resp)

	if !got.Running || got.Active != 0 {
		t.Errorf("running=%v active=%d, want running with 0 active", got.Running, got.Active)
	}
	if len(got.Sessions) != 0 {
		t.Errorf("got %d sessions, want none", len(got.Sessions))
	}
}
