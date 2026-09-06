import type { FillerDecisionOverviewDTO, FillerReadinessDTO } from "@loomarr/api";
import type { Decorator, Meta, StoryObj } from "@storybook/react-vite";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { widthFrame, withRouter } from "@/test/story-utils";
import { FillerOverview } from "./filler-overview";

const readiness: FillerReadinessDTO = {
  ready: true,
  nextAction: "none",
  repairs: { count: 0 },
  fetch: { enabled: true, catalogClips: 48 },
  pipeline: {
    runnable: 0,
    scheduled: 0,
    inProgress: 0,
    needsDecision: 0,
    recoverable: 0,
    admitted: 48,
    rejected: 22,
    dismissed: 0,
  },
  pool: {
    clips: 48,
    commercials: 38,
    eligible: 42,
    untagged: 0,
    channels: [
      {
        channelId: "ch-7",
        name: "Saturday Mornings",
        number: 7,
        level: "exact",
        total: 31,
        durationMs: 780_000,
        categories: 6,
        brands: 19,
      },
    ],
  },
  acquisitions: [],
};

const withOverview =
  (overview: FillerDecisionOverviewDTO): Decorator =>
  (Story) => {
    window.fetch = ((input: RequestInfo | URL) => {
      const url = String(input);
      const body = url.includes("/decisions/overview") ? overview : readiness;
      return Promise.resolve(
        new Response(JSON.stringify(body), { status: 200, headers: { "content-type": "application/json" } }),
      );
    }) as typeof fetch;
    return (
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <Story />
      </QueryClientProvider>
    );
  };

const meta = {
  title: "Filler/DecisionOverview",
  component: FillerOverview,
  decorators: [widthFrame(960), withRouter("/filler")],
} satisfies Meta<typeof FillerOverview>;

export default meta;
type Story = StoryObj<typeof meta>;

export const HealthyZeroWork: Story = {
  decorators: [
    withOverview({
      healthy: true,
      nextAction: "none",
      counts: { admitted: 48, rejected: 22, reviews: 1, unresolvedReviews: 0, operational: 0, retryable: 0 },
    }),
  ],
};

export const RecoverableFailure: Story = {
  decorators: [
    withOverview({
      healthy: false,
      nextAction: "retry_processing",
      actionCount: 2,
      counts: { admitted: 46, rejected: 22, reviews: 1, unresolvedReviews: 0, operational: 2, retryable: 2 },
    }),
  ],
};
