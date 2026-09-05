import { getChannelGuideUrl } from "@loomarr/api/endpoints/channels";
import type { GuideOutputBody } from "@loomarr/api/models/guideOutputBody";

import { defaultGuideWindow, guideSelectionForChannel, layoutGuide, moveGuideSelection } from "../guide";
import type {
  GuideController,
  GuideControllerOptions,
  GuideControllerSnapshot,
  GuideSourcePort,
} from "./guide-controller.type";

const createGuideSourcePort = (request: typeof globalThis.fetch): GuideSourcePort => ({
  load: async (window, signal) => {
    const response = await request(getChannelGuideUrl(window), { method: "GET", signal });
    if (!response.ok) throw new Error(`Couldn't load the Guide (${response.status}).`);
    return (await response.json()) as GuideOutputBody;
  },
});

const createGuideController = ({
  now = Date.now,
  resolveWindow = defaultGuideWindow,
  source,
}: GuideControllerOptions): GuideController => {
  let disposed = false;
  let request: AbortController | undefined;
  let snapshot: GuideControllerSnapshot = { status: "loading" };
  const listeners = new Set<() => void>();

  const publish = (next: GuideControllerSnapshot) => {
    snapshot = next;
    for (const listener of listeners) listener();
  };

  return {
    dispose: () => {
      if (disposed) return;
      disposed = true;
      request?.abort();
      listeners.clear();
    },
    getSnapshot: () => snapshot,
    move: (direction) => {
      if (disposed || snapshot.status !== "ready" || !snapshot.layout || !snapshot.selection) {
        return undefined;
      }
      const result = moveGuideSelection(snapshot.layout, snapshot.selection, direction);
      if (!result.boundary) publish({ ...snapshot, selection: result.selection });
      return result;
    },
    refresh: async (preferredChannelId) => {
      if (disposed) return;
      request?.abort();
      const nextRequest = new AbortController();
      request = nextRequest;
      publish({ ...snapshot, error: undefined, status: "loading" });
      const at = now();

      try {
        const sourceGuide = await source.load(resolveWindow(at), nextRequest.signal);
        if (disposed || nextRequest.signal.aborted || request !== nextRequest) return;

        const layout = layoutGuide(sourceGuide, at);
        const anchorMs = snapshot.selection?.anchorMs ?? at;
        const requestedChannelId =
          preferredChannelId ?? snapshot.selection?.channelId ?? layout.channels[0]?.source.channelId;
        const requestedSelection = requestedChannelId
          ? guideSelectionForChannel(layout, requestedChannelId, anchorMs)
          : undefined;
        const fallbackChannelId = layout.channels[0]?.source.channelId;
        const selection =
          requestedSelection ??
          (fallbackChannelId ? guideSelectionForChannel(layout, fallbackChannelId, anchorMs) : undefined);

        publish({ layout, selection, status: layout.channels.length ? "ready" : "empty" });
      } catch (error) {
        if (disposed || nextRequest.signal.aborted || request !== nextRequest) return;
        publish({
          ...snapshot,
          error: error instanceof Error ? error.message : "Couldn't load the Guide.",
          status: "error",
        });
      }
    },
    select: (selection) => {
      if (disposed || snapshot.status !== "ready") return;
      publish({ ...snapshot, selection });
    },
    subscribe: (listener) => {
      if (disposed) return () => undefined;
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
  };
};

export { createGuideController, createGuideSourcePort };
