import type { ClientObservation as BrowserClientObservation } from "@loomarr/core/client-diagnostics";
import type Hls from "hls.js";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { LivePlaybackState } from "../player-controller";

// useHlsPlayer — binds a channel's live ABR HLS to a <video> element (§9.1 Watch, V46).
//
// It returns an `attach(video)` the VideoPlayer primitive drives: the primitive owns the element
// and its accessible controls; this hook owns the two things that make a live CHANNEL different
// from a file:
//
//  1. THE TRANSPORT SPLIT. Safari/iOS play `.m3u8` natively (set video.src and go); every other
//     browser needs hls.js over Media Source Extensions. We pick the native path when the browser
//     advertises it (canPlayType) and fall back to hls.js otherwise — the same master playlist a
//     native app would hand to AVPlayer/ExoPlayer, which is why the transport is HLS.
//  2. THE SIGNED URL. The stream authenticates by a short-lived signed URL, not the device token
//     (§11) — so the source comes from POST /v1/channels/{id}/play-url, minted per session. The
//     device token never touches the browser.
//
// Rendition SELECTION stays with the client: hls.js measures real throughput and switches per
// segment, and capLevelToPlayerSize keeps it from fetching a rendition larger than the frame. The
// server only decides which renditions to OFFER, hinted by the device signals below.

type BrowserPlayerStatus = "idle" | "loading" | "playing" | "error";
const LIVE_DVR_HORIZON_MS = 15 * 60_000;

type BrowserTunePhase = "first-frame" | "manifest";

interface BrowserTuneAttempt {
  markPhase: (phase: BrowserTunePhase) => void;
  playURL?: string;
}

interface BrowserPlaySource {
  expiresAt?: number;
  url: string;
}

interface BrowserLivePlaybackTransport {
  state: LivePlaybackState;
  play: (video: HTMLVideoElement) => Promise<void> | void;
  pause: (video: HTMLVideoElement) => void;
  goLive: (video: HTMLVideoElement) => Promise<void> | void;
}

interface BrowserHlsPlayerOptions {
  attempt?: BrowserTuneAttempt;
  channelId: string;
  errorMessage: (error: unknown) => string;
  mintSource: (signal: AbortSignal) => Promise<BrowserPlaySource | undefined>;
  recordDiagnostic: (observation: BrowserClientObservation) => void;
}

interface UseBrowserHlsPlayer {
  status: BrowserPlayerStatus;
  error?: string;
  playbackSessionId: string;
  liveTransport: BrowserLivePlaybackTransport;
  /**
   * Bind live playback to a <video>. Pass to VideoPlayer's `attach` prop. Mints a signed URL,
   * attaches via native HLS or hls.js, and returns a cleanup that tears both down.
   */
  attach: (video: HTMLVideoElement) => () => void;
}

let cachedHlsController: typeof Hls | undefined;

interface LiveClock {
  channelId: string;
  mode: LivePlaybackState["mode"];
  lagMs: number;
  viewerTimeMs: number;
  pausedMediaTime?: number;
  noticeRevision: number;
}

const liveStateAt = (clock: LiveClock, now: number): LivePlaybackState => {
  const viewerTimeMs = clock.viewerTimeMs;
  return {
    mode: clock.mode,
    lagSeconds: clock.mode === "live" ? 0 : Math.max(0, Math.round((now - viewerTimeMs) / 1000)),
    viewerTimeMs,
    noticeRevision: clock.noticeRevision,
  };
};

type NativeDatedMedia = HTMLVideoElement & { getStartDate?: () => Date };

// The Watch clock describes the frame on screen, not the wall-clock edge the Channel is producing.
// hls.js maps currentTime through EXT-X-PROGRAM-DATE-TIME directly. Safari's native HLS surface
// exposes the same mapping as the Date at media time zero, so add the element's currentTime there.
// Both are optional while metadata warms; callers supply the mode-correct wall-clock fallback.
const displayedWallClockMs = (
  video: HTMLVideoElement | undefined,
  hls: Hls | undefined,
  fallback: number,
): number => {
  const playingDate = hls?.playingDate;
  const playingMs = playingDate?.getTime();
  if (playingMs !== undefined && Number.isFinite(playingMs)) return playingMs;

  const native = video as NativeDatedMedia | undefined;
  const startMs = native?.getStartDate?.().getTime();
  if (startMs !== undefined && Number.isFinite(startMs) && Number.isFinite(native?.currentTime)) {
    return startMs + (native?.currentTime ?? 0) * 1_000;
  }
  return fallback;
};

const containsMediaTime = (ranges: TimeRanges, point: number): boolean => {
  // An empty TimeRanges means the native/MSE pipeline has not published its window yet. Treat it as
  // unknown rather than expired; the server is authoritative and may still serve the paused point.
  if (ranges.length === 0) return true;
  for (let index = 0; index < ranges.length; index++) {
    if (point >= ranges.start(index) - 0.1 && point <= ranges.end(index) + 0.1) return true;
  }
  return false;
};

const clearTransferredBuffers = async (transferred: NonNullable<ReturnType<Hls["transferMedia"]>>) => {
  for (const track of Object.values(transferred.tracks)) {
    const buffer = track?.buffer;
    if (!buffer) continue;
    if (buffer.updating) buffer.abort();
    if (buffer.buffered.length === 0) continue;
    const start = buffer.buffered.start(0);
    const end = buffer.buffered.end(buffer.buffered.length - 1);
    await new Promise<void>((resolve, reject) => {
      const done = () => {
        buffer.removeEventListener("updateend", complete);
        buffer.removeEventListener("error", failed);
      };
      const complete = () => {
        done();
        resolve();
      };
      const failed = () => {
        done();
        reject(new Error("source buffer clear failed"));
      };
      buffer.addEventListener("updateend", complete, { once: true });
      buffer.addEventListener("error", failed, { once: true });
      buffer.remove(start, end);
    });
  }
};

const transferredBufferedRange = (
  transferred: NonNullable<ReturnType<Hls["transferMedia"]>>,
): { start: number; end: number } | undefined => {
  let start = Number.NEGATIVE_INFINITY;
  let end = Number.POSITIVE_INFINITY;
  let found = false;
  for (const track of Object.values(transferred.tracks)) {
    const buffered = track?.buffer?.buffered;
    if (!buffered?.length) continue;
    const last = buffered.length - 1;
    start = Math.max(start, buffered.start(last));
    end = Math.min(end, buffered.end(last));
    found = true;
  }
  return found && start < end ? { start, end } : undefined;
};

const releaseTransferredDecoder = async (
  video: HTMLVideoElement,
  transferred: NonNullable<ReturnType<Hls["transferMedia"]>>,
) => {
  const range = transferredBufferedRange(transferred);
  if (!range || video.currentTime + 0.05 >= range.end) return;

  // TimeRanges.end() is a half-open boundary, not a presentable timestamp. WebKit can leave a seek
  // to that exact value pending until live playback naturally reaches it. Park at the final
  // presentable-frame interval instead, and cap the acknowledgement: a platform that cannot release
  // this already-buffered seek promptly takes the bounded fresh-MSE fallback rather than delaying
  // the viewer by the outgoing fragment remainder.
  const target = Math.max(range.start, range.end - 0.05);
  await new Promise<void>((resolve, reject) => {
    let complete = false;
    const finish = (error?: Error) => {
      if (complete) return;
      complete = true;
      window.clearTimeout(timer);
      video.removeEventListener("seeked", onSeeked);
      if (error) reject(error);
      else resolve();
    };
    const onSeeked = () => finish();
    const timer = window.setTimeout(() => {
      if (video.currentTime + 0.05 >= target) finish();
      else finish(new Error("decoder release seek timed out"));
    }, 100);
    video.addEventListener("seeked", onSeeked, { once: true });
    video.currentTime = target;
  });
};

const webKitRequiresFreshMSEHandoff = (): boolean => {
  if (typeof navigator === "undefined") return false;
  const agent = navigator.userAgent;
  return /AppleWebKit/i.test(agent) && !/(?:Chromium|Chrome|CriOS|Edg)/i.test(agent);
};

const discardTransferredMedia = (
  video: HTMLVideoElement,
  objectURL: string | undefined,
  resetBeforeReplacement = true,
) => {
  // transferMedia deliberately leaves the MediaSource object URL on the element so another hls.js
  // controller can adopt it. The fresh-MSE branch does not adopt that transfer. Replacing src
  // without first revoking the abandoned URL leaves WebKit retaining each ended MediaSource and its
  // decoder lease; over repeated tunes, opening the next source then drifts into multi-second waits.
  // Detach it explicitly while the held poster owns continuity, then let the fresh controller bind.
  let detached = false;
  if (objectURL && (video.src === objectURL || video.currentSrc === objectURL)) {
    video.removeAttribute("src");
    detached = true;
  }
  for (const source of video.querySelectorAll("source")) {
    if (objectURL && source.src !== objectURL) continue;
    source.remove();
    detached = true;
  }
  // WebKit may synchronously spend more than a second processing an empty load. Skip that
  // intermediate state when the fresh controller below immediately replaces the source; assigning
  // its MediaSource performs the required element load. A superseded generation still resets here
  // because no replacement follows to release the detached decoder.
  if (detached && resetBeforeReplacement) video.load();
  if (objectURL?.startsWith("blob:")) URL.revokeObjectURL(objectURL);
};

const createHlsController = (HlsController: typeof Hls): Hls =>
  new HlsController({
    // A source-scoped controller stays empty until its transferred MediaSource is attached. The
    // handoff below then loads the source and performs one explicit media start.
    autoStartLoad: false,
    capLevelToPlayerSize: true,
    // Baseline HLS is MPEG-TS. Keep its transmux off the UI thread; hls.js shares and reference-
    // counts this worker across the bounded source-scoped controller pair.
    enableWorker: true,
    // Live channel: keep chasing the live edge, and be patient while it warms up. A channel takes a
    // few seconds to produce its first segment (the encoder spins up), during which the playlist
    // may briefly have no media — hls.js must RETRY, not give up.
    liveDurationInfinity: true,
    manifestLoadingMaxRetry: 8,
    manifestLoadingRetryDelay: 1000,
    levelLoadingMaxRetry: 8,
    fragLoadingMaxRetry: 8,
    // ⚠ Start ~TWO segments from the live edge — the balance between fast first-paint and a
    // survivable buffer (both measured in-browser). hls.js's default is 3 (~12s at our 4s
    // segments); 1 sits at the live edge and leaves transcodes no cushion against a realtime dip.
    liveSyncDurationCount: 2,
    // Keep corrective live-edge seeks well above the sync target so a slow transcode can drift and
    // let its buffer absorb a dip rather than causing another visible stall.
    // The shared DVR window, not hls.js's latency correction, decides when an intentional pause
    // expires. Keep this above the complete fifteen-minute server horizon.
    liveMaxLatencyDurationCount: 10_000,
    // Build a forward cushion after fast start and retain the complete shared DVR horizon.
    maxBufferLength: 60,
    backBufferLength: 900,
  });

const createPlaybackSessionID = () => {
  const bytes = new Uint8Array(16);
  globalThis.crypto.getRandomValues(bytes);
  return `web_${Array.from(bytes, (byte) => byte.toString(16).padStart(2, "0")).join("")}`;
};

function useBrowserHlsPlayer({
  attempt,
  channelId,
  errorMessage,
  mintSource,
  recordDiagnostic,
}: BrowserHlsPlayerOptions): UseBrowserHlsPlayer {
  const playbackSessionIDRef = useRef(createPlaybackSessionID());
  const channelIdentityRef = useRef(channelId);
  const replacedChannelRef = useRef<string | undefined>(undefined);
  if (channelIdentityRef.current !== channelId) {
    replacedChannelRef.current = channelIdentityRef.current;
    channelIdentityRef.current = channelId;
  }
  // TanStack commits the route parameter after the tuner has already activated its target. That
  // commit can drop the transient attempt object while the Channel itself is unchanged. Retain the
  // attempt for that Channel so VideoPlayer does not tear down and reattach the same live source
  // immediately after its first frame; the next Channel replaces it atomically.
  const retainedAttemptRef = useRef<{ channelId: string; attempt?: BrowserTuneAttempt }>({
    channelId,
    attempt,
  });
  if (retainedAttemptRef.current.channelId !== channelId) {
    retainedAttemptRef.current = { channelId, attempt };
  } else if (attempt) {
    retainedAttemptRef.current.attempt = attempt;
  }
  const playbackAttempt = retainedAttemptRef.current.attempt;
  const [state, setState] = useState<{
    channelId: string;
    status: BrowserPlayerStatus;
    error?: string;
  }>({
    channelId,
    status: "idle",
  });
  // A channel change is loading immediately, before VideoPlayer's source-binding layout effect
  // runs. The previous source's "playing" state must never hide the new attempt's tuning presentation.
  const status = state.channelId === channelId ? state.status : "loading";
  const error = state.channelId === channelId ? state.error : undefined;
  // Each attachment gets a generation in addition to its AbortController. Abort stops the fetch;
  // generation guards the small race where a promise has already resolved and queued its callback.
  // A boolean cannot do this: the next attach resets it to false and accidentally re-authorizes an
  // older request that resolves late.
  const generationRef = useRef(0);
  // A Channel route-param change keeps this hook and its <video> mounted. The active controller
  // transfers its MediaSource and compatible SourceBuffers to a fresh, unused standby. Controllers
  // are source-scoped: once the replacement paints, the detached old active is destroyed and a new
  // standby is constructed off the critical path. Only two are live; unmount destroys both.
  const hlsRef = useRef<{
    instance?: Hls;
    standby?: Hls;
    standbyFresh?: boolean;
    destroyTimer?: number;
  }>({});
  const now = Date.now();
  const clockRef = useRef<LiveClock>({
    channelId,
    mode: "live",
    lagMs: 0,
    viewerTimeMs: now,
    noticeRevision: 0,
  });
  const activeRef = useRef<{
    video?: HTMLVideoElement;
    hls?: Hls;
    sourceURL?: string;
    lastKeepaliveMs: number;
  }>({ lastKeepaliveMs: 0 });
  const [transportState, setTransportState] = useState<{ channelId: string; value: LivePlaybackState }>({
    channelId,
    value: liveStateAt(clockRef.current, now),
  });
  const warmedPlayURL = playbackAttempt?.playURL;

  const publishTransport = useCallback(
    (sampleFrame = true) => {
      const at = Date.now();
      const clock = clockRef.current;
      if (clock.channelId !== channelId) return;
      if (sampleFrame && clock.mode !== "paused") {
        const fallback = clock.mode === "live" ? at : at - clock.lagMs;
        const active = activeRef.current;
        clock.viewerTimeMs = displayedWallClockMs(active.video, active.hls, fallback);
      }
      setTransportState({ channelId, value: liveStateAt(clock, at) });
    },
    [channelId],
  );

  const returnLive = useCallback(
    async (video: HTMLVideoElement, expired: boolean) => {
      const clock = clockRef.current;
      if (clock.channelId !== channelId) return;
      clock.mode = "live";
      clock.lagMs = 0;
      clock.viewerTimeMs = Date.now();
      clock.pausedMediaTime = undefined;
      if (expired) clock.noticeRevision++;

      const hls = activeRef.current.video === video ? activeRef.current.hls : undefined;
      hls?.startLoad(-1);
      const liveEdge = hls?.liveSyncPosition;
      if (liveEdge !== null && liveEdge !== undefined && Number.isFinite(liveEdge)) {
        video.currentTime = liveEdge;
      } else if (video.seekable.length > 0) {
        video.currentTime = video.seekable.end(video.seekable.length - 1);
      }
      publishTransport(false);
      await video.play().catch(() => undefined);
    },
    [channelId, publishTransport],
  );

  const pauseLive = useCallback(
    (video: HTMLVideoElement) => {
      const at = Date.now();
      const clock = clockRef.current;
      if (clock.channelId !== channelId) return;
      const fallback = clock.mode === "behind" ? at - clock.lagMs : at;
      const active = activeRef.current;
      clock.viewerTimeMs = displayedWallClockMs(active.video, active.hls, fallback);
      clock.mode = "paused";
      clock.pausedMediaTime = video.currentTime;
      video.pause();
      publishTransport(false);
    },
    [channelId, publishTransport],
  );

  const playLive = useCallback(
    async (video: HTMLVideoElement) => {
      const clock = clockRef.current;
      if (clock.channelId !== channelId || clock.mode !== "paused") {
        await video.play().catch(() => undefined);
        return;
      }
      const pausedMediaTime = clock.pausedMediaTime;
      if (
        Date.now() - clock.viewerTimeMs >= LIVE_DVR_HORIZON_MS ||
        pausedMediaTime === undefined ||
        !containsMediaTime(video.seekable, pausedMediaTime)
      ) {
        await returnLive(video, true);
        return;
      }
      clock.mode = "behind";
      clock.lagMs = Math.max(0, Date.now() - clock.viewerTimeMs);
      const hls = activeRef.current.video === video ? activeRef.current.hls : undefined;
      hls?.startLoad(pausedMediaTime);
      video.currentTime = pausedMediaTime;
      publishTransport();
      await video.play().catch(() => undefined);
    },
    [channelId, publishTransport, returnLive],
  );

  useEffect(() => {
    const clock = clockRef.current;
    if (clock.channelId !== channelId) {
      const at = Date.now();
      clockRef.current = {
        channelId,
        mode: "live",
        lagMs: 0,
        viewerTimeMs: at,
        noticeRevision: 0,
      };
      setTransportState({ channelId, value: liveStateAt(clockRef.current, at) });
    }

    const timer = window.setInterval(() => {
      publishTransport();
      const current = clockRef.current;
      const active = activeRef.current;
      if (current.channelId !== channelId || current.mode !== "paused" || !active.video) return;

      if (
        Date.now() - current.viewerTimeMs >= LIVE_DVR_HORIZON_MS ||
        (current.pausedMediaTime !== undefined &&
          !containsMediaTime(active.video.seekable, current.pausedMediaTime))
      ) {
        void returnLive(active.video, true);
        return;
      }

      // Native HLS implementations may stop playlist polling while the element is paused. A cheap
      // manifest read keeps the one shared server remux leased; it never creates a viewer encoder.
      const at = Date.now();
      if (!active.sourceURL || at - active.lastKeepaliveMs < 10_000) return;
      active.lastKeepaliveMs = at;
      void fetch(active.sourceURL, { cache: "no-store" })
        .then((response) => response.arrayBuffer())
        .catch(() => undefined);
    }, 1_000);
    return () => window.clearInterval(timer);
  }, [channelId, publishTransport, returnLive]);

  const bind = useCallback(
    async (video: HTMLVideoElement, url: string, current: () => boolean): Promise<() => void> => {
      // hls.js is almost half a megabyte minified. Loading it with the Watch route made the route's
      // controls and programme context wait for a transport library they do not need to render.
      // Fetch it only after the signed URL arrives; the page paints first, then playback attaches.
      // The generation check matters because a channel switch can happen while this chunk is in
      // flight — an obsolete import must never attach its stream to the replacement video.
      // WebKit can defer even an already-resolved dynamic import behind media work for hundreds of
      // milliseconds. Resolve the transport class once per document; every later tune takes the
      // synchronous cached value while the bounded controller pool still owns runtime resources.
      // Safari-family browsers have a native HLS pipeline that avoids the repeated MediaSource
      // replacement hls.js needs. Prefer it before importing the half-megabyte controller. The UA
      // guard is load-bearing: some Chromium builds answer "maybe" for the MIME type without being
      // able to play HLS, and must continue through hls.js below.
      const nativeHLS =
        webKitRequiresFreshMSEHandoff() && Boolean(video.canPlayType("application/vnd.apple.mpegurl"));
      const HlsController = nativeHLS ? undefined : (cachedHlsController ?? (await import("hls.js")).default);
      if (HlsController) cachedHlsController = HlsController;
      if (!current()) return () => undefined;

      const diagnosticTransport = HlsController?.isSupported() ? "hls_js" : "native_hls";
      const diagnosticBase = {
        playbackSessionId: playbackSessionIDRef.current,
        channelId,
        transport: diagnosticTransport,
      } as const;
      const bufferedMs = () => {
        if (video.buffered.length === 0) return 0;
        return Math.max(
          0,
          Math.round((video.buffered.end(video.buffered.length - 1) - video.currentTime) * 1_000),
        );
      };
      const reportPosition = (
        event: "player.buffering_started" | "player.buffering_ended" | "player.seeking" | "player.seeked",
      ) =>
        recordDiagnostic({
          ...diagnosticBase,
          event,
          viewerTimeMs: Math.round(clockRef.current.viewerTimeMs),
          ...(event.startsWith("player.buffering") ? { bufferedMs: bufferedMs() } : {}),
        });
      const onWaiting = () => reportPosition("player.buffering_started");
      const onPlaying = () => reportPosition("player.buffering_ended");
      const onNativeError = () =>
        recordDiagnostic({
          ...diagnosticBase,
          event: "player.media_error",
          errorCode: `media_${video.error?.code ?? 0}`,
          fatal: true,
        });
      video.addEventListener("waiting", onWaiting);
      video.addEventListener("playing", onPlaying);
      video.addEventListener("error", onNativeError);
      recordDiagnostic({ ...diagnosticBase, event: "player.attached", reason: "mount" });
      const replacedChannelID = replacedChannelRef.current;
      if (replacedChannelID) {
        replacedChannelRef.current = undefined;
        recordDiagnostic({
          ...diagnosticBase,
          event: "player.source_replaced",
          reason: "channel_change",
          previousChannelId: replacedChannelID,
        });
      }
      const stopDiagnostics = (reason: "unmount" | "retry" = "unmount") => {
        video.removeEventListener("waiting", onWaiting);
        video.removeEventListener("playing", onPlaying);
        video.removeEventListener("error", onNativeError);
        recordDiagnostic({ ...diagnosticBase, event: "player.detached", reason });
      };

      // Name the generation on the element before any decoded-frame observer is armed. The tuner
      // certification captures this value when requestVideoFrameCallback is REQUESTED, so a late
      // callback from the outgoing Channel cannot be mistaken for the replacement when both use
      // the same transferred MediaSource blob URL.
      video.dataset.playbackChannel = channelId;

      let replenishAfterFirstFrame: (() => void) | undefined;
      let firstFrame = false;
      const onFirstFrame = () => {
        if (firstFrame) return;
        firstFrame = true;
        if (video.poster.startsWith("data:image/png;base64,")) video.removeAttribute("poster");
        playbackAttempt?.markPhase("first-frame");
        setState({ channelId, status: "playing" });
        recordDiagnostic({ ...diagnosticBase, event: "player.ready" });
        replenishAfterFirstFrame?.();
      };
      let frameCallback: number | undefined;
      let firstFrameWatchArmed = false;
      const requestFrame = (
        video as Partial<Pick<HTMLVideoElement, "requestVideoFrameCallback">>
      ).requestVideoFrameCallback?.bind(video);
      const armFirstFrameWatch = () => {
        if (firstFrameWatchArmed) return;
        firstFrameWatchArmed = true;
        if (requestFrame) frameCallback = requestFrame(() => onFirstFrame());
        else video.addEventListener("playing", onFirstFrame, { once: true });
      };
      let joinReplacementOnLoadedMetadata: (() => void) | undefined;
      let joinReplacementOnLoadedData: (() => void) | undefined;
      let joinReplacementOnCanPlay: (() => void) | undefined;
      const onLoadedMetadata = () => {
        const joinReplacement = joinReplacementOnLoadedMetadata;
        joinReplacementOnLoadedMetadata = undefined;
        joinReplacement?.();
      };
      const onLoadedData = () => {
        armFirstFrameWatch();
        const joinReplacement = joinReplacementOnLoadedData;
        joinReplacementOnLoadedData = undefined;
        joinReplacement?.();
      };
      const onCanPlay = () => {
        const joinReplacement = joinReplacementOnCanPlay;
        joinReplacementOnCanPlay = undefined;
        joinReplacement?.();
      };
      const stopFirstFrameWatch = () => {
        joinReplacementOnLoadedMetadata = undefined;
        joinReplacementOnLoadedData = undefined;
        joinReplacementOnCanPlay = undefined;
        if (frameCallback !== undefined) video.cancelVideoFrameCallback?.(frameCallback);
        video.removeEventListener("loadedmetadata", onLoadedMetadata);
        video.removeEventListener("loadeddata", onLoadedData);
        video.removeEventListener("canplay", onCanPlay);
        video.removeEventListener("playing", onFirstFrame);
      };

      // hls.js FIRST when it is supported — deliberately, and it is the fix for a real bug.
      //
      // ⚠ Chromium returns a truthy `canPlayType("application/vnd.apple.mpegurl")` ("maybe") on some
      // builds but CANNOT actually decode HLS — so a native-first order set `video.src`, the element
      // sat at readyState 0 with a black frame, and no .m3u8 was ever fetched. hls.js (Media Source
      // Extensions) is the correct path on every non-Safari browser, so we take it whenever it is
      // supported and only fall back to native HLS when it is not (that is Safari/iOS, which plays
      // `.m3u8` natively and genuinely). capLevelToPlayerSize stops hls.js fetching a rendition
      // larger than the frame; low-latency off — this is live TV, and the steadier buffer survives a
      // flaky link.
      if (HlsController?.isSupported()) {
        const Hls = HlsController;
        const discardTransferForWebKit = webKitRequiresFreshMSEHandoff();
        const previous = hlsRef.current.instance;
        let transferred: ReturnType<Hls["transferMedia"]> = null;
        let transferredObjectURL: string | undefined;
        if (previous?.url && previous.media === video) {
          previous.stopLoad();
          // Freeze the decoder before removing its current SourceBuffer range. WebKit otherwise
          // holds the still-playing outgoing bytes until their natural end, adding roughly the
          // remaining segment duration to an otherwise cached adjacent tune. VideoPlayer keeps the
          // last decoded frame as the handoff poster until the replacement produces its own frame.
          // WebKit's fresh-MSE branch detaches the outgoing source immediately below. Avoid
          // explicitly changing the element to paused first: preserving its active playback intent
          // lets the replacement join as soon as its first bytes are ready.
          if (!discardTransferForWebKit) video.pause();
          transferredObjectURL = video.currentSrc || video.src;
          transferred = previous.transferMedia();
        } else {
          previous?.stopLoad();
        }
        // Each hls.js controller owns one source URL for its lifetime. A normal tune consumes the
        // already-constructed fresh standby; a superseding burst can arrive before replenishment,
        // in which case retire the older detached controller and create the required source owner.
        const parked = hlsRef.current.standby;
        const hls = parked && hlsRef.current.standbyFresh ? parked : createHlsController(Hls);
        if (parked && parked !== hls && parked !== previous) parked.destroy();
        hlsRef.current.instance = hls;
        hlsRef.current.standby = previous;
        hlsRef.current.standbyFresh = false;
        activeRef.current = { video, hls, sourceURL: url, lastKeepaliveMs: Date.now() };
        replenishAfterFirstFrame = () => {
          if (!current() || hlsRef.current.instance !== hls) return;
          const retired = hlsRef.current.standby;
          if (retired && retired !== hls) retired.destroy();
          hlsRef.current.standby = createHlsController(Hls);
          hlsRef.current.standbyFresh = true;
        };
        let manifestParsed = false;
        let replacementAttached = false;
        const playReplacement = () => {
          // Fragment-buffered continues throughout a live session. It may finish after the viewer
          // deliberately pauses, so only the initial/live join is allowed to drive autoplay.
          if (
            !replacementAttached ||
            clockRef.current.channelId !== channelId ||
            clockRef.current.mode === "paused"
          ) {
            return;
          }
          void video.play().catch(() => {
            /* autoplay policy — the control handles it */
          });
        };
        joinReplacementOnLoadedMetadata = playReplacement;
        joinReplacementOnLoadedData = playReplacement;
        joinReplacementOnCanPlay = playReplacement;
        const joinTransferredLiveSync = () => {
          if (!transferred || firstFrame || !current()) return;
          const liveSyncPosition = hls.liveSyncPosition;
          if (
            liveSyncPosition === null ||
            !Number.isFinite(liveSyncPosition) ||
            Math.abs(video.currentTime - liveSyncPosition) <= 0.05
          ) {
            return;
          }
          // Clearing a transferred MediaSource temporarily rewinds the persistent element. hls.js
          // computes the replacement's live start from its level details, but Firefox does not
          // always apply that implicit jump before the first append. Join explicitly so cached
          // media cannot wait for the next playlist refresh.
          video.currentTime = liveSyncPosition;
        };
        const onManifestParsed = () => {
          manifestParsed = true;
          playbackAttempt?.markPhase("manifest");
          joinTransferredLiveSync();
          playReplacement();
        };
        const onLevelUpdated = () => joinTransferredLiveSync();
        const onFragmentBuffered = () => {
          armFirstFrameWatch();
          // attachMedia can queue a target loadstart after it returns. WebKit then resets the
          // element to paused even when the immediate post-attach play() succeeded. Join once as
          // bytes buffer and once more when target loadeddata proves that queued loadstart is past;
          // both belong to this viewer tune, rather than being an autoplay retry loop.
          playReplacement();
        };
        const onError = (_evt: string, data: { fatal: boolean; type: string }) => {
          recordDiagnostic({
            ...diagnosticBase,
            event: "player.media_error",
            errorCode:
              data.type === Hls.ErrorTypes.NETWORK_ERROR
                ? "hls_network"
                : data.type === Hls.ErrorTypes.MEDIA_ERROR
                  ? "hls_media"
                  : "hls_other",
            fatal: data.fatal,
          });
          if (!data.fatal) return; // non-fatal: hls.js recovers on its own
          // ⚠ A fatal error during a LIVE stream is usually RECOVERABLE, not terminal — the channel
          // is warming up (empty playlist for a beat) or a segment hiccuped. hls.js has built-in
          // recovery for exactly this, so attempt it before declaring the stream dead: reload the
          // network pipeline for network errors, recover the media buffer for media errors. Only a
          // genuinely unrecoverable error (or one that keeps recurring) surfaces to the viewer.
          switch (data.type) {
            case Hls.ErrorTypes.NETWORK_ERROR:
              if (clockRef.current.mode === "live") {
                hls.startLoad(); // re-fetch the manifest/segments — the fix for warmup 404s/empties
              } else {
                void returnLive(video, true);
              }
              break;
            case Hls.ErrorTypes.MEDIA_ERROR:
              hls.recoverMediaError();
              break;
            default:
              setState({ channelId, status: "error", error: "The stream stopped. Try again in a moment." });
              if (hlsRef.current.instance === hls) {
                const standby = hlsRef.current.standby;
                hlsRef.current.instance = undefined;
                hlsRef.current.standby = undefined;
                hlsRef.current.standbyFresh = false;
                activeRef.current = { lastKeepaliveMs: 0 };
                if (standby && standby !== hls) standby.destroy();
              }
              hls.destroy();
          }
        };
        if (requestFrame) {
          video.addEventListener("loadedmetadata", onLoadedMetadata, { once: true });
          video.addEventListener("loadeddata", onLoadedData, { once: true });
          video.addEventListener("canplay", onCanPlay, { once: true });
        } else armFirstFrameWatch();
        hls.on(Hls.Events.MANIFEST_PARSED, onManifestParsed);
        hls.on(Hls.Events.LEVEL_UPDATED, onLevelUpdated);
        hls.on(Hls.Events.FRAG_BUFFERED, onFragmentBuffered);
        hls.on(Hls.Events.ERROR, onError);

        // Preserve an OPEN MediaSource and compatible SourceBuffers, but never outgoing
        // Channel-relative bytes. Decoder release seeks to the last presentable frame rather than
        // the half-open end and is time-bounded; ended and closed sources, a seek timeout, and
        // failed clears consume the standby's bounded fresh-MSE branch.
        let discardTransfer = false;
        if (transferred?.mediaSource?.readyState === "open") {
          // Hosted and shipping WebKit can block its event loop while seeking an open live source,
          // so a JavaScript timer cannot honestly bound that transfer path. Consume the already-
          // constructed fresh standby instead. Chromium and Firefox keep the lower-allocation
          // transfer path, whose in-range seek remains explicitly time-bounded.
          if (discardTransferForWebKit) {
            discardTransfer = true;
            transferred = null;
          } else {
            try {
              await releaseTransferredDecoder(video, transferred);
              await clearTransferredBuffers(transferred);
            } catch {
              // A decoder seek or range removal that cannot finish promptly cannot be reused safely.
              // Attaching the element directly gives the replacement controller a fresh MediaSource.
              discardTransfer = true;
              transferred = null;
            }
          }
        } else {
          discardTransfer = Boolean(transferred);
          transferred = null;
        }
        if (!current()) {
          if (discardTransfer) discardTransferredMedia(video, transferredObjectURL);
          stopFirstFrameWatch();
          hls.off(Hls.Events.MANIFEST_PARSED, onManifestParsed);
          hls.off(Hls.Events.LEVEL_UPDATED, onLevelUpdated);
          hls.off(Hls.Events.FRAG_BUFFERED, onFragmentBuffered);
          hls.off(Hls.Events.ERROR, onError);
          hls.stopLoad();
          // A newer bind has already swapped this controller into standby; keep the bounded pool.
          // If it is still active, the Watch surface was left while clear was pending, so no future
          // bind can own either controller and both must be released here.
          if (hlsRef.current.instance === hls) {
            const standby = hlsRef.current.standby;
            hls.destroy();
            if (standby && standby !== hls) standby.destroy();
            hlsRef.current.instance = undefined;
            hlsRef.current.standby = undefined;
            hlsRef.current.standbyFresh = false;
            activeRef.current = { lastKeepaliveMs: 0 };
          }
          stopDiagnostics("retry");
          return () => undefined;
        }
        let sourceLoaded = false;
        if (discardTransfer) {
          discardTransferredMedia(video, transferredObjectURL, !discardTransferForWebKit);
          // A fresh controller has no SourceBuffers to adopt, so manifest parsing can overlap its
          // MediaSource attachment safely. autoStartLoad remains false: init/media bytes still wait
          // for attachment and the generation-scoped start below.
          hls.loadSource(url);
          sourceLoaded = true;
        }
        // Do not rewind into the range being removed. WebKit can keep SourceBuffer.remove pending
        // while its decoder still owns that position, turning an otherwise cached tune into a
        // multi-second stall. Rewind only after updateend releases the outgoing bytes, and only
        // while this generation still owns the element.
        if (transferred) video.currentTime = 0;
        if (transferred) hls.attachMedia(transferred);
        else hls.attachMedia(video);
        // Attachment is the first point where a frame callback can only belong to this source:
        // transferred buffers are empty and a fresh attach has replaced the old MediaSource. Arm
        // before startLoad so a fast cached append cannot present its first frame before the
        // observer exists and leave certification (and the tuning overlay) waiting for a later one.
        armFirstFrameWatch();
        replacementAttached = true;
        // hls.js transfer is an attach-before-source transaction. Parsing a source on a detached
        // controller can fetch its init segment before the transferred SourceBuffers are adopted;
        // WebKit can then strand that controller without ever requesting the media fragment. The
        // fresh branch above has no transferred buffers and deliberately overlaps manifest parse.
        if (!sourceLoaded) hls.loadSource(url);
        // Queue the target join before any media bytes can arrive. WebKit can decode a cached first
        // append before MANIFEST_PARSED is delivered; waiting for that event leaves a real target
        // frame paused. Later event joins remain necessary because loadstart can reset the element.
        playReplacement();
        hls.startLoad();
        if (manifestParsed) playReplacement();
        return () => {
          stopDiagnostics();
          stopFirstFrameWatch();
          hls.off(Hls.Events.MANIFEST_PARSED, onManifestParsed);
          hls.off(Hls.Events.LEVEL_UPDATED, onLevelUpdated);
          hls.off(Hls.Events.FRAG_BUFFERED, onFragmentBuffered);
          hls.off(Hls.Events.ERROR, onError);
          if (hlsRef.current.instance !== hls) return;
          if (activeRef.current.hls === hls) activeRef.current = { lastKeepaliveMs: 0 };
          hls.stopLoad();
          hlsRef.current.destroyTimer = window.setTimeout(() => {
            if (hlsRef.current.instance !== hls) return;
            const standby = hlsRef.current.standby;
            hls.destroy();
            if (standby && standby !== hls) standby.destroy();
            hlsRef.current.instance = undefined;
            hlsRef.current.standby = undefined;
            hlsRef.current.standbyFresh = false;
            activeRef.current = { lastKeepaliveMs: 0 };
            hlsRef.current.destroyTimer = undefined;
          }, 0);
        };
      }

      // NATIVE HLS (Safari/iOS): hls.js is unsupported here BECAUSE the platform plays `.m3u8`
      // itself, doing its own ABR. Only reached when MSE is absent, so this is a genuine native-HLS
      // browser, not a Chromium false-positive.
      if (nativeHLS || video.canPlayType("application/vnd.apple.mpegurl")) {
        activeRef.current = { video, sourceURL: url, lastKeepaliveMs: Date.now() };
        const onManifest = () => playbackAttempt?.markPhase("manifest");
        // Native HLS has no transport controller to detach the previous URL for us.
        video.removeAttribute("src");
        video.load();
        if (requestFrame) video.addEventListener("loadeddata", onLoadedData, { once: true });
        else armFirstFrameWatch();
        video.addEventListener("loadedmetadata", onManifest, { once: true });
        video.src = url;
        void video.play().catch(() => {
          /* autoplay may be blocked; the play control covers it */
        });
        return () => {
          stopDiagnostics();
          stopFirstFrameWatch();
          video.removeEventListener("loadedmetadata", onManifest);
          if (activeRef.current.video === video) activeRef.current = { lastKeepaliveMs: 0 };
          video.removeAttribute("src");
          video.load();
        };
      }

      stopFirstFrameWatch();
      recordDiagnostic({
        ...diagnosticBase,
        event: "player.media_error",
        errorCode: "unsupported",
        fatal: true,
      });
      stopDiagnostics("retry");
      setState({ channelId, status: "error", error: "This browser can't play live channels." });
      return () => undefined;
    },
    [channelId, playbackAttempt, recordDiagnostic, returnLive],
  );

  const attach = useCallback(
    (video: HTMLVideoElement): (() => void) => {
      if (hlsRef.current.destroyTimer !== undefined) {
        window.clearTimeout(hlsRef.current.destroyTimer);
        hlsRef.current.destroyTimer = undefined;
      }
      const generation = ++generationRef.current;
      const controller = new AbortController();
      const current = () => generationRef.current === generation && !controller.signal.aborted;
      // Keep programme context on the frame the decoder advances to. The one-second transport
      // timer remains the stall/lag heartbeat; timeupdate makes ordinary playback boundary-accurate
      // instead of allowing the chrome to lead by as much as a polling interval.
      const onTimeUpdate = () => publishTransport();
      video.addEventListener("timeupdate", onTimeUpdate);
      setState({ channelId, status: "loading" });
      const at = Date.now();
      clockRef.current = {
        channelId,
        mode: "live",
        lagMs: 0,
        viewerTimeMs: at,
        noticeRevision: 0,
      };
      setTransportState({ channelId, value: liveStateAt(clockRef.current, at) });

      // The mint is the STANDALONE client function, not the useChannelPlayUrl() mutation hook, and
      // that choice is load-bearing: the mutation object gets a fresh identity every render, which
      // made `attach` unstable, which made VideoPlayer's `[attach]` effect re-fire on every render
      // and setState in a loop ("Maximum update depth exceeded"). The plain function has no such
      // identity, so `attach` depends only on the stable `channelId` and `bind`.
      let teardown: (() => void) | undefined;
      // Reuse an adjacent warmer's signed URL when present. Otherwise mint normally. Both paths
      // arrive at the same transport attachment; warming never creates a second player.
      const source = warmedPlayURL
        ? Promise.resolve({ url: warmedPlayURL, expiresAt: Number.POSITIVE_INFINITY })
        : mintSource(controller.signal);
      source
        .then((body) => {
          if (!current()) return;
          // The Web adapter prefers a relative same-origin form, which avoids CORS and works when
          // server.public_url is unset.
          const src = body?.url;
          if (!src) {
            setState({ channelId, status: "error", error: "Couldn't get a stream for this channel." });
            return;
          }
          return bind(video, src, current).then((nextTeardown) => {
            if (!current()) {
              nextTeardown();
              return;
            }
            teardown = nextTeardown;
          });
        })
        .catch((e) => {
          if (!current()) return;
          setState({
            channelId,
            status: "error",
            error: errorMessage(e),
          });
        });

      return () => {
        video.removeEventListener("timeupdate", onTimeUpdate);
        controller.abort();
        if (generationRef.current === generation) generationRef.current++;
        teardown?.();
      };
    },
    [channelId, bind, errorMessage, mintSource, publishTransport, warmedPlayURL],
  );

  const liveTransport = useMemo<BrowserLivePlaybackTransport>(
    () => ({
      state:
        transportState.channelId === channelId
          ? transportState.value
          : { mode: "live", lagSeconds: 0, viewerTimeMs: Date.now(), noticeRevision: 0 },
      play: playLive,
      pause: pauseLive,
      goLive: (video) => returnLive(video, false),
    }),
    [channelId, pauseLive, playLive, returnLive, transportState],
  );

  return { status, error, playbackSessionId: playbackSessionIDRef.current, attach, liveTransport };
}

export type {
  BrowserClientObservation,
  BrowserHlsPlayerOptions,
  BrowserLivePlaybackTransport,
  BrowserPlayerStatus,
  BrowserPlaySource,
  BrowserTuneAttempt,
  BrowserTunePhase,
  UseBrowserHlsPlayer,
};
export { useBrowserHlsPlayer };
