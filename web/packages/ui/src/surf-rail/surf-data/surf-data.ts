import {
  formatGuideEpisode,
  formatGuideTime,
  formatGuideTimeRange,
  type GuideAiringLayout,
  type GuideLayout,
  guideAiringLabel,
} from "@loomarr/core/guide";

import type { SurfChannelData, SurfGroupData, SurfSelection } from "../surf-rail.type";
import type { SurfGroupsArgs } from "./surf-data.type";

const programmeFacts = (airing: GuideAiringLayout) =>
  [
    airing.source.year ? String(airing.source.year) : undefined,
    airing.source.rating,
    airing.source.genres?.slice(0, 2).join(" · "),
  ].filter((fact): fact is string => Boolean(fact));

const surfChannelData = (
  channel: GuideLayout["channels"][number],
  nowMs: number,
  timezone?: string,
): SurfChannelData => {
  const now = channel.airings.find(
    (airing) => airing.source.startMs <= nowMs && airing.source.stopMs > nowMs,
  );
  const next = channel.airings.find((airing) => airing.source.startMs >= (now?.source.stopMs ?? nowMs));

  return {
    channelLogoUri: channel.source.logo,
    channelLogoState: channel.source.logo ? "ready" : "missing",
    channelName: channel.source.name,
    channelNumber: String(channel.source.number),
    id: channel.source.channelId,
    next: next
      ? {
          timeLabel: formatGuideTime(next.source.startMs, timezone),
          title: guideAiringLabel(next.source),
        }
      : undefined,
    now: now
      ? {
          artworkState: now.source.thumbImage || now.source.thumbUrl ? "ready" : "missing",
          artworkUri: now.source.thumbImage?.src ?? now.source.thumbUrl,
          badge: { label: "On now", tone: "live" },
          description: now.source.description,
          episodeLabel: formatGuideEpisode(now.source.season, now.source.episode),
          facts: programmeFacts(now),
          progressPercent: now.progressRatio === undefined ? undefined : now.progressRatio * 100,
          remainingLabel: `${Math.max(1, Math.ceil((now.source.stopMs - nowMs) / 60_000))}m left`,
          seriesTitle: now.source.series,
          timeLabel: formatGuideTimeRange(now.source.startMs, now.source.stopMs, timezone),
          title: guideAiringLabel(now.source),
        }
      : undefined,
  };
};

const orderedKnownChannels = (
  ids: readonly string[],
  byId: ReadonlyMap<string, SurfChannelData>,
  excludedId?: string,
): SurfChannelData[] => {
  const seen = new Set<string>();
  return ids.flatMap((id) => {
    if (id === excludedId || seen.has(id)) return [];
    seen.add(id);
    const channel = byId.get(id);
    return channel ? [channel] : [];
  });
};

const surfPreviousChannel = (
  currentChannelId: string | undefined,
  recentChannelIds: readonly string[],
  playableChannelIds: readonly string[],
): string | undefined => {
  const playable = new Set(playableChannelIds);
  return recentChannelIds.find((id) => id !== currentChannelId && playable.has(id));
};

const surfGroupsFromGuide = ({
  currentChannelId,
  favoriteChannelIds,
  layout,
  nowMs,
  playableChannelIds,
  recentChannelIds,
}: SurfGroupsArgs): SurfGroupData[] => {
  const playable = new Set(playableChannelIds);
  const all = layout.channels
    .filter((channel) => playable.has(channel.source.channelId))
    .map((channel) => surfChannelData(channel, nowMs, layout.timezone));
  const byId = new Map(all.map((channel) => [channel.id, channel]));
  const favorites = favoriteChannelIds ? new Set(favoriteChannelIds) : undefined;

  return [
    {
      channels: favorites ? all.filter((channel) => favorites.has(channel.id)) : [],
      kind: "favourites",
      label: "Favourites",
    },
    {
      channels: orderedKnownChannels(recentChannelIds, byId, currentChannelId),
      kind: "recent",
      label: "Recent",
    },
    { channels: all, kind: "all", label: "All channels" },
  ];
};

const watchingScheduleFromGuide = (
  layout: GuideLayout | undefined,
  channelId: string | undefined,
  nowMs: number,
): Pick<SurfChannelData, "next" | "now"> | undefined => {
  if (!layout) return undefined;
  const channel = layout.channels.find((candidate) => candidate.source.channelId === channelId);
  return channel ? surfChannelData(channel, nowMs, layout.timezone) : undefined;
};

const restoreSurfSelection = (
  groups: readonly SurfGroupData[],
  selection?: SurfSelection,
): SurfSelection | undefined => {
  const selections = groups.flatMap((group) =>
    group.channels.map((channel) => ({ channelId: channel.id, group: group.kind })),
  );
  if (!selection) return selections[0];
  return (
    selections.find(
      (candidate) => candidate.group === selection.group && candidate.channelId === selection.channelId,
    ) ??
    selections.find((candidate) => candidate.channelId === selection.channelId) ??
    selections[0]
  );
};

export {
  restoreSurfSelection,
  surfChannelData,
  surfGroupsFromGuide,
  surfPreviousChannel,
  watchingScheduleFromGuide,
};
