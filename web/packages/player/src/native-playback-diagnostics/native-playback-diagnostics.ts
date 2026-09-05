import type { ClientDiagnosticsReporter } from "@loomarr/core/client-diagnostics";
import type { PlayerTransportEvent } from "../player-controller";

type NativeDiagnosticsRecorder = Pick<ClientDiagnosticsReporter, "flush" | "record">;

interface NativePlaybackDiagnostics {
  channelChanged: (channelId: string | undefined) => void;
  dispose: () => void;
  transportEvent: (event: PlayerTransportEvent) => void;
}

const createNativePlaybackDiagnostics = (
  reporter: NativeDiagnosticsRecorder,
  playbackSessionId: string,
): NativePlaybackDiagnostics => {
  if (!playbackSessionId || playbackSessionId.length > 128) {
    throw new Error("Native playback session identity must contain 1-128 characters.");
  }
  let channelId: string | undefined;
  let disposed = false;

  return {
    channelChanged: (nextChannelId) => {
      if (disposed || !nextChannelId || nextChannelId === channelId) return;
      const previousChannelId = channelId;
      reporter.record(
        previousChannelId
          ? {
              channelId: nextChannelId,
              event: "player.source_replaced",
              playbackSessionId,
              previousChannelId,
              reason: "channel_change",
              transport: "native_hls",
            }
          : {
              channelId: nextChannelId,
              event: "player.attached",
              playbackSessionId,
              reason: "mount",
              transport: "native_hls",
            },
      );
      channelId = nextChannelId;
    },
    dispose: () => {
      if (disposed) return;
      disposed = true;
      if (channelId) {
        reporter.record({
          channelId,
          event: "player.detached",
          playbackSessionId,
          reason: "unmount",
          transport: "native_hls",
        });
        void reporter.flush();
      }
    },
    transportEvent: (event) => {
      if (disposed || !channelId) return;
      if (event.type === "first-frame") {
        reporter.record({
          channelId,
          event: "player.ready",
          playbackSessionId,
          transport: "native_hls",
        });
      } else if (event.type === "error") {
        reporter.record({
          channelId,
          errorCode: "native_transport_error",
          event: "player.media_error",
          fatal: false,
          playbackSessionId,
          transport: "native_hls",
        });
      }
    },
  };
};

export type { NativeDiagnosticsRecorder, NativePlaybackDiagnostics };
export { createNativePlaybackDiagnostics };
