import type { SurfGroupData, SurfSelection } from "@loomarr/ui";

import type { TvSurfActivation, TvSurfDirection, TvSurfMoveResult } from "./surf-navigation.type";

const surfSelections = (groups: readonly SurfGroupData[]): SurfSelection[] =>
  groups.flatMap((group) => group.channels.map((channel) => ({ channelId: channel.id, group: group.kind })));

const moveTvSurfSelection = (
  groups: readonly SurfGroupData[],
  selection: SurfSelection,
  direction: TvSurfDirection,
): TvSurfMoveResult => {
  const selections = surfSelections(groups);
  const index = selections.findIndex(
    (candidate) => candidate.group === selection.group && candidate.channelId === selection.channelId,
  );
  const next = selections[index + (direction === "up" ? -1 : 1)];
  return next ? { selection: next } : { boundary: direction, selection };
};

const restoreTvSurfSelection = (
  groups: readonly SurfGroupData[],
  selection: SurfSelection,
): SurfSelection | undefined => {
  const selections = surfSelections(groups);
  return (
    selections.find(
      (candidate) => candidate.group === selection.group && candidate.channelId === selection.channelId,
    ) ??
    selections.find((candidate) => candidate.channelId === selection.channelId) ??
    selections[0]
  );
};

const activateTvSurfSelection = (selection: SurfSelection): TvSurfActivation => ({
  channelId: selection.channelId,
  kind: "tune",
});

export { activateTvSurfSelection, moveTvSurfSelection, restoreTvSurfSelection, surfSelections };
