import type { GuideLayout } from "@loomarr/core/guide";

interface SurfGroupsArgs {
  currentChannelId?: string;
  /** Omitted until an authoritative preference source exists; the visible group stays empty. */
  favoriteChannelIds?: readonly string[];
  layout: GuideLayout;
  nowMs: number;
  playableChannelIds: readonly string[];
  recentChannelIds: readonly string[];
}

export type { SurfGroupsArgs };
