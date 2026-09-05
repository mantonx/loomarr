import type { DevicePlaybackProfile, PlayerChannel, PlayerSource, PlayerSourcePort } from "../player-source";

type TuneDirection = -1 | 1;

type TuneReason = "catalog" | "channel" | "number" | "previous" | "retry" | "step";

type PlayerStatus = "empty" | "idle" | "tuning" | "playing" | "paused" | "failed";

type LivePlaybackMode = "behind" | "live" | "paused";

interface LivePlaybackState {
  /** Whole seconds between the displayed frame and the Channel wall clock. */
  lagSeconds: number;
  mode: LivePlaybackMode;
  /**
   * Increments when a paused point expires and the transport returns to live.
   * Consumers compare revisions so each expiry can be announced exactly once.
   */
  noticeRevision: number;
  /** Programme-date-time of the displayed frame, or the tune-time fallback before one is known. */
  viewerTimeMs: number;
}

type PlayerTransportEvent =
  | { attemptId: number; type: "first-frame" }
  | { attemptId: number; state: LivePlaybackState; type: "live-state" }
  | { attemptId: number; type: "paused" }
  | { attemptId: number; type: "playing" }
  | { attemptId: number; error: string; type: "error" };

interface PlayerTransport {
  dispose: () => void;
  goLive: () => Promise<void> | void;
  pause: () => void;
  play: () => Promise<void> | void;
  replace: (source: PlayerSource, context: { attemptId: number; signal: AbortSignal }) => Promise<void>;
  subscribe: (listener: (event: PlayerTransportEvent) => void) => () => void;
}

interface PlayerSnapshot {
  attemptId?: number;
  catalog: readonly PlayerChannel[];
  channel?: PlayerChannel;
  error?: string;
  livePlayback?: LivePlaybackState;
  previousChannelId?: string;
  recentChannelIds: readonly string[];
  status: PlayerStatus;
  tuneReason?: TuneReason;
}

interface PlayerController {
  dispose: () => void;
  getSnapshot: () => PlayerSnapshot;
  goLive: () => Promise<void>;
  pause: () => void;
  play: () => Promise<void>;
  previous: () => Promise<void>;
  reconcile: (channels: readonly PlayerChannel[]) => Promise<void>;
  retry: () => Promise<void>;
  step: (direction: TuneDirection) => Promise<void>;
  subscribe: (listener: (snapshot: PlayerSnapshot) => void) => () => void;
  tuneChannel: (channelId: string) => Promise<void>;
  tuneNumber: (digits: string) => Promise<void>;
}

interface PlayerControllerOptions {
  initialTune?: "first" | "none";
  profile: DevicePlaybackProfile;
  source: PlayerSourcePort;
  transport: PlayerTransport;
}

export type {
  LivePlaybackMode,
  LivePlaybackState,
  PlayerController,
  PlayerControllerOptions,
  PlayerSnapshot,
  PlayerStatus,
  PlayerTransport,
  PlayerTransportEvent,
  TuneDirection,
  TuneReason,
};
