package playout

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/loomarr/loomarr/internal/diagnostics"
	"github.com/loomarr/loomarr/internal/prepared"
)

// PreparedAiring binds one immutable source rendition to its authoritative place on a Channel's
// wall clock. Offset is meaningful for Current; previous Airings contribute their trailing media.
type PreparedAiring struct {
	Specification prepared.Specification
	StartedAt     time.Time
	Offset        time.Duration
	// Identity is the scheduler-owned boundary carried by the raw MPEG-TS adapter. HLS derives
	// its wall clock from StartedAt; raw delivery also needs the exact end and correlation ids.
	Identity AiringIdentity
	// DiscontinuitySequence is how many programme boundaries have already scrolled out of this
	// Channel's rendered window — the EXT-X-DISCONTINUITY-SEQUENCE of the FIRST segment rendered
	// from this Airing (RFC 8216 §4.3.3.3).
	//
	// It is stamped by the resolver, not computed by the renderer: the renderer only ever sees the
	// Airings still inside the window, so it cannot count the ones that already aged out. Zero is
	// the honest default — a Channel whose history is unknown renders a window that simply starts
	// counting at zero, which is stable for as long as that window lives.
	DiscontinuitySequence int64
}

// PreparedWindow is the live edge plus the preceding Airings needed to render the shared DVR
// horizon across programme boundaries. Previous is chronological, oldest first.
type PreparedWindow struct {
	Previous []PreparedAiring
	Current  PreparedAiring
}

// PreparedResolver maps a tune request onto the authoritative Airings and prepared identities. It
// is implemented at composition, where the accepted schedule and source catalogue already meet.
type PreparedResolver interface {
	ResolvePrepared(context.Context, TuneRequest) (PreparedWindow, bool, error)
}

// PreparedOrigin renders short live HLS manifests over immutable shared publications. It owns no
// encoder and cannot create media; absent or invalid current publications are misses for Origin's
// live fallback.
type PreparedOrigin struct {
	library  *prepared.Library
	resolver PreparedResolver
}

func newPreparedOrigin(library *prepared.Library, resolver PreparedResolver) *PreparedOrigin {
	return &PreparedOrigin{library: library, resolver: resolver}
}

// NewPreparedOrigin creates the prepared implementation consumed by Origin. Construction does not
// start background work: publications are produced by the separate readiness control plane.
func NewPreparedOrigin(library *prepared.Library, resolver PreparedResolver) *PreparedOrigin {
	return newPreparedOrigin(library, resolver)
}

type preparedBlockStarter func(context.Context, []string, diagnostics.ProcessSpec) (*Process, error)

// MPEGTSBlockSource adapts immutable fMP4 prepared media into finite MPEG-TS blocks without
// decoding or encoding. The application composes it ahead of the ordinary live source inside the
// existing shared Manager session.
func (o *PreparedOrigin) MPEGTSBlockSource(
	ffmpeg string, log *slog.Logger, manager *diagnostics.ProcessManager,
) BlockSource {
	ffmpeg = strings.TrimSpace(ffmpeg)
	if ffmpeg == "" {
		ffmpeg = "ffmpeg"
	}
	return newPreparedMPEGTSBlockSource(o, func(
		ctx context.Context, args []string, spec diagnostics.ProcessSpec,
	) (*Process, error) {
		return StartObserved(ctx, ffmpeg, args, log, nil, manager, spec)
	})
}

// MPEGTSReady is the lookup-only admission proof for a copy-only prepared cold start. It opens no
// original source and starts no process; a miss or malformed boundary keeps conservative admission.
func (o *PreparedOrigin) MPEGTSReady(ctx context.Context, channelID string, plan EncodePlan) (bool, error) {
	_, ready, err := o.resolveMPEGTSBlock(ctx, channelID, plan)
	return ready, err
}

type preparedMPEGTSBlock struct {
	media    preparedMedia
	format   BroadcastFormat
	identity AiringIdentity
	limit    time.Duration
}

// resolveMPEGTSBlock is the one admission definition shared by the lookup-only cost proof and the
// process-opening source. Keeping publication, format, identity, and remaining-boundary validation
// together prevents a session being admitted cheaply under rules the actual block later rejects.
func (o *PreparedOrigin) resolveMPEGTSBlock(
	ctx context.Context, channelID string, plan EncodePlan,
) (preparedMPEGTSBlock, bool, error) {
	if o == nil || o.library == nil || o.resolver == nil {
		return preparedMPEGTSBlock{}, false, nil
	}
	window, ok, err := o.resolver.ResolvePrepared(ctx, TuneRequest{
		ChannelID: channelID, Plan: plan, Delivery: DeliveryMPEGTS,
	})
	if err != nil || !ok {
		return preparedMPEGTSBlock{}, false, err
	}
	media, ok, err := o.load(window.Current)
	if err != nil || !ok {
		return preparedMPEGTSBlock{}, false, err
	}
	format, ok := preparedBroadcastFormat(window.Current.Specification.Rendition)
	identity := window.Current.Identity
	limit := identity.EndsAt.Sub(identity.StartedAt) - window.Current.Offset
	if !ok || identity.StartedAt.IsZero() || !identity.EndsAt.After(identity.StartedAt) ||
		!identity.StartedAt.Equal(window.Current.StartedAt) || window.Current.Offset < 0 ||
		identity.ContentID == "" || identity.ScheduleBlockID == "" || limit <= 0 {
		return preparedMPEGTSBlock{}, false, nil
	}
	return preparedMPEGTSBlock{media: media, format: format, identity: identity, limit: limit}, true, nil
}

func newPreparedMPEGTSBlockSource(o *PreparedOrigin, start preparedBlockStarter) BlockSource {
	return func(ctx context.Context, channelID string, plan EncodePlan) (Block, error) {
		if start == nil {
			return Block{}, ErrPreparedUnavailable
		}
		resolved, ok, err := o.resolveMPEGTSBlock(ctx, channelID, plan)
		if err != nil {
			return Block{}, err
		}
		if !ok {
			return Block{}, ErrPreparedUnavailable
		}
		args := ProgramArgs(ProgramSpec{
			Profile: Profile{
				Width: resolved.format.Width, Height: resolved.format.Height, Framerate: resolved.format.Framerate,
				VideoBitrate: resolved.format.VideoBitrate, AudioBitrate: resolved.format.AudioBitrate,
			},
			Input: resolved.media.manifestPath, Offset: resolved.media.airing.Offset, Limit: resolved.limit,
			Plan: CopyPlan{CopyVideo: true, CopyAudio: true}, UnpacedInput: true,
		})
		diagnosticArgs := append([]string(nil), args...)
		for index := range diagnosticArgs {
			if diagnosticArgs[index] == resolved.media.manifestPath {
				diagnosticArgs[index] = "[prepared-manifest]"
			}
		}
		proc, err := start(ctx, args, diagnostics.ProcessSpec{
			Purpose: "playout_prepared_remux", ChannelID: channelID, Target: plan.String(),
			ScheduleBlockID: resolved.identity.ScheduleBlockID, Args: diagnosticArgs,
		})
		if err != nil {
			return Block{}, err
		}
		if proc == nil || proc.Stdout == nil {
			if proc != nil {
				proc.Stop()
			}
			return Block{}, fmt.Errorf("playout: prepared remux started without output")
		}
		return Block{
			Content:  &processBlockContent{reader: proc.Stdout, process: proc},
			Identity: resolved.identity, Format: resolved.format,
		}, nil
	}
}

func preparedBroadcastFormat(r prepared.RenditionContract) (BroadcastFormat, bool) {
	codec := strings.ToLower(strings.TrimSpace(r.VideoCodec))
	if codec != "h264" && !IsHEVCCodec(codec) {
		return BroadcastFormat{}, false
	}
	if !strings.EqualFold(strings.TrimSpace(r.AudioCodec), "aac") ||
		(strings.TrimSpace(r.AudioLayout) != "" && !strings.EqualFold(strings.TrimSpace(r.AudioLayout), "stereo")) {
		return BroadcastFormat{}, false
	}
	format := BroadcastFormat{
		VideoCodec: codec, Width: r.Width, Height: r.Height, Framerate: r.FrameRate,
		VideoBitrate: r.VideoBitrateKbps, AudioBitrate: r.AudioBitrateKbps,
	}
	parsed, ok := ParseBroadcastFormat(format.String())
	return parsed, ok
}

type processBlockContent struct {
	reader  io.ReadCloser
	process *Process
	once    sync.Once
	err     error
}

func (c *processBlockContent) Read(p []byte) (int, error) { return c.reader.Read(p) }

func (c *processBlockContent) Close() error {
	c.once.Do(func() {
		c.err = c.reader.Close()
		c.process.Stop()
	})
	return c.err
}

func (o *PreparedOrigin) Tune(ctx context.Context, request TuneRequest) (Presentation, bool, error) {
	if request.Delivery != DeliveryHLS || o == nil || o.library == nil || o.resolver == nil {
		return Presentation{}, false, nil
	}
	window, ok, err := o.resolver.ResolvePrepared(ctx, request)
	if err != nil || !ok {
		return Presentation{}, false, err
	}
	current, ok, err := o.load(window.Current)
	if err != nil || !ok {
		return Presentation{}, false, err
	}
	previous := make([]preparedMedia, 0, len(window.Previous))
	for _, airing := range window.Previous {
		media, hit, loadErr := o.load(airing)
		if loadErr != nil || !hit {
			continue
		}
		previous = append(previous, media)
	}
	manifest, err := renderPreparedManifest(current, previous)
	if err != nil {
		return Presentation{}, false, err
	}
	return Presentation{Manifest: manifest, Release: func() {}}, true, nil
}

func (o *PreparedOrigin) load(airing PreparedAiring) (preparedMedia, bool, error) {
	pub, ok, err := o.library.Lookup(airing.Specification)
	if err != nil || !ok {
		return preparedMedia{}, false, err
	}
	asset, ok, err := o.library.Open(pub.Key, prepared.MediaManifestName)
	if err != nil || !ok {
		if err == nil {
			err = prepared.ErrIncomplete
		}
		return preparedMedia{}, false, err
	}
	body, readErr := io.ReadAll(asset.Content)
	closeErr := asset.Content.Close()
	if readErr != nil {
		return preparedMedia{}, false, fmt.Errorf("playout: read prepared manifest: %w", readErr)
	}
	if closeErr != nil {
		return preparedMedia{}, false, fmt.Errorf("playout: close prepared manifest: %w", closeErr)
	}
	media, err := parsePreparedManifest(body, pub.Key, pub.Files, airing)
	media.manifestPath = filepath.Join(pub.Directory, prepared.MediaManifestName)
	return media, err == nil, err
}

// OpenAsset opens a publication-keyed URI emitted by Tune. The key binds a follow-up request to
// the immutable publication that authored its manifest, even across an Airing boundary.
func (o *PreparedOrigin) OpenAsset(_ string, _ EncodePlan, rel string) (Asset, bool, error) {
	if o == nil || o.library == nil {
		return Asset{}, false, nil
	}
	key, name, ok := parsePreparedAssetToken(rel)
	if !ok {
		return Asset{}, false, nil
	}
	asset, ok, err := o.library.Open(key, name)
	if err != nil || !ok {
		return Asset{}, ok, err
	}
	return Asset{Content: asset.Content, Modified: asset.Modified, Immutable: true}, true, nil
}

type preparedSegment struct {
	duration time.Duration
	extinf   string
	uri      string
	startsAt time.Time
}

type preparedMedia struct {
	airing       PreparedAiring
	key          string
	manifestPath string
	version      string
	independent  string
	init         string
	segments     []preparedSegment
}

type preparedSegmentRef struct {
	media   preparedMedia
	segment preparedSegment
}

func parsePreparedManifest(body []byte, key string, files []string, airing PreparedAiring) (preparedMedia, error) {
	declared := make(map[string]struct{}, len(files))
	for _, name := range files {
		declared[name] = struct{}{}
	}
	lines := strings.Split(strings.ReplaceAll(string(body), "\r\n", "\n"), "\n")
	media := preparedMedia{airing: airing, key: key}
	startsAt := airing.StartedAt
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		switch {
		case strings.HasPrefix(line, "#EXT-X-VERSION:"):
			media.version = line
		case line == "#EXT-X-INDEPENDENT-SEGMENTS":
			media.independent = line
		case strings.HasPrefix(line, "#EXT-X-MAP:"):
			var err error
			media.init, err = prefixMapURI(line, key, declared)
			if err != nil {
				return preparedMedia{}, err
			}
		case strings.HasPrefix(line, "#EXTINF:"):
			raw := strings.TrimSuffix(strings.TrimPrefix(line, "#EXTINF:"), ",")
			if comma := strings.IndexByte(raw, ','); comma >= 0 {
				raw = raw[:comma]
			}
			seconds, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
			if err != nil || seconds <= 0 {
				return preparedMedia{}, fmt.Errorf("playout: invalid prepared EXTINF %q", line)
			}
			if i+1 >= len(lines) || strings.TrimSpace(lines[i+1]) == "" || strings.HasPrefix(strings.TrimSpace(lines[i+1]), "#") {
				return preparedMedia{}, errors.New("playout: prepared EXTINF has no media URI")
			}
			i++
			uri, err := preparedAssetURI(strings.TrimSpace(lines[i]), key, declared)
			if err != nil {
				return preparedMedia{}, err
			}
			duration := time.Duration(seconds * float64(time.Second))
			media.segments = append(media.segments, preparedSegment{
				duration: duration, extinf: line, uri: uri, startsAt: startsAt,
			})
			startsAt = startsAt.Add(duration)
		}
	}
	if media.init == "" || len(media.segments) == 0 {
		return preparedMedia{}, errors.New("playout: prepared manifest has no initialisation or media")
	}
	return media, nil
}

func renderPreparedManifest(current preparedMedia, previous []preparedMedia) ([]byte, error) {
	offset := current.airing.Offset
	if offset < 0 {
		offset = 0
	}
	index := 0
	for index < len(current.segments) && offset >= current.segments[index].duration {
		offset -= current.segments[index].duration
		index++
	}
	if index == len(current.segments) {
		return nil, errors.New("playout: prepared window is beyond its media")
	}

	cutoff := current.airing.StartedAt.Add(current.airing.Offset).Add(-DVRHorizon)
	refs := make([]preparedSegmentRef, 0, index+1)
	for i := index; i >= 0; i-- {
		segment := current.segments[i]
		if !segment.startsAt.Add(segment.duration).After(cutoff) {
			break
		}
		refs = append(refs, preparedSegmentRef{media: current, segment: segment})
	}
	for p := len(previous) - 1; p >= 0; p-- {
		for i := len(previous[p].segments) - 1; i >= 0; i-- {
			segment := previous[p].segments[i]
			if !segment.startsAt.Add(segment.duration).After(cutoff) {
				break
			}
			refs = append(refs, preparedSegmentRef{media: previous[p], segment: segment})
		}
	}
	reversePrepared(refs)

	longest := time.Duration(0)
	for _, ref := range refs {
		if ref.segment.duration > longest {
			longest = ref.segment.duration
		}
	}
	cadence := refs[0].media.airing.Specification.Rendition.SegmentDurationMS
	sequence := int64(0)
	if cadence > 0 {
		sequence = refs[0].segment.startsAt.UnixMilli() / int64(cadence)
	}
	if sequence < 0 {
		sequence = 0
	}

	// EXT-X-DISCONTINUITY-SEQUENCE counts the boundaries that have already aged OUT of the window,
	// so it must be derived from the same wall clock MEDIA-SEQUENCE is — never from a counter held
	// across requests. Every tune is stateless and any two callers asking at the same instant must
	// get the same manifest, which a mutable counter cannot guarantee.
	//
	// ⚠ REQUIRED, not cosmetic (RFC 8216 §4.3.3.3). A sliding window drops a discontinuity from its
	// head roughly once per programme; without this tag a player cannot correlate discontinuity
	// indices between two reloads. hls.js tolerates the omission because it dead-reckons from
	// EXT-X-PROGRAM-DATE-TIME, which is why the browser gates never caught this — ExoPlayer and
	// AVPlayer do not, and resynchronise the decoder instead.
	//
	// The count is carried on the Airing rather than recomputed here: this renderer only ever sees
	// the airings still INSIDE the window, so it cannot count what already scrolled past. The
	// resolver walks the accepted schedule and is the only layer that knows a Channel's programme
	// history, so it stamps the boundary ordinal it already has.
	discontinuities := refs[0].media.airing.DiscontinuitySequence
	if discontinuities < 0 {
		discontinuities = 0
	}
	out := []string{
		"#EXTM3U", current.version,
		fmt.Sprintf("#EXT-X-TARGETDURATION:%d", int(math.Ceil(longest.Seconds()))),
		fmt.Sprintf("#EXT-X-MEDIA-SEQUENCE:%d", sequence),
	}
	if discontinuities > 0 {
		out = append(out, fmt.Sprintf("#EXT-X-DISCONTINUITY-SEQUENCE:%d", discontinuities))
	}
	if current.independent != "" {
		out = append(out, current.independent)
	}
	previousBoundary := ""
	for _, ref := range refs {
		boundary := fmt.Sprintf("%s/%d", ref.media.key, ref.media.airing.StartedAt.UnixNano())
		startsBoundary := boundary != previousBoundary
		if startsBoundary {
			if previousBoundary != "" {
				out = append(out, "#EXT-X-DISCONTINUITY")
			}
			out = append(out, ref.media.init)
			previousBoundary = boundary
		}
		// A PDT at every boundary, not one at the head. Wall clock is what maps a segment back onto
		// the Channel's timeline, and a player cannot carry that mapping ACROSS a discontinuity:
		// timestamps restart there, so everything after the first boundary would be dead-reckoned
		// from EXTINF sums alone. hls.js hides this by re-deriving from the single head PDT, but the
		// V60 time-shift readout ("1M 23S BEHIND") is wall-clock based, so on a native player it
		// drifts from the first programme change onward.
		//
		// Emitted immediately before the segment it describes — the only position it applies to.
		if startsBoundary {
			out = append(out, "#EXT-X-PROGRAM-DATE-TIME:"+ref.segment.startsAt.UTC().Format(time.RFC3339Nano))
		}
		out = append(out, ref.segment.extinf, ref.segment.uri)
	}
	return []byte(strings.Join(compactPreparedLines(out), "\n") + "\n"), nil
}

func reversePrepared[T any](values []T) {
	for left, right := 0, len(values)-1; left < right; left, right = left+1, right-1 {
		values[left], values[right] = values[right], values[left]
	}
}

func compactPreparedLines(lines []string) []string {
	out := lines[:0]
	for _, line := range lines {
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func prefixMapURI(line, key string, declared map[string]struct{}) (string, error) {
	const marker = `URI="`
	start := strings.Index(line, marker)
	if start < 0 {
		return "", errors.New("playout: prepared map has no URI")
	}
	uriStart := start + len(marker)
	end := strings.Index(line[uriStart:], `"`)
	if end < 0 {
		return "", errors.New("playout: prepared map has an unterminated URI")
	}
	uri, err := preparedAssetURI(line[uriStart:uriStart+end], key, declared)
	if err != nil {
		return "", err
	}
	return line[:uriStart] + uri + line[uriStart+end:], nil
}

func preparedAssetURI(raw, key string, declared map[string]struct{}) (string, error) {
	name := strings.TrimSpace(raw)
	clean := path.Clean(name)
	if name == "" || clean == "." || path.IsAbs(clean) || clean == ".." ||
		strings.HasPrefix(clean, "../") || strings.ContainsAny(name, `\\?#`) {
		return "", fmt.Errorf("playout: unsafe prepared asset URI %q", raw)
	}
	if _, ok := declared[clean]; !ok {
		return "", fmt.Errorf("playout: prepared manifest references undeclared asset %q", clean)
	}
	return preparedAssetToken(key, clean), nil
}

// Prepared HLS assets must fit the API's single `{asset}` path segment. Encoding the immutable
// publication key and its validated relative file together avoids a wildcard route (which OpenAPI
// cannot describe portably) while preserving the publication binding across airing changes.
const preparedAssetTokenPrefix = "p-"

func preparedAssetToken(key, name string) string {
	payload := key + "\x00" + name // NUL cannot occur in a filesystem path.
	// Retain only the extension outside the opaque payload so the transport adapter can emit the
	// correct HLS content type without learning how publication tokens are encoded.
	return preparedAssetTokenPrefix + base64.RawURLEncoding.EncodeToString([]byte(payload)) + path.Ext(name)
}

func parsePreparedAssetToken(token string) (key, name string, ok bool) {
	if !strings.HasPrefix(token, preparedAssetTokenPrefix) {
		return "", "", false
	}
	encoded := strings.TrimPrefix(token, preparedAssetTokenPrefix)
	extension := ""
	if dot := strings.LastIndexByte(encoded, '.'); dot >= 0 {
		extension = encoded[dot:]
		encoded = encoded[:dot]
	}
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return "", "", false
	}
	key, name, ok = strings.Cut(string(payload), "\x00")
	return key, name, ok && key != "" && name != "" && path.Ext(name) == extension
}
