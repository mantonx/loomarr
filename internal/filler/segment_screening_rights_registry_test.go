package filler

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type memoryFillerRightsGrantRepository struct {
	grants map[FillerRightsScope]FillerRightsGrant
	err    error
}

func (r *memoryFillerRightsGrantRepository) PutFillerRightsGrant(_ context.Context, grant FillerRightsGrant) error {
	if r.err != nil {
		return r.err
	}
	if r.grants == nil {
		r.grants = make(map[FillerRightsScope]FillerRightsGrant)
	}
	current, found := r.grants[grant.Scope]
	if found && current.SHA256 == grant.SHA256 {
		return nil
	}
	if found && grant.SupersedesSHA256 != current.SHA256 || !found && grant.SupersedesSHA256 != "" {
		return ErrFillerRightsGrantConflict
	}
	r.grants[grant.Scope] = grant
	return nil
}

func (r *memoryFillerRightsGrantRepository) CurrentFillerRightsGrant(_ context.Context, scope FillerRightsScope) (FillerRightsGrant, bool, error) {
	if r.err != nil {
		return FillerRightsGrant{}, false, r.err
	}
	grant, found := r.grants[scope]
	return grant, found, nil
}

func TestFillerRightsRegistryDerivesSubjectSpecificCurrentDecision(t *testing.T) {
	at := time.Date(2026, time.September, 13, 12, 0, 0, 0, time.UTC)
	request := fillerRightsRegistryRequest(at)
	validUntil := at.Add(time.Hour)
	grant := fillerRightsGrantFixture(t, fillerRightsScopeForRequest(request), FillerRightsAuthorized, FillerRightsWithdrawalClear, at.Add(-time.Hour), &validUntil, nil, "")
	repository := &memoryFillerRightsGrantRepository{}
	registry, err := NewFillerRightsRegistry(repository)
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Record(t.Context(), grant); err != nil {
		t.Fatal(err)
	}
	decision, found, err := registry.CurrentFillerRights(t.Context(), request)
	if err != nil || !found {
		t.Fatalf("decision not found: found=%t err=%v", found, err)
	}
	if decision.SubjectSHA256 != request.SubjectSHA256 || decision.Status != FillerRightsAuthorized ||
		decision.Withdrawal != FillerRightsWithdrawalClear || decision.BasisSHA256 != grant.SHA256 ||
		decision.ValidUntil == nil || !decision.ValidUntil.Equal(validUntil) || ValidateFillerRightsUseDecision(decision) != nil {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestFillerRightsRegistryTurnsInactiveGrantIntoAttributableUnknown(t *testing.T) {
	at := time.Date(2026, time.September, 13, 12, 0, 0, 0, time.UTC)
	request := fillerRightsRegistryRequest(at)
	tests := []struct {
		name        string
		effectiveAt time.Time
		validUntil  *time.Time
	}{
		{name: "future", effectiveAt: at.Add(time.Minute)},
		{name: "expired", effectiveAt: at.Add(-time.Hour), validUntil: timePointer(at)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			grant := fillerRightsGrantFixture(t, fillerRightsScopeForRequest(request), FillerRightsAuthorized, FillerRightsWithdrawalClear, test.effectiveAt, test.validUntil, nil, "")
			repository := &memoryFillerRightsGrantRepository{grants: map[FillerRightsScope]FillerRightsGrant{grant.Scope: grant}}
			registry, err := NewFillerRightsRegistry(repository)
			if err != nil {
				t.Fatal(err)
			}
			decision, found, err := registry.CurrentFillerRights(t.Context(), request)
			if err != nil || !found || decision.Status != FillerRightsUnknown || decision.BasisSHA256 != grant.SHA256 || decision.ValidUntil != nil {
				t.Fatalf("decision=%+v found=%t err=%v", decision, found, err)
			}
		})
	}
}

func TestFillerRightsRegistryPreservesWithdrawalAndRejectsScopeDrift(t *testing.T) {
	at := time.Date(2026, time.September, 13, 12, 0, 0, 0, time.UTC)
	request := fillerRightsRegistryRequest(at)
	withdrawnAt := at.Add(-time.Hour)
	grant := fillerRightsGrantFixture(t, fillerRightsScopeForRequest(request), FillerRightsProhibited, FillerRightsWithdrawalActive, at.Add(-time.Minute), nil, &withdrawnAt, "")
	repository := &memoryFillerRightsGrantRepository{grants: map[FillerRightsScope]FillerRightsGrant{grant.Scope: grant}}
	registry, err := NewFillerRightsRegistry(repository)
	if err != nil {
		t.Fatal(err)
	}
	decision, found, err := registry.CurrentFillerRights(t.Context(), request)
	if err != nil || !found || decision.Status != FillerRightsProhibited || decision.Withdrawal != FillerRightsWithdrawalActive ||
		decision.WithdrawnAt == nil || !decision.WithdrawnAt.Equal(withdrawnAt) {
		t.Fatalf("decision=%+v found=%t err=%v", decision, found, err)
	}

	drifted := grant
	drifted.Scope.SourceID = "different-source"
	drifted.SHA256 = FillerRightsGrantSHA256(drifted)
	repository.grants[grant.Scope] = drifted
	if _, _, err := registry.CurrentFillerRights(t.Context(), request); err == nil {
		t.Fatal("scope-drifted current grant was accepted")
	}
}

func TestFillerRightsRegistryRecordsOneLinearImmutableHistory(t *testing.T) {
	at := time.Date(2026, time.September, 13, 12, 0, 0, 0, time.UTC)
	scope := fillerRightsScopeForRequest(fillerRightsRegistryRequest(at))
	repository := &memoryFillerRightsGrantRepository{}
	registry, err := NewFillerRightsRegistry(repository)
	if err != nil {
		t.Fatal(err)
	}
	first := fillerRightsGrantFixture(t, scope, FillerRightsUnknown, FillerRightsWithdrawalUnknown, at, nil, nil, "")
	if err := registry.Record(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	second := fillerRightsGrantFixture(t, scope, FillerRightsAuthorized, FillerRightsWithdrawalClear, at.Add(time.Minute), nil, nil, first.SHA256)
	if err := registry.Record(t.Context(), second); err != nil {
		t.Fatal(err)
	}
	fork := fillerRightsGrantFixture(t, scope, FillerRightsProhibited, FillerRightsWithdrawalClear, at.Add(2*time.Minute), nil, nil, first.SHA256)
	if err := registry.Record(t.Context(), fork); !errors.Is(err, ErrFillerRightsGrantConflict) {
		t.Fatalf("fork error=%v", err)
	}
	if repository.grants[scope].SHA256 != second.SHA256 {
		t.Fatal("conflicting grant replaced the current authority")
	}
}

func TestValidateFillerRightsGrantRejectsInvalidStatusTimeAndIdentity(t *testing.T) {
	at := time.Date(2026, time.September, 13, 12, 0, 0, 0, time.UTC)
	valid := fillerRightsGrantFixture(t, fillerRightsScopeForRequest(fillerRightsRegistryRequest(at)), FillerRightsAuthorized, FillerRightsWithdrawalClear, at, nil, nil, "")
	tests := []struct {
		name   string
		mutate func(*FillerRightsGrant)
	}{
		{name: "status", mutate: func(grant *FillerRightsGrant) { grant.Status = "maybe" }},
		{name: "authorized withdrawal", mutate: func(grant *FillerRightsGrant) { grant.Withdrawal = FillerRightsWithdrawalUnknown }},
		{name: "expiry", mutate: func(grant *FillerRightsGrant) { grant.ValidUntil = timePointer(grant.EffectiveAt) }},
		{name: "actor", mutate: func(grant *FillerRightsGrant) { grant.ActorID = "" }},
		{name: "control character", mutate: func(grant *FillerRightsGrant) { grant.ActorID = "operator\x00one" }},
		{name: "supersedes self", mutate: func(grant *FillerRightsGrant) { grant.SupersedesSHA256 = grant.SHA256 }},
		{name: "evidence identity", mutate: func(grant *FillerRightsGrant) { grant.EvidenceSHA256 = "" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			grant := valid
			test.mutate(&grant)
			if test.name != "supersedes self" {
				grant.SHA256 = FillerRightsGrantSHA256(grant)
			}
			if err := ValidateFillerRightsGrant(grant); err == nil {
				t.Fatal("invalid grant was accepted")
			}
		})
	}
}

func fillerRightsRegistryRequest(at time.Time) FillerRightsUseRequest {
	return FillerRightsUseRequest{
		SubjectSHA256: strings.Repeat("1", 64), SourceID: "archive:commercials", AcquisitionID: "acq-17",
		SourceMasterSHA256: strings.Repeat("2", 64), PolicySHA256: strings.Repeat("3", 64),
		Use: FillerBroadcastUse, RequestedAt: at,
	}
}

func fillerRightsGrantFixture(
	t *testing.T,
	scope FillerRightsScope,
	status FillerRightsDecisionStatus,
	withdrawal FillerRightsWithdrawalStatus,
	effectiveAt time.Time,
	validUntil, withdrawnAt *time.Time,
	supersedes string,
) FillerRightsGrant {
	t.Helper()
	grant, err := NewFillerRightsGrant(
		scope, status, withdrawal, strings.Repeat("4", 64), "operator-1", effectiveAt,
		validUntil, withdrawnAt, supersedes, effectiveAt.Add(time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	return grant
}

func timePointer(value time.Time) *time.Time {
	return &value
}
