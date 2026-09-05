package app

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/loomarr/loomarr/internal/diagnostics"
	"github.com/loomarr/loomarr/internal/store"
)

var ErrApplicationQuiescing = errors.New("application generation is quiescing")

const interactiveOperationCompletionTimeout = 5 * time.Second

type interactiveOperationLauncher func(
	time.Duration,
	func(context.Context) error,
	func(context.Context, error),
) error

// Application is one fully wired Loomarr generation. The process owns the listener, signals,
// and store; Application owns the handler and every generation-scoped worker built behind it.
type Application struct {
	handler         http.Handler
	log             *slog.Logger
	lifecycle       *generationLifecycle
	playoutResolver *playoutResolver
	serverPublicURL func() string
}

// Build constructs one application generation. If composition fails after starting any owned
// work, Build unwinds that partial generation before returning the construction error.
func Build(parent context.Context, st store.Store, log *slog.Logger, ov Overrides) (*Application, error) {
	lifecycle := newGenerationLifecycle(parent)
	var resolver *playoutResolver
	handler, generationLog, serverPublicURL, err := buildHandler(lifecycle.ctx, st, log, ov, lifecycle, func(built *playoutResolver) {
		resolver = built
	})
	if err != nil {
		if ov.Startup != nil {
			ov.Startup.Complete(diagnostics.StartupCheckHTTP, diagnostics.StartupFailed,
				"application HTTP assembly failed", "/settings/system/diagnostics", "")
			ov.Startup.CompletePending(diagnostics.StartupSkipped, "application assembly stopped")
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return nil, errors.Join(err, lifecycle.shutdown(shutdownCtx))
	}
	return &Application{handler: handler, log: generationLog, lifecycle: lifecycle, playoutResolver: resolver, serverPublicURL: serverPublicURL}, nil
}

// ServerPublicURL returns the current validated address advertised to local unpaired clients.
func (a *Application) ServerPublicURL() string {
	if a == nil || a.serverPublicURL == nil {
		return ""
	}
	return a.serverPublicURL()
}

// Logger returns the generation's logger. Once a store-backed generation is built, this is the
// redacted stdout-plus-diagnostics logger; store-less builds retain the supplied stdout logger.
func (a *Application) Logger() *slog.Logger {
	if a == nil {
		return nil
	}
	return a.log
}

// Handler returns the generation's immutable HTTP entry point.
func (a *Application) Handler() http.Handler {
	if a == nil {
		return nil
	}
	return a.handler
}

// Quiesce closes generation admission, cancels generation-owned work, and stops the narrow set of
// network resources whose active responses cannot finish without lifecycle intervention. It is
// safe to call repeatedly or concurrently and deliberately does not run ordinary finalizers.
func (a *Application) Quiesce(ctx context.Context) error {
	if a == nil || a.lifecycle == nil {
		return nil
	}
	return a.lifecycle.quiesce(ctx)
}

// Shutdown cancels generation-owned work, runs explicit stops in reverse construction order,
// and waits for tracked workers. It is safe to call repeatedly or concurrently. A caller whose
// context expires may call again with a fresh context to continue waiting for the same shutdown.
func (a *Application) Shutdown(ctx context.Context) error {
	if a == nil || a.lifecycle == nil {
		return nil
	}
	return a.lifecycle.shutdown(ctx)
}

type generationLifecycle struct {
	ctx    context.Context
	cancel context.CancelFunc

	mu          sync.Mutex
	closed      bool
	quiescers   []func(context.Context) error
	stops       []func(context.Context) error
	wg          sync.WaitGroup
	quiesceOnce sync.Once
	quiesced    chan struct{}
	quiesceErr  error
	stopOnce    sync.Once
	done        chan struct{}
	err         error
}

func newGenerationLifecycle(parent context.Context) *generationLifecycle {
	ctx, cancel := context.WithCancel(parent)
	return &generationLifecycle{
		ctx: ctx, cancel: cancel, quiesced: make(chan struct{}), done: make(chan struct{}),
	}
}

// goRun starts one generation-owned worker. Construction is single-threaded, but the closed
// guard makes an accidental late start fail immediately instead of escaping Shutdown's wait.
func (l *generationLifecycle) goRun(run func(context.Context)) {
	if run == nil {
		return
	}
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		panic("app: start generation worker after shutdown")
	}
	l.wg.Add(1)
	l.mu.Unlock()
	go func() {
		defer l.wg.Done()
		run(l.ctx)
	}()
}

// startInteractiveOperation accepts request-triggered work into this application generation.
// Callers cannot supply the request context: accepted work is intentionally independent of an
// HTTP disconnect and is instead bounded by the generation plus its operation-specific timeout.
func (l *generationLifecycle) startInteractiveOperation(
	timeout time.Duration,
	run func(context.Context) error,
	complete func(context.Context, error),
) error {
	if run == nil {
		return nil
	}
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return ErrApplicationQuiescing
	}
	l.wg.Add(1)
	l.mu.Unlock()
	go func() {
		defer l.wg.Done()
		ctx := l.ctx
		if timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}
		runErr := run(ctx)
		if complete == nil {
			return
		}
		completionCtx, completionCancel := context.WithTimeout(
			context.WithoutCancel(l.ctx), interactiveOperationCompletionTimeout,
		)
		defer completionCancel()
		complete(completionCtx, runErr)
	}()
	return nil
}

// addStop registers teardown for a resource that is not represented by a worker function.
// Reverse order preserves the dependency order established during construction.
func (l *generationLifecycle) addStop(stop func(context.Context) error) {
	if stop == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		panic("app: register generation stop after shutdown")
	}
	l.stops = append(l.stops, stop)
}

// addQuiesce registers a network-facing resource that must stop before HTTP drain can wait for
// active responses. Reverse order preserves the dependency order established during construction.
func (l *generationLifecycle) addQuiesce(quiesce func(context.Context) error) {
	if quiesce == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		panic("app: register generation quiescer after shutdown")
	}
	l.quiescers = append(l.quiescers, quiesce)
}

func (l *generationLifecycle) quiesce(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	l.startQuiesce(ctx)
	select {
	case <-l.quiesced:
		return l.quiesceErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (l *generationLifecycle) startQuiesce(ctx context.Context) {
	l.quiesceOnce.Do(func() {
		l.mu.Lock()
		l.closed = true
		quiescers := append([]func(context.Context) error(nil), l.quiescers...)
		l.mu.Unlock()
		l.cancel()
		go l.finishQuiesce(ctx, quiescers)
	})
}

func (l *generationLifecycle) finishQuiesce(ctx context.Context, quiescers []func(context.Context) error) {
	var errs []error
	for _, quiescer := range slices.Backward(quiescers) {
		if err := quiescer(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	l.quiesceErr = errors.Join(errs...)
	close(l.quiesced)
}

func (l *generationLifecycle) shutdown(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	l.stopOnce.Do(func() {
		l.startQuiesce(ctx)
		l.mu.Lock()
		stops := append([]func(context.Context) error(nil), l.stops...)
		l.mu.Unlock()
		go l.finish(ctx, stops)
	})

	select {
	case <-l.done:
		return l.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (l *generationLifecycle) finish(ctx context.Context, stops []func(context.Context) error) {
	<-l.quiesced
	var stopErrs []error
	for _, stop := range slices.Backward(stops) {
		if err := stop(ctx); err != nil {
			stopErrs = append(stopErrs, err)
		}
	}
	l.wg.Wait()
	l.err = errors.Join(l.quiesceErr, errors.Join(stopErrs...))
	close(l.done)
}
