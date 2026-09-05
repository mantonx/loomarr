import type { PlayerChannel } from "../player-source";
import type {
  PlayerController,
  PlayerControllerOptions,
  PlayerSnapshot,
  TuneDirection,
  TuneReason,
} from "./player-controller.type";

const RECENT_CHANNEL_LIMIT = 6;

const playableCatalog = (channels: readonly PlayerChannel[]): PlayerChannel[] =>
  [...channels]
    .filter((channel) => channel.inAppPlayable)
    .sort((left, right) => left.number - right.number || left.id.localeCompare(right.id));

const createPlayerController = ({
  initialTune = "first",
  profile,
  source,
  transport,
}: PlayerControllerOptions): PlayerController => {
  let disposed = false;
  let attempt = 0;
  let activeRequest: AbortController | undefined;
  let snapshot: PlayerSnapshot = {
    catalog: [],
    recentChannelIds: [],
    status: "empty",
  };
  const listeners = new Set<(next: PlayerSnapshot) => void>();

  const publish = (next: PlayerSnapshot) => {
    snapshot = next;
    for (const listener of listeners) listener(snapshot);
  };

  const isCurrentAttempt = (attemptId: number, signal?: AbortSignal) =>
    !disposed && attemptId === attempt && !signal?.aborted;

  const tune = async (channel: PlayerChannel, reason: TuneReason, force = false) => {
    if (disposed) return;
    if (!force && snapshot.channel?.id === channel.id && snapshot.status !== "failed") return;

    const previousId = snapshot.channel?.id;
    const recentChannelIds =
      previousId && previousId !== channel.id
        ? [previousId, ...snapshot.recentChannelIds]
            .filter((id, index, all) => id !== channel.id && all.indexOf(id) === index)
            .slice(0, RECENT_CHANNEL_LIMIT)
        : [...snapshot.recentChannelIds];

    activeRequest?.abort();
    const request = new AbortController();
    activeRequest = request;
    attempt += 1;
    const attemptId = attempt;
    publish({
      attemptId,
      catalog: snapshot.catalog,
      channel,
      livePlayback: {
        lagSeconds: 0,
        mode: "live",
        noticeRevision: 0,
        viewerTimeMs: Date.now(),
      },
      previousChannelId: recentChannelIds[0],
      recentChannelIds,
      status: "tuning",
      tuneReason: reason,
    });

    try {
      const nextSource = await source.mint(channel, profile, request.signal);
      if (!isCurrentAttempt(attemptId, request.signal)) return;
      await transport.replace(nextSource, { attemptId, signal: request.signal });
      if (!isCurrentAttempt(attemptId, request.signal)) return;
      if (snapshot.status === "paused") return;
      await transport.play();
    } catch (error) {
      if (!isCurrentAttempt(attemptId, request.signal)) return;
      publish({
        ...snapshot,
        error: error instanceof Error ? error.message : "Couldn't tune that channel.",
        status: "failed",
      });
    }
  };

  const findAdjacent = (direction: TuneDirection) => {
    const catalog = snapshot.catalog;
    if (catalog.length === 0) return undefined;
    const index = catalog.findIndex((channel) => channel.id === snapshot.channel?.id);
    if (index < 0) return direction > 0 ? catalog[0] : catalog.at(-1);
    return catalog[(index + direction + catalog.length) % catalog.length];
  };

  const unsubscribeTransport = transport.subscribe((event) => {
    if (!isCurrentAttempt(event.attemptId) || event.attemptId !== snapshot.attemptId) return;
    if (event.type === "live-state") {
      publish({ ...snapshot, livePlayback: event.state });
      return;
    }
    if (event.type === "error") {
      publish({ ...snapshot, error: event.error, status: "failed" });
      return;
    }
    if (event.type === "paused") {
      publish({ ...snapshot, error: undefined, status: "paused" });
      return;
    }
    if (event.type === "first-frame" && snapshot.status === "paused") return;
    publish({ ...snapshot, error: undefined, status: "playing" });
  });

  return {
    dispose: () => {
      if (disposed) return;
      disposed = true;
      activeRequest?.abort();
      unsubscribeTransport();
      transport.pause();
      transport.dispose();
      listeners.clear();
    },
    getSnapshot: () => snapshot,
    goLive: async () => {
      if (disposed || !snapshot.channel) return;
      await transport.goLive();
    },
    pause: () => {
      if (disposed || !snapshot.channel) return;
      transport.pause();
      publish({ ...snapshot, error: undefined, status: "paused" });
    },
    play: async () => {
      if (disposed || !snapshot.channel) return;
      await transport.play();
    },
    previous: async () => {
      const channel = snapshot.catalog.find(({ id }) => id === snapshot.previousChannelId);
      if (channel) await tune(channel, "previous");
    },
    reconcile: async (channels) => {
      if (disposed) return;
      const catalog = playableCatalog(channels);
      if (catalog.length === 0) {
        activeRequest?.abort();
        transport.pause();
        publish({
          catalog,
          recentChannelIds: snapshot.recentChannelIds,
          status: "empty",
        });
        return;
      }

      const current = catalog.find((channel) => channel.id === snapshot.channel?.id);
      if (current) {
        publish({ ...snapshot, catalog, channel: current });
        return;
      }
      if (!snapshot.channel && initialTune === "none") {
        publish({
          catalog,
          recentChannelIds: snapshot.recentChannelIds,
          status: "idle",
        });
        return;
      }

      publish({ ...snapshot, catalog });
      const first = catalog[0];
      if (first) await tune(first, "catalog");
    },
    retry: async () => {
      if (snapshot.channel) await tune(snapshot.channel, "retry", true);
    },
    step: async (direction) => {
      const channel = findAdjacent(direction);
      if (channel && (snapshot.catalog.length > 1 || channel.id !== snapshot.channel?.id)) {
        await tune(channel, "step");
      }
    },
    subscribe: (listener) => {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    tuneChannel: async (channelId) => {
      const channel = snapshot.catalog.find(({ id }) => id === channelId);
      if (channel) await tune(channel, "channel");
    },
    tuneNumber: async (digits) => {
      if (!/^\d+$/.test(digits)) return;
      const number = Number.parseInt(digits, 10);
      const channel = snapshot.catalog.find((candidate) => candidate.number === number);
      if (channel) await tune(channel, "number");
    },
  };
};

export { createPlayerController, playableCatalog };
