import {
  Action,
  AdaptiveSplit,
  ArtworkFrame,
  type Density,
  ProgressTrack,
  ScrollFrame,
  Surface,
  Text,
} from "@loomarr/design-system";

import { ChannelIdentity, ProgrammeIdentity } from "../identity";
import { StatePanel } from "../state-panel";
import type { SurfChannelData, SurfGroupData, SurfRailProps, SurfSelection } from "./surf-rail.type";
import { TvSurfRail } from "./surf-rail-tv";

const surfRailWidth = (density: Density) => (density === "tv" ? 680 : density === "touch" ? "100%" : 480);

const findSurfChannel = (
  groups: readonly SurfGroupData[],
  selection: SurfSelection,
): SurfChannelData | undefined =>
  groups
    .find((group) => group.kind === selection.group)
    ?.channels.find((channel) => channel.id === selection.channelId);

const SurfRail = ({
  clientVersion,
  currentChannelId,
  density = "pointer",
  focusRegistry,
  groups,
  onFocusSelection,
  onDisconnect,
  onTune,
  renderArtwork,
  renderChannelLogo,
  selection,
  serverName,
  serverVersion,
}: SurfRailProps) => {
  const viewportWidth = Reflect.get(globalThis, "innerWidth");
  // The dedicated ten-foot rail assumes the 1280px canvas used by supported TV hosts.
  // Storybook and browser previews can render TV density in a phone-sized viewport; retain
  // the adaptive rail there so channel identity and programme detail remain usable.
  if (density === "tv" && (viewportWidth === undefined || viewportWidth >= 1280)) {
    return (
      <TvSurfRail
        clientVersion={clientVersion}
        currentChannelId={currentChannelId}
        density={density}
        focusRegistry={focusRegistry}
        groups={groups}
        onFocusSelection={onFocusSelection}
        onDisconnect={onDisconnect}
        onTune={onTune}
        renderArtwork={renderArtwork}
        renderChannelLogo={renderChannelLogo}
        selection={selection}
        serverName={serverName}
        serverVersion={serverVersion}
      />
    );
  }
  const selectedChannel = findSurfChannel(groups, selection);
  const selectedArtwork = selectedChannel ? renderArtwork?.(selectedChannel) : undefined;
  const selectedLogo = selectedChannel ? renderChannelLogo?.(selectedChannel) : undefined;

  return (
    <Surface
      accessibilityLabel="Channel surfer"
      gap="$control"
      level="overlay"
      maxHeight="100%"
      maxWidth="100%"
      padding={density === "tv" ? "$section" : "$control"}
      width={surfRailWidth(density)}
    >
      <Surface
        alignItems="center"
        backgroundColor="$transparent"
        borderWidth={0}
        flexDirection="row"
        justifyContent="space-between"
      >
        <Text density={density} textRole="title">
          Channel surfer
        </Text>
        <Text density={density} textRole="metadata" tone="info">
          LIVE TV
        </Text>
      </Surface>

      {selectedChannel?.now ? (
        <Surface gap="$control" padding="$control">
          <AdaptiveSplit
            breakpoint={density === "touch" ? 520 : 420}
            density={density}
            gap="$control"
            primary={
              <ArtworkFrame
                density={density}
                state={selectedArtwork ? selectedChannel.now.artworkState : "missing"}
                width="100%"
              >
                {selectedArtwork}
              </ArtworkFrame>
            }
            secondary={
              <Surface backgroundColor="$transparent" borderWidth={0} gap="$control">
                <ChannelIdentity
                  channel={{
                    channelLogoState: selectedLogo ? selectedChannel.channelLogoState : "missing",
                    channelName: selectedChannel.channelName,
                    channelNumber: selectedChannel.channelNumber,
                  }}
                  density={density}
                  logo={selectedLogo}
                />
                <ProgrammeIdentity density={density} programme={selectedChannel.now} />
              </Surface>
            }
            secondaryWidth={density === "tv" ? 360 : 250}
          />
          {selectedChannel.now.progressPercent === undefined ? null : (
            <ProgressTrack percent={selectedChannel.now.progressPercent} tone="live" width="100%" />
          )}
          {selectedChannel.next ? (
            <Text density={density} numberOfLines={1} textRole="metadata">
              {`Next ${selectedChannel.next.timeLabel} · ${selectedChannel.next.title}`}
            </Text>
          ) : null}
        </Surface>
      ) : selectedChannel ? (
        <StatePanel
          density={density}
          description="This channel has no current programme information."
          kind="empty"
          title="Schedule unavailable"
        />
      ) : null}

      <ScrollFrame density={density} style={{ maxHeight: density === "tv" ? 440 : 320 }}>
        {groups.map((group) => (
          <Surface backgroundColor="$transparent" borderWidth={0} gap="$inline" key={group.kind}>
            <Text density={density} textRole="metadata" tone="secondary">
              {group.label.toUpperCase()}
            </Text>
            {group.channels.length === 0 ? (
              <Text density={density} textRole="body" tone="muted">
                {group.kind === "favourites" ? "No favourites yet" : "No channels here yet"}
              </Text>
            ) : (
              group.channels.map((channel) => {
                const selected = selection.group === group.kind && selection.channelId === channel.id;
                return (
                  <Action
                    accessibilityLabel={`${group.label}, channel ${channel.channelNumber}, ${channel.channelName}`}
                    density={density}
                    key={`${group.kind}-${channel.id}`}
                    onFocus={() => onFocusSelection({ channelId: channel.id, group: group.kind })}
                    onPress={() => onTune(channel.id)}
                    selected={selected}
                    tone={selected ? "primary" : "secondary"}
                  >
                    {`${channel.channelNumber}  ${channel.channelName}`}
                  </Action>
                );
              })
            )}
          </Surface>
        ))}
      </ScrollFrame>

      <Text density={density} textAlign="center" textRole="metadata">
        {`Loomarr TV ${clientVersion} · Server ${serverVersion ?? "unavailable"}`}
      </Text>
    </Surface>
  );
};

export { findSurfChannel, SurfRail, surfRailWidth };
