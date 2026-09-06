package filler

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/mediatools"
)

// Scanning FILLER_DIR directly (§10, revised by §9.1).
//
// Loomarr walks its own drop-folder and probes each clip with ffprobe. This replaces asking
// Tunarr to scan the folder and reporting its program ids back — see Clip for why the old
// arrangement could not serve internal playout, and why the dependency ran the wrong way.
//
// The premise that justified the detour is also gone: it existed so "probing stays out of
// loomarr entirely", and §14 now bundles ffmpeg AND ffprobe as core runtime dependencies
// precisely because internal playout owns duration and cut points. Scanning locally spends a
// dependency we already have rather than requiring a service we made optional.

// clipExtensions are the container formats a filler clip may use.
//
// An ALLOWLIST rather than "probe everything and see": a drop-folder accumulates
// `.DS_Store`, `.nfo`, partial `.part` downloads and cover art, and probing each one costs an
// ffprobe exec plus a log line. Extensions are the cheap first filter; ffprobe is still the
// authority on whether a file has a usable stream.
var clipExtensions = map[string]bool{
	".mp4": true, ".mkv": true, ".avi": true, ".mov": true,
	".m4v": true, ".webm": true, ".ts": true, ".mpg": true, ".mpeg": true,
}

// QualityFromHeight buckets a pixel height into the label the guide shows.
//
// Bucketed by NEAREST standard rather than by exact match: real files are 1088, 1082, 718 —
// encoder padding and anamorphic sources both produce off-by-a-few heights, and an exact-match
// table would leave those blank while a person looking at the file would call it 1080p.
func QualityFromHeight(h int) string {
	switch {
	case h <= 0:
		return ""
	case h >= 2000:
		return "4K"
	case h >= 1000:
		return "1080p"
	case h >= 700:
		return "720p"
	case h >= 460:
		return "480p"
	case h >= 340:
		return "360p"
	default:
		return "240p"
	}
}

// ScanDir walks dir and returns one RawClip per playable file found.
//
// Errors on individual files are SKIPPED, not fatal. A drop-folder is operator-managed and
// will contain junk, a half-copied file, or something with no video stream; refusing to build
// a catalog because one file is bad would mean one stray download silently costs a channel all
// of its commercials. What is skipped is reported so the caller can log a count.
// ⚠ `minDurationMs` is VARIADIC rather than a fourth parameter, and that is a deliberate
// trade. Ten callers exist, nine of them tests that care nothing about the floor; making it
// required would have meant touching all of them to write `0` — a diff that says nothing and
// buries the one call site that matters. Omitted ⇒ no floor, which is exactly what a test
// asserting "this file is catalogued" wants.
func ScanDir(ctx context.Context, dir string, probe Prober, minDurationMs ...int64) (clips []RawClip, skipped int, err error) {
	if dir == "" {
		return nil, 0, nil // filler not configured — an empty catalog, not an error
	}
	if probe == nil {
		probe = FFprobe
	}
	var minMs int64
	if len(minDurationMs) > 0 {
		minMs = minDurationMs[0]
	}
	return scanDir(ctx, dir, "", probe, minMs)
}

// scanDir is ScanDir with the generation's applied watch folder excluded. The public ScanDir
// remains the small standalone filesystem helper; DirSource is the storage-layout-aware module.
func scanDir(ctx context.Context, dir, watchDir string, probe Prober, minMs int64) (clips []RawClip, skipped int, err error) {
	if dir == "" {
		return nil, 0, nil
	}
	if probe == nil {
		probe = FFprobe
	}
	watchInside := ""
	if rel, relErr := filepath.Rel(dir, watchDir); watchDir != "" && relErr == nil && rel != "." &&
		!filepath.IsAbs(rel) && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		watchInside = filepath.Clean(filepath.Join(dir, rel))
	}

	walkErr := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// An error on the ROOT is fatal; one on a child is not, and telling them apart
			// matters. WalkDir reports a missing/unreadable root by calling this callback once
			// with path == dir, so swallowing every error here made a misconfigured FILLER_DIR
			// return an empty catalog and no error — "filler mysteriously does nothing", the
			// exact failure the check below was written to prevent. (Verified by executing
			// WalkDir against a missing directory; the test asserted the intent and caught it.)
			if path == dir {
				return err
			}
			// A child: an unreadable subdirectory shouldn't abort the whole scan.
			skipped++
			return nil //nolint:nilerr // deliberate: skip and continue
		}
		if d.IsDir() {
			if watchInside != "" && filepath.Clean(path) == watchInside {
				return fs.SkipDir
			}
			// ⚠ The watch folder sits INSIDE the clip folder by default (§10 V38c), and the scan
			// must not descend into it. A file still waiting to be filed would otherwise be
			// catalogued at its ARRIVAL path — and then pruned on the very next sync, once intake
			// moved it to its hash. DirSource excludes the exact applied watch subtree above;
			// the name check remains for legacy/generated `_watch` folders elsewhere in the tree.
			// Dot-directories are Loomarr's generated/staging data. In particular, the transcode
			// rung builds a fully-probed replacement under `.transcode` before publishing it at its
			// content-addressed path; cataloguing that work-in-progress would create a second clip.
			if path != dir && (d.Name() == WatchDirName || strings.HasPrefix(d.Name(), ".")) {
				return fs.SkipDir
			}
			return nil
		}
		if !clipExtensions[strings.ToLower(filepath.Ext(path))] {
			return nil // not a media container; not counted as skipped (it is not a clip)
		}

		// The location: path RELATIVE to dir, forward slashes so it is identical on every
		// platform. A Windows-authored catalog and a Linux one must agree, because the pod seed
		// hashes clip ids and a differing separator would silently change pod contents.
		//
		// ⚠ This is the clip's LOCATION, not its identity (V38c). Intake files clips as
		// `a3/f9/<hash>.mp4`, so a well-formed clip folder yields exactly that shape here and
		// `ClipID` below recovers the identity from the bytes.
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			skipped++
			return nil
		}
		rel = filepath.ToSlash(rel)

		pr, probeErr := probe(ctx, path)
		if probeErr != nil || pr.DurationMs <= 0 {
			// No usable duration means the pod assembler cannot place it: it fills a break to
			// a target length, so a zero-duration clip would either be skipped downstream or
			// break the arithmetic. Rejecting here keeps that invariant at the boundary.
			skipped++
			return nil
		}
		// ⚠ **The quality gate (§10 V40).** These are REJECTS, not eligibility filters: the file
		// never becomes a catalog row, so nothing downstream has to know about it.
		//
		// The duration floor exists because `> 0` above was the only guard, and a 2.9KB /
		// **33-millisecond** truncated download passed it — then sat filed and airable in the dev
		// catalog. "Has a readable duration" and "is a usable clip" are different questions.
		//
		// Missing streams are the other hard failures: a video-only file plays as dead air in the
		// middle of a break, while an audio-only file gives a channel nothing to show. (Loudness is
		// NOT judged here — a quiet clip is normalisation's problem at playout, not a reject.)
		if minMs > 0 && pr.DurationMs < minMs {
			skipped++
			return nil
		}
		if pr.Silent {
			skipped++
			return nil
		}
		if pr.NoVideo {
			skipped++
			return nil
		}

		// ⚠ Two names, deliberately (§10 V44). `heuristicName` is what the era/kind filename rules
		// read — it must be the FILENAME (or `originalName`), because §8 grounds an era only when the
		// year appears in a text signal and the filename is one. `name` is what a HUMAN sees in the
		// guide, and it prefers the sidecar's clean `title` over the filename.
		//
		// The file is now `a3f9….mp4`, so both fall back through the sidecar. The display preference
		// order is: sidecar `title` → `originalName` → the bare filename. This fixes the doubled
		// display the Archive downloader's collision-avoidance produced: it files clips as
		// `"<archive-id> - <title>.mp4"`, so `originalName` reads "CampbellsSoupAdvert - Campbell's
		// Soup Advert" while the `title` field carries just "Campbell's Soup Advert".
		heuristicName := strings.TrimSuffix(filepath.Base(rel), filepath.Ext(rel))
		id := ""
		tags, sidecarState := ReadSidecarTagsState(path)
		if sidecarState == SidecarValid {
			if tags.OriginalName != "" {
				heuristicName = strings.TrimSuffix(tags.OriginalName, filepath.Ext(tags.OriginalName))
			}
		}
		// Display name: the clean sidecar title when present, else the heuristic (filename) name —
		// so a clip that lost its sidecar stays readable rather than showing a hash.
		name := heuristicName
		if title := SidecarTitle(path); title != "" {
			name = title
		}
		// Identity from the bytes. A file we cannot hash cannot be catalogued — it has no id —
		// which is the same call the probe above makes about a file with no duration.
		if id, relErr = ClipID(path); relErr != nil {
			skipped++
			return nil
		}
		parentHash := ""
		lineageInvalid := false
		conditioningHold := sidecarState == SidecarInvalid || tags.SupersededByHash != "" ||
			tags.ConditioningPublication != nil ||
			(tags.Conditioning != nil && tags.ConditioningLineage == nil)
		if tags.ConditioningLineage != nil {
			parentHash = tags.ConditioningLineage.ParentHash
			lineageInvalid = !validConditioningLineage(tags.ConditioningLineage, parentHash)
			if !lineageInvalid {
				if tags.Mezzanine == "" {
					lineageInvalid = tags.ConditioningLineage.ChildHash != id
					conditioningHold = !lineageInvalid
				} else {
					lineageInvalid = tags.Conditioning == nil || tags.MediaQuality == nil ||
						validateConditioningPair(*tags.Conditioning, tags.ConditioningLineage.ChildHash, id) != nil ||
						mediatools.ValidateMediaQualityEvidence(*tags.MediaQuality) != nil ||
						!reflect.DeepEqual(*tags.MediaQuality, tags.Conditioning.AfterRewrite.Quality)
				}
			}
		}

		kind := KindFromName(heuristicName)
		if tags.Kind != "" && validKind(Kind(tags.Kind)) {
			kind = Kind(tags.Kind)
		}
		clips = append(clips, RawClip{
			ID:               id,
			Path:             rel,
			Name:             name,
			Source:           tags.SourceID,
			ParentHash:       parentHash,
			LineageInvalid:   lineageInvalid,
			ConditioningHold: conditioningHold,
			SidecarInvalid:   sidecarState == SidecarInvalid,
			// Kind + Era from the HEURISTIC name (the filename), NOT the display name — the cheapest
			// tagging tier (§10). ⚠ Must be `heuristicName`: the display `name` now prefers the
			// sidecar title, which often drops the year ("Campbell's Soup Advert" has no 1993), and
			// §8 grounds an era only when the year is literally in the text. Reading Era off the
			// clean title would silently lose filename-encoded eras. Without this a clip lands as a
			// generic interstitial the pod assembler can never place, so filler would silently never
			// build unless AI tagging is on.
			Kind:            kind,
			Era:             EraFromName(heuristicName),
			GeographicScope: GeographicScope(tags.GeographicScope),
			Country:         tags.Country, Market: tags.Market, Network: tags.Network,
			Station: tags.Station, AirDate: tags.AirDate, GeoEvidence: tags.GeoEvidence,
			DurationMs: pr.DurationMs,
			// Quality is DERIVED, not probed: a clip with no video stream (or an
			// unreadable one) simply has none, and the guide omits the badge rather
			// than claiming a resolution nothing measured.
			Quality: QualityFromHeight(pr.Height),
			// The licence the source declared, from the info-JSON beside the file (V33).
			//
			// ⚠ Read by EXACT PATH here, unlike the tagger's `sidecarText`, which walks the
			// drop-folder matching normalized basenames. That indirection exists because
			// Tunarr supplies a display NAME that may not match the file; the scan is
			// walking the filesystem itself and already holds the real path, so guessing
			// would be strictly worse. Same sidecar, different amount of information.
			License: licenseBeside(path),
		})
		return nil
	})
	if walkErr != nil {
		// The directory itself is unreadable or missing. That IS worth reporting: it is
		// almost always a misconfigured FILLER_DIR, and silently returning an empty catalog
		// would present as "filler mysteriously does nothing".
		return nil, skipped, fmt.Errorf("scan filler dir %s: %w", dir, walkErr)
	}
	return clips, skipped, nil
}

// FFprobe returns a media file's duration and video height.
//
// JSON output rather than the `-show_entries … -of csv` form: csv silently yields "N/A" for a
// file with no duration metadata, which parses to 0 and is indistinguishable from a real
// zero-length result. JSON gives an absent field we can tell apart.
func FFprobe(ctx context.Context, path string) (Probed, error) {
	return ffprobeWith(ctx, "ffprobe", path)
}

// FFprobeNextTo returns a Prober using the ffprobe that sits ALONGSIDE the given ffmpeg
// binary.
//
// It takes the ffmpeg path deliberately, because that is the setting operators actually have
// (`playout.ffmpeg_path`), and derives ffprobe from it: the two ship together, so an operator
// who moved one moved both. Adding a second path setting would be a knob whose only correct
// value is derivable from the first.
//
// ⚠ It takes the FFMPEG path and returns an FFPROBE prober, which is worth stating loudly: an
// earlier version was named FFprobeDurationUsing(bin) and was handed `playout.ffmpeg_path` at
// the call site. That ran `ffmpeg -show_entries format=duration -of json`, which is not a valid
// ffmpeg invocation, so EVERY probe failed — and because ScanDir skips unprobeable files by
// design, the catalog came back silently empty with no error anywhere. The name now says which
// binary it wants.
func FFprobeNextTo(ffmpegPath string) Prober {
	bin := FFprobePathNextTo(ffmpegPath)
	return func(ctx context.Context, path string) (Probed, error) {
		return ffprobeWith(ctx, bin, path)
	}
}

// FFprobePathNextTo derives the ffprobe BINARY PATH from the ffmpeg path (the
// setting operators actually have — `playout.ffmpeg_path`): the two ship
// together, so an operator who moved one moved both. Splitting (V34) execs
// ffprobe directly, which is why the derivation is exported rather than buried
// in the prober above.
func FFprobePathNextTo(ffmpegPath string) string {
	return mediatools.FFprobePathNextTo(ffmpegPath)
}

func ffprobeWith(ctx context.Context, bin, path string) (Probed, error) {
	// Duration comes from -show_format, not from a stream: container duration is what a pod
	// needs (how long the clip occupies a break), and a stream's duration can differ from it —
	// an audio stream running a few frames past the video is normal and would give a subtly
	// wrong answer. Height necessarily comes from the streams, hence both sections.
	out, err := exec.CommandContext(ctx, bin,
		"-v", "error",
		"-show_entries", "format=duration:stream=width,height,codec_type,avg_frame_rate,sample_aspect_ratio,display_aspect_ratio,field_order,start_time,duration",
		"-of", "json",
		path,
	).Output()
	if err != nil {
		return Probed{}, fmt.Errorf("ffprobe %s: %w", filepath.Base(path), err)
	}

	var probed struct {
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
		Streams []struct {
			Width              int    `json:"width"`
			Height             int    `json:"height"`
			CodecType          string `json:"codec_type"`
			AverageFrameRate   string `json:"avg_frame_rate"`
			SampleAspectRatio  string `json:"sample_aspect_ratio"`
			DisplayAspectRatio string `json:"display_aspect_ratio"`
			FieldOrder         string `json:"field_order"`
			StartTime          string `json:"start_time"`
			Duration           string `json:"duration"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(out, &probed); err != nil {
		return Probed{}, fmt.Errorf("ffprobe %s: parse output: %w", filepath.Base(path), err)
	}
	if probed.Format.Duration == "" {
		return Probed{}, fmt.Errorf("ffprobe %s: no duration in the container", filepath.Base(path))
	}
	secs, err := strconv.ParseFloat(probed.Format.Duration, 64)
	if err != nil {
		return Probed{}, fmt.Errorf("ffprobe %s: duration %q: %w", filepath.Base(path), probed.Format.Duration, err)
	}
	if secs <= 0 {
		return Probed{}, fmt.Errorf("ffprobe %s: duration %v is not positive", filepath.Base(path), secs)
	}

	// The VIDEO stream's height specifically. Taking streams[0] would read the audio track on
	// any file that lists audio first — which reports height 0 and silently downgrades a 1080p
	// clip to "no quality". An audio-only clip legitimately has none.
	// ⚠ No `break` on the video match any more: the loop now answers TWO questions, and stopping
	// at the first video stream would report `HasAudio: false` for every file that happens to
	// list its video track first — which is most of them. The whole stream list is cheap.
	result := Probed{DurationMs: int64(secs * 1000)}
	hasAudio, hasVideo, selectedAudio, selectedVideo := false, false, false, false
	for _, s := range probed.Streams {
		if s.CodecType == "video" {
			hasVideo = true
			if !selectedVideo {
				selectedVideo = true
				result.Width, result.Height = s.Width, s.Height
				result.Cadence = observedFFprobeValue(s.AverageFrameRate)
				result.SampleAspect = observedFFprobeValue(s.SampleAspectRatio)
				result.DisplayAspect = observedFFprobeValue(s.DisplayAspectRatio)
				result.FieldOrder = observedFFprobeValue(s.FieldOrder)
				result.VideoStartMs, result.VideoDurationMs, result.VideoTimingKnown = parseStreamTiming(s.StartTime, s.Duration)
			}
		}
		if s.CodecType == "audio" {
			hasAudio = true
			if !selectedAudio {
				selectedAudio = true
				result.AudioStartMs, result.AudioDurationMs, result.AudioTimingKnown = parseStreamTiming(s.StartTime, s.Duration)
			}
		}
	}
	// A missing height is not a probe error, because the caller owns the policy decision. ScanDir
	// and ProbeStage both reject it as an audio-only file; keeping that decision out of FFprobe
	// also lets callers distinguish "valid media with no video" from "could not inspect media".
	result.Silent, result.NoVideo = !hasAudio, !hasVideo
	return result, nil
}

func observedFFprobeValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.EqualFold(value, "n/a") || value == "0/0" {
		return ""
	}
	return value
}

func parseStreamTiming(start, duration string) (int64, int64, bool) {
	startSeconds, startErr := strconv.ParseFloat(strings.TrimSpace(start), 64)
	durationSeconds, durationErr := strconv.ParseFloat(strings.TrimSpace(duration), 64)
	if startErr != nil || durationErr != nil || durationSeconds <= 0 {
		return 0, 0, false
	}
	return int64(startSeconds * 1000), int64(durationSeconds * 1000), true
}

// ShardDepth is how many 2-character directory levels a clip is filed under.
//
// ⚠ Sharding rather than one flat directory (§10 V38c): a catalog is thousands of clips, and a
// single directory holding them all degrades badly on several filesystems and makes `ls`
// unusable. Two levels of 2 hex characters is 65,536 buckets, so even a 50k-clip catalog averages
// under one file per directory. Same trick git and Docker use for content-addressed stores.
const ShardDepth = 2

// hexClipID matches the identity ClipID produces: 64 lowercase hex characters.
var hexClipID = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ClipRelPath is a clip's location under the clip folder: `a3/f9/a3f9….mp4`.
//
// Derived from the id rather than stored, so there is one rule for where a clip lives and no way
// for a recorded path to disagree with it.
func ClipRelPath(clipID, ext string) string {
	parts := make([]string, 0, ShardDepth+1)
	for i := 0; i < ShardDepth; i++ {
		parts = append(parts, clipID[i*2:i*2+2])
	}
	return filepath.Join(append(parts, clipID+strings.ToLower(ext))...)
}

// ClipPath resolves a clip's identity to an absolute path, given the clip folder.
//
// ⚠ THIS IS A SECURITY BOUNDARY, not just a join. A clip id reaches here from the database, and
// the pod assembler and the API both accept ids from callers — so a crafted id containing `../`
// would otherwise make playout read (and stream) an arbitrary file outside the clip folder.
//
// ⚠ **Under hash identity the guard gets STRONGER, not weaker** (V38c). It used to sanitise an
// operator-shaped relative path and verify containment after cleaning. An id is now a 64-character
// hex hash, so the check is an ALLOW-LIST on the alphabet: nothing containing a separator, a dot
// or an encoding trick can match `^[0-9a-f]{64}$` in the first place. Validating what an id may
// BE beats reasoning about what a path might become.
//
// ⚠ Accepts EITHER a bare clip id or the shard-relative path a clip row stores
// (`a3/f9/a3f9….mp4`). Consumers hold the row and pass its `Path`; intake holds only the fresh
// hash. Both resolve here so there is ONE containment check rather than one per caller — the
// property that made this function a security boundary in the first place.
func ClipPath(dir, clipRef, ext string) (string, error) {
	if dir == "" {
		return "", fmt.Errorf("filler: no clip folder configured")
	}
	rel := clipRef
	if hexClipID.MatchString(clipRef) {
		rel = ClipRelPath(clipRef, ext)
	} else if !validShardPath(clipRef) {
		// Not a hash, and not a shard path built from one. Covers empty, absolute, traversal and
		// every encoding of them: an id that is not a hash is not an id.
		return "", fmt.Errorf("filler: %q is not a clip id", clipRef)
	}
	return filepath.Join(filepath.Clean(dir), filepath.FromSlash(rel)), nil
}

// shardPath matches `a3/f9/<64 hex>.<ext>` — what a clip row stores.
var shardPath = regexp.MustCompile(`^[0-9a-f]{2}/[0-9a-f]{2}/[0-9a-f]{64}\.[a-z0-9]{1,5}$`)

// validShardPath checks a stored clip path against the shape intake produces.
//
// ⚠ An ALLOW-LIST on the whole string, not a traversal check on its parts. Every character of a
// legitimate path is hex, a slash or an extension, so nothing containing `..`, a backslash or a
// percent-encoding can match — which is a stronger guarantee than cleaning a path and hoping the
// result stayed inside.
func validShardPath(p string) bool { return shardPath.MatchString(p) }

// DirSource is the FillerSource that scans FILLER_DIR directly (§9.1).
//
// It optionally consults Tunarr to learn each clip's program uuid, so Tunarr-backed channels
// can still build filler-lists. THE SCAN IS THE SOURCE OF TRUTH for what exists — Tunarr only
// annotates. That ordering is the fix: previously Tunarr's knowledge determined the catalog, so
// no Tunarr meant no clips at all.
type DirSource struct {
	// Layout is the immutable clip/watch topology for this application generation. Scan excludes
	// the applied watch subtree, so a delayed arrival is never catalogued before intake files it.
	Layout Layout
	// Probe reads durations; nil ⇒ FFprobeDuration.
	Probe Prober
	// Tunarr, when non-nil, supplies program uuids for clips it knows. Nil is a fully
	// supported configuration — an install with no Tunarr — not a degraded one.
	Tunarr TunarrClipSource
	// Artwork renders a clip's still AND its animated hover preview (V28/V39); nil ⇒
	// FFmpegArtwork("ffmpeg"). A separate seam from Probe because it is a separate binary and a
	// separate exec — reading metadata is ffprobe, rendering frames is ffmpeg.
	//
	// ⚠ ONE seam for BOTH assets, because it is one ffmpeg invocation: the two outputs come from
	// a single decode. Two seams would let a caller render them separately and quietly
	// reintroduce the second decode that merging them removed.
	Artwork ArtworkRenderer
	// MinDuration is the quality gate's floor (§10 V40, `filler.min_duration`). A file shorter
	// than this is rejected at the scan boundary and never becomes a clip.
	//
	// Read through a func because policy remains live even though storage topology does not.
	// nil or 0 ⇒ no floor.
	MinDuration func() time.Duration
	// Log records how many clips could not have their artwork generated. Optional, but the reason it
	// exists is not cosmetic: extraction is best-effort and failures are skipped, which is
	// exactly the shape that once let a misconfigured binary produce a silently empty catalog
	// (see FFprobeNextTo). A count in the log makes "ffmpeg is wrong" legible.
	Log func(msg string, args ...any)
}

// TunarrClipSource is the narrow slice of Tunarr the scan uses: register the drop-folder as a
// `local` media source, and report the program ids it assigned.
type TunarrClipSource interface {
	EnsureLocalSource(ctx context.Context, dir string) error
	// LocalClipIDsByName maps a clip's file NAME to its Tunarr program uuid.
	//
	// By name rather than by path because that is all Tunarr's scan reports back, and it is
	// the only join key available. Imperfect: two clips with the same basename in different
	// subfolders collide, so one may get the other's uuid. That is tolerable precisely because
	// the uuid is no longer identity — a wrong uuid degrades a Tunarr filler-list, it cannot
	// corrupt the catalog or internal playout.
	LocalClipIDsByName(ctx context.Context) (map[string]string, error)
}

// EnsureLocalSource registers the drop-folder with Tunarr when one is configured.
//
// Best-effort by design: with no Tunarr there is nothing to register, and that is not an
// error — it is the configuration §9.1 exists to support.
func (d DirSource) EnsureLocalSource(ctx context.Context, dir string) error {
	if d.Tunarr == nil {
		return nil
	}
	return d.Tunarr.EnsureLocalSource(ctx, dir)
}

// ListLocalClips scans FILLER_DIR, then annotates with Tunarr uuids where available.
func (d DirSource) ListLocalClips(ctx context.Context) ([]RawClip, error) {
	// The floor is read HERE rather than captured at wiring, so `filler.min_duration` hot-applies
	// like every other setting the scan consults.
	var minMs int64
	if d.MinDuration != nil {
		minMs = d.MinDuration().Milliseconds()
	}
	clips, rejected, err := scanDir(ctx, d.Layout.ClipDir(), d.Layout.WatchDir(), d.Probe, minMs)
	if err != nil {
		return nil, err
	}
	// ⚠ Rejections are LOGGED, not silent. A quality gate that quietly drops files is
	// indistinguishable from a scan that never saw them — the same silence that once let a
	// misconfigured ffmpeg produce an empty catalog with no error anywhere (see FFprobeNextTo).
	// An operator who wonders where a clip went gets an answer here.
	if rejected > 0 && d.Log != nil {
		d.Log("filler: some files were rejected at the scan boundary",
			"rejected", rejected, "catalogued", len(clips),
			"hint", "unreadable, shorter than filler.min_duration, or missing an audio/video stream")
	}
	// Thumbnails BEFORE the Tunarr annotation, and independent of it: the images are for the
	// catalog UI and have nothing to do with whether Tunarr is reachable.
	if failed := GenerateArtwork(ctx, d.Layout.ClipDir(), clips, d.Artwork); failed > 0 && d.Log != nil {
		d.Log("filler: some clip artwork could not be generated",
			"failed", failed, "of", len(clips),
			"hint", "check playout.ffmpeg_path — a wrong binary fails every render, and the "+
				"animation needs libwebp compiled in (ffmpeg -encoders | grep webp)")
	}

	if d.Tunarr == nil || len(clips) == 0 {
		return clips, nil
	}

	// Tunarr's uuids are a BONUS, so a failure here must not fail the scan: the catalog is
	// already complete and internal playout needs nothing more. Only Tunarr-backed channels
	// lose anything, and they lose it until the next sync rather than permanently.
	byName, err := d.Tunarr.LocalClipIDsByName(ctx)
	if err != nil {
		return clips, nil //nolint:nilerr // deliberate: annotation is optional, see above
	}
	for i := range clips {
		if id, ok := byName[clips[i].Name]; ok {
			clips[i].TunarrProgramID = id
		}
	}
	return clips, nil
}

// licenseBeside reads the licence URL from the info-JSON sitting next to a clip file.
//
// A direct os.ReadFile rather than SidecarLicense's fs.FS: ScanDir walks real paths and already
// holds this clip's, so there is nothing to resolve. The fs.FS variant exists for the tagger,
// which is handed a drop-folder FS and a possibly-renamed display name.
//
// Every failure is "" — no sidecar (the normal case for a hand-copied clip), unreadable, or
// malformed. A licence we cannot read is UNKNOWN, and unknown is the honest default; a scan must
// never fail over a missing optional file.
func licenseBeside(mediaPath string) string {
	raw, err := os.ReadFile(sidecarPathFor(mediaPath))
	if err != nil {
		return ""
	}
	var info sidecarInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		return ""
	}
	return strings.TrimSpace(info.License)
}
