import type { GuideAiringLayout, GuideLayout, GuideSelection } from "@loomarr/core/guide";
import type { Density } from "@loomarr/design-system";
import type { ReactNode } from "react";

import type { FocusTargetRegistry } from "../focus-target";

type GuideFilter = "all" | "favourites" | "recent";

type GuideFocusTarget =
  | { filter: GuideFilter; kind: "filter" }
  | { kind: "airing"; selection: GuideSelection };

type GuideFilterOption = {
  disabled?: boolean;
  label: string;
  value: GuideFilter;
};

type GuideArtworkRenderer = (airing: GuideAiringLayout) => ReactNode;
type GuideLogoRenderer = (channel: GuideLayout["channels"][number]) => ReactNode;

type GuideChannelWindow = {
  end: number;
  positionLabel: string;
  start: number;
};

interface GuideSurfaceProps {
  density?: Density;
  channelWindow?: GuideChannelWindow;
  filter?: GuideFilter;
  filters?: readonly GuideFilterOption[];
  focusRegistry?: FocusTargetRegistry<GuideFocusTarget>;
  layout: GuideLayout;
  onFilterChange?: (filter: GuideFilter) => void;
  onSelectionChange: (selection: GuideSelection) => void;
  onTune?: (selection: GuideSelection) => void;
  renderArtwork?: GuideArtworkRenderer;
  renderChannelLogo?: GuideLogoRenderer;
  selection: GuideSelection;
}

type GuideUnavailableState = "empty" | "error" | "loading" | "offline";
type GuideReadyProps = GuideSurfaceProps & { state?: "ready" };

interface GuideUnavailableProps {
  density?: Density;
  onRetry?: () => void;
  state: GuideUnavailableState;
}

type GuideExperienceProps = GuideReadyProps | GuideUnavailableProps;

export type {
  GuideArtworkRenderer,
  GuideChannelWindow,
  GuideExperienceProps,
  GuideFilter,
  GuideFilterOption,
  GuideFocusTarget,
  GuideLogoRenderer,
  GuideSurfaceProps,
  GuideUnavailableState,
};
