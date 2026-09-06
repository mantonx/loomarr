package filler

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type currentFillerRightsAuthorityFunc func(context.Context, FillerRightsUseRequest) (FillerRightsUseDecision, bool, error)

func (f currentFillerRightsAuthorityFunc) CurrentFillerRights(ctx context.Context, request FillerRightsUseRequest) (FillerRightsUseDecision, bool, error) {
	return f(ctx, request)
}

func TestFillerRightsEvaluatorPassesExactCurrentAuthorityAndReplays(t *testing.T) {
	media := playbackIntegrityMediaFixture(t, validPlaybackIntegrityQuality())
	repository, err := NewFileSegmentScreeningEvidenceRepository(filepath.Join(t.TempDir(), "screening-evidence"))
	if err != nil {
		t.Fatal(err)
	}
	clockCalls, authorityCalls := 0, 0
	at := time.Date(2026, time.September, 14, 2, 0, 0, 0, time.UTC)
	authority := currentFillerRightsAuthorityFunc(func(_ context.Context, request FillerRightsUseRequest) (FillerRightsUseDecision, bool, error) {
		authorityCalls++
		decision, err := NewFillerRightsUseDecision(request, FillerRightsAuthorized, FillerRightsWithdrawalClear, screeningDigest("7"), nil, nil)
		return decision, true, err
	})
	evaluator := mustFillerRightsEvaluator(t, repository, authority, func() time.Time {
		clockCalls++
		return at
	})
	first, err := evaluator.Evaluate(t.Context(), media)
	if err != nil || first.Evidence.Outcome != ScreenPass || first.Evidence.ReasonCode != "rights_current" {
		t.Fatalf("first=%+v error=%v", first, err)
	}
	if bytes.Contains(first.RawEvidence, []byte(filepath.Dir(media.PlaybackPath))) {
		t.Fatal("private artifact path leaked into rights evidence")
	}
	if err := repository.PutSegmentScreeningAxisEvidence(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	second, err := evaluator.Evaluate(t.Context(), media)
	if err != nil || second.Evidence.SHA256 != first.Evidence.SHA256 || clockCalls != 1 || authorityCalls != 1 {
		t.Fatalf("second=%+v clockCalls=%d authorityCalls=%d error=%v", second, clockCalls, authorityCalls, err)
	}
}

func TestFillerRightsEvaluatorMapsClosedCurrentDecisions(t *testing.T) {
	tests := []struct {
		name        string
		decision    func(FillerRightsUseRequest) (FillerRightsUseDecision, bool, error)
		wantOutcome SegmentScreeningOutcome
		wantReason  string
	}{
		{
			name: "no decision", wantOutcome: ScreenHold, wantReason: "rights_unknown",
			decision: func(FillerRightsUseRequest) (FillerRightsUseDecision, bool, error) {
				return FillerRightsUseDecision{}, false, nil
			},
		},
		{
			name: "explicitly unknown", wantOutcome: ScreenHold, wantReason: "rights_unknown",
			decision: rightsDecisionFixture(FillerRightsUnknown, FillerRightsWithdrawalUnknown, nil),
		},
		{
			name: "prohibited", wantOutcome: ScreenReject, wantReason: "rights_prohibited",
			decision: rightsDecisionFixture(FillerRightsProhibited, FillerRightsWithdrawalClear, nil),
		},
		{
			name: "withdrawn", wantOutcome: ScreenReject, wantReason: "rights_withdrawn",
			decision: rightsDecisionFixture(FillerRightsProhibited, FillerRightsWithdrawalActive, func(request FillerRightsUseRequest) *time.Time {
				withdrawn := request.RequestedAt.Add(-time.Minute)
				return &withdrawn
			}),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			media := playbackIntegrityMediaFixture(t, validPlaybackIntegrityQuality())
			repository, err := NewFileSegmentScreeningEvidenceRepository(filepath.Join(t.TempDir(), "screening-evidence"))
			if err != nil {
				t.Fatal(err)
			}
			evaluator := mustFillerRightsEvaluator(t, repository, currentFillerRightsAuthorityFunc(func(_ context.Context, request FillerRightsUseRequest) (FillerRightsUseDecision, bool, error) {
				return test.decision(request)
			}), time.Now)
			recorded, err := evaluator.Evaluate(t.Context(), media)
			if err != nil || recorded.Evidence.Outcome != test.wantOutcome || recorded.Evidence.ReasonCode != test.wantReason {
				t.Fatalf("recorded=%+v error=%v", recorded, err)
			}
		})
	}
}

func TestFillerRightsEvaluatorHoldsMissingAcquisitionIdentityWithoutConsultingAuthority(t *testing.T) {
	media := playbackIntegrityMediaFixture(t, validPlaybackIntegrityQuality())
	media.Subject.AcquisitionID = ""
	media.Subject.SHA256 = SegmentScreeningSubjectSHA256(media.Subject)
	calls := 0
	authority := currentFillerRightsAuthorityFunc(func(context.Context, FillerRightsUseRequest) (FillerRightsUseDecision, bool, error) {
		calls++
		return FillerRightsUseDecision{}, false, nil
	})
	repository, err := NewFileSegmentScreeningEvidenceRepository(filepath.Join(t.TempDir(), "screening-evidence"))
	if err != nil {
		t.Fatal(err)
	}
	evaluator := mustFillerRightsEvaluator(t, repository, authority, time.Now)
	recorded, err := evaluator.Evaluate(t.Context(), media)
	if err != nil || recorded.Evidence.Outcome != ScreenHold || recorded.Evidence.ReasonCode != "rights_identity_missing" || calls != 0 {
		t.Fatalf("recorded=%+v calls=%d error=%v", recorded, calls, err)
	}
}

func TestFillerRightsEvaluatorRejectsUntrustworthyAuthority(t *testing.T) {
	tests := []struct {
		name      string
		authority currentFillerRightsAuthorityFunc
	}{
		{
			name: "authority failure",
			authority: func(context.Context, FillerRightsUseRequest) (FillerRightsUseDecision, bool, error) {
				return FillerRightsUseDecision{}, false, errors.New("registry unavailable")
			},
		},
		{
			name: "different acquisition",
			authority: func(_ context.Context, request FillerRightsUseRequest) (FillerRightsUseDecision, bool, error) {
				decision, err := NewFillerRightsUseDecision(request, FillerRightsAuthorized, FillerRightsWithdrawalClear, screeningDigest("7"), nil, nil)
				decision.AcquisitionID = "another-acquisition"
				decision.SHA256 = FillerRightsUseDecisionSHA256(decision)
				return decision, true, err
			},
		},
		{
			name: "expired decision",
			authority: func(_ context.Context, request FillerRightsUseRequest) (FillerRightsUseDecision, bool, error) {
				future := request.RequestedAt.Add(time.Minute)
				decision, err := NewFillerRightsUseDecision(request, FillerRightsAuthorized, FillerRightsWithdrawalClear, screeningDigest("7"), &future, nil)
				past := request.RequestedAt.Add(-time.Minute)
				decision.ValidUntil = &past
				decision.SHA256 = FillerRightsUseDecisionSHA256(decision)
				return decision, true, err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			media := playbackIntegrityMediaFixture(t, validPlaybackIntegrityQuality())
			repository, err := NewFileSegmentScreeningEvidenceRepository(filepath.Join(t.TempDir(), "screening-evidence"))
			if err != nil {
				t.Fatal(err)
			}
			evaluator := mustFillerRightsEvaluator(t, repository, test.authority, time.Now)
			if _, err := evaluator.Evaluate(t.Context(), media); err == nil {
				t.Fatal("untrustworthy rights authority produced a semantic result")
			}
		})
	}
}

func mustFillerRightsEvaluator(t *testing.T, repository SegmentScreeningAxisEvidenceReplay, authority CurrentFillerRightsAuthority, now func() time.Time) *FillerRightsEvaluator {
	t.Helper()
	profile := screeningProfileFixture(ScreenRights, "3")
	profile.EvidenceContract = fillerRightsEvidenceContractVersion
	evaluator, err := NewFillerRightsEvaluator(profile, repository, authority, now)
	if err != nil {
		t.Fatal(err)
	}
	return evaluator
}

func rightsDecisionFixture(status FillerRightsDecisionStatus, withdrawal FillerRightsWithdrawalStatus, withdrawnAt func(FillerRightsUseRequest) *time.Time) func(FillerRightsUseRequest) (FillerRightsUseDecision, bool, error) {
	return func(request FillerRightsUseRequest) (FillerRightsUseDecision, bool, error) {
		var at *time.Time
		if withdrawnAt != nil {
			at = withdrawnAt(request)
		}
		decision, err := NewFillerRightsUseDecision(request, status, withdrawal, screeningDigest("7"), nil, at)
		return decision, true, err
	}
}

func screeningDigest(value string) string { return strings.Repeat(value, 64) }
