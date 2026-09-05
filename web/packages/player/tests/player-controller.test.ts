import {
  createPlayerController,
  type PlayerChannel,
  type PlayerSourcePort,
  type PlayerTransport,
  type PlayerTransportEvent,
  playableCatalog,
} from "@loomarr/player";
import { describe, expect, it, vi } from "vitest";

const channels: PlayerChannel[] = [
  { id: "thirty", inAppPlayable: true, name: "Thirty", number: 30 },
  { id: "ten", inAppPlayable: true, name: "Ten", number: 10 },
  { id: "twenty", inAppPlayable: false, name: "Twenty", number: 20 },
];

const deferred = <T>() => {
  let resolve!: (value: T) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((onResolve, onReject) => {
    resolve = onResolve;
    reject = onReject;
  });
  return { promise, reject, resolve };
};

const harness = (source?: PlayerSourcePort) => {
  const listeners = new Set<(event: PlayerTransportEvent) => void>();
  const transport: PlayerTransport = {
    dispose: vi.fn(),
    goLive: vi.fn(),
    pause: vi.fn(),
    play: vi.fn(),
    replace: vi.fn().mockResolvedValue(undefined),
    subscribe: vi.fn((listener) => {
      listeners.add(listener);
      return () => listeners.delete(listener);
    }),
  };
  const sourcePort: PlayerSourcePort =
    source ??
    ({
      mint: vi.fn((channel) => Promise.resolve({ uri: `https://loomarr.test/${channel.id}.m3u8` })),
    } satisfies PlayerSourcePort);
  const controller = createPlayerController({
    profile: { maxResolution: 2160 },
    source: sourcePort,
    transport,
  });
  return {
    controller,
    emit: (event: PlayerTransportEvent) => {
      for (const listener of listeners) listener(event);
    },
    source: sourcePort,
    transport,
  };
};

describe("player controller", () => {
  it("uses only server-declared playable channels with stable number ordering", () => {
    expect(playableCatalog(channels).map(({ id }) => id)).toEqual(["ten", "thirty"]);
  });

  it("starts deterministically, wraps, and tunes exact numbers without guessing", async () => {
    const { controller, source } = harness();
    await controller.reconcile(channels);
    expect(controller.getSnapshot()).toMatchObject({ channel: { id: "ten" }, status: "tuning" });

    await controller.step(-1);
    expect(controller.getSnapshot().channel?.id).toBe("thirty");
    await controller.step(1);
    expect(controller.getSnapshot().channel?.id).toBe("ten");

    vi.mocked(source.mint).mockClear();
    await controller.tuneNumber("3");
    await controller.tuneNumber("30x");
    expect(source.mint).not.toHaveBeenCalled();
    await controller.tuneNumber("030");
    expect(controller.getSnapshot()).toMatchObject({ channel: { id: "thirty" }, tuneReason: "number" });
  });

  it("can reconcile a catalog without tuning before viewer intent", async () => {
    const source: PlayerSourcePort = { mint: vi.fn() };
    const { transport } = harness(source);
    const controller = createPlayerController({
      initialTune: "none",
      profile: {},
      source,
      transport,
    });

    await controller.reconcile(channels);

    expect(controller.getSnapshot()).toMatchObject({ catalog: expect.any(Array), status: "idle" });
    expect(source.mint).not.toHaveBeenCalled();
    expect(transport.play).not.toHaveBeenCalled();
  });

  it("aborts an older mint and never lets it replace the latest request", async () => {
    const first = deferred<{ uri: string }>();
    const second = deferred<{ uri: string }>();
    const signals: AbortSignal[] = [];
    const source: PlayerSourcePort = {
      mint: vi.fn((_channel, _profile, signal) => {
        signals.push(signal);
        return signals.length === 1 ? first.promise : second.promise;
      }),
    };
    const { controller, transport } = harness(source);

    const initial = controller.reconcile(channels);
    const latest = controller.tuneChannel("thirty");
    expect(signals[0]?.aborted).toBe(true);

    second.resolve({ uri: "https://loomarr.test/thirty.m3u8" });
    await latest;
    first.resolve({ uri: "https://loomarr.test/ten.m3u8" });
    await initial;

    expect(transport.replace).toHaveBeenCalledTimes(1);
    expect(transport.replace).toHaveBeenCalledWith(
      { uri: "https://loomarr.test/thirty.m3u8" },
      expect.objectContaining({ attemptId: 2 }),
    );
  });

  it("reconciles by identity and falls back only when the tuned channel disappears", async () => {
    const { controller, source } = harness();
    await controller.reconcile(channels);
    await controller.tuneChannel("thirty");
    vi.mocked(source.mint).mockClear();

    await controller.reconcile([{ ...channels[0]!, name: "Thirty renamed" }, channels[1]!]);
    expect(controller.getSnapshot().channel).toMatchObject({ id: "thirty", name: "Thirty renamed" });
    expect(source.mint).not.toHaveBeenCalled();

    await controller.reconcile([channels[1]!]);
    expect(controller.getSnapshot()).toMatchObject({ channel: { id: "ten" }, tuneReason: "catalog" });
    expect(source.mint).toHaveBeenCalledTimes(1);
  });

  it("keeps bounded newest-first history and previous toggles through the tune seam", async () => {
    const many = Array.from({ length: 9 }, (_, index) => ({
      id: `channel-${index + 1}`,
      inAppPlayable: true,
      name: `Channel ${index + 1}`,
      number: index + 1,
    }));
    const { controller } = harness();
    await controller.reconcile(many);
    for (const channel of many.slice(1)) await controller.tuneChannel(channel.id);

    expect(controller.getSnapshot().recentChannelIds).toEqual([
      "channel-8",
      "channel-7",
      "channel-6",
      "channel-5",
      "channel-4",
      "channel-3",
    ]);
    await controller.previous();
    expect(controller.getSnapshot().channel?.id).toBe("channel-8");
    expect(controller.getSnapshot().previousChannelId).toBe("channel-9");
  });

  it("ignores stale transport events and retries a failed attempt only on explicit intent", async () => {
    const { controller, emit, source } = harness();
    await controller.reconcile(channels);
    const firstAttempt = controller.getSnapshot().attemptId!;
    await controller.tuneChannel("thirty");
    const latestAttempt = controller.getSnapshot().attemptId!;

    emit({ attemptId: firstAttempt, type: "first-frame" });
    expect(controller.getSnapshot().status).toBe("tuning");
    emit({
      attemptId: firstAttempt,
      state: { lagSeconds: 20, mode: "behind", noticeRevision: 1, viewerTimeMs: 1_000 },
      type: "live-state",
    });
    expect(controller.getSnapshot().livePlayback).toMatchObject({ mode: "live", noticeRevision: 0 });
    emit({ attemptId: latestAttempt, type: "paused" });
    expect(controller.getSnapshot().status).toBe("paused");
    emit({
      attemptId: latestAttempt,
      state: { lagSeconds: 20, mode: "paused", noticeRevision: 0, viewerTimeMs: 1_000 },
      type: "live-state",
    });
    expect(controller.getSnapshot().livePlayback).toEqual({
      lagSeconds: 20,
      mode: "paused",
      noticeRevision: 0,
      viewerTimeMs: 1_000,
    });
    emit({ attemptId: latestAttempt, type: "playing" });
    expect(controller.getSnapshot().status).toBe("playing");
    emit({
      attemptId: latestAttempt,
      state: { lagSeconds: 0, mode: "live", noticeRevision: 1, viewerTimeMs: 2_000 },
      type: "live-state",
    });
    expect(controller.getSnapshot().livePlayback).toEqual({
      lagSeconds: 0,
      mode: "live",
      noticeRevision: 1,
      viewerTimeMs: 2_000,
    });
    emit({ attemptId: latestAttempt, error: "decoder failed", type: "error" });
    expect(controller.getSnapshot()).toMatchObject({ error: "decoder failed", status: "failed" });
    expect(source.mint).toHaveBeenCalledTimes(2);

    await controller.retry();
    expect(controller.getSnapshot()).toMatchObject({ status: "tuning", tuneReason: "retry" });
    expect(source.mint).toHaveBeenCalledTimes(3);
  });

  it("resets every Channel tune to live and delegates playback intents", async () => {
    const now = vi.spyOn(Date, "now").mockReturnValueOnce(1_000).mockReturnValueOnce(2_000);
    const { controller, emit, transport } = harness();

    controller.pause();
    await controller.play();
    await controller.goLive();
    expect(transport.pause).not.toHaveBeenCalled();
    expect(transport.play).not.toHaveBeenCalled();
    expect(transport.goLive).not.toHaveBeenCalled();

    await controller.reconcile(channels);
    const firstAttempt = controller.getSnapshot().attemptId!;
    expect(controller.getSnapshot().livePlayback).toEqual({
      lagSeconds: 0,
      mode: "live",
      noticeRevision: 0,
      viewerTimeMs: 1_000,
    });
    emit({
      attemptId: firstAttempt,
      state: { lagSeconds: 30, mode: "behind", noticeRevision: 2, viewerTimeMs: 500 },
      type: "live-state",
    });

    controller.pause();
    expect(transport.pause).toHaveBeenCalledOnce();
    expect(controller.getSnapshot()).toMatchObject({
      error: undefined,
      livePlayback: { mode: "behind" },
      status: "paused",
    });
    await controller.play();
    await controller.goLive();
    expect(transport.play).toHaveBeenCalledTimes(2);
    expect(transport.goLive).toHaveBeenCalledOnce();

    await controller.tuneChannel("thirty");
    expect(controller.getSnapshot().livePlayback).toEqual({
      lagSeconds: 0,
      mode: "live",
      noticeRevision: 0,
      viewerTimeMs: 2_000,
    });

    controller.dispose();
    vi.mocked(transport.pause).mockClear();
    vi.mocked(transport.play).mockClear();
    vi.mocked(transport.goLive).mockClear();
    controller.pause();
    await controller.play();
    await controller.goLive();
    expect(transport.pause).not.toHaveBeenCalled();
    expect(transport.play).not.toHaveBeenCalled();
    expect(transport.goLive).not.toHaveBeenCalled();
    now.mockRestore();
  });

  it("never lets a pending replacement or first-frame callback override an intentional pause", async () => {
    const replacement = deferred<void>();
    const { controller, emit, transport } = harness();
    vi.mocked(transport.replace).mockReturnValue(replacement.promise);

    const tuning = controller.reconcile(channels);
    await vi.waitFor(() => expect(transport.replace).toHaveBeenCalledOnce());
    const attemptId = controller.getSnapshot().attemptId!;
    controller.pause();

    replacement.resolve();
    await tuning;
    expect(transport.play).not.toHaveBeenCalled();
    emit({ attemptId, type: "first-frame" });
    expect(controller.getSnapshot().status).toBe("paused");

    emit({ attemptId, type: "playing" });
    expect(controller.getSnapshot().status).toBe("playing");
  });

  it("surfaces source failures without entering an automatic retry loop", async () => {
    const source: PlayerSourcePort = {
      mint: vi
        .fn()
        .mockRejectedValueOnce(new Error("signed source unavailable"))
        .mockResolvedValueOnce({ uri: "https://loomarr.test/ten.m3u8" }),
    };
    const { controller } = harness(source);

    await controller.reconcile(channels);
    expect(controller.getSnapshot()).toMatchObject({
      error: "signed source unavailable",
      status: "failed",
    });
    expect(source.mint).toHaveBeenCalledOnce();

    await Promise.resolve();
    expect(source.mint).toHaveBeenCalledOnce();
    await controller.retry();
    expect(controller.getSnapshot()).toMatchObject({ status: "tuning", tuneReason: "retry" });
    expect(source.mint).toHaveBeenCalledTimes(2);
  });

  it("aborts tuning and pauses when the playable catalog becomes empty", async () => {
    const pending = deferred<{ uri: string }>();
    let signal: AbortSignal | undefined;
    const { controller, emit, transport } = harness({
      mint: vi.fn((_channel, _profile, nextSignal) => {
        signal = nextSignal;
        return pending.promise;
      }),
    });
    const tuning = controller.reconcile(channels);
    const attemptId = controller.getSnapshot().attemptId!;

    await controller.reconcile([]);
    expect(signal?.aborted).toBe(true);
    expect(transport.pause).toHaveBeenCalledOnce();
    expect(controller.getSnapshot()).toMatchObject({ catalog: [], status: "empty" });
    emit({ attemptId, type: "first-frame" });
    expect(controller.getSnapshot().status).toBe("empty");

    pending.resolve({ uri: "https://loomarr.test/late.m3u8" });
    await tuning;
    expect(transport.replace).not.toHaveBeenCalled();
  });

  it("synchronously aborts, unsubscribes, pauses, and releases on dispose", async () => {
    const pending = deferred<{ uri: string }>();
    let signal: AbortSignal | undefined;
    const { controller, emit, transport } = harness({
      mint: vi.fn((_channel, _profile, nextSignal) => {
        signal = nextSignal;
        return pending.promise;
      }),
    });
    const tuning = controller.reconcile(channels);
    const beforeDispose = controller.getSnapshot();

    controller.dispose();
    controller.dispose();
    expect(signal?.aborted).toBe(true);
    expect(transport.pause).toHaveBeenCalledOnce();
    expect(transport.dispose).toHaveBeenCalledOnce();
    emit({ attemptId: beforeDispose.attemptId!, type: "first-frame" });
    expect(controller.getSnapshot()).toBe(beforeDispose);

    pending.resolve({ uri: "https://loomarr.test/late.m3u8" });
    await tuning;
    expect(transport.replace).not.toHaveBeenCalled();
  });
});
