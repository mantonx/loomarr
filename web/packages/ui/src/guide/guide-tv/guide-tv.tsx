import { formatGuideTime, formatGuideTimeRange, guideAiringLabel } from "@loomarr/core/guide";
import { ArtworkFrame, Surface, Text } from "@loomarr/design-system";
import type { ComponentRef } from "react";
import { forwardRef, useState } from "react";
import { Pressable, ScrollView, View } from "react-native";

import type { GuideFilterOption, GuideSurfaceProps } from "../guide.type";

const tvCanvasWidth = 960;
const channelRailWidth = 298;
const positionRailWidth = 12;
const rulerHeight = 36;
const rowHeight = 48;
const detailHeight = 124;
const channelRailPercent = (channelRailWidth / tvCanvasWidth) * 100;
const timelinePercent = ((tvCanvasWidth - channelRailWidth - positionRailWidth) / tvCanvasWidth) * 100;
const hourMs = 60 * 60_000;

const formatTvEpisode = (season?: number, episode?: number) => {
  const seasonLabel = season === undefined ? "" : `S${season}`;
  const episodeLabel = episode === undefined ? "" : `E${episode}`;
  return seasonLabel || episodeLabel ? `${seasonLabel}${episodeLabel}` : undefined;
};

type FilterButtonProps = {
  accessibilityLabel: string;
  children: string;
  disabled?: boolean;
  onPress: () => void;
  selected: boolean;
};

const FilterButton = forwardRef<ComponentRef<typeof Pressable>, FilterButtonProps>(
  ({ accessibilityLabel, children, disabled = false, onPress, selected }, ref) => {
    const [focused, setFocused] = useState(false);
    return (
      <Pressable
        accessibilityLabel={accessibilityLabel}
        accessibilityRole="button"
        accessibilityState={{ disabled, selected }}
        aria-disabled={disabled || undefined}
        aria-pressed={selected || undefined}
        disabled={disabled}
        onBlur={() => setFocused(false)}
        onFocus={() => setFocused(true)}
        onPress={onPress}
        ref={ref}
      >
        <Surface
          backgroundColor={selected ? "$stateWarningSurface" : "$surfaceCanvas"}
          borderColor={focused || selected ? "$actionFocus" : "$borderDecorative"}
          borderRadius={12}
          borderWidth={focused ? 2 : 1}
          opacity={disabled ? 0.4 : 1}
          paddingHorizontal={12}
          paddingVertical={4}
        >
          <Text density="tv" textRole="metadata" tone={focused ? "signal" : selected ? "primary" : "muted"}>
            {children}
          </Text>
        </Surface>
      </Pressable>
    );
  },
);

FilterButton.displayName = "FilterButton";

const TvGuideSurface = ({
  channelWindow,
  filter = "all",
  filters,
  focusRegistry,
  layout,
  onFilterChange,
  onSelectionChange,
  onTune,
  renderArtwork,
  selection,
}: GuideSurfaceProps & { filters: readonly GuideFilterOption[] }) => {
  const selectedChannel = layout.channels.find((channel) => channel.source.channelId === selection.channelId);
  const selectedAiring = selectedChannel?.airings.find(
    (airing) => airing.scheduleBlockId === selection.scheduleBlockId,
  );
  const artwork = selectedAiring ? renderArtwork?.(selectedAiring) : undefined;
  const visibleChannels = channelWindow
    ? layout.channels.slice(channelWindow.start, channelWindow.end)
    : layout.channels;
  const span = layout.toMs - layout.fromMs;
  const ticks = Array.from(
    { length: Math.max(1, Math.ceil(span / hourMs)) },
    (_, index) => layout.fromMs + index * hourMs,
  ).filter((tick) => tick < layout.toMs);
  const onNow = layout.channels.flatMap((channel) => channel.airings).find((airing) => airing.isOnNow);
  const nowMs =
    onNow?.progressRatio === undefined
      ? undefined
      : onNow.source.startMs + onNow.progressRatio * (onNow.source.stopMs - onNow.source.startMs);
  const nowPercent = nowMs === undefined ? undefined : ((nowMs - layout.fromMs) / span) * 100;

  return (
    <Surface
      aria-label="Programme guide"
      borderRadius={0}
      borderWidth={0}
      flex={1}
      level="canvas"
      overflow="hidden"
    >
      <Surface
        alignItems="center"
        backgroundColor="$transparent"
        borderWidth={0}
        flexDirection="row"
        gap={12}
        paddingBottom={12}
        paddingHorizontal={48}
        paddingTop={48}
      >
        <Text density="tv" textRole="headline">
          Guide
        </Text>
        {filters.map((option) => {
          const count = option.value === "all" ? layout.channels.length : 0;
          const name = option.value === "favourites" ? "Favorites" : option.label;
          const label = `${option.value === "favourites" ? "★ " : ""}${name} · ${count}`;
          return (
            <FilterButton
              accessibilityLabel={`${name} channels`}
              disabled={option.disabled}
              key={option.value}
              onPress={() => onFilterChange?.(option.value)}
              ref={(handle) => focusRegistry?.register({ filter: option.value, kind: "filter" }, handle)}
              selected={filter === option.value}
            >
              {label}
            </FilterButton>
          );
        })}
        <Text density="tv" flex={1} textAlign="right" textRole="metadata" tone="muted">
          {channelWindow?.positionLabel ?? "▲ Filters"}
        </Text>
      </Surface>

      <Surface
        backgroundColor="$transparent"
        borderRadius={0}
        borderWidth={0}
        flex={1}
        minHeight={0}
        position="relative"
      >
        <View style={{ flexDirection: "row", height: rulerHeight, paddingRight: positionRailWidth }}>
          <View style={{ justifyContent: "center", paddingLeft: 60, width: channelRailWidth }}>
            <Text density="tv" textRole="section" tone="muted">
              CHANNEL
            </Text>
          </View>
          <View style={{ flex: 1, position: "relative" }}>
            {ticks.map((tick) => (
              <Text
                density="tv"
                key={tick}
                position="absolute"
                left={`${((tick - layout.fromMs) / span) * 100}%`}
                marginLeft={6}
                textRole="time"
                tone="muted"
              >
                {formatGuideTime(tick, layout.timezone)}
              </Text>
            ))}
          </View>
        </View>

        <ScrollView style={{ flex: 1, marginRight: positionRailWidth }}>
          <Surface aria-label="Channel schedule" borderRadius={0} borderWidth={0} role="group">
            {visibleChannels.map((channel) => {
              const selectedRow = channel.source.channelId === selection.channelId;
              return (
                <View
                  key={channel.source.channelId}
                  style={{ flexDirection: "row", height: rowHeight, minWidth: 0 }}
                >
                  <Surface
                    alignItems="center"
                    backgroundColor={selectedRow ? "$surfaceRaised" : "$surfaceCanvas"}
                    borderRadius={0}
                    borderWidth={0}
                    flexDirection="row"
                    gap={12}
                    paddingBottom={4}
                    paddingLeft={60}
                    paddingRight={8}
                    width={channelRailWidth}
                  >
                    <Text density="tv" textRole="data" tone={selectedRow ? "signal" : "muted"}>
                      {String(channel.source.number).padStart(2, "0")}
                    </Text>
                    <Text density="tv" flex={1} numberOfLines={1} textRole="caption">
                      {channel.source.name}
                    </Text>
                  </Surface>
                  <View style={{ flex: 1, minWidth: 0, position: "relative" }}>
                    {channel.airings.length === 0 ? (
                      <Text density="tv" textRole="caption" tone="muted">
                        Nothing scheduled
                      </Text>
                    ) : null}
                    {channel.airings.map((airing) => {
                      const selected =
                        channel.source.channelId === selection.channelId &&
                        airing.scheduleBlockId === selection.scheduleBlockId;
                      const label = airing.source.series?.trim() || guideAiringLabel(airing.source);
                      const next = {
                        anchorMs: airing.source.startMs + (airing.source.stopMs - airing.source.startMs) / 2,
                        channelId: channel.source.channelId,
                        scheduleBlockId: airing.scheduleBlockId,
                      };
                      const target = { kind: "airing" as const, selection: next };
                      return (
                        <Pressable
                          accessibilityLabel={`${channel.source.name}, ${label}, ${formatGuideTimeRange(
                            airing.source.startMs,
                            airing.source.stopMs,
                            layout.timezone,
                          )}`}
                          accessibilityRole="button"
                          hasTVPreferredFocus={selected}
                          key={airing.scheduleBlockId}
                          onFocus={() => onSelectionChange(next)}
                          onPress={() => {
                            onSelectionChange(next);
                            onTune?.(next);
                          }}
                          ref={(handle) => focusRegistry?.register(target, handle)}
                          style={{
                            height: rowHeight,
                            left: `${airing.startRatio * 100}%`,
                            overflow: "hidden",
                            paddingBottom: 2,
                            position: "absolute",
                            top: 0,
                            width: `${airing.widthRatio * 100}%`,
                          }}
                        >
                          <Surface
                            backgroundColor={
                              selected || airing.isOnNow ? "$stateAiringSurface" : "$surfaceElevated"
                            }
                            borderColor={
                              selected
                                ? "$actionFocus"
                                : airing.isOnNow
                                  ? "$borderAiring"
                                  : "$borderDecorative"
                            }
                            borderRadius={4}
                            borderWidth={selected ? 3 : 1}
                            height="100%"
                            justifyContent="center"
                            overflow="hidden"
                            paddingHorizontal={12}
                          >
                            <Text density="tv" numberOfLines={1} textRole="caption">
                              {airing.widthRatio >= 0.08 ? label : ""}
                            </Text>
                          </Surface>
                        </Pressable>
                      );
                    })}
                  </View>
                </View>
              );
            })}
          </Surface>
        </ScrollView>

        {nowPercent === undefined ? null : (
          <Surface
            backgroundColor="$stateLive"
            borderRadius={0}
            borderWidth={0}
            bottom={0}
            left={`${channelRailPercent + (nowPercent * timelinePercent) / 100}%`}
            position="absolute"
            top={rulerHeight}
            width={3}
          />
        )}
        <Surface
          backgroundColor="$surfaceCanvas"
          borderRadius={0}
          borderWidth={0}
          bottom={0}
          position="absolute"
          right={0}
          top={rulerHeight}
          width={positionRailWidth}
        >
          {layout.channels.length ? (
            <Surface
              backgroundColor="$borderDecorative"
              borderRadius={4}
              borderWidth={0}
              height={`${Math.min(1, 5 / layout.channels.length) * 100}%`}
              marginHorizontal={4}
              position="absolute"
              top={`${
                layout.channels.length <= 1
                  ? 0
                  : (Math.max(
                      0,
                      layout.channels.findIndex(
                        (channel) => channel.source.channelId === selection.channelId,
                      ),
                    ) /
                      (layout.channels.length - 1)) *
                    (1 - Math.min(1, 5 / layout.channels.length)) *
                    100
              }%`}
            />
          ) : null}
        </Surface>
      </Surface>

      {selectedAiring && selectedChannel ? (
        <Surface
          alignItems="center"
          borderRadius={0}
          flexDirection="row"
          gap={12}
          height={detailHeight}
          paddingBottom={8}
          paddingLeft={56}
          paddingRight={56}
          paddingTop={8}
        >
          {artwork ? (
            <ArtworkFrame density="tv" height={76} state="ready" width={136}>
              {artwork}
            </ArtworkFrame>
          ) : (
            <Surface
              alignItems="center"
              backgroundColor="$artworkPlaceholder"
              borderRadius={8}
              height={76}
              justifyContent="center"
              width={136}
            >
              <Text density="tv" textRole="metadata" tone="primary">
                NO ART
              </Text>
            </Surface>
          )}
          <Surface backgroundColor="$transparent" borderWidth={0} flex={1} gap={2} minWidth={0}>
            <Text density="tv" numberOfLines={1} textRole="compact">
              {selectedAiring.source.series ??
                (selectedAiring.source.title.trim() || guideAiringLabel(selectedAiring.source))}
            </Text>
            <Text density="tv" numberOfLines={1} textRole="metadata" tone="primary">
              {[
                selectedAiring.source.series ? `“${selectedAiring.source.title}”` : undefined,
                formatTvEpisode(selectedAiring.source.season, selectedAiring.source.episode),
                selectedAiring.source.year ? String(selectedAiring.source.year) : undefined,
                selectedAiring.source.rating,
              ]
                .filter(Boolean)
                .join(" · ")}
            </Text>
            <Text density="tv" numberOfLines={1} textRole="metadata">
              {[
                selectedAiring.source.genres?.slice(0, 2).join(" / "),
                formatGuideTimeRange(
                  selectedAiring.source.startMs,
                  selectedAiring.source.stopMs,
                  layout.timezone,
                ),
                `CH ${selectedChannel.source.number}`,
              ]
                .filter(Boolean)
                .join(" · ")}
            </Text>
            {selectedAiring.source.description ? (
              <Text density="tv" numberOfLines={1} textRole="caption" tone="muted">
                {selectedAiring.source.description}
              </Text>
            ) : null}
          </Surface>
        </Surface>
      ) : null}
    </Surface>
  );
};

export { TvGuideSurface };
