import type { GuideController, GuideLayout, GuideSelection } from "@loomarr/core/guide";
import type { Density } from "@loomarr/design-system";

import type { FocusTargetRegistry } from "../../focus-target";
import type {
  GuideArtworkRenderer,
  GuideChannelWindow,
  GuideFocusTarget,
  GuideLogoRenderer,
} from "../guide.type";

interface GuideJourneyProps {
  channelWindow?: (layout: GuideLayout, selection: GuideSelection) => GuideChannelWindow;
  controller: GuideController;
  density?: Density;
  focusRegistry?: FocusTargetRegistry<GuideFocusTarget>;
  onTune: (channelId: string) => void;
  preferredChannelId?: string;
  renderArtwork?: GuideArtworkRenderer;
  renderChannelLogo?: GuideLogoRenderer;
}

export type { GuideJourneyProps };
