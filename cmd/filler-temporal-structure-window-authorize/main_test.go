package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/loomarr/loomarr/internal/fillerreview"
	"github.com/loomarr/loomarr/internal/fillerstructure"
	"github.com/loomarr/loomarr/internal/fillerstructurewindow"
)

func TestRunPublishesExplicitWindowMaterializationAuthority(t *testing.T) {
	var stdout, stderr bytes.Buffer
	called := false
	args := []string{
		"--window-set", "manifest.json", "--window-certificate", "certificate.json",
		"--complete-decisions", "complete.json", "--window-decisions", "windows.json",
		"--short-long-shadow", "shadow.json", "--reviewer", "maintainer",
		"--reviewed-at", "2026-09-14T10:00:00Z", "--allow-materialization", "--out", "authority.json",
	}
	code := run(args, &stdout, &stderr, capabilities{publish: func(config fillerreview.TemporalStructureWindowAuthorityConfig) (fillerstructurewindow.MaterializationAuthority, string, error) {
		called = true
		if config.ReviewerID != "maintainer" || !config.AutomaticMaterializationAllowed || config.ShortLongShadowPath != "shadow.json" {
			t.Fatalf("config=%+v", config)
		}
		return fillerstructurewindow.MaterializationAuthority{
			Assessors: []fillerstructure.AssessorProfile{{}, {}}, AllowedUnits: []fillerstructure.Unit{fillerstructure.UnitProgrammeSpots},
			AllowedRoles:            []fillerstructure.Role{fillerstructure.RoleCommercial, fillerstructure.RoleProgrammeFragment},
			MinimumSourceDurationMS: 100, MaximumSourceDurationMS: 200, MaximumWindows: 2, MaximumWindowBytes: 300,
		}, strings.Repeat("a", 64), nil
	}})
	if code != 0 || !called || stderr.Len() != 0 || !strings.Contains(stdout.String(), "authorized held-child materialization") ||
		!strings.Contains(stdout.String(), "training=false admission=false") {
		t.Fatalf("code=%d called=%v stdout=%q stderr=%q", code, called, stdout.String(), stderr.String())
	}
}

func TestRunRequiresExplicitWindowMaterializationReview(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr, capabilities{}); code != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "--allow-materialization") {
		t.Fatalf("missing code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}
