package library

import (
	"context"
	"encoding/json"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/loomarr/loomarr/internal/inventory"
	"github.com/loomarr/loomarr/internal/testkit"
)

const richInventoryItem = `{
  "Id":"episode-7","Type":"Episode","Name":"Pilot","OriginalTitle":"Original Pilot",
  "SortName":"Pilot, The","Overview":"A retained synopsis","Genres":[],"Tags":["classic"],
  "Studios":[{"Name":"Example Studio"}],"People":[{"Name":"A. Actor","Type":"Actor","Role":"Lead"}],
  "ProductionYear":2024,"PremiereDate":"2024-01-02T00:00:00.0000000Z","OfficialRating":"TV-PG",
  "CommunityRating":8.4,"RunTimeTicks":18000000000,"DateLastSaved":"2026-09-01T12:00:00Z",
  "ProviderIds":{"Tmdb":"900","Tvdb":"901"},"ImageTags":{"Primary":"art-tag"},
  "UserData":{"Played":true,"PlaybackPositionTicks":1234},
  "SeriesId":"series-1","SeasonId":"season-1","ParentIndexNumber":1,"IndexNumber":1,
  "FutureProviderFact":{"Nested":true},"ApiKey":"must-not-persist","Path":"/private/media/Pilot.mkv",
  "MediaSources":[{
    "Id":"media-1","Protocol":"File","Container":"mkv","Size":123456,"Bitrate":4000000,
    "RunTimeTicks":18000000000,"ETag":"revision-1","Path":"/private/media/Pilot.mkv",
    "TranscodingUrl":"/Videos/episode-7/master.m3u8?api_key=must-not-persist",
    "MediaStreams":[
      {"Index":4,"Type":"Audio","Codec":"aac","Language":"jpn","Channels":2,"IsDefault":false},
      {"Index":0,"Type":"Video","Codec":"h264","Profile":"High","Level":41,"Width":1920,"Height":1080,"RealFrameRate":23.976,"PixelFormat":"yuv420p","VideoRangeType":"SDR"},
      {"Index":2,"Type":"Audio","Codec":"aac","Language":"eng","Channels":2,"IsDefault":true}
    ]
  }]
}`

func TestInventorySnapshotProjectsEmbyAndJellyfinToOneProviderNeutralModel(t *testing.T) {
	var projected []inventory.Snapshot
	for _, flavor := range []Flavor{Emby, Jellyfin} {
		t.Run(string(flavor), func(t *testing.T) {
			server := testkit.NewMediaServer(t)
			server.InventoryItems = map[string]json.RawMessage{"episode-7": json.RawMessage(richInventoryItem)}
			client := New(flavor, server.URL, server.AdminToken, "device-1")
			snapshot, ok, err := client.InventorySnapshot(context.Background(), "episode-7")
			if err != nil || !ok {
				t.Fatalf("InventorySnapshot = (%+v, %v, %v), want hit", snapshot, ok, err)
			}
			if snapshot.Kind != "episode" || snapshot.Observation.Facts.Name != "Pilot" ||
				snapshot.Observation.Coverage["genres"] != inventory.CoverageEmpty ||
				len(snapshot.Sources) != 1 || snapshot.Sources[0].Revision != "etag:revision-1" {
				t.Fatalf("snapshot = %+v", snapshot)
			}
			streams := snapshot.Sources[0].Observation.Facts.Streams
			if len(streams) != 3 || streams[0].Kind != inventory.StreamVideo || streams[1].Language != "eng" || streams[2].Language != "jpn" {
				t.Fatalf("ordered streams = %+v", streams)
			}
			encoded, err := json.Marshal(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			for _, secret := range []string{"must-not-persist", "/private/media/Pilot.mkv", "TranscodingUrl", "ApiKey"} {
				if strings.Contains(string(encoded), secret) {
					t.Fatalf("snapshot retained protected %q: %s", secret, encoded)
				}
			}
			if !strings.Contains(string(snapshot.Observation.Extension), "FutureProviderFact") {
				t.Fatalf("safe unknown field lost: %s", snapshot.Observation.Extension)
			}
			var extension map[string]any
			if err := json.Unmarshal(snapshot.Observation.Extension, &extension); err != nil {
				t.Fatal(err)
			}
			for _, subtree := range []string{"MediaSources", "MediaStreams", "UserData"} {
				if _, exists := extension[subtree]; exists {
					t.Fatalf("protected %s subtree survived sanitization: %s", subtree, snapshot.Observation.Extension)
				}
			}
			requests := server.Requests()
			if len(requests) != 1 {
				t.Fatalf("inventory requests = %+v, want one", requests)
			}
			query, queryErr := url.ParseQuery(requests[0].RawQuery)
			if queryErr != nil || query.Get("EnableUserData") != "false" {
				t.Fatalf("inventory request did not disable user data: %+v", requests)
			}
			// Authority is adapter provenance, so compare the provider-neutral content separately.
			snapshot.Origin.Authority = ""
			for i := range snapshot.Sources {
				snapshot.Sources[i].Locator.Authority = ""
			}
			projected = append(projected, snapshot)
		})
	}
	if len(projected) != 2 {
		return
	}
	projected[0].Observation.ObservedAt = projected[1].Observation.ObservedAt
	projected[0].Sources[0].Observation.ObservedAt = projected[1].Sources[0].Observation.ObservedAt
	if !reflect.DeepEqual(projected[0], projected[1]) {
		t.Fatalf("Emby and Jellyfin projections differ:\nEmby: %+v\nJellyfin: %+v", projected[0], projected[1])
	}
}

func TestInventoryOriginNeedsNoNetworkAndIgnoresTokenRotation(t *testing.T) {
	server := testkit.NewMediaServer(t)
	clientA := New(Emby, server.URL, "token-a", "device")
	clientB := New(Emby, server.URL+"/", "token-b", "device")
	originA, err := clientA.InventoryOrigin("episode-7")
	if err != nil {
		t.Fatal(err)
	}
	originB, err := clientB.InventoryOrigin("episode-7")
	if err != nil {
		t.Fatal(err)
	}
	if originA != originB {
		t.Fatalf("token rotation changed origin: %+v != %+v", originA, originB)
	}
	if requests := server.Requests(); len(requests) != 0 {
		t.Fatalf("InventoryOrigin made network requests: %+v", requests)
	}
}

func TestInventorySnapshotRejectsAmbiguousStreamOrderingWithoutLosingItem(t *testing.T) {
	server := testkit.NewMediaServer(t)
	raw := strings.Replace(richInventoryItem, `{"Index":4,"Type":"Audio"`, `{"Index":2,"Type":"Audio"`, 1)
	server.InventoryItems = map[string]json.RawMessage{"episode-7": json.RawMessage(raw)}
	snapshot, ok, err := New(Emby, server.URL, server.AdminToken, "device").InventorySnapshot(context.Background(), "episode-7")
	if err != nil || !ok {
		t.Fatalf("InventorySnapshot = (%+v, %v, %v), want item hit", snapshot, ok, err)
	}
	if snapshot.Sources[0].Observation.Coverage["streams"] != "" || len(snapshot.Sources[0].Observation.Facts.Streams) != 0 {
		t.Fatalf("ambiguous streams became authoritative: %+v", snapshot.Sources[0].Observation)
	}
}

func TestInventorySnapshotDerivesPlayabilityFromSourceFactsNotItemKind(t *testing.T) {
	server := testkit.NewMediaServer(t)
	server.InventoryItems = map[string]json.RawMessage{"future-1": json.RawMessage(`{
		"Id":"future-1","Type":"FuturePlayableKind","MediaStreams":[
			{"Index":0,"Type":"Video","Codec":"h264"},
			{"Index":1,"Type":"Audio","Language":"eng"}
		]
	}`)}
	snapshot, ok, err := New(Emby, server.URL, server.AdminToken, "device").InventorySnapshot(
		context.Background(), "future-1",
	)
	if err != nil || !ok {
		t.Fatalf("InventorySnapshot = (%+v, %v, %v), want hit", snapshot, ok, err)
	}
	if snapshot.Kind != "futureplayablekind" || len(snapshot.Sources) != 1 {
		t.Fatalf("future item = %+v, want source-derived playability", snapshot)
	}
}
