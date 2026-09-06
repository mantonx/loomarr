import type {
  FillerRightsGrantDTO,
  FillerRightsGrantInputBody,
  FillerRightsReviewDTO,
  RewindFillerClipInputBody,
} from "@loomarr/api";
import { getRecordFillerRightsGrantMockHandler, getRewindFillerClipMockHandler } from "@loomarr/api/msw";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { describe, expect, it } from "vitest";
import { server } from "@/test/msw/server";
import { RightsReview } from "./rights-review";

const MASTER = "a".repeat(64);
const POLICY = "b".repeat(64);
const GRANT = "c".repeat(64);

const review: FillerRightsReviewDTO = {
  sourceId: "archive:commercials",
  acquisitionId: "acq-17",
  sourceMasterSha256: MASTER,
  policySha256: POLICY,
  use: "filler_broadcast",
  canRecord: true,
};

const grant: FillerRightsGrantDTO = {
  sha256: GRANT,
  sourceId: review.sourceId,
  acquisitionId: review.acquisitionId,
  sourceMasterSha256: MASTER,
  policySha256: POLICY,
  use: review.use,
  status: "authorized",
  withdrawal: "clear",
  evidenceSha256: "d".repeat(64),
  actorId: "admin-1",
  effectiveAt: "2026-09-04T18:00:00Z",
  recordedAt: "2026-09-04T18:00:00Z",
};

const Wrapper = ({ children }: { children: ReactNode }) => (
  <QueryClientProvider
    client={new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })}
  >
    {children}
  </QueryClientProvider>
);

describe("RightsReview", () => {
  it("binds an append to server-owned context and fingerprints evidence locally", async () => {
    let recorded: FillerRightsGrantInputBody | undefined;
    let rewind: RewindFillerClipInputBody | undefined;
    server.use(
      getRecordFillerRightsGrantMockHandler(async ({ request }) => {
        recorded = (await request.json()) as FillerRightsGrantInputBody;
        return grant;
      }),
      getRewindFillerClipMockHandler(async ({ request }) => {
        rewind = (await request.json()) as RewindFillerClipInputBody;
      }),
    );

    render(<RightsReview clipHash={"e".repeat(64)} review={review} />, { wrapper: Wrapper });

    expect(screen.getByText(/archive:commercials · acquisition acq-17/i)).toBeInTheDocument();
    expect(screen.queryByText(MASTER)).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Review rights" }));
    await userEvent.upload(
      screen.getByLabelText("Private review file"),
      new File(["reviewed rights evidence"], "rights-note.txt", { type: "text/plain" }),
    );
    await screen.findByText(/rights-note.txt/);
    await userEvent.click(screen.getByRole("button", { name: "Record rights decision" }));

    await waitFor(() => expect(recorded).toBeDefined());
    expect(recorded).toMatchObject({
      sourceId: review.sourceId,
      acquisitionId: review.acquisitionId,
      sourceMasterSha256: MASTER,
      policySha256: POLICY,
      status: "authorized",
      withdrawal: "clear",
    });
    expect(recorded?.evidenceSha256).toMatch(/^[0-9a-f]{64}$/);
    expect(recorded?.supersedesSha256).toBeUndefined();

    await userEvent.click(await screen.findByRole("button", { name: "Re-run screening" }));
    await waitFor(() => expect(rewind).toEqual({ hash: "e".repeat(64), from: "screen" }));
  });

  it("shows exact context without offering a write when the registry is unavailable", () => {
    render(<RightsReview clipHash={"e".repeat(64)} review={{ ...review, canRecord: false }} />, {
      wrapper: Wrapper,
    });

    expect(screen.getByText(/registry is unavailable/i)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /review rights/i })).not.toBeInTheDocument();
  });

  it("compare-and-swaps a withdrawal against the exact current grant", async () => {
    let recorded: FillerRightsGrantInputBody | undefined;
    server.use(
      getRecordFillerRightsGrantMockHandler(async ({ request }) => {
        recorded = (await request.json()) as FillerRightsGrantInputBody;
        return { ...grant, status: "prohibited", withdrawal: "withdrawn", supersedesSha256: GRANT };
      }),
    );
    render(<RightsReview clipHash={"e".repeat(64)} review={{ ...review, currentGrant: grant }} />, {
      wrapper: Wrapper,
    });

    await userEvent.click(screen.getByRole("button", { name: "Replace rights decision" }));
    await userEvent.click(screen.getByLabelText("Decision"));
    await userEvent.click(screen.getByRole("option", { name: "Withdraw the current rights" }));
    await userEvent.upload(
      screen.getByLabelText("Private review file"),
      new File(["withdrawal evidence"], "withdrawal.txt", { type: "text/plain" }),
    );
    await screen.findByText(/withdrawal.txt/);
    await userEvent.click(screen.getByRole("button", { name: "Append replacement decision" }));

    await waitFor(() => expect(recorded).toBeDefined());
    expect(recorded).toMatchObject({
      status: "prohibited",
      withdrawal: "withdrawn",
      supersedesSha256: GRANT,
    });
    expect(recorded?.effectiveAt).toBe(recorded?.withdrawnAt);
  });
});
