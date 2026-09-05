import type { PlayerSnapshot } from "@loomarr/player";
import { describe, expect, it, vi } from "vitest";

vi.mock("expo-video", () => ({
  createVideoPlayer: vi.fn(),
  VideoView: vi.fn(),
}));

const { createNativePlayerLifecycle } = await import("@loomarr/player/native");

const pausedSnapshot = (): PlayerSnapshot => ({
  catalog: [{ id: "seven", inAppPlayable: true, name: "Seven", number: 7 }],
  channel: { id: "seven", inAppPlayable: true, name: "Seven", number: 7 },
  recentChannelIds: ["six"],
  status: "paused",
});

describe("native player application lifecycle", () => {
  it("pauses controller state before synchronously releasing native resources", () => {
    const order: string[] = [];
    const lifecycle = createNativePlayerLifecycle({
      controller: {
        getSnapshot: pausedSnapshot,
        pause: () => order.push("pause"),
        retry: vi.fn(),
      },
      refresh: vi.fn(),
      transport: {
        resume: vi.fn(),
        suspend: () => order.push("suspend"),
      },
    });

    lifecycle.enterBackground();

    expect(order).toEqual(["pause", "suspend"]);
  });

  it("recreates the player and refreshes authority before retuning the remembered Channel", async () => {
    const order: string[] = [];
    const lifecycle = createNativePlayerLifecycle({
      controller: {
        getSnapshot: pausedSnapshot,
        pause: vi.fn(),
        retry: async () => {
          order.push("retry");
        },
      },
      refresh: async () => {
        order.push("refresh");
      },
      transport: {
        resume: () => order.push("resume"),
        suspend: vi.fn(),
      },
    });

    await lifecycle.enterForeground();

    expect(order).toEqual(["resume", "refresh", "retry"]);
  });

  it("does not invent or duplicate a tune after authoritative reconciliation", async () => {
    const retry = vi.fn();
    let snapshot: PlayerSnapshot = pausedSnapshot();
    const lifecycle = createNativePlayerLifecycle({
      controller: {
        getSnapshot: () => snapshot,
        pause: vi.fn(),
        retry,
      },
      refresh: async () => {
        snapshot = { catalog: [], recentChannelIds: ["six"], status: "empty" };
      },
      transport: { resume: vi.fn(), suspend: vi.fn() },
    });

    await lifecycle.enterForeground();
    expect(retry).not.toHaveBeenCalled();

    snapshot = pausedSnapshot();
    const reconciledElsewhere = createNativePlayerLifecycle({
      controller: {
        getSnapshot: () => snapshot,
        pause: vi.fn(),
        retry,
      },
      refresh: async () => {
        snapshot = { ...snapshot, status: "tuning" };
      },
      transport: { resume: vi.fn(), suspend: vi.fn() },
    });
    await reconciledElsewhere.enterForeground();
    expect(retry).not.toHaveBeenCalled();
  });

  it("does not let a stale foreground refresh restart playback after backgrounding", async () => {
    let finishRefresh!: () => void;
    const retry = vi.fn();
    const lifecycle = createNativePlayerLifecycle({
      controller: {
        getSnapshot: pausedSnapshot,
        pause: vi.fn(),
        retry,
      },
      refresh: () =>
        new Promise<void>((resolve) => {
          finishRefresh = resolve;
        }),
      transport: { resume: vi.fn(), suspend: vi.fn() },
    });

    const foreground = lifecycle.enterForeground();
    lifecycle.enterBackground();
    finishRefresh();
    await foreground;

    expect(retry).not.toHaveBeenCalled();
  });

  it("ignores a stale refresh failure but reports the active foreground failure", async () => {
    let rejectRefresh!: (error: Error) => void;
    const failure = new Error("Channel refresh failed");
    const retry = vi.fn();
    const lifecycle = createNativePlayerLifecycle({
      controller: {
        getSnapshot: pausedSnapshot,
        pause: vi.fn(),
        retry,
      },
      refresh: () =>
        new Promise<void>((_resolve, reject) => {
          rejectRefresh = reject;
        }),
      transport: { resume: vi.fn(), suspend: vi.fn() },
    });

    const staleForeground = lifecycle.enterForeground();
    lifecycle.enterBackground();
    rejectRefresh(failure);
    await expect(staleForeground).resolves.toBeUndefined();

    const activeLifecycle = createNativePlayerLifecycle({
      controller: {
        getSnapshot: pausedSnapshot,
        pause: vi.fn(),
        retry,
      },
      refresh: () => Promise.reject(failure),
      transport: { resume: vi.fn(), suspend: vi.fn() },
    });
    await expect(activeLifecycle.enterForeground()).rejects.toThrow("Channel refresh failed");
    expect(retry).not.toHaveBeenCalled();
  });
});
