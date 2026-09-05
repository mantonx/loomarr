package api

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/loomarr/loomarr/internal/activity"
	"github.com/loomarr/loomarr/internal/auth"
	"github.com/loomarr/loomarr/internal/channels"
	"github.com/loomarr/loomarr/internal/contact"
	"github.com/loomarr/loomarr/internal/filler"
	"github.com/loomarr/loomarr/internal/fillerdecision"
	"github.com/loomarr/loomarr/internal/invitation"
	"github.com/loomarr/loomarr/internal/media"
	"github.com/loomarr/loomarr/internal/metrics"
	"github.com/loomarr/loomarr/internal/notifications"
	"github.com/loomarr/loomarr/internal/recovery"
	"github.com/loomarr/loomarr/internal/schedule"
	"github.com/loomarr/loomarr/internal/store"
	"github.com/loomarr/loomarr/internal/suggest"
)

// Server holds the API dependencies and builds the Huma API on a stdlib mux
// (§7.1, §14: code-first OpenAPI 3.1 via humago — no third-party router).
type Server struct {
	store   store.Store
	auth    Authorizer
	log     *slog.Logger
	metrics *metrics.Recorder
	// backupSQLite is set when the store is the SQLite backend (GET /v1/backup —
	// §16). Nil for Postgres, which returns 501 + a pg_dump docs pointer.
	backupSQLite BackupStreamer
	// login/sessions wire /v1/auth/* (§11); nil until Phase 9 is configured.
	login     LoginService
	sessions  SessionManager
	passwords PasswordService
	userSync  UserSyncer
	// invitations owns administrator admission decisions and one-time sharing grants (§11).
	invitations          InvitationService
	invitationDelivery   InvitationDeliveryService
	invitationRedemption InvitationRedemptionService
	passwordRecovery     PasswordRecoveryService
	accessPublicURL      func() string
	// devices wires /v1/auth/device/* — the pairing handshake a keyboard-less native client uses
	// (§11, Shield P1). Nil until configured, which 404s the routes rather than half-mounting them.
	devices *auth.DeviceManager
	// deviceLimiter bounds pairing starts and code-approval attempts. The user code is short by
	// design (a human reads it off a TV), so a bounded attempt count is what makes it safe.
	deviceLimiter *auth.RateLimiter
	cookieSecure  string // COOKIE_SECURE: auto|true|false (§11)
	trustProxy    bool   // TRUST_PROXY: honor X-Forwarded-For/-Proto only when true (§11)
	// devLogin mounts POST /v1/auth/dev-login — a credential-free admin sign-in for
	// development (§11). Set from LOOMARR_DEV_LOGIN at boot and read ONCE here, when
	// routes are registered: an unset flag means the route never exists, which is a
	// stronger guarantee than a handler that checks a value it could later re-read.
	devLogin bool
	// channels/livetv wire /v1/channels* and /v1/setup/* (§7/§9); nil until
	// Phase 10 is configured.
	channels ChannelService
	livetv   LiveTVService
	// tunerRescanner refreshes the media server's channel list after a lifecycle write removes an
	// internal channel without running reconcile (pause/detach). It is separate from setup wiring:
	// one is a per-operation freshness poke, the other owns tuner registration.
	tunerRescanner TunerRescanner
	// tunarrConnect wires the media server as Tunarr's media source (§6) — backs
	// POST /v1/setup/tunarr-connect + the tunarr_library setup check. Nil ⇒ 501.
	tunarrConnect TunarrConnector
	// suggest/search wire /v1/proposals*, /v1/search (§7.2/§8); nil until
	// Phase 11 is configured.
	suggest          SuggestService
	proposalWorkflow ProposalWorkflow
	search           SearchService
	// collections backs the scope.collections picker; nil ⇒ the route 501s, the same
	// nil-semantics every other optional service here uses.
	collections CollectionService
	// icons backs GET /v1/channels/{id}/icon-suggestions (§icon P2): candidate poster
	// URLs drawn from the channel's own lineup titles. nil ⇒ 501 (TMDB unconfigured).
	icons IconService
	// images backs /v1/images* — the one pipeline every image travels (§22, V52). nil ⇒ the
	// byte route 404s and the record route reports the image absent, which is the honest answer
	// for an instance where the service is not wired.
	images          ImageService
	events          EventSource             // /v1/events SSE (Phase 11); nil ⇒ route 501
	shutdown        <-chan struct{}         // generation shutdown closes long-lived streams before HTTP drain
	filler          FillerService           // /v1/filler* (Phase 12); nil ⇒ sync/tag routes 501
	fillerDecisions *fillerdecision.Service // durable V63 admission audit and projections
	pods            PodPreviewer            // /v1/channels/{id}/pods (§12); nil ⇒ 501
	taxonomy        TaxonomyEditor          // taxonomy impact + graph convergence; nil keeps store-only test wiring
	// fillerLayout is the immutable filesystem topology applied to this server generation.
	// Operational routes keep interpreting catalog paths against this root until restart.
	fillerLayout filler.Layout
	// jobs wires /v1/jobs* (the background-job scheduler, §18.1); nil ⇒ routes 501.
	jobs JobService
	// systemLLM wires /v1/system/llm* (§8.1 model selection); nil ⇒ routes 501.
	systemLLM SystemLLMService
	// database wires /v1/system/database* — the SQLite→PostgreSQL migration stepper
	// (§18, V11); nil ⇒ routes 501.
	database   DatabaseService
	encryption EncryptionService
	// backups wires /v1/system/backups* — the backups on disk (§16, V12); nil ⇒ routes
	// 501, which is the Postgres case (in-app backup is SQLite-only by design).
	backups BackupsService
	// startedAt is when THIS GENERATION came up — the instant About counts uptime from
	// (§16). Set per Server, never a package-level var.
	//
	// ⚠ It was a package-level `var processStart = time.Now()` and the restart loop
	// (§9.2) proved that wrong on the first live test: the value survived the rebuild, so
	// About kept reporting the ORIGINAL boot and would have claimed days of uptime on a
	// freshly restarted instance. Exactly the silent package-level-state hazard §9.2
	// warns about — no panic, no log line, just a number quietly lying in a bug report.
	startedAt time.Time
	// sso wires /v1/auth/sso/* — the OIDC credential path (§11, V8). nil ⇒ the routes are
	// NOT MOUNTED, which is the honest posture for an unconfigured provider: a
	// sign-in-with button that 501s is worse than one that is not offered.
	sso SSOService
	// activity records Dashboard feed lines (§12, V32). Nil-safe on the Recorder itself, so
	// a handler built without one (unit tests) records nothing rather than needing a guard
	// at every write point.
	activity *activity.Recorder
	// diagnosticEvents is the admin-only retained technical timeline. Nil keeps the route mounted
	// but truthfully unavailable for store-less generations.
	diagnosticEvents DiagnosticEventService
	// diagnosticProcesses owns bounded Process-run metadata, progress, and output reads.
	diagnosticProcesses DiagnosticProcessService
	// diagnosticBundles owns bounded preview and on-demand archive assembly.
	diagnosticBundles DiagnosticBundleService
	diagnosticCapture DiagnosticCaptureService
	// clientDiagnostics accepts a closed authenticated web/TV event set; it never grants reads.
	clientDiagnostics ClientDiagnosticService
	// restart wires POST /v1/system/restart (§9.2, V13); nil ⇒ 501. Nil is the honest
	// answer for a handler built without main's generation loop behind it (tests, the
	// integration harness): a button that silently does nothing is worse than none.
	restart RestartService
	// restartDrift names restart-scoped settings whose saved value differs from the one
	// this application generation is running (config-design §3). nil ⇒ no drift is reported, which is
	// correct for a handler with no config seam rather than a false "restart needed".
	restartDrift func() []string
	// settings wires /v1/settings* + secrets regeneration (config-design §8);
	// nil ⇒ routes 501. Implemented by a thin adapter over settings.Service.
	settings                 SettingsService
	notificationDestinations NotificationDestinationService
	webPushPublicKey         string
	proposalNotifications    suggest.ProposalNotifier
	decisionQuality          ProposalDecisionQuality
	// backendTransition owns the durable setting mutation -> prepare -> publish -> retire
	// workflow for playout backend and URL changes. It serializes that entire workflow
	// across replicas; failures after mutation remain non-fatal follow-on failures.
	backendTransition BackendTransitioner
	backendCheckpoint func(context.Context) (BackendCheckpoint, error)
	// provision wires /v1/setup/bootstrap + /v1/users/import (§11); nil ⇒ routes
	// absent. Implemented by auth.Provisioner.
	provision Provisioner
	// approver is the one proposal -> titles + channel gate shared with every
	// automatic approval path (§7/§8). The API owns authorization and presentation;
	// the coordinator owns the indivisible domain transition.
	approver ProposalApprover
	// binder serves the explicit channel creation helpers. Proposal approval does
	// not call it directly; doing so would split the local transaction again.
	binder ChannelBinder
	// liveConfig reads a setting's live resolved value (config-design §3 hot-apply).
	// The composition root always-constructs the feature services and passes this so
	// a route gates on the CURRENT config (a saved connection enables it with no
	// restart, §8.1). Nil in unit tests that wire deps directly — then the nil-dep
	// check alone gates, preserving the old contract.
	liveConfig func(key string) string
	// libraryConfigured resolves the complete live media-server connection from one settings
	// snapshot. A domain-specific seam keeps handlers from independently reading flavor, URL,
	// and token and accidentally observing a mixed generation during a concurrent save.
	libraryConfigured func() bool
	// ready is the same readiness /readyz reports, surfaced through the typed
	// /v1/system/version so the UI can show it without an untyped fetch. nil ⇒ ready.
	ready          ReadyFunc
	startupReports StartupReportService
	healthRefresh  HealthRefreshService

	liveConfigInt func(key string) int
	// liveConfigBoolOn reads a BOOL setting whose safe answer is true (resolved.boolOn).
	//
	// ⚠ Bool keys must never go through liveConfig: settings.String PANICS on a non-string
	// Kind, so `liveConfig("filler.source.folder.enabled")` killed the request rather than
	// returning anything a comparison could inspect. A dedicated reader makes the Kind
	// mismatch impossible to write instead of leaving the string accessor as the only tool.
	//
	// It is boolOn, not boolv, deliberately: nil ⇒ true, matching the syncer's own gate and
	// the declared default. The keys read here answer "why is my catalog empty", so an
	// unanswerable read must not render the drop-folder as switched off.
	liveConfigBoolOn func(key string) bool
	// guide routes now/next to the backend streaming each channel (§9.1); nil ⇒ reads empty.
	guide GuideReader
	// playoutObserver supplies operational snapshots and program progress (§9.1, §12).
	playoutObserver PlayoutObserver
	// preparedObserver supplies the readiness planner's immutable operational snapshot.
	preparedObserver PreparedObserver
	// playoutSecret reads the generated `playout_token` (§11 device auth). A func rather
	// than the value so a REGENERATED token takes effect without a restart — rotation is
	// an operator action the UI offers, and a cached value would keep authorizing the old
	// one. Nil ⇒ every playout route 404s (fail closed, never serve unauthenticated).
	playoutSecret func() string
	// playoutSecretCurrent is the durable Postgres-replica read. Production prefers it at
	// request security boundaries so a rotation handled by another process invalidates the
	// old token and admits the new one immediately. Tests and SQLite use playoutSecret.
	playoutSecretCurrent func(context.Context) (string, error)
	// playoutResolver answers "what is airing now" for /playout/program (§9.1) — the
	// finite-block layer the Go supervisor re-opens per program. nil ⇒ the route 501s.
	playoutResolver PlayoutResolver
	// playoutEncoder starts one supervised ffmpeg. Injected so the program handler is
	// testable without executing a binary; the composition root passes playout.Start.
	playoutEncoder PlayoutEncoder
	// playout is the one playback seam for MPEG-TS and HLS (§9.1 V56).
	playout Playout
	// playoutGuide resolves programme timelines for /playout/guide.xml (§9.1, V6b);
	// nil ⇒ the route 501s.
	playoutGuide PlayoutGuide
	// timelineThumbs resolves a TMDB preview image per programme for the Watch timeline (§9.1 V47).
	// nil ⇒ the strip renders with no images (TMDB optional); it never fails the timeline.
	timelineThumbs TimelineThumbResolver
	// playoutFont labels the offline card; nil ⇒ unlabelled, never a failed encode.
	playoutFont func() string
	// playoutTonemap reports whether this ffmpeg build can tone-map HDR→SDR; nil ⇒ no, so an HDR
	// source transcodes untone-mapped rather than emitting a filter the build would reject at
	// graph-init. Same fail-safe direction as playoutFont, for the same reason.
	playoutTonemap func() bool
	// reclaimVRAM frees GPU memory the encoders need — in practice, evicts the resident local LLM
	// (§8.2 Evictor, §9.1 V47 retry ladder). Called ONLY when a hardware encode has already produced
	// nothing, so the common case never touches it. Nil ⇒ no local LLM to reclaim (a hosted provider,
	// or none), and the ladder skips straight from hardware to the software fallback.
	reclaimVRAM func(ctx context.Context)
	// residentLLMVRAM reports the VRAM the LLM ACTUALLY holds right now + its model tag, from a live
	// Ollama /api/ps probe (§9.1 V47 doctor). The doctor uses it for the true contention picture,
	// replacing an on-disk-size estimate that misreported an unloaded model as resident. Returns
	// (0, "") when nothing is resident or the provider is hosted; nil ⇒ the doctor omits the header.
	residentLLMVRAM func(ctx context.Context) (gib float64, model string)
	// encodePool is the one host-wide hardware-encode admission boundary shared with preparation.
	// Nil preserves the pre-gate behavior for tests and installs without a capability probe.
	encodePool *media.EncodePool
	// schemaOnly is set ONLY by ExportOpenAPI (§7.1): it makes the register* funcs
	// emit every operation's SCHEMA into the spec even when its live service is nil,
	// so the exported `api/openapi.yaml` is complete (auth, bootstrap, import, sync)
	// and orval can type the whole surface. Handlers are never invoked during export,
	// so a nil service behind a registered op is harmless. Runtime nil-guarding is
	// unchanged — at runtime schemaOnly is false and an absent service still 404s.
	schemaOnly bool
}

// featureOff reports whether a named feature (config-design §7) is configured-off
// right now. Safe when settings is unset (unit tests) — reports false so a wired
// dep still serves.
func (s *Server) featureOff(ctx context.Context, feature string) bool {
	return s.settings != nil && !s.settings.Features(ctx)[feature]
}

// unconfigured reports whether a required setting is empty right now. Safe when
// liveConfig is unset (unit tests) — reports false so a wired dep still serves.
func (s *Server) unconfigured(keys ...string) bool {
	if s.liveConfig == nil {
		return false
	}
	for _, k := range keys {
		if s.liveConfig(k) == "" {
			return true
		}
	}
	return false
}

// libraryUnconfigured gates every optional media-library operation on the same complete,
// atomic {flavor,url,token} predicate. Nil preserves the unit-test convention that an
// explicitly wired adapter is usable without a settings runtime.
func (s *Server) libraryUnconfigured() bool {
	return s.libraryConfigured != nil && !s.libraryConfigured()
}

// SettingsService is the settings surface the API depends on (config-design §8).
// Implemented by a settings.Service adapter in the composition root; abstracted
// so the API package needn't import internal/settings. Secrets are masked here —
// a value never crosses this boundary (§4).
type SettingsService interface {
	// List returns every operator-managed setting with its resolved value (secrets
	// masked) + provenance + audit metadata (config-design §8).
	List(ctx context.Context) []SettingEntry
	// Patch applies per-key edits, returning a per-key result (saved | invalid |
	// pinned). Hot-applies on success. updatedBy is the admin's id (§3 audit).
	Patch(ctx context.Context, edits map[string]string, updatedBy string) []SettingResult
	// Clear drops one key's stored override (reverts to env/default) — the explicit
	// clear, and the only way to unset a secret (config-design §8/§9). The result
	// status maps to HTTP: invalid → 404, pinned → 409, saved → 204.
	Clear(ctx context.Context, key string) SettingResult
	// SetEnvOverride takes a key back from the environment, or hands it back
	// (config-design §3.1). Status maps to HTTP: unknown → 404, not_pinned → 409,
	// applied → 204. updatedBy is the admin's id, so the field's existing
	// "changed by … · when" line reports who overrode the deploy config.
	SetEnvOverride(ctx context.Context, key string, on bool, updatedBy string) SettingResult
	// Features returns the computed feature availability (config-design §7).
	Features(ctx context.Context) map[string]bool
	// RegenerateSecret rotates a generated API or playout token and returns the new
	// value (config-design §4).
	RegenerateSecret(ctx context.Context, name string) (value string, err error)
	// RevealSecret returns a generated API or playout token's CURRENT value
	// (config-design §4's eye-toggle). Reading must never rotate.
	RevealSecret(ctx context.Context, name string) (value string, err error)
	// Test runs one named connection check (config-design §8, powers Test buttons).
	Test(ctx context.Context, check string) (ok bool, hint string)
}

type NotificationDestinationService interface {
	Create(context.Context, notifications.Principal, notifications.DestinationCommand) (notifications.DestinationSummary, error)
	List(context.Context, notifications.Principal) ([]notifications.DestinationSummary, error)
	Update(context.Context, notifications.Principal, string, notifications.DestinationUpdateCommand) (notifications.DestinationSummary, error)
	Delete(context.Context, notifications.Principal, string, string) (notifications.DestinationDeleteResult, error)
	Test(context.Context, notifications.Principal, string, string) (notifications.DestinationTestResult, error)
}

// BackendTransitioner is the one deep settings consequence for playout publication. The
// implementation holds its cross-replica lock while invoking mutation and all publication phases.
// mutation returns whether it durably changed a transition input; false skips transition repair.
type BackendTransitioner interface {
	ApplyMutation(ctx context.Context, mutation func(context.Context) bool) error
	// Reconnect force-repairs the durably applied tuner/listing target under the same
	// cross-replica serialization boundary as ordinary backend publication.
	Reconnect(ctx context.Context) (tunersReset int, err error)
}

// SettingEntry is the API view of one setting (config-design §8). For a secret,
// Value is empty and Set/Preview carry the masked state — the value never appears.
// SettingEnumOption is one choice of an enum setting: the stored value + its display
// label (config-design §5). The label is registry-owned so the UI never re-derives it.
type SettingEnumOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

type SettingEntry struct {
	Key          string              `json:"key"`
	Label        string              `json:"label,omitempty" doc:"Registry-owned human label for workflow forms."`
	Group        string              `json:"group"`
	Kind         string              `json:"kind"`
	Apply        string              `json:"apply" enum:"live,restart" doc:"When a saved value becomes active: live on the next owning operation, or restart in the next app generation."`
	Presentation string              `json:"presentation,omitempty" doc:"Human editor semantics beyond kind, e.g. bytes, path, or language."`
	Value        string              `json:"value,omitempty" doc:"Resolved value (non-secret). Empty for secrets."`
	Set          bool                `json:"set" doc:"For secrets: whether a value is stored."`
	Preview      string              `json:"preview,omitempty" doc:"For secrets: masked '…a1b2' tail (§4)."`
	Provenance   string              `json:"provenance" enum:"env,db,default" doc:"env locks the UI field (§3)."`
	Caution      bool                `json:"caution,omitempty" doc:"A stored value self-healed to default (§3)."`
	EnvPinnable  bool                `json:"envPinnable,omitempty" doc:"The environment sets this key, so it can be taken back with the unlock (§3.1). Only meaningful together with provenance/envOverride."`
	EnvOverride  bool                `json:"envOverride,omitempty" doc:"An admin took this key back from the environment (§3.1): the env var is set but deliberately not winning. Reported alongside provenance — such a key resolves honestly as 'db' — so the UI can say 'overriding SEERR_URL' rather than implying the variable is unset."`
	EnvVar       string              `json:"envVar,omitempty" doc:"The environment variable that pins (or would pin) this key — named so the UI can tell the operator exactly what it is overriding, and what to unset to hand it back."`
	Advanced     bool                `json:"advanced"`
	Secret       bool                `json:"secret"`
	Enum         []string            `json:"enum,omitempty" doc:"Enum values (the closed set) — labels are in enumOptions."`
	EnumOptions  []SettingEnumOption `json:"enumOptions,omitempty" doc:"Enum choices with display labels (config-design §5)."`
	ShowWhen     map[string][]string `json:"showWhen,omitempty" doc:"Conditional visibility: show only when a named key's current value is listed (§5)."`
	RequiredFor  string              `json:"requiredFor,omitempty"`
	Doc          string              `json:"doc"`
	UpdatedBy    string              `json:"updatedBy,omitempty"`
	UpdatedAt    string              `json:"updatedAt,omitempty" doc:"RFC3339; empty for env/system writes."`
}

// SettingResult is one key's PATCH outcome (config-design §8).
type SettingResult struct {
	Key     string `json:"key"`
	Status  string `json:"status" enum:"saved,invalid,pinned" doc:"saved | invalid(problem) | pinned (§8)."`
	Problem string `json:"problem,omitempty" doc:"Validation message when status=invalid (never echoes a secret, §4)."`
}

// FillerService backs the filler ingestion routes (§10): catalog sync and the AI
// tagging job. Implemented by filler.Syncer + filler.Tagger. list/patch read/write
// the store directly (no service needed).
type FillerService interface {
	// Readiness is the one server-owned operational summary used by the Filler overview.
	Readiness(ctx context.Context) (filler.Readiness, error)
	// Sync reconciles the clip catalog from the media server's filler library.
	Sync(ctx context.Context) (total, added, updated, pruned int, err error)
	// Fetch runs one bounded acquisition pass over enabled remote sources. It is the same
	// operation the scheduled filler-fetch job runs; the Sources page uses it to run that policy
	// now rather than merely scanning local files and claiming a remote fetch succeeded.
	Fetch(ctx context.Context, sourceID string) (filler.FetchResult, error)
	// Tag runs AI classification over untagged commercials.
	Tag(ctx context.Context) (considered, tagged, partial, skipped int, err error)
	// Ingest downloads clips from the given source URLs into the drop-folder, returning
	// a job id immediately (§10). Downloads take minutes to hours, so this is
	// fire-and-report: progress arrives on the SSE bus as `filler_ingest` frames, the
	// same shape the model pull uses (§8.1). ErrIngestUnavailable when the running image
	// lacks the tooling.
	//
	// ⚠ Downloads and nothing else — it does not register a source.
	Ingest(ctx context.Context, urls []string) (jobID string, err error)
	// IngestPull retains the approval id and each plan row's registered source attribution.
	IngestPull(ctx context.Context, pullID string, targets []filler.AcquisitionTarget) (jobID string, err error)
	// IngestAsked is Ingest plus "remember this source", for `POST /v1/filler/ingest` only.
	//
	// ⚠ The split is a correctness fix: registration used to live inside Ingest, and auto-fetch
	// passes the URL of every ITEM inside an already-registered collection — so one fetch
	// registered 30 clips as 30 sources. Only the caller knows which it holds.
	IngestAsked(ctx context.Context, urls []string) (jobID string, err error)
	// Discover searches archive.org for candidate clips WITHOUT downloading anything (§10,
	// V33). One search request whatever the result count — the operator is deciding what to
	// fetch, and making them download gigabytes to find out would defeat the point.
	//
	// ⚠ Unlike Ingest this is SYNCHRONOUS: it is one HTTP call to a public JSON API, fast
	// enough to answer in the request, so there is no job to report on. That asymmetry is
	// deliberate — a job id for a sub-second read would be ceremony the caller has to poll.
	Discover(ctx context.Context, query string, limit int) ([]DiscoveredClip, int, error)
	// DiscoverCollection lists ONE named collection, downloading nothing (§10, V17d). Same
	// contract as Discover — synchronous, listing-only — with the collection as the argument
	// instead of a keyword. This is the starter pack: a curated collection an operator keeps
	// or excludes from before anything is fetched, so a suggested pack and a search result
	// travel the same path rather than the pack growing its own acquisition route.
	//
	// `ref` accepts a full URL, a /details/<id> path, or a bare identifier — the spellings
	// Ingest already takes, so an operator pasting a URL need not know which form is wanted.
	// When query is non-empty, results are filtered within the named collection. An empty query
	// lists the collection, preserving the starter-pack browse path.
	DiscoverCollection(ctx context.Context, ref, query string, limit int) ([]DiscoveredClip, int, error)
	// EnrichDiscovered fills in duration + quality for specific results, ON DEMAND.
	//
	// ⚠ Separate from Discover because it is a DIFFERENT cost, measured: a listing is one
	// request, while these fields are one `/metadata/<id>` call per row — 22.6s for a page of
	// 25 against the live API, and worse if the fan-out is widened, because archive.org
	// throttles. So a client asks only for the rows a person is actually looking at.
	//
	// Returns what it learned, keyed by id; an id it could not answer for is simply absent
	// rather than zero, because 0 means "unknown" to every caller and a map entry saying 0
	// would be indistinguishable from a genuinely empty clip.
	EnrichDiscovered(ctx context.Context, ids []string) (map[string]DiscoveredClipStats, error)
	// Split proposes cuts for a compilation clip (§10, V34). Fire-and-report like
	// Ingest — detection runs minutes per file, so progress arrives on the SSE bus
	// as `filler_split` frames and the finished proposal is read back via
	// GET /v1/filler/splits/{id}. The clip must exist (ErrNotFound otherwise).
	Split(ctx context.Context, clipID string) (jobID string, err error)
	// ConfirmSplit commits the operator's reviewed cut list (§10, V34): the ONLY
	// path by which proposal segments become catalog clips. Synchronous — stream
	// copy cuts seek rather than decode, so this is seconds, not the detection's
	// minutes. filler.ErrSplitValidation ⇒ 422; a missing proposal ⇒ ErrNotFound.
	ConfirmSplit(ctx context.Context, proposalID string, segments []filler.SplitSegment) error
}

// TaxonomyEditor is the deep graph-edit module used by taxonomy writes. The store still owns the
// atomic graph transaction; this seam adds an authoritative preview and post-commit channel
// convergence without making HTTP handlers understand either implementation.
type TaxonomyEditor interface {
	Preview(ctx context.Context, edit store.TaxonomyEdit) (TaxonomyImpact, error)
	Apply(ctx context.Context, edit store.TaxonomyEdit) (TaxonomyImpact, error)
}

type TaxonomyImpact struct {
	Store    store.TaxonomyEditImpact
	Channels []TaxonomyChannelImpact
}

type TaxonomyChannelImpact struct {
	ID     string
	Name   string
	Number int
}

// FillerRewinder is the optional recovery seam behind the Incoming UI. Separate from
// FillerService so a runtime without a pipeline reports 501 and lightweight callers need not fake
// a recovery operation they cannot perform.
type FillerRewinder interface {
	Rewind(ctx context.Context, hash string, from filler.StageID, force bool) error
	RetryFailure(ctx context.Context, hash string) error
}

// DiscoveredClip is one candidate the operator could add (§10, V33).
//
// ⚠ NOT a ClipDTO: nothing has been downloaded, so it has no path and no tags — only what the
// source knows about it. Reusing ClipDTO would advertise fields that are structurally
// unavailable at this stage.
//
// ⚠ DurationMS and Height are absent from a SEARCH and filled by a follow-up enrich call (V35),
// so a client must treat them as arriving late rather than as missing. They are on this struct
// rather than a separate type because a row gains them in place.
type DiscoveredClip struct {
	// ID is the source's identifier — what Ingest would be given to fetch it.
	ID string `json:"id"`
	// Title as the source has it; may be empty, in which case a client shows the id.
	Title string `json:"title,omitempty"`
	// Year the source catalogued it under; 0 when it declares none, which is common.
	//
	// ⚠ A WEAK hint, never a clip's era. It is when the ITEM was catalogued — for a
	// compilation upload that is often the upload year, not the broadcast year — which is the
	// same trap §10 records for yt-dlp's `upload_date`.
	Year int `json:"year,omitempty"`
	// URL is the item's page, so an operator can look at it before adding it.
	URL string `json:"url"`
	// Date is the source's catalogued date (RFC3339), absent when it declares none.
	//
	// ⚠ Carries Year's weak-hint caveat, and the live data shows it: one pinned item is dated
	// 1996 while archive.org's `publicdate` for it is 2023. Rendered for a human to read;
	// never used to set a clip's era.
	Date string `json:"date,omitempty"`
	// DurationMS is the item's runtime. ⚠ ABSENT means unknown, never zero-length — archive.org
	// has not probed every item's files, and a client must render "—" rather than "0:00", which
	// would claim the clip is empty. Costs a per-item metadata call (see clipfetch.enrich).
	DurationMS int `json:"durationMs,omitempty"`
	// Height is the best available derivative's vertical resolution, rendered as a quality hint
	// ("480p"). Absent means unknown, on the same terms as DurationMS.
	Height int `json:"height,omitempty"`
	// ThumbnailURL is archive.org's own item thumbnail. Free — a stable URL pattern needing no
	// API call — which is why it is built here rather than fetched.
	ThumbnailURL string `json:"thumbnailUrl,omitempty"`
}

// DiscoveredClipStats is what a per-item metadata call learns about one result (§10, V35) —
// the two fields a search cannot afford to include.
//
// ⚠ A separate type rather than reusing DiscoveredClip, because the enrich response must not be
// able to CONTRADICT the search: returning whole clips would let a second call disagree about a
// title or a URL the client already rendered, and there is no rule for which one wins. This
// carries only what the metadata call is authoritative for.
type DiscoveredClipStats struct {
	// DurationMS is the runtime. ⚠ Only ever present when known: an id archive.org has not
	// probed is OMITTED from the map, never returned as 0, since 0 and "unknown" render
	// differently and only one of them is true.
	DurationMS int `json:"durationMs,omitempty"`
	// Height is the best derivative's vertical resolution — the "480p" quality hint.
	Height int `json:"height,omitempty"`
}

// ErrIngestUnavailable reports that this install cannot run the ingest tooling. It is NOT
// a configuration error — no setting can assert that a binary executes — which is why the
// API renders it as a distinct problem type rather than feature_not_configured.
//
// ⚠ This used to mean "you are on loomarr:latest, run loomarr:filler instead". The single
// image always ships the tooling (§16), so it now means a DEGRADED install: a custom build
// without the vendored binaries, or a path that is missing or not executable.
var ErrIngestUnavailable = errors.New("ingest tooling not present in this image")

// ErrSplitUnavailable reports that compilation splitting cannot run because the
// filler drop-folder (filler.dir) is unset — clip paths are relative to it, so
// without it there is nothing to cut. Unlike ErrIngestUnavailable this IS a
// configuration remedy: Settings → Filler opens it (§10, V34).
var ErrSplitUnavailable = errors.New("splitting needs the filler drop-folder (filler.dir)")

// PodPreviewer answers "what commercials would this channel get" without touching
// Tunarr (§12). Implemented by filler.PodAdapter, which serves preview and the
// reconciler from ONE code path — a preview that could diverge from what reconcile
// attaches would be worse than none. nil ⇒ the route 501s.
type PodPreviewer interface {
	// Preview takes only a channel id: the selection + seed are derived by the adapter
	// using the SAME exported helpers the reconciler uses (channels.SelectionForChannel/
	// PodSeed), so the API cannot accidentally preview a differently-seeded pod than the
	// one that ships. This is the SAVED-state preview (GET …/pods).
	Preview(ctx context.Context, channelID string) (filler.Pod, error)
	// PreviewDraft assembles the pool for an UNSAVED draft selection (POST …/pods/preview,
	// the sandbox), through the same assembler + seed — only the selection differs. It's
	// what lets the channel page show exactly what a filler change would air before Apply.
	PreviewDraft(ctx context.Context, channelID string, sel filler.Selection) (filler.Pod, error)
	// PreviewAt assembles the pod for the break STARTING AT a specific instant — the guide's
	// per-airing composition (§12 hover card), and what internal playout actually airs.
	//
	// Separate from Preview because the seed differs: Preview answers "what pool does this
	// channel draw from", while this answers "what plays in THIS break". Consecutive breaks
	// must not replay the same adverts, which is only expressible with the start time.
	PreviewAt(ctx context.Context, channelID string, breakStartMs int64) (filler.Pod, error)
	// Coverage reports which ladder rung this channel's breaks would draw from, and how much
	// material each rung holds (V29a/V29b). Same catalog, same Window, same policy as Preview —
	// it counts what is available instead of drawing from it, which is why it takes no seed.
	//
	// On the SAME interface as the previews deliberately: the meter's whole reason to exist is
	// that it agrees with what airs, and splitting it onto its own service is the first step
	// toward two implementations of "what would this channel get".
	Coverage(ctx context.Context, channelID string) (filler.CoverageReport, error)
	// CoverageDraft is Coverage for an UNSAVED selection — the draft half of the sandbox
	// `PreviewDraft` already provides for pods (§10 V51f).
	//
	// ⚠ It exists because the meter and the pod timeline below it were describing different
	// selections during an edit: the meter read the saved policy, the timeline the draft. Both
	// now come from one selection in one response, which is the same "cannot disagree" property
	// `Coverage` was added for in the first place, applied to the editing path.
	CoverageDraft(ctx context.Context, channelID string, sel filler.Selection) (filler.CoverageReport, error)
	// ClipFit reports how ONE clip relates to EVERY channel's selection (§10 V35 item 1.7) —
	// the override picker's per-channel note, keyed by channel id.
	//
	// On this interface for the same reason Coverage and Pool are: the answer must be the one
	// assembly would give. A note reading "exact match" beside a channel whose breaks never
	// play the clip is the confident-wrong-answer failure `channelpreview.go` records the v2
	// mock's own meter committing.
	//
	// ⚠ CLIP-centric, not channel-centric: the picker shows one clip against every channel, so
	// a per-channel endpoint would be N requests to render one card.
	ClipFit(ctx context.Context, clipPath string) (map[string]filler.Fit, error)
	// Pool reports catalog-wide filler health for the Filler page's pool strip (§10 V35):
	// how much material exists, and what every live channel's breaks currently resolve to.
	//
	// On this interface for the same reason Coverage is: its per-channel numbers ARE Coverage,
	// called once per channel. An aggregate that computed its own would be a second opinion
	// about the ladder, and the two would disagree on exactly the pages an operator compares
	// when a channel looks wrong.
	Pool(ctx context.Context) (filler.PoolReport, error)
}

// GuideReader answers "what is airing now" from whichever backend streams each channel.
// Keys and arguments are always Loomarr channel ids; an adapter that reads a remote backend
// owns the translation to that backend's id. nil ⇒ now/next reads empty.
type GuideReader interface {
	// NowNext returns, per Loomarr channel id, the program airing at `now` and the one
	// following it. A channel with no generated guide is simply absent.
	NowNext(ctx context.Context, now time.Time) (map[string]ChannelNowNext, error)
	// Upcoming returns, for one Loomarr channel, the program airing now (if any) followed by
	// the next programs in airtime order, up to `limit` entries; commercial/flex gaps are
	// skipped (§9 guide freshness). Powers the Overview "what's on later" strip. An unknown
	// id / empty guide yields an empty slice, not an error.
	Upcoming(ctx context.Context, channelID string, now time.Time, limit int) ([]NowNextEntry, error)
}

// SuggestService is the suggestion surface the API depends on (§8). Implemented
// by suggest.Service + the store; abstracted so the API doesn't couple to the
// worker internals.
type SuggestService interface {
	// Submit enqueues a suggestion job for an intent and returns the job id. It takes
	// the DOMAIN intent rather than flat args: the flat form was "dependency-light",
	// but this package already imports suggest (ProposalDTO carries suggest.Proposal),
	// and the flat mirror had silently dropped RuntimeTgt — a knob the suggester
	// honors (suggester.go prompt + scoring) that no client could set.
	Submit(ctx context.Context, intent suggest.Intent, createdBy string) (jobID string, err error)
	// Refine re-runs an existing suggestion job (the channel's IntentRef) with a
	// refine-flavored intent, so the new proposal binds back to the same channel
	// (§7 POST /v1/channels/{id}/refine). Returns the job id to poll.
	Refine(ctx context.Context, jobID string, intent suggest.Intent) (string, error)
}

// SearchService backs GET /v1/search (§7.2) — the SAME catalog impl as the LLM
// grounding tool. Returns candidates as generic maps so the API layer needn't
// import the catalog types (kept dependency-light).
type SearchService interface {
	Search(ctx context.Context, request SearchRequest) ([]SearchCandidate, error)
}

// SearchRequest selects exactly one Catalog operation. Query uses federated title
// search; Discovery uses the provider-neutral structured discovery path. The API
// handler rejects requests that provide both or neither.
type SearchRequest struct {
	Query     string
	MediaType string
	Scope     string
	Limit     int
	Discovery *SearchDiscovery
}

// SearchDiscovery is the dependency-light API representation of Catalog's
// structured discovery request (§7.2). The composition-root adapter is the sole
// conversion seam into catalog.DiscoveryQuery.
type SearchDiscovery struct {
	MediaType        string
	Keywords         []string
	Genres           []string
	YearFrom         int
	YearTo           int
	OriginalLanguage string
	OriginCountry    string
	RuntimeMin       int
	RuntimeMax       int
	VoteAverageMin   float64
	VoteCountMin     int
	Network          string
	Cast             []string
	Creators         []string
}

// CollectionService backs GET /v1/library/collections — the media server's collections
// (Emby/Jellyfin BoxSets) that back `scope.collections` (programming-design §2.2).
//
// ⚠ A LIST, not a search. Collections are a small closed set the operator curates by hand,
// so the picker shows all of them; there is no query parameter because there is nothing to
// narrow. This is why it is not a `scope` on /v1/search — a Candidate models a provisionable
// title and a collection is not one, the same distinction that keeps clips off that route.
type CollectionService interface {
	Collections(ctx context.Context) ([]LibraryCollection, error)
}

// LibraryCollection is the API view of one media-server collection. The ID is opaque and
// server-local — it is what `scope.collections` stores — so the Name is carried alongside it
// purely so the picker can show a human something. ⚠ Never a TMDB collection (franchise) id;
// see programming-design §2.2 on the two namespaces sharing one English word.
type LibraryCollection struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	ChildCount int    `json:"childCount,omitempty"`
}

// SearchCandidate is the API view of a catalog candidate (§7.2). It carries the
// same source-backed editorial and enforcement evidence as the LLM grounding
// tool, so the doc's "humans and the model see identical results" promise (§8)
// holds on the read side too.
type SearchCandidate struct {
	MediaType        string   `json:"mediaType"`
	TMDBID           int      `json:"tmdbId,omitempty"`
	TVDBID           int      `json:"tvdbId,omitempty"`
	Name             string   `json:"name"`
	Year             int      `json:"year,omitempty"`
	InLibrary        bool     `json:"inLibrary"`
	LibraryItemID    string   `json:"libraryItemId,omitempty"`
	Genres           []string `json:"genres,omitempty"`
	Overview         string   `json:"overview,omitempty"`
	OriginalLanguage string   `json:"originalLanguage,omitempty"`
	OriginCountries  []string `json:"originCountries,omitempty"`
	RuntimeMinutes   int      `json:"runtimeMinutes,omitempty"`
	VoteAverage      float64  `json:"voteAverage,omitempty"`
	VoteCount        int      `json:"voteCount,omitempty"`
	Keywords         []string `json:"keywords,omitempty"`
	Networks         []string `json:"networks,omitempty"`
	Cast             []string `json:"cast,omitempty"`
	Creators         []string `json:"creators,omitempty"`
	OfficialRating   string   `json:"officialRating,omitempty"`
}

// IconService backs GET /v1/channels/{id}/icon-suggestions (§icon P2): candidate
// channel-icon images drawn from the channel's OWN lineup — a Star Trek channel
// offers its five series' posters, so the operator picks a themed icon without
// leaving the channel page. Implemented by an adapter over the store + *tmdb.Client;
// abstracted so the API package needn't import tmdb. nil ⇒ the route 501s (TMDB
// unconfigured — matching every other TMDB-dependent feature's gate).
type IconService interface {
	// IconSuggestions returns candidate poster URLs for one channel, best-effort:
	// a lookup failure on one lineup title is skipped, never fails the whole call
	// (§icon — one bad title must not deny every other suggestion).
	IconSuggestions(ctx context.Context, channelID string) ([]IconSuggestion, error)
}

// IconSuggestion is one candidate channel icon: a lineup title's display name paired with the
// poster's URL on THIS instance's image service.
//
// ⚠ **`url` stopped being a TMDB URL in V52 phase 7.** It used to point at TMDB's CDN, which meant
// the icon picker — one of the three surfaces an operator looks at most — loaded a dozen
// third-party images in their browser. The poster is now adopted, fetched server-side, and served
// from our own disk (§22). The field keeps its name and its job: it is what the channel's `logo`
// is set to when the operator picks this suggestion.
type IconSuggestion struct {
	Title string `json:"title" example:"Star Trek: The Next Generation"`
	URL   string `json:"url" example:"https://loomarr.example/v1/images/9f2b1c4e/w500.jpg" doc:"Image-service URL for the poster; becomes the channel's logo when picked"`
	// ImageHash links the suggestion to its image record. Carried separately from `url` because
	// the handler resolves it to `image` in one deduplicated pass — see imageDTOsByHash.
	ImageHash string `json:"imageHash,omitempty" doc:"Content hash of the poster's image record"`
	// Image is the full record the frontend's <Image> primitive needs: real width/height, the
	// ThumbHash, and both srcsets. Absent only if the record disappeared between listing and
	// enrichment, in which case the plain `url` still renders.
	Image *ImageDTO `json:"image,omitempty" doc:"The poster's image record, for srcset and placeholder rendering"`
}

// ChannelService is the channel-management surface the API depends on (§9).
// Implemented by channels.Engine + the store; abstracted so the API doesn't
// couple to the reconcile internals.
type ChannelService interface {
	// Reconcile converges one channel on its selected playout backend (§9,
	// POST /v1/channels/{id}/reconcile).
	Reconcile(ctx context.Context, channelID string) error
	// Purge hard-deletes Loomarr's channel state plus any retained managed Tunarr projection —
	// the DELETE /v1/channels/{id}?purge=true path (§7).
	Purge(ctx context.Context, channelID string) error
	// CyclePreviewDraft computes "what would air at `at`" for one channel WITHOUT touching
	// Tunarr or the store beyond the read — the §8.1 time-travel preview. A draftLineup /
	// draftPolicy (nil = use the saved value) stand in for the channel's own, so the editor
	// previews what an edit WOULD air before applying it. A zero `at` means "now". Read-only;
	// nothing persists.
	//
	// ⚠ Both preview endpoints go through this ONE method — GET …/cycle passes nil drafts.
	// `Engine.CyclePreview` still exists for the playout adapter (which wants only the slots
	// and a cache around them), but the API does not declare it: it was the saved-preview path
	// until #263, and routing that through here is what lets the SAVED preview report what the
	// §4 filters refused, not just the mid-edit one.
	CyclePreviewDraft(ctx context.Context, channelID string, at time.Time,
		draftLineup []schedule.LineupEntry, draftPolicy *schedule.ChannelPolicy) (channels.CycleResult, error)
}

// LiveTVService backs the Live TV setup status check (§6/§7). Mutating repair belongs to
// BackendTransitioner so it cannot bypass durable publication ordering.
type LiveTVService interface {
	Wired(ctx context.Context) (bool, error)
}

// TunerRescanner is the operation-specific media-server freshness seam from §9. A guide refresh
// cannot remove a channel from an already-registered M3U tuner; the tuner itself must be re-read.
type TunerRescanner interface {
	RescanTuner(ctx context.Context) error
}

// TunarrConnector wires the media server as *Tunarr's* media source (§6) so Tunarr
// can stream + index the library — backs POST /v1/setup/tunarr-connect and the
// tunarr_library setup check. Idempotent. Implemented by setup.MediaSourceConnector.
type TunarrConnector interface {
	Connect(ctx context.Context) (sourceID string, librariesEnabled int, err error)
	LibrariesReady(ctx context.Context) (bool, error)
}

// ProposalApprover is the complete approval gate. Its implementation plans the
// channel and commits proposal, titles, and channel atomically before post-commit
// runtime work; a handler cannot reproduce or reorder that choreography.
type ProposalApprover interface {
	Approve(ctx context.Context, p store.Proposal, edit *suggest.ApprovalEdit, approvedBy string) (suggest.ApprovalResult, error)
}

// ProposalDecisionQuality receives a denial only after its submitted-to-denied
// compare-and-swap commits. It cannot affect the HTTP decision outcome.
type ProposalDecisionQuality interface {
	ProposalDeclined(context.Context, string, time.Time)
}

// ChannelBinder resolves the legacy explicit POST /v1/channels intent helpers and
// shares channel-number occupancy with manually typed channel numbers. Approval
// uses ProposalApprover instead of reaching through this lower-level seam.
type ChannelBinder interface {
	LineupFromIntent(ctx context.Context, intentRef string) ([]schedule.LineupEntry, error)
	PolicyFromIntent(ctx context.Context, intentRef string) (schedule.ChannelPolicy, error)
	// NumberInUse answers the SAME question the approve path's numbering asks — is this
	// channel number free in Loomarr's store AND in Tunarr? — so an operator who types a
	// number gets the same answer the server gives itself when it picks one. Best-effort:
	// an unreachable Tunarr reports not-in-use rather than blocking a create.
	NumberInUse(ctx context.Context, number int) (bool, error)
}

// UserSyncer refreshes already-imported users from the media server (§11).
// Returns the count synced. It never ADDS users (import defines the allowlist).
type UserSyncer interface {
	Sync(ctx context.Context) (int, error)
}

// Provisioner owns first-run bootstrap + explicit import (§11). Together with
// PasswordService.CreateLocal these are the ONLY paths that create users — every
// one of them an explicit admin action, never a side effect of someone signing in.
// Implemented by auth.Provisioner.
type Provisioner interface {
	// Bootstrap creates the first local admin, once (ErrBootstrapClosed after).
	Bootstrap(ctx context.Context, username, password string) (store.User, error)
	// Import allowlists the named media-server user ids (admin-only).
	Import(ctx context.Context, ids []string) (int, error)
	// Candidates lists media-server accounts available to import, each flagged with
	// whether it is already allowlisted (§11). Read-only — listing never provisions.
	Candidates(ctx context.Context) ([]auth.Candidate, error)
	// Bootstrapped reports whether an owning admin exists — the one fact the
	// unauthenticated GET /v1/setup/state exposes (§7).
	Bootstrapped(ctx context.Context) (bool, error)
}

// LoginService verifies credentials and issues a session (Phase 9, §11).
type LoginService interface {
	Login(ctx context.Context, username, password, rateKey string) (token string, expires time.Time, u store.User, err error)
	// DevLogin issues a session for an existing admin with no credential (§11).
	// Only ever called when the server was started with LOOMARR_DEV_LOGIN=1 — the
	// route is not registered otherwise.
	DevLogin(ctx context.Context) (token string, expires time.Time, u store.User, err error)
	Disable(ctx context.Context, userID string) error
}

// PasswordService owns LOCAL credential mutations (§11). Kept separate from
// LoginService because verifying a credential and changing one are different
// authority: the first is what any signed-in user does constantly, the second is
// what an account owner (or an admin) does deliberately.
// Implemented by auth.PasswordService.
type PasswordService interface {
	// ChangePassword is the SELF path: proves the current password, then replaces it
	// and revokes every session for that user.
	ChangePassword(ctx context.Context, userID, current, next string) error
	// CreateLocal mints a local account — an allowlist row carrying an Argon2id hash.
	// Admin-only at the route layer; the same kind of explicit write as Import.
	CreateLocal(ctx context.Context, username, password string, role store.Role, quota int) (store.User, error)
	// ResetPassword is the ADMIN path: no current password (that's the point), so it
	// relies entirely on the caller's role. Deliberately not the same call as
	// ChangePassword — sharing one would make the "prove you know it" check optional.
	ResetPassword(ctx context.Context, targetUserID, next string) error
}

// InvitationService is the narrow administrator-facing seam of the Invitation module.
// Durable grant hashes and provider lookup mechanics remain behind it.
type InvitationService interface {
	Create(context.Context, invitation.CreateCommand) (invitation.Invitation, error)
	Get(context.Context, string) (invitation.Invitation, error)
	List(context.Context) ([]invitation.Invitation, error)
	Contact(context.Context, string) (contact.Address, error)
	Regenerate(context.Context, string, invitation.Conveyance) (invitation.IssuedGrant, error)
	Revoke(context.Context, string) error
}

// InvitationDeliveryService composes provider-neutral delivery work beside the Invitation
// lifecycle. Sending mail never creates or redeems an allowlist row.
type InvitationDeliveryService interface {
	SendEmail(context.Context, string, string) (notifications.DeliverySummary, error)
	LatestEmail(context.Context, string) (notifications.DeliverySummary, error)
}

// InvitationRedemptionService is the public credential-proof seam. Bearers are
// accepted only in request bodies and never enter a route, query, or durable DTO.
type InvitationRedemptionService interface {
	Preview(context.Context, string) (invitation.Invitation, error)
	RedeemLocal(context.Context, string, string) (string, time.Time, store.User, error)
	RedeemLibrary(context.Context, string, string, string) (string, time.Time, store.User, error)
}

// PasswordRecoveryService is the public, enumeration-safe local credential recovery seam.
// Bearers remain body-only and successful reset deliberately issues no replacement session.
type PasswordRecoveryService interface {
	Request(context.Context, string, string) error
	Preview(context.Context, string, string) (recovery.Record, error)
	Redeem(context.Context, string, string, string) error
}

// SessionManager revokes sessions (logout) and exposes them for admin review (§11).
type SessionManager interface {
	Revoke(ctx context.Context, token string) error
	List(ctx context.Context, userID string) ([]store.Session, error)
	RevokeHash(ctx context.Context, tokenHash string) error
}

// BackupStreamer streams a consistent DB snapshot (§16). Implemented by the
// SQLite backend via VACUUM INTO. Matches store.SQLiteBackuper's return type.
type BackupStreamer interface {
	StreamBackup(ctx context.Context, w io.Writer) error
}

// Options configures the API server.
type Options struct {
	Store                store.Store
	Auth                 Authorizer
	Log                  *slog.Logger
	Metrics              *metrics.Recorder
	BackupSQLite         BackupStreamer // nil ⇒ /v1/backup returns 501 (Postgres)
	Ready                ReadyFunc
	Login                LoginService                // /v1/auth/login + user disable (Phase 9); nil ⇒ routes absent
	Passwords            PasswordService             // /v1/auth/password + local account create/reset (§11); nil ⇒ routes absent
	Sessions             SessionManager              // /v1/auth/logout (Phase 9)
	UserSync             UserSyncer                  // POST /v1/users/sync (Phase 9); nil ⇒ route absent
	Invitations          InvitationService           // /v1/invitations* (§11); nil ⇒ routes absent
	InvitationRedemption InvitationRedemptionService // public preview and explicit activation (§11)
	PasswordRecovery     PasswordRecoveryService     // public local-password recovery (§11)
	// InvitationDelivery adds explicit email send/resend and provider-safe delivery summaries.
	InvitationDelivery InvitationDeliveryService
	// AccessPublicURL supplies the recipient-reachable browser origin. It is read
	// at grant issuance so a hot-applied setting takes effect without restart.
	AccessPublicURL func() string
	// Devices wires /v1/auth/device/* (§11, Shield P1); nil ⇒ routes absent.
	Devices *auth.DeviceManager
	// DeviceLimiter bounds pairing starts and code-approval attempts; nil ⇒ unlimited, which is
	// acceptable only in tests. Production wires it at composition beside Devices.
	DeviceLimiter    *auth.RateLimiter
	CookieSecure     string            // COOKIE_SECURE: auto|true|false (§11)
	TrustProxy       bool              // TRUST_PROXY: honor X-Forwarded-For/-Proto only when true (§11)
	DevLogin         bool              // LOOMARR_DEV_LOGIN=1 ⇒ mount POST /v1/auth/dev-login (§11); default false ⇒ route absent
	Pprof            bool              // LOOMARR_PPROF=1 ⇒ mount /debug/pprof/* (§7); default false ⇒ routes absent
	Channels         ChannelService    // /v1/channels* reconcile (Phase 10); nil ⇒ reconcile route absent
	LiveTV           LiveTVService     // /v1/setup/* (Phase 10); nil ⇒ setup routes absent
	TunerRescanner   TunerRescanner    // §9 channel-list freshness; nil ⇒ best-effort poke unavailable
	TunarrConnect    TunarrConnector   // /v1/setup/tunarr-connect + tunarr_library check (§6); nil ⇒ 501
	Suggest          SuggestService    // /v1/proposals submit (Phase 11); nil ⇒ submit route 501
	ProposalWorkflow ProposalWorkflow  // authoritative /v1/proposal-jobs Journey reads
	Search           SearchService     // /v1/search (Phase 11); nil ⇒ search route 501
	Collections      CollectionService // /v1/library/collections (§2.2); nil ⇒ route 501
	Icons            IconService       // /v1/channels/{id}/icon-suggestions (§icon P2); nil ⇒ 501
	// Images backs /v1/images* — the one pipeline every image travels (§22, V52). nil ⇒ the byte
	// route 404s and the record route reports the image absent, which is the honest answer for an
	// instance with no store behind it.
	Images          ImageService
	Events          EventSource             // /v1/events SSE (Phase 11); nil ⇒ route 501
	Shutdown        <-chan struct{}         // generation lifetime; closes SSE so http.Server.Shutdown can drain
	Filler          FillerService           // /v1/filler sync/tag (Phase 12); nil ⇒ those routes 501
	FillerDecisions *fillerdecision.Service // /v1/filler/decisions* (§10 V63)
	Pods            PodPreviewer            // /v1/channels/{id}/pods preview (§12); nil ⇒ 501
	Taxonomy        TaxonomyEditor          // taxonomy impact + committed channel convergence
	// FillerLayout is the immutable storage topology applied to this server generation. Its zero
	// value means filler storage is unavailable; saved desired values do not replace it mid-run.
	FillerLayout filler.Layout
	SystemLLM    SystemLLMService // /v1/system/llm* model selection (§8.1); nil ⇒ routes 501
	// Database backs /v1/system/database* — the SQLite→PostgreSQL migration stepper
	// (§18, V11). Production wires it on both backends so a reconnect can observe the
	// successful cutover; nil is reserved for embeddings without migration status.
	Database   DatabaseService
	Encryption EncryptionService
	// Backups backs /v1/system/backups* — listing, downloading and writing the backups
	// on disk (§16, V12). nil ⇒ routes 501 (a Postgres install wires it nil).
	Backups BackupsService
	// SSO backs /v1/auth/sso/* (§11, V8); nil ⇒ the routes are not mounted.
	SSO SSOService
	// Activity records Dashboard feed lines (§12, V32); nil ⇒ nothing is recorded.
	Activity *activity.Recorder
	// DiagnosticEvents backs the bounded admin/agent diagnostic timeline.
	DiagnosticEvents DiagnosticEventService
	// DiagnosticProcesses backs active/recent Process runs and bounded output.
	DiagnosticProcesses DiagnosticProcessService
	// DiagnosticBundles backs preview and on-demand redacted ZIP creation.
	DiagnosticBundles DiagnosticBundleService
	// DiagnosticCapture controls the recorder's process-local bounded debug window.
	DiagnosticCapture DiagnosticCaptureService
	// ClientDiagnostics accepts bounded curated events from authenticated web/TV clients.
	ClientDiagnostics ClientDiagnosticService
	// StartupReports backs the current/recent application-generation report.
	StartupReports StartupReportService
	// HealthRefresh invokes the same runner as the named System health task.
	HealthRefresh HealthRefreshService
	// Restart backs POST /v1/system/restart (§9.2, V13) — implemented over main's
	// generation loop. nil ⇒ 501, the honest answer for a handler with no loop behind it.
	Restart RestartService
	// RestartDrift names bootstrap and app-managed generation settings waiting on a restart
	// (config-design §3).
	RestartDrift             func() []string
	Jobs                     JobService                                       // /v1/jobs* background-job scheduler (§18.1); nil ⇒ routes 501
	Settings                 SettingsService                                  // /v1/settings* (config-design §8); nil ⇒ routes 501
	NotificationDestinations NotificationDestinationService                   // redacted product-delivery routing (§11)
	WebPushPublicKey         string                                           // public half of the generated VAPID identity (§11)
	ProposalNotifications    suggest.ProposalNotifier                         // best-effort product lifecycle publication (§11)
	DecisionQuality          ProposalDecisionQuality                          // committed Proposal denial quality outcome (§17)
	BackendTransition        BackendTransitioner                              // durable backend prepare/publish/retire coordinator
	BackendCheckpoint        func(context.Context) (BackendCheckpoint, error) // durable checkpoint, once per operation
	Guide                    GuideReader                                      // /v1/channels/now-next (§6, §9); nil ⇒ empty now/next
	Provision                Provisioner                                      // /v1/setup/bootstrap + /v1/users/import (§11); nil ⇒ routes absent
	Approver                 ProposalApprover                                 // atomic proposal + titles + channel gate (§7); required for approval
	Binder                   ChannelBinder                                    // explicit channel intent/number helpers; not the approval gate
	// PlayoutObserver supplies operational snapshots and program progress.
	PlayoutObserver PlayoutObserver
	// PreparedObserver supplies prepared readiness and retention status without rescanning.
	PreparedObserver PreparedObserver
	// PlayoutSecret reads the generated `playout_token` (§11 device auth). A func so a
	// REGENERATED token takes effect without a restart. Nil ⇒ playout routes fail closed.
	PlayoutSecret func() string
	// PlayoutSecretCurrent reads the durable token at a request boundary. Production wires
	// this for Postgres replica coherence; nil preserves the local/SQLite seam above.
	PlayoutSecretCurrent func(context.Context) (string, error)
	// PlayoutResolver answers "what is airing now" for /playout/program (§9.1). Nil ⇒ 501.
	PlayoutResolver PlayoutResolver
	// PlayoutEncoder starts one supervised ffmpeg (playout.Start). Nil ⇒ /playout/program 501s.
	PlayoutEncoder PlayoutEncoder
	// Playout is the one playback seam for MPEG-TS and HLS (§9.1 V56).
	Playout Playout
	// PlayoutGuide resolves programme timelines for the XMLTV guide (§9.1). Nil ⇒ the route 501s.
	PlayoutGuide PlayoutGuide
	// TimelineThumbs resolves a TMDB preview image per programme for the Watch timeline (§9.1 V47).
	// Nil ⇒ the strip renders with no images (TMDB optional, and it never fails the timeline).
	TimelineThumbs TimelineThumbResolver
	// PlayoutFont is the font the offline card is labelled with — a property of the HOST
	// (filesystem + ffmpeg build), so it is injected like PlayoutSecret rather than resolved
	// in the handler. Nil or "" ⇒ an unlabelled card, which is a supported rendering and the
	// deliberate fail-safe: see playout.CardFontFor for why a font file alone is not enough.
	PlayoutFont func() string
	// PlayoutTonemap reports whether this ffmpeg build carries the HDR→SDR filters (zscale +
	// tonemap). A property of the BUILD, so it is injected here rather than probed in the handler,
	// which keeps ProgramArgs a pure function. Nil ⇒ no tone-mapping: an HDR source still plays,
	// flat rather than dead. See playout.TonemapperFor.
	PlayoutTonemap func() bool
	// ReclaimVRAM frees GPU memory the hardware encoders need — evicts the resident local LLM
	// (§8.2 Evictor, §9.1 V47). Wired to the LLM provider's Evict when the provider is local and
	// implements Evictor; nil for a hosted provider (nothing local to reclaim). The retry ladder
	// calls it only after a hardware encode has already produced nothing.
	ReclaimVRAM func(ctx context.Context)
	// ResidentLLMVRAM reports the VRAM the LLM currently holds + its model tag, from a live Ollama
	// /api/ps probe (§9.1 V47 doctor). Powers the doctor's TRUE contention header. Nil for a hosted
	// provider or an install without a local LLM; the composition root wires it to llm ListResident.
	ResidentLLMVRAM func(ctx context.Context) (gib float64, model string)
	// EncodePool is the one host-wide hardware-encode admission boundary. Live playout uses
	// foreground leases; the readiness planner uses its preemptible background lease.
	EncodePool *media.EncodePool
	// LiveConfig reads a setting's live resolved value so feature routes gate on the
	// CURRENT config (a saved connection enables the route with no restart, §8.1).
	// The composition root passes settings.Service.String; unit tests omit it.
	LiveConfig func(key string) string
	// LibraryConfigured atomically reports whether the complete live media-server
	// {flavor,url,token} connection is usable. The composition root supplies it; unit tests
	// that wire a concrete adapter may omit it.
	LibraryConfigured func() bool
	// LiveConfigInt is the INT-typed twin. Separate rather than parsing LiveConfig's
	// string, because settings.String PANICS on a non-string key — an int setting read
	// through the string seam took down GET /v1/users in the quota work, and only a run
	// against a real settings service surfaced it (unit tests leave the seam nil, so the
	// typed accessor was never reached).
	LiveConfigInt func(key string) int
	// LiveConfigBoolOn is the BOOL-typed twin, and it exists for the same reason
	// LiveConfigInt does — the third occurrence of that one bug, so the seam is now typed
	// for every Kind a route reads rather than only the two that have already broken.
	// GET /v1/filler/sources panicked on `filler.source.folder.enabled` (a bool) exactly
	// as /v1/users did on an int, and again only a real settings service reached it.
	//
	// ⚠ boolOn, not boolv: nil ⇒ TRUE. Callers here gate "is this source being scanned",
	// where a false read renders a working drop-folder as switched off on the page whose
	// job is explaining an empty catalog. A key that is off by default needs its own
	// accessor rather than this one.
	LiveConfigBoolOn func(key string) bool
}

// arrayNullableOnce guards the one write to Huma's package-level DefaultArrayNullable (see the
// note in humaConfig): the value is constant, and doing it once keeps parallel Router() builds
// from racing Huma's schema registry on it.
var arrayNullableOnce sync.Once

// humaConfig builds the OpenAPI 3.1 config with our metadata (§7.1).
func humaConfig() huma.Config {
	// ⚠ Arrays are NOT nullable (V53b). Huma's default is `true` because a Go nil slice really
	// does marshal to `null`, so out of the box every list field in this API was typed
	// `T[] | null` — 109 nullable type-unions against 4 plain arrays — and every client had to
	// handle two representations of "nothing", forever.
	//
	// ⚠ This flag alone would make the document LIE, and Huma says so in its own doc comment:
	// "any `nil` slice will still encode as `null` in JSON". It is only honest because
	// `TestResponses_ContainNoJSONNull` drives every parameterless GET against an EMPTY store
	// — the state that actually produces nil slices — and fails on any null in the body. Setting
	// this without that guard would swap a truthful awkward spec for a tidy false one.
	//
	// ⚠ Set HERE deliberately: `humaConfig` is the single constructor behind both the served API
	// (api.go) and the spec export (export.go), so the runtime and the document cannot disagree
	// about it. It is a package-level global in Huma rather than a Config field, so there is no
	// per-instance place to put it.
	//
	// ⚠ Through a sync.Once, NOT bare. `humaConfig` runs on every `Router()` build, and tests build
	// many routers IN PARALLEL (the device-pairing suite is the one that surfaced it): a bare write
	// here races Huma's schema registry, which READS this same global while another goroutine's
	// Router() writes it → `-race` failure. The value is a constant, so writing it once is
	// sufficient and correct; the Once makes that write happen-before every reader without changing
	// behavior. Kept inside humaConfig so the "single constructor owns it" invariant above holds.
	arrayNullableOnce.Do(func() { huma.DefaultArrayNullable = false })
	cfg := huma.DefaultConfig("Loomarr API", "0.1.0")
	cfg.Info.Description = "Turn a sentence into a self-maintaining virtual TV channel. " +
		"Every /v1 route requires a session cookie or Authorization: Bearer API_TOKEN (§7)."
	// Serve our own docs assets offline; Huma's default loads Stoplight from a
	// CDN which violates the offline rule (§7.1). We disable the built-in docs
	// path and mount our own at /v1/reference (ops.go).
	cfg.DocsPath = ""
	// ⚠ The spec and schema paths move under /v1 with everything else. Huma writes SchemasPath
	// into every `$ref` in the document, so changing it rewrites references throughout — that is
	// expected, not drift. The bare /openapi* and /schemas/ prefixes stay in the SPA guard
	// (api.go) so a stale bookmark 404s instead of being handed index.html with a 200.
	cfg.OpenAPIPath = "/v1/openapi"
	cfg.SchemasPath = "/v1/schemas"
	// Wire descriptions for domain types that must not import the HTTP framework
	// (durationwire.go). ⚠ Registered HERE rather than at the two `humago.New` call sites
	// (api.go, export.go) so the runtime API and the spec export cannot disagree about a type's
	// wire format — the same reason DocsPath and the paths above live in this one function.
	registerWireAliases(cfg.Components.Schemas)
	return cfg
}
