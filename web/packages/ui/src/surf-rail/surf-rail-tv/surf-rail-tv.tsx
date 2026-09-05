import { ProgressTrack, ScrollFrame, Surface, Text } from "@loomarr/design-system";
import type { ComponentRef } from "react";
import { useRef } from "react";
import { Pressable } from "react-native";
import { DeviceDisconnectAction } from "../../device-disconnect";
import type { FocusTargetRegistry } from "../../focus-target";
import type { SurfChannelData, SurfGroupKind, SurfRailProps } from "../surf-rail.type";

const TvSurfChannel = ({
  channel,
  current,
  focusRegistry,
  group,
  onFocus,
  onTune,
  selected,
}: {
  channel: SurfChannelData;
  current: boolean;
  focusRegistry?: FocusTargetRegistry<{ channelId: string; group: SurfGroupKind }>;
  group: SurfGroupKind;
  onFocus: () => void;
  onTune: () => void;
  selected: boolean;
}) => (
  <Pressable
    accessibilityLabel={`${group}, channel ${channel.channelNumber}, ${channel.channelName}`}
    accessibilityRole="button"
    accessibilityState={{ selected }}
    hasTVPreferredFocus={selected}
    onFocus={onFocus}
    onPress={onTune}
    ref={(handle) => focusRegistry?.register({ channelId: channel.id, group }, handle)}
  >
    <Surface
      backgroundColor={selected ? "$surfaceRaised" : "$surfaceCanvas"}
      borderColor={selected ? "$actionFocus" : "$surfaceCanvas"}
      borderRadius={12}
      borderWidth={selected ? 3 : 1}
      gap={4}
      paddingHorizontal={16}
      paddingVertical={12}
    >
      <Surface
        alignItems="center"
        backgroundColor="$transparent"
        borderWidth={0}
        flexDirection="row"
        gap={12}
      >
        <Text density="tv" textRole="data" tone={selected ? "signal" : "muted"}>
          {channel.channelNumber.padStart(2, "0")}
        </Text>
        <Text density="tv" flex={1} numberOfLines={1} textRole="compact">
          {channel.channelName}
        </Text>
        {current ? (
          <Surface
            backgroundColor="$stateSuccess"
            borderRadius="$round"
            borderWidth={0}
            height={8}
            width={8}
          />
        ) : null}
      </Surface>
      {selected ? (
        <>
          <Surface
            alignItems="center"
            backgroundColor="$transparent"
            borderWidth={0}
            flexDirection="row"
            gap="$inline"
            paddingLeft={32}
          >
            <Text density="tv" flex={1} numberOfLines={1} textRole="caption" tone="muted">
              {channel.now?.seriesTitle
                ? channel.now.title.replace(`${channel.now.seriesTitle} · `, "")
                : (channel.now?.title ?? "Nothing scheduled")}
            </Text>
            {channel.now?.remainingLabel ? (
              <Text density="tv" textRole="metadata" tone="muted">
                {channel.now.remainingLabel}
              </Text>
            ) : null}
          </Surface>
          <Surface backgroundColor="$transparent" borderWidth={0} paddingLeft={32} paddingTop={4}>
            <ProgressTrack
              accessibilityLabel={channel.now?.title ?? channel.channelName}
              percent={channel.now?.progressPercent ?? 0}
              width="100%"
            />
          </Surface>
        </>
      ) : null}
    </Surface>
  </Pressable>
);

const TvSurfRail = ({
  clientVersion,
  currentChannelId,
  focusRegistry,
  groups,
  onFocusSelection,
  onDisconnect,
  onTune,
  selection,
  serverName,
  serverVersion,
}: SurfRailProps) => {
  const scrollFrame = useRef<ComponentRef<typeof ScrollFrame>>(null);
  const selectable = groups.flatMap((group) =>
    group.channels.map((channel) => ({ channel, group: group.kind })),
  );
  const selectedIndex = Math.max(
    0,
    selectable.findIndex(
      ({ channel, group }) => selection.channelId === channel.id && selection.group === group,
    ),
  );

  return (
    <Surface
      accessibilityLabel="Channel surfer"
      backgroundColor="$transparent"
      borderRadius={0}
      borderWidth={0}
      flex={1}
    >
      <Surface
        backgroundColor="$surfaceChrome"
        borderRadius={0}
        borderWidth={0}
        bottom={0}
        left={0}
        paddingBottom={48}
        paddingLeft={48}
        paddingRight="$control"
        paddingTop={48}
        position="absolute"
        top={0}
        width={420}
      >
        <ScrollFrame
          density="tv"
          onContentSizeChange={() => {
            if (selection.group === "all") scrollFrame.current?.scrollToEnd({ animated: false });
          }}
          ref={scrollFrame}
        >
          {groups.map((group) => (
            <Surface backgroundColor="$transparent" borderWidth={0} gap={4} key={group.kind}>
              <Text density="tv" paddingBottom={4} paddingTop={12} textRole="section" tone="muted">
                {`${group.label.toUpperCase()} · ${group.channels.length}`}
              </Text>
              {group.channels.length === 0 ? (
                <Text density="tv" textRole="metadata" tone="muted">
                  {group.kind === "favourites" ? "No favorites yet" : "No recent channels yet"}
                </Text>
              ) : (
                group.channels.map((channel) => {
                  const selected = selection.group === group.kind && selection.channelId === channel.id;
                  return (
                    <TvSurfChannel
                      channel={channel}
                      current={channel.id === currentChannelId}
                      focusRegistry={focusRegistry}
                      group={group.kind}
                      key={`${group.kind}-${channel.id}`}
                      onFocus={() => onFocusSelection({ channelId: channel.id, group: group.kind })}
                      onTune={() => onTune(channel.id)}
                      selected={selected}
                    />
                  );
                })
              )}
            </Surface>
          ))}
          {onDisconnect ? (
            <DeviceDisconnectAction density="tv" onDisconnect={onDisconnect} serverName={serverName} />
          ) : null}
        </ScrollFrame>
        <Text density="tv" textRole="metadata" tone="muted">
          {`${selectedIndex + 1} of ${selectable.length} · ▲▼ browse`}
        </Text>
        <Text density="tv" numberOfLines={1} textRole="metadata" tone="muted">
          {`Loomarr TV ${clientVersion} · Server ${serverVersion ?? "unavailable"}`}
        </Text>
      </Surface>
      <Surface
        backgroundColor="$surfaceOverlay"
        borderRadius={8}
        bottom={48}
        padding="$control"
        position="absolute"
        right={48}
      >
        <Text density="tv" textRole="metadata" tone="muted">
          OK tune · BACK cancel
        </Text>
      </Surface>
    </Surface>
  );
};

export { TvSurfRail };
