import type { PlayerTransportEvent } from "@loomarr/player";
import type { VideoPlayer } from "expo-video";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("expo-video", () => ({
  createVideoPlayer: vi.fn(),
  VideoView: vi.fn(),
}));

const { createNativePlayerTransport, pairedNativeImageSource } = await import("@loomarr/player/native");

type PlayerListener = (payload: never) => void;

const deferred = () => {
  let resolve!: () => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<void>((onResolve, onReject) => {
    resolve = onResolve;
    reject = onReject;
  });
  return { promise, reject, resolve };
};

const nativePlayer = () => {
  const listeners = new Map<string, PlayerListener>();
  const removals: ReturnType<typeof vi.fn>[] = [];
  const player = {
    addListener: vi.fn((event: string, listener: PlayerListener) => {
      listeners.set(event, listener);
      const remove = vi.fn(() => listeners.delete(event));
      removals.push(remove);
      return { remove };
    }),
    currentLiveTimestamp: null as number | null,
    currentOffsetFromLive: null as number | null,
    currentTime: 0,
    duration: 0,
    loop: true,
    pause: vi.fn(),
    play: vi.fn(),
    release: vi.fn(),
    replaceAsync: vi.fn().mockResolvedValue(undefined),
    seekBy: vi.fn(),
    showNowPlayingNotification: true,
    staysActiveInBackground: true,
    timeUpdateEventInterval: 0,
  };
  return {
    emit: (event: string, payload: unknown) => listeners.get(event)?.(payload as never),
    player: player as unknown as VideoPlayer,
    raw: player,
    removals,
  };
};

describe("Expo video transport", () => {
  beforeEach(() => vi.clearAllMocks());
  afterEach(() => vi.useRealTimers());

  it("configures foreground HLS playback and emits native state", async () => {
    const { emit, player, raw } = nativePlayer();
    const transport = createNativePlayerTransport(player);
    const events: PlayerTransportEvent[] = [];
    transport.subscribe((event) => events.push(event));

    await transport.replace(
      {
        headers: { Authorization: "Bearer signed-source" },
        uri: "https://loomarr.test/live.m3u8",
      },
      { attemptId: 7, signal: new AbortController().signal },
    );
    transport.firstFrame();
    emit("playingChange", { isPlaying: true });
    emit("statusChange", { error: { message: "decoder failed" }, status: "error" });

    expect(raw).toMatchObject({
      loop: false,
      showNowPlayingNotification: false,
      staysActiveInBackground: false,
      timeUpdateEventInterval: 0.25,
    });
    expect(raw.replaceAsync).toHaveBeenCalledWith({
      contentType: "hls",
      headers: { Authorization: "Bearer signed-source" },
      uri: "https://loomarr.test/live.m3u8",
      useCaching: false,
    });
    expect(events.map(({ type }) => type)).toEqual(["first-frame", "playing", "live-state", "error"]);
  });

  it("serializes replacements and skips an aborted request before it reaches native playback", async () => {
    const first = deferred();
    const { player, raw } = nativePlayer();
    raw.replaceAsync.mockImplementationOnce(() => first.promise).mockResolvedValue(undefined);
    const transport = createNativePlayerTransport(player);

    const firstReplace = transport.replace(
      { uri: "https://loomarr.test/first.m3u8" },
      { attemptId: 1, signal: new AbortController().signal },
    );
    await vi.waitFor(() => expect(raw.replaceAsync).toHaveBeenCalledOnce());

    const skipped = new AbortController();
    const skippedReplace = transport.replace(
      { uri: "https://loomarr.test/skipped.m3u8" },
      { attemptId: 2, signal: skipped.signal },
    );
    skipped.abort();
    const latestReplace = transport.replace(
      { uri: "https://loomarr.test/latest.m3u8" },
      { attemptId: 3, signal: new AbortController().signal },
    );

    first.resolve();
    await Promise.all([firstReplace, skippedReplace, latestReplace]);
    expect(raw.replaceAsync).toHaveBeenCalledTimes(2);
    expect(raw.replaceAsync.mock.calls.map(([source]) => source.uri)).toEqual([
      "https://loomarr.test/first.m3u8",
      "https://loomarr.test/latest.m3u8",
    ]);
  });

  it("recovers its replacement queue after native playback rejects a source", async () => {
    const { player, raw } = nativePlayer();
    raw.replaceAsync.mockRejectedValueOnce(new Error("native rejected source"));
    const transport = createNativePlayerTransport(player);

    await expect(
      transport.replace(
        { uri: "https://loomarr.test/bad.m3u8" },
        { attemptId: 1, signal: new AbortController().signal },
      ),
    ).rejects.toThrow("native rejected source");
    await transport.replace(
      { uri: "https://loomarr.test/good.m3u8" },
      { attemptId: 2, signal: new AbortController().signal },
    );

    expect(raw.replaceAsync).toHaveBeenCalledTimes(2);
  });

  it("pauses, detaches listeners, publishes player removal, and releases idempotently", async () => {
    const { emit, player, raw, removals } = nativePlayer();
    const transport = createNativePlayerTransport(player);
    const events = vi.fn();
    const playerChanges = vi.fn();
    transport.subscribe(events);
    transport.subscribePlayer(playerChanges);
    await transport.replace(
      { uri: "https://loomarr.test/live.m3u8" },
      { attemptId: 1, signal: new AbortController().signal },
    );

    transport.dispose();
    transport.dispose();
    emit("playingChange", { isPlaying: true });

    expect(transport.getPlayer()).toBeUndefined();
    expect(raw.pause).toHaveBeenCalledOnce();
    expect(raw.release).toHaveBeenCalledOnce();
    expect(removals).toHaveLength(3);
    expect(removals.every((remove) => remove.mock.calls.length === 1)).toBe(true);
    expect(playerChanges).toHaveBeenCalledOnce();
    expect(events).not.toHaveBeenCalled();
  });

  it("releases on suspension and reattaches a fresh foreground player", async () => {
    const first = nativePlayer();
    const second = nativePlayer();
    const transport = createNativePlayerTransport(first.player, () => second.player);
    const playerChanges = vi.fn();
    transport.subscribePlayer(playerChanges);

    transport.suspend();

    expect(first.raw.pause).toHaveBeenCalledOnce();
    expect(first.raw.release).toHaveBeenCalledOnce();
    expect(transport.getPlayer()).toBeUndefined();

    transport.resume();
    expect(transport.getPlayer()).toBe(second.player);
    expect(second.raw).toMatchObject({
      loop: false,
      showNowPlayingNotification: false,
      staysActiveInBackground: false,
      timeUpdateEventInterval: 0.25,
    });
    expect(playerChanges).toHaveBeenCalledTimes(2);

    await transport.replace(
      { uri: "https://loomarr.test/resumed.m3u8" },
      { attemptId: 9, signal: new AbortController().signal },
    );
    expect(second.raw.replaceAsync).toHaveBeenCalledWith(
      expect.objectContaining({ uri: "https://loomarr.test/resumed.m3u8" }),
    );
  });

  it("publishes a paused point, resumes behind live, and returns explicitly to live", async () => {
    vi.useFakeTimers();
    const now = new Date("2026-08-26T20:00:00Z");
    vi.setSystemTime(now);
    const { emit, player, raw } = nativePlayer();
    raw.currentLiveTimestamp = now.getTime() - 12_000;
    raw.currentOffsetFromLive = 12;
    const transport = createNativePlayerTransport(player);
    const events: PlayerTransportEvent[] = [];
    transport.subscribe((event) => events.push(event));
    await transport.replace(
      { uri: "https://loomarr.test/live.m3u8" },
      { attemptId: 4, signal: new AbortController().signal },
    );

    emit("timeUpdate", {
      bufferedPosition: 30,
      currentLiveTimestamp: raw.currentLiveTimestamp,
      currentOffsetFromLive: raw.currentOffsetFromLive,
      currentTime: 100,
    });
    transport.pause();
    vi.advanceTimersByTime(30_000);
    transport.play();

    expect(events).toContainEqual({ attemptId: 4, type: "paused" });
    expect(events).toContainEqual({
      attemptId: 4,
      state: {
        lagSeconds: 42,
        mode: "behind",
        noticeRevision: 0,
        viewerTimeMs: now.getTime() - 12_000,
      },
      type: "live-state",
    });

    transport.goLive();
    expect(raw.seekBy).toHaveBeenCalledWith(12);
    expect(events.at(-1)).toEqual({
      attemptId: 4,
      state: {
        lagSeconds: 0,
        mode: "live",
        noticeRevision: 0,
        viewerTimeMs: now.getTime() + 30_000,
      },
      type: "live-state",
    });
  });

  it("expires an old paused point once and resets the notice for a new Channel", async () => {
    vi.useFakeTimers();
    const now = new Date("2026-08-26T20:00:00Z");
    vi.setSystemTime(now);
    const { emit, player, raw } = nativePlayer();
    raw.currentLiveTimestamp = now.getTime() - 901_000;
    raw.currentOffsetFromLive = 901;
    const transport = createNativePlayerTransport(player);
    const events: PlayerTransportEvent[] = [];
    transport.subscribe((event) => events.push(event));
    await transport.replace(
      { uri: "https://loomarr.test/old.m3u8" },
      { attemptId: 8, signal: new AbortController().signal },
    );

    transport.pause();
    transport.play();
    expect(raw.seekBy).toHaveBeenCalledWith(901);
    expect(events.at(-1)).toEqual({
      attemptId: 8,
      state: { lagSeconds: 0, mode: "live", noticeRevision: 1, viewerTimeMs: now.getTime() },
      type: "live-state",
    });

    await transport.replace(
      { uri: "https://loomarr.test/new.m3u8" },
      { attemptId: 9, signal: new AbortController().signal },
    );
    emit("timeUpdate", {
      bufferedPosition: 0,
      currentLiveTimestamp: now.getTime(),
      currentOffsetFromLive: 0,
      currentTime: 0,
    });
    expect(events.at(-1)).toEqual({
      attemptId: 9,
      state: { lagSeconds: 0, mode: "live", noticeRevision: 0, viewerTimeMs: now.getTime() },
      type: "live-state",
    });
  });
});

describe("paired native image source", () => {
  const credential = { serverUrl: "http://loomarr.test:8080", token: "device-secret" };

  it("authenticates only same-origin image service paths", () => {
    expect(pairedNativeImageSource(credential, "/v1/images/poster.jpg")).toEqual({
      headers: { Authorization: "Bearer device-secret" },
      uri: "http://loomarr.test:8080/v1/images/poster.jpg",
    });
    expect(
      pairedNativeImageSource(
        { serverUrl: "http://loomarr.test:8080/", token: "device-secret" },
        "/v1/images/banner.jpg",
      ),
    ).toEqual({
      headers: { Authorization: "Bearer device-secret" },
      uri: "http://loomarr.test:8080/v1/images/banner.jpg",
    });
  });

  it("never sends the device token to an external image host", () => {
    expect(pairedNativeImageSource(credential, "https://cdn.example/poster.jpg")).toEqual({
      uri: "https://cdn.example/poster.jpg",
    });
    expect(pairedNativeImageSource(credential, "http://cdn.example/poster.jpg")).toBeUndefined();
    expect(pairedNativeImageSource(credential, "javascript:alert(1)")).toBeUndefined();
  });
});
