package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/sse"

	"github.com/loomarr/loomarr/internal/events"
)

// EventSource is the subscribe surface the SSE stream needs (implemented by
// events.Bus). Nil ⇒ /v1/events is not mounted.
type EventSource interface {
	Subscribe() (<-chan events.Event, func())
}

// --- Frame payloads (§7 /v1/events, §8) ---
//
// ⚠ **These are the ONE definition of each frame's shape, and that is the point of typing
// them.** They used to be `map[string]string` literals built at ten publish sites, mirrored
// by hand into TypeScript interfaces — a shape defined twice, in two languages, with nothing
// binding them. That mirror had already drifted: `LlmPullEvent.percent` was missing on the
// frontend while the backend had been sending it all along (so the UI recomputed a worse
// version and showed nothing during "starting"), and `ChannelEvent` declared `id` while the
// backend sends `channelId` — invisible because an index signature swallowed it.
//
// Now they generate into api/openapi.yaml through sse.Register and the frontend imports them.
//
// ⚠ **huma derives the SSE event NAME from the Go TYPE of the payload** — sse.Message has no
// Event field. So each frame needs its own named type, every type must appear in eventTypeMap,
// and a type that is missing from the map ships a frame with NO name, which every
// EventSource listener keyed by that name silently ignores. TestEveryPublishedEventIsInTheEventTypeMap
// exists for exactly that failure.

// TitleEvent is a provisioning state change (§4).
type TitleEvent struct {
	Key   string `json:"key" example:"movie:tmdb:1111867"`
	State string `json:"state" enum:"wanted,requested,downloading,available,unavailable"`
	Name  string `json:"name,omitempty" example:"In Flames"`
}

// SuggestionEvent is one generation-progress frame (§8) so the workspace's progress strip
// advances searching→reasoning→scoring→done/failed live.
type SuggestionEvent struct {
	JobID string `json:"jobId"`
	Phase string `json:"phase" enum:"searching,reasoning,scoring,done,failed"`
	// ⚠ A real int now. It was stringified only because the payload was a flat
	// map[string]string — the frontend type carried a comment warning that declaring it a
	// number "would typecheck and then compare wrong at runtime". Typing the frame removes
	// the hazard rather than documenting it.
	//
	// Phases repeat: the model thinks, searches, then thinks again about what came back. The
	// round distinguishes "still working, third pass" from "stuck", which is why a long run
	// looks like it is progressing. 0 means outside the tool loop.
	Round int `json:"round"`
}

// ChannelEvent fires after a reconcile so the Channels pages update live (§9).
//
// ⚠ The field is `channelId`. The hand-written frontend mirror called it `id` and therefore
// read undefined forever; it only went unnoticed because the handler invalidates by prefix
// without reading the payload.
type ChannelEvent struct {
	ChannelID string `json:"channelId" example:"ch_abc123"`
	Status    string `json:"status"`
}

// JobEvent fires whenever a scheduled job's state changes (§18.1). Carries only the name —
// the Tasks page refetches GET /v1/jobs, keeping the backend the single source of timing truth.
type JobEvent struct {
	Name string `json:"name" example:"library-scan"`
}

// ActivityEvent announces that a Dashboard feed row was written (§12, V32).
//
// ⚠ **Deliberately EMPTY.** The frame says "something happened"; the page refetches
// GET /v1/activity, which is the truth on reconnect (§8). Carrying the row would invite a
// client to build the list from frames — and this bus drops frames for a slow subscriber by
// design, so that list would be silently missing entries.
type ActivityEvent struct{}

// HealthEvent announces that Current Health changed. The payload is intentionally empty: clients
// refetch the typed health endpoint so a dropped SSE frame cannot create partial client truth.
type HealthEvent struct{}

// FillerIngestEvent tracks a clip-fetch job (§10 V38b).
type FillerIngestEvent struct {
	JobID   string `json:"jobId"`
	Status  string `json:"status" enum:"starting,success,error"`
	Fetched int    `json:"fetched"`
	Skipped int    `json:"skipped"`
	Failed  int    `json:"failed"`
	// Sources that returned no clips AND no error — almost always a typo'd Archive id, which
	// answers 200 with nothing. Surfaced so "fetched: 0" has a reason attached.
	Empty int    `json:"empty"`
	Error string `json:"error,omitempty"`
}

// FillerSplitEvent tracks compilation-split detection (§10 V34). Detection runs minutes per
// file, so the POST returns a job id and the terminal frame hands the review UI its proposal id.
type FillerSplitEvent struct {
	JobID string `json:"jobId"`
	// ClipHash is the compilation's IDENTITY (§10 V38c). ⚠ This was `ClipPath` while every caller
	// already passed a hash into it — only the field and the wire key said "path". That is the same
	// naming drift that broke split confirm outright: the persisted proposal stored a path in a
	// field the hash-keyed lookup needed (§10 V51a).
	ClipHash   string `json:"clipHash"`
	Status     string `json:"status" enum:"running,success,error"`
	ProposalID string `json:"proposalId,omitempty"`
	Segments   int    `json:"segments"`
	Error      string `json:"error,omitempty"`
}

// FillerClipEvent reports where one clip is in the ingest pipeline (§10 V51b).
//
// ⚠ **A SELF-SUFFICIENT SNAPSHOT — any single frame fully describes where the clip is now.** The
// tempting alternative is a per-stage frame ("transcode finished"), which would make the client
// assemble the ladder from the sequence it received. This bus DROPS frames for a slow subscriber
// by design, so that ladder would be silently missing rungs — verbatim the failure the
// ActivityRecorded comment names. Every field needed to render the row travels in every frame,
// and GET /v1/filler/incoming is the truth on reconnect.
type FillerClipEvent struct {
	Hash  string `json:"hash"`
	Stage string `json:"stage" enum:"probe,transcode,split,screen,language,transcribe,tag,vision,admission,score"`
	// Status is how the CURRENT stage is going. `skipped` means the stage does not apply to this
	// clip in this install — a different fact from `done`, and re-evaluated on every pass.
	Status string `json:"status" enum:"queued,running,done,failed,skipped"`
	// Progress is 0-100 WITHIN the running stage, and **-1 when the stage cannot measure itself**.
	//
	// ⚠ -1 is a sentinel, not a small number: it must render as an indeterminate spinner, never as
	// a bar frozen at zero. Only the transcode stage has a real percentage (ffmpeg reports
	// out_time and the duration is known, so the ratio is exact). Whisper and an LLM turn are
	// single opaque calls, and a bar interpolated over them would be invented progress — the same
	// distinction `confidence == 0` draws between "no measurement" and "a measurement of none".
	Progress int `json:"progress"`
	// Disposition is the CLIP-level outcome. `review` is terminal for the pipeline even though it
	// is not terminal for the operator.
	Disposition string `json:"disposition" enum:"running,review,filed,rejected,dismissed"`
	// Reason is a stable CODE, never prose — the frontend owns the wording, the §11 refusal-code
	// precedent. Empty unless disposition is `rejected`.
	Reason string `json:"reason,omitempty"`
	// Detail is the measured fact behind the reason — "8.2s; the floor is 10.0s" — which is what
	// makes a reject arguable rather than an assertion.
	Detail string `json:"detail,omitempty"`
	Name   string `json:"name,omitempty"`
}

// LLMPullEvent tracks a model download (§8.1).
type LLMPullEvent struct {
	JobID  string `json:"jobId"`
	Model  string `json:"model"`
	Status string `json:"status" doc:"Ollama's own status strings pass through, plus Loomarr's terminal success/error"`
	// ⚠ Backend-computed 0-100, and **-1 on failure** — a sentinel, never a percentage to render.
	Percent int `json:"percent"`
	// Bytes for the layer downloading; 0 when unknown.
	Completed int64  `json:"completed"`
	Total     int64  `json:"total"`
	Error     string `json:"error,omitempty"`
}

// DatabaseEvent tracks the SQLite→PostgreSQL migration (§18, V11).
//
// A dropped frame costs a stale progress bar, never a wrong outcome — which matters more here
// than elsewhere, because the operator is watching a data migration and a UI that invented
// progress it had not been told about would be actively misleading.
type DatabaseEvent struct {
	Phase  string          `json:"phase" enum:"idle,migrating,verified,failed"`
	Parity string          `json:"parity" enum:"unknown,match,mismatch"`
	Tables []DatabaseTable `json:"tables,omitempty" doc:"Per-table progress; empty when idle"`
	Error  string          `json:"error,omitempty"`
}

// PlayoutEvent fires when a channel starts or stops encoding (§9.1).
//
// Session lifecycle only, NOT per ffmpeg progress sample: those arrive about once a second per
// stream, and republishing each would push several frames a second at every open browser for
// numbers that move by fractions. The count is a "something changed" signal — the dashboard
// re-reads GET /v1/playout/sessions, which owns the shape.
type PlayoutEvent struct {
	Active int `json:"active"`
}

// eventTypeMap binds each frame's SSE event NAME to its payload type. huma reads the name off
// the Go type, so this map is what makes `event: title` appear on the wire at all.
//
// ⚠ **Adding a frame means adding it here.** Publishing a type absent from this map emits a
// frame with no event name, so `es.addEventListener("whatever", …)` never fires and the
// feature simply looks broken. huma does print "unknown event type" + a stack to STDERR — but
// that is a raw dump that never reaches the app's slog, fails nothing, and returns 200 to a
// client that then waits forever. Guarded by TestEveryPublishedEventIsInTheEventTypeMap.
func eventTypeMap() map[string]any {
	return map[string]any{
		"title":         TitleEvent{},
		"channel":       ChannelEvent{},
		"suggestion":    SuggestionEvent{},
		"job":           JobEvent{},
		"activity":      ActivityEvent{},
		"health":        HealthEvent{},
		"filler_ingest": FillerIngestEvent{},
		"filler_split":  FillerSplitEvent{},
		"filler_clip":   FillerClipEvent{},
		"llm_pull":      LLMPullEvent{},
		"database":      DatabaseEvent{},
		"playout":       PlayoutEvent{},
	}
}

// registerEvents mounts GET /v1/events (§7, §8).
//
// ⚠ **Registered only when a bus is wired**, like the other nil-guarded registrars — but the
// schemaOnly escape keeps the frame schemas in api/openapi.yaml regardless, so the generated
// client always has the types. Dropping a route from the spec because a dependency happened to
// be nil at export time is a bug that has already happened here once (see export.go).
//
// Per §8 this stream is a LATENCY optimization and makes no delivery guarantees: a client that
// misses a frame re-reads the GET endpoints, which are the source of truth.
func (s *Server) registerEvents(api huma.API) {
	if s.events == nil && !s.schemaOnly {
		return
	}
	sse.Register(api, withRole(huma.Operation{
		OperationID: "events", Method: http.MethodGet, Path: "/v1/events",
		Summary: "Live update stream (SSE)",
		Description: "Any authenticated user. State changes as Server-Sent Events, each frame " +
			"named by its type. A latency optimization only (§8): every frame's information is " +
			"also readable from a GET, which stays the source of truth on reconnect, so a dropped " +
			"frame costs freshness and never correctness.",
		Tags: []string{"events"},
	}, RoleMember), eventTypeMap(), s.streamEvents)
}

// streamEvents fans the bus out to one subscriber until they disconnect.
//
// The payload arrives already typed — the publishers construct the DTOs (internal/app) — so
// this hands ev.Payload straight to huma, which picks the event name off its Go type. A frame
// whose type is not in eventTypeMap would go out unnamed; see the map's docstring.
func (s *Server) streamEvents(ctx context.Context, _ *struct{}, send sse.Sender) {
	if s.events == nil {
		return // schema-only registration; nothing to stream
	}
	ch, unsubscribe := s.events.Subscribe()
	defer unsubscribe()

	// An opening comment so proxies flush headers and the client knows the stream is live.
	_ = send.Comment("connected")

	for {
		select {
		case <-ctx.Done():
			return // client disconnected
		case <-s.shutdown:
			return // generation drain; the browser reconnects to the next one
		case ev, ok := <-ch:
			if !ok {
				return // bus closed
			}
			// Best-effort by design (§8). A write error means the client is gone; a payload
			// huma cannot encode is a bug in the publisher, and dropping the frame is
			// strictly better than tearing down every other subscriber's stream.
			_ = send(sse.Message{Data: ev.Payload})
		}
	}
}
