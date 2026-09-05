package prepared

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPruneEvictsWholeColdPublicationsOldestFirst(t *testing.T) {
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	lib, err := newLibrary(t.TempDir(), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}

	oldest := publishSized(t, lib, "oldest", 600<<10)
	setPublicationTime(t, oldest, now)
	now = now.Add(time.Hour)
	protected := publishSized(t, lib, "protected", 600<<10)
	setPublicationTime(t, protected, now)
	now = now.Add(time.Hour)
	newest := publishSized(t, lib, "newest", 600<<10)
	setPublicationTime(t, newest, now)
	now = now.Add(time.Hour)
	if _, ok, err := lib.Lookup(baselineSpec("protected")); err != nil || !ok {
		t.Fatalf("touch protected publication = (_, %v, %v)", ok, err)
	}

	result, err := lib.Prune(context.Background(), 1<<20, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.PublicationsEvicted != 2 || result.BytesEvicted == 0 || result.RemainingBytes > result.BudgetBytes {
		t.Fatalf("Prune = %+v, want two whole publications evicted under budget", result)
	}
	assertPublicationMissing(t, oldest)
	assertPublicationPresent(t, protected)
	assertPublicationMissing(t, newest)
}

func TestPruneLeavesRecentPublicationsOverTheSoftCap(t *testing.T) {
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	lib, err := newLibrary(t.TempDir(), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	one := publishSized(t, lib, "one", 600<<10)
	two := publishSized(t, lib, "two", 600<<10)
	now = now.Add(31 * time.Minute)
	asset, ok, err := lib.Open(one.Key, "segment.m4s")
	if err != nil || !ok {
		t.Fatalf("Open = (_, %v, %v)", ok, err)
	}
	if err := asset.Content.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := lib.Lookup(baselineSpec("two")); err != nil || !ok {
		t.Fatalf("Lookup = (_, %v, %v)", ok, err)
	}

	result, err := lib.Prune(context.Background(), 1<<20, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.PublicationsEvicted != 0 || result.RemainingBytes <= result.BudgetBytes || result.ProtectedBytes == 0 {
		t.Fatalf("Prune = %+v, want protected overage", result)
	}
	assertPublicationPresent(t, one)
	assertPublicationPresent(t, two)
}

func TestPruneProtectsTheWholeStoreDuringStartupGrace(t *testing.T) {
	root := t.TempDir()
	seedNow := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	seed, err := newLibrary(root, func() time.Time { return seedNow })
	if err != nil {
		t.Fatal(err)
	}
	publication := publishSized(t, seed, "old", 600<<10)
	if err := os.Chtimes(publication.Directory, seedNow, seedNow); err != nil {
		t.Fatal(err)
	}

	now := seedNow.Add(14 * 24 * time.Hour)
	reopened, err := newLibrary(root, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	result, err := reopened.Prune(context.Background(), 1, nil)
	if err != nil || result.PublicationsEvicted != 0 {
		t.Fatalf("startup Prune = (%+v, %v), want protected", result, err)
	}
	assertPublicationPresent(t, publication)

	now = now.Add(preparedStartupGrace + time.Second)
	result, err = reopened.Prune(context.Background(), 1, nil)
	if err != nil || result.PublicationsEvicted != 1 {
		t.Fatalf("post-grace Prune = (%+v, %v), want eviction", result, err)
	}
	assertPublicationMissing(t, publication)
}

func TestPruneRemovesOnlyAbandonedOwnedStagingDirectories(t *testing.T) {
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	root := t.TempDir()
	lib, err := newLibrary(root, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(root, ".staging-"+validTestKey()+"-stale")
	fresh := filepath.Join(root, ".staging-"+validTestKey()+"-fresh")
	unknown := filepath.Join(root, "operator-data")
	for _, dir := range []string{stale, fresh, unknown} {
		if err := os.Mkdir(dir, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chtimes(stale, now.Add(-preparedStagingGrace-time.Second), now.Add(-preparedStagingGrace-time.Second)); err != nil {
		t.Fatal(err)
	}

	result, err := lib.Prune(context.Background(), 1<<20, nil)
	if err == nil || !errors.Is(err, ErrUnknownEntry) {
		t.Fatalf("Prune error = %v, want ErrUnknownEntry", err)
	}
	if result.StagingRemoved != 1 {
		t.Fatalf("Prune = %+v, want one abandoned staging directory removed", result)
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("stale staging still exists: %v", err)
	}
	for _, dir := range []string{fresh, unknown} {
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("Prune removed %s: %v", dir, err)
		}
	}
}

func TestPruneProtectsTheAcceptedScheduleWithoutTreatingAProbeAsPlayback(t *testing.T) {
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	lib, err := newLibrary(t.TempDir(), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	publication := publishSized(t, lib, "scheduled", 600<<10)
	if err := os.Chtimes(publication.Directory, now, now); err != nil {
		t.Fatal(err)
	}
	specification := baselineSpec("scheduled")
	now = now.Add(preparedStartupGrace + preparedUseGrace + time.Hour)
	if _, ok, err := lib.Peek(specification); err != nil || !ok {
		t.Fatalf("Peek = (_, %v, %v), want hit", ok, err)
	}

	result, err := lib.Prune(context.Background(), 1, []Specification{specification})
	if err != nil || result.PublicationsEvicted != 0 || result.ProtectedBytes == 0 {
		t.Fatalf("scheduled Prune = (%+v, %v), want protected", result, err)
	}
	assertPublicationPresent(t, publication)

	result, err = lib.Prune(context.Background(), 1, nil)
	if err != nil || result.PublicationsEvicted != 1 {
		t.Fatalf("unscheduled Prune = (%+v, %v), want eviction", result, err)
	}
	assertPublicationMissing(t, publication)
}

func TestPruneKeepsFiftyAndOneHundredChannelHotSetsWithinDefaultBudget(t *testing.T) {
	const (
		defaultBudget   = int64(512) << 30
		publicationSize = int64(2) << 30
	)
	for _, channelCount := range []int{50, 100} {
		channelCount := channelCount
		t.Run(fmt.Sprintf("%d_channels", channelCount), func(t *testing.T) {
			seedNow := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
			now := seedNow
			lib, err := newLibrary(t.TempDir(), func() time.Time { return now })
			if err != nil {
				t.Fatal(err)
			}
			protected := make([]Specification, 0, channelCount*2)
			hotPublications := make([]Publication, 0, channelCount*2)
			laterPublications := make([]Publication, 0, channelCount)
			for i := range channelCount {
				for _, class := range []string{"current", "next"} {
					source := fmt.Sprintf("%s-%03d", class, i)
					protected = append(protected, baselineSpec(source))
					hotPublications = append(hotPublications, publishSparseSized(t, lib, source, publicationSize))
				}
				laterPublications = append(laterPublications,
					publishSparseSized(t, lib, fmt.Sprintf("later-%03d", i), publicationSize))
			}
			now = seedNow.Add(preparedStartupGrace + preparedUseGrace + time.Hour)

			result, err := lib.Prune(t.Context(), defaultBudget, protected)
			if err != nil {
				t.Fatal(err)
			}
			if result.RemainingBytes > defaultBudget {
				t.Fatalf("retained bytes = %d, exceed default budget %d", result.RemainingBytes, defaultBudget)
			}
			if result.ProtectedBytes < int64(channelCount*2)*publicationSize {
				t.Fatalf("protected bytes = %d, lost current/next hot set", result.ProtectedBytes)
			}
			for _, publication := range hotPublications {
				assertPublicationPresent(t, publication)
			}
			if channelCount == 50 && result.PublicationsEvicted != 0 {
				t.Fatalf("50-channel default-budget evictions = %d, want none", result.PublicationsEvicted)
			}
			if channelCount == 100 && result.PublicationsEvicted == 0 {
				t.Fatal("100-channel over-budget optional lookahead was not evicted")
			}
			laterRemaining := 0
			for _, publication := range laterPublications {
				if _, err := os.Stat(publication.Directory); err == nil {
					laterRemaining++
				} else if !errors.Is(err, os.ErrNotExist) {
					t.Fatal(err)
				}
			}
			if laterRemaining != channelCount-result.PublicationsEvicted {
				t.Fatalf("later publications remaining = %d, want %d", laterRemaining, channelCount-result.PublicationsEvicted)
			}
		})
	}
}

func TestPruneOwnsReadinessControlFiles(t *testing.T) {
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	lib, err := newLibrary(t.TempDir(), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	readiness, err := OpenReadiness(lib)
	if err != nil {
		t.Fatal(err)
	}
	request := Request{Source: testSource("retained"), Rendition: baselineRendition()}
	if err := readiness.RememberBinding(
		BindingKey{ChannelID: "ch", LibraryItemID: "item"}, Binding{Policy: "policy", Request: request},
	); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(lib.root, ".readiness-abandoned")
	if err := os.WriteFile(stale, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := now.Add(-preparedStagingGrace - time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}

	result, err := lib.Prune(context.Background(), 1, nil)
	if err != nil {
		t.Fatalf("Prune with readiness control files: %v", err)
	}
	if result.StagingRemoved != 1 {
		t.Fatalf("Prune = %+v, want abandoned readiness workspace removed", result)
	}
	if _, err := os.Stat(filepath.Join(lib.root, readinessMetadata)); err != nil {
		t.Fatalf("readiness index was removed: %v", err)
	}
}

func publishSized(t *testing.T, lib *Library, source string, size int) Publication {
	t.Helper()
	pub, err := lib.Publish(context.Background(), baselineSpec(source), func(_ context.Context, workspace string) (Output, error) {
		return Output{Files: []string{"segment.m4s"}}, os.WriteFile(
			filepath.Join(workspace, "segment.m4s"), make([]byte, size), 0o600,
		)
	})
	if err != nil {
		t.Fatal(err)
	}
	// Retention deliberately falls back to the publication directory's mtime after a restart.
	// Keep that durable timestamp on the same injected clock as the in-memory LRU; using the
	// host clock makes a fixed-date test start failing once wall time passes its fake `now`.
	if err := os.Chtimes(pub.Directory, lib.now(), lib.now()); err != nil {
		t.Fatal(err)
	}
	return pub
}

func publishSparseSized(t *testing.T, lib *Library, source string, size int64) Publication {
	t.Helper()
	pub, err := lib.Publish(context.Background(), baselineSpec(source), func(_ context.Context, workspace string) (Output, error) {
		path := filepath.Join(workspace, "segment.m4s")
		file, createErr := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if createErr != nil {
			return Output{}, createErr
		}
		truncateErr := file.Truncate(size)
		closeErr := file.Close()
		return Output{Files: []string{"segment.m4s"}}, errors.Join(truncateErr, closeErr)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(pub.Directory, lib.now(), lib.now()); err != nil {
		t.Fatal(err)
	}
	return pub
}

func setPublicationTime(t *testing.T, publication Publication, at time.Time) {
	t.Helper()
	if err := os.Chtimes(publication.Directory, at, at); err != nil {
		t.Fatal(err)
	}
}

func baselineSpec(source string) Specification {
	return Specification{SourceFingerprint: source, Rendition: RenditionContract{
		VideoCodec: "h264", AudioCodec: "aac", Width: 1920, Height: 1080,
		FrameRate: 25, VideoBitrateKbps: 5000, AudioBitrateKbps: 160,
		SegmentDurationMS: 2000, PackagingVersion: 1,
	}}
}

func assertPublicationPresent(t *testing.T, publication Publication) {
	t.Helper()
	if _, err := os.Stat(publication.Directory); err != nil {
		t.Fatalf("publication %s is missing: %v", publication.Key, err)
	}
}

func assertPublicationMissing(t *testing.T, publication Publication) {
	t.Helper()
	if _, err := os.Stat(publication.Directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("publication %s still exists: %v", publication.Key, err)
	}
}

func validTestKey() string { return "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" }
