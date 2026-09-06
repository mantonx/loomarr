// Package api wires Loomarr's inbound HTTP surface (§7). The router is the
// stdlib net/http ServeMux (Go 1.22 patterns) with Huma v2 mounted via humago
// for the versioned /v1 API — code-first OpenAPI 3.1, one source of truth for
// spec + validation + docs (§7.1). No third-party router; the embedded
// same-origin SPA means no CORS layer.
package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"github.com/loomarr/loomarr/internal/metrics"
	"github.com/loomarr/loomarr/internal/web"
)

// ReadyFunc reports readiness (DB + migrations; soft Tunarr) — §17.
type ReadyFunc func() (ready bool, detail string)

// Router builds the top-level handler from the given options.
func Router(log *slog.Logger, opts Options) http.Handler {
	mux := http.NewServeMux()
	recorder := opts.Metrics
	if recorder == nil {
		recorder = metrics.New(metrics.Options{Version: "dev", Database: "unknown"})
	}

	ready := opts.Ready
	if ready == nil {
		ready = func() (bool, string) { return true, "ok" }
	}

	// ⚠ The ops surface (liveness, readiness, Prometheus, the profiler, the API reference) now
	// registers with everything else, in registerOps — under /v1, with its bare paths kept as
	// hidden aliases. It was the last of the raw-mux world; see ops.go for why the aliases are
	// not optional and why moving these reverses a decision that was pinned on purpose.

	// The Huma API (§7.1): /v1 operations, /v1/openapi.{json,yaml}. Auth is applied
	// as Huma middleware so every /v1 op resolves a role (§7 authorization model).
	cfg := humaConfig()
	// Stamp every error response with the request's correlation id + log its full cause
	// (§7). A response Transformer is the one seam that sees EVERY error body — returned
	// StatusErrors AND huma's own validation failures — with the request context.
	cfg.Transformers = append(cfg.Transformers, errorTransformer(log))
	humaAPI := humago.New(mux, cfg)
	srv := &Server{
		// Stamped per handler build, so a restart (§9.2) resets the uptime About reports
		// — a package-level var would survive the rebuild and keep counting from the
		// original boot.
		startedAt: time.Now(),
		store:     opts.Store, auth: opts.Auth, log: log, metrics: recorder, backupSQLite: opts.BackupSQLite,
		login: opts.Login, sessions: opts.Sessions, passwords: opts.Passwords, userSync: opts.UserSync, invitations: opts.Invitations, invitationDelivery: opts.InvitationDelivery, invitationRedemption: opts.InvitationRedemption, passwordRecovery: opts.PasswordRecovery, accessPublicURL: opts.AccessPublicURL, devices: opts.Devices, deviceLimiter: opts.DeviceLimiter, cookieSecure: opts.CookieSecure, trustProxy: opts.TrustProxy, devLogin: opts.DevLogin,
		channels: opts.Channels, livetv: opts.LiveTV, tunerRescanner: opts.TunerRescanner, tunarrConnect: opts.TunarrConnect,
		suggest: opts.Suggest, proposalWorkflow: opts.ProposalWorkflow, search: opts.Search, collections: opts.Collections, icons: opts.Icons, images: opts.Images, events: opts.Events, shutdown: opts.Shutdown, filler: opts.Filler, fillerDecisions: opts.FillerDecisions, fillerRights: opts.FillerRights, pods: opts.Pods, taxonomy: opts.Taxonomy,
		fillerLayout:     opts.FillerLayout,
		jobs:             opts.Jobs,
		diagnosticEvents: opts.DiagnosticEvents, diagnosticProcesses: opts.DiagnosticProcesses, diagnosticBundles: opts.DiagnosticBundles,
		diagnosticCapture: opts.DiagnosticCapture,
		clientDiagnostics: opts.ClientDiagnostics,
		startupReports:    opts.StartupReports,
		healthRefresh:     opts.HealthRefresh,
		systemLLM:         opts.SystemLLM, database: opts.Database, encryption: opts.Encryption, backups: opts.Backups, restart: opts.Restart, activity: opts.Activity, sso: opts.SSO,
		restartDrift:             opts.RestartDrift,
		settings:                 opts.Settings,
		notificationDestinations: opts.NotificationDestinations, backendTransition: opts.BackendTransition,
		webPushPublicKey:      opts.WebPushPublicKey,
		proposalNotifications: opts.ProposalNotifications,
		decisionQuality:       opts.DecisionQuality,
		backendCheckpoint:     opts.BackendCheckpoint,
		provision:             opts.Provision, guide: opts.Guide,
		liveConfig: opts.LiveConfig, liveConfigInt: opts.LiveConfigInt,
		libraryConfigured: opts.LibraryConfigured,
		liveConfigBoolOn:  opts.LiveConfigBoolOn, ready: ready,
		approver:        opts.Approver,
		binder:          opts.Binder,
		playoutObserver: opts.PlayoutObserver, preparedObserver: opts.PreparedObserver,
		playoutSecret: opts.PlayoutSecret, playoutSecretCurrent: opts.PlayoutSecretCurrent,
		playoutResolver: opts.PlayoutResolver, playoutEncoder: opts.PlayoutEncoder,
		playout:      opts.Playout,
		playoutGuide: opts.PlayoutGuide, playoutFont: opts.PlayoutFont,
		playoutTonemap:  opts.PlayoutTonemap,
		timelineThumbs:  opts.TimelineThumbs,
		reclaimVRAM:     opts.ReclaimVRAM,
		residentLLMVRAM: opts.ResidentLLMVRAM,
		encodePool:      opts.EncodePool,
	}
	srv.registerMiddleware(humaAPI)
	srv.registerTitles(humaAPI)
	srv.registerAuth(humaAPI)
	srv.registerDeviceAuth(humaAPI)
	srv.registerUsers(humaAPI)
	srv.registerInvitations(humaAPI)
	srv.registerInvitationRedemption(humaAPI)
	srv.registerPasswordRecovery(humaAPI)
	srv.registerPasswords(humaAPI)
	srv.registerChannels(humaAPI)
	srv.registerPlayout(humaAPI) // §9.1 streaming routes (V47): Huma-mounted, shared auth
	srv.registerGuide(humaAPI)
	srv.registerProgramming(humaAPI)
	srv.registerSetup(humaAPI)
	srv.registerProposals(humaAPI)
	srv.registerDiscoveryFeedback(humaAPI)
	srv.registerProposalJourneys(humaAPI)
	srv.registerSearch(humaAPI)
	srv.registerCollections(humaAPI)
	srv.registerFiller(humaAPI)
	srv.registerFillerDecisions(humaAPI)
	srv.registerFillerRights(humaAPI)
	srv.registerFillerSources(humaAPI)
	srv.registerFillerWatch(humaAPI)
	srv.registerFillerPulls(humaAPI)
	srv.registerFillerIncoming(humaAPI)
	srv.registerFillerBulk(humaAPI)
	srv.registerFillerFile(humaAPI)
	srv.registerTaxonomy(humaAPI)
	srv.registerImages(humaAPI)
	srv.registerJobs(humaAPI)
	srv.registerDashboard(humaAPI)
	srv.registerDiagnostics(humaAPI)
	srv.registerPlayoutStatus(humaAPI) // §9.1 V47: playout status projection
	srv.registerSystemLLM(humaAPI)
	srv.registerSystemDatabase(humaAPI)
	srv.registerSystemSecurity(humaAPI)
	srv.registerDiscoveryQuality(humaAPI)
	srv.registerSystemBackups(humaAPI)
	srv.registerSystemRestart(humaAPI)
	srv.registerDashboardPanels(humaAPI)
	srv.registerSettings(humaAPI)
	srv.registerNotificationDestinations(humaAPI)
	srv.registerHelp(humaAPI)
	srv.registerEvents(humaAPI) // §8 SSE — typed frames, nil-guarded on the bus
	srv.registerSSO(humaAPI)    // §11 V8 redirects, nil-guarded on the provider
	srv.registerProvisioning(humaAPI)
	srv.registerOps(humaAPI, opts.Pprof) // §17 probes/metrics/profiler + the API reference

	// ⚠ Everything that used to be listed here — /v1/backup, the backup download, the three clip
	// byte routes, and the channel-icon serve — is now registered with its own domain, as a rawOp
	// (rawop.go). They stream bytes and keep the (w, r) signature that http.ServeContent needs,
	// but they mount on the SAME Huma API as every other route, so they are covered by the one
	// authorization middleware and appear in api/openapi.yaml. Splitting one resource across two
	// registration mechanisms is what let /v1/system/backups be spec'd while its download was not.
	//
	// Internal playout (§9.1) mounts the same way — see registerPlayout.

	// The embedded SPA at / (§12): the catch-all, and now the ONLY raw mux registration —
	// it serves embedded static assets and is not an API route. Guard the prefix-based API
	// surfaces so an unmatched path under one 404s as an API error rather than silently
	// serving index.html. Exact routes already win by ServeMux specificity and never reach here.
	spa := web.Handler()
	// ⚠ Each entry earns its place by a failure it prevents, and "it moved under /v1" does not
	// remove the need for the OLD prefix — a stale bookmark, a scrape config or a docs link
	// still resolves against this server.
	//
	// `/openapi` and `/schemas/` stay even though both now live under /v1: without them a
	// request to the old /openapi.json falls through and returns index.html with a 200, so a
	// client parsing it reports malformed YAML rather than "moved".
	// `/debug/` stays even though pprof is usually NOT mounted, and that is the point: with the
	// flag off, /debug/pprof/profile would otherwise return index.html with a 200 and
	// `go tool pprof` reports "unrecognized profile format" — reading as a broken profiler
	// rather than a disabled endpoint. That cost two failed capture attempts before a test
	// caught it.
	// `/metrics` likewise: a scrape hitting HTML should fail as a 404, not parse-error.
	//
	// ⚠ `/hooks/` is GONE. No /hooks route has existed since the inbound arr webhook was
	// deleted, so the guard was reserving a prefix against nothing.
	// `/playout/` is likewise absent: those routes have been under /v1/playout since V47, so
	// /v1/ already covers them.
	apiPrefixes := []string{"/v1/", "/openapi", "/schemas/", "/metrics", "/debug/", "/healthz", "/readyz", "/docs"}
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, p := range apiPrefixes {
			if strings.HasPrefix(r.URL.Path, p) {
				srv.writeProblem(w, r, http.StatusNotFound, "Not found", "There's no endpoint at that address.")
				return
			}
		}
		spa.ServeHTTP(w, r)
	}))

	// Outermost so it observes total handling time; it reads r.Pattern after the
	// mux has matched (§18 request metrics). withRequestID wraps everything so the
	// correlation id exists for every handler — typed Huma ops AND the plain-mux ones
	// (/v1/events, /v1/backup) — and is echoed on the response header.
	// FanoutMiddleware sits outside the Recorder middleware and every handler: it installs the
	// per-request outbound counter that the instrumented transport increments, so
	// `loomarr_http_outbound_fanout{route=…}` answers "how many downstream calls does this
	// endpoint make" without serialising traffic and diffing a global counter by hand.
	// It must be outside because it routes a request copy carrying that counter; the Recorder and
	// mux must see that same copy for the mux-populated Pattern to reach the HTTP route label.
	return withRequestID(recorder.FanoutMiddleware(recorder.Middleware(logRequests(log, mux))))
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func logRequests(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
		log.Debug("http", "method", r.Method, "path", r.URL.Path, "remote", r.RemoteAddr)
	})
}
