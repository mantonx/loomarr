package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/loomarr/loomarr/internal/diagnostics"
	"github.com/loomarr/loomarr/internal/playout"
	"github.com/loomarr/loomarr/internal/schedule"
	"github.com/loomarr/loomarr/internal/store"
)

// GET /playout/program/{id} — "what is airing right now?" (§9.1, prior-art §1).
//
// THIS IS THE FINITE BLOCK LAYER. The Go supervisor opens this URL, receives one MPEG-TS block,
// writes it into the session's long-lived copy mux, and resolves again at EOF. Airing identity and
// the first block's pinned broadcast format travel in internal headers so the supervisor never has
// to infer a transition or decoder shape from anonymous bytes.
//
// It also means this handler is called REPEATEDLY for one channel — once per program, forever.
// It must therefore be cheap, idempotent, and above all CONSISTENT: two calls at the same
// instant must resolve to the same program at the same offset, or a channel replaying a program
// it already showed would be indistinguishable from a scheduling bug.

// PlayoutResolver answers "what should this channel play right now" and turns it into an ffmpeg
// input. Implemented by an adapter over channels.Engine + library.Client; abstracted so the api
// package need not import either.
type PlayoutResolver interface {
	// AiringNow resolves the channel's current program and the URL ffmpeg should read.
	//
	// The returned Airing carries the seek offset and the remaining duration, which together
	// make a mid-program tune-in land in the right place. An Airing that is not Playable means
	// "nothing is airing" — an empty lineup, or one where nothing has landed yet — which the
	// caller renders as the offline card rather than as an error.
	AiringNow(ctx context.Context, channelID string) (playout.Airing, string, error)
	// Profile is the encode profile to normalize this program to, resolved against measured
	// capacity and current load (§9.1 quality ladder).
	Profile(ctx context.Context) playout.Profile
	// AudioTrackFor picks which audio track to play from a source — the `N` in `-map 0:a:N`,
	// honouring the operator's preferred language (§9.1). Best-effort: 0 (the file's first
	// track) whenever the preference cannot be resolved, so a probe failure costs the language
	// and never the programme.
	AudioTrackFor(ctx context.Context, channelID, libraryItemID, streamURL string) int
	// Tracks probes the audio + subtitle tracks of the channel's CURRENTLY-AIRING program, for
	// the Watch surface's pickers (§9.1, V46). The options a viewer sees are what the airing file
	// actually carries — not a hardcoded list and not an app setting. Best-effort: empty tracks
	// when nothing is airing or the probe fails, so a UI request never blocks on a bad probe.
	Tracks(ctx context.Context, channelID string) (playout.MediaTracks, error)
	// PlanFor decides the copy/transcode plan for an input against a target (§9.1 direct play,
	// V47): copy the streams the target can play, transcode the rest. Fails safe toward transcode
	// (the zero CopyPlan) when the source cannot be probed.
	//
	// It also returns the MediaFormat the plan was derived from, so the transcode path can act on
	// what the probe already learned without paying for a second ffprobe on the program boundary.
	// Tone-mapping is the first caller; the zero value means "not probed", which every consumer
	// must treat as unknown rather than as a positive claim.
	PlanFor(ctx context.Context, input string, target playout.EncodePlan) (playout.CopyPlan, playout.MediaFormat)
	// ChannelCodec returns the persisted codec the Channel's live timeline normalizes to. It is
	// needed only to turn the broad tuner capability plan into one stable output codec.
	ChannelCodec(ctx context.Context, channelID string) string
}

// The block supervisor pins the first child's output format and returns it on every later internal
// request. These names are exported only for the composition adapter that performs that owned HTTP
// hop; device clients never set or consume them.
const (
	PlayoutBroadcastFormatQuery   = "broadcast"
	PlayoutBroadcastFormatHeader  = "X-Loomarr-Broadcast-Format"
	PlayoutAiringStartedAtHeader  = "X-Loomarr-Airing-Started-At"
	PlayoutAiringEndsAtHeader     = "X-Loomarr-Airing-Ends-At"
	PlayoutAiringKindHeader       = "X-Loomarr-Airing-Kind"
	PlayoutAiringContentHeader    = "X-Loomarr-Airing-Content"
	PlayoutScheduleBlockHeader    = "X-Loomarr-Schedule-Block"
	PlayoutParentProcessRunHeader = "X-Loomarr-Parent-Process-Run"
)

func setPlayoutAiringIdentity(header http.Header, airing playout.Airing) {
	header.Set(PlayoutAiringStartedAtHeader, airing.StartedAt.UTC().Format(time.RFC3339Nano))
	if airing.Remaining > 0 {
		endsAt := airing.StartedAt.Add(airing.Offset).Add(airing.Remaining)
		header.Set(PlayoutAiringEndsAtHeader, endsAt.UTC().Format(time.RFC3339Nano))
	}
	header.Set(PlayoutAiringKindHeader, string(airing.Kind))
	header.Set(PlayoutAiringContentHeader, airing.Identity)
	header.Set(PlayoutScheduleBlockHeader, airing.ScheduleBlockID)
}

// ParsePlayoutAiringIdentity is the composition adapter's inverse of setPlayoutAiringIdentity.
// The hop is authenticated and internal, but malformed metadata still fails closed: accepting it
// would make transition telemetry claim an identity the scheduler never supplied.
func ParsePlayoutAiringIdentity(header http.Header) (playout.AiringIdentity, bool) {
	startedAt, err := time.Parse(time.RFC3339Nano, header.Get(PlayoutAiringStartedAtHeader))
	var endsAt time.Time
	if raw := header.Get(PlayoutAiringEndsAtHeader); raw != "" {
		endsAt, err = time.Parse(time.RFC3339Nano, raw)
	}
	kind := schedule.SlotKind(header.Get(PlayoutAiringKindHeader))
	contentID := header.Get(PlayoutAiringContentHeader)
	scheduleBlockID := header.Get(PlayoutScheduleBlockHeader)
	if err != nil || startedAt.IsZero() || (!endsAt.IsZero() && !endsAt.After(startedAt)) || contentID == "" ||
		scheduleBlockID == "" ||
		(kind != schedule.SlotProgram && kind != schedule.SlotFiller && kind != schedule.SlotFlex) {
		return playout.AiringIdentity{}, false
	}
	return playout.AiringIdentity{
		StartedAt: startedAt, EndsAt: endsAt, Kind: kind, ContentID: contentID,
		ScheduleBlockID: scheduleBlockID,
	}, true
}

// PlayoutEncoder starts a supervised ffmpeg for the given args. Injected so the handlers can be
// tested without executing a binary; the composition root supplies playout.Start.
//
// `onProgress` carries ffmpeg's structured progress back (V16 telemetry). It was always
// available — `playout.Start` has taken this callback since the process supervisor was written
// — and every caller passed nil, so each sample was parsed and discarded. Threading it through
// here is what puts real encoder speed on the dashboard instead of a plausible-looking constant.
type PlayoutEncoder func(ctx context.Context, args []string, onProgress func(playout.Progress)) (*playout.Process, error)

// programHandler streams ONE program as finite MPEG-TS, then exits.
//
// "Then exits" is the contract, not an implementation detail: the child's EOF is what advances
// the channel. A handler that looped, or that held the connection open after the program ended,
// would pin the channel to one program forever.
func (s *Server) programHandler(w http.ResponseWriter, r *http.Request) {
	if s.playoutResolver == nil || s.playoutEncoder == nil {
		s.writeProblem(w, r, http.StatusNotImplemented, "Playout unavailable",
			"Internal playout isn't running on this instance.")
		return
	}
	channelID := r.PathValue("id")
	if channelID == "" {
		http.NotFound(w, r)
		return
	}
	r = r.WithContext(diagnostics.WithProcessSpec(r.Context(), diagnostics.ProcessSpec{
		Purpose: "playout_program", ParentRunID: r.Header.Get(PlayoutParentProcessRunHeader), ChannelID: channelID,
	}))
	admission, ok := s.acquirePlayoutAdmission(w, r, channelID)
	if !ok {
		return
	}
	defer admission.Release()
	r = r.WithContext(admission.Context)

	// The codec audience this program is for (§9.1 V48) — the EncodePlan set on the URL by the session
	// parent that requested it, so a baseline session's programs plan for baseline and a full/tuner
	// session's for the broad set. It drives both the copy plan below and which session gets this
	// child's telemetry. This is an internal parent→program hop, so ParseEncodePlan round-trips the
	// canonical token (safe PlanBaseline default on absent). Parsed once here so the offline-card path
	// (which also spawns a child) carries it too.
	encPlan := playout.ParseEncodePlan(r.URL.Query().Get(playoutPlanParam))
	profile := s.playoutResolver.Profile(r.Context())
	broadcastCodec := playout.BroadcastVideoCodec(encPlan, s.playoutResolver.ChannelCodec(r.Context(), channelID))
	if pinned, ok := playout.ParseBroadcastFormat(r.URL.Query().Get(PlayoutBroadcastFormatQuery)); ok {
		profile = pinned.Apply(profile)
		broadcastCodec = pinned.VideoCodec
	}
	if playout.IsHEVCCodec(broadcastCodec) {
		profile.Encoder = playout.HEVCEncoderFor(profile.Encoder)
	}
	w.Header().Set(PlayoutBroadcastFormatHeader, playout.NewBroadcastFormat(profile, broadcastCodec).String())

	airing, streamURL, err := s.playoutResolver.AiringNow(r.Context(), channelID)
	if r.Context().Err() != nil {
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.log.Warn("playout: could not resolve what is airing — showing the card", "channel", channelID, "err", err)
		cardAiring := playout.Airing{
			StartedAt: time.Now().UTC().Truncate(offlineCardDuration),
			Identity:  "unavailable-card",
			Kind:      schedule.SlotFlex,
		}
		cardAiring.ScheduleBlockID = playout.ScheduledBlockID(
			channelID, cardAiring.StartedAt, cardAiring.Kind, cardAiring.Identity,
		)
		setPlayoutAiringIdentity(w.Header(), cardAiring)
		processSpec, _ := diagnostics.ProcessSpecFromContext(r.Context())
		processSpec.ScheduleBlockID = cardAiring.ScheduleBlockID
		r = r.WithContext(diagnostics.WithProcessSpec(r.Context(), processSpec))
		// The card, not a 502 (see serveCard): the usual cause is the media server being
		// unreachable, which is upstream of us and typically transient — and writing an error
		// document in place of MPEG-TS would leave every viewer without channel bytes
		// rather than the one program we could not resolve.
		if !s.serveCard(w, r, channelID, encPlan, profile, cardUnavailable, offlineCardDuration) {
			s.failProgram(w, r, channelID, "offline card", "the offline card could not be rendered")
		}
		return
	}
	if airing.StartedAt.IsZero() {
		airing.StartedAt = time.Now().UTC().Truncate(offlineCardDuration)
	}
	if airing.Identity == "" {
		airing.Identity = string(airing.Kind)
		if airing.Identity == "" {
			airing.Identity = string(schedule.SlotFlex)
		}
	}
	if airing.ScheduleBlockID == "" {
		airing.ScheduleBlockID = playout.ScheduledBlockID(channelID, airing.StartedAt, airing.Kind, airing.Identity)
	}
	setPlayoutAiringIdentity(w.Header(), airing)
	processSpec, _ := diagnostics.ProcessSpecFromContext(r.Context())
	processSpec.Target = encPlan.String()
	processSpec.ScheduleBlockID = airing.ScheduleBlockID
	r = r.WithContext(diagnostics.WithProcessSpec(r.Context(), processSpec))

	// Nothing airing ⇒ the offline card, NOT an error and NOT an empty body.
	//
	// An empty 200 would make the demuxer EOF instantly and re-request in a tight loop,
	// spinning a core on a channel with no content. The card is a real encode occupying real
	// time, so the loop paces itself — and the viewer sees "nothing scheduled" rather than a
	// channel that fails to tune.
	if !airing.Playable() || streamURL == "" {
		title, duration := cardNothingScheduled, offlineCardDuration
		if airing.Kind == schedule.SlotFiller {
			title = cardCommercialBreak
			duration = boundedCardDuration(airing.Remaining)
		}
		if !s.serveCard(w, r, channelID, encPlan, profile, title, duration) {
			// The card itself failed. This is the one honestly terminal case — there is no
			// further fallback to reach for, so the channel does stop here.
			s.failProgram(w, r, channelID, "offline card", "the offline card could not be rendered")
		}
		return
	}

	// Which audio track, honouring the operator's language preference (§9.1). Resolved here
	// rather than inside ProgramArgs because it needs a probe of the source, and the args
	// builder is deliberately a pure function.
	audioTrack := s.playoutResolver.AudioTrackFor(r.Context(), channelID, airing.LibraryItemID, streamURL)

	// Loudness normalisation, FILLER ONLY (§10 V40).
	//
	// ⚠ `airing.Source` is the discriminator, and it needs no new plumbing: it is set for a
	// resolved filler clip and empty for a library title (see Airing.Source). Normalising a
	// feature film to advert loudness would flatten its dynamic range — the problem this solves is
	// adverts recorded a decade apart at wildly different levels, measured at an 11 dB spread
	// across real fetched clips.
	//
	// ⚠ Read LIVE rather than captured at wiring, so `filler.target_lufs` hot-applies like every
	// other setting (config-design §3). Empty (or no liveConfig, as in unit tests that build a
	// bare Server) ⇒ no filter, byte-identical to what shipped before V40.
	targetLUFS := ""
	if airing.Source != "" && s.liveConfig != nil {
		targetLUFS = s.liveConfig("filler.target_lufs")
	}

	// The copy/transcode plan (§9.1 direct play, V47; V50 content-driven codec): copy the video when
	// the served plan can carry this program's codec (the common case — instant, no GPU), transcode
	// only what it cannot. The plan comes from the request (`?plan=`), which encodes HOW THIS SESSION
	// SERVES THE CHANNEL (V50): PlanBaseline = h264/TS (a native-h264 channel, or an HEVC channel
	// down-converted for an incapable client); PlanHEVC8/10 = the HEVC channel served native over
	// fMP4. A channel's stream is not one thing — different clients attach their own session with their
	// own served plan — and this handler feeds that session's parent. ParseEncodePlan defaults to
	// PlanBaseline, so an un-parameterised request stays h264/TS (the black-frame-safe floor).
	//
	// `source` is the probe the plan came from, kept rather than discarded so the transcode below
	// can act on what was already learned (HDR today) without a second ffprobe at the program
	// boundary — the one moment continuity is most fragile.
	plan, source := s.playoutResolver.PlanFor(r.Context(), streamURL, encPlan)
	plan = playout.ConformCopyPlan(source, plan, profile, broadcastCodec)

	// ⚠ Keep an HEVC-plan session's stream UNIFORMLY HEVC (§9.1 V49). An hevc8/hevc10 client watches
	// over fMP4, which binds ONE decoder from its init segment and cannot survive a mid-stream codec
	// change. So when THIS program must transcode video (a VP9/h264/mpeg2 commercial between HEVC
	// shows), transcode it to HEVC — not the profile's default h264 — so the fMP4 the browser plays
	// never switches codec. A source already matching the complete HEVC session format still copies;
	// every mismatch normalizes to HEVC. For a baseline (h264/TS) session this is a no-op.
	spec := playout.ProgramSpec{
		Profile:    profile,
		Input:      streamURL,
		Offset:     airing.Offset,
		Limit:      airing.Remaining,
		AudioTrack: audioTrack,
		TargetLUFS: targetLUFS,
		Plan:       plan,
		Source:     source,
		// Whether this BUILD can tone-map, asked once per process by the composition root. Nil
		// here means "no" — the same fail-safe direction as playoutFont: a missing filter emitted
		// anyway fails at graph-init and kills the channel, so an unknown answer must never be
		// optimistic.
		Tonemap: s.playoutTonemap != nil && s.playoutTonemap(),
	}
	// The retry ladder (§9.1 V47) lives in streamChild: it runs the hardware encode, and only if it
	// produces NO output does it reclaim VRAM + retry, then fall back to software. Passing the spec
	// (not pre-built args) is what lets the ladder rebuild the SAME program with a software encoder.
	// A copy plan (no video transcode) has no ladder — a copy that produces nothing is a bad file.
	s.streamProgram(w, r, channelID, encPlan, airing.Title, spec)
}

// offlineCardDuration is how long one offline-card request lasts before the demuxer re-asks.
//
// Short enough that a channel starts playing promptly once content lands (the operator approves
// a title, an acquisition completes, the next re-request finds it), long enough that an empty
// channel is not re-encoding every second.
const offlineCardDuration = 30 * time.Second

// The card's headline. Which one is showing is the only thing distinguishing "this channel has
// nothing on it" from "something went wrong just now", so they must not be merged: the first is a
// steady state an operator fixes by approving content, the second is transient and usually fixes
// itself. Both are titles, not error text — this is rendered into a video frame someone is
// watching on a television, not into a log.
const (
	cardNothingScheduled = "Nothing scheduled"
	cardCommercialBreak  = "We'll be right back"
	cardUnavailable      = "Program unavailable"
)

// boundedCardDuration keeps a synthetic card from crossing the schedule boundary it stands in
// for. The ordinary 30-second bound remains the retry cadence for an empty Channel or an upstream
// failure; a scheduled break can be shorter when a viewer joins part-way through it.
func boundedCardDuration(remaining time.Duration) time.Duration {
	if remaining > 0 && remaining < offlineCardDuration {
		return remaining
	}
	return offlineCardDuration
}

// serveCard renders the offline card and streams it, returning false only if even the card could
// not be produced.
//
// ⚠ **Why every non-fatal playout failure ends up here rather than at an HTTP error.**
//
// A problem document is not transport data. The supervisor rejects a non-2xx response and retries,
// but a bounded card keeps the channel paced, visible and self-healing instead of turning a missing
// source into repeated connection churn. The card is what a broadcaster does with a dead feed, and
// it works here for a reason
// specific to Loomarr's model: "what is on now" is re-derived from the wall clock on every
// request, so the card needs no knowledge of how long the failed program had left. It occupies
// offlineCardDuration, the supervisor re-asks, and the channel resumes at whatever is genuinely airing
// by then — mid-program, which is correct for a wall clock. A transient media-server outage now
// costs the viewer a caption for a few seconds instead of the channel.
func (s *Server) serveCard(
	w http.ResponseWriter, r *http.Request, channelID string,
	encPlan playout.EncodePlan, profile playout.Profile, title string, duration time.Duration,
) bool {
	// The font comes from the composition root, not playout.FindFont(), because the question is
	// not "does this host have a font file?" but "can this ffmpeg BUILD draw one?" — a build
	// without drawtext dies at graph-init and takes the channel with it. See playout.CardFontFor.
	// Nil-guarded: "" is an unlabelled card, a valid rendering.
	font := ""
	if s.playoutFont != nil {
		font = s.playoutFont()
	}
	// The card is a synthetic lavfi source, so it does not contend for VRAM the way a decode+
	// transcode does — but it still uses the profile's encoder, so it gets the SOFTWARE fallback
	// (not the VRAM eviction step): a card that cannot hardware-encode should still render rather
	// than leave the channel with no frame either.
	card := func(enc playout.Encoder) []string {
		p := profile
		p.Encoder = enc
		// The route key is an opaque internal identifier, not viewer-facing Channel identity.
		// Keep fallback cards unlabelled until a real name/number is explicitly supplied.
		return playout.OfflineCardArgs(p, font, title, "", duration)
	}
	if c := s.startChild(r.Context(), channelID, encPlan, profile.Encoder, true, card(profile.Encoder)); c != nil {
		s.pipeChild(w, r, channelID, "offline card", c)
		return true
	}
	softwareEncoder := playout.SoftwareEncoderFor(profile.Encoder)
	if profile.Encoder != softwareEncoder {
		if c := s.startChild(r.Context(), channelID, encPlan, softwareEncoder, true, card(softwareEncoder)); c != nil {
			s.pipeChild(w, r, channelID, "offline card", c)
			return true
		}
	}
	return false
}

// streamProgram runs ONE program down the retry ladder (§9.1 V47), then streams it to the response.
//
// The ladder exists because a hardware encode can fail SILENTLY — most often the shared GPU is out
// of VRAM (the suggester's model is resident, §8.2), where the encoder cannot allocate its device
// context and emits zero frames with no error. A silent zero-byte encode is a black channel. So:
//
//  1. Try the spec as-is (hardware, if that is what the Profile resolved).
//  2. Produced nothing AND this is a video transcode? Reclaim VRAM (evict the LLM) and retry the
//     SAME encoder — the freed VRAM is often all it needed.
//  3. Still nothing? Rebuild the spec with the codec-matching software encoder (libx264 or libx265)
//     and run that — slower, but the channel PLAYS instead of going black. Software is the floor;
//     the live session's pinned codec never changes.
//
// A `-c copy` program is NOT laddered: a copy that produces nothing is a bad source file, which no
// encoder change fixes — so it runs once and fails through (attempt still surfaces the stderr).
//
// The ladder is only possible because startChild PEEKS the first chunk before committing the HTTP
// response: with nothing yet written, a retry is free. Once bytes are flowing, there is no retry —
// a mid-stream death is a normal program boundary, handled by the demuxer re-requesting.
func (s *Server) streamProgram(
	w http.ResponseWriter, r *http.Request, channelID string, target playout.EncodePlan, what string, spec playout.ProgramSpec,
) {
	transcoding := !spec.Plan.CopyVideo
	softwareEncoder := playout.SoftwareEncoderFor(spec.Profile.Encoder)
	wantsHardware := transcoding && !playout.IsSoftwareEncoder(spec.Profile.Encoder)

	// ADMISSION, up front (§9.1 V47). A hardware transcode must hold a GPU slot; when the encoder is
	// saturated we choose software NOW rather than piling on and stalling. A copy needs no slot (it
	// does no encoding); a box with no hardware encoder reports zero slots and always lands here on
	// software. The reactive evict-and-retry below stays only as a safety net for a slot-holder whose
	// hardware encode still fails.
	if wantsHardware && s.encodePool != nil {
		if release, ok := s.encodePool.AcquireForeground(r.Context()); ok {
			defer release()
		} else {
			s.log.Info("playout: GPU encode slots full — using software for this program",
				"channel", channelID, "program", what, "wanted", spec.Profile.Encoder)
			spec.Profile.Encoder = softwareEncoder
			wantsHardware = false
		}
	}

	// Attempt 1: as resolved (hardware when a slot was granted, else software).
	if c := s.startChild(r.Context(), channelID, target, spec.Profile.Encoder, transcoding, playout.ProgramArgs(spec)); c != nil {
		s.pipeChild(w, r, channelID, what, c)
		return
	}

	// Only a VIDEO TRANSCODE can be rescued by the ladder — a copy failing is a bad file. But a bad
	// file is one PROGRAM, not the channel: fall through to the card so the demuxer gets valid
	// MPEG-TS and the lineup carries on past it (see serveCard).
	if !transcoding {
		s.cardOrFail(w, r, channelID, target, spec.Profile, what, "the source could not be read")
		return
	}

	// Attempt 2 — the SAFETY NET (§9.1 V47). We got here despite holding a GPU slot (wantsHardware),
	// so this is not saturation — the admission gate already routes saturation to software up front.
	// It is the rarer case: a slot-holder's hardware encode produced nothing, usually because the
	// resident LLM is holding the VRAM the encoder needs. Reclaim it (evict the LLM) and retry the
	// SAME hardware encoder once. The zero-byte first attempt is the signal — no polling, no guess.
	if wantsHardware && s.reclaimVRAM != nil {
		s.log.Info("playout: hardware encode produced nothing despite a free slot — reclaiming GPU memory and retrying",
			"channel", channelID, "program", what, "encoder", spec.Profile.Encoder)
		s.reclaimVRAM(r.Context())
		if c := s.startChild(r.Context(), channelID, target, spec.Profile.Encoder, transcoding, playout.ProgramArgs(spec)); c != nil {
			s.pipeChild(w, r, channelID, what, c)
			return
		}
	}

	// Attempt 3: codec-preserving software fallback. An HEVC session uses libx265 and an H.264
	// session uses libx264; changing codec here would poison the pinned parent stream even if this
	// individual child successfully produced bytes.
	if spec.Profile.Encoder != softwareEncoder {
		softSpec := spec
		softSpec.Profile.Encoder = softwareEncoder
		s.log.Warn("playout: falling back to software encoding for this program",
			"channel", channelID, "program", what, "from", spec.Profile.Encoder, "to", softwareEncoder)
		if c := s.startChild(r.Context(), channelID, target, softwareEncoder, transcoding, playout.ProgramArgs(softSpec)); c != nil {
			s.pipeChild(w, r, channelID, what, c)
			return
		}
	}

	// Even software produced nothing — genuinely unplayable (a corrupt file, a missing mount). The
	// PROGRAM is lost either way; the card is what keeps the CHANNEL.
	s.cardOrFail(w, r, channelID, target, spec.Profile, what, "no encoder could produce this program")
}

// cardOrFail is the end of the ladder: this program cannot be produced, so show the card, and only
// if even that fails write the error that ends the channel.
//
// Split out so the two terminal sites in streamProgram cannot drift apart, and so the log keeps the
// real reason — the card is what the VIEWER sees, `reason` is what the OPERATOR needs, and merging
// them would trade one for the other.
func (s *Server) cardOrFail(
	w http.ResponseWriter, r *http.Request, channelID string,
	target playout.EncodePlan, profile playout.Profile, what, reason string,
) {
	s.log.Warn("playout: program could not be produced — showing the card and continuing",
		"channel", channelID, "program", what, "reason", reason)
	if !s.serveCard(w, r, channelID, target, profile, cardUnavailable, offlineCardDuration) {
		s.failProgram(w, r, channelID, what, reason)
	}
}

// liveChild is an encoder that has PROVEN it produces output — its peeked first chunk plus the
// still-running process. Returned by startChild only on success, so a caller holding one can commit
// the HTTP response knowing bytes will flow.
type liveChild struct {
	proc   *playout.Process
	first  []byte             // the first chunk, already read — must be written before draining proc
	enc    playout.Encoder    // for telemetry
	target playout.EncodePlan // for telemetry
	cancel context.CancelFunc // ties the child to the request; pipeChild owns calling it
}

// startChild spawns one encoder and PEEKS its first chunk to prove it produces output before any
// response is committed. Returns the live child on success, or nil when the encoder produced nothing
// (the silent-failure case the ladder rescues) — logging the stderr either way. Deliberately NOT
// through the session Manager: this is one private child per demuxer request whose whole job is to
// END so the parent advances (routing it through the session map would collide on the channel key).
func (s *Server) startChild(
	ctx context.Context, channelID string, target playout.EncodePlan, enc playout.Encoder, transcoding bool, args []string,
) *liveChild {
	if ctx.Err() != nil {
		return nil
	}
	if s.playoutObserver != nil && !s.playoutObserver.AdmitProgram(channelID, target, transcoding) {
		s.log.Info("playout: program waiting for transcode capacity", "channel", channelID, "target", target.String())
		return nil
	}
	// The child dies with the request. cancel is handed to the caller (pipeChild) which owns the
	// stream's lifetime; on a nil return here we cancel immediately so a failed attempt leaves no
	// orphan before the next ladder step.
	cctx, cancel := context.WithCancel(ctx)
	processSpec, _ := diagnostics.ProcessSpecFromContext(cctx)
	processSpec.Purpose = "playout_program"
	processSpec.ChannelID = channelID
	processSpec.Target = target.String()
	cctx = diagnostics.WithProcessSpec(cctx, processSpec)

	enc2 := enc // capture for the progress closure
	onProgress := func(p playout.Progress) {
		if s.playoutObserver != nil {
			// Admission already transitioned to this real cost before spawn. Reporting repeats that
			// transition idempotently while recording the child encoder and progress (§9.1 V49).
			s.playoutObserver.ReportProgram(channelID, target, enc2, transcoding, p)
		}
	}
	proc, err := s.playoutEncoder(cctx, args, onProgress)
	if err != nil {
		s.log.Warn("playout: could not start the program encoder", "channel", channelID, "err", err)
		cancel()
		return nil
	}

	// PEEK the first chunk. A hardware-init failure closes stdout with no bytes — read returns
	// EOF/zero, which is exactly the silent failure the ladder exists to catch. A real encode
	// returns a chunk within a moment (the readrate burst fills the buffer immediately).
	buf := make([]byte, 64*1024)
	n, readErr := proc.Stdout.Read(buf)
	if n == 0 {
		// Nothing produced. Surface ffmpeg's own last stderr line — "Device creation failed",
		// "No capable devices found" — the actionable reason, not a generic message.
		s.log.Warn("playout: encoder produced NO OUTPUT",
			"channel", channelID, "encoder", enc, "ffmpeg", proc.LastError(), "readErr", readErr)
		cancel()
		_ = proc.Wait()
		return nil
	}
	first := make([]byte, n)
	copy(first, buf[:n])
	return &liveChild{proc: proc, first: first, enc: enc, target: target, cancel: cancel}
}

// pipeChild commits the HTTP response and streams the live child to it: the peeked first chunk, then
// the rest of the encoder's output, until the program ends (EOF) or the client disconnects.
func (s *Server) pipeChild(w http.ResponseWriter, r *http.Request, channelID, what string, c *liveChild) {
	defer c.cancel()

	flusher, _ := w.(http.Flusher)
	w.Header().Set("Content-Type", "video/mp2t")
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
	// No Content-Length: this IS finite, unlike the tuner stream, but finite at a length not
	// known until the encode is done.
	w.Header().Set("Accept-Ranges", "none")
	w.WriteHeader(http.StatusOK)

	// The peeked first chunk goes out FIRST — it was read off the pipe before the header, so
	// skipping it would drop the start of the program.
	if _, err := w.Write(c.first); err == nil && flusher != nil {
		flusher.Flush()
	}

	_, _ = copyAndFlush(w, c.proc.Stdout, flusher)

	// Reap before returning so the encoder count is accurate the moment the demuxer re-requests.
	c.cancel()
	_ = c.proc.Wait()
}

// failProgram writes the 502 for a program that could not be produced at all — every ladder step
// exhausted AND the offline card itself unrenderable.
//
// This is a LAST RESORT and there are exactly two callers, both of which have already tried the
// card. The supervisor will retry the failed block with bounded backoff, but viewers receive no new
// transport bytes until one attempt succeeds.
func (s *Server) failProgram(w http.ResponseWriter, r *http.Request, channelID, what, reason string) {
	s.log.Warn("playout: program could not be produced — this channel will not play",
		"channel", channelID, "program", what, "reason", reason)
	s.writeProblem(w, r, http.StatusBadGateway, "Couldn't start the program",
		"Loomarr couldn't start encoding this program.")
}

// copyAndFlush streams src to dst, flushing so the demuxer sees bytes promptly.
//
// Not plain io.Copy: Go buffers the response, and the block supervisor does not treat the stream as
// started until data arrives. Flushing per chunk keeps the handoff between programs from stalling
// at the boundary — the point where a stall is most visible to a viewer.
//
// io.EOF is the SUCCESS case here: the encoder finishing is exactly what advances the channel.
func copyAndFlush(dst io.Writer, src io.Reader, flusher http.Flusher) (int64, error) {
	buf := make([]byte, 64*1024)
	var total int64
	for {
		n, readErr := src.Read(buf)
		if n > 0 {
			written, writeErr := dst.Write(buf[:n])
			total += int64(written)
			if flusher != nil {
				flusher.Flush()
			}
			if writeErr != nil {
				// The demuxer went away. Normal (a channel being stopped), not a problem.
				return total, writeErr
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return total, nil
			}
			return total, readErr
		}
	}
}
