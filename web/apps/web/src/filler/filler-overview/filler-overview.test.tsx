import type { FillerDecisionOverviewDTO, FillerReadinessDTO } from "@loomarr/api";
import { getFillerDecisionOverviewMockHandler, getFillerReadinessMockHandler } from "@loomarr/api/msw";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { server } from "@/test/msw/server";
import { RouterHarness } from "@/test/story-utils";
import { FillerOverview } from "./filler-overview";

const decision = (over: Partial<FillerDecisionOverviewDTO> = {}): FillerDecisionOverviewDTO => ({
  healthy: true,
  nextAction: "none",
  counts: { admitted: 25, rejected: 12, reviews: 0, unresolvedReviews: 0, operational: 0, retryable: 0 },
  ...over,
});

const readiness = (over: Partial<FillerReadinessDTO> = {}): FillerReadinessDTO => {
  const { repairs, ...rest } = over;
  return {
    ready: true,
    nextAction: "none",
    repairs: repairs ?? { count: 0 },
    fetch: { enabled: true, catalogClips: 25 },
    pipeline: {
      runnable: 0,
      scheduled: 0,
      inProgress: 0,
      needsDecision: 0,
      recoverable: 0,
      admitted: 25,
      rejected: 0,
      dismissed: 0,
    },
    pool: { clips: 25, commercials: 20, eligible: 18, untagged: 0, channels: [] },
    acquisitions: [],
    ...rest,
  };
};

const show = (overview: FillerDecisionOverviewDTO, coverage: FillerReadinessDTO = readiness()) => {
  server.use(getFillerDecisionOverviewMockHandler(overview), getFillerReadinessMockHandler(coverage));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <RouterHarness
      content={
        <QueryClientProvider client={client}>
          <FillerOverview />
        </QueryClientProvider>
      }
      initialPath="/filler"
    />,
  );
};

describe("FillerOverview", () => {
  it("renders the server-owned healthy answer without inventing an action", async () => {
    show(decision());
    expect(await screen.findByText("Filler is working on its own")).toBeInTheDocument();
    expect(screen.getByText("Working automatically")).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: /diagnostics|review clips/i })).not.toBeInTheDocument();
  });

  it("renders the server-ranked review action and distinct counts", async () => {
    show(
      decision({
        healthy: false,
        nextAction: "review_decisions",
        actionCount: 4,
        counts: { admitted: 18, rejected: 9, reviews: 5, unresolvedReviews: 4, operational: 2, retryable: 1 },
      }),
    );

    expect(await screen.findByText("A few clips need your judgment")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Review clips" })).toHaveAttribute("href", "/filler/incoming");
    const summary = screen.getByRole("heading", { name: "Admission summary" }).parentElement?.parentElement;
    expect(summary).toBeTruthy();
    expect(within(summary as HTMLElement).getByText("18")).toBeInTheDocument();
    expect(within(summary as HTMLElement).getByText("9")).toBeInTheDocument();
    expect(within(summary as HTMLElement).getByText("4")).toBeInTheDocument();
    expect(within(summary as HTMLElement).getByText("2")).toBeInTheDocument();
  });

  it("routes operational recovery to diagnostics, never the review queue", async () => {
    show(decision({ healthy: false, nextAction: "retry_processing", actionCount: 2 }));
    expect(await screen.findByText("Some filler can be retried")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Open diagnostics" })).toHaveAttribute("href", "/filler/manage");
  });

  it("keeps channel coverage separate from admission health", async () => {
    show(
      decision(),
      readiness({
        pool: {
          clips: 25,
          commercials: 20,
          eligible: 18,
          untagged: 0,
          channels: [
            {
              channelId: "ch-42",
              name: "Saturday Mornings",
              number: 42,
              level: "widened",
              total: 12,
              durationMs: 360_000,
              categories: 3,
              brands: 7,
            },
          ],
        },
      }),
    );
    expect(await screen.findByText("6m playable · 12 clips")).toBeInTheDocument();
    expect(screen.getByText("3 categories · 7 brands")).toBeInTheDocument();
  });
});
