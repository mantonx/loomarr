import {
  getFillerDecisionActivityMockHandler,
  getFillerDecisionDiagnosticsMockHandler,
  getMeMockHandler,
} from "@loomarr/api/msw";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import type { ReactNode } from "react";
import { describe, expect, it } from "vitest";
import { me } from "@/test/fixtures/users";
import { server } from "@/test/msw/server";
import { RouterHarness } from "@/test/story-utils";
import { FillerManage } from "./filler-manage";

const wrapper = ({ children }: { children: ReactNode }) => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return (
    <QueryClientProvider client={client}>
      <RouterHarness content={children} initialPath="/filler/manage" />
    </QueryClientProvider>
  );
};

describe("FillerManage", () => {
  it("distinguishes shadow and applied automatic outcomes", async () => {
    server.use(
      getMeMockHandler(me({ name: "Admin" })),
      getFillerDecisionActivityMockHandler({
        rows: [
          {
            id: "event-1",
            decisionId: "decision-1",
            clipHash: "abcdef012345",
            kind: "automatic_admit",
            applicationMode: "shadow",
            createdAt: "2026-08-25T12:00:00Z",
          },
          {
            id: "event-2",
            decisionId: "decision-2",
            clipHash: "123456abcdef",
            kind: "automatic_reject",
            applicationMode: "shadow",
            createdAt: "2026-08-25T12:00:01Z",
          },
          {
            id: "event-3",
            decisionId: "decision-3",
            clipHash: "fedcba654321",
            kind: "automatic_admit",
            applicationMode: "applied",
            createdAt: "2026-08-25T12:00:02Z",
          },
          {
            id: "event-4",
            decisionId: "decision-4",
            clipHash: "987654abcdef",
            kind: "automatic_reject",
            applicationMode: "applied",
            createdAt: "2026-08-25T12:00:03Z",
          },
        ],
        total: 4,
      }),
      getFillerDecisionDiagnosticsMockHandler({ rows: [], total: 0 }),
    );
    render(<FillerManage />, { wrapper });

    expect(await screen.findByText("Would admit (shadow)")).toHaveClass("text-caution");
    expect(screen.getByText("Would reject (shadow)")).toHaveClass("text-caution");
    expect(screen.getByText("Admitted automatically")).toHaveClass("text-signal");
    expect(screen.getByText("Rejected automatically")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Show filler diagnostics" })).toHaveAttribute(
      "aria-expanded",
      "false",
    );
    expect(screen.queryByText("Processing queue")).not.toBeInTheDocument();
  });

  it("does not claim an applied effect when decision mode is unavailable", async () => {
    server.use(
      getMeMockHandler(me({ name: "Admin" })),
      http.get("*/v1/filler/decisions/activity", () =>
        HttpResponse.json({
          rows: [
            {
              id: "event-unknown",
              decisionId: "decision-unknown",
              clipHash: "abcdef012345",
              kind: "automatic_admit",
              applicationMode: "automatic",
              createdAt: "2026-08-25T12:00:00Z",
            },
            {
              id: "event-omitted",
              decisionId: "decision-omitted",
              clipHash: "123456abcdef",
              kind: "automatic_reject",
              createdAt: "2026-08-25T12:00:01Z",
            },
          ],
          total: 2,
        }),
      ),
      getFillerDecisionDiagnosticsMockHandler({ rows: [], total: 0 }),
    );
    render(<FillerManage />, { wrapper });

    const unavailable = await screen.findAllByText("Decision mode unavailable");
    expect(unavailable).toHaveLength(2);
    for (const badge of unavailable) expect(badge).toHaveClass("text-caution");
    expect(screen.queryByText("Admitted automatically")).not.toBeInTheDocument();
    expect(screen.queryByText("Rejected automatically")).not.toBeInTheDocument();
  });

  it("shows recoverable failures only after opening Diagnostics", async () => {
    server.use(
      getMeMockHandler(me({ name: "Admin" })),
      getFillerDecisionActivityMockHandler({ rows: [], total: 0 }),
      getFillerDecisionDiagnosticsMockHandler({
        rows: [
          {
            id: "hold-1",
            clipHash: "abcdef012345",
            code: "provider_unavailable",
            recovery: "configure_provider",
            retryable: true,
            createdAt: "2026-08-25T12:00:00Z",
          },
        ],
        total: 1,
      }),
    );
    render(<FillerManage />, { wrapper });

    await userEvent.click(await screen.findByRole("button", { name: "Show filler diagnostics" }));
    expect(await screen.findByText("provider unavailable")).toBeInTheDocument();
    expect(screen.getByText(/Recovery: configure provider/)).toBeInTheDocument();
    expect(screen.queryByText("Processing queue")).not.toBeInTheDocument();
  });
});
