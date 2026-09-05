import type { GuideOutputBody } from "@loomarr/api/models/guideOutputBody";
import { describe, expect, it, vi } from "vitest";

import { createGuideController, createGuideSourcePort } from "./guide-controller";

const guide = (channelId = "seven"): GuideOutputBody => ({
  channels: [
    {
      airings: [
        {
          kind: "program",
          scheduleBlockId: `airing-${channelId}-one`,
          startMs: 1_000,
          stopMs: 4_000,
          title: "Radioactive Man",
        },
        {
          kind: "program",
          scheduleBlockId: `airing-${channelId}-two`,
          startMs: 4_000,
          stopMs: 5_000,
          title: "Fallout Boy",
        },
      ],
      channelId,
      name: "Science Fiction",
      number: 7,
      pendingCount: 0,
      status: "live",
    },
  ],
  fromMs: 1_000,
  toMs: 5_000,
});

const deferred = <T>() => {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
};

describe("Guide controller", () => {
  it("loads the resolved authoritative window and selects the preferred channel at now", async () => {
    const source = { load: vi.fn().mockResolvedValue(guide()) };
    const resolveWindow = vi.fn().mockReturnValue({ from: 1_000, to: 5_000 });
    const controller = createGuideController({ now: () => 2_000, resolveWindow, source });

    await controller.refresh("seven");

    expect(resolveWindow).toHaveBeenCalledWith(2_000);
    expect(source.load).toHaveBeenCalledWith({ from: 1_000, to: 5_000 }, expect.any(AbortSignal));
    expect(controller.getSnapshot()).toMatchObject({
      selection: { anchorMs: 2_000, channelId: "seven", scheduleBlockId: "airing-seven-one" },
      status: "ready",
    });
  });

  it("keeps only the latest refresh authoritative", async () => {
    const first = deferred<GuideOutputBody>();
    const second = deferred<GuideOutputBody>();
    const signals: AbortSignal[] = [];
    const source = {
      load: vi.fn((_window, signal: AbortSignal) => {
        signals.push(signal);
        return signals.length === 1 ? first.promise : second.promise;
      }),
    };
    const controller = createGuideController({ now: () => 2_000, source });
    const stale = controller.refresh();
    const latest = controller.refresh("latest");
    expect(signals[0]?.aborted).toBe(true);

    second.resolve(guide("latest"));
    await latest;
    first.resolve(guide("stale"));
    await stale;

    expect(controller.getSnapshot().selection?.channelId).toBe("latest");
  });

  it("preserves the selected time column across refreshes", async () => {
    const source = { load: vi.fn().mockResolvedValue(guide()) };
    const controller = createGuideController({ now: () => 2_000, source });
    await controller.refresh();
    controller.select({ anchorMs: 4_500, channelId: "seven", scheduleBlockId: "airing-seven-two" });

    await controller.refresh();

    expect(controller.getSnapshot().selection).toEqual({
      anchorMs: 4_500,
      channelId: "seven",
      scheduleBlockId: "airing-seven-two",
    });
  });

  it("exposes platform-neutral movement and its boundaries", async () => {
    const controller = createGuideController({
      now: () => 2_000,
      source: { load: vi.fn().mockResolvedValue(guide()) },
    });
    await controller.refresh();

    expect(controller.move("right")?.selection.scheduleBlockId).toBe("airing-seven-two");
    expect(controller.getSnapshot().selection?.scheduleBlockId).toBe("airing-seven-two");
    expect(controller.move("right")?.boundary).toBe("right");
  });

  it("falls back deterministically and reports an empty authoritative Guide", async () => {
    const source = { load: vi.fn().mockResolvedValue(guide("available")) };
    const controller = createGuideController({ now: () => 2_000, source });
    await controller.refresh("removed");
    expect(controller.getSnapshot().selection?.channelId).toBe("available");

    source.load.mockResolvedValue({ ...guide(), channels: [] });
    await controller.refresh();
    expect(controller.getSnapshot()).toMatchObject({ status: "empty" });
    expect(controller.getSnapshot().selection).toBeUndefined();
  });

  it("uses the generated Guide URL and reports HTTP failure without inventing data", async () => {
    const request = vi.fn().mockResolvedValue(new Response("no", { status: 503 }));
    const source = createGuideSourcePort(request);

    await expect(source.load({ from: 1_000, to: 5_000 }, new AbortController().signal)).rejects.toThrow(
      "Couldn't load the Guide (503).",
    );
    expect(request).toHaveBeenCalledWith(
      "/v1/guide?from=1000&to=5000",
      expect.objectContaining({ method: "GET" }),
    );
  });

  it("aborts work and stops publishing after disposal", async () => {
    const pending = deferred<GuideOutputBody>();
    let signal: AbortSignal | undefined;
    const controller = createGuideController({
      source: {
        load: vi.fn((_window, nextSignal: AbortSignal) => {
          signal = nextSignal;
          return pending.promise;
        }),
      },
    });
    const refresh = controller.refresh();
    controller.dispose();
    expect(signal?.aborted).toBe(true);
    pending.resolve(guide());
    await refresh;
    expect(controller.getSnapshot().status).toBe("loading");
  });
});
