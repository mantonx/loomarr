import { Surface } from "@loomarr/design-system";
import { useEffect, useMemo, useState, useSyncExternalStore } from "react";
import { DeviceDisconnectAction } from "../../device-disconnect";
import { StatePanel } from "../../state-panel";
import { restoreSurfSelection, surfGroupsFromGuide } from "../surf-data";
import { SurfRail } from "../surf-rail";
import type { SurfSelection } from "../surf-rail.type";
import type { SurfJourneyProps } from "./surf-journey.type";

const SurfJourney = ({
  clientVersion,
  controller,
  currentChannelId,
  density = "pointer",
  favoriteChannelIds,
  focusRegistry,
  now = Date.now,
  onDisconnect,
  onTune,
  playableChannelIds,
  recentChannelIds,
  renderArtwork,
  renderChannelLogo,
  restoreSelection = restoreSurfSelection,
  serverName,
  serverVersion,
}: SurfJourneyProps) => {
  const snapshot = useSyncExternalStore(controller.subscribe, controller.getSnapshot, controller.getSnapshot);
  const [selection, setSelection] = useState<SurfSelection>();

  useEffect(() => {
    void controller.refresh(currentChannelId);
  }, [controller, currentChannelId]);

  const groups = useMemo(
    () =>
      snapshot.layout
        ? surfGroupsFromGuide({
            currentChannelId,
            favoriteChannelIds,
            layout: snapshot.layout,
            nowMs: now(),
            playableChannelIds,
            recentChannelIds,
          })
        : [],
    [currentChannelId, favoriteChannelIds, now, playableChannelIds, recentChannelIds, snapshot.layout],
  );
  const preferredSelection = currentChannelId
    ? { channelId: currentChannelId, group: "recent" as const }
    : undefined;
  const resolvedSelection = selection
    ? restoreSelection(groups, selection)
    : preferredSelection
      ? restoreSelection(groups, preferredSelection)
      : restoreSurfSelection(groups);
  const resolvedChannelId = resolvedSelection?.channelId;
  const resolvedGroup = resolvedSelection?.group;
  useEffect(() => {
    if (!resolvedChannelId || !resolvedGroup) return;
    focusRegistry?.request({ channelId: resolvedChannelId, group: resolvedGroup });
  }, [focusRegistry, resolvedChannelId, resolvedGroup]);

  if (snapshot.status !== "ready" || !resolvedSelection) {
    const kind =
      snapshot.status === "error"
        ? "error"
        : snapshot.status === "empty" || snapshot.status === "ready"
          ? "empty"
          : "loading";
    return (
      <Surface
        alignItems="center"
        borderRadius={0}
        borderWidth={0}
        flex={1}
        gap="$control"
        justifyContent="center"
        level="canvas"
      >
        <StatePanel
          action={
            kind === "error"
              ? { label: "Try again", onPress: () => void controller.refresh(currentChannelId) }
              : undefined
          }
          density={density}
          description={
            kind === "error"
              ? "The latest channel schedule could not be loaded."
              : kind === "empty"
                ? "No playable channels are available yet."
                : "Reading the latest channels."
          }
          kind={kind}
          title={
            kind === "error"
              ? "Surf unavailable"
              : kind === "empty"
                ? "No channels on air"
                : "Loading channels"
          }
        />
        {onDisconnect ? (
          <DeviceDisconnectAction
            density={density}
            onDisconnect={onDisconnect}
            preferredFocus={density === "tv" && kind !== "error"}
            serverName={serverName}
          />
        ) : null}
      </Surface>
    );
  }

  return (
    <SurfRail
      clientVersion={clientVersion}
      currentChannelId={currentChannelId}
      density={density}
      focusRegistry={focusRegistry}
      groups={groups}
      onFocusSelection={setSelection}
      onDisconnect={onDisconnect}
      onTune={onTune}
      renderArtwork={renderArtwork}
      renderChannelLogo={renderChannelLogo}
      selection={resolvedSelection}
      serverName={serverName}
      serverVersion={serverVersion}
    />
  );
};

export { SurfJourney };
