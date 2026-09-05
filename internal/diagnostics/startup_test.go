package diagnostics

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

type startupRetrySink struct {
	mu      sync.Mutex
	fail    bool
	records []Record
}

func (s *startupRetrySink) AppendDiagnosticEvents(_ context.Context, records []Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail {
		s.fail = false
		return errors.New("temporary store failure")
	}
	s.records = append(s.records, records...)
	return nil
}

func TestCompletedStartupPersistsStructuredReportAndArchiveReadsIt(t *testing.T) {
	now := time.Unix(100, 0)
	sink := &memorySink{}
	recorder := New(sink, Options{Now: func() time.Time { return now }, BatchSize: 1, FlushInterval: time.Hour})
	startup := NewStartup(now, 1, "v1.2.3", []StartupCheck{{Key: "database", Label: "Database", Required: true}}, func() time.Time { return now })
	startup.AttachPersistence(recorder, "instance-1")
	startup.Complete("database", StartupPassed, "ready", "", "")
	// A completed report is a durable checkpoint, not something that only appears after the
	// best-effort recorder happens to flush.
	if records := sink.snapshot(); len(records) != 1 {
		t.Fatalf("records before recorder flush = %d, want 1", len(records))
	}
	if err := recorder.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	records := sink.snapshot()
	if len(records) != 1 || records[0].Event != startupCompleteEvent || records[0].InstanceID != "instance-1" {
		t.Fatalf("persisted records = %+v", records)
	}
	var attributes struct {
		Report StartupReport `json:"report"`
	}
	if err := json.Unmarshal([]byte(records[0].AttributesJSON), &attributes); err != nil {
		t.Fatal(err)
	}
	if attributes.Report.ID != startup.Snapshot().ID || len(attributes.Report.Checks) != 1 {
		t.Fatalf("persisted report = %+v", attributes.Report)
	}

	archive := NewStartupReports(startup, eventReaderFunc(func(_ context.Context, query EventStoreQuery) ([]Record, error) {
		if query.Event != startupCompleteEvent || query.Limit != 20 {
			t.Fatalf("archive query = %+v", query)
		}
		return records, nil
	}), func() time.Time { return now.Add(time.Hour) })
	reports, err := archive.Recent(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(reports) != 1 || reports[0].ID != startup.Snapshot().ID {
		t.Fatalf("reports = %+v", reports)
	}
}

func TestStartupReportsPreservePriorGeneration(t *testing.T) {
	now := time.Unix(100, 0)
	sink := &memorySink{}
	recorder := New(sink, Options{Now: func() time.Time { return now }})
	first := NewStartup(now, 1, "v1", []StartupCheck{{Key: "database", Required: true}}, func() time.Time { return now })
	first.AttachPersistence(recorder, "instance-1")
	first.Complete("database", StartupPassed, "ready", "", "")
	now = now.Add(time.Second)
	second := NewStartup(time.Unix(100, 0), 2, "v2", []StartupCheck{{Key: "database", Required: true}}, func() time.Time { return now })
	second.AttachPersistence(recorder, "instance-1")
	second.Complete("database", StartupPassed, "ready", "", "")
	records := sink.snapshot()
	reader := eventReaderFunc(func(context.Context, EventStoreQuery) ([]Record, error) {
		return []Record{records[1], records[0]}, nil
	})
	reports, err := NewStartupReports(second, reader, func() time.Time { return now }).Recent(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(reports) != 2 || reports[0].Generation != 2 || reports[1].Generation != 1 || reports[0].ID == reports[1].ID {
		t.Fatalf("reports = %+v", reports)
	}
}

func TestCompletedStartupPersistenceFailureRemainsRetryable(t *testing.T) {
	now := time.Unix(100, 0)
	sink := &startupRetrySink{fail: true}
	recorder := New(sink, Options{Now: func() time.Time { return now }})
	startup := NewStartup(now, 1, "v1", []StartupCheck{{Key: "database", Required: true}}, func() time.Time { return now })
	startup.AttachPersistence(recorder, "instance-1")
	startup.Complete("database", StartupPassed, "ready", "", "")
	if !startup.PersistCompleted() {
		t.Fatal("terminal report did not retry its failed durable checkpoint")
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.records) != 1 || sink.records[0].Event != startupCompleteEvent {
		t.Fatalf("records = %+v", sink.records)
	}
}

func TestStartupDerivesReadinessFromRequiredChecks(t *testing.T) {
	now := time.Unix(100, 0)
	report := NewStartup(now.Add(-time.Minute), 2, "v1.2.3", []StartupCheck{
		{Key: "config", Label: "Configuration", Required: true},
		{Key: "database", Label: "Database", Required: true},
		{Key: "media", Label: "Media server", Required: false},
	}, func() time.Time { return now })

	if ready, _ := report.Ready(); ready {
		t.Fatal("pending required checks reported ready")
	}
	report.Complete("config", StartupPassed, "valid", "", "")
	report.Complete("database", StartupPassed, "SQLite ready", "", "")
	if ready, _ := report.Ready(); !ready {
		t.Fatal("completed required checks did not report ready")
	}
	if got := report.Snapshot().State; got != StartupReady {
		t.Fatalf("state = %q, want ready", got)
	}

	report.Complete("media", StartupWarning, "unreachable", "/settings/media", "diag_1")
	got := report.Snapshot()
	if got.State != StartupDegraded || got.GenerationEnded == 0 {
		t.Fatalf("completed report = %+v", got)
	}
	if ready, detail := report.Ready(); !ready || detail != "running with warnings" {
		t.Fatalf("degraded readiness = (%v, %q)", ready, detail)
	}
}

func TestCurrentHealthRefreshesContinuousChecksWithoutRewritingStartup(t *testing.T) {
	now := time.Unix(100, 0)
	report := NewStartup(now, 1, "v1", []StartupCheck{
		{Key: "database", Label: "Database", Required: true, Mode: HealthCheckContinuous, FreshFor: time.Minute},
		{Key: "config", Label: "Configuration", Required: true},
	}, func() time.Time { return now })
	report.Complete("database", StartupPassed, "available at boot", "", "")
	report.Complete("config", StartupPassed, "valid", "", "")
	startup := report.Snapshot()

	now = now.Add(30 * time.Second)
	if !report.Observe("database", HealthObservation{Status: HealthFailed, Detail: "probe unavailable"}) {
		t.Fatal("continuous observation was rejected")
	}
	health := report.Health()
	if health.State != HealthDegraded || health.Checks[0].Status != HealthWarning || health.Checks[0].ConsecutiveFailures != 1 {
		t.Fatalf("first failed observation = %+v", health)
	}
	if ready, _ := report.Ready(); !ready {
		t.Fatal("one periodic failure flapped readiness")
	}

	now = now.Add(time.Second)
	report.Observe("database", HealthObservation{Status: HealthFailed, Detail: "still unavailable"})
	health = report.Health()
	if health.State != HealthUnhealthy || health.Checks[0].Status != HealthFailed || health.Checks[0].ConsecutiveFailures != 2 {
		t.Fatalf("confirmed failure = %+v", health)
	}
	if ready, detail := report.Ready(); ready || detail != "still unavailable" {
		t.Fatalf("confirmed readiness = (%v, %q)", ready, detail)
	}

	now = now.Add(time.Second)
	report.Observe("database", HealthObservation{Status: HealthPassed, Detail: "recovered"})
	health = report.Health()
	if health.State != HealthHealthy || health.Checks[0].Status != HealthPassed || health.Checks[0].ConsecutiveFailures != 0 {
		t.Fatalf("recovery = %+v", health)
	}
	if got := report.Snapshot(); got.Checks[0].Detail != startup.Checks[0].Detail || got.Checks[0].Status != startup.Checks[0].Status {
		t.Fatalf("continuous observation rewrote startup history: before=%+v after=%+v", startup, got)
	}
}

func TestCurrentHealthExpiresContinuousObservation(t *testing.T) {
	now := time.Unix(100, 0)
	report := NewStartup(now, 1, "v1", []StartupCheck{
		{Key: "database", Label: "Database", Required: true, Mode: HealthCheckContinuous, FreshFor: time.Minute},
		{Key: "media", Required: false, Mode: HealthCheckContinuous, FreshFor: 2 * time.Minute},
	}, func() time.Time { return now })
	report.Complete("database", StartupPassed, "available", "", "")
	report.Complete("media", StartupSkipped, "not configured", "", "")

	now = now.Add(time.Minute + time.Millisecond)
	health := report.Health()
	if health.State != HealthUnhealthy || health.Checks[0].Status != HealthStale {
		t.Fatalf("stale required observation = %+v", health)
	}
	if health.Checks[1].Status != HealthSkipped {
		t.Fatalf("unconfigured optional check became stale: %+v", health.Checks[1])
	}
	if ready, detail := report.Ready(); ready || detail != "Database health observation is stale" {
		t.Fatalf("stale required readiness = (%v, %q)", ready, detail)
	}
}

func TestRequiredStartupSkipIsUnhealthy(t *testing.T) {
	now := time.Unix(100, 0)
	report := NewStartup(now, 1, "v1", []StartupCheck{{Key: "database", Required: true}}, func() time.Time { return now })
	report.Complete("database", StartupSkipped, "database unavailable", "", "")
	if got := report.Snapshot().State; got != StartupBlocked {
		t.Fatalf("startup state = %q, want blocked", got)
	}
	if got := report.Health().State; got != HealthUnhealthy {
		t.Fatalf("health state = %q, want unhealthy", got)
	}
}

func TestCurrentHealthPersistsOnlyIncidentTransitions(t *testing.T) {
	now := time.Unix(100, 0)
	sink := &memorySink{}
	recorder := New(sink, Options{Now: func() time.Time { return now }})
	report := NewStartup(now, 1, "v1", []StartupCheck{{
		Key: "media", Required: false, Mode: HealthCheckContinuous, FreshFor: time.Minute,
	}}, func() time.Time { return now })
	report.AttachPersistence(recorder, "instance-1")
	report.Complete("media", StartupPassed, "available", "", "")

	now = now.Add(time.Second)
	report.Observe("media", HealthObservation{Status: HealthFailed, Detail: "unavailable"})
	now = now.Add(time.Second)
	report.Observe("media", HealthObservation{Status: HealthFailed, Detail: "still unavailable"})
	now = now.Add(time.Second)
	report.Observe("media", HealthObservation{Status: HealthPassed, Detail: "recovered"})

	records := sink.snapshot()
	if len(records) != 3 {
		t.Fatalf("records = %d, want startup plus failure and recovery", len(records))
	}
	if records[0].Event != startupCompleteEvent || records[1].Event != healthTransitionEvent || records[2].Event != healthTransitionEvent {
		t.Fatalf("events = %q, %q, %q", records[0].Event, records[1].Event, records[2].Event)
	}
	if records[1].Level != LevelWarn || records[2].Level != LevelInfo {
		t.Fatalf("transition levels = %q, %q", records[1].Level, records[2].Level)
	}
}

func TestCurrentHealthNotifiesOnlyMaterialTransitions(t *testing.T) {
	now := time.Unix(100, 0)
	report := NewStartup(now, 1, "v1", []StartupCheck{{
		Key: "media", Mode: HealthCheckContinuous, FreshFor: time.Minute,
	}}, func() time.Time { return now })
	report.Complete("media", StartupPassed, "available", "", "")
	notifications := 0
	report.SetHealthNotifier(func() { notifications++ })

	report.Observe("media", HealthObservation{Status: HealthFailed, Detail: "unavailable"})
	report.Observe("media", HealthObservation{Status: HealthFailed, Detail: "still unavailable"})
	report.Observe("media", HealthObservation{Status: HealthPassed, Detail: "recovered"})
	if notifications != 2 {
		t.Fatalf("notifications = %d, want failure and recovery only", notifications)
	}
}

func TestCurrentHealthTransitionCheckpointRetries(t *testing.T) {
	now := time.Unix(100, 0)
	sink := &startupRetrySink{}
	recorder := New(sink, Options{Now: func() time.Time { return now }})
	report := NewStartup(now, 1, "v1", []StartupCheck{{
		Key: "media", Mode: HealthCheckContinuous, FreshFor: time.Minute,
	}}, func() time.Time { return now })
	report.AttachPersistence(recorder, "instance-1")
	report.Complete("media", StartupPassed, "available", "", "")

	sink.mu.Lock()
	sink.fail = true
	sink.mu.Unlock()
	now = now.Add(time.Second)
	report.Observe("media", HealthObservation{Status: HealthFailed, Detail: "unavailable"})
	// Repeating the same observation creates no duplicate transition, but retries the retained
	// checkpoint that failed.
	report.Observe("media", HealthObservation{Status: HealthFailed, Detail: "unavailable"})

	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.records) != 2 || sink.records[1].Event != healthTransitionEvent {
		t.Fatalf("records = %+v", sink.records)
	}
}

func TestStartupRequiredFailureBlocksAndCannotBeRewritten(t *testing.T) {
	now := time.Unix(100, 0)
	report := NewStartup(now, 1, "dev", []StartupCheck{
		{Key: "database", Label: "Database", Required: true},
	}, func() time.Time { return now })
	if !report.Complete("database", StartupFailed, "migration failed", "/system/database", "diag_2") {
		t.Fatal("first completion was rejected")
	}
	if report.Complete("database", StartupPassed, "rewritten", "", "") {
		t.Fatal("terminal check was rewritten")
	}
	if ready, detail := report.Ready(); ready || detail != "migration failed" {
		t.Fatalf("blocked readiness = (%v, %q)", ready, detail)
	}
	got := report.Snapshot()
	if got.Checks[0].Status != StartupFailed || got.Checks[0].DiagnosticEvent != "diag_2" {
		t.Fatalf("check = %+v", got.Checks[0])
	}
}

func TestStartupSnapshotIsCallerOwnedAndDetailIsBounded(t *testing.T) {
	now := time.Unix(100, 0)
	report := NewStartup(now, 1, "dev", []StartupCheck{{Key: "config", Required: true}}, func() time.Time { return now })
	report.Complete("config", StartupFailed, string(make([]byte, 600)), "", "")
	first := report.Snapshot()
	first.Checks[0].Status = StartupPassed
	second := report.Snapshot()
	if second.Checks[0].Status != StartupFailed {
		t.Fatal("snapshot mutation changed owned state")
	}
	if len(second.Checks[0].Detail) > 512 {
		t.Fatalf("detail length = %d", len(second.Checks[0].Detail))
	}
}
