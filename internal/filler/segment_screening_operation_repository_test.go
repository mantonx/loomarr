package filler

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFileSegmentScreeningEvidenceRepositoryReplaysOneSettledAxisOperation(t *testing.T) {
	repository, err := NewFileSegmentScreeningEvidenceRepository(filepath.Join(t.TempDir(), "screening-evidence"))
	if err != nil {
		t.Fatal(err)
	}
	subject := screeningChildSubjectFixture(t)
	recorded := passingAxisEvidence(t, subject)[0]
	if err := repository.PutSegmentScreeningAxisEvidence(t.Context(), recorded); err != nil {
		t.Fatal(err)
	}
	replayed, found, err := repository.FindSegmentScreeningAxisEvidence(t.Context(), subject.SHA256, recorded.Evidence.Profile)
	if err != nil || !found || replayed.Evidence.SHA256 != recorded.Evidence.SHA256 {
		t.Fatalf("found=%v replayed=%+v error=%v", found, replayed, err)
	}
	operationSHA256 := segmentScreeningOperationSHA256(subject.SHA256, recorded.Evidence.Profile)
	info, err := os.Lstat(repository.operationPath(operationSHA256))
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Fatalf("operation pointer info=%v error=%v", info, err)
	}

	raw := []byte("different-observation")
	conflicting, err := NewSegmentScreeningAxisEvidence(
		subject, recorded.Evidence.Profile, ScreenHold, "manual_review_required",
		screeningSuitabilityForOutcome(subject, recorded.Evidence.Profile, ScreenHold, raw), raw,
		time.Date(2026, time.September, 13, 2, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.PutSegmentScreeningAxisEvidence(t.Context(), conflicting); err == nil {
		t.Fatal("one settled subject/profile operation acquired a second result")
	}
	replayed, found, err = repository.FindSegmentScreeningAxisEvidence(t.Context(), subject.SHA256, recorded.Evidence.Profile)
	if err != nil || !found || replayed.Evidence.SHA256 != recorded.Evidence.SHA256 {
		t.Fatalf("conflict displaced settled result: found=%v replayed=%+v error=%v", found, replayed, err)
	}
}

func TestFileSegmentScreeningEvidenceRepositoryRejectsOperationPointerTampering(t *testing.T) {
	repository, err := NewFileSegmentScreeningEvidenceRepository(filepath.Join(t.TempDir(), "screening-evidence"))
	if err != nil {
		t.Fatal(err)
	}
	subject := screeningChildSubjectFixture(t)
	recorded := passingAxisEvidence(t, subject)[0]
	if err := repository.PutSegmentScreeningAxisEvidence(t.Context(), recorded); err != nil {
		t.Fatal(err)
	}
	operationSHA256 := segmentScreeningOperationSHA256(subject.SHA256, recorded.Evidence.Profile)
	if err := os.WriteFile(repository.operationPath(operationSHA256), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.FindSegmentScreeningAxisEvidence(t.Context(), subject.SHA256, recorded.Evidence.Profile); err == nil {
		t.Fatal("tampered operation pointer was accepted")
	}
}

func TestFileSegmentScreeningEvidenceRepositoryReportsUnsettledAxisOperation(t *testing.T) {
	repository, err := NewFileSegmentScreeningEvidenceRepository(filepath.Join(t.TempDir(), "screening-evidence"))
	if err != nil {
		t.Fatal(err)
	}
	subject := screeningChildSubjectFixture(t)
	profile := screeningProfileFixture(ScreenPlayback, "7")
	if _, found, err := repository.FindSegmentScreeningAxisEvidence(t.Context(), subject.SHA256, profile); err != nil || found {
		t.Fatalf("found=%v error=%v", found, err)
	}
}
