import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { hlsMock as hls } from "@/test/hls.mock";

// useHlsPlayer's core (bind) drives hls.js over Media Source Extensions, which jsdom cannot run. So
// the seams tested here are the ones that are OURS and reachable without a media pipeline:
//  - the mint: attach() POSTs for a signed play-url and carries a device quality hint;
//  - the failure surfaces: a missing URL and a rejected mint both land on status "error";
//  - the transport fallthrough: with neither hls.js NOR native HLS available, we say so plainly.
// Real playback (MANIFEST_PARSED → playing) is left to the browser, where V46 verified it live.

// Hoisted so the vi.mock factory (which runs before module init) can close over them.
const { channelPlayUrl, unwrap } = vi.hoisted(() => ({
  channelPlayUrl: vi.fn(),
  unwrap: vi.fn((res: unknown) => res),
}));
vi.mock("@loomarr/api/endpoints/channels", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@loomarr/api/endpoints/channels")>();
  return {
    ...actual,
    channelPlayUrl,
  };
});
vi.mock("@loomarr/api/unwrap", () => ({ unwrap }));
vi.mock("@loomarr/api/mutator", () => ({
  toProblem: (e: unknown) => ({ detail: (e as Error)?.message, title: "Problem" }),
}));
vi.mock("@/diagnostics/client-reporter", () => ({ clientDiagnostics: { record: vi.fn() } }));

import { useHlsPlayer } from "./use-hls-player";

const videoEl = (canPlay = "") =>
  ({
    canPlayType: () => canPlay,
    dataset: {},
    poster: "",
    currentTime: 0,
    paused: false,
    pause: vi.fn(),
    seekable: { length: 0, start: vi.fn(), end: vi.fn() },
    play: vi.fn().mockResolvedValue(undefined),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    removeAttribute: vi.fn(),
    querySelectorAll: vi.fn().mockReturnValue([]),
    load: vi.fn(),
  }) as unknown as HTMLVideoElement;

describe("useHlsPlayer", () => {
  beforeEach(() => {
    channelPlayUrl.mockReset();
    hls.supported = false;
    hls.instances.length = 0;
    unwrap.mockReset().mockImplementation((res: unknown) => res);
    vi.spyOn(navigator, "userAgent", "get").mockReturnValue("Mozilla/5.0 Firefox/142.0");
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it("mints a signed play-url for the channel, with a device quality hint", async () => {
    channelPlayUrl.mockResolvedValue({ relativeUrl: "/v1/playout/hls/master.m3u8" });
    const { result } = renderHook(() => useHlsPlayer("ch-1"));

    act(() => {
      result.current.attach(videoEl("application/vnd.apple.mpegurl"));
    });

    await waitFor(() => expect(channelPlayUrl).toHaveBeenCalledOnce());
    // The mint now carries the client's DeviceProfile body (§9.1 V48) plus the quality hint. The
    // profile shape is what device-profile.ts probes; here we assert the call CONTRACT — a profile
    // object first, then the quality params — not the specific capabilities (those depend on the JS
    // env's MediaSource, exercised in device-profile's own test).
    expect(channelPlayUrl).toHaveBeenCalledWith(
      "ch-1",
      expect.objectContaining({
        video: expect.any(Array),
        audio: expect.any(Array),
        video10bit: expect.any(Boolean),
      }),
      { quality: expect.stringMatching(/^(auto|720|480)$/) },
      { signal: expect.any(AbortSignal) },
    );
  });

  it("reuses an adjacent warmer's exact signed URL without minting again", async () => {
    const video = videoEl("application/vnd.apple.mpegurl");
    const { result } = renderHook(() =>
      useHlsPlayer("ch-2", {
        id: 2,
        adjacent: true,
        warmed: true,
        playURL: "/v1/playout/hls/ch-2/master.m3u8?sig=warmed",
      }),
    );

    act(() => {
      result.current.attach(video);
    });
    await waitFor(() => expect(video.play).toHaveBeenCalledOnce());
    expect(channelPlayUrl).not.toHaveBeenCalled();
    expect(video.src).toContain("sig=warmed");
  });

  it("prefers native HLS on Apple WebKit even when hls.js MSE is available", async () => {
    vi.spyOn(navigator, "userAgent", "get").mockReturnValue(
      "Mozilla/5.0 AppleWebKit/605.1.15 Version/18.6 Safari/605.1.15",
    );
    hls.supported = true;
    channelPlayUrl.mockResolvedValue({ relativeUrl: "/v1/playout/hls/ch-1/master.m3u8" });
    const video = videoEl("probably");
    const { result } = renderHook(() => useHlsPlayer("ch-1"));

    act(() => {
      result.current.attach(video);
    });

    await waitFor(() => expect(video.play).toHaveBeenCalledOnce());
    expect(video.src).toBe("/v1/playout/hls/ch-1/master.m3u8");
    expect(hls.instances).toHaveLength(0);
  });

  it("keeps the completed tune attachment stable when route state drops its attempt", () => {
    const attempt = {
      id: 2,
      adjacent: true,
      warmed: true,
      playURL: "/v1/playout/hls/ch-2/master.m3u8?sig=warmed",
    };
    const { result, rerender } = renderHook(({ currentAttempt }) => useHlsPlayer("ch-2", currentAttempt), {
      initialProps: { currentAttempt: attempt as typeof attempt | undefined },
    });
    const attach = result.current.attach;

    rerender({ currentAttempt: undefined });

    expect(result.current.attach).toBe(attach);
  });

  it("keeps one active controller and a fresh one-source standby across repeated tunes", async () => {
    hls.supported = true;
    channelPlayUrl.mockImplementation((id: string) =>
      Promise.resolve({ relativeUrl: `/v1/playout/hls/${id}/master.m3u8` }),
    );
    const frames: Array<() => void> = [];
    const video = {
      ...videoEl(),
      requestVideoFrameCallback: vi.fn((callback: () => void) => {
        frames.push(callback);
        return frames.length;
      }),
      cancelVideoFrameCallback: vi.fn(),
    } as unknown as HTMLVideoElement;
    const { result, rerender } = renderHook(({ id }) => useHlsPlayer(id), {
      initialProps: { id: "ch-1" },
    });

    let release!: () => void;
    act(() => {
      release = result.current.attach(video);
    });
    await waitFor(() => expect(hls.instances).toHaveLength(1));
    const first = hls.instances[0] as {
      destroy: ReturnType<typeof vi.fn>;
      loadSource: ReturnType<typeof vi.fn>;
      on: ReturnType<typeof vi.fn>;
      stopLoad: ReturnType<typeof vi.fn>;
      transferMedia: ReturnType<typeof vi.fn>;
    };
    await waitFor(() => expect(first.loadSource).toHaveBeenCalledWith("/v1/playout/hls/ch-1/master.m3u8"));
    expect(video.dataset.playbackChannel).toBe("ch-1");

    const firstFragment = first.on.mock.calls.find((call: unknown[]) => call[0] === "fragBuffered")?.[1] as
      | (() => void)
      | undefined;
    act(() => firstFragment?.());
    act(() => frames.shift()?.());
    await waitFor(() => expect(hls.instances).toHaveLength(2));
    const freshSecond = hls.instances[1] as {
      attachMedia: ReturnType<typeof vi.fn>;
      destroy: ReturnType<typeof vi.fn>;
      loadSource: ReturnType<typeof vi.fn>;
      on: ReturnType<typeof vi.fn>;
    };
    expect(freshSecond.loadSource).not.toHaveBeenCalled();

    act(() => release());
    rerender({ id: "ch-2" });
    let finalRelease!: () => void;
    act(() => {
      finalRelease = result.current.attach(video);
    });

    await waitFor(() =>
      expect(freshSecond.loadSource).toHaveBeenCalledWith("/v1/playout/hls/ch-2/master.m3u8"),
    );
    expect(video.dataset.playbackChannel).toBe("ch-2");
    expect(first.stopLoad).toHaveBeenCalled();
    expect(first.transferMedia).toHaveBeenCalledOnce();
    expect(first.destroy).not.toHaveBeenCalled();
    expect(freshSecond.attachMedia).toHaveBeenLastCalledWith(video);

    const secondFragment = freshSecond.on.mock.calls.find(
      (call: unknown[]) => call[0] === "fragBuffered",
    )?.[1] as (() => void) | undefined;
    act(() => secondFragment?.());
    act(() => frames.shift()?.());
    await waitFor(() => expect(first.destroy).toHaveBeenCalledOnce());
    await waitFor(() => expect(hls.instances).toHaveLength(3));
    const freshThird = hls.instances[2] as {
      destroy: ReturnType<typeof vi.fn>;
      loadSource: ReturnType<typeof vi.fn>;
    };
    expect(freshThird.loadSource).not.toHaveBeenCalled();
    expect(
      hls.instances.filter(
        (instance) => !(instance as { destroy: ReturnType<typeof vi.fn> }).destroy.mock.calls.length,
      ),
    ).toHaveLength(2);

    act(() => finalRelease());
    rerender({ id: "ch-3" });
    let thirdRelease!: () => void;
    act(() => {
      thirdRelease = result.current.attach(video);
    });
    await waitFor(() =>
      expect(freshThird.loadSource).toHaveBeenCalledWith("/v1/playout/hls/ch-3/master.m3u8"),
    );
    expect(hls.instances).toHaveLength(3);
    for (const instance of hls.instances) {
      expect((instance as { loadSource: ReturnType<typeof vi.fn> }).loadSource).toHaveBeenCalledTimes(
        instance === freshThird ? 1 : instance === freshSecond || instance === first ? 1 : 0,
      );
    }

    act(() => thirdRelease());
    await waitFor(() => expect(freshSecond.destroy).toHaveBeenCalledOnce());
    await waitFor(() => expect(freshThird.destroy).toHaveBeenCalledOnce());
  });

  it("reuses compatible open SourceBuffers across channel tunes", async () => {
    hls.supported = true;
    channelPlayUrl.mockImplementation((id: string) =>
      Promise.resolve({ relativeUrl: `/v1/playout/hls/${id}/master.m3u8` }),
    );
    let replacementLoadedMetadata!: () => void;
    let replacementLoadedData!: () => void;
    let replacementCanPlay!: () => void;
    let finishOutgoingSeek!: () => void;
    let finishTargetFrame!: () => void;
    const video = {
      ...videoEl(),
      addEventListener: vi.fn((event: string, callback: () => void) => {
        if (event === "loadedmetadata") replacementLoadedMetadata = callback;
        if (event === "loadeddata") replacementLoadedData = callback;
        if (event === "canplay") replacementCanPlay = callback;
        if (event === "seeked") finishOutgoingSeek = callback;
      }),
      requestVideoFrameCallback: vi.fn((callback: () => void) => {
        finishTargetFrame = callback;
        return 1;
      }),
      cancelVideoFrameCallback: vi.fn(),
    } as unknown as HTMLVideoElement;
    const resetCurrentTime = vi.fn();
    Object.defineProperty(video, "currentTime", {
      configurable: true,
      get: () => 0.5,
      set: resetCurrentTime,
    });
    const { result, rerender } = renderHook(({ id }) => useHlsPlayer(id), {
      initialProps: { id: "ch-1" },
    });

    let release!: () => void;
    act(() => {
      release = result.current.attach(video);
    });
    await waitFor(() => expect(hls.instances).toHaveLength(1));
    const controller = hls.instances[0] as {
      attachMedia: ReturnType<typeof vi.fn>;
      destroy: ReturnType<typeof vi.fn>;
      loadSource: ReturnType<typeof vi.fn>;
      transferMedia: ReturnType<typeof vi.fn>;
    };
    await waitFor(() =>
      expect(controller.loadSource).toHaveBeenCalledWith("/v1/playout/hls/ch-1/master.m3u8"),
    );
    vi.mocked(video.requestVideoFrameCallback).mockClear();
    vi.mocked(video.play).mockClear();

    let finishRemoval!: () => void;
    const remove = vi.fn();
    const transferred = {
      media: video,
      mediaSource: { readyState: "open" },
      tracks: {
        video: {
          buffer: {
            updating: false,
            buffered: { length: 1, start: () => 0, end: () => 1 },
            addEventListener: vi.fn((event: string, callback: () => void) => {
              if (event === "updateend") finishRemoval = callback;
            }),
            removeEventListener: vi.fn(),
            remove,
          },
        },
      },
    };
    controller.transferMedia.mockReturnValue(transferred);
    act(() => release());
    rerender({ id: "ch-2" });
    act(() => {
      result.current.attach(video);
    });

    await waitFor(() => expect(finishOutgoingSeek).toBeTypeOf("function"));
    expect(video.pause).toHaveBeenCalledOnce();
    // The exact range end is not presentable in WebKit. Seek to the final frame interval instead,
    // and do not start removal until the media element acknowledges that in-range seek.
    expect(resetCurrentTime).toHaveBeenLastCalledWith(0.95);
    expect(remove).not.toHaveBeenCalled();
    act(() => finishOutgoingSeek());
    await waitFor(() => expect(remove).toHaveBeenCalledWith(0, 1));
    expect(hls.instances).toHaveLength(2);
    expect(controller.destroy).not.toHaveBeenCalled();
    expect(video.play).not.toHaveBeenCalled();
    const replacement = hls.instances[1] as {
      attachMedia: ReturnType<typeof vi.fn>;
      config: { autoStartLoad?: boolean; enableWorker?: boolean };
      loadSource: ReturnType<typeof vi.fn>;
      on: ReturnType<typeof vi.fn>;
      startLoad: ReturnType<typeof vi.fn>;
    };
    expect(replacement.attachMedia).not.toHaveBeenCalled();
    expect(replacement.config.autoStartLoad).toBe(false);
    // Baseline HLS carries MPEG-TS segments, so transmuxing must stay off the UI thread. hls.js
    // shares and reference-counts its worker across source-scoped controllers; the bounded
    // active/standby pool must preserve that production invariant while disabling auto-load.
    expect(replacement.config.enableWorker).toBe(true);
    expect(replacement.loadSource).not.toHaveBeenCalled();
    expect(replacement.startLoad).not.toHaveBeenCalled();
    expect(video.play).not.toHaveBeenCalled();

    act(() => finishRemoval());
    await waitFor(() => expect(resetCurrentTime).toHaveBeenCalledWith(0));
    await waitFor(() => expect(replacement.attachMedia).toHaveBeenLastCalledWith(transferred));
    await waitFor(() => expect(video.requestVideoFrameCallback).toHaveBeenCalledOnce());
    await waitFor(() =>
      expect(replacement.loadSource).toHaveBeenCalledWith("/v1/playout/hls/ch-2/master.m3u8"),
    );
    await waitFor(() => expect(replacement.startLoad).toHaveBeenCalledOnce());
    expect(replacement.attachMedia.mock.invocationCallOrder.at(-1)).toBeLessThan(
      replacement.loadSource.mock.invocationCallOrder.at(-1) ?? 0,
    );
    expect(replacement.loadSource.mock.invocationCallOrder.at(-1)).toBeLessThan(
      replacement.startLoad.mock.invocationCallOrder.at(-1) ?? 0,
    );
    expect(vi.mocked(video.requestVideoFrameCallback).mock.invocationCallOrder.at(-1)).toBeLessThan(
      replacement.startLoad.mock.invocationCallOrder.at(-1) ?? 0,
    );
    // A cached WebKit append can decode before MANIFEST_PARSED is delivered. The target must have
    // a pending playback join before media loading starts so its first decoded frame cannot surface
    // paused; later manifest/fragment/loadeddata joins still cover WebKit's queued load resets.
    await waitFor(() => expect(video.play).toHaveBeenCalledOnce());
    expect(replacement.loadSource.mock.invocationCallOrder.at(-1)).toBeLessThan(
      vi.mocked(video.play).mock.invocationCallOrder.at(-1) ?? 0,
    );
    expect(vi.mocked(video.play).mock.invocationCallOrder.at(-1)).toBeLessThan(
      replacement.startLoad.mock.invocationCallOrder.at(-1) ?? 0,
    );
    vi.mocked(video.play).mockClear();
    expect(replacementLoadedMetadata).toBeTypeOf("function");
    replacementLoadedMetadata();
    expect(video.play).toHaveBeenCalledOnce();
    vi.mocked(video.play).mockClear();
    const manifestParsed = replacement.on.mock.calls
      .filter((call: unknown[]) => call[0] === "manifestParsed")
      .at(-1)?.[1] as (() => void) | undefined;
    expect(manifestParsed).toBeTypeOf("function");
    manifestParsed?.();
    expect(resetCurrentTime).toHaveBeenLastCalledWith(98);
    await waitFor(() => expect(video.play).toHaveBeenCalledOnce());

    resetCurrentTime.mockClear();
    const levelUpdated = replacement.on.mock.calls
      .filter((call: unknown[]) => call[0] === "levelUpdated")
      .at(-1)?.[1] as (() => void) | undefined;
    expect(levelUpdated).toBeTypeOf("function");
    levelUpdated?.();
    expect(resetCurrentTime).toHaveBeenLastCalledWith(98);

    finishTargetFrame();
    resetCurrentTime.mockClear();
    levelUpdated?.();
    expect(resetCurrentTime).not.toHaveBeenCalled();

    vi.mocked(video.play).mockClear();
    const fragmentBuffered = replacement.on.mock.calls
      .filter((call: unknown[]) => call[0] === "fragBuffered")
      .at(-1)?.[1] as (() => void) | undefined;
    expect(fragmentBuffered).toBeTypeOf("function");
    fragmentBuffered?.();
    expect(video.play).toHaveBeenCalledOnce();

    vi.mocked(video.play).mockClear();
    replacementLoadedData();
    expect(video.play).toHaveBeenCalledOnce();
    replacementLoadedData();
    expect(video.play).toHaveBeenCalledOnce();

    vi.mocked(video.play).mockClear();
    expect(replacementCanPlay).toBeTypeOf("function");
    replacementCanPlay();
    expect(video.play).toHaveBeenCalledOnce();
  });

  it("does not wait for a seek event when playback is already at the buffered edge", async () => {
    hls.supported = true;
    channelPlayUrl.mockImplementation((id: string) =>
      Promise.resolve({ relativeUrl: `/v1/playout/hls/${id}/master.m3u8` }),
    );
    let mediaTime = 4.041666;
    let seeking = false;
    const video = {
      ...videoEl(),
      requestVideoFrameCallback: vi.fn(() => 1),
      cancelVideoFrameCallback: vi.fn(),
    } as unknown as HTMLVideoElement;
    Object.defineProperty(video, "currentTime", {
      configurable: true,
      get: () => mediaTime,
      // Chromium, Firefox, and WebKit all clamp this fixture's exact half-open range end to the last
      // decoded timestamp and can remain in `seeking` without ever dispatching `seeked`.
      set: () => {
        mediaTime = 4.083332;
        seeking = true;
      },
    });
    Object.defineProperty(video, "seeking", {
      configurable: true,
      get: () => seeking,
    });
    const { result, rerender } = renderHook(({ id }) => useHlsPlayer(id), {
      initialProps: { id: "ch-3" },
    });

    let release!: () => void;
    act(() => {
      release = result.current.attach(video);
    });
    await waitFor(() => expect(hls.instances).toHaveLength(1));
    const first = hls.instances[0] as {
      loadSource: ReturnType<typeof vi.fn>;
      transferMedia: ReturnType<typeof vi.fn>;
    };
    await waitFor(() => expect(first.loadSource).toHaveBeenCalledWith("/v1/playout/hls/ch-3/master.m3u8"));

    let finishRemoval!: () => void;
    const remove = vi.fn();
    first.transferMedia.mockReturnValue({
      media: video,
      mediaSource: { readyState: "open" },
      tracks: {
        video: {
          buffer: {
            updating: false,
            buffered: { length: 1, start: () => 0, end: () => 4.083333333333333 },
            addEventListener: vi.fn((event: string, callback: () => void) => {
              if (event === "updateend") finishRemoval = callback;
            }),
            removeEventListener: vi.fn(),
            remove,
          },
        },
      },
    });

    act(() => release());
    rerender({ id: "ch-17" });
    act(() => {
      result.current.attach(video);
    });

    // Browser media clocks round the same frame edge slightly differently from TimeRanges. Setting
    // the range end clamps to an effectively identical decoded timestamp without starting a seek,
    // so the handoff must proceed instead of waiting forever before HLS attachment.
    await waitFor(() => expect(remove).toHaveBeenCalledWith(0, 4.083333333333333));
    expect(video.seeking).toBe(false);
    act(() => finishRemoval());
    const replacement = hls.instances[1] as { loadSource: ReturnType<typeof vi.fn> };
    await waitFor(() =>
      expect(replacement.loadSource).toHaveBeenCalledWith("/v1/playout/hls/ch-17/master.m3u8"),
    );
  });

  it("uses the bounded fresh-MSE branch for an open WebKit handoff", async () => {
    hls.supported = true;
    vi.spyOn(navigator, "userAgent", "get").mockReturnValue(
      "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 Version/18.0 Safari/605.1.15",
    );
    channelPlayUrl.mockImplementation((id: string) =>
      Promise.resolve({ relativeUrl: `/v1/playout/hls/${id}/master.m3u8` }),
    );
    let seekListenerInstalled = false;
    const video = {
      ...videoEl(),
      addEventListener: vi.fn((event: string) => {
        if (event === "seeked") seekListenerInstalled = true;
      }),
      requestVideoFrameCallback: vi.fn(() => 1),
      cancelVideoFrameCallback: vi.fn(),
    } as unknown as HTMLVideoElement;
    video.src = "blob:http://localhost/webkit-open-source";
    Object.defineProperty(video, "currentSrc", {
      configurable: true,
      get: () => video.src,
    });
    Object.defineProperty(video, "currentTime", {
      configurable: true,
      get: () => 0.5,
      set: vi.fn(),
    });
    const revokeObjectURL = vi.spyOn(URL, "revokeObjectURL");
    const { result, rerender } = renderHook(({ id }) => useHlsPlayer(id), {
      initialProps: { id: "ch-1" },
    });

    let release!: () => void;
    act(() => {
      release = result.current.attach(video);
    });
    await waitFor(() => expect(hls.instances).toHaveLength(1));
    const controller = hls.instances[0] as {
      loadSource: ReturnType<typeof vi.fn>;
      transferMedia: ReturnType<typeof vi.fn>;
    };
    await waitFor(() =>
      expect(controller.loadSource).toHaveBeenCalledWith("/v1/playout/hls/ch-1/master.m3u8"),
    );
    const remove = vi.fn();
    controller.transferMedia.mockReturnValue({
      media: video,
      mediaSource: { readyState: "open" },
      tracks: {
        video: {
          buffer: {
            updating: false,
            buffered: { length: 1, start: () => 0, end: () => 4 },
            addEventListener: vi.fn(),
            removeEventListener: vi.fn(),
            remove,
          },
        },
      },
    });

    act(() => release());
    rerender({ id: "ch-2" });
    act(() => {
      result.current.attach(video);
    });

    await waitFor(() => expect(hls.instances).toHaveLength(2));
    const replacement = hls.instances[1] as {
      attachMedia: ReturnType<typeof vi.fn>;
      loadSource: ReturnType<typeof vi.fn>;
      startLoad: ReturnType<typeof vi.fn>;
    };
    await waitFor(() => expect(replacement.attachMedia).toHaveBeenCalledWith(video));
    await waitFor(() =>
      expect(replacement.loadSource).toHaveBeenCalledWith("/v1/playout/hls/ch-2/master.m3u8"),
    );
    expect(replacement.startLoad).toHaveBeenCalledOnce();
    expect(replacement.loadSource.mock.invocationCallOrder.at(-1)).toBeLessThan(
      replacement.attachMedia.mock.invocationCallOrder.at(-1) ?? 0,
    );
    expect(video.pause).not.toHaveBeenCalled();
    expect(seekListenerInstalled).toBe(false);
    expect(remove).not.toHaveBeenCalled();
    expect(video.load).not.toHaveBeenCalled();
    expect(revokeObjectURL).toHaveBeenCalledWith("blob:http://localhost/webkit-open-source");
  });

  it("replaces an ended MediaSource without waiting on its decoded range", async () => {
    hls.supported = true;
    const requestAnimationFrame = vi.fn();
    vi.stubGlobal("requestAnimationFrame", requestAnimationFrame);
    channelPlayUrl.mockImplementation((id: string) =>
      Promise.resolve({ relativeUrl: `/v1/playout/hls/${id}/master.m3u8` }),
    );
    const video = videoEl();
    video.src = "blob:http://localhost/ended-source";
    Object.defineProperty(video, "currentSrc", {
      configurable: true,
      get: () => video.src,
    });
    const source = { src: video.src, remove: vi.fn() };
    vi.mocked(video.querySelectorAll).mockReturnValue([source] as unknown as NodeListOf<HTMLSourceElement>);
    const revokeObjectURL = vi.spyOn(URL, "revokeObjectURL");
    const { result, rerender } = renderHook(({ id }) => useHlsPlayer(id), {
      initialProps: { id: "ch-1" },
    });

    let release!: () => void;
    act(() => {
      release = result.current.attach(video);
    });
    await waitFor(() => expect(hls.instances).toHaveLength(1));
    const controller = hls.instances[0] as {
      loadSource: ReturnType<typeof vi.fn>;
      transferMedia: ReturnType<typeof vi.fn>;
    };
    await waitFor(() =>
      expect(controller.loadSource).toHaveBeenCalledWith("/v1/playout/hls/ch-1/master.m3u8"),
    );
    const remove = vi.fn();
    controller.transferMedia.mockReturnValue({
      media: video,
      mediaSource: { readyState: "ended" },
      tracks: {
        video: {
          buffer: {
            updating: false,
            buffered: { length: 1, start: () => 0, end: () => 4 },
            addEventListener: vi.fn(),
            removeEventListener: vi.fn(),
            remove,
          },
        },
      },
    });

    act(() => release());
    rerender({ id: "ch-2" });
    act(() => {
      result.current.attach(video);
    });

    await waitFor(() => expect(hls.instances).toHaveLength(2));
    const replacement = hls.instances[1] as {
      attachMedia: ReturnType<typeof vi.fn>;
      loadSource: ReturnType<typeof vi.fn>;
      startLoad: ReturnType<typeof vi.fn>;
    };
    await waitFor(() => expect(replacement.attachMedia).toHaveBeenCalledWith(video));
    await waitFor(() =>
      expect(replacement.loadSource).toHaveBeenCalledWith("/v1/playout/hls/ch-2/master.m3u8"),
    );
    expect(replacement.startLoad).toHaveBeenCalledOnce();
    expect(requestAnimationFrame).not.toHaveBeenCalled();
    expect(remove).not.toHaveBeenCalled();
    expect(video.removeAttribute).toHaveBeenCalledWith("src");
    expect(source.remove).toHaveBeenCalledOnce();
    expect(video.load).toHaveBeenCalledOnce();
    expect(revokeObjectURL).toHaveBeenCalledWith("blob:http://localhost/ended-source");
    expect(vi.mocked(video.load).mock.invocationCallOrder.at(-1)).toBeLessThan(
      replacement.attachMedia.mock.invocationCallOrder.at(-1) ?? 0,
    );
  });

  it("does not attach or load a superseded generation after its transferred clear finishes", async () => {
    hls.supported = true;
    channelPlayUrl.mockImplementation((id: string) =>
      Promise.resolve({ relativeUrl: `/v1/playout/hls/${id}/master.m3u8` }),
    );
    let finishOutgoingSeek!: () => void;
    let mediaTime = 0.5;
    const video = videoEl();
    vi.mocked(video.addEventListener).mockImplementation((event, callback) => {
      if (event === "seeked") finishOutgoingSeek = callback as () => void;
    });
    Object.defineProperty(video, "currentTime", {
      configurable: true,
      get: () => mediaTime,
      set: (value: number) => {
        mediaTime = value;
      },
    });
    const { result, rerender } = renderHook(({ id }) => useHlsPlayer(id), {
      initialProps: { id: "ch-1" },
    });

    let firstRelease!: () => void;
    act(() => {
      firstRelease = result.current.attach(video);
    });
    await waitFor(() => expect(hls.instances).toHaveLength(1));
    const first = hls.instances[0] as {
      destroy: ReturnType<typeof vi.fn>;
      transferMedia: ReturnType<typeof vi.fn>;
    };
    let finishRemoval!: () => void;
    first.transferMedia.mockReturnValue({
      media: video,
      mediaSource: { readyState: "open" },
      tracks: {
        video: {
          buffer: {
            updating: false,
            buffered: { length: 1, start: () => 0, end: () => 1 },
            addEventListener: vi.fn((event: string, callback: () => void) => {
              if (event === "updateend") finishRemoval = callback;
            }),
            removeEventListener: vi.fn(),
            remove: vi.fn(),
          },
        },
      },
    });

    act(() => firstRelease());
    rerender({ id: "ch-2" });
    let supersededRelease!: () => void;
    act(() => {
      supersededRelease = result.current.attach(video);
    });
    await waitFor(() => expect(finishOutgoingSeek).toBeTypeOf("function"));
    act(() => finishOutgoingSeek());
    await waitFor(() => expect(finishRemoval).toBeTypeOf("function"));
    expect(hls.instances).toHaveLength(2);

    act(() => supersededRelease());
    const superseded = hls.instances[1] as {
      attachMedia: ReturnType<typeof vi.fn>;
      destroy: ReturnType<typeof vi.fn>;
      loadSource: ReturnType<typeof vi.fn>;
      startLoad: ReturnType<typeof vi.fn>;
    };
    expect(superseded.loadSource).not.toHaveBeenCalled();
    expect(superseded.attachMedia).not.toHaveBeenCalled();
    expect(superseded.startLoad).not.toHaveBeenCalled();
    rerender({ id: "ch-3" });
    let currentRelease!: () => void;
    act(() => {
      currentRelease = result.current.attach(video);
    });
    await waitFor(() => expect(hls.instances).toHaveLength(3));
    const current = hls.instances[2] as {
      attachMedia: ReturnType<typeof vi.fn>;
      destroy: ReturnType<typeof vi.fn>;
      loadSource: ReturnType<typeof vi.fn>;
    };
    await waitFor(() => expect(current.loadSource).toHaveBeenCalledWith("/v1/playout/hls/ch-3/master.m3u8"));
    video.currentTime = 7;

    act(() => finishRemoval());
    await act(async () => undefined);
    expect(hls.instances).toHaveLength(3);
    expect(current.attachMedia).toHaveBeenLastCalledWith(video);
    expect(first.destroy).toHaveBeenCalledOnce();
    expect(superseded.destroy).not.toHaveBeenCalled();
    expect(superseded.attachMedia).not.toHaveBeenCalled();
    expect(superseded.startLoad).not.toHaveBeenCalled();
    expect(video.currentTime).toBe(7);

    act(() => currentRelease());
    await waitFor(() => expect(current.destroy).toHaveBeenCalled());
    await waitFor(() => expect(superseded.destroy).toHaveBeenCalled());
  });

  it("resumes the exact paused point and can return to the live edge", async () => {
    hls.supported = true;
    channelPlayUrl.mockResolvedValue({ relativeUrl: "/v1/playout/hls/ch-1/master.m3u8" });
    vi.spyOn(Date, "now").mockReturnValue(1_000_000);
    const video = videoEl() as HTMLVideoElement;
    video.currentTime = 44;
    Object.defineProperty(video, "seekable", {
      value: { length: 1, start: () => 0, end: () => 100 },
      configurable: true,
    });
    const { result } = renderHook(() => useHlsPlayer("ch-1"));

    act(() => result.current.attach(video));
    await waitFor(() => expect(hls.instances).toHaveLength(1));
    const controller = hls.instances[0] as {
      liveSyncPosition: number;
      startLoad: ReturnType<typeof vi.fn>;
    };

    act(() => result.current.liveTransport.pause(video));
    expect(video.pause).toHaveBeenCalledOnce();
    expect(result.current.liveTransport.state.mode).toBe("paused");

    vi.mocked(Date.now).mockReturnValue(1_015_000);
    await act(async () => result.current.liveTransport.play(video));
    expect(controller.startLoad).toHaveBeenCalledWith(44);
    expect(video.currentTime).toBe(44);
    expect(result.current.liveTransport.state).toMatchObject({ mode: "behind", lagSeconds: 15 });

    await act(async () => result.current.liveTransport.goLive(video));
    expect(controller.startLoad).toHaveBeenCalledWith(-1);
    expect(video.currentTime).toBe(98);
    expect(result.current.liveTransport.state).toMatchObject({ mode: "live", lagSeconds: 0 });
  });

  it("anchors live programme time to the frame being shown instead of the wall-clock live edge", async () => {
    hls.supported = true;
    channelPlayUrl.mockResolvedValue({ relativeUrl: "/v1/playout/hls/ch-1/master.m3u8" });
    vi.spyOn(Date, "now").mockReturnValue(1_000_000);
    const video = videoEl() as HTMLVideoElement;
    video.currentTime = 44;
    const { result } = renderHook(() => useHlsPlayer("ch-1"));

    act(() => result.current.attach(video));
    await waitFor(() => expect(hls.instances).toHaveLength(1));
    const controller = hls.instances[0] as { playingDate: Date | null };
    // Two HLS segments of safety buffer put the decoded frame eight seconds behind real time.
    // At an Airing boundary those eight seconds are the difference between "commercial" and the
    // following programme, so the Watch chrome must describe the frame, not Date.now().
    controller.playingDate = new Date(992_000);

    const timeUpdate = vi
      .mocked(video.addEventListener)
      .mock.calls.find(([event]) => event === "timeupdate")?.[1];
    expect(timeUpdate).toBeTypeOf("function");
    act(() => (timeUpdate as EventListener)(new Event("timeupdate")));

    expect(result.current.liveTransport.state).toMatchObject({
      mode: "live",
      viewerTimeMs: 992_000,
    });
  });

  it("keeps an intentional pause while the active controller buffers another fragment", async () => {
    hls.supported = true;
    channelPlayUrl.mockResolvedValue({ relativeUrl: "/v1/playout/hls/ch-1/master.m3u8" });
    const video = videoEl() as HTMLVideoElement;
    const { result } = renderHook(() => useHlsPlayer("ch-1"));

    act(() => result.current.attach(video));
    await waitFor(() => expect(hls.instances).toHaveLength(1));
    const controller = hls.instances[0] as { on: ReturnType<typeof vi.fn> };
    const fragmentBuffered = controller.on.mock.calls
      .filter((call: unknown[]) => call[0] === "fragBuffered")
      .at(-1)?.[1] as (() => void) | undefined;
    expect(fragmentBuffered).toBeTypeOf("function");

    act(() => result.current.liveTransport.pause(video));
    vi.mocked(video.play).mockClear();
    act(() => fragmentBuffered?.());

    expect(video.play).not.toHaveBeenCalled();
    expect(result.current.liveTransport.state.mode).toBe("paused");
  });

  it("returns live and raises a notice when the paused point has left the shared window", async () => {
    hls.supported = true;
    channelPlayUrl.mockResolvedValue({ relativeUrl: "/v1/playout/hls/ch-1/master.m3u8" });
    const video = videoEl() as HTMLVideoElement;
    video.currentTime = 44;
    let seekableStart = 0;
    Object.defineProperty(video, "seekable", {
      value: { length: 1, start: () => seekableStart, end: () => 100 },
      configurable: true,
    });
    const { result } = renderHook(() => useHlsPlayer("ch-1"));
    act(() => result.current.attach(video));
    await waitFor(() => expect(hls.instances).toHaveLength(1));

    act(() => result.current.liveTransport.pause(video));
    seekableStart = 50;
    await act(async () => result.current.liveTransport.play(video));

    expect(result.current.liveTransport.state).toMatchObject({ mode: "live", noticeRevision: 1 });
  });

  it("surfaces an error when the mint returns no URL", async () => {
    channelPlayUrl.mockResolvedValue({}); // neither relativeUrl nor url
    const { result } = renderHook(() => useHlsPlayer("ch-1"));

    act(() => {
      result.current.attach(videoEl());
    });

    await waitFor(() => expect(result.current.status).toBe("error"));
    expect(result.current.error).toMatch(/couldn't get a stream/i);
  });

  it("surfaces an error when the mint fails", async () => {
    // Drive the failure through unwrap throwing inside the hook's own .then — the same .catch path a
    // rejected fetch takes, but without a top-level rejected promise vitest would flag as unhandled.
    channelPlayUrl.mockResolvedValue({});
    unwrap.mockImplementation(() => {
      throw new Error("network down");
    });
    const { result } = renderHook(() => useHlsPlayer("ch-1"));

    act(() => {
      result.current.attach(videoEl());
    });

    await waitFor(() => expect(result.current.status).toBe("error"));
    expect(result.current.error).toBe("network down");
  });

  it("reports plainly when the browser can play neither MSE nor native HLS", async () => {
    channelPlayUrl.mockResolvedValue({ relativeUrl: "/v1/playout/hls/master.m3u8" });
    const { result } = renderHook(() => useHlsPlayer("ch-1"));

    act(() => {
      result.current.attach(videoEl("")); // canPlayType → "" : no native HLS, hls.js unsupported
    });

    await waitFor(() => expect(result.current.status).toBe("error"));
    expect(result.current.error).toMatch(/can't play live channels/i);
  });

  it("keeps the tuning overlay up until the replacement produces a decoded frame", async () => {
    channelPlayUrl.mockResolvedValue({ relativeUrl: "/v1/playout/hls/master.m3u8" });
    let firstFrame!: () => void;
    let loadedMetadata!: () => void;
    let loadedData!: () => void;
    const video = {
      ...videoEl("application/vnd.apple.mpegurl"),
      poster: "data:image/png;base64,held",
      addEventListener: vi.fn((event: string, callback: () => void) => {
        if (event === "loadedmetadata") loadedMetadata = callback;
        if (event === "loadeddata") loadedData = callback;
      }),
      requestVideoFrameCallback: vi.fn((callback: () => void) => {
        firstFrame = callback;
        return 1;
      }),
      cancelVideoFrameCallback: vi.fn(),
    } as unknown as HTMLVideoElement;
    const { result } = renderHook(() => useHlsPlayer("ch-1"));

    act(() => {
      result.current.attach(video);
    });
    await waitFor(() => expect(channelPlayUrl).toHaveBeenCalledOnce());
    expect(result.current.status).toBe("loading");
    expect(video.requestVideoFrameCallback).not.toHaveBeenCalled();

    act(() => loadedMetadata());
    expect(video.requestVideoFrameCallback).not.toHaveBeenCalled();
    act(() => loadedData());
    act(() => firstFrame());
    expect(result.current.status).toBe("playing");
    expect(video.removeAttribute).toHaveBeenCalledWith("poster");
  });

  it("ignores a mint that resolves after teardown (no state update)", async () => {
    let resolve!: (v: unknown) => void;
    channelPlayUrl.mockReturnValue(new Promise((r) => (resolve = r)));
    const { result } = renderHook(() => useHlsPlayer("ch-1"));

    let cleanup!: () => void;
    act(() => {
      cleanup = result.current.attach(videoEl());
    });
    // Unmount/switch before the mint lands, then let it resolve — cancelledRef must swallow it.
    act(() => cleanup());
    await act(async () => {
      resolve({ relativeUrl: "/late.m3u8" });
    });

    // The late resolution was cancelled, so status never advanced to playing/error from it.
    expect(result.current.status).toBe("loading");
  });

  it("aborts the superseded mint and lets only the latest attachment continue", async () => {
    const pending: Array<(value: unknown) => void> = [];
    channelPlayUrl.mockImplementation(() => new Promise((resolve) => pending.push(resolve)));
    const { result } = renderHook(() => useHlsPlayer("ch-1"));

    let firstCleanup!: () => void;
    act(() => {
      firstCleanup = result.current.attach(videoEl());
    });
    const firstSignal = channelPlayUrl.mock.calls[0]?.[3]?.signal as AbortSignal;
    act(() => firstCleanup());
    act(() => {
      result.current.attach(videoEl());
    });

    expect(firstSignal.aborted).toBe(true);
    await act(async () => {
      pending[0]?.({ relativeUrl: "/superseded.m3u8" });
      pending[1]?.({});
    });
    await waitFor(() => expect(result.current.status).toBe("error"));
    expect(result.current.error).toMatch(/couldn't get a stream/i);
  });
});
