package mediatools_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/loomarr/loomarr/internal/mediatools"
)

func TestSnapshotRegularFileBindsPrivateImmutableBytes(t *testing.T) {
	t.Parallel()
	contents := []byte("source bytes")
	source := filepath.Join(t.TempDir(), "source.mp4")
	if err := os.WriteFile(source, contents, 0o600); err != nil {
		t.Fatal(err)
	}

	snapshot, err := mediatools.SnapshotRegularFile(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	snapshotPath := snapshot.Path()
	if snapshotPath == source || filepath.Ext(snapshotPath) != filepath.Ext(source) {
		t.Fatalf("unexpected snapshot path %q", snapshotPath)
	}
	sum := sha256.Sum256(contents)
	if snapshot.SHA256() != fmt.Sprintf("%x", sum) || snapshot.Bytes() != int64(len(contents)) {
		t.Fatalf("snapshot identity=%s/%d", snapshot.SHA256(), snapshot.Bytes())
	}
	if err := os.WriteFile(source, []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(snapshotPath)
	if err != nil || string(got) != string(contents) {
		t.Fatalf("snapshot changed with source: %q err=%v", got, err)
	}
	if err := snapshot.Close(); err != nil {
		t.Fatal(err)
	}
	if err := snapshot.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(snapshotPath); !os.IsNotExist(err) {
		t.Fatalf("snapshot survived close: %v", err)
	}
}

func TestSnapshotRegularFileObservesPreexistingCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := mediatools.SnapshotRegularFile(ctx, filepath.Join(t.TempDir(), "source.mp4")); err == nil {
		t.Fatal("expected cancellation")
	}
}
