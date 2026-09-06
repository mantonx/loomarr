package filler_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/loomarr/loomarr/internal/filler"
)

// fakeProbe reports a fixed duration, so the scanner is testable without ffmpeg.
func fakeProbe(ms int64) filler.Prober {
	return func(context.Context, string) (filler.Probed, error) {
		return filler.Probed{DurationMs: ms}, nil
	}
}

// fakeProbeHD reports a duration AND a 1080p-shaped height, for the quality assertions.
func fakeProbeHD(ms int64, height int) filler.Prober {
	return func(context.Context, string) (filler.Probed, error) {
		return filler.Probed{DurationMs: ms, Height: height}, nil
	}
}

func writeFile(t *testing.T, dir, rel string) {
	t.Helper()
	full := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The identity is the path RELATIVE to FILLER_DIR, with forward slashes. Relative because
// FILLER_DIR differs between a host and a container and absolute ids would break on a remount;
// forward slashes so a Windows-authored catalog and a Linux one agree (the pod seed hashes ids,
// so a differing separator would silently change pod contents rather than failing).
func TestScanDir_IdentityIsTheRelativePath(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "1994/toys-transformers.mp4")
	writeFile(t, dir, "station-id.mp4")

	clips, _, err := filler.ScanDir(context.Background(), dir, fakeProbe(30000))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, c := range clips {
		got[c.Path] = true
	}
	for _, want := range []string{"1994/toys-transformers.mp4", "station-id.mp4"} {
		if !got[want] {
			t.Errorf("missing %q; got %v", want, got)
		}
	}
	for p := range got {
		if filepath.IsAbs(p) {
			t.Errorf("id %q is absolute — it would break the first time the mount moves", p)
		}
		if strings.Contains(p, `\`) {
			t.Errorf("id %q has a backslash — ids must be platform-independent", p)
		}
	}
}

// ⚠ THE display-vs-grounding split (§10 V44, live-found): the Archive downloader files a clip as
// "<archive-id> - <title>.mp4" to dodge id collisions, so `originalName` reads
// "CampbellsSoupAdvert - Campbell's Soup Advert" — which the guide showed verbatim. The scan must
// prefer the sidecar's clean `title` for the DISPLAY name, while still reading the era off the
// filename (the clean title has no year). This pins both halves: nice name, era still grounds.
func TestScanDir_PrefersSidecarTitleForDisplayButGroundsEraFromFilename(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "ab/cd/hash.mp4")
	// A sidecar with a clean title AND a mangled originalName that carries the year 1993.
	sidecar := `{"title":"Campbell's Soup Advert","loomarr":{"originalName":"CampbellsSoupAdvert - Campbell's Soup Advert 1993.mp4"}}`
	if err := os.WriteFile(filepath.Join(dir, "ab/cd/hash.info.json"), []byte(sidecar), 0o600); err != nil {
		t.Fatal(err)
	}

	clips, _, err := filler.ScanDir(context.Background(), dir, fakeProbe(30000))
	if err != nil {
		t.Fatal(err)
	}
	if len(clips) != 1 {
		t.Fatalf("got %d clips, want 1", len(clips))
	}
	c := clips[0]
	if c.Name != "Campbell's Soup Advert" {
		t.Errorf("display Name = %q, want the clean sidecar title, not the doubled filename", c.Name)
	}
	// ⚠ The load-bearing half: era must still ground off the filename's 1993, even though the clean
	// display title has no year. If naming used the title for BOTH, this era would be silently lost.
	if c.Era != 1993 {
		t.Errorf("Era = %d, want 1993 grounded from the filename — the display-name change must not break era grounding", c.Era)
	}
}

// A drop-folder accumulates junk. Non-media files must be ignored silently rather than probed.
func TestScanDir_IgnoresNonMediaFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "ad.mp4")
	for _, junk := range []string{".DS_Store", "notes.txt", "poster.jpg", "half.mp4.part"} {
		writeFile(t, dir, junk)
	}

	clips, _, err := filler.ScanDir(context.Background(), dir, fakeProbe(15000))
	if err != nil {
		t.Fatal(err)
	}
	if len(clips) != 1 || clips[0].Path != "ad.mp4" {
		t.Errorf("want only ad.mp4, got %+v", clips)
	}
}

// One bad file must not cost the channel all of its commercials. A half-copied download or a
// file with no video stream is normal in an operator-managed folder.
func TestScanDir_SkipsUnprobeableFilesRatherThanFailing(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "good.mp4")
	writeFile(t, dir, "broken.mp4")

	probe := func(_ context.Context, path string) (filler.Probed, error) {
		if strings.Contains(path, "broken") {
			return filler.Probed{}, os.ErrInvalid
		}
		return filler.Probed{DurationMs: 30000}, nil
	}
	clips, skipped, err := filler.ScanDir(context.Background(), dir, probe)
	if err != nil {
		t.Fatalf("one bad file failed the whole scan: %v", err)
	}
	if len(clips) != 1 || clips[0].Path != "good.mp4" {
		t.Errorf("want the good clip only, got %+v", clips)
	}
	if skipped != 1 {
		t.Errorf("skipped = %d, want 1 so the caller can log it", skipped)
	}
}

// A zero duration is not usable: the pod assembler fills a break to a target length, so a
// clip that occupies no time would either be skipped downstream or break the arithmetic.
func TestScanDir_RejectsZeroDuration(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "empty.mp4")

	clips, skipped, err := filler.ScanDir(context.Background(), dir, fakeProbe(0))
	if err != nil {
		t.Fatal(err)
	}
	if len(clips) != 0 {
		t.Errorf("a zero-duration clip was admitted: %+v", clips)
	}
	if skipped != 1 {
		t.Errorf("skipped = %d, want 1", skipped)
	}
}

// ⚠ **THE case the duration floor exists for** (§10 V40). `DurationMs > 0` was the only guard,
// and a **2.9KB / 33-millisecond** truncated download passed it — then sat filed and airable in
// the dev catalog, i.e. a third of a second of nothing in the middle of an ad break.
//
// "Has a readable duration" and "is a usable clip" are different questions, and only the second
// one matters at the boundary.
func TestScanDir_RejectsAClipShorterThanTheFloor(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "truncated.mp4")

	// The real fragment's length, against the shipped 10s floor.
	clips, skipped, err := filler.ScanDir(context.Background(), dir, fakeProbe(33), 10_000)
	if err != nil {
		t.Fatal(err)
	}
	if len(clips) != 0 {
		t.Errorf("a 33ms fragment was catalogued: %+v", clips)
	}
	if skipped != 1 {
		t.Errorf("skipped = %d, want 1", skipped)
	}
}

// The mirror: a clip AT the floor is kept. Without this, a floor that rejected everything would
// satisfy the test above while emptying the catalog.
func TestScanDir_KeepsAClipAtTheFloor(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "spot.mp4")

	clips, _, err := filler.ScanDir(context.Background(), dir, fakeProbe(10_000), 10_000)
	if err != nil {
		t.Fatal(err)
	}
	if len(clips) != 1 {
		t.Fatalf("a clip exactly at the floor was rejected, got %d", len(clips))
	}
}

// ⚠ **Omitting the floor means NO floor**, which is what keeps the other nine callers of ScanDir
// — almost all tests — meaning what they meant before V40 added the parameter.
func TestScanDir_NoFloorGivenAdmitsAShortClip(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "tiny.mp4")

	clips, _, err := filler.ScanDir(context.Background(), dir, fakeProbe(500))
	if err != nil {
		t.Fatal(err)
	}
	if len(clips) != 1 {
		t.Errorf("a short clip was rejected with no floor configured, got %d", len(clips))
	}
}

// ⚠ A video-only file plays as DEAD AIR in the middle of a break, which reads to a viewer as the
// stream having dropped. Rejected on sight.
//
// ⚠ Note this is about the STREAM's existence, never its level: a quiet clip is normalisation's
// problem at playout (§10 V40), and rejecting one would throw away perfectly good filler.
func TestScanDir_RejectsAClipWithNoAudioStream(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "silent.mp4")

	silent := func(context.Context, string) (filler.Probed, error) {
		return filler.Probed{DurationMs: 30_000, Height: 480, Silent: true}, nil
	}
	clips, skipped, err := filler.ScanDir(context.Background(), dir, silent, 10_000)
	if err != nil {
		t.Fatal(err)
	}
	if len(clips) != 0 {
		t.Errorf("a video-only file was catalogued: %+v", clips)
	}
	if skipped != 1 {
		t.Errorf("skipped = %d, want 1", skipped)
	}
}

func TestScanDir_RejectsAClipWithNoVideoStream(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "audio-only.mp4")

	audioOnly := func(context.Context, string) (filler.Probed, error) {
		return filler.Probed{DurationMs: 30_000, NoVideo: true}, nil
	}
	clips, skipped, err := filler.ScanDir(context.Background(), dir, audioOnly, 10_000)
	if err != nil {
		t.Fatal(err)
	}
	if len(clips) != 0 {
		t.Errorf("an audio-only file was catalogued: %+v", clips)
	}
	if skipped != 1 {
		t.Errorf("skipped = %d, want 1", skipped)
	}
}

// An unset FILLER_DIR is "filler not configured" — an empty catalog, not an error. A missing
// one IS an error: it is almost always a misconfigured path, and silently returning nothing
// presents as "filler mysteriously does nothing".
func TestScanDir_UnsetIsEmptyButMissingIsAnError(t *testing.T) {
	clips, _, err := filler.ScanDir(context.Background(), "", fakeProbe(1000))
	if err != nil || len(clips) != 0 {
		t.Errorf("unset dir: got %d clips, err %v — want empty and no error", len(clips), err)
	}

	if _, _, err := filler.ScanDir(context.Background(), "/definitely/not/here", fakeProbe(1000)); err == nil {
		t.Error("a missing FILLER_DIR must be reported, not silently empty")
	}
}

// Filename tagging is the cheapest tier (§10). Without it a clip lands as a generic
// interstitial the pod assembler can never place, so filler would silently never build.
func TestScanDir_InfersKindAndEraFromTheFilename(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "1994-toys-ad.mp4")
	writeFile(t, dir, "bumper-back-soon.mp4")

	clips, _, err := filler.ScanDir(context.Background(), dir, fakeProbe(20000))
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]filler.RawClip{}
	for _, c := range clips {
		byPath[c.Path] = c
	}
	if got := byPath["1994-toys-ad.mp4"]; got.Era != 1994 {
		t.Errorf("era not inferred from the filename: %+v", got)
	}
	if got := byPath["bumper-back-soon.mp4"]; got.Kind != filler.Bumper {
		t.Errorf("kind not inferred from the filename: %+v", got)
	}
}

func TestScanDir_RestoresCatalogKindFromPortableSidecar(t *testing.T) {
	dir := t.TempDir()
	media := filepath.Join(dir, "opaque.mp4")
	if err := os.WriteFile(media, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := filler.WriteSidecarTags(media, filler.SidecarTags{OriginalName: "untitled clip.mp4", Kind: string(filler.PSA)}, false); err != nil {
		t.Fatal(err)
	}

	clips, _, err := filler.ScanDir(context.Background(), dir, fakeProbe(20_000))
	if err != nil {
		t.Fatal(err)
	}
	if len(clips) != 1 || clips[0].Kind != filler.PSA {
		t.Fatalf("rebuilt clips=%+v, want portable PSA kind", clips)
	}
}

// --- ClipPath: the containment boundary ---
//
// ⚠ THREE tests lived here and are SUPERSEDED by `clippath_test.go` (V38c). They asserted the
// pre-hash model, where a clip id was an operator-shaped relative path (`1994/toys.mp4`) and
// containment was proved by cleaning the path and checking it had not escaped.
//
// Under hash identity the guard is an ALLOW-LIST on the alphabet instead — an id is 64 lowercase
// hex characters, so a separator, a dot or an encoding cannot appear in a valid one at all. The
// old tests could not simply be updated because one of them asserted the OPPOSITE of the new
// rule: `TestClipPath_AllowsInternalDotDotThatStaysInside` required `ads/../bumpers/x.mp4` to be
// ACCEPTED, which the allow-list rightly refuses.
//
// Validating what an id may BE is a stronger guarantee than reasoning about what a path might
// become, which is why the replacement is not a like-for-like port.

// --- DirSource: the scan is the source of truth, Tunarr only annotates ---

type stubTunarr struct {
	ids map[string]string
	err error
}

func (s stubTunarr) EnsureLocalSource(context.Context, string) error { return nil }
func (s stubTunarr) LocalClipIDsByName(context.Context) (map[string]string, error) {
	return s.ids, s.err
}

// THE POINT OF THE CHANGE: no Tunarr still yields a full catalog. Previously clips were
// discovered BY asking Tunarr, so an internal-playout install with no Tunarr had no commercials.
func TestDirSource_NoTunarrStillProducesAFullCatalog(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "ad1.mp4")
	writeFile(t, dir, "ad2.mp4")

	src := filler.DirSource{Layout: testLayout(dir), Probe: fakeProbe(30000)}
	clips, err := src.ListLocalClips(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(clips) != 2 {
		t.Fatalf("want 2 clips with no Tunarr configured, got %d", len(clips))
	}
	for _, c := range clips {
		if c.Path == "" {
			t.Error("a clip has no identity")
		}
		if c.TunarrProgramID != "" {
			t.Errorf("clip %q has a Tunarr id with no Tunarr configured", c.Path)
		}
	}
}

func TestDirSource_ExcludesAppliedCustomWatchSubtree(t *testing.T) {
	dir := t.TempDir()
	watch := filepath.Join(dir, "inbox")
	writeFile(t, dir, "filed.mp4")
	writeFile(t, dir, "inbox/still-arriving.mp4")

	src := filler.DirSource{Layout: testLayout(dir, watch), Probe: fakeProbe(30000)}
	clips, err := src.ListLocalClips(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(clips) != 1 || clips[0].Path != "filed.mp4" {
		t.Fatalf("custom-watch scan = %+v, want only the filed clip", clips)
	}
}

func TestDirSource_ExcludesAliasedNestedWatchSubtree(t *testing.T) {
	realRoot := t.TempDir()
	aliases := t.TempDir()
	aliasA := filepath.Join(aliases, "a")
	aliasB := filepath.Join(aliases, "b")
	if err := os.Symlink(realRoot, aliasA); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(realRoot, aliasB); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	layout, err := filler.NewLayout(filepath.Join(aliasA, "clips"), filepath.Join(aliasB, "clips", "inbox"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(layout.WatchDir(), 0o750); err != nil {
		t.Fatal(err)
	}
	writeFile(t, layout.ClipDir(), "filed.mp4")
	writeFile(t, layout.WatchDir(), "still-arriving.mp4")

	clips, err := (filler.DirSource{Layout: layout, Probe: fakeProbe(30000)}).ListLocalClips(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(clips) != 1 || clips[0].Path != "filed.mp4" {
		t.Fatalf("aliased-watch scan = %+v, want only the filed clip", clips)
	}
}

// When Tunarr IS configured its uuids are attached, so Tunarr-backed channels still build
// filler-lists from the same catalog.
func TestDirSource_AnnotatesWithTunarrIDsWhenAvailable(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "ad1.mp4")

	src := filler.DirSource{
		Layout: testLayout(dir), Probe: fakeProbe(30000),
		Tunarr: stubTunarr{ids: map[string]string{"ad1": "tun-123"}},
	}
	clips, err := src.ListLocalClips(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(clips) != 1 || clips[0].TunarrProgramID != "tun-123" {
		t.Errorf("Tunarr id not attached: %+v", clips)
	}
	if clips[0].Path != "ad1.mp4" {
		t.Errorf("identity should still be the path, got %q", clips[0].Path)
	}
}

// A Tunarr failure must not fail the scan: the catalog is already complete and internal
// playout needs nothing more. Only Tunarr-backed channels lose anything, and only until the
// next sync.
func TestDirSource_TunarrFailureDoesNotFailTheScan(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "ad1.mp4")

	src := filler.DirSource{
		Layout: testLayout(dir), Probe: fakeProbe(30000),
		Tunarr: stubTunarr{err: os.ErrDeadlineExceeded},
	}
	clips, err := src.ListLocalClips(context.Background())
	if err != nil {
		t.Fatalf("a Tunarr outage failed the whole scan: %v", err)
	}
	if len(clips) != 1 {
		t.Fatalf("want the clip anyway, got %d", len(clips))
	}
	if clips[0].TunarrProgramID != "" {
		t.Error("a uuid appeared despite the Tunarr call failing")
	}
}

// The prober takes the FFMPEG path and derives ffprobe from it — the two ship together, so an
// operator who moved one moved both, and a second setting would have only one correct value.
//
// This test exists because the original API took a bare `bin` and the call site handed it
// `playout.ffmpeg_path`. That ran `ffmpeg -show_entries …`, which is not valid, so every probe
// failed — and since ScanDir skips unprobeable files by design, the catalog came back silently
// empty with no error anywhere. Only running it end to end surfaced that.
func TestFFprobeDurationNextTo_DerivesFfprobeFromTheFfmpegPath(t *testing.T) {
	// A path that does not exist, so the error text tells us which binary it TRIED.
	probe := filler.FFprobeNextTo("/opt/custom/bin/ffmpeg")
	_, err := probe(context.Background(), "whatever.mp4")
	if err == nil {
		t.Fatal("expected a failure for a nonexistent binary")
	}
	if strings.Contains(err.Error(), "ffmpeg") && !strings.Contains(err.Error(), "ffprobe") {
		t.Errorf("the prober invoked ffmpeg rather than ffprobe: %v", err)
	}
}

// Quality is bucketed by NEAREST standard, not exact match: real files are 1088 (encoder
// padding), 718, 1082. An exact-match table would leave those blank while anyone looking at
// the file would call it 1080p.
func TestQualityFromHeight_BucketsRealWorldHeights(t *testing.T) {
	for _, tc := range []struct {
		height int
		want   string
	}{
		{2160, "4K"},
		{1080, "1080p"},
		{1088, "1080p"}, // encoder padding — the case exact matching gets wrong
		{720, "720p"},
		{718, "720p"},
		{480, "480p"},
		{360, "360p"},
		{240, "240p"},
		{0, ""}, // no video stream: no badge, rather than a claimed resolution
	} {
		if got := filler.QualityFromHeight(tc.height); got != tc.want {
			t.Errorf("QualityFromHeight(%d) = %q, want %q", tc.height, got, tc.want)
		}
	}
}

// The scan carries quality onto the clip, so the guide's hover card can explain a grainy ad.
func TestScanDir_RecordsClipQuality(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "sunnyd.mp4")

	clips, _, err := filler.ScanDir(context.Background(), dir, fakeProbeHD(30000, 480))
	if err != nil {
		t.Fatal(err)
	}
	if len(clips) != 1 {
		t.Fatalf("got %d clips", len(clips))
	}
	if clips[0].Quality != "480p" {
		t.Errorf("quality = %q, want 480p from the probed height", clips[0].Quality)
	}
}

// A clip with no video stream is still USABLE — an audio-only bumper plays fine. It simply
// has no quality badge; rejecting it would cost a channel a clip over a display detail.
func TestScanDir_NoVideoStreamStillYieldsAClip(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "jingle.mp4")

	clips, skipped, err := filler.ScanDir(context.Background(), dir, fakeProbeHD(5000, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(clips) != 1 || skipped != 0 {
		t.Fatalf("got %d clips, %d skipped — a video-less clip must still be usable", len(clips), skipped)
	}
	if clips[0].Quality != "" {
		t.Errorf("quality = %q, want empty rather than an invented resolution", clips[0].Quality)
	}
}

// The scan does NOT descend into the watch folder (§10 V38c). It sits inside the clip folder by
// default, so without this a file still waiting to be filed is catalogued at its ARRIVAL path —
// and then pruned on the very next sync, once intake has moved it to its hash. The operator sees
// clips appear and vanish with nothing wrong on disk.
func TestScanDir_SkipsTheWatchFolder(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "filed.mp4")
	writeFile(t, dir, filler.WatchDirName+"/just-arrived.mp4")
	// Nested, too — a downloader may write into a subfolder of the watch folder.
	writeFile(t, dir, filler.WatchDirName+"/pending/also-arriving.mp4")

	clips, _, err := filler.ScanDir(context.Background(), dir, fakeProbe(30_000))
	if err != nil {
		t.Fatal(err)
	}
	if len(clips) != 1 {
		var got []string
		for _, c := range clips {
			got = append(got, c.Path)
		}
		t.Fatalf("scanned %v, want only the filed clip — the watch folder was walked", got)
	}
	if clips[0].Path != "filed.mp4" {
		t.Errorf("scanned %q, want filed.mp4", clips[0].Path)
	}
}

// ⚠ Skipped by NAME, not by "it is the first level down". A folder genuinely called `_watch`
// nested deeper is still the watch folder as far as an operator is concerned, and a clip folder
// that legitimately contains one is not a case worth optimising for. This pins the rule so the
// implementation cannot quietly narrow to a top-level-only check.
func TestScanDir_SkipsANestedWatchFolderToo(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "ads/filed.mp4")
	writeFile(t, dir, "ads/"+filler.WatchDirName+"/arriving.mp4")

	clips, _, err := filler.ScanDir(context.Background(), dir, fakeProbe(30_000))
	if err != nil {
		t.Fatal(err)
	}
	if len(clips) != 1 || clips[0].Path != "ads/filed.mp4" {
		t.Errorf("scanned %d clips, want only ads/filed.mp4", len(clips))
	}
}
