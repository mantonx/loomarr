import type { GuideController } from "@loomarr/core/guide";
import type { Density } from "@loomarr/design-system";
import type { ReactNode } from "react";

import type { FocusTargetRegistry } from "../../focus-target";
import type { SurfChannelData, SurfGroupData, SurfSelection } from "../surf-rail.type";

interface SurfJourneyProps {
  clientVersion: string;
  controller: GuideController;
  currentChannelId?: string;
  density?: Density;
  focusRegistry?: FocusTargetRegistry<SurfSelection>;
  /** Authoritative preference IDs. Omit when no preference contract is available. */
  favoriteChannelIds?: readonly string[];
  now?: () => number;
  onDisconnect?: () => Promise<void> | void;
  onTune: (channelId: string) => void;
  playableChannelIds: readonly string[];
  recentChannelIds: readonly string[];
  restoreSelection?: (
    groups: readonly SurfGroupData[],
    selection: SurfSelection,
  ) => SurfSelection | undefined;
  renderArtwork?: (channel: SurfChannelData) => ReactNode;
  renderChannelLogo?: (channel: SurfChannelData) => ReactNode;
  serverName?: string;
  serverVersion?: string;
}

export type { SurfJourneyProps };
