import { createGuideController } from "@loomarr/core";
import { guideChannels, guideFrom, guideNow, guideTo } from "@loomarr/fixtures";
import { GuideJourney } from "@loomarr/ui";
import type { Meta, StoryObj } from "@storybook/react-native";
import { useEffect, useMemo } from "react";

import { tvGuideChannels, tvGuideFrom, tvGuideNow, tvGuideTo } from "./tv-parity-fixtures";

type GuideScenario = "empty" | "error" | "loading" | "ready";

const GuideJourneyWorkshop = ({
  density = "touch",
  scenario = "ready",
}: {
  density?: "touch" | "tv";
  scenario?: GuideScenario;
}) => {
  const tv = density === "tv";
  const channels = tv ? tvGuideChannels : guideChannels;
  const fromMs = tv ? tvGuideFrom : guideFrom;
  const nowMs = tv ? tvGuideNow : guideNow;
  const toMs = tv ? tvGuideTo : guideTo;
  const controller = useMemo(
    () =>
      createGuideController({
        now: () => nowMs,
        source: {
          load: async (_signal) => {
            if (scenario === "error") throw new Error("The guide could not be loaded.");
            if (scenario === "loading") return new Promise(() => undefined);
            return {
              channels: scenario === "empty" ? [] : channels,
              fromMs,
              timezone: "America/New_York",
              toMs,
            };
          },
        },
      }),
    [channels, fromMs, nowMs, scenario, toMs],
  );
  useEffect(() => () => controller.dispose(), [controller]);

  return (
    <GuideJourney
      controller={controller}
      density={density}
      onTune={() => undefined}
      preferredChannelId={tv ? "noir" : "ch-springfield"}
    />
  );
};

const meta = {
  title: "Loomarr Components/Guide Journey",
  component: GuideJourneyWorkshop,
} satisfies Meta<typeof GuideJourneyWorkshop>;

type Story = StoryObj<typeof meta>;
const Touch: Story = {};
const Tv: Story = { args: { density: "tv" } };
const TvEmpty: Story = { args: { density: "tv", scenario: "empty" } };
const TvError: Story = { args: { density: "tv", scenario: "error" } };
const TvLoading: Story = { args: { density: "tv", scenario: "loading" } };
const Light: Story = { globals: { theme: "light" } };

export default meta;
export { Light, Touch, Tv, TvEmpty, TvError, TvLoading };
