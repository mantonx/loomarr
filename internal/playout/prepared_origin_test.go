package playout

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/diagnostics"
	"github.com/loomarr/loomarr/internal/metrics"
	"github.com/loomarr/loomarr/internal/prepared"
)

type fixedPreparedResolver struct {
	window PreparedWindow
	ok     bool
}

func (r fixedPreparedResolver) ResolvePrepared(context.Context, TuneRequest) (PreparedWindow, bool, error) {
	return r.window, r.ok, nil
}

func preparedSpec(source string) prepared.Specification {
	return prepared.Specification{
		SourceFingerprint: source,
		Rendition: prepared.RenditionContract{
			VideoCodec: "h264", AudioCodec: "aac", Width: 1920, Height: 1080,
			FrameRate: 25, VideoBitrateKbps: 5000, AudioBitrateKbps: 160,
			SegmentDurationMS: 2000, PackagingVersion: 1,
		},
	}
}

func publishHLS(t *testing.T, lib *prepared.Library, spec prepared.Specification) prepared.Publication {
	return publishHLSWithSegments(t, lib, spec, 4)
}

func publishHLSWithSegments(
	t *testing.T, lib *prepared.Library, spec prepared.Specification, segmentCount int,
) prepared.Publication {
	t.Helper()
	pub, err := lib.Publish(t.Context(), spec, func(_ context.Context, workspace string) (prepared.Output, error) {
		var manifest strings.Builder
		manifest.WriteString("#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-TARGETDURATION:2\n")
		manifest.WriteString("#EXT-X-MAP:URI=\"init.mp4\"\n")
		files := []string{prepared.MediaManifestName, "init.mp4"}
		for i := range segmentCount {
			name := fmt.Sprintf("seg-%d.m4s", i)
			files = append(files, name)
			fmt.Fprintf(&manifest, "#EXTINF:2.000,\n%s\n", name)
		}
		manifest.WriteString("#EXT-X-ENDLIST\n")
		for _, file := range files {
			body := []byte(file)
			if file == prepared.MediaManifestName {
				body = []byte(manifest.String())
			}
			if err := os.WriteFile(filepath.Join(workspace, file), body, 0o600); err != nil {
				return prepared.Output{}, err
			}
		}
		return prepared.Output{Files: files}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return pub
}

func TestPreparedOriginExposesTheSharedDVRHorizonAcrossAirings(t *testing.T) {
	t.Parallel()
	lib, err := prepared.NewLibrary(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	oldestSpec := preparedSpec("episode-a")
	previousSpec := preparedSpec("episode-b")
	currentSpec := preparedSpec("episode-c")
	oldest := publishHLSWithSegments(t, lib, oldestSpec, 240)
	previous := publishHLSWithSegments(t, lib, previousSpec, 240)
	current := publishHLSWithSegments(t, lib, currentSpec, 240)
	started := time.Unix(10_000, 0).UTC()
	origin := newPreparedOrigin(lib, fixedPreparedResolver{ok: true, window: PreparedWindow{
		Previous: []PreparedAiring{
			{Specification: oldestSpec, StartedAt: started.Add(-16 * time.Minute)},
			{Specification: previousSpec, StartedAt: started.Add(-8 * time.Minute)},
		},
		Current: PreparedAiring{Specification: currentSpec, StartedAt: started, Offset: time.Minute},
	}})

	presentation, hit, err := origin.Tune(t.Context(), TuneRequest{
		ChannelID: "ch-one", Plan: PlanBaseline, Delivery: DeliveryHLS,
	})
	if err != nil || !hit {
		t.Fatalf("Tune = (_, %v, %v), want prepared hit", hit, err)
	}
	manifest := string(presentation.Manifest)
	firstInWindow := preparedAssetToken(oldest.Key, "seg-60.m4s")
	justExpired := preparedAssetToken(oldest.Key, "seg-59.m4s")
	for _, want := range []string{
		"#EXT-X-PROGRAM-DATE-TIME:" + started.Add(-14*time.Minute).Format(time.RFC3339Nano),
		firstInWindow,
		preparedAssetToken(previous.Key, "seg-0.m4s"),
		preparedAssetToken(current.Key, "seg-30.m4s"),
	} {
		if !strings.Contains(manifest, want) {
			t.Errorf("manifest missing %q", want)
		}
	}
	if strings.Contains(manifest, justExpired) {
		t.Errorf("manifest retained expired segment %q", justExpired)
	}
	if got := strings.Count(manifest, "#EXT-X-DISCONTINUITY"); got != 2 {
		t.Errorf("discontinuities = %d, want two Airing boundaries", got)
	}
}

func TestPreparedOriginRendersAKeyedWallClockManifest(t *testing.T) {
	t.Parallel()
	lib, err := prepared.NewLibrary(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	spec := preparedSpec("source-a")
	pub := publishHLS(t, lib, spec)
	started := time.Unix(1_000, 0).UTC()
	preparedOrigin := newPreparedOrigin(lib, fixedPreparedResolver{
		window: PreparedWindow{Current: PreparedAiring{
			Specification: spec, StartedAt: started, Offset: 5 * time.Second,
		}}, ok: true,
	})

	presentation, hit, err := preparedOrigin.Tune(t.Context(), TuneRequest{
		ChannelID: "ch-one", Plan: PlanBaseline, Delivery: DeliveryHLS,
	})
	if err != nil || !hit {
		t.Fatalf("Tune = (_, %v, %v), want prepared hit", hit, err)
	}
	manifest := string(presentation.Manifest)
	initAsset := preparedAssetToken(pub.Key, "init.mp4")
	seg0 := preparedAssetToken(pub.Key, "seg-0.m4s")
	seg1 := preparedAssetToken(pub.Key, "seg-1.m4s")
	seg2 := preparedAssetToken(pub.Key, "seg-2.m4s")
	for _, token := range []string{initAsset, seg0, seg1, seg2} {
		if strings.Contains(token, "/") {
			t.Fatalf("prepared asset token %q spans multiple route segments", token)
		}
	}
	for _, want := range []string{
		"#EXT-X-MEDIA-SEQUENCE:500", "#EXT-X-PROGRAM-DATE-TIME:1970-01-01T00:16:40Z",
		fmt.Sprintf(`#EXT-X-MAP:URI="%s"`, initAsset), seg0, seg1, seg2,
	} {
		if !strings.Contains(manifest, want) {
			t.Errorf("manifest missing %q:\n%s", want, manifest)
		}
	}
	for _, unwanted := range []string{preparedAssetToken(pub.Key, "seg-3.m4s"), "#EXT-X-ENDLIST"} {
		if strings.Contains(manifest, unwanted) {
			t.Errorf("manifest contains %q:\n%s", unwanted, manifest)
		}
	}

	asset, ok, err := preparedOrigin.OpenAsset("ch-one", PlanBaseline, seg2)
	if err != nil || !ok {
		t.Fatalf("OpenAsset = (_, %v, %v), want hit", ok, err)
	}
	defer func() { _ = asset.Content.Close() }()
	body, err := io.ReadAll(asset.Content)
	if err != nil || string(body) != "seg-2.m4s" {
		t.Fatalf("asset body = %q, err=%v", body, err)
	}
	if !asset.Immutable {
		t.Fatal("prepared asset is not marked immutable")
	}
	if _, ok, err := preparedOrigin.OpenAsset("ch-one", PlanBaseline, seg2+".ts"); err != nil || ok {
		t.Fatalf("asset with a forged content-type suffix = (_, %v, %v), want miss", ok, err)
	}
}

func TestPreparedMPEGTSBlockCopiesPublicationAtAiringOffset(t *testing.T) {
	t.Parallel()
	lib, err := prepared.NewLibrary(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	spec := preparedSpec("source-a")
	pub := publishHLS(t, lib, spec)
	started := time.Unix(1_000, 0).UTC()
	identity := AiringIdentity{
		StartedAt: started, EndsAt: started.Add(8 * time.Minute), Kind: "program",
		ContentID: "movie:tmdb:1", ScheduleBlockID: "block-one",
	}
	origin := newPreparedOrigin(lib, fixedPreparedResolver{ok: true, window: PreparedWindow{
		Current: PreparedAiring{
			Specification: spec, StartedAt: started, Offset: 75 * time.Second, Identity: identity,
		},
	}})
	var gotArgs []string
	var gotSpec diagnostics.ProcessSpec
	source := newPreparedMPEGTSBlockSource(origin, func(
		_ context.Context, args []string, processSpec diagnostics.ProcessSpec,
	) (*Process, error) {
		gotArgs = append([]string(nil), args...)
		gotSpec = processSpec
		return &Process{Stdout: io.NopCloser(strings.NewReader("prepared-ts"))}, nil
	})

	block, err := source(t.Context(), "ch-one", PlanFull)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = block.Content.Close() }()
	body, err := io.ReadAll(block.Content)
	if err != nil || string(body) != "prepared-ts" {
		t.Fatalf("block body = %q, err=%v", body, err)
	}
	if block.Identity != identity {
		t.Fatalf("block identity = %+v, want %+v", block.Identity, identity)
	}
	wantFormat := BroadcastFormat{
		VideoCodec: "h264", Width: 1920, Height: 1080, Framerate: 25,
		VideoBitrate: 5000, AudioBitrate: 160,
	}
	if block.Format != wantFormat {
		t.Fatalf("block format = %+v, want %+v", block.Format, wantFormat)
	}
	joined := strings.Join(gotArgs, " ")
	for _, want := range []string{
		"-ss 75.000", "-t 405.000", "-c:v copy", "-c:a copy", "-f mpegts",
		filepath.Join(pub.Directory, prepared.MediaManifestName),
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("prepared remux args missing %q: %s", want, joined)
		}
	}
	if strings.Contains(joined, "-readrate") {
		t.Fatalf("prepared copy remux must leave pacing to the Channel mux: %s", joined)
	}
	if gotSpec.Purpose != "playout_prepared_remux" || gotSpec.ChannelID != "ch-one" ||
		gotSpec.ScheduleBlockID != "block-one" || strings.Contains(strings.Join(gotSpec.Args, " "), pub.Directory) {
		t.Fatalf("diagnostic process spec = %+v, want correlated and path-redacted", gotSpec)
	}
}

func TestPreparedMPEGTSRejectsUnsupportedPublicationFormat(t *testing.T) {
	contract := preparedSpec("source-a").Rendition
	contract.VideoCodec = "vp9"
	if _, ok := preparedBroadcastFormat(contract); ok {
		t.Fatal("raw prepared delivery accepted an unsupported video codec")
	}
	contract.VideoCodec = "h264"
	contract.AudioLayout = "5.1"
	if _, ok := preparedBroadcastFormat(contract); ok {
		t.Fatal("raw prepared delivery accepted audio that violates the stable stereo session shape")
	}
}

func TestOriginPreparedHitBypassesLiveAndMissFallsBack(t *testing.T) {
	t.Parallel()
	lib, err := prepared.NewLibrary(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	spec := preparedSpec("source-a")
	publishHLS(t, lib, spec)
	hls := &tuneHLS{path: filepath.Join(t.TempDir(), "live.m3u8")}
	if err := os.WriteFile(hls.path, []byte("live"), 0o600); err != nil {
		t.Fatal(err)
	}

	hitOrigin := newOrigin(newPreparedOrigin(lib, fixedPreparedResolver{
		window: PreparedWindow{Current: PreparedAiring{
			Specification: spec, StartedAt: time.Unix(1_000, 0), Offset: 0,
		}}, ok: true,
	}), nil, hls)
	got, err := hitOrigin.Tune(t.Context(), TuneRequest{ChannelID: "prepared", Plan: PlanBaseline, Delivery: DeliveryHLS})
	if err != nil || string(got.Manifest) == "live" || hls.channel != "" {
		t.Fatalf("prepared Tune = (%q, %v), live channel=%q", got.Manifest, err, hls.channel)
	}
	gotAgain, err := hitOrigin.Tune(t.Context(), TuneRequest{
		ChannelID: "another-channel", Plan: PlanBaseline, Delivery: DeliveryHLS,
	})
	if err != nil || string(gotAgain.Manifest) != string(got.Manifest) || hls.channel != "" {
		t.Fatalf("shared prepared Tune = (%q, %v), live channel=%q", gotAgain.Manifest, err, hls.channel)
	}

	recorder := metrics.New(metrics.Options{})
	missOrigin := newOrigin(newPreparedOrigin(lib, fixedPreparedResolver{ok: false}), nil, hls)
	missOrigin.observer = recorder
	got, err = missOrigin.Tune(t.Context(), TuneRequest{ChannelID: "fallback", Plan: PlanBaseline, Delivery: DeliveryHLS})
	if err != nil || string(got.Manifest) != "live" || hls.channel != "fallback" {
		t.Fatalf("fallback Tune = (%q, %v), live channel=%q", got.Manifest, err, hls.channel)
	}
	assertMetricsContain(t, recorder, `loomarr_playout_fallbacks_total{reason="prepared_to_live"} 1`)

	hls.channel = ""
	_, err = missOrigin.Tune(t.Context(), TuneRequest{
		ChannelID: "warm-only", Plan: PlanBaseline, Delivery: DeliveryHLS, PreparedOnly: true,
	})
	if !errors.Is(err, ErrPreparedUnavailable) || hls.channel != "" {
		t.Fatalf("prepared-only miss = %v, live channel=%q; want clean miss without live fallback", err, hls.channel)
	}
}

func TestPreparedManifestCannotReferenceOutsideItsPublication(t *testing.T) {
	t.Parallel()
	spec := preparedSpec("source-a")
	window := PreparedAiring{Specification: spec, StartedAt: time.Unix(1_000, 0)}
	manifest := []byte("#EXTM3U\n#EXT-X-TARGETDURATION:2\n#EXTINF:2,\n../outside.m4s\n")
	if _, err := parsePreparedManifest(manifest, strings.Repeat("a", 64), []string{"media.m3u8"}, window); err == nil {
		t.Fatal("renderPreparedManifest accepted an asset outside the publication")
	}
}

func TestPreparedOriginCarriesThePreviousAiringAcrossADiscontinuity(t *testing.T) {
	t.Parallel()
	lib, err := prepared.NewLibrary(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	previousSpec := preparedSpec("episode-a")
	currentSpec := preparedSpec("episode-b")
	previous := publishHLS(t, lib, previousSpec)
	current := publishHLS(t, lib, currentSpec)
	started := time.Unix(1_004, 0).UTC()
	origin := newPreparedOrigin(lib, fixedPreparedResolver{ok: true, window: PreparedWindow{
		Previous: []PreparedAiring{{Specification: previousSpec, StartedAt: started.Add(-8 * time.Second)}},
		Current:  PreparedAiring{Specification: currentSpec, StartedAt: started, Offset: 500 * time.Millisecond},
	}})

	presentation, hit, err := origin.Tune(t.Context(), TuneRequest{ChannelID: "ch-one", Plan: PlanBaseline, Delivery: DeliveryHLS})
	if err != nil || !hit {
		t.Fatalf("Tune = (_, %v, %v), want prepared hit", hit, err)
	}
	manifest := string(presentation.Manifest)
	wantOrder := []string{
		preparedAssetToken(previous.Key, "seg-2.m4s"), preparedAssetToken(previous.Key, "seg-3.m4s"),
		"#EXT-X-DISCONTINUITY", fmt.Sprintf(`#EXT-X-MAP:URI="%s"`, preparedAssetToken(current.Key, "init.mp4")),
		preparedAssetToken(current.Key, "seg-0.m4s"),
	}
	position := 0
	for _, want := range wantOrder {
		next := strings.Index(manifest[position:], want)
		if next < 0 {
			t.Fatalf("manifest missing %q after byte %d:\n%s", want, position, manifest)
		}
		position += next + len(want)
	}
}

// The rendered window must carry BOTH tags a native player needs across a programme boundary. The
// pre-existing discontinuity test asserts an ordered subsequence, which is structurally blind to a
// tag that is simply absent — so these assert presence and value instead.
func TestPreparedManifestCarriesDiscontinuitySequenceAndPerBoundaryDateTime(t *testing.T) {
	t.Parallel()
	lib, err := prepared.NewLibrary(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	previousSpec := preparedSpec("episode-a")
	currentSpec := preparedSpec("episode-b")
	publishHLS(t, lib, previousSpec)
	publishHLS(t, lib, currentSpec)
	started := time.Unix(1_004, 0).UTC()
	origin := newPreparedOrigin(lib, fixedPreparedResolver{ok: true, window: PreparedWindow{
		Previous: []PreparedAiring{{
			Specification: previousSpec,
			StartedAt:     started.Add(-8 * time.Second),
			// Three programmes already scrolled out of this Channel's window.
			DiscontinuitySequence: 3,
		}},
		Current: PreparedAiring{Specification: currentSpec, StartedAt: started, Offset: 500 * time.Millisecond},
	}})

	presentation, hit, err := origin.Tune(t.Context(), TuneRequest{ChannelID: "ch-one", Plan: PlanBaseline, Delivery: DeliveryHLS})
	if err != nil || !hit {
		t.Fatalf("Tune = (_, %v, %v), want prepared hit", hit, err)
	}
	manifest := string(presentation.Manifest)

	// The ordinal comes from the airing at the HEAD of the window, which is what a reload must be
	// able to correlate against.
	if want := "#EXT-X-DISCONTINUITY-SEQUENCE:3"; !strings.Contains(manifest, want) {
		t.Errorf("manifest missing %q:\n%s", want, manifest)
	}

	// One PDT per boundary: the window spans two airings, so a single head PDT is the bug.
	if got, want := strings.Count(manifest, "#EXT-X-PROGRAM-DATE-TIME:"), 2; got != want {
		t.Errorf("PROGRAM-DATE-TIME count = %d, want %d (one per boundary):\n%s", got, want, manifest)
	}

	// The second PDT must describe the segment it precedes, not repeat the window's head.
	discontinuity := strings.Index(manifest, "#EXT-X-DISCONTINUITY\n")
	if discontinuity < 0 {
		t.Fatalf("manifest has no discontinuity:\n%s", manifest)
	}
	if !strings.Contains(manifest[discontinuity:], "#EXT-X-PROGRAM-DATE-TIME:"+started.Format(time.RFC3339Nano)) {
		t.Errorf("no PDT for the current airing's start after the boundary:\n%s", manifest)
	}
}

// A Channel with no boundary history omits the tag rather than asserting a false zero point.
func TestPreparedManifestOmitsDiscontinuitySequenceAtTheStartOfAChannel(t *testing.T) {
	t.Parallel()
	lib, err := prepared.NewLibrary(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	spec := preparedSpec("episode-a")
	publishHLS(t, lib, spec)
	origin := newPreparedOrigin(lib, fixedPreparedResolver{ok: true, window: PreparedWindow{
		Current: PreparedAiring{Specification: spec, StartedAt: time.Unix(1_000, 0).UTC(), Offset: time.Second},
	}})

	presentation, hit, err := origin.Tune(t.Context(), TuneRequest{ChannelID: "ch-one", Plan: PlanBaseline, Delivery: DeliveryHLS})
	if err != nil || !hit {
		t.Fatalf("Tune = (_, %v, %v), want prepared hit", hit, err)
	}
	if manifest := string(presentation.Manifest); strings.Contains(manifest, "#EXT-X-DISCONTINUITY-SEQUENCE") {
		t.Errorf("unstarted Channel should omit the tag:\n%s", manifest)
	}
}
