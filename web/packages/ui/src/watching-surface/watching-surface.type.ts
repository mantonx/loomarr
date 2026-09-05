import type { Density } from "@loomarr/design-system";
import type { PlayerSnapshot } from "@loomarr/player";
import type { ReactNode } from "react";

import type { ProgrammeIdentityData } from "../identity";

interface ChannelNumberEntry {
  channelName?: string;
  digits: string;
}

interface WatchingProgrammeData extends ProgrammeIdentityData {
  progressPercent?: number;
}

interface WatchingScheduleData {
  next?: Pick<ProgrammeIdentityData, "timeLabel" | "title">;
  now?: WatchingProgrammeData;
}

interface WatchingSurfaceProps {
  /** False while another transient journey is drawn over the still-mounted player. */
  chromeVisible?: boolean;
  /** Presentation-owned visibility; playback state deliberately does not own UI chrome. */
  controlsVisible?: boolean;
  /** Changes only after handled Watching activity, not while live playback presentation updates. */
  controlsActivityKey?: number;
  density: Density;
  /** True only while the authoritative Channel catalog request is unresolved. */
  loading?: boolean;
  loadError?: string;
  numberEntry?: ChannelNumberEntry;
  onChannelDown: () => void;
  onChannelUp: () => void;
  onDismissControls: () => void;
  onGoLive: () => void;
  onOpenGuide: () => void;
  onOpenSurf: () => void;
  onPause: () => void;
  onPlay: () => void;
  onPrevious: () => void;
  onRetry: () => void;
  onShowControls: () => void;
  player: ReactNode;
  schedule?: WatchingScheduleData;
  snapshot: PlayerSnapshot;
}

export type { ChannelNumberEntry, WatchingProgrammeData, WatchingScheduleData, WatchingSurfaceProps };
