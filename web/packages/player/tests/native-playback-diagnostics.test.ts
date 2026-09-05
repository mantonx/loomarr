import { describe, expect, it, vi } from "vitest";

vi.mock("expo-video", () => ({
  createVideoPlayer: vi.fn(),
  VideoView: vi.fn(),
}));

const { createNativePlaybackDiagnostics } = await import("@loomarr/player/native");

describe("native playback diagnostics", () => {
  it("records the closed lifecycle vocabulary without retaining native error prose", () => {
    const reporter = { flush: vi.fn(async () => undefined), record: vi.fn() };
    const diagnostics = createNativePlaybackDiagnostics(reporter, "native-1");

    diagnostics.transportEvent({ attemptId: 1, type: "first-frame" });
    diagnostics.channelChanged(undefined);
    diagnostics.channelChanged("seven");
    diagnostics.channelChanged("seven");
    diagnostics.transportEvent({ attemptId: 1, type: "first-frame" });
    diagnostics.channelChanged("eight");
    diagnostics.transportEvent({ attemptId: 2, error: "secret decoder detail", type: "error" });
    diagnostics.dispose();
    diagnostics.dispose();

    expect(reporter.record.mock.calls.map(([event]) => event)).toEqual([
      {
        channelId: "seven",
        event: "player.attached",
        playbackSessionId: "native-1",
        reason: "mount",
        transport: "native_hls",
      },
      {
        channelId: "seven",
        event: "player.ready",
        playbackSessionId: "native-1",
        transport: "native_hls",
      },
      {
        channelId: "eight",
        event: "player.source_replaced",
        playbackSessionId: "native-1",
        previousChannelId: "seven",
        reason: "channel_change",
        transport: "native_hls",
      },
      {
        channelId: "eight",
        errorCode: "native_transport_error",
        event: "player.media_error",
        fatal: false,
        playbackSessionId: "native-1",
        transport: "native_hls",
      },
      {
        channelId: "eight",
        event: "player.detached",
        playbackSessionId: "native-1",
        reason: "unmount",
        transport: "native_hls",
      },
    ]);
    expect(JSON.stringify(reporter.record.mock.calls)).not.toContain("secret decoder detail");
    expect(reporter.flush).toHaveBeenCalledOnce();
  });

  it("rejects an unbounded client-authored playback session identity", () => {
    const reporter = { flush: vi.fn(async () => undefined), record: vi.fn() };
    expect(() => createNativePlaybackDiagnostics(reporter, "")).toThrow("1-128 characters");
    expect(() => createNativePlaybackDiagnostics(reporter, "x".repeat(129))).toThrow("1-128 characters");
  });
});
