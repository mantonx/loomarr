package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/filler"
)

// testFillerRightsGrantAuthority runs unchanged against SQLite and Postgres.
func testFillerRightsGrantAuthority(t *testing.T, newStore NewStoreFunc) {
	t.Helper()
	s := newStore(t)
	defer func() { _ = s.Close() }()
	ctx := context.Background()
	at := time.Date(2026, time.September, 13, 12, 0, 0, 123, time.UTC)
	scope := filler.FillerRightsScope{
		SourceID: "archive:commercials", AcquisitionID: "acq-17",
		SourceMasterSHA256: strings.Repeat("1", 64), PolicySHA256: strings.Repeat("2", 64),
		Use: filler.FillerBroadcastUse,
	}
	if _, found, err := s.CurrentFillerRightsGrant(ctx, scope); err != nil || found {
		t.Fatalf("missing current grant: found=%t err=%v", found, err)
	}

	first := newConformanceFillerRightsGrant(t, scope, filler.FillerRightsUnknown, filler.FillerRightsWithdrawalUnknown, at, "")
	if err := s.PutFillerRightsGrant(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := s.PutFillerRightsGrant(ctx, first); err != nil {
		t.Fatalf("idempotent grant: %v", err)
	}
	got, found, err := s.CurrentFillerRightsGrant(ctx, scope)
	if err != nil || !found || got != first {
		t.Fatalf("first current grant=%+v found=%t err=%v", got, found, err)
	}

	second := newConformanceFillerRightsGrant(t, scope, filler.FillerRightsAuthorized, filler.FillerRightsWithdrawalClear, at.Add(time.Minute), first.SHA256)
	if err := s.PutFillerRightsGrant(ctx, second); err != nil {
		t.Fatal(err)
	}
	got, found, err = s.CurrentFillerRightsGrant(ctx, scope)
	if err != nil || !found || got != second {
		t.Fatalf("second current grant=%+v found=%t err=%v", got, found, err)
	}

	fork := newConformanceFillerRightsGrant(t, scope, filler.FillerRightsProhibited, filler.FillerRightsWithdrawalClear, at.Add(2*time.Minute), first.SHA256)
	if err := s.PutFillerRightsGrant(ctx, fork); !errors.Is(err, filler.ErrFillerRightsGrantConflict) {
		t.Fatalf("stale authority update error=%v", err)
	}
	got, found, err = s.CurrentFillerRightsGrant(ctx, scope)
	if err != nil || !found || got != second {
		t.Fatalf("fork changed current grant=%+v found=%t err=%v", got, found, err)
	}

	registry, err := filler.NewFillerRightsRegistry(s)
	if err != nil {
		t.Fatal(err)
	}
	request := filler.FillerRightsUseRequest{
		SubjectSHA256: strings.Repeat("3", 64), SourceID: scope.SourceID, AcquisitionID: scope.AcquisitionID,
		SourceMasterSHA256: scope.SourceMasterSHA256, PolicySHA256: scope.PolicySHA256,
		Use: scope.Use, RequestedAt: second.EffectiveAt,
	}
	decision, found, err := registry.CurrentFillerRights(ctx, request)
	if err != nil || !found || decision.Status != filler.FillerRightsAuthorized || decision.BasisSHA256 != second.SHA256 {
		t.Fatalf("registry decision=%+v found=%t err=%v", decision, found, err)
	}
}

func newConformanceFillerRightsGrant(
	t *testing.T,
	scope filler.FillerRightsScope,
	status filler.FillerRightsDecisionStatus,
	withdrawal filler.FillerRightsWithdrawalStatus,
	at time.Time,
	supersedes string,
) filler.FillerRightsGrant {
	t.Helper()
	grant, err := filler.NewFillerRightsGrant(
		scope, status, withdrawal, strings.Repeat("4", 64), "operator-1", at,
		nil, nil, supersedes, at.Add(time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	return grant
}
