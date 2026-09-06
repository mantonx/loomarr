package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/loomarr/loomarr/internal/filler"
	"github.com/loomarr/loomarr/internal/store"
	"github.com/loomarr/loomarr/internal/taxonomy"
)

// registerFiller mounts /v1/filler* (§7/§10). List is visible to any
// authenticated user; tag edit, sync, and the AI-tagging job require admin
// (filler ingestion is an admin concern, §7).
func (s *Server) registerFiller(api huma.API) {
	s.registerFillerScreening(api)
	huma.Register(api, withRole(huma.Operation{
		OperationID: "list-filler", Method: http.MethodGet, Path: "/v1/filler",
		Summary: "List filler clips",
		Description: "Filter by kind/era/audience/category, search across name/brand/on-screen-text/tags, sort, and page (§10 V51d). " +
			"⚠ **Paged by default — `limit` is 100 and caps at 500.** The unbounded read this replaced hard-fails past ~65k clips on " +
			"Postgres (one bind parameter per clip in the tag batch, against a 65535-parameter statement cap). `total` is the number " +
			"MATCHING THE FILTER, counted in SQL through the same predicate as the rows, so a pager can say 'showing 61-120 of 1,204' " +
			"without the two disagreeing. " +
			"Held clips and composites are excluded unless asked for, because neither is in the playable catalog.",
		Tags: []string{"filler"},
	}, RoleMember), s.listFiller)

	huma.Register(api, withRole(huma.Operation{
		OperationID: "filler-pool", Method: http.MethodGet, Path: "/v1/filler/pool",
		Summary: "Catalog-wide filler health",
		Description: "How well the catalog can fill breaks across the whole install (§10 V35) — the Filler page's pool strip. " +
			"Counts what exists (clips, commercials, duration-eligible, untagged) and lists every live channel's coverage, WORST FIRST. " +
			"The per-channel answers are the SAME `Coverage` computation `/v1/channels/{id}/filler/coverage` returns, called once per channel, " +
			"so this page and the channel page cannot disagree — there is no aggregate ladder to drift from the real one. " +
			"Read-only, so any authenticated user may call it.",
		Tags: []string{"filler"},
	}, RoleMember), s.fillerPool)

	huma.Register(api, withRole(huma.Operation{
		OperationID: "filler-readiness", Method: http.MethodGet, Path: "/v1/filler/readiness",
		Summary:     "Filler readiness and next action",
		Description: "One server-owned operational projection across acquisition limits, durable runs, pipeline ownership, playable inventory, and worst-first channel coverage (§10 V59). Clients render the typed next action rather than reconstructing priorities from counters.",
		Tags:        []string{"filler"},
	}, RoleMember), s.fillerReadiness)

	huma.Register(api, withRole(huma.Operation{
		OperationID: "tag-filler-clip", Method: http.MethodPatch, Path: "/v1/filler/tags",
		Summary: "Edit a clip's classification", Description: "Admin only. Corrects kind, era, audience, brand, and directly asserted taxonomy tags. The clip is identified by content `hash` in the body (§10 V45a).", Tags: []string{"filler"},
	}, RoleAdmin), s.patchFillerClip)

	huma.Register(api, withRole(huma.Operation{
		OperationID: "sync-filler", Method: http.MethodPost, Path: "/v1/filler/sync",
		Summary: "Sync the clip catalog from the media server", Description: "Admin only (§10).",
		Tags: []string{"filler"},
	}, RoleAdmin), s.syncFiller)

	huma.Register(api, withRole(huma.Operation{
		OperationID: "tag-filler", Method: http.MethodPost, Path: "/v1/filler/tag",
		Summary: "AI-tag untagged clips", Description: "Admin only. Text-signal classification (§10).",
		Tags: []string{"filler"},
	}, RoleAdmin), s.tagFiller)

	huma.Register(api, withRole(huma.Operation{
		OperationID: "rewind-filler-clip", Method: http.MethodPost, Path: "/v1/filler/rewind",
		Summary: "Re-run a clip's ingest pipeline from one stage",
		Description: "Admin only. Resets the selected stage and every later stage, durably forces the selected stage to run, and invalidates only derived data safe for that stage to replace. " +
			"Re-running transcode requires force because the original bytes are no longer available and another encode loses quality.",
		Tags: []string{"filler"},
	}, RoleAdmin), s.rewindFillerClip)

	huma.Register(api, withRole(huma.Operation{
		OperationID: "retry-filler-failures", Method: http.MethodPost, Path: "/v1/filler/retry",
		Summary:     "Retry eligible ingest failures",
		Description: "Admin only. Retries one or a bounded set of execution failures at the server-selected failed stage, preserving completed upstream work. Content decisions use restore instead.",
		Tags:        []string{"filler"},
	}, RoleAdmin), s.retryFillerFailures)

	huma.Register(api, withRole(huma.Operation{
		OperationID: "ingest-filler", Method: http.MethodPost, Path: "/v1/filler/ingest",
		Summary: "Download clips into the drop-folder (admin; needs the vendored yt-dlp + ffmpeg)",
		Tags:    []string{"filler"},
	}, RoleAdmin), s.ingestFiller)

	huma.Register(api, withRole(huma.Operation{
		OperationID: "discover-filler", Method: http.MethodGet, Path: "/v1/filler/discover",
		Summary: "Search archive.org for clips to add",
		Description: "Admin only (§10, V33). Searches archive.org by keyword and returns candidates " +
			"WITHOUT downloading anything — the operator is deciding what to fetch, and making them " +
			"download to find out would defeat the point. Results carry only what the source's search " +
			"index knows: no tags, no licence (see below), and no duration or quality — those cost a " +
			"metadata call per row, so ask GET /v1/filler/discover/stats for the rows you are showing. " +
			"Feed an id to POST /v1/filler/ingest to actually fetch it. Synchronous: one upstream " +
			"request, no job to poll.",
		Tags: []string{"filler"},
	}, RoleAdmin), s.discoverFiller)

	huma.Register(api, withRole(huma.Operation{
		OperationID: "discover-filler-stats", Method: http.MethodGet, Path: "/v1/filler/discover/stats",
		Summary: "Runtime and quality for specific search results",
		Description: "Admin only (§10, V35). Fills in the duration and quality a search deliberately " +
			"omits. ⚠ This is ONE UPSTREAM CALL PER ID and archive.org is slow (median 1.8s, and it " +
			"gets slower if asked for more at once), so send only the ids currently on screen — a " +
			"full page of 25 measured 22.6s. An id archive.org cannot answer for is ABSENT from the " +
			"response rather than zero: 0 would be indistinguishable from a genuinely empty clip.",
		Tags: []string{"filler"},
	}, RoleAdmin), s.discoverFillerStats)

	huma.Register(api, withRole(huma.Operation{
		// ⚠ The clip is a QUERY parameter, not a path segment, and that is forced rather than
		// stylistic. Clip identity is a PATH (§9.1 — `ads/cereal.mp4`), so `/v1/filler/{id}/fit`
		// collides with every literal segment under /v1/filler: Go's ServeMux refuses to register
		// it beside `/v1/filler/splits/{proposalId}` ("both match some paths, like
		// /v1/filler/splits/fit"), and it panics at ROUTE REGISTRATION, i.e. at boot. A query
		// parameter also avoids double-encoding a path that legitimately contains slashes.
		OperationID: "clip-channel-fit", Method: http.MethodGet, Path: "/v1/filler/fit",
		Summary: "Where this clip lands on each channel's ladder",
		Description: "The override picker's per-channel note (§10 V35). For every channel: which rung " +
			"of the §10 ladder this clip would be drawn from, or WHY it would never be picked, plus the " +
			"operator's pin/exclude state. Computed from the SAME predicates assembly uses, through the " +
			"same per-channel selection as the pod preview — a note that disagrees with what airs is a " +
			"confident wrong answer about why a channel looks the way it does. Read-only. ⚠ A PINNED clip " +
			"reports no rung and no reason: a pin is placed ahead of the ladder, so it has none. ⚠ Every " +
			"channel is listed, including paused ones — pinning to a paused channel is a decision that " +
			"takes effect when it resumes.",
		Tags: []string{"filler", "channels"},
	}, RoleMember), s.clipChannelFit)

	// Compilation splitting (§10, V34). Propose is a job (detection runs minutes
	// per file); the proposal read and the confirm are synchronous. All admin:
	// these routes write the catalog, which is the same trust level as ingest.
	huma.Register(api, withRole(huma.Operation{
		OperationID: "split-filler", Method: http.MethodPost, Path: "/v1/filler/split",
		Summary: "Propose splits for a compilation clip",
		Description: "Admin only (§10, V34). The clip is identified by `path` in the body (§10 V45a). Runs " +
			"detection — chapters → blackdetect/silencedetect → transcript rescue for over-long segments — " +
			"as a background job (progress on /v1/events as filler_split frames), producing a PERSISTED " +
			"split proposal. Nothing enters the catalog: review is not optional, because detection quality " +
			"is a property of the source.",
		Tags: []string{"filler"},
	}, RoleAdmin), s.splitFiller)

	huma.Register(api, withRole(huma.Operation{
		OperationID: "get-filler-split-operation", Method: http.MethodGet, Path: "/v1/filler/split-operations/{jobId}",
		Summary:     "Read split-detection progress and outcome",
		Description: "Admin only. Authoritative reconnect state for the jobId returned by POST /v1/filler/split. A successful operation carries the proposalId to read from /v1/filler/splits/{proposalId}; SSE is latency-only.",
		Tags:        []string{"filler"},
	}, RoleAdmin), s.getFillerSplitOperation)

	huma.Register(api, withRole(huma.Operation{
		OperationID: "get-filler-split", Method: http.MethodGet, Path: "/v1/filler/splits/{proposalId}",
		Summary: "Read a split proposal",
		Description: "Admin only (§10, V34). The review surface's source of truth on SSE reconnect — " +
			"the same pattern as /v1/proposals/{id}.",
		Tags: []string{"filler"},
	}, RoleAdmin), s.getFillerSplit)

	huma.Register(api, withRole(huma.Operation{
		OperationID: "confirm-filler-split", Method: http.MethodPost, Path: "/v1/filler/splits/{proposalId}/confirm",
		Summary: "Commit a reviewed split",
		Description: "Admin only (§10, V34). The body is the operator's confirmed cut list — the " +
			"proposal as returned, possibly edited (cuts moved/merged/dropped; era suggestions accepted " +
			"or rejected; dedup-flagged segments kept or skipped). Only now do segments become catalog " +
			"clips: cut with ffmpeg stream copy, and the original compilation row removed — its identity " +
			"is a path that now means twenty clips, not one.",
		Tags: []string{"filler"},
	}, RoleAdmin), s.confirmFillerSplit)

	// The three clip byte routes (V30/V35/V39). Member-visible, matching `list-filler`: the
	// catalog listing is visible to any authenticated user, and an asset is strictly less
	// information than the row it decorates. NOT public like the channel icon — that one is open
	// so Tunarr can fetch it machine-to-machine, a reason that does not apply here.
	//
	// ⚠ `media`, never `preview`: /v1/channels/{id}/filler/coverage and PodAdapter.Preview
	// already mean "the pod pool a channel would get", as JSON.
	//
	// ⚠ **`filler-thumb` and `filler-hover` were RETIRED in V52 phase 8.** A clip's still and
	// hover loop are image-service images (§22): they are content-addressed, carry a srcset and a
	// ThumbHash, and are served by /v1/images/{hash} under an honest immutable cache. Two routes
	// serving the same two assets from a second store, with none of that, is the duplication §22
	// exists to remove. `filler-media` stays — the clip's OWN bytes are not an image and the image
	// service has no opinion about them.
	//
	// It keeps the stdlib (w, r) signature because http.ServeContent answers Range,
	// If-Modified-Since and 206 in one call — see rawop.go.
	//
	// ⚠ Media type ranges, not a single type: this route serves whatever the clip's extension
	// maps to in the `mediaTypes` allowlist (video/mp4, video/x-matroska, audio/mpeg, …). Naming
	// one of them would be a documented lie, and enumerating eleven says less than the range does.
	rawOp[hashInput](api, bytesResponse(huma.Operation{
		OperationID: "filler-media", Method: http.MethodGet, Path: "/v1/filler/media/{hash}",
		Summary: "A clip's own bytes",
		Description: "Any authenticated user. The clip itself, so an operator can watch one before " +
			"deciding about it. Range-capable, so a <video> element can seek. The exact media type " +
			"comes from a short server-side allowlist keyed by extension.",
		Tags: []string{"filler"},
	}, "The clip's own bytes.", "video/*", "audio/*"), RoleMember, s.serveFillerMedia)
}

type rewindFillerClipInput struct {
	Body struct {
		Hash  string `json:"hash" minLength:"1"`
		From  string `json:"from" enum:"probe,transcode,split,screen,language,transcribe,tag,vision,admission,score"`
		Force bool   `json:"force,omitempty"`
	}
}

func (s *Server) rewindFillerClip(ctx context.Context, in *rewindFillerClipInput) (*struct{}, error) {
	rewinder, ok := s.filler.(FillerRewinder)
	if !ok {
		return nil, huma.Error501NotImplemented("the filler pipeline is not configured")
	}
	if _, err := s.store.GetClip(ctx, in.Body.Hash); errors.Is(err, store.ErrNotFound) {
		return nil, errNotFound("Clip not found", "That clip is no longer in the catalog.")
	} else if err != nil {
		return nil, err
	}
	err := rewinder.Rewind(ctx, in.Body.Hash, filler.StageID(in.Body.From), in.Body.Force)
	switch {
	case errors.Is(err, filler.ErrUnknownStage):
		return nil, errUnprocessable("Unknown pipeline stage", "Choose a stage from the clip's pipeline ladder.")
	case errors.Is(err, filler.ErrTranscodeNeedsForce):
		return nil, errConflict("Re-encoding needs confirmation", err.Error())
	case err != nil:
		return nil, err
	default:
		return &struct{}{}, nil
	}
}

type retryFillerFailuresInput struct {
	Body struct {
		Hashes []string `json:"hashes" minItems:"1" maxItems:"50" doc:"Eligible clip hashes; duplicate and non-retryable rows are reported without mutation"`
	}
}

type retryFillerFailuresOutput struct {
	Body struct {
		Retried      int `json:"retried"`
		NotRetryable int `json:"notRetryable"`
	}
}

func (s *Server) retryFillerFailures(ctx context.Context, in *retryFillerFailuresInput) (*retryFillerFailuresOutput, error) {
	rewinder, ok := s.filler.(FillerRewinder)
	if !ok {
		return nil, huma.Error501NotImplemented("the filler pipeline is not configured")
	}
	out := &retryFillerFailuresOutput{}
	seen := make(map[string]struct{}, len(in.Body.Hashes))
	for _, hash := range in.Body.Hashes {
		if _, duplicate := seen[hash]; duplicate {
			out.Body.NotRetryable++
			continue
		}
		seen[hash] = struct{}{}
		err := rewinder.RetryFailure(ctx, hash)
		if errors.Is(err, filler.ErrPipelineNotRetryable) {
			out.Body.NotRetryable++
			continue
		}
		if err != nil {
			return nil, err
		}
		out.Body.Retried++
	}
	return out, nil
}

// ClipDTO is the API view of a filler clip (§10). Identity is the content HASH (V38c/V45a) — the
// store has keyed on hash since V38c, and V45a makes the wire identity the hash too, so a
// slash-bearing path never crosses the API (the source of the routing/proxy 404 class).
type ClipDTO struct {
	// Hash is the clip's IDENTITY (its sparse content hash, V38c) — what every clip-addressing route
	// takes (tags, split, and the byte routes). Hex, no slashes, so it is a clean URL/route id.
	Hash string `json:"hash"`
	// TunarrProgramID is informational since §9.1: it exists for Tunarr filler-lists and is
	// TunarrProgramID is informational since §9.1: it exists for Tunarr filler-lists and is
	// empty on an install with no Tunarr. NOT an identity — clients must key on Path.
	TunarrProgramID string `json:"tunarrProgramId,omitempty"`
	Name            string `json:"name"`
	Kind            string `json:"kind" enum:"commercial,bumper,station_id,psa,trailer,interstitial"`
	Era             int    `json:"era,omitempty"`
	Audience        string `json:"audience,omitempty" enum:"kids,family,general,late_night,"`
	GeographicScope string `json:"geographicScope,omitempty" enum:"unknown,national,local"`
	Country         string `json:"country,omitempty" doc:"ISO 3166-1 alpha-2 country when grounded"`
	Market          string `json:"market,omitempty" doc:"Local broadcast market when geographicScope is local"`
	Network         string `json:"network,omitempty"`
	Station         string `json:"station,omitempty"`
	AirDate         string `json:"airDate,omitempty" doc:"Grounded broadcast date in YYYY-MM-DD form"`
	GeoEvidence     string `json:"geographyEvidence,omitempty" doc:"Literal source evidence or operator attribution"`
	// Category is the DERIVED product-leaf shadow of Tags (§10 V45a) — kept for readers that show a
	// single primary category; `Tags` is the full taxonomy set and the source of truth.
	Category string `json:"category,omitempty" doc:"Derived primary product-leaf tag; the full set is in tags (§10 V45a)"`
	// Tags is the clip's taxonomy tag set (§10 V45a) — the full leaf+rollup expansion, so a client can
	// show every tag and a curation rule can match any ancestor. Empty for a clip not yet tagged.
	Tags []string `json:"tags,omitempty" doc:"The clip's taxonomy tags (leaf + rolled-up ancestors); category is the derived primary product leaf (§10 V45a)"`
	// AssertedTags is what the classifier/operator actually chose. Editors round-trip this field;
	// Tags includes derived ancestors and must never be sent back as assertions.
	AssertedTags []string `json:"assertedTags,omitempty" doc:"Directly asserted taxonomy tags; excludes derived rollups and is the set clip editors should round-trip"`
	DurationMs   int64    `json:"durationMs"`
	Source       string   `json:"source,omitempty"`
	// Quality is the resolution label ("1080p", "480p"); "" for an audio-only clip or one
	// scanned before the column existed. Shipped in migration 00014 and surfaced here by V28 —
	// it existed in the store for two phases with no way to see it.
	// ⚠ The `doc` string is a CLIENT-VISIBLE contract (it ships in api/openapi.yaml and the
	// generated TS). It used to say "never affects pod selection"; V17c made that false by
	// adding an opt-in floor, so it was amended in the same PR rather than left as a lie the
	// generated client repeats.
	Quality string `json:"quality,omitempty" doc:"Resolution label from the probed video height (1080p, 480p). Display-only by default; affects selection only when the filler.min_quality floor is set, which is off unless an operator turns it on."`
	// ThumbImage / HoverImage are the clip's still and hover loop, as image-service records
	// (§22) — content-addressed, with a width ladder, a ThumbHash and honest immutable caching.
	//
	// ⚠ **They REPLACED `thumbnail`/`preview` in V52 phase 8; those two string fields and the
	// routes behind them are gone.** Phase 6 carried both while the adoption job caught up, which
	// was the right shape for a migration window and the wrong thing to keep: two ways to address
	// one asset is the duplication §22 exists to remove, and the legacy pair carried none of what
	// the image service provides.
	//
	// ⚠ **Absence is ORDINARY and the client must treat it that way** — it is not an error and not
	// a reason to draw a placeholder. A clip whose artwork the adoption job has not reached yet has
	// neither field, and so does one where ffmpeg never ran (the entire catalog on a Tunarr-backed
	// install). The card's no-frame layout is the designed answer for both, and it is what shipped
	// before extracted frames existed.
	//
	// The forward pointer on the clip ROW is still `thumbnail`/`preview` — the render pipeline
	// writes files under FILLER_DIR and the adoption job converts them (§22, phase 6). Those
	// columns are the seam between the two, deliberately not exposed here: a path relative to a
	// cache directory is not something a client can do anything with now that the routes are gone.
	ThumbImage *ImageDTO `json:"thumbImage,omitempty" doc:"Image-service record for the still; absent until the adoption job has run, or when no frame was extracted"`
	HoverImage *ImageDTO `json:"hoverImage,omitempty" doc:"Image-service record for the hover loop; absent until adopted, or when no animation was rendered"`
	// PlayCount / LastPlayedAt count airings on INTERNAL playout only.
	//
	// ⚠ PlaysCounted is what stops this being a lie. A Tunarr-backed channel airs its filler
	// through Tunarr, which never reports back, so those clips sit at 0 forever — and "0
	// plays" and "we cannot see plays here" are different facts. The UI must render the
	// second as "not counted", never as a zero.
	PlayCount    int64  `json:"playCount"`
	LastPlayedAt string `json:"lastPlayedAt,omitempty" doc:"RFC3339; absent if never played (or not counted)"`
	PlaysCounted bool   `json:"playsCounted" doc:"False when this install cannot observe airings (Tunarr-backed playout) — render as 'not counted', not as 0"`
	AITagged     bool   `json:"aiTagged"`
	Tagged       bool   `json:"tagged" doc:"Whether the clip has all match tags (era+audience+category)"`
	// SuggestedEra is an AI-proposed era the validator refused to persist (§10 era
	// grounding, V34): the year appears nowhere in the clip's text signals, so it is a
	// question for the operator, not a tag. Confirming = PATCHing era (clears this);
	// 0/absent = no suggestion. Nothing in pod matching ever reads it.
	SuggestedEra int `json:"suggestedEra,omitempty" doc:"AI-proposed era NOT persisted as a tag because the year is absent from the clip's text signals (§10). Confirm by PATCHing era; 0 = no suggestion."`
	// IsComposite marks a recorded break — many adverts in one file — which is NOT airable (§10 V45).
	// The client renders it as a container (the "COMPOSITE · NOT AIRABLE" row that expands to its
	// segments), never as a playable clip. Its segments (ParentHash == this clip's identity) are the
	// airable clips.
	IsComposite bool `json:"isComposite,omitempty" doc:"A recorded break (many ads in one file), not airable — render as a container whose segments are the airable clips (§10 V45)"`
	// ParentHash is the identity of the composite this clip was split out of (§10 V45), or absent for
	// a clip with no parent. The lineage link: a segment card shows "from <break>", and a composite's
	// segments are fetched by this value.
	ParentHash string `json:"parentHash,omitempty" doc:"The composite this clip was split from; absent for a non-segment clip (§10 V45)"`
	// Brand is the advertiser the clip is FOR (§10 V44) — grounded, so absent is the common and
	// honest case, never a gap to fill in with a guess.
	Brand string `json:"brand,omitempty" doc:"The advertiser, when it appears literally in a text or visual signal (§10 V44). Absent means no GROUNDED brand — never inferred."`
	// Confidence is the grounding-capped tagging score (§10 V38), 0-100. 0 means never scored,
	// which is not the same as scored-low: it can never clear an auto-file threshold.
	Confidence int `json:"confidence,omitempty" doc:"Tagging confidence 0-100; 0 = never scored (§10 V38), which is distinct from a low score"`
	// Held marks a clip recorded but NOT yet in the playable catalog (§10 V38).
	//
	// ⚠ Shipped in the same change as the `includeHeld` parameter, never after it. A client that
	// can ASK for held clips and cannot TELL which ones they are renders an unreviewed clip
	// identically to a filed one — the parameter alone would be worse than neither.
	Held bool `json:"held,omitempty" doc:"Waiting for review in Incoming, not in the playable catalog (§10 V38). Only ever true when includeHeld or heldOnly was passed."`
	// Language has THREE states and a client must not collapse them (§10 V40):
	// absent = NOT YET CHECKED (the detection job has not reached this clip; never a fault),
	// "none" = CHECKED and there is no speech to judge (a wordless visual spot, always kept),
	// "en"/… = CHECKED, and this is what was heard.
	Language string `json:"language,omitempty" doc:"Absent = not yet checked; 'none' = checked and wordless; otherwise the detected language (§10 V40). The first two are different facts."`
	// VisionTagged records that a VISION pass contributed to this clip's tags, distinct from
	// AITagged (text-only). Different trust and different cost, so a reviewer wants both.
	VisionTagged bool `json:"visionTagged,omitempty" doc:"A vision pass over keyframes contributed tags, distinct from aiTagged (text-only) (§10 V44)"`
	// License is the licence URL the SOURCE declared.
	//
	// ⚠ **"" means UNKNOWN, never "public domain".** About 92% of archive.org items declare no
	// licence at all, so an empty value is the COMMON case and carries no permission. This field
	// is a record of what a source claimed, not a rights determination, and it never gates
	// selection or playback.
	License string `json:"license,omitempty" doc:"The licence URL the source declared. ⚠ Absent means UNKNOWN, never public domain — most archive.org items declare none."`
	// HasTranscript reports whether a transcript exists, without shipping it.
	//
	// ⚠ Deliberately NOT the transcript itself: it is kilobytes per clip that no grid renders, so
	// at a 100-row page it would be roughly ten times the rest of the payload. The detail surface
	// fetches the text; the listing only needs to know there is some.
	HasTranscript bool `json:"hasTranscript,omitempty" doc:"Whether a transcript exists (§10 V44). The text itself is a detail-surface read — kilobytes per clip that no grid renders."`
}

// playsCounted reports whether THIS install can observe a filler clip airing.
//
// It is exactly "does internal playout run here": the resolver is what records a play (see
// playoutadapter.airingFiller), and a Tunarr-backed install has none — its filler is aired by
// Tunarr, which never reports back. Deriving it from the same field the program route checks
// means the flag cannot drift from the behaviour it describes.
func (s *Server) playsCounted() bool { return s.playoutResolver != nil }

// clipPathByHash resolves a clip's content HASH (the wire identity, V45a) to its disk path (the disk
// LOCATION, server-internal). The byte routes (thumb/media/hover) take the hash in the URL and need
// the path to find the file. Returns ("", false) for a missing clip or a nil/unconfigured store — the
// caller renders that as an ordinary 404, never an error (a missing clip and a missing thumbnail are
// the same non-event to the client). ⚠ One store read per byte request: the tradeoff for a single
// wire identity (V45a) — the path never crosses the API, so no slash-in-URL encoding hazard remains.
func (s *Server) clipPathByHash(ctx context.Context, hash string) (string, bool) {
	if s.store == nil || hash == "" {
		return "", false
	}
	clip, err := s.store.GetClip(ctx, hash)
	if err != nil || clip.Path == "" {
		return "", false
	}
	return clip.Path, true
}

// clipToDTO maps a stored clip. playsCounted comes from the caller because it is a property
// of the INSTALL (does internal playout run here?), not of the clip.
// clipArtworkResolver pre-resolves every artwork image for a page of clips in one pass.
//
// ⚠ **Called ONCE per page, before the render loop — never inside it.** The filler catalog is a
// grid, and each tile has a still AND a hover loop, so a per-row lookup is two queries per tile:
// a 200-clip page would be 400 round trips to render one screen. This is the same N+1 the channel
// list already avoids, one surface over, and it matters more here because the page is bigger.
func (s *Server) clipArtworkResolver(ctx context.Context, clips []store.Clip) func(string) *ImageDTO {
	if s.images == nil || len(clips) == 0 {
		return nil
	}
	hashes := make([]string, 0, len(clips)*2)
	for _, c := range clips {
		hashes = append(hashes, c.ThumbImageHash, c.HoverImageHash)
	}
	byHash := s.imageDTOsByHash(ctx, hashes)
	if len(byHash) == 0 {
		return nil
	}
	return func(hash string) *ImageDTO {
		if hash == "" {
			return nil
		}
		return byHash[hash]
	}
}

// clipToDTO renders a clip for the wire. `img`, when non-nil, resolves an image hash to its
// record — pre-resolved by the caller for the whole page, never looked up per clip (see
// imageDTOsByHash: the catalog is a grid, so a per-row lookup is two queries per tile).
func clipToDTO(c store.Clip, playsCounted bool, img func(string) *ImageDTO) ClipDTO {
	d := ClipDTO{
		Hash: c.Hash, TunarrProgramID: c.TunarrProgramID, Name: c.Name, Kind: string(c.Kind),
		Era: c.Era, Audience: string(c.Audience), Category: c.Category, Tags: c.Tags, AssertedTags: c.AssertedTags,
		GeographicScope: string(c.GeographicScope), Country: c.Country, Market: c.Market,
		Network: c.Network, Station: c.Station, AirDate: c.AirDate, GeoEvidence: c.GeoEvidence,
		DurationMs: c.DurationMs, Source: c.Source, Quality: c.Quality,
		PlayCount: c.PlayCount, PlaysCounted: playsCounted,
		AITagged: c.AITagged, Tagged: c.Tagged(), SuggestedEra: c.SuggestedEra,
		IsComposite: c.IsComposite, ParentHash: c.ParentHash,
		Brand: c.Brand, Confidence: c.Confidence, Held: c.Held, Language: c.Language,
		VisionTagged: c.VisionTagged, License: c.License,
		// ⚠ The PRESENCE of the transcript, not the transcript. See the field's own warning.
		HasTranscript: c.Transcript != "",
	}
	if !c.LastPlayedAt.IsZero() {
		d.LastPlayedAt = c.LastPlayedAt.UTC().Format(time.RFC3339)
	}
	if img != nil {
		d.ThumbImage = img(c.ThumbImageHash)
		d.HoverImage = img(c.HoverImageHash)
	}
	return d
}

type listFillerInput struct {
	Kind            string `query:"kind" enum:"commercial,bumper,station_id,psa,trailer,interstitial"`
	Era             int    `query:"era"`
	Audience        string `query:"audience" enum:"kids,family,general,late_night"`
	Category        string `query:"category"`
	GeographicScope string `query:"geographicScope" enum:"unknown,national,local"`
	Country         string `query:"country" minLength:"2" maxLength:"2"`
	Market          string `query:"market"`
	Taxon           string `query:"taxon" doc:"Match clips carrying this taxon directly or through a descendant rollup"`
	Unclassified    bool   `query:"unclassified" doc:"Only playable clips with no directly asserted taxonomy tags on any axis"`
	WithoutAxis     string `query:"withoutAxis" enum:"product,format,seasonal,audience-cue" doc:"Only playable clips without a directly asserted taxonomy tag on this axis; absence may be valid for sparse cue axes"`
	Untagged        bool   `query:"untagged" doc:"Only commercials missing match tags"`
	// Q is the clip corpus's search box (§7.2). Clip search lives here rather than on
	// /v1/search because a clip is not a provisionable title (§10) and cannot be a
	// federated Candidate without leaking a non-title into the LLM grounding path.
	Q string `query:"q" doc:"Case-insensitive substring match across name, brand, on-screen text and tags (§10 V51d)"`
	// SearchTranscript widens Q to the persisted transcript (§10 V51d) — opt-in because it is the
	// one long column (megabytes over a 500-row page) and the one noisy one ("ford" matches
	// "afford", with no ranking to explain the hit).
	SearchTranscript bool `query:"searchTranscript" doc:"Also search the transcript. Opt-in: it is kilobytes per clip and matches inside words."`
	// Limit / Offset page the catalog (§10 V51d).
	//
	// ⚠ **The default lives HERE, never in the store.** `ClipFilter.Limit == 0` means no LIMIT
	// clause because pod assembly loads the catalog through the zero filter — a store-side default
	// would silently cut every channel's break pool to the first hundred clips.
	//
	// ⚠ `minimum:"1"` matters as much as the default: it stops a client sending `limit=0` and
	// reaching the store's "unbounded" sentinel by accident.
	Limit  int `query:"limit" minimum:"1" maximum:"500" default:"100" doc:"Page size (§10 V51d). Capped at 500 — the unbounded read this replaced hard-fails past ~65k clips on Postgres."`
	Offset int `query:"offset" minimum:"0" doc:"Rows to skip. Use with total to render 'showing 61-120 of 1,204'."`
	// Sort / Order. ⚠ A closed enum, mapped to a column by a fixed switch in the store; an unknown
	// value is rejected rather than silently ignored.
	Sort  string `query:"sort" enum:"name,duration,added,plays,confidence" doc:"Sort key; default is the historical path order. Every ordering is made total by a hash tie-break (§10 V51d)."`
	Order string `query:"order" enum:"asc,desc" doc:"Sort direction; default ascending."`
	// IncludeHeld / HeldOnly surface the review queue's clips (§10 V38).
	//
	// ⚠ Opt-in on the wire exactly as in the store, and for the same reason: a held clip is not in
	// the playable catalog. `ClipDTO.held` ships with this so a client can tell them apart.
	IncludeHeld bool `query:"includeHeld" doc:"Include clips awaiting review (§10 V38). They are excluded by default because they are not in the playable catalog."`
	// IncludeComposites / TopLevel are how the catalog listing renders breaks as CONTAINERS.
	//
	// ⚠ Both opt-in, and NOT defaults for this route, because /v1/filler is also the add-search
	// behind the channel pin/exclude editor and the ⌘K palette. Those want AIRABLE clips —
	// segments, not the 16-minute break they came out of — so flipping either default would put a
	// non-airable container into a channel's pin list.
	IncludeComposites bool `query:"includeComposites" doc:"Include composites — recorded breaks, which are NOT airable (§10 V45). The catalog listing pairs this with topLevel to render them as containers."`
	TopLevel          bool `query:"topLevel" doc:"Only clips with no parent, so a break paginates as one container row instead of the twenty adverts inside it (§10 V51d)."`
	// ParentHash loads the SEGMENTS of one composite — what expanding a break asks for.
	ParentHash string `query:"parentHash" doc:"Load the segments split out of this composite (§10 V45). Takes precedence over topLevel, which it contradicts by construction."`
	// Hashes is the batch read: resolve these specific clips.
	//
	// ⚠ It exists because paging deleted the alternative. The channel pin/exclude editor used to
	// load the whole catalog to map N saved ids to names, which a 100-row page truncates.
	// Hashes resolves exactly these clip hashes — the batch read behind "name these N pinned clips".
	//
	// ⚠ **`explode`, and it is load-bearing.** Huma turns explode OFF by default for query fields
	// ("parsing is *much* easier if we use comma-separated values"), which means `?hashes=a&hashes=b`
	// binds ONE element — the rest are silently dropped and the request still returns 200. The
	// generated TypeScript client sends exactly that repeated form, so before this tag every pinned
	// clip after the first failed to resolve and the channel page reported real clips as "no longer
	// in your catalog". Found on the live stack; neither side's tests could see it, because the Go
	// tests hand-wrote `?hashes=a,b` and the client was generated from a spec that agreed with them.
	Hashes []string `query:"hashes,explode" doc:"Resolve exactly these clip hashes (§10 V51d) — the batch read behind 'name these N pinned clips'. Repeat the parameter per hash. Unknown hashes are simply absent."`
}
type listFillerOutput struct {
	Body struct {
		Clips []ClipDTO `json:"clips"`
		// Total is how many clips MATCH THE FILTER, not how many are on this page (§10 V51d).
		//
		// ⚠ Counted in SQL through the same WHERE builder the rows come from, so a page's total can
		// never disagree with its contents — including under a search, which widens both together.
		Total int `json:"total" doc:"Total matching the filter, ignoring limit/offset — the 'of 1,204' in a pager (§10 V51d)"`
	}
}

func (s *Server) listFiller(ctx context.Context, in *listFillerInput) (*listFillerOutput, error) {
	f := store.ClipFilter{
		Kind:                filler.Kind(in.Kind),
		Era:                 in.Era,
		Audience:            filler.Audience(in.Audience),
		Category:            in.Category,
		GeographicScope:     filler.GeographicScope(in.GeographicScope),
		Country:             in.Country,
		Market:              in.Market,
		Taxon:               in.Taxon,
		WithoutTaxonomyTags: in.Unclassified,
		WithoutTaxonomyAxis: taxonomy.Axis(in.WithoutAxis),
		UntaggedOnly:        in.Untagged,
		Query:               in.Q,
		QueryTranscript:     in.SearchTranscript,
		IncludeHeld:         in.IncludeHeld,
		IncludeComposites:   in.IncludeComposites,
		TopLevelOnly:        in.TopLevel,
		ParentHash:          in.ParentHash,
		Hashes:              in.Hashes,
		Sort:                store.ClipSort(in.Sort),
		Desc:                in.Order == "desc",
		Limit:               in.Limit,
		Offset:              in.Offset,
	}
	clips, err := s.store.ListClips(ctx, f)
	if err != nil {
		// ⚠ Defence in depth: huma's enum already rejects an unknown sort at validation, so this
		// can only fire if the two vocabularies drift. It is a 422 rather than a 500 because it is
		// the caller's value that is wrong — and never a silent fall-back to the default order.
		if errors.Is(err, store.ErrUnknownClipSort) {
			return nil, huma.Error422UnprocessableEntity("unknown sort", err)
		}
		return nil, err
	}
	// ⚠ Counted with the SAME filter, deliberately including limit/offset — CountClips ignores
	// them by construction (they live outside the WHERE builder), so passing the whole filter is
	// what guarantees the count and the rows share every predicate.
	total, err := s.store.CountClips(ctx, f)
	if err != nil {
		return nil, huma.Error500InternalServerError("count filler clips", err)
	}
	out := &listFillerOutput{}
	out.Body.Total = total
	out.Body.Clips = make([]ClipDTO, 0, len(clips))
	// Resolved for the whole page BEFORE the loop — see clipArtworkResolver on why a lookup
	// inside the loop would be two queries per tile.
	img := s.clipArtworkResolver(ctx, clips)
	for _, c := range clips {
		out.Body.Clips = append(out.Body.Clips, clipToDTO(c, s.playsCounted(), img))
	}
	return out, nil
}

type patchClipInput struct {
	Body struct {
		// Hash is the clip's identity (its content hash, V38c). ⚠ In the BODY (§10 V45a): the wire
		// identity is the hash — hex, no slashes — so it needs no encoding and no {id} URL segment.
		// Required.
		Hash     string `json:"hash" doc:"The clip's content hash (its identity)"`
		Era      int    `json:"era,omitempty"`
		Audience string `json:"audience,omitempty" enum:"kids,family,general,late_night,"`
		// Tags is the operator's chosen taxonomy tag set (§10 V45a) — REPLACES the old single
		// `category` string. Each is grounded against the live taxonomy on write (an unknown slug is
		// rejected), and `category` is DERIVED from the accepted set (the primary product leaf). A nil
		// Tags means "leave the tags alone"; an explicit empty array clears them.
		Tags *[]string `json:"tags,omitempty" doc:"The operator's taxonomy tags (leaf slugs). Grounded on write; category is derived. Omit to leave unchanged, send [] to clear (§10 V45a)."`
		// Brand is a grounded advertiser name, deliberately separate from the taxonomy. Pointer
		// semantics distinguish omission (leave it alone) from an explicit empty string (clear a
		// bad model guess). The operator is a valid grounding source.
		Brand *string `json:"brand,omitempty" maxLength:"120" doc:"Grounded advertiser name. Omit to leave unchanged; send an empty string to clear."`
		// Kind is correctable by hand (§10). Detection at sync gets it wrong in one
		// direction often enough to matter — a trailer scanned as a commercial — and
		// kind drives pod ROLE (a bumper bookends a pod, a commercial fills it), so a
		// wrong kind yields structurally wrong pods rather than just a mis-tagged clip.
		// Empty means "leave it alone", so a tag-only edit never rewrites kind.
		Kind string `json:"kind,omitempty" enum:"commercial,bumper,station_id,psa,trailer,interstitial,"`
		// Geography is a complete, grounded replacement. Omit to leave it unchanged; use scope
		// unknown to clear a bad classification. Operator edits are recorded as their own evidence.
		Geography *ClipGeographyDTO `json:"geography,omitempty"`
	}
}

type ClipGeographyDTO struct {
	Scope   string `json:"scope" enum:"unknown,national,local"`
	Country string `json:"country,omitempty" maxLength:"2"`
	Market  string `json:"market,omitempty" maxLength:"120"`
	Network string `json:"network,omitempty" maxLength:"120"`
	Station string `json:"station,omitempty" maxLength:"120"`
	AirDate string `json:"airDate,omitempty" doc:"YYYY-MM-DD when known"`
}
type clipOutput struct{ Body ClipDTO }

func (s *Server) patchFillerClip(ctx context.Context, in *patchClipInput) (*clipOutput, error) {
	now := time.Now()
	if in.Body.Hash == "" {
		return nil, errUnprocessable("Missing clip", "A clip tag edit must name the clip by its hash.")
	}
	// ⚠ Identity is the HASH (§10 V45a), so the store lookups (`GetClip`/classification update/
	// `UpdateClipKind`, all keyed `WHERE hash = ?`) take it directly — no path resolution. Fetch once
	// to 404 a missing clip synchronously and to read the pre-edit tags below.
	clip, err := s.store.GetClip(ctx, in.Body.Hash)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, errNotFound("Clip not found", "That filler clip doesn't exist — it may have been removed by a catalog sync.")
		}
		return nil, err
	}
	// Resolve the operator's tag edit against the LIVE taxonomy (§10 V45a). Tags nil ⇒ leave the
	// clip's tags alone (an era/audience/kind-only edit); an explicit [] ⇒ clear them. Each slug is
	// grounded (forest.Resolve) exactly as the tagger grounds the model — an unknown slug is a 422,
	// never silently persisted. The store derives `category` from the accepted set in the same
	// transaction, against its current graph; the client never supplies that compatibility shadow.
	if in.Body.Tags != nil {
		taxa, err := s.store.ListTaxa(ctx)
		if err != nil {
			return nil, err
		}
		forest := taxonomy.New(taxa)
		leaves := make([]string, 0, len(*in.Body.Tags))
		seen := map[string]bool{}
		for _, raw := range *in.Body.Tags {
			slug, ok := forest.Resolve(raw)
			if !ok {
				return nil, errUnprocessable("Unknown tag",
					fmt.Sprintf("The tag %q is not in the classification vocabulary. Add it under Filler → Advanced → Classification vocabulary first, or choose an existing tag.", raw))
			}
			if !seen[slug] {
				seen[slug] = true
				leaves = append(leaves, slug)
			}
		}
		sort.Strings(leaves)
		if err := s.store.SetClipTags(ctx, clip.Hash, leaves); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return nil, errNotFound("Clip not found", "That filler clip doesn't exist — it may have been removed by a catalog sync.")
			}
			if errors.Is(err, store.ErrTaxonConflict) {
				return nil, errConflict("Taxonomy changed", "The tag vocabulary changed while this clip was being edited. Review the tags and try again.")
			}
			return nil, err
		}
	}
	// A manual edit clears the AI flag (a human tagged it). suggestedEra is 0 here —
	// only the tagger writes suggestions — and the store's rule applies: setting era
	// CONFIRMS and clears any suggestion in the same write, while an era-less edit
	// leaves the operator's unanswered question alone (§10, V34). `category` is the
	if err := s.store.UpdateClipClassification(ctx, clip.Hash, in.Body.Era, in.Body.Audience, 0, false, now); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, errNotFound("Clip not found", "That filler clip doesn't exist — it may have been removed by a catalog sync.")
		}
		return nil, err
	}
	// Kind is a separate write because the AI tagging job shares UpdateClipClassification and must
	// never touch kind. Both are idempotent single-row updates, so the same PATCH is
	// safe to retry if the second fails.
	if in.Body.Kind != "" {
		if err := s.store.UpdateClipKind(ctx, clip.Hash, in.Body.Kind, now); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return nil, errNotFound("Clip not found", "That filler clip doesn't exist — it may have been removed by a catalog sync.")
			}
			return nil, err
		}
	}
	if in.Body.Geography != nil {
		g := in.Body.Geography
		scope := filler.GeographicScope(g.Scope)
		geo := filler.Geography{Country: g.Country, Market: g.Market}.Normalize()
		if err := geo.Validate(); err != nil {
			return nil, errUnprocessable("Invalid geography", err.Error())
		}
		switch scope {
		case filler.GeographicUnknown:
			geo = filler.Geography{}
		case filler.GeographicNational:
			if geo.Country == "" || geo.Market != "" {
				return nil, errUnprocessable("Invalid geography", "National clips require a country and cannot carry a local market.")
			}
		case filler.GeographicLocal:
			if geo.Country == "" || geo.Market == "" {
				return nil, errUnprocessable("Invalid geography", "Local clips require both a country and a local market.")
			}
		default:
			return nil, errUnprocessable("Invalid geography", "Geographic scope must be unknown, national, or local.")
		}
		airDate := strings.TrimSpace(g.AirDate)
		if airDate != "" {
			if _, err := time.Parse(time.DateOnly, airDate); err != nil {
				return nil, errUnprocessable("Invalid air date", "Air date must use YYYY-MM-DD.")
			}
		}
		if err := s.store.UpdateClipGeography(ctx, clip.Hash, string(scope), geo.Country, geo.Market,
			strings.TrimSpace(g.Network), strings.TrimSpace(g.Station), airDate, "operator", now); err != nil {
			return nil, err
		}
	}
	if in.Body.Brand != nil {
		brand := strings.TrimSpace(*in.Body.Brand)
		if err := s.store.SetClipBrand(ctx, clip.Path, brand, now); err != nil {
			return nil, err
		}
	}
	c, err := s.store.GetClip(ctx, clip.Hash)
	if err != nil {
		return nil, err
	}
	s.reconcileChannelsForFillerChange(ctx, clip.Clip, c.Clip)
	return &clipOutput{Body: clipToDTO(c, s.playsCounted(), s.clipArtworkResolver(ctx, []store.Clip{c}))}, nil
}

type syncFillerOutput struct {
	Body struct {
		Total   int `json:"total"`
		Added   int `json:"added"`
		Updated int `json:"updated"`
		Pruned  int `json:"pruned"`
	}
}

func (s *Server) syncFiller(ctx context.Context, _ *struct{}) (*syncFillerOutput, error) {
	if s.filler == nil || s.featureOff(ctx, "filler") {
		return nil, errNotImplemented("Filler isn't set up", "Enable filler in Settings to sync a commercial and bumper catalog.")
	}
	total, added, updated, pruned, err := s.filler.Sync(ctx)
	// ⚠ A switched-off drop-folder is not a failure, and must not be reported as one. Left as a
	// 502 it reads "your media server is broken" — sending the operator to check a connection
	// that is fine — when the true answer is a switch they flipped themselves, on this page.
	if errors.Is(err, filler.ErrSourceDisabled) {
		return nil, errSourceDisabled()
	}
	if err != nil {
		return nil, apiErrWithCause(http.StatusBadGateway, "Couldn't sync filler",
			"Loomarr couldn't sync the filler catalog from your media server. Check its connection in Settings and try again.", err)
	}
	out := &syncFillerOutput{}
	out.Body.Total, out.Body.Added, out.Body.Updated, out.Body.Pruned = total, added, updated, pruned
	return out, nil
}

type tagFillerOutput struct {
	Body struct {
		Considered int `json:"considered"`
		Tagged     int `json:"tagged"`
		Partial    int `json:"partial"`
		Skipped    int `json:"skipped"`
	}
}

func (s *Server) tagFiller(ctx context.Context, _ *struct{}) (*tagFillerOutput, error) {
	if s.filler == nil || s.featureOff(ctx, "filler") {
		return nil, errNotImplemented("Filler isn't set up", "Enable filler in Settings to sync a commercial and bumper catalog.")
	}
	considered, tagged, partial, skipped, err := s.filler.Tag(ctx)
	if err != nil {
		return nil, apiErrWithCause(http.StatusBadGateway, "Couldn't tag filler",
			"Loomarr couldn't AI-tag the filler clips. Check that an AI provider is connected in Settings and try again.", err)
	}
	out := &tagFillerOutput{}
	out.Body.Considered, out.Body.Tagged, out.Body.Partial, out.Body.Skipped = considered, tagged, partial, skipped
	return out, nil
}

type ingestFillerInput struct {
	Body struct {
		// URLs are supplied per request by an admin rather than configured globally:
		// there is no unattended crawler (§10), so ingestion is always a deliberate act
		// with a person attached to it.
		URLs []string `json:"urls" minItems:"1" doc:"YouTube playlist/video or Archive.org collection/item URLs"`
	}
}
type ingestFillerOutput struct {
	Body struct {
		JobID string `json:"jobId" doc:"Watch /v1/events for filler_ingest frames carrying this id"`
	}
}

// ingestFiller starts a download job and returns immediately. Downloads run for minutes
// to hours, so the response carries a job id and progress arrives on the SSE bus — the
// same contract as the §8.1 model pull.
func (s *Server) ingestFiller(ctx context.Context, in *ingestFillerInput) (*ingestFillerOutput, error) {
	if s.filler == nil {
		return nil, errNotImplemented("Filler isn't set up", "Enable filler in Settings to sync a commercial and bumper catalog.")
	}
	// ⚠ `IngestAsked`, not `Ingest`: this is the one path where an OPERATOR named the target, so
	// it is the one path that registers a source. Auto-fetch and approved pulls carry the items
	// inside an already-registered collection and must not add a row per clip.
	jobID, err := s.filler.IngestAsked(ctx, in.Body.URLs)
	if errors.Is(err, ErrIngestUnavailable) {
		// NOT feature_not_configured: no setting can assert that a binary RUNS. ⚠ This
		// used to say "run the loomarr:filler image" — that variant no longer exists (the
		// single image always ships the tooling, §16), so the remedy it named was a dead
		// end of exactly the kind §7 warns about. Unavailable now means a degraded
		// install, and the message says what an operator can actually check.
		return nil, errConflict("Downloads aren't available here",
			"This install can't run the download tooling. The official image ships it; a custom build may be missing it, or INGEST_YTDLP_PATH may point somewhere wrong.")
	}
	if err != nil {
		return nil, err
	}
	out := &ingestFillerOutput{}
	out.Body.JobID = jobID
	return out, nil
}

// --- discovery (§10, V33) ---

type discoverFillerInput struct {
	// Query is what the operator typed. An empty search would return archive.org's entire
	// movies corpus ranked by nothing, which is not an answer to any question — so a search
	// needs at least two characters. No longer `required`, because `collection` is the other
	// way to ask; exactly one of the two must be given (checked in the handler).
	Query string `query:"q" minLength:"2" doc:"Words to search for, e.g. \"1980s cereal commercial\". When collection is also present, searches within that collection."`
	// Collection lists ONE named archive.org collection instead of searching. This is what a
	// starter pack is (§10, V17d): a curated collection an operator keeps or excludes from
	// before anything is fetched. Deliberately the same endpoint as the keyword search — a
	// separate route would be a second implementation of "list clips, download nothing".
	Collection string `query:"collection" doc:"An archive.org collection to list or search: a URL, a /details/<id> path, or a bare identifier. May be combined with q to search within it."`
	// Limit caps the page. A listing is for DECIDING — an operator judges a source from a
	// handful of titles — so the ceiling is low on purpose.
	Limit int `query:"limit" minimum:"1" maximum:"25" doc:"Max results (default 25)"`
}

type discoverFillerOutput struct {
	Body struct {
		Items []DiscoveredClip `json:"items"`
		// Total is how many the SOURCE matched, not how many were returned. An operator
		// judging "is this search any good" needs the real number: 54 hits shown 5 at a
		// time is a different situation from 5 hits total.
		Total int `json:"total"`
		// ⚠ Stated ONCE here rather than as a per-item field, and that is deliberate.
		// archive.org declares a licence on only ~8% of items, and yt-dlp reports none at
		// all, so a per-result licence chip would read "unknown" on nearly every row —
		// implying a check that never happened. See the build plan §6.3.
		LicenceNote string `json:"licenceNote"`
	}
}

// discoverFiller searches for clips the operator could add, downloading nothing.
func (s *Server) discoverFiller(ctx context.Context, in *discoverFillerInput) (*discoverFillerOutput, error) {
	if s.filler == nil {
		return nil, errNotImplemented("Filler isn't set up",
			"Enable filler in Settings before searching for clips to add.")
	}

	// At least one mode. With both present, the keyword is scoped to the named collection — the
	// request made by a source row's search box. Neither is the empty search the minLength above
	// exists to prevent.
	query, collection := strings.TrimSpace(in.Query), strings.TrimSpace(in.Collection)
	if query == "" && collection == "" {
		return nil, apiErr(http.StatusUnprocessableEntity, "Ask for one thing",
			"Send q to search Archive.org, collection to list one collection, or both to search within it.")
	}

	var (
		items []DiscoveredClip
		total int
		err   error
	)
	if collection != "" {
		items, total, err = s.filler.DiscoverCollection(ctx, collection, query, in.Limit)
	} else {
		items, total, err = s.filler.Discover(ctx, query, in.Limit)
	}
	if err != nil {
		// A listing failure is upstream (archive.org unreachable or refusing), not the
		// caller's fault — so it is a 502-shaped problem, and the message says which side
		// broke rather than blaming the query.
		return nil, apiErrWithCause(http.StatusBadGateway, "Couldn't search for clips",
			"archive.org didn't answer. Try again in a moment.", err)
	}

	out := &discoverFillerOutput{}
	// Non-nil so a client can iterate without guarding; "nothing matched" is a real answer.
	out.Body.Items = items
	if out.Body.Items == nil {
		out.Body.Items = []DiscoveredClip{}
	}
	out.Body.Total = total
	out.Body.LicenceNote = "Licence information isn't available for most results. Check before reusing."
	return out, nil
}

type discoverStatsInput struct {
	// IDs are the search-result identifiers to describe, as REPEATED params (`?id=a&id=b&id=c`).
	//
	// ⚠ **This was comma-separated, and the comment here used to explain why** — huma disables
	// `explode` for query fields by default ("parsing is *much* easier if we use comma-separated
	// values"), so `?id=a&id=b` bound ONE element. The note was correct about the mechanism and
	// wrong about the conclusion: the generated TypeScript client sends the repeated form, so the
	// documented convention was one nobody on the wire actually used. `explode` makes the contract
	// match the caller instead of the other way round.
	//
	// ⚠ **It also makes `maxItems` work for the first time.** The old note said the cap "never
	// fires, because the slice is never long" — which is exactly right and exactly the problem:
	// each id is one upstream call, so the cap exists to stop an uncapped list making Loomarr
	// hammer archive.org on someone else's behalf. A limit that cannot fire is not a limit.
	IDs []string `query:"id,explode" required:"true" maxItems:"25" doc:"Result ids to describe. Repeat the parameter per id (?id=a&id=b)."`
}

type discoverStatsOutput struct {
	Body struct {
		// Stats is keyed by result id. ⚠ An id archive.org could not answer for is ABSENT,
		// never present-with-zeros: 0 renders as "0:00", which claims a clip is empty, and
		// "unknown" is the only honest answer for an item it never probed.
		Stats map[string]DiscoveredClipStats `json:"stats"`
	}
}

// discoverFillerStats fills in the duration + quality a search omits (§10, V35).
//
// ⚠ **A separate route because it is a separate COST, measured against the live API.** A
// listing is one Solr request; these two fields are one `/metadata/<id>` call each — 22.6s for
// a page of 25, with a per-call median of 1.78s. Widening the fan-out makes it worse (12 and 25
// concurrent both measured 25s against 6's 15.6s), so archive.org is throttling and the latency
// is not ours to tune away. Asking only for rows a person is looking at is the fix.
func (s *Server) discoverFillerStats(ctx context.Context, in *discoverStatsInput) (*discoverStatsOutput, error) {
	if s.filler == nil {
		return nil, errNotImplemented("Filler isn't set up",
			"Enable filler in Settings before searching for clips to add.")
	}
	if len(in.IDs) == 0 {
		return nil, apiErr(http.StatusUnprocessableEntity, "Nothing to describe",
			"Send at least one id.")
	}

	stats, err := s.filler.EnrichDiscovered(ctx, in.IDs)
	if err != nil {
		return nil, apiErrWithCause(http.StatusBadGateway, "Couldn't read clip details",
			"archive.org didn't answer. Try again in a moment.", err)
	}

	out := &discoverStatsOutput{}
	// Non-nil so a client can index without guarding; "nothing known" is a real answer.
	out.Body.Stats = stats
	if out.Body.Stats == nil {
		out.Body.Stats = map[string]DiscoveredClipStats{}
	}
	return out, nil
}

// ChannelFitDTO is one channel's answer for one clip (§10, V35 item 1.7).
type ChannelFitDTO struct {
	ChannelID string `json:"channelId"`
	Name      string `json:"name"`
	Number    int    `json:"number"`
	// Level is the ladder rung this clip would be drawn from on this channel.
	//
	// ⚠ `bumper_card` here means "this clip is not a candidate", NOT "this channel falls back to
	// the card" — the same word carries both meanings because it is the bottom of one ladder.
	// `reason` disambiguates: a rejected clip always carries one, a PINNED clip never does.
	Level string `json:"level" enum:"exact,widened,audience,bumper_card" doc:"The rung this clip would be drawn from; bumper_card means it is not a candidate (see reason)"`
	// Reason names the predicate that rejected the clip, absent when it was not rejected.
	//
	// ⚠ A CODE, never a sentence: the frontend owns the wording (the §11 refusal-code
	// precedent), because a server sentence cannot be translated and cannot link to the setting
	// that caused it.
	Reason string `json:"reason,omitempty" enum:"kind,duration,quality,category,audience,excluded" doc:"Why the clip is not a candidate; absent when it is one"`
	// Pinned / Excluded are the operator's explicit override for this clip on this channel.
	//
	// ⚠ Both can be true, and EXCLUDED WINS — assembly seeds excluded ids into the used-set
	// before pins are placed. Reported as stored rather than normalised, or the picker would
	// show a state the database does not hold.
	Pinned   bool `json:"pinned"`
	Excluded bool `json:"excluded"`
}

type clipFitInput struct {
	// Clip is the clip's PATH (its identity, §10) — a query parameter because a path
	// containing slashes cannot be a path segment; see the route registration.
	Clip string `query:"clip" required:"true" doc:"The clip's path (its identity, §10)"`
}

type clipFitOutput struct {
	Body struct {
		// Channels is EVERY channel, paused ones included — see the route description.
		Channels []ChannelFitDTO `json:"channels"`
	}
}

// clipChannelFit answers "where does this clip land on each channel's ladder" (§10, V35 1.7).
func (s *Server) clipChannelFit(ctx context.Context, in *clipFitInput) (*clipFitOutput, error) {
	if s.pods == nil {
		return nil, errNotImplemented("Filler isn't set up",
			"Set up commercials and filler before choosing which channels use a clip.")
	}

	fits, err := s.pods.ClipFit(ctx, in.Clip)
	if errors.Is(err, store.ErrNotFound) {
		return nil, errNotFound("Clip not found", "That clip isn't in the catalog — it may have been removed.")
	} else if err != nil {
		return nil, err
	}

	chans, err := s.store.ListChannels(ctx)
	if err != nil {
		return nil, err
	}

	out := &clipFitOutput{}
	// ⚠ Built from the CHANNEL list, not by ranging the fit map: Go map order is randomised, so
	// the picker's rows would shuffle on every render. The store's order is number-sorted, which
	// is the order an operator reads their channels in.
	out.Body.Channels = make([]ChannelFitDTO, 0, len(chans))
	for _, ch := range chans {
		fit, ok := fits[ch.ID]
		if !ok {
			continue // a channel created between the two reads; absent beats a fabricated answer
		}
		out.Body.Channels = append(out.Body.Channels, ChannelFitDTO{
			ChannelID: ch.ID, Name: ch.Name, Number: ch.Number,
			Level:    string(fit.Level),
			Reason:   string(fit.Reason),
			Pinned:   fit.Pinned,
			Excluded: fit.Excluded,
		})
	}
	return out, nil
}

// --- compilation splitting (§10, V34) ---

type splitFillerInput struct {
	Body struct {
		// Hash identifies the compilation clip (its content hash, V38c). ⚠ In the BODY (§10 V45a):
		// the wire identity is the hash. This is ALSO what Propose needs — it is keyed `WHERE hash = ?`
		// — so passing the hash straight through fixes the "clip not found" the old path arg caused.
		Hash string `json:"hash" doc:"The compilation clip's content hash (its identity, §10)"`
	}
}
type splitFillerOutput struct {
	Body struct {
		JobID string `json:"jobId" doc:"Detection operation id — read /v1/filler/split-operations/{jobId}; filler_split events only accelerate progress"`
	}
}

// splitFiller starts detection on a compilation clip. It answers immediately:
// a full-decode detection pass takes minutes, so the job id's progress rides
// the SSE bus and the proposal is read back when the terminal frame lands.
func (s *Server) splitFiller(ctx context.Context, in *splitFillerInput) (*splitFillerOutput, error) {
	if s.filler == nil {
		return nil, errNotImplemented("Filler isn't set up",
			"Enable filler in Settings before splitting a compilation.")
	}
	if in.Body.Hash == "" {
		return nil, errUnprocessable("Missing clip", "A split must name the compilation clip by its hash.")
	}
	jobID, err := s.filler.Split(ctx, in.Body.Hash)
	if errors.Is(err, store.ErrNotFound) {
		return nil, errNotFound("Clip not found", "That filler clip doesn't exist — it may have been removed by a catalog sync.")
	}
	if errors.Is(err, ErrSplitUnavailable) {
		return nil, errConflict("Splitting isn't set up",
			"Splitting needs the filler drop-folder (filler.dir). Set it in Settings → Filler.")
	}
	if err != nil {
		return nil, err
	}
	out := &splitFillerOutput{}
	out.Body.JobID = jobID
	return out, nil
}

type getFillerSplitOperationInput struct {
	JobID string `path:"jobId"`
}

type splitOperationDTO struct {
	JobID       string    `json:"jobId"`
	ClipHash    string    `json:"clipHash"`
	Status      string    `json:"status" enum:"queued,running,success,error"`
	ProposalID  string    `json:"proposalId,omitempty"`
	Error       string    `json:"error,omitempty"`
	StartedAt   time.Time `json:"startedAt"`
	CompletedAt time.Time `json:"completedAt,omitempty"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type getFillerSplitOperationOutput struct {
	Body splitOperationDTO
}

func (s *Server) getFillerSplitOperation(
	ctx context.Context,
	in *getFillerSplitOperationInput,
) (*getFillerSplitOperationOutput, error) {
	operation, err := s.store.GetInteractiveOperation(ctx, in.JobID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, errNotFound("Split operation not found", "That split operation doesn't exist.")
	}
	if err != nil {
		return nil, err
	}
	if operation.Kind != store.InteractiveOperationFillerSplit {
		return nil, errNotFound("Split operation not found", "That split operation doesn't exist.")
	}
	return &getFillerSplitOperationOutput{Body: splitOperationDTO{
		JobID: operation.ID, ClipHash: operation.Subject, Status: string(operation.Status),
		ProposalID: operation.ResultID, Error: operation.Error, StartedAt: operation.StartedAt,
		CompletedAt: operation.CompletedAt, UpdatedAt: operation.UpdatedAt,
	}}, nil
}

type getFillerSplitInput struct {
	ProposalID string `path:"proposalId"`
}
type getFillerSplitOutput struct {
	Body filler.SplitProposal
}

// getFillerSplit reads one proposal — the review surface's source of truth.
// Read straight from the store, like list/patch: no service indirection needed.
func (s *Server) getFillerSplit(ctx context.Context, in *getFillerSplitInput) (*getFillerSplitOutput, error) {
	p, err := s.store.GetSplitProposal(ctx, in.ProposalID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, errNotFound("Split proposal not found",
			"That proposal doesn't exist — it may have been confirmed, rejected, or replaced by a re-run of detection.")
	}
	if err != nil {
		return nil, err
	}
	if !p.Ready() {
		return nil, errNotFound("Split proposal not found",
			"Detection is still running; the proposal will appear here when its cut list is ready.")
	}
	return &getFillerSplitOutput{Body: p}, nil
}

type confirmFillerSplitInput struct {
	ProposalID string `path:"proposalId"`
	Body       struct {
		// Segments is the operator's REVIEWED cut list: the proposal as returned,
		// possibly edited. It is re-validated server-side (§10 — the review gate's
		// teeth live on the write path, not in the UI).
		Segments []filler.SplitSegment `json:"segments" minItems:"1"`
	}
}
type confirmFillerSplitOutput struct {
	Body struct {
		// Clips is how many catalog clips the confirm created — the confirmation
		// the UI toasts before the catalog list refreshes.
		Clips int `json:"clips"`
	}
}

// confirmFillerSplit commits the reviewed cut list. Synchronous: stream-copy
// cuts seek rather than decode, so even twenty segments are seconds — the
// minutes belong to detection, which already ran.
func (s *Server) confirmFillerSplit(ctx context.Context, in *confirmFillerSplitInput) (*confirmFillerSplitOutput, error) {
	if s.filler == nil {
		return nil, errNotImplemented("Filler isn't set up",
			"Enable filler in Settings before confirming a split.")
	}
	if err := s.filler.ConfirmSplit(ctx, in.ProposalID, in.Body.Segments); err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			return nil, errNotFound("Split proposal not found",
				"That proposal doesn't exist — it may have been confirmed already, or replaced by a re-run of detection.")
		case errors.Is(err, filler.ErrSplitValidation):
			return nil, apiErrWithCause(http.StatusUnprocessableEntity, "Invalid cut list",
				"The edited segments are outside the clip, overlap, or too short — fix the cuts and try again.", err)
		default:
			return nil, err
		}
	}
	out := &confirmFillerSplitOutput{}
	out.Body.Clips = len(in.Body.Segments)
	return out, nil
}
