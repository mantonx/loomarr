package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/loomarr/loomarr/internal/playout"
)

// The dashboard's telemetry surface (§12, V16).
//
// ⚠ TWO CHANNELS, ONE TRUTH. `GET /v1/playout/sessions` is the source of truth; the `playout`
// SSE frame is a LATENCY optimization that carries the same snapshot. That split is the bus's
// documented discipline (§8, events/bus.go): "a dropped event is a latency bug, not a
// correctness bug — the store is always the source of truth on reconnect". A dashboard that
// only listened to SSE would show an empty transcoding panel after any reconnect, and one that
// only polled would lag a stream start by the poll interval.
//
// So the frame carries the WHOLE snapshot rather than a delta. Deltas would make a dropped
// frame corrupting rather than merely stale — the bus drops for slow subscribers by design, so
// any delta protocol over it would need a resync path, which is the polling endpoint again.

// PlayoutTelemetry is the live encoder picture the dashboard renders.
type PlayoutTelemetry struct {
	// Sessions is one row per channel currently encoding, sorted by channel id.
	Sessions []playout.SessionStat `json:"sessions"`
	// Active and Capacity are the "2 / 4" load line. Capacity is the effective measured admission
	// bound after any operator safety cap and VRAM shading — the point at which a new viewer is
	// refused rather than someone else's channel being evicted (§9.1).
	Active   int `json:"active"`
	Capacity int `json:"capacity"`
	// ViewerActiveSessions and GraceIdleSessions split Active by demand. A zero-viewer session is
	// retained only inside its warm grace interval and may be reclaimed to admit foreground work.
	ViewerActiveSessions int `json:"viewerActiveSessions"`
	GraceIdleSessions    int `json:"graceIdleSessions"`
	// TranscodeCost is the number of live sessions currently consuming video-transcode slots.
	// Copy sessions remain visible in Active but contribute zero here.
	TranscodeCost int `json:"transcodeCost"`
	// Running reports whether internal playout is wired at all. False on a Tunarr-only
	// install, where an empty session list means "not our job" rather than "nothing playing" —
	// a distinction the panel has to draw or it reads as a fault.
	Running bool `json:"running"`
}

type playoutTelemetryOutput struct {
	Body PlayoutTelemetry
}

// registerDashboard mounts the dashboard's data routes (§12).
func (s *Server) registerDashboard(api huma.API) {
	huma.Register(api, withRole(huma.Operation{
		OperationID: "get-playout-telemetry", Method: http.MethodGet, Path: "/v1/playout/sessions",
		Summary: "Live internal-playout encoder telemetry (admin)",
		Description: "Per-channel encoder state for the dashboard: viewers, resolved encoder, " +
			"realtime speed and buffer-ahead. Admin only. The `playout` SSE frame carries the " +
			"same snapshot for liveness; this endpoint is the source of truth on reconnect.",
		Tags: []string{"dashboard"},
	}, RoleAdmin), s.getPlayoutTelemetry)
}

// getPlayoutTelemetry snapshots the live encoders.
//
// ADMIN ONLY. This is operational detail about the machine — encoder families, realtime speed,
// how close the box is to its channel ceiling — and §11 keeps machine internals to admins. The
// member-facing dashboard shows a lockout explaining that, never a 403 wall (V16's gate).
func (s *Server) getPlayoutTelemetry(ctx context.Context, _ *struct{}) (*playoutTelemetryOutput, error) {
	out := &playoutTelemetryOutput{}
	out.Body = s.playoutTelemetry(time.Now())
	return out, nil
}

// playoutTelemetry builds the snapshot. Shared by the GET and the SSE publisher so the two can
// never disagree about what a session looks like.
func (s *Server) playoutTelemetry(now time.Time) PlayoutTelemetry {
	if s.playoutObserver == nil {
		// Not wired: a Tunarr-only install, or playout disabled. `Running:false` with an empty
		// list, so the panel can say "Tunarr streams these channels" instead of rendering an
		// empty table that looks like every channel just died.
		return PlayoutTelemetry{Sessions: []playout.SessionStat{}}
	}
	stats := s.playoutObserver.Stats(now)
	if stats == nil {
		// A non-nil empty slice: `null` and `[]` are different things to a client, and the
		// difference here is "no data" versus "no streams", which the panel renders differently.
		stats = []playout.SessionStat{}
	}
	viewerActive, transcodeCost := 0, 0
	for _, stat := range stats {
		if stat.Viewers > 0 {
			viewerActive++
		}
		transcodeCost += stat.TranscodeCost
	}
	return PlayoutTelemetry{
		Sessions:             stats,
		Active:               len(stats),
		Capacity:             s.playoutObserver.Capacity(),
		ViewerActiveSessions: viewerActive,
		GraceIdleSessions:    len(stats) - viewerActive,
		TranscodeCost:        transcodeCost,
		Running:              true,
	}
}
