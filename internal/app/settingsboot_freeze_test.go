package app

import (
	"testing"

	"github.com/loomarr/loomarr/internal/settings"
)

func TestResolvedFreezeKeepsAppliedValuesAcrossLiveWrites(t *testing.T) {
	set := visionSet(t, map[string]string{
		"filler.dir":                              "/clips/old",
		"filler.watch_dir":                        "/watch/old",
		"filler.structure_window_authority_path":  "/authority/old.json",
		"filler.structure_window_deployment_path": "/deployment/old.json",
		"filler.sync_every":                       "5m",
		"filler.weight":                           "3",
		"filler.source.folder.enabled":            "true",
	})

	frozen, applied := set.freeze(settings.NewRegistry().RestartKeys()...)
	if applied["filler.dir"] != "/clips/old" || applied["filler.watch_dir"] != "/watch/old" {
		t.Fatalf("applied = %v, want canonical generation values", applied)
	}
	if applied["diagnostics.dir"] != "/data/diagnostics" || applied["filler.structure_window_authority_path"] != "/authority/old.json" ||
		applied["filler.structure_window_deployment_path"] != "/deployment/old.json" || len(applied) != 5 {
		t.Fatalf("applied = %v, want all restart-scoped storage keys", applied)
	}

	set.svc.SetDB(map[string]string{
		"filler.dir":                              "/clips/new",
		"filler.watch_dir":                        "/watch/new",
		"filler.structure_window_authority_path":  "/authority/new.json",
		"filler.structure_window_deployment_path": "/deployment/new.json",
		"filler.sync_every":                       "10m",
		"filler.weight":                           "7",
		"filler.source.folder.enabled":            "false",
	})

	if got := frozen.str("filler.dir"); got != "/clips/old" {
		t.Errorf("frozen string = %q, want old value", got)
	}
	if got := frozen.str("filler.watch_dir"); got != "/watch/old" {
		t.Errorf("frozen watch = %q, want old value", got)
	}
	if got := frozen.str("filler.structure_window_authority_path"); got != "/authority/old.json" {
		t.Errorf("frozen authority = %q, want old value", got)
	}
	if got := frozen.str("filler.structure_window_deployment_path"); got != "/deployment/old.json" {
		t.Errorf("frozen deployment = %q, want old value", got)
	}
	if got := frozen.dur("filler.sync_every").String(); got != "10m0s" {
		t.Errorf("live duration = %q, want 10m0s", got)
	}
	if got := frozen.intv("filler.weight"); got != 7 {
		t.Errorf("live int = %d, want 7", got)
	}
	if got := frozen.boolv("filler.source.folder.enabled"); got {
		t.Error("live bool did not move to the newly persisted false value")
	}
	if got := frozen.boolOn("filler.source.folder.enabled"); got {
		t.Error("live safe-on bool did not move to the newly persisted false value")
	}

	if got := set.str("filler.dir"); got != "/clips/new" {
		t.Errorf("live facade = %q, want newly persisted value", got)
	}
}
