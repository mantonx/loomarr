package inventory_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/inventory"
	"github.com/loomarr/loomarr/internal/testkit"
)

func validSnapshot(now time.Time) inventory.Snapshot {
	return inventory.Snapshot{
		Origin: inventory.OriginKey{Authority: "library-main", ExternalItemID: "item-7"}, Kind: "episode",
		Observation: inventory.Observation[inventory.ItemFacts]{
			SchemaVersion: 1, ObservedAt: now, Coverage: map[string]inventory.Coverage{
				"genres": inventory.CoverageEmpty,
			}, Facts: inventory.ItemFacts{Name: "Pilot"}, Extension: json.RawMessage(`{
			"FutureProviderFact":{"Nested":true},
			"ApiKey":"secret",
			"PlaybackSessionId":"session",
			"SafeURL":"https://example.test/art/7",
			"UnsafeURL":"https://example.test/video?api_key=secret"
		}`)},
		ExternalIDs: []inventory.ExternalID{{Namespace: "tmdb", Value: "99"}},
		Sources: []inventory.SourceSnapshot{{
			ExternalSourceID: "source-1", Kind: inventory.SourceLibraryOriginal, Revision: "rev-1",
			Locator: inventory.Locator{Authority: "library-main", ExternalItemID: "item-7", ExternalSourceID: "source-1"},
			Observation: inventory.Observation[inventory.SourceFacts]{SchemaVersion: 1, ObservedAt: now,
				Coverage: map[string]inventory.Coverage{"streams": inventory.CoveragePresent},
				Facts: inventory.SourceFacts{Container: "mkv", Streams: []inventory.Stream{
					{Index: 2, Kind: inventory.StreamAudio, Language: "eng"},
					{Index: 0, Kind: inventory.StreamVideo, Codec: "h264"},
				}}},
		}},
	}
}

func TestApplySnapshotPreservesBroadFactsAndStripsOperationalSecrets(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.FixedZone("test", 3600))
	service := inventory.New(testkit.MigratedSQLiteStore(t))
	itemID, err := service.ApplySnapshot(context.Background(), validSnapshot(now))
	if err != nil {
		t.Fatal(err)
	}
	item, ok, err := service.Item(context.Background(), inventory.ItemRef{ID: itemID})
	if err != nil || !ok {
		t.Fatalf("Item = (%+v, %v, %v), want hit", item, ok, err)
	}
	got := item.Origins[0]
	if got.Observation.ObservedAt.Location() != time.UTC ||
		got.Observation.Coverage["genres"] != inventory.CoverageEmpty {
		t.Fatalf("observation = %+v, want UTC with explicit empty genres", got.Observation)
	}
	var extension map[string]any
	if err := json.Unmarshal(got.Observation.Extension, &extension); err != nil {
		t.Fatal(err)
	}
	if extension["FutureProviderFact"] == nil || extension["SafeURL"] == nil {
		t.Fatalf("safe unknown facts were not retained: %s", got.Observation.Extension)
	}
	for _, key := range []string{"ApiKey", "PlaybackSessionId", "UnsafeURL"} {
		if _, exists := extension[key]; exists {
			t.Fatalf("secret-bearing %s survived sanitization: %s", key, got.Observation.Extension)
		}
	}
	streams := item.Sources[0].Origins[0].Observation.Facts.Streams
	if streams[0].Index != 0 || streams[1].Index != 2 {
		t.Fatalf("streams = %+v, want stable global-index order", streams)
	}
}

func TestValidateSnapshotRejectsCredentialsAndAmbiguousStreams(t *testing.T) {
	now := time.Now()
	for _, mutate := range []func(*inventory.Snapshot){
		func(snapshot *inventory.Snapshot) {
			snapshot.Sources[0].Locator.Path = "https://server/video?api_key=secret"
		},
		func(snapshot *inventory.Snapshot) { snapshot.Sources[0].Observation.Facts.Streams[1].Index = 2 },
		func(snapshot *inventory.Snapshot) { snapshot.Observation.Coverage["genres"] = "unknown" },
	} {
		snapshot := validSnapshot(now)
		mutate(&snapshot)
		if _, err := inventory.ValidateSnapshot(snapshot); !errors.Is(err, inventory.ErrInvalid) {
			t.Fatalf("ValidateSnapshot error = %v, want ErrInvalid", err)
		}
	}
}

func TestResolveSourceUsesFreshExactRevisionMeasurement(t *testing.T) {
	now := time.Now().UTC()
	service := inventory.New(testkit.MigratedSQLiteStore(t))
	snapshot := validSnapshot(now.Add(-time.Hour))
	snapshot.Sources[0].Revision = "rev-2"
	itemID, err := service.ApplySnapshot(context.Background(), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	item, ok, err := service.Item(context.Background(), inventory.ItemRef{ID: itemID})
	if err != nil || !ok {
		t.Fatalf("Item = (%+v, %v, %v), want hit", item, ok, err)
	}
	measurement := inventory.Measurement{SourceID: item.Sources[0].ID, Revision: "rev-2",
		Observation: inventory.Observation[inventory.SourceFacts]{SchemaVersion: 1, ObservedAt: now,
			Coverage: map[string]inventory.Coverage{"streams": inventory.CoveragePresent},
			Facts: inventory.SourceFacts{Streams: []inventory.Stream{
				{Index: 0, Kind: inventory.StreamAudio, Language: "jpn"},
			}}},
	}
	if err := service.RecordMeasurement(context.Background(), measurement); err != nil {
		t.Fatal(err)
	}
	resolved, ok, err := service.ResolveSource(context.Background(), inventory.SourceRequest{
		Item: inventory.ItemRef{ID: itemID}, Now: now, MaxAge: time.Minute, RequiredCoverage: []string{"streams"},
	})
	if err != nil || !ok {
		t.Fatalf("ResolveSource = (%+v, %v, %v), want hit", resolved, ok, err)
	}
	if got := resolved.Observation.Facts.Streams[0].Language; got != "jpn" {
		t.Fatalf("resolved language = %q, want measured jpn", got)
	}
	snapshot.Sources[0].Revision = "rev-3"
	if _, err := service.ApplySnapshot(context.Background(), snapshot); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := service.ResolveSource(context.Background(), inventory.SourceRequest{
		Item: inventory.ItemRef{ID: itemID}, Now: now, MaxAge: time.Minute, RequiredCoverage: []string{"streams"},
	}); err != nil || ok {
		t.Fatalf("stale imported observation plus old measurement = hit %v, err %v; want miss", ok, err)
	}
}
