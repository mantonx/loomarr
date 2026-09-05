import {
  formatGuideEpisode,
  formatGuideTime,
  formatGuideTimeRange,
  guideAiringLabel,
} from "@loomarr/core/guide";
import {
  Action,
  AdaptiveSplit,
  type BadgeTone,
  type Density,
  Surface,
  semanticSpace,
  semanticTargets,
  Text,
} from "@loomarr/design-system";
import { ScrollView, View } from "react-native";

import { ChannelIdentity } from "../identity";
import { ProgrammeCard } from "../programme-card";
import { StatePanel } from "../state-panel";
import type {
  GuideExperienceProps,
  GuideFilterOption,
  GuideSurfaceProps,
  GuideUnavailableState,
} from "./guide.type";
import { TvGuideSurface } from "./guide-tv";

const defaultFilters: readonly GuideFilterOption[] = [
  { label: "All", value: "all" },
  { disabled: true, label: "Favourites", value: "favourites" },
  { disabled: true, label: "Recent", value: "recent" },
];

const guideRowHeight = (density: Density) => (density === "tv" ? 84 : density === "touch" ? 68 : 60);
const guideRailWidth = (density: Density) => (density === "tv" ? 280 : density === "touch" ? 168 : 196);

const airingBadge = (kind: string, isOnNow: boolean): { label: string; tone: BadgeTone } => {
  if (kind === "pending") return { label: "Coming soon", tone: "warning" };
  if (kind === "filler") return { label: "Break", tone: "neutral" };
  if (kind === "flex") return { label: "Filler", tone: "neutral" };
  return isOnNow ? { label: "On now", tone: "live" } : { label: "Scheduled", tone: "neutral" };
};

const airingFacts = (airing: GuideSurfaceProps["layout"]["channels"][number]["airings"][number]) => {
  const source = airing.source;
  return [
    source.year ? String(source.year) : undefined,
    source.rating,
    source.genres?.slice(0, 2).join(" · "),
  ].filter((fact): fact is string => Boolean(fact));
};

const GuideSurface = ({
  channelWindow,
  density = "pointer",
  filter = "all",
  filters = defaultFilters,
  focusRegistry,
  layout,
  onFilterChange,
  onSelectionChange,
  onTune,
  renderArtwork,
  renderChannelLogo,
  selection,
}: GuideSurfaceProps) => {
  const viewportWidth = Reflect.get(globalThis, "innerWidth");
  // The dedicated ten-foot guide assumes the 1280px canvas used by supported TV hosts.
  // Storybook and browser previews can render TV density in a phone-sized viewport; retain
  // the adaptive guide there so channel identity and programme detail remain usable.
  if (density === "tv" && (viewportWidth === undefined || viewportWidth >= 1280)) {
    return (
      <TvGuideSurface
        channelWindow={channelWindow}
        density={density}
        filter={filter}
        filters={filters}
        focusRegistry={focusRegistry}
        layout={layout}
        onFilterChange={onFilterChange}
        onSelectionChange={onSelectionChange}
        onTune={onTune}
        renderArtwork={renderArtwork}
        renderChannelLogo={renderChannelLogo}
        selection={selection}
      />
    );
  }
  const rowHeight = guideRowHeight(density);
  const railWidth = guideRailWidth(density);
  const minimumGridWidth = railWidth + (density === "tv" ? 900 : density === "touch" ? 560 : 640);
  const selectedChannel = layout.channels.find((channel) => channel.source.channelId === selection.channelId);
  const selectedAiring = selectedChannel?.airings.find(
    (airing) => airing.scheduleBlockId === selection.scheduleBlockId,
  );
  const artwork = selectedAiring ? renderArtwork?.(selectedAiring) : undefined;
  const channelLogo = selectedChannel ? renderChannelLogo?.(selectedChannel) : undefined;
  const tickCount = 5;
  const span = layout.toMs - layout.fromMs;
  const ticks = Array.from(
    { length: tickCount },
    (_, index) => layout.fromMs + (span * index) / (tickCount - 1),
  );
  const visibleChannels = channelWindow
    ? layout.channels.slice(channelWindow.start, channelWindow.end)
    : layout.channels;

  const grid = (
    <Surface gap="$inline" overflow="hidden" padding="$control" width="100%">
      <View accessibilityLabel="Guide filters" role="toolbar" style={{ flexDirection: "row", gap: 8 }}>
        {filters.map((option) => (
          <Action
            accessibilityLabel={`${option.label} channels`}
            density={density}
            disabled={option.disabled}
            key={option.value}
            onPress={() => onFilterChange?.(option.value)}
            selected={filter === option.value}
            tone="secondary"
          >
            {option.label}
          </Action>
        ))}
      </View>

      <ScrollView
        accessibilityLabel="Guide timeline"
        horizontal
        showsHorizontalScrollIndicator
        style={{ width: "100%" }}
      >
        <View style={{ minWidth: minimumGridWidth, width: "100%" }}>
          <View style={{ flexDirection: "row", minHeight: semanticTargets[density] }}>
            <View style={{ width: railWidth }} />
            <View style={{ flex: 1, flexDirection: "row", justifyContent: "space-between" }}>
              {ticks.map((tick) => (
                <Text density={density} key={tick} textRole="time">
                  {formatGuideTime(tick, layout.timezone)}
                </Text>
              ))}
            </View>
          </View>

          <Surface aria-label="Channel schedule" borderRadius={0} gap={2} role="group">
            {visibleChannels.map((channel) => {
              const logo = renderChannelLogo?.(channel);
              return (
                <View
                  key={channel.source.channelId}
                  style={{ flexDirection: "row", height: rowHeight, minWidth: 0 }}
                >
                  <View
                    style={{
                      borderRightColor: "transparent",
                      justifyContent: "center",
                      paddingRight: semanticSpace.control,
                      width: railWidth,
                    }}
                  >
                    <ChannelIdentity
                      channel={{
                        channelLogoState: channel.source.logo && logo ? "ready" : "missing",
                        channelName: channel.source.name,
                        channelNumber: String(channel.source.number),
                      }}
                      density={density}
                      logo={logo}
                    />
                  </View>
                  <View style={{ flex: 1, minWidth: 0, position: "relative" }}>
                    {channel.airings.map((airing) => {
                      const selected =
                        channel.source.channelId === selection.channelId &&
                        airing.scheduleBlockId === selection.scheduleBlockId;
                      const label = guideAiringLabel(airing.source);
                      return (
                        <Action
                          accessibilityLabel={`${channel.source.name}, ${label}, ${formatGuideTimeRange(
                            airing.source.startMs,
                            airing.source.stopMs,
                            layout.timezone,
                          )}`}
                          density={density}
                          key={airing.scheduleBlockId}
                          onFocus={() =>
                            onSelectionChange({
                              anchorMs:
                                airing.source.startMs + (airing.source.stopMs - airing.source.startMs) / 2,
                              channelId: channel.source.channelId,
                              scheduleBlockId: airing.scheduleBlockId,
                            })
                          }
                          onPress={() => {
                            const next = {
                              anchorMs:
                                airing.source.startMs + (airing.source.stopMs - airing.source.startMs) / 2,
                              channelId: channel.source.channelId,
                              scheduleBlockId: airing.scheduleBlockId,
                            };
                            onSelectionChange(next);
                            onTune?.(next);
                          }}
                          selected={selected}
                          style={{
                            height: rowHeight - 4,
                            left: `${airing.startRatio * 100}%`,
                            minHeight: 0,
                            overflow: "hidden",
                            paddingHorizontal: 8,
                            position: "absolute",
                            top: 2,
                            width: `${airing.widthRatio * 100}%`,
                          }}
                          tone={selected ? "primary" : "secondary"}
                        >
                          {airing.widthRatio >= 0.11 ? label : ""}
                        </Action>
                      );
                    })}
                  </View>
                </View>
              );
            })}
          </Surface>
        </View>
      </ScrollView>

      <Text density={density} textRole="metadata">
        {`${layout.channels.length} channels${channelWindow ? ` · ${channelWindow.positionLabel}` : ""} · ${formatGuideTimeRange(
          layout.fromMs,
          layout.toMs,
          layout.timezone,
        )}`}
      </Text>
    </Surface>
  );

  const detail =
    selectedAiring && selectedChannel ? (
      <ProgrammeCard
        artwork={artwork}
        channelLogo={channelLogo}
        density={density}
        focused
        programme={{
          artworkState:
            (selectedAiring.source.thumbImage || selectedAiring.source.thumbUrl) && artwork
              ? "ready"
              : "missing",
          badge: airingBadge(selectedAiring.source.kind, selectedAiring.isOnNow),
          channelLogoState: selectedChannel.source.logo && channelLogo ? "ready" : "missing",
          channelName: selectedChannel.source.name,
          channelNumber: String(selectedChannel.source.number),
          description: selectedAiring.source.description,
          episodeLabel: formatGuideEpisode(selectedAiring.source.season, selectedAiring.source.episode),
          facts: airingFacts(selectedAiring),
          progressPercent:
            selectedAiring.progressRatio === undefined ? undefined : selectedAiring.progressRatio * 100,
          seriesTitle: selectedAiring.source.series,
          timeLabel: formatGuideTimeRange(
            selectedAiring.source.startMs,
            selectedAiring.source.stopMs,
            layout.timezone,
          ),
          title: selectedAiring.source.title.trim() || guideAiringLabel(selectedAiring.source),
        }}
      />
    ) : (
      <Surface alignItems="center" justifyContent="center" minHeight={rowHeight * 3} padding="$section">
        <Text density={density} textAlign="center" textRole="body">
          Choose a programme to see its details.
        </Text>
      </Surface>
    );

  return (
    <AdaptiveSplit
      accessibilityLabel="Programme guide"
      density={density}
      primary={grid}
      secondary={detail}
      secondaryWidth={360}
    />
  );
};

const unavailableGuideCopy: Record<
  GuideUnavailableState,
  { description: string; kind: GuideUnavailableState; title: string }
> = {
  empty: {
    description: "No playable channels are available yet.",
    kind: "empty",
    title: "No channels on air",
  },
  error: {
    description: "The schedule could not be loaded. Your current channel is unchanged.",
    kind: "error",
    title: "Guide unavailable",
  },
  loading: {
    description: "Reading the latest channel schedule.",
    kind: "loading",
    title: "Loading channels",
  },
  offline: {
    description: "Reconnect to refresh the channel schedule.",
    kind: "offline",
    title: "You're offline",
  },
};

const GuideExperience = (props: GuideExperienceProps) => {
  if (!props.state || props.state === "ready") return <GuideSurface {...props} />;
  const copy = unavailableGuideCopy[props.state];
  const onRetry = "onRetry" in props ? props.onRetry : undefined;
  return (
    <StatePanel
      action={
        onRetry && (props.state === "error" || props.state === "offline")
          ? { label: "Try again", onPress: onRetry }
          : undefined
      }
      density={props.density}
      description={copy.description}
      kind={copy.kind}
      title={copy.title}
    />
  );
};

export { defaultFilters, GuideExperience, GuideSurface, guideRailWidth, guideRowHeight };
