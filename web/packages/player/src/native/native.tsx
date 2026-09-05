import type { PairingCredential } from "@loomarr/core/pairing";
import { createVideoPlayer, type VideoPlayer, VideoView, type VideoViewProps } from "expo-video";
import { useSyncExternalStore } from "react";
import { Image, type ImageProps } from "react-native";
import type {
  LivePlaybackMode,
  LivePlaybackState,
  PlayerTransport,
  PlayerTransportEvent,
} from "../player-controller";
import type { PlayerSource } from "../player-source";

interface NativePlayerTransport extends PlayerTransport {
  /** Signals the first frame rendered by the native VideoView for the active attempt. */
  firstFrame: () => void;
  getPlayer: () => VideoPlayer | undefined;
  resume: () => void;
  subscribePlayer: (listener: () => void) => () => void;
  suspend: () => void;
}

interface NativePlayerViewProps {
  style?: VideoViewProps["style"];
  transport: NativePlayerTransport;
}

interface PairedNativeImageProps {
  credential: Pick<PairingCredential, "serverUrl" | "token">;
  resizeMode?: ImageProps["resizeMode"];
  style?: ImageProps["style"];
  uri: string;
}

const pairedNativeImageSource = (
  credential: Pick<PairingCredential, "serverUrl" | "token">,
  rawUrl: string,
): { headers?: { Authorization: string }; uri: string } | undefined => {
  try {
    const uri = new URL(rawUrl, `${credential.serverUrl}/`);
    if (uri.protocol !== "http:" && uri.protocol !== "https:") return undefined;
    if (uri.origin === new URL(credential.serverUrl).origin) {
      return { headers: { Authorization: `Bearer ${credential.token}` }, uri: uri.toString() };
    }
    return uri.protocol === "https:" ? { uri: uri.toString() } : undefined;
  } catch {
    return undefined;
  }
};

const LIVE_DVR_HORIZON_SECONDS = 15 * 60;

const createNativePlayerTransport = (
  initialPlayer: VideoPlayer,
  recreatePlayer?: () => VideoPlayer,
): NativePlayerTransport => {
  let disposed = false;
  let activeAttemptId: number | undefined;
  let player: VideoPlayer | undefined;
  let replacement = Promise.resolve();
  const listeners = new Set<(event: PlayerTransportEvent) => void>();
  const playerListeners = new Set<() => void>();
  let playingSubscription: { remove: () => void } | undefined;
  let statusSubscription: { remove: () => void } | undefined;
  let timeSubscription: { remove: () => void } | undefined;
  let liveMode: LivePlaybackMode = "live";
  let noticeRevision = 0;
  let viewerTimeMs = Date.now();

  const emit = (event: PlayerTransportEvent) => {
    if (disposed) return;
    for (const listener of listeners) listener(event);
  };

  const liveState = (
    currentLiveTimestamp: number | null = player?.currentLiveTimestamp ?? null,
    currentOffsetFromLive: number | null = player?.currentOffsetFromLive ?? null,
  ): LivePlaybackState => {
    const now = Date.now();
    if (currentLiveTimestamp !== null && Number.isFinite(currentLiveTimestamp)) {
      viewerTimeMs = currentLiveTimestamp;
    } else if (currentOffsetFromLive !== null && Number.isFinite(currentOffsetFromLive)) {
      viewerTimeMs = now - Math.max(0, currentOffsetFromLive) * 1_000;
    }
    return {
      lagSeconds: liveMode === "live" ? 0 : Math.max(0, Math.round((now - viewerTimeMs) / 1_000)),
      mode: liveMode,
      noticeRevision,
      viewerTimeMs,
    };
  };

  const emitLiveState = (currentLiveTimestamp?: number | null, currentOffsetFromLive?: number | null) => {
    if (activeAttemptId === undefined) return;
    emit({
      attemptId: activeAttemptId,
      state: liveState(currentLiveTimestamp, currentOffsetFromLive),
      type: "live-state",
    });
  };

  const attachPlayer = (next: VideoPlayer) => {
    player = next;
    next.loop = false;
    next.showNowPlayingNotification = false;
    next.staysActiveInBackground = false;
    next.timeUpdateEventInterval = 0.25;
    statusSubscription = next.addListener("statusChange", ({ error, status }) => {
      if (status === "error" && activeAttemptId !== undefined) {
        emit({
          attemptId: activeAttemptId,
          error: error?.message ?? "Native playback failed.",
          type: "error",
        });
      }
    });
    playingSubscription = next.addListener("playingChange", ({ isPlaying }) => {
      if (!isPlaying || activeAttemptId === undefined) return;
      emit({ attemptId: activeAttemptId, type: "playing" });
      emitLiveState();
    });
    timeSubscription = next.addListener("timeUpdate", ({ currentLiveTimestamp, currentOffsetFromLive }) => {
      emitLiveState(currentLiveTimestamp, currentOffsetFromLive);
    });
  };

  const releasePlayer = () => {
    const current = player;
    if (!current) return;
    current.pause();
    statusSubscription?.remove();
    playingSubscription?.remove();
    timeSubscription?.remove();
    statusSubscription = undefined;
    playingSubscription = undefined;
    timeSubscription = undefined;
    activeAttemptId = undefined;
    player = undefined;
    current.release();
    for (const listener of playerListeners) listener();
  };

  attachPlayer(initialPlayer);

  return {
    dispose: () => {
      if (disposed) return;
      disposed = true;
      releasePlayer();
      listeners.clear();
      playerListeners.clear();
    },
    firstFrame: () => {
      if (activeAttemptId !== undefined) emit({ attemptId: activeAttemptId, type: "first-frame" });
    },
    getPlayer: () => player,
    goLive: () => {
      if (!player) return;
      const offset = player.currentOffsetFromLive;
      if (offset !== null && Number.isFinite(offset) && offset > 0) {
        player.seekBy(offset);
      } else if (Number.isFinite(player.duration) && player.duration > 0) {
        player.currentTime = player.duration;
      }
      liveMode = "live";
      viewerTimeMs = Date.now();
      emitLiveState(null, 0);
      player.play();
    },
    pause: () => {
      if (!player) return;
      const timestamp = player.currentLiveTimestamp;
      const offset = player.currentOffsetFromLive;
      if (timestamp !== null && Number.isFinite(timestamp)) {
        viewerTimeMs = timestamp;
      } else if (offset !== null && Number.isFinite(offset)) {
        viewerTimeMs = Date.now() - Math.max(0, offset) * 1_000;
      }
      liveMode = "paused";
      player.pause();
      if (activeAttemptId !== undefined) emit({ attemptId: activeAttemptId, type: "paused" });
      emitLiveState();
    },
    play: () => {
      if (!player) return;
      if (liveMode === "paused") {
        const lagSeconds = Math.max(0, Math.round((Date.now() - viewerTimeMs) / 1_000));
        if (lagSeconds >= LIVE_DVR_HORIZON_SECONDS) {
          noticeRevision += 1;
          const offset = player.currentOffsetFromLive;
          if (offset !== null && Number.isFinite(offset) && offset > 0) {
            player.seekBy(offset);
          } else if (Number.isFinite(player.duration) && player.duration > 0) {
            player.currentTime = player.duration;
          }
          liveMode = "live";
          viewerTimeMs = Date.now();
          emitLiveState(null, 0);
        } else {
          liveMode = "behind";
          emitLiveState();
        }
      }
      player.play();
    },
    replace: async (source: PlayerSource, context: { attemptId: number; signal: AbortSignal }) => {
      const queued = replacement
        .catch(() => undefined)
        .then(async () => {
          if (disposed || context.signal.aborted) return;
          const current = player;
          if (!current) throw new Error("Native player is unavailable.");
          activeAttemptId = context.attemptId;
          liveMode = "live";
          noticeRevision = 0;
          viewerTimeMs = Date.now();
          await current.replaceAsync({
            contentType: "hls",
            headers: source.headers ? { ...source.headers } : undefined,
            uri: source.uri,
            useCaching: false,
          });
        });
      replacement = queued;
      await queued;
    },
    resume: () => {
      if (disposed || player) return;
      if (!recreatePlayer) throw new Error("Native player cannot resume without a player factory.");
      attachPlayer(recreatePlayer());
      liveMode = "live";
      noticeRevision = 0;
      viewerTimeMs = Date.now();
      for (const listener of playerListeners) listener();
    },
    subscribe: (listener) => {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    subscribePlayer: (listener) => {
      playerListeners.add(listener);
      return () => playerListeners.delete(listener);
    },
    suspend: releasePlayer,
  };
};

const createExpoVideoTransport = (): NativePlayerTransport =>
  createNativePlayerTransport(createVideoPlayer(null), () => createVideoPlayer(null));

const NativePlayerView = ({ style, transport }: NativePlayerViewProps) => {
  const player = useSyncExternalStore(transport.subscribePlayer, transport.getPlayer, transport.getPlayer);
  return player ? (
    <VideoView
      allowsPictureInPicture={false}
      allowsVideoFrameAnalysis={false}
      contentFit="contain"
      nativeControls={false}
      onFirstFrameRender={transport.firstFrame}
      player={player}
      startsPictureInPictureAutomatically={false}
      style={style}
      surfaceType="surfaceView"
    />
  ) : null;
};

const PairedNativeImage = ({ credential, resizeMode = "cover", style, uri }: PairedNativeImageProps) => {
  const source = pairedNativeImageSource(credential, uri);
  return source ? <Image resizeMode={resizeMode} source={source} style={style} /> : null;
};

export type { NativePlayerTransport, NativePlayerViewProps, PairedNativeImageProps };
export {
  createExpoVideoTransport,
  createNativePlayerTransport,
  NativePlayerView,
  PairedNativeImage,
  pairedNativeImageSource,
};
