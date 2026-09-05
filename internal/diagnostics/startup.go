package diagnostics

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// StartupState is the derived readiness state of one application generation.
type StartupState string

const (
	StartupStarting StartupState = "starting"
	StartupReady    StartupState = "ready"
	StartupDegraded StartupState = "degraded"
	StartupBlocked  StartupState = "blocked"
)

// StartupCheckStatus is the bounded lifecycle of one startup check.
type StartupCheckStatus string

const (
	StartupPending StartupCheckStatus = "pending"
	StartupPassed  StartupCheckStatus = "passed"
	StartupWarning StartupCheckStatus = "warning"
	StartupFailed  StartupCheckStatus = "failed"
	StartupSkipped StartupCheckStatus = "skipped"
)

// HealthCheckMode distinguishes immutable boot evidence from a fact that must be refreshed while
// the generation is running.
type HealthCheckMode string

const (
	HealthCheckStartup    HealthCheckMode = "startup"
	HealthCheckContinuous HealthCheckMode = "continuous"
)

// HealthState is the operator-facing state of the running application generation.
type HealthState string

const (
	HealthStarting  HealthState = "starting"
	HealthHealthy   HealthState = "healthy"
	HealthDegraded  HealthState = "degraded"
	HealthUnhealthy HealthState = "unhealthy"
)

// HealthCheckStatus is the current, freshness-aware state of one Health check.
type HealthCheckStatus string

const (
	HealthPending HealthCheckStatus = "pending"
	HealthPassed  HealthCheckStatus = "passed"
	HealthWarning HealthCheckStatus = "warning"
	HealthFailed  HealthCheckStatus = "failed"
	HealthSkipped HealthCheckStatus = "skipped"
	HealthStale   HealthCheckStatus = "stale"
)

// StartupCheck is one ordered, operator-readable fact in a StartupReport.
type StartupCheck struct {
	Key              string             `json:"key"`
	Label            string             `json:"label"`
	Required         bool               `json:"required"`
	Status           StartupCheckStatus `json:"status" enum:"pending,passed,warning,failed,skipped"`
	StartedAt        int64              `json:"startedAt,omitempty"`
	EndedAt          int64              `json:"endedAt,omitempty"`
	DurationMillis   int64              `json:"durationMillis"`
	Detail           string             `json:"detail,omitempty"`
	RemediationRoute string             `json:"remediationRoute,omitempty"`
	DiagnosticEvent  string             `json:"diagnosticEvent,omitempty"`
	Mode             HealthCheckMode    `json:"mode" enum:"startup,continuous"`
	FreshFor         time.Duration      `json:"-"`
}

// HealthCheck is one current observation. ConsecutiveFailures is intentionally exposed: it tells
// an operator that Loomarr is confirming a transient failure rather than silently ignoring it.
type HealthCheck struct {
	Key                 string            `json:"key"`
	Label               string            `json:"label"`
	Required            bool              `json:"required"`
	Mode                HealthCheckMode   `json:"mode" enum:"startup,continuous"`
	Status              HealthCheckStatus `json:"status" enum:"pending,passed,warning,failed,skipped,stale"`
	ObservedAt          int64             `json:"observedAt,omitempty"`
	FreshUntil          int64             `json:"freshUntil,omitempty"`
	ConsecutiveFailures int               `json:"consecutiveFailures,omitempty"`
	Detail              string            `json:"detail,omitempty"`
	RemediationRoute    string            `json:"remediationRoute,omitempty"`
	DiagnosticEvent     string            `json:"diagnosticEvent,omitempty"`
}

// HealthReport is a caller-owned snapshot of the running generation's Current Health.
type HealthReport struct {
	GenerationID      string        `json:"generationId"`
	Generation        int           `json:"generation"`
	Version           string        `json:"version"`
	ProcessStartedAt  int64         `json:"processStartedAt"`
	GenerationStarted int64         `json:"generationStartedAt"`
	UpdatedAt         int64         `json:"updatedAt"`
	NextRefreshAt     int64         `json:"nextRefreshAt,omitempty"`
	State             HealthState   `json:"state" enum:"starting,healthy,degraded,unhealthy"`
	Checks            []HealthCheck `json:"checks"`
}

// HealthObservation is the small caller interface for updating a continuous check. Immediate
// failures bypass the periodic two-failure threshold when a lifecycle seam already knows the
// component cannot function.
type HealthObservation struct {
	Status           HealthCheckStatus
	Detail           string
	RemediationRoute string
	DiagnosticEvent  string
	Immediate        bool
	FreshFor         time.Duration
}

// StartupReport is the immutable snapshot exposed to projections. Times are epoch milliseconds,
// matching the rest of Diagnostics and avoiding dialect-specific timestamp behavior.
type StartupReport struct {
	ID                string         `json:"id"`
	Generation        int            `json:"generation"`
	Version           string         `json:"version"`
	ProcessStartedAt  int64          `json:"processStartedAt"`
	GenerationStarted int64          `json:"generationStartedAt"`
	GenerationEnded   int64          `json:"generationEndedAt,omitempty"`
	DurationMillis    int64          `json:"durationMillis"`
	State             StartupState   `json:"state" enum:"starting,ready,degraded,blocked"`
	Checks            []StartupCheck `json:"checks"`
}

// Startup owns the mutable current-generation state. Callers mutate by stable check key and all
// consumers receive copies, so renderers and transports cannot race startup or rewrite truth.
type Startup struct {
	mu           sync.RWMutex
	report       StartupReport
	now          func() time.Time
	persisted    bool
	persisting   bool
	recorder     *Recorder
	instanceID   string
	health       HealthReport
	freshFor     map[string]time.Duration
	healthBase   string
	healthBusy   bool
	healthNext   []healthTransition
	healthNotify func()
}

type healthTransition struct {
	fingerprint string
	report      HealthReport
}

const (
	StartupCheckConfiguration    = "configuration"
	StartupCheckDatabase         = "database"
	StartupCheckGeneratedSecrets = "generated_secrets"
	StartupCheckImageWorker      = "image_worker"
	StartupCheckHTTP             = "http"
	StartupCheckMediaServer      = "media_server"
	StartupCheckTunarr           = "tunarr"
	StartupCheckRequester        = "requester"
	StartupCheckLLM              = "llm"
	StartupCheckTMDB             = "tmdb"

	startupCompleteEvent  = "startup.complete"
	healthTransitionEvent = "health.transition"
)

// PersistCompleted durably checkpoints a terminal report. Startup completion is retained state,
// not best-effort traffic: it bypasses the ordinary bounded queue but uses the same normalization,
// redaction boundary, sink, and write timeout. A failed write remains retryable.
func (s *Startup) PersistCompleted() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	if s.persisted || s.persisting || s.report.GenerationEnded == 0 || s.recorder == nil {
		s.mu.Unlock()
		return false
	}
	report := s.report
	report.Checks = append([]StartupCheck(nil), s.report.Checks...)
	recorder, instanceID := s.recorder, s.instanceID
	s.persisting = true
	s.mu.Unlock()
	level := LevelInfo
	switch report.State {
	case StartupDegraded:
		level = LevelWarn
	case StartupBlocked:
		level = LevelError
	}
	err := recorder.RecordDurable(context.Background(), Event{
		OccurredAt: time.UnixMilli(report.GenerationEnded), Level: level, Source: SourceServer,
		Subsystem: "startup", Name: startupCompleteEvent, Message: "application generation startup completed",
		InstanceID: instanceID, Attributes: map[string]any{"report": startupReportMap(report)},
	})
	s.mu.Lock()
	s.persisting = false
	if err == nil {
		s.persisted = true
	}
	s.mu.Unlock()
	return err == nil
}

func startupReportMap(report StartupReport) map[string]any {
	encoded, err := json.Marshal(report)
	if err != nil {
		return map[string]any{}
	}
	var value map[string]any
	if err := json.Unmarshal(encoded, &value); err != nil {
		return map[string]any{}
	}
	return value
}

// AttachPersistence connects the report to the generation's redacted bounded recorder once the
// store exists. Attaching after startup completed immediately emits the retained projection.
func (s *Startup) AttachPersistence(recorder *Recorder, instanceID string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.recorder, s.instanceID = recorder, instanceID
	s.mu.Unlock()
	s.PersistCompleted()
	s.persistHealthTransitions()
}

// SetHealthNotifier attaches a best-effort latency signal. The notifier carries no state; readers
// always refetch Health, so dropping a notification cannot make a client incorrect.
func (s *Startup) SetHealthNotifier(notify func()) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.healthNotify = notify
	s.mu.Unlock()
}

// StartupReports combines the current in-memory generation with completed reports retained as
// Diagnostic events. It owns decoding so HTTP and UI never depend on event attribute layout.
type StartupReports struct {
	current *Startup
	reader  EventReader
	now     func() time.Time
}

func NewStartupReports(current *Startup, reader EventReader, now func() time.Time) *StartupReports {
	if now == nil {
		now = time.Now
	}
	return &StartupReports{current: current, reader: reader, now: now}
}

func (s *StartupReports) Current() StartupReport {
	if s == nil || s.current == nil {
		return StartupReport{State: StartupBlocked}
	}
	return s.current.Snapshot()
}

// Health returns Current Health from the same generation state that backs readiness.
func (s *StartupReports) Health() HealthReport {
	if s == nil || s.current == nil {
		return HealthReport{State: HealthUnhealthy}
	}
	return s.current.Health()
}

// Recent returns the current generation first followed by distinct retained generations.
func (s *StartupReports) Recent(ctx context.Context, limit int) ([]StartupReport, error) {
	if limit < 1 || limit > 20 {
		return nil, errors.New("startup report limit must be between 1 and 20")
	}
	current := s.Current()
	reports := []StartupReport{current}
	if s == nil || s.reader == nil || limit == 1 {
		return reports, nil
	}
	records, err := s.reader.QueryDiagnosticEvents(ctx, EventStoreQuery{
		From: 0, To: s.now().UnixMilli(), Limit: 20, Event: startupCompleteEvent,
	})
	if err != nil {
		return nil, fmt.Errorf("query startup reports: %w", err)
	}
	for _, record := range records {
		var attributes struct {
			Report StartupReport `json:"report"`
		}
		if err := json.Unmarshal([]byte(record.AttributesJSON), &attributes); err != nil {
			return nil, fmt.Errorf("decode startup report %s: %w", record.ID, err)
		}
		if attributes.Report.ID == "" || attributes.Report.ID == current.ID {
			continue
		}
		reports = append(reports, attributes.Report)
		if len(reports) == limit {
			break
		}
	}
	return reports, nil
}

// NewStartup creates a generation with its complete ordered check set already pending. Declaring
// checks up front prevents readiness from briefly reporting true before a later required check is
// discovered.
func NewStartup(processStarted time.Time, generation int, version string, checks []StartupCheck, now func() time.Time) *Startup {
	if now == nil {
		now = time.Now
	}
	started := now()
	owned := append([]StartupCheck(nil), checks...)
	healthChecks := make([]HealthCheck, len(owned))
	freshFor := make(map[string]time.Duration, len(owned))
	for i := range owned {
		if owned[i].Mode == "" {
			owned[i].Mode = HealthCheckStartup
		}
		owned[i].Status = StartupPending
		owned[i].StartedAt = started.UnixMilli()
		healthChecks[i] = HealthCheck{
			Key: owned[i].Key, Label: owned[i].Label, Required: owned[i].Required,
			Mode: owned[i].Mode, Status: HealthPending,
			RemediationRoute: owned[i].RemediationRoute,
		}
		if owned[i].Mode == HealthCheckContinuous && owned[i].FreshFor > 0 {
			freshFor[owned[i].Key] = owned[i].FreshFor
		}
	}
	id := startupID()
	return &Startup{now: now, report: StartupReport{
		ID: id, Generation: generation, Version: version,
		ProcessStartedAt: processStarted.UnixMilli(), GenerationStarted: started.UnixMilli(),
		State: StartupStarting, Checks: owned,
	}, health: HealthReport{
		GenerationID: id, Generation: generation, Version: version,
		ProcessStartedAt: processStarted.UnixMilli(), GenerationStarted: started.UnixMilli(),
		UpdatedAt: started.UnixMilli(), State: HealthStarting, Checks: healthChecks,
	}, freshFor: freshFor}
}

// Complete records one terminal check result. Unknown keys and attempts to rewrite a completed
// check are ignored: startup evidence is append-only within a generation.
func (s *Startup) Complete(key string, status StartupCheckStatus, detail, remediation, eventID string) bool {
	if s == nil || status == StartupPending {
		return false
	}
	s.mu.Lock()
	for i := range s.report.Checks {
		check := &s.report.Checks[i]
		if check.Key != key || check.Status != StartupPending {
			continue
		}
		ended := s.now().UnixMilli()
		check.Status, check.EndedAt = status, ended
		check.DurationMillis = max(0, ended-check.StartedAt)
		check.Detail = boundedStartupDetail(detail)
		check.RemediationRoute, check.DiagnosticEvent = remediation, eventID
		s.observeStartupLocked(key, status, detail, remediation, eventID, ended)
		s.deriveLocked()
		s.mu.Unlock()
		s.PersistCompleted()
		return true
	}
	s.mu.Unlock()
	return false
}

// Observe replaces the current observation for a continuous check without rewriting the immutable
// Startup report. Unknown and startup-only keys are rejected.
func (s *Startup) Observe(key string, observation HealthObservation) bool {
	if s == nil || observation.Status == HealthPending || observation.Status == HealthStale {
		return false
	}
	s.mu.Lock()
	for i := range s.health.Checks {
		check := &s.health.Checks[i]
		if check.Key != key || check.Mode != HealthCheckContinuous {
			continue
		}
		now := s.now().UnixMilli()
		status := observation.Status
		if observation.FreshFor > 0 {
			s.freshFor[key] = observation.FreshFor
		}
		if status == HealthFailed {
			check.ConsecutiveFailures++
			if check.Required && !observation.Immediate && check.Status == HealthPassed && check.ConsecutiveFailures < 2 {
				status = HealthWarning
			}
		} else {
			check.ConsecutiveFailures = 0
		}
		check.Status = status
		check.ObservedAt = now
		check.FreshUntil = freshUntil(now, s.freshFor[key], status)
		check.Detail = boundedStartupDetail(observation.Detail)
		if observation.RemediationRoute != "" {
			check.RemediationRoute = observation.RemediationRoute
		}
		check.DiagnosticEvent = observation.DiagnosticEvent
		s.health.UpdatedAt = now
		s.deriveHealthLocked(now)
		changed := s.queueHealthTransitionLocked()
		notify := s.healthNotify
		s.mu.Unlock()
		if changed && notify != nil {
			notify()
		}
		s.persistHealthTransitions()
		return true
	}
	s.mu.Unlock()
	return false
}

func (s *Startup) observeStartupLocked(
	key string,
	status StartupCheckStatus,
	detail, remediation, eventID string,
	observedAt int64,
) {
	for i := range s.health.Checks {
		check := &s.health.Checks[i]
		if check.Key != key {
			continue
		}
		check.Status = healthStatus(status)
		check.ObservedAt = observedAt
		check.FreshUntil = freshUntil(observedAt, s.freshFor[key], check.Status)
		check.Detail = boundedStartupDetail(detail)
		if remediation != "" {
			check.RemediationRoute = remediation
		}
		check.DiagnosticEvent = eventID
		s.health.UpdatedAt = observedAt
		return
	}
}

// CompletePending terminates every check that a failed prerequisite made unreachable. It keeps
// declaration order and never rewrites evidence already recorded at a more precise seam.
func (s *Startup) CompletePending(status StartupCheckStatus, detail string) {
	if s == nil || status == StartupPending {
		return
	}
	for _, check := range s.Snapshot().Checks {
		if check.Status == StartupPending {
			s.Complete(check.Key, status, detail, check.RemediationRoute, "")
		}
	}
}

// Snapshot returns a caller-owned report value.
func (s *Startup) Snapshot() StartupReport {
	if s == nil {
		return StartupReport{State: StartupBlocked}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := s.report
	result.Checks = append([]StartupCheck(nil), s.report.Checks...)
	return result
}

// Health returns a caller-owned, freshness-aware Current Health snapshot.
func (s *Startup) Health() HealthReport {
	if s == nil {
		return HealthReport{State: HealthUnhealthy}
	}
	s.mu.Lock()
	now := s.now().UnixMilli()
	s.deriveHealthLocked(now)
	changed := s.queueHealthTransitionLocked()
	notify := s.healthNotify
	result := s.health
	result.Checks = append([]HealthCheck(nil), s.health.Checks...)
	s.mu.Unlock()
	if changed && notify != nil {
		notify()
	}
	s.persistHealthTransitions()
	return result
}

// Ready is the shared readiness projection used by /readyz and the report API.
func (s *Startup) Ready() (bool, string) {
	report := s.Health()
	switch report.State {
	case HealthHealthy:
		return true, "ok"
	case HealthDegraded:
		return true, "running with warnings"
	case HealthStarting:
		return false, "health checks are still running"
	default:
		for _, check := range report.Checks {
			if check.Required && check.Status == HealthStale {
				label := strings.TrimSpace(check.Label)
				if label == "" {
					label = strings.TrimSpace(check.Key)
				}
				if label != "" {
					return false, label + " health observation is stale"
				}
				return false, "a required health observation is stale"
			}
			if check.Required && check.Status == HealthFailed && check.Detail != "" {
				return false, check.Detail
			}
		}
		return false, "application health is unhealthy"
	}
}

func (s *Startup) deriveLocked() {
	state := StartupReady
	allTerminal := true
	for _, check := range s.report.Checks {
		if check.Required && check.Status != StartupPassed && check.Status != StartupPending {
			state = StartupBlocked
			break
		}
		if check.Required && check.Status == StartupPending {
			state = StartupStarting
		}
		if check.Status == StartupPending {
			allTerminal = false
		}
		if !check.Required && (check.Status == StartupWarning || check.Status == StartupFailed) && state == StartupReady {
			state = StartupDegraded
		}
	}
	s.report.State = state
	s.deriveHealthLocked(s.now().UnixMilli())
	if allTerminal && s.report.GenerationEnded == 0 {
		s.report.GenerationEnded = s.now().UnixMilli()
		s.report.DurationMillis = max(0, s.report.GenerationEnded-s.report.GenerationStarted)
		// Startup completion is its own durable snapshot. Establish the Current Health baseline
		// here so the initial state is not duplicated as a health-transition event.
		s.healthBase = healthFingerprint(s.health)
	}
}

func (s *Startup) queueHealthTransitionLocked() bool {
	if s.report.GenerationEnded == 0 {
		return false
	}
	fingerprint := healthFingerprint(s.health)
	if len(s.healthNext) > 0 {
		if s.healthNext[len(s.healthNext)-1].fingerprint == fingerprint {
			return false
		}
	} else if fingerprint == s.healthBase {
		return false
	}
	report := s.health
	report.Checks = append([]HealthCheck(nil), s.health.Checks...)
	s.healthNext = append(s.healthNext, healthTransition{fingerprint: fingerprint, report: report})
	return true
}

func (s *Startup) persistHealthTransitions() {
	if s == nil {
		return
	}
	for {
		s.mu.Lock()
		if s.healthBusy || s.recorder == nil || len(s.healthNext) == 0 {
			s.mu.Unlock()
			return
		}
		transition := s.healthNext[0]
		recorder, instanceID := s.recorder, s.instanceID
		s.healthBusy = true
		s.mu.Unlock()

		level := LevelInfo
		switch transition.report.State {
		case HealthDegraded:
			level = LevelWarn
		case HealthUnhealthy:
			level = LevelError
		}
		err := recorder.RecordDurable(context.Background(), Event{
			OccurredAt: time.UnixMilli(transition.report.UpdatedAt), Level: level, Source: SourceServer,
			Subsystem: "health", Name: healthTransitionEvent,
			Message: "application health changed", InstanceID: instanceID,
			Attributes: map[string]any{"health": healthReportMap(transition.report)},
		})

		s.mu.Lock()
		s.healthBusy = false
		if err != nil {
			s.mu.Unlock()
			return
		}
		s.healthBase = transition.fingerprint
		if len(s.healthNext) > 0 && s.healthNext[0].fingerprint == transition.fingerprint {
			s.healthNext = s.healthNext[1:]
		}
		s.mu.Unlock()
	}
}

func healthReportMap(report HealthReport) map[string]any {
	encoded, err := json.Marshal(report)
	if err != nil {
		return map[string]any{}
	}
	var value map[string]any
	if err := json.Unmarshal(encoded, &value); err != nil {
		return map[string]any{}
	}
	return value
}

func healthFingerprint(report HealthReport) string {
	var value strings.Builder
	value.WriteString(string(report.State))
	for _, check := range report.Checks {
		value.WriteByte('|')
		value.WriteString(check.Key)
		value.WriteByte('=')
		value.WriteString(string(check.Status))
	}
	return value.String()
}

func (s *Startup) deriveHealthLocked(now int64) {
	state := HealthHealthy
	nextRefresh := int64(0)
	becameStale := false
	for i := range s.health.Checks {
		check := &s.health.Checks[i]
		status := check.Status
		if check.Mode == HealthCheckContinuous && check.FreshUntil > 0 {
			if nextRefresh == 0 || check.FreshUntil < nextRefresh {
				nextRefresh = check.FreshUntil
			}
			if now > check.FreshUntil && status != HealthPending && status != HealthSkipped {
				status = HealthStale
				if check.Status != HealthStale {
					check.Status = HealthStale
					becameStale = true
				}
			}
		}
		if check.Required && (status == HealthFailed || status == HealthSkipped || status == HealthStale) {
			state = HealthUnhealthy
			continue
		}
		if check.Required && status == HealthPending && state != HealthUnhealthy {
			state = HealthStarting
			continue
		}
		if (status == HealthWarning || (!check.Required && (status == HealthFailed || status == HealthStale))) && state == HealthHealthy {
			state = HealthDegraded
		}
	}
	s.health.State = state
	s.health.NextRefreshAt = nextRefresh
	if becameStale {
		s.health.UpdatedAt = now
	}
}

func healthStatus(status StartupCheckStatus) HealthCheckStatus {
	switch status {
	case StartupPassed:
		return HealthPassed
	case StartupWarning:
		return HealthWarning
	case StartupFailed:
		return HealthFailed
	case StartupSkipped:
		return HealthSkipped
	default:
		return HealthPending
	}
}

func freshUntil(observedAt int64, duration time.Duration, status HealthCheckStatus) int64 {
	if duration <= 0 || status == HealthPending || status == HealthSkipped {
		return 0
	}
	return observedAt + duration.Milliseconds()
}

func boundedStartupDetail(value string) string {
	const limit = 512
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	return value[:limit-3] + "..."
}

func startupID() string {
	var value [12]byte
	if _, err := rand.Read(value[:]); err == nil {
		return "startup_" + hex.EncodeToString(value[:])
	}
	return "startup_" + time.Now().UTC().Format("20060102T150405.000000000")
}
