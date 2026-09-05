import { Surface } from "@loomarr/design-system";
import type { ReactNode } from "react";
import { useEffect, useSyncExternalStore } from "react";

import { GuideExperience } from "../guide";
import type { GuideJourneyProps } from "./guide-journey.type";

const GuideJourney = ({
  channelWindow,
  controller,
  density = "pointer",
  focusRegistry,
  onTune,
  preferredChannelId,
  renderArtwork,
  renderChannelLogo,
}: GuideJourneyProps) => {
  const snapshot = useSyncExternalStore(controller.subscribe, controller.getSnapshot, controller.getSnapshot);

  useEffect(() => {
    void controller.refresh(preferredChannelId);
  }, [controller, preferredChannelId]);

  const selectedAnchorMs = snapshot.selection?.anchorMs;
  const selectedChannelId = snapshot.selection?.channelId;
  const selectedScheduleBlockId = snapshot.selection?.scheduleBlockId;
  useEffect(() => {
    if (
      selectedAnchorMs === undefined ||
      selectedChannelId === undefined ||
      selectedScheduleBlockId === undefined
    )
      return;
    focusRegistry?.request({
      kind: "airing",
      selection: {
        anchorMs: selectedAnchorMs,
        channelId: selectedChannelId,
        scheduleBlockId: selectedScheduleBlockId,
      },
    });
  }, [focusRegistry, selectedAnchorMs, selectedChannelId, selectedScheduleBlockId]);

  let content: ReactNode;
  if (snapshot.status !== "ready" || !snapshot.layout || !snapshot.selection) {
    content = (
      <GuideExperience
        density={density}
        onRetry={snapshot.status === "error" ? () => void controller.refresh(preferredChannelId) : undefined}
        state={snapshot.status === "ready" ? "error" : snapshot.status}
      />
    );
  } else {
    content = (
      <GuideExperience
        channelWindow={channelWindow?.(snapshot.layout, snapshot.selection)}
        density={density}
        focusRegistry={focusRegistry}
        layout={snapshot.layout}
        onSelectionChange={controller.select}
        onTune={(selection) => onTune(selection.channelId)}
        renderArtwork={renderArtwork}
        renderChannelLogo={renderChannelLogo}
        selection={snapshot.selection}
      />
    );
  }

  return (
    <Surface borderRadius={0} borderWidth={0} flex={1} level="canvas">
      {content}
    </Surface>
  );
};

export { GuideJourney };
