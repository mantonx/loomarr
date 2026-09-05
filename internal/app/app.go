// Package app is the composition root: it wires every subsystem from an open
// store into the API handler that cmd/loomarr serves and the integration tests
// drive. Keeping it importable (not package main) is what lets tests exercise
// the REAL wiring end to end (§21), and keeps cmd/loomarr a thin entrypoint.
package app

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/diagnostics"
	"github.com/loomarr/loomarr/internal/llm"
	"github.com/loomarr/loomarr/internal/notifications"
	"github.com/loomarr/loomarr/internal/programmer"
	"github.com/loomarr/loomarr/internal/settings"
	"github.com/loomarr/loomarr/internal/store"
)

// Overrides injects the two in-process boundaries (the Tunarr push target and the
// LLM provider) for tests. Both nil ⇒ the real URL-built adapters (production).
type Overrides struct {
	// EncryptionDataDir is the boot-configured directory for the generated
	// installation key. Empty derives it from SQLite and otherwise uses /data.
	EncryptionDataDir string
	// Startup is the process-owned report for this application generation. nil creates a minimal
	// embedded-build report so /readyz still derives from the same state object in tests.
	Startup    *diagnostics.Startup
	Programmer programmer.Programmer // nil ⇒ programmer.NewDynamic(live Tunarr config)
	LLM        llm.Provider          // nil ⇒ the Swappable from buildLLM
	// NotificationHTTP redirects provider requests to a hermetic transport in composition tests.
	// nil uses the bounded production HTTP client owned by notifications.
	NotificationHTTP notifications.HTTPDoer
	// TMDBBaseURL redirects the real dynamic TMDB adapter to an in-process external
	// service double. The credential still comes from live settings; accepting a
	// prebuilt client here used to freeze its key at boot and bypass composition.
	TMDBBaseURL string // empty ⇒ https://api.themoviedb.org/3
	// DevLogin mounts POST /v1/auth/dev-login (§11). run() sets it from
	// LOOMARR_DEV_LOGIN; it rides Overrides so the §19 negative tests can build a
	// handler BOTH ways through the real composition root, rather than asserting
	// against a hand-rolled router that could drift from what production mounts.
	DevLogin bool
	// Pprof mounts /debug/pprof/* (§7). Rides Overrides alongside DevLogin for the same
	// reason: run() sets it from LOOMARR_PPROF, and a test can build a handler either way
	// through the real composition root.
	Pprof bool
	// Restart asks the process to rebuild itself in place (§9.2). nil ⇒ the restart
	// routes report 501 rather than pretending: an embedded or test-built handler has no
	// loop behind it, and a button that silently does nothing is worse than an absent one.
	//
	// A func rather than a channel so the API package never learns how restarting works —
	// only main owns the generation loop. It must NOT block: the handler calls it while
	// responding, and the drain it triggers cannot begin until that response is written.
	Restart func()
	// DatabaseMigration asks the process-owned generation loop to drain the current
	// handler and perform a SQLite→Postgres migration after every old-generation
	// writer has stopped. nil keeps the database service fail-closed in embedded tests.
	DatabaseMigration func(string) error
	// DatabaseMigrationError carries a failed attempt into the replacement SQLite
	// generation, where the database status endpoint can explain why it stayed put.
	DatabaseMigrationError string
	// ImageWorkerExecutable selects the required Rust image worker in tests and embedded builds.
	// Empty resolves LOOMARR_IMAGE_WORKER, then the sibling/local/PATH-installed release binary.
	ImageWorkerExecutable string
}

// Build wires every subsystem from the already-open store + logger and returns one
// lifecycle-owned application generation. It is the seam run() (production) and the integration
// harness (tests) both call, so tests drive the real composition rather than a copy. It does not
// open/close the store, read process config, listen, or handle signals; those stay in run().
//
// # Why composition uses functions instead of a builder object
//
// Each subsystem builder takes immutable inputs and returns a concrete result. A shared mutable
// builder would widen intermediate values into fields whose validity depends on call order and
// trade compile-time use-before-assignment errors for runtime nils. The short ordered assembly in
// buildHandler keeps the dependency direction visible; the few deliberate late connections remain
// explicit at their owning seams (§14.1).
//
// # The nil-store path is deliberate
//
// Most sections sit inside `if st != nil`. That guard is not defensive habit: a container
// started without DATABASE_URL must still build a handler and answer /readyz with the reason,
// rather than crash-looping past the probe that would explain the problem. See
// TestBuildWithoutStoreServesReadinessInsteadOfPanicking — the nesting is the price of
// that behaviour.
//
// # Builder map (in dependency order — later builders consume earlier results)
//
//	foundation    readiness, settings, clients, events, jobs
//	provisioning  acquisition, availability, retention
//	channels      scheduler, Tunarr, Live TV, backend view
//	playout       sessions, resolver, HLS, lifecycle
//	approval      the one approved-proposal to channel path
//	suggestions   LLM, catalog, grounding, search, images
//	filler        catalog sync and pod assembly
//	operations    auth, backup, settings, restart, River
//	HTTP          the single api.Options assembly point
const replicaSettingsRefreshInterval = 30 * time.Second

// internalPlayoutNeedsPublicURL reports whether the boot path should warn that internal
// playout has no reachable base URL. Callers invoke it only inside the internal-playout branch,
// so the sole question here is whether server.public_url is effectively empty — whitespace is
// treated as unset because a blank env value round-trips to "  " through some shells and is not
// a usable URL. (beta-readiness D-4.)
func internalPlayoutNeedsPublicURL(publicURL string) bool {
	return strings.TrimSpace(publicURL) == ""
}

func trackReplicaSettingsRefresh(
	owner *generationLifecycle,
	dialect store.Dialect,
	svc *settings.Service,
	interval time.Duration,
	after func(),
) bool {
	if dialect != store.DialectPostgres || svc == nil {
		return false
	}
	owner.goRun(func(ctx context.Context) { svc.RefreshEvery(ctx, interval, after) })
	return true
}

func buildHandler(
	rootCtx context.Context,
	st store.Store,
	log *slog.Logger,
	ov Overrides,
	owner *generationLifecycle,
	capturePlayoutResolver func(*playoutResolver),
) (http.Handler, *slog.Logger, func() string, error) {
	foundation, err := buildFoundation(rootCtx, st, log, ov, owner)
	if err != nil {
		return nil, nil, nil, err
	}
	// Every later builder completes checks on the exact state /readyz uses. Embedded callers may
	// omit the override, in which case foundation created that state for them.
	ov.Startup = foundation.startup
	set, desiredSet := foundation.set, foundation.desiredSet
	fillerLayout := foundation.fillerLayout
	secrets, log := foundation.secrets, foundation.log
	libraryClient, tmdbClient := foundation.libraryClient, foundation.tmdbClient
	refreshSecretRedactor, readGeneratedSecret := foundation.refreshSecretRedactor, foundation.readGeneratedSecret
	eventBus, emitter := foundation.eventBus, foundation.emitter
	jobReg, activityRec := foundation.jobs, foundation.activity

	episodeRefresh := buildProvisioning(st, set, libraryClient, emitter, jobReg, activityRec,
		foundation.processDiagnostics, log, foundation.metrics)

	channelsBuilt, err := buildChannels(
		rootCtx, st, set, ov, owner, capturePlayoutResolver, libraryClient, secrets,
		readGeneratedSecret, eventBus, emitter, activityRec, jobReg, episodeRefresh, fillerLayout, log,
		foundation.processDiagnostics, foundation.metrics,
	)
	if err != nil {
		return nil, nil, nil, err
	}
	channelSvc := channelsBuilt.channelService
	appliedBackendContext := channelsBuilt.appliedBackend
	playoutRes, chanNumbers := channelsBuilt.playoutResolver, channelsBuilt.channelNumbers
	setResidentVRAM := channelsBuilt.setResidentVRAM

	approval := buildApproval(st, channelSvc, playoutRes, activityRec, chanNumbers, emitter, log)
	proposalApprover := approval.approver

	suggestions, err := buildSuggestions(
		rootCtx, st, set, ov, eventBus, emitter, jobReg, fillerLayout, activityRec, log,
		libraryClient, tmdbClient, channelSvc, proposalApprover, owner,
		foundation.metrics,
	)
	if err != nil {
		return nil, nil, nil, err
	}
	fillers := buildFillerSubsystem(
		st, set, fillerLayout, log, libraryClient, eventBus, emitter, jobReg, playoutRes, channelSvc,
		foundation.processDiagnostics, foundation.metrics, owner,
	)
	healthProbes := connectionTests(set, libraryClient, tmdbClient)
	completeStartupIntegrations(rootCtx, foundation.startup, set, healthProbes)
	healthRunner := newCurrentHealthRunner(foundation.startup, st, set, healthProbes)
	jobReg.Add(healthRunner.Job())

	operations, err := buildOperations(
		rootCtx, st, set, desiredSet, secrets, readGeneratedSecret, refreshSecretRedactor,
		libraryClient, tmdbClient, eventBus, emitter, jobReg, owner, playoutRes,
		appliedBackendContext, ov, log, foundation.metrics,
		foundation.protection, foundation.secretRedactor,
	)
	if err != nil {
		return nil, nil, nil, err
	}
	emitter.setNotifications(operations.productNotifications)
	if playoutRes != nil {
		setResidentVRAM(operations.residentLLM.probe)
	}

	handler := buildHTTP(httpBuild{
		rootCtx: rootCtx, store: st, log: log, overrides: ov, foundation: foundation,
		channels: channelsBuilt, approval: approval, suggestions: suggestions, fillers: fillers,
		auth: operations.auth, backups: operations.backups,
		restart: operations.restart, bootConfig: operations.bootConfig,
		guide: operations.guide, settings: operations.settings,
		notificationDestinations: operations.notificationDestinations,
		webPushPublicKey:         operations.webPushPublicKey,
		invitationDelivery:       operations.invitationDelivery, passwordRecovery: operations.passwordRecovery,
		liveConfig:        operations.liveConfig,
		libraryConfigured: operations.libraryConfigured, jobs: operations.jobs,
		database: operations.database, residentLLM: operations.residentLLM,
		healthRefresh: healthRunner,
	})
	// SQLite is single-process by contract, so its settings snapshot changes only
	// through this process's writes and needs no polling reads. Postgres permits
	// ordinary replicas; one cancellable refresher per application generation keeps
	// their complete runtime snapshot within the documented ~30-second bound.
	trackReplicaSettingsRefresh(owner, store.DialectOf(st), set.svc,
		replicaSettingsRefreshInterval, refreshSecretRedactor)
	return handler, log, func() string { return set.str("server.public_url") }, nil
}
