// Command loomarr is the single-binary media-channel builder (design §2, §21).
// Phase 1 wires config, structured logging, and an HTTP server exposing /healthz
// and /readyz with graceful shutdown. Subsystems are added in later phases.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/loomarr/loomarr/internal/app"
	"github.com/loomarr/loomarr/internal/buildinfo"
	"github.com/loomarr/loomarr/internal/config"
	"github.com/loomarr/loomarr/internal/diagnostics"
	"github.com/loomarr/loomarr/internal/landiscovery"
	"github.com/loomarr/loomarr/internal/store"
)

func main() {
	if err := dispatch(os.Args[1:]); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

// dispatch routes the command line. With no arguments — the ENTRYPOINT case — it boots
// the server, so the shipped image's behaviour is unchanged.
//
// ⚠ main() previously ignored os.Args entirely, which meant every argument booted a
// server. compose.yaml's healthcheck had been calling `/loomarr healthcheck` against
// that for as long as the file existed: the argument was discarded and a second full
// instance started, failed to bind the port, and exited non-zero every 30 seconds. The
// unknown-subcommand error below is the part that keeps this honest — a typo now fails
// visibly instead of quietly starting something.
func dispatch(args []string) error {
	if len(args) == 0 {
		return run()
	}
	switch args[0] {
	case "healthcheck":
		return runHealthcheck()
	default:
		return fmt.Errorf("%w: %q", errUnknownSubcommand, args[0])
	}
}

// run boots Loomarr and keeps booting it until a generation exits for a reason other
// than "restart" (§9.2).
//
// ⚠ **The loop IS the restart mechanism.** Loomarr never re-execs and never exits to be
// restarted by something else: `syscall.Exec` is a stub on Windows (compiles, fails at
// runtime), and exiting assumes a supervisor that `make dev-be` and any bare binary do
// not have. Rebuilding in place works identically on both platforms and cannot strand an
// operator with a dead service.
//
// Only work that must NOT re-run belongs out here. Everything a generation owns — config,
// store, background context, handler, HTTP server — is constructed inside runOnce and torn
// down before the next pass, because a value carried across generations is a value still
// pointing at the closed resources of the last one.
func run() error {
	processStarted := time.Now()
	// Config is loaded once for the log level only. Each generation re-loads it, so an
	// operator who edits a boot-time setting gets the new value on restart — which is the
	// entire point of the RestartRequired flag (config-design §3).
	bootCfg, err := config.Load()
	if err != nil {
		health := newGenerationHealth(processStarted, 1)
		health.Complete(diagnostics.StartupCheckConfiguration, diagnostics.StartupFailed,
			"configuration is invalid", "", "")
		health.CompletePending(diagnostics.StartupSkipped, "configuration unavailable")
		printStartupReport(os.Stdout, health.Snapshot(), stdoutInteractive(), terminalWidth(), os.Getenv("NO_COLOR") == "")
		return err
	}
	// Logging is hoisted above the loop deliberately: slog.SetDefault mutates process
	// state, and re-running it per generation would be the exact package-level-mutation
	// hazard §9.2 warns about.
	log := newLogger(bootCfg.LogLevel)
	slog.SetDefault(log)

	databaseMigration := &databaseMigrationState{bootstrapDir: config.ConventionalDataDir}
	for generation := 1; ; generation++ {
		startup := newGenerationHealth(processStarted, generation)
		restart, err := runOnce(log, generation, databaseMigration, startup)
		if err != nil {
			return err
		}
		if !restart {
			return nil
		}
		log.Info("restarting in place", "next_generation", generation+1)
	}
}

func newGenerationHealth(processStarted time.Time, generation int) *diagnostics.Startup {
	return diagnostics.NewStartup(processStarted, generation, buildinfo.Get().Version, []diagnostics.StartupCheck{
		{Key: diagnostics.StartupCheckConfiguration, Label: "Configuration", Required: true},
		{Key: diagnostics.StartupCheckDatabase, Label: "Database and migrations", Required: true,
			Mode: diagnostics.HealthCheckContinuous, FreshFor: 3 * time.Minute,
			RemediationRoute: "/settings/system/database"},
		{Key: diagnostics.StartupCheckGeneratedSecrets, Label: "Generated secrets", Required: true},
		{Key: diagnostics.StartupCheckImageWorker, Label: "Image worker certification", Required: true},
		{Key: diagnostics.StartupCheckHTTP, Label: "HTTP assembly and listener", Required: true},
		{Key: diagnostics.StartupCheckMediaServer, Label: "Media server", Required: false,
			Mode: diagnostics.HealthCheckContinuous, FreshFor: 3 * time.Minute,
			RemediationRoute: "/settings/connections"},
		{Key: diagnostics.StartupCheckTunarr, Label: "Tunarr", Required: false,
			Mode: diagnostics.HealthCheckContinuous, FreshFor: 3 * time.Minute,
			RemediationRoute: "/settings/connections"},
		{Key: diagnostics.StartupCheckRequester, Label: "Requester", Required: false,
			Mode: diagnostics.HealthCheckContinuous, FreshFor: 3 * time.Minute,
			RemediationRoute: "/settings/connections"},
		{Key: diagnostics.StartupCheckLLM, Label: "AI provider", Required: false,
			Mode: diagnostics.HealthCheckContinuous, FreshFor: 3 * time.Minute,
			RemediationRoute: "/settings/ai"},
		{Key: diagnostics.StartupCheckTMDB, Label: "TMDB", Required: false,
			Mode: diagnostics.HealthCheckContinuous, FreshFor: 3 * time.Minute,
			RemediationRoute: "/settings/connections"},
	}, time.Now)
}

// runOnce is one generation: build everything, serve, tear it all down. It returns true
// when the operator asked for a restart, so run() can build the next generation. A
// failed database migration records the concise error the next generation exposes.
//
// The returned bool and error are deliberately separate: "the operator restarted us" is
// not a failure, and collapsing it into an error would make a normal action look like one
// in the logs.
func runOnce(log *slog.Logger, generation int, databaseMigration *databaseMigrationState, startup *diagnostics.Startup) (restart bool, err error) {
	cfg, err := config.Load()
	if err != nil {
		startup.Complete(diagnostics.StartupCheckConfiguration, diagnostics.StartupFailed,
			"configuration is invalid", "", "")
		startup.CompletePending(diagnostics.StartupSkipped, "configuration unavailable")
		printStartupReport(os.Stdout, startup.Snapshot(), stdoutInteractive(), terminalWidth(), os.Getenv("NO_COLOR") == "")
		return false, err
	}
	startup.Complete(diagnostics.StartupCheckConfiguration, diagnostics.StartupPassed, "valid", "", "")
	// LLM_PROVIDER (llm.provider) is now a registry setting validated at settings
	// boot (an invalid enum fails there, config-design §3) — no separate check here.

	log.Info("loomarr starting", "listen", cfg.ListenAddr, "log_level", cfg.LogLevel, "generation", generation)

	// Open the store (§5): backend chosen by DATABASE_URL scheme, migrations run
	// on startup when AUTO_MIGRATE. If unset, run without a store for now (later
	// phases require it; readiness reflects the truth either way).
	var st store.Store
	if cfg.DatabaseURL != "" {
		st, err = store.Open(context.Background(), cfg.DatabaseURL, cfg.AutoMigrate)
		if err != nil {
			if databaseMigration.fallbackSQLiteURL != "" {
				if restoreErr := databaseMigration.restoreSQLite(fmt.Errorf("open first PostgreSQL generation: %w", err)); restoreErr != nil {
					return false, restoreErr
				}
				return true, nil
			}
			startup.Complete(diagnostics.StartupCheckDatabase, diagnostics.StartupFailed,
				"database open or migration failed", "/settings/system/database", "")
			log.Error("database unavailable; serving blocked startup report", "err", err)
			st = nil
		} else {
			startup.Complete(diagnostics.StartupCheckDatabase, diagnostics.StartupPassed,
				string(store.DialectOf(st))+" ready", "", "")
		}
		// Closed on EVERY exit from this generation, including a restart — the next
		// generation opens its own handle, and a leaked one would hold the SQLite file
		// (and, on a migration, the database it just moved off).
		if st != nil {
			log.Info("store opened", "auto_migrate", cfg.AutoMigrate)
		}
	} else {
		log.Warn("no DATABASE_URL set — running without a store (not ready)")
		startup.Complete(diagnostics.StartupCheckDatabase, diagnostics.StartupFailed,
			"no store configured", "/settings/system/database", "")
	}
	if databaseMigration.fallbackSQLiteURL != "" && store.DialectOf(st) != store.DialectPostgres {
		databaseMigration.lastError = "database migration verified, but bootstrap did not select PostgreSQL; still running on SQLite"
		databaseMigration.fallbackSQLiteURL = ""
	}
	closeStore := func() error {
		if st == nil {
			return nil
		}
		closing := st
		st = nil
		return closing.Close()
	}
	defer func() { _ = closeStore() }()

	// Background work (reconciler, sweeps, worker pools) runs under rootCtx;
	// shutdown cancels it alongside the HTTP drain.
	rootCtx, cancelRoot := context.WithCancel(context.Background())
	defer cancelRoot()

	// A credential-free sign-in is worth shouting about on EVERY start, not once in a
	// changelog: the failure mode is an operator who turned it on months ago for a dev
	// session and never took it off (§11).
	if cfg.DevLogin {
		log.Warn("LOOMARR_DEV_LOGIN is set — POST /v1/auth/dev-login grants an admin session with NO credential. Never set this on an install you care about.")
	}
	// Same reasoning as dev-login: a profiling surface exposing stack traces and memory, with
	// no auth in front of it, is worth shouting about on every start rather than once.
	if cfg.Pprof {
		log.Warn("LOOMARR_PPROF is set — /debug/pprof/* is exposed UNAUTHENTICATED. Development only; never leave this on.")
	}

	// One channel serializes every operator-owned generation transition. Keeping restart
	// and migration on separate channels would let two simultaneous clicks both be
	// accepted while select arbitrarily discarded one. Buffered so the winning handler
	// can respond before this loop begins its drain (§7).
	lifecycle := newLifecycleRequester()

	// Build the fully-wired API handler. This is the composition seam that the
	// integration harness also calls, so tests exercise the REAL wiring (§21).
	application, err := app.Build(rootCtx, st, log, app.Overrides{
		Startup:                startup,
		EncryptionDataDir:      config.ConventionalDataDir,
		DevLogin:               cfg.DevLogin,
		Pprof:                  cfg.Pprof,
		Restart:                lifecycle.RequestRestart,
		DatabaseMigration:      lifecycle.RequestMigration,
		DatabaseMigrationError: databaseMigration.lastError,
	})
	if err != nil {
		printStartupReport(os.Stdout, startup.Snapshot(), stdoutInteractive(), terminalWidth(), os.Getenv("NO_COLOR") == "")
		if databaseMigration.fallbackSQLiteURL != "" {
			if restoreErr := databaseMigration.restoreSQLite(fmt.Errorf("build first PostgreSQL generation: %w", err)); restoreErr != nil {
				return false, restoreErr
			}
			return true, nil
		}
		return false, err
	}
	// From this point onward, process lifecycle messages belong to this generation's retained
	// diagnostic timeline as well as stdout. Pre-store/open failures necessarily remain stdout-only.
	log = application.Logger()
	// A FRESH server per generation. http.Server is single-use by design — Shutdown sets
	// `inShutdown` and never clears it, so a reused one would refuse to serve (§9.2).
	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           application.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Acquire the listener synchronously. For the first PostgreSQL generation this is the
	// final cutover check: a bind failure must restore SQLite rather than clear fallback
	// before ListenAndServe reports the error asynchronously.
	listener, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		startup.Complete(diagnostics.StartupCheckHTTP, diagnostics.StartupFailed,
			"listener could not start", "/settings/system/diagnostics", "")
		printStartupReport(os.Stdout, startup.Snapshot(), stdoutInteractive(), terminalWidth(), os.Getenv("NO_COLOR") == "")
		if databaseMigration.fallbackSQLiteURL != "" {
			if restoreErr := databaseMigration.restoreSQLite(fmt.Errorf("listen for first PostgreSQL generation: %w", err)); restoreErr != nil {
				return false, restoreErr
			}
			return true, nil
		}
		return false, err
	}
	startup.Complete(diagnostics.StartupCheckHTTP, diagnostics.StartupPassed,
		"listener is accepting connections", "", "")
	printStartupReport(os.Stdout, startup.Snapshot(), stdoutInteractive(), terminalWidth(), os.Getenv("NO_COLOR") == "")
	defer func() { _ = listener.Close() }()
	hostname, hostnameErr := os.Hostname()
	if hostnameErr != nil {
		log.Warn("LAN discovery: host name unavailable", "err", hostnameErr)
		hostname = ""
	}
	if advertisement, advertiseErr := landiscovery.Start(listener.Addr(), hostname); advertiseErr != nil {
		// Discovery failure cannot make the HTTP service unready. The TV reports the missing
		// automatic path and retains manual address recovery when multicast is unavailable.
		log.Warn("LAN discovery: advertisement unavailable", "err", advertiseErr)
	} else {
		defer advertisement.Shutdown()
		log.Info("LAN discovery: advertising Loomarr", "service", landiscovery.ServiceType)
	}
	if responder, respondErr := landiscovery.StartBroadcast(hostname, application.ServerPublicURL); respondErr != nil {
		log.Warn("LAN discovery: container-safe responder unavailable", "err", respondErr)
	} else {
		defer responder.Shutdown()
		log.Info("LAN discovery: listening for container-safe requests", "udp_port", landiscovery.BroadcastPort)
	}
	// A fully built generation with an acquired listener is the cutover's final commit.
	// Future, unrelated runtime failures must not roll back a live PostgreSQL install.
	databaseMigration.fallbackSQLiteURL = ""

	// Run the server; surface an accept error via this channel.
	serverErr := make(chan error, 1)
	go func() {
		if err := srv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	// Block until a signal, a restart request, or a server error.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var databaseMigrationDSN string
	select {
	case err := <-serverErr:
		return false, err
	case request := <-lifecycle.requests:
		restart = true
		databaseMigrationDSN = request.databaseMigrationDSN
		if databaseMigrationDSN == "" {
			log.Info("restart requested")
		} else {
			log.Info("database migration requested")
		}
	case <-ctx.Done():
		log.Info("shutdown signal received")
	}
	// ⚠ Stop trapping signals BEFORE the next generation starts. NotifyContext keeps the
	// handler registered until stop() runs, and on a restart the deferred stop() would not
	// fire until this function returns — leaving the old generation's context able to
	// swallow the Ctrl-C an operator uses on the new one.
	stop()

	// Graceful drain.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	// End generation-owned work and long-lived SSE streams before draining HTTP. Request
	// contexts are independent, so the accepted migration response can still finish.
	cancelRoot()
	drainErr := drainGeneration(shutdownCtx, srv.Shutdown, application.Quiesce, application.Shutdown)
	if drainErr != nil {
		// A drain that timed out is worth reporting, but on a restart it must not abort
		// the loop: the generation is finished either way, and refusing to come back up
		// because a client held a connection open is the outage this feature prevents.
		if databaseMigrationDSN != "" {
			msg := conciseDatabaseMigrationError(fmt.Errorf("drain SQLite generation: %w", drainErr))
			log.Error("database migration could not start; restarting on SQLite", "err", msg)
			databaseMigration.lastError = msg
			return true, nil
		}
		if !restart {
			return false, drainErr
		}
		log.Warn("drain did not finish cleanly before restart", "err", drainErr)
	}
	if databaseMigrationDSN != "" {
		if err := performDatabaseCutover(
			context.Background(),
			closeStore,
			func(ctx context.Context) error {
				return migrateDatabase(ctx, cfg.DatabaseURL, databaseMigrationDSN)
			},
		); err != nil {
			msg := conciseDatabaseMigrationError(err)
			log.Error("database migration failed; restarting on SQLite", "err", msg)
			databaseMigration.lastError = msg
			return true, nil
		}
		databaseMigration.lastError = ""
		databaseMigration.fallbackSQLiteURL = cfg.DatabaseURL
		log.Info("database migration verified; restarting on Postgres")
		return true, nil
	}
	if restart {
		databaseMigration.lastError = ""
		return true, nil
	}
	log.Info("loomarr stopped cleanly")
	return false, nil
}

func drainGeneration(
	ctx context.Context,
	drainHTTP func(context.Context) error,
	quiesce func(context.Context) error,
	finalize func(context.Context) error,
) error {
	var quiesceErr error
	if quiesce != nil {
		quiesceErr = quiesce(ctx)
	}
	return errors.Join(quiesceErr, drainHTTP(ctx), finalize(ctx))
}

type databaseMigrationState struct {
	lastError         string
	fallbackSQLiteURL string
	bootstrapDir      string
}

func (s *databaseMigrationState) restoreSQLite(cause error) error {
	if err := config.UpdateBootstrapFile(s.bootstrapDir, map[string]string{
		"DATABASE_URL": s.fallbackSQLiteURL,
	}); err != nil {
		return fmt.Errorf("%v; restore SQLite bootstrap: %w", cause, err)
	}
	s.lastError = conciseDatabaseMigrationError(cause)
	s.fallbackSQLiteURL = ""
	return nil
}

// performDatabaseCutover is the offline safety boundary. The live store must close
// successfully before a structurally read-only source is reopened for the bounded copy.
func performDatabaseCutover(
	parent context.Context,
	closeLive func() error,
	migrate func(context.Context) error,
) error {
	if err := closeLive(); err != nil {
		return fmt.Errorf("close SQLite generation: %w", err)
	}
	return runDatabaseMigrationWithTimeout(parent, databaseMigrationTimeout, migrate)
}

var errLifecycleTransitionAlreadyRequested = errors.New("a restart or database migration is already requested")

var errDatabaseURLPinned = errors.New("DATABASE_URL is pinned by the environment")

const databaseMigrationTimeout = 30 * time.Minute

type lifecycleRequest struct {
	databaseMigrationDSN string
}

// lifecycleRequester is a generation-long admission gate, not merely a channel buffer.
// Receiving the winning request must not free capacity for a handler already in flight
// to enqueue another transition that this generation will silently discard.
type lifecycleRequester struct {
	mu       sync.Mutex
	claimed  bool
	requests chan lifecycleRequest
}

func newLifecycleRequester() *lifecycleRequester {
	return &lifecycleRequester{requests: make(chan lifecycleRequest, 1)}
}

func (l *lifecycleRequester) RequestRestart() {
	_ = l.request(lifecycleRequest{})
}

func (l *lifecycleRequester) RequestMigration(dsn string) error {
	return l.request(lifecycleRequest{databaseMigrationDSN: dsn})
}

func (l *lifecycleRequester) request(req lifecycleRequest) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.claimed {
		return errLifecycleTransitionAlreadyRequested
	}
	l.claimed = true
	l.requests <- req
	return nil
}

// migrateDatabase delegates the structurally read-only source copy and verification to
// store, and only then commits the new bootstrap URL.
func migrateDatabase(ctx context.Context, sourceURL, targetURL string) error {
	// The handler checked this before enqueueing, but authority can change while the
	// response drains. Re-check at the process-owned commit point before touching either DB.
	if config.PinnedByEnv("DATABASE_URL") {
		return errDatabaseURLPinned
	}
	return migrateThenPersistDatabaseURL(
		func() error {
			_, err := store.MigrateToPostgres(ctx, sourceURL, targetURL, nil)
			return err
		},
		func() error {
			return config.UpdateBootstrapFile(
				config.ConventionalDataDir,
				map[string]string{"DATABASE_URL": targetURL},
			)
		},
	)
}

func runDatabaseMigrationWithTimeout(
	parent context.Context,
	timeout time.Duration,
	migrate func(context.Context) error,
) error {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	return migrate(ctx)
}

// migrateThenPersistDatabaseURL is the commit point: a failed copy or parity check
// must never change which database the next generation opens.
func migrateThenPersistDatabaseURL(migrate, persist func() error) error {
	if err := migrate(); err != nil {
		return fmt.Errorf("copy and verify database: %w", err)
	}
	if err := persist(); err != nil {
		return fmt.Errorf("persist DATABASE_URL: %w", err)
	}
	return nil
}

func conciseDatabaseMigrationError(err error) string {
	const maxLen = 240
	msg := strings.Join(strings.Fields(err.Error()), " ")
	runes := []rune(msg)
	if len(runes) > maxLen {
		msg = string(runes[:maxLen-1]) + "…"
	}
	return msg
}

// newLogger builds a JSON slog logger at the configured level (§14, §17).
func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}))
}

func stdoutInteractive() bool {
	info, err := os.Stdout.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func terminalWidth() int {
	if width, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && width >= 40 && width <= 240 {
		return width
	}
	return 100
}

func printStartupReport(out io.Writer, report diagnostics.StartupReport, interactive bool, width int, color bool) {
	if !interactive || out == nil {
		return
	}
	_, _ = fmt.Fprintln(out, diagnostics.RenderStartupTable(report, diagnostics.StartupTableOptions{Width: width, Color: color}))
}
