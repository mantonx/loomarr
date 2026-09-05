import type {
  GuideLayout,
  GuideNavigationDirection,
  GuideNavigationResult,
  GuideSelection,
  GuideWindow,
} from "../guide.type";

type GuideControllerStatus = "empty" | "error" | "loading" | "ready";

interface GuideSourcePort {
  load: (window: GuideWindow, signal: AbortSignal) => Promise<GuideLayout["source"]>;
}

interface GuideControllerSnapshot {
  error?: string;
  layout?: GuideLayout;
  selection?: GuideSelection;
  status: GuideControllerStatus;
}

interface GuideController {
  dispose: () => void;
  getSnapshot: () => GuideControllerSnapshot;
  move: (direction: GuideNavigationDirection) => GuideNavigationResult | undefined;
  refresh: (preferredChannelId?: string) => Promise<void>;
  select: (selection: GuideSelection) => void;
  subscribe: (listener: () => void) => () => void;
}

interface GuideControllerOptions {
  now?: () => number;
  resolveWindow?: (at: number) => GuideWindow;
  source: GuideSourcePort;
}

export type {
  GuideController,
  GuideControllerOptions,
  GuideControllerSnapshot,
  GuideControllerStatus,
  GuideSourcePort,
};
