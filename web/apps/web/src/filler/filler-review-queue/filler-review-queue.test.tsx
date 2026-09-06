import type { ClipDTO, FillerScreeningDTO } from "@loomarr/api";
import {
  getActOnFillerDecisionMockHandler,
  getFillerDecisionReviewsMockHandler,
  getGetFillerScreeningMockHandler,
  getListFillerMockHandler,
} from "@loomarr/api/msw";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it } from "vitest";
import { server } from "@/test/msw/server";
import { RouterHarness } from "@/test/story-utils";
import { FillerReviewQueue } from "./filler-review-queue";

const HASH = "abcdef0123456789".repeat(4);
const DIGEST = "1".repeat(64);

const review = {
  id: "decision-1",
  clipHash: HASH,
  applicationMode: "shadow" as const,
  createdAt: "2026-08-25T12:00:00Z",
  question: "Is this a soda commercial?",
  reasonCodes: ["brand_category_conflict"],
  evidenceRefs: ["transcript", "frame-2"],
  conflicts: [
    {
      claim: "product category",
      values: ["Mountain Dew", "unknown"],
      evidenceRefs: ["transcript", "frame-2"],
      resolved: false,
    },
  ],
};

const clip: ClipDTO = {
  hash: HASH,
  name: "Mountain Dew commercial",
  kind: "commercial",
  durationMs: 30_000,
  tagged: true,
  aiTagged: true,
  playCount: 0,
  playsCounted: true,
};

const screening: FillerScreeningDTO = {
  state: "available",
  clipHash: HASH,
  subjectSha256: "2".repeat(64),
  evidenceSha256: "3".repeat(64),
  outcome: "pass",
  assessedAt: "2026-09-04T21:00:00Z",
  axes: ["visual_safety", "spoken_safety", "written_safety", "rights", "playback_integrity"].map((axis) => ({
    axis: axis as FillerScreeningDTO["axes"][number]["axis"],
    outcome: "pass",
    reasonCode: "policy_clear",
    evidenceSha256: DIGEST,
  })),
  airworthiness: {
    schemaVersion: 1,
    contractVersion: "filler-airworthiness-decision-v1",
    subjectSha256: "2".repeat(64),
    profile: "all_ages",
    policyVersion: "filler-airworthiness-policy-v1",
    vocabularyVersion: "filler-airworthiness-vocabulary-v1",
    verdict: "pass",
    reasonCodes: ["airworthiness_evidence_satisfied"],
    observedFlags: [],
    heldAxes: [],
    triggers: [],
    evidenceSha256s: ["4".repeat(64), "5".repeat(64), "6".repeat(64)],
    authoritySha256: "7".repeat(64),
    decisionSha256: "8".repeat(64),
  },
};

const wrapper = ({ children }: { children: ReactNode }) => {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return (
    <QueryClientProvider client={client}>
      <RouterHarness content={children} initialPath="/filler/incoming" />
    </QueryClientProvider>
  );
};

beforeEach(() => {
  server.use(
    getListFillerMockHandler({ clips: [clip], total: 1 }),
    getGetFillerScreeningMockHandler(screening),
  );
});

const prepareForPositiveDecision = async () => {
  await userEvent.click(await screen.findByRole("button", { name: "Review evidence" }));
  await userEvent.click(await screen.findByRole("button", { name: "Play exact clip" }));
  fireEvent.playing(document.querySelector("video") as HTMLVideoElement);
  await userEvent.click(await screen.findByRole("button", { name: "Close player" }));
  await waitFor(() => expect(screen.getByRole("button", { name: "Record as filler" })).toBeEnabled());
};

describe("FillerReviewQueue", () => {
  it("renders one plain evidence-first question without operational state", async () => {
    server.use(getFillerDecisionReviewsMockHandler({ rows: [review], total: 1 }));
    render(<FillerReviewQueue />, { wrapper });

    expect(await screen.findByRole("heading", { name: "Is this a soda commercial?" })).toBeInTheDocument();
    expect(screen.getByText("Mountain Dew · unknown")).toBeInTheDocument();
    expect(screen.getByText("2 evidence sources")).toBeInTheDocument();
    expect(screen.queryByText(/provider|budget|retry/i)).not.toBeInTheDocument();
    expect(await screen.findByText("Mountain Dew commercial · 30s")).toBeInTheDocument();
    expect(screen.getByText("Shadow review")).toBeInTheDocument();
    expect(screen.getByText(/does not change the clip/i)).toBeInTheDocument();
    expect(screen.getByText(/neither files nor removes the clip/i)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Record as filler" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Correct answer" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Record as not filler" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Skip for now" })).toBeInTheDocument();
  });

  it("loads and presents the five independent screens only when evidence is opened", async () => {
    let screeningReads = 0;
    server.use(
      getFillerDecisionReviewsMockHandler({ rows: [review], total: 1 }),
      getGetFillerScreeningMockHandler(() => {
        screeningReads += 1;
        return screening;
      }),
    );
    render(<FillerReviewQueue />, { wrapper });

    await screen.findByText("Mountain Dew commercial · 30s");
    expect(screeningReads).toBe(0);
    await userEvent.click(screen.getByRole("button", { name: "Review evidence" }));

    expect(await screen.findByText("Visual safety")).toBeInTheDocument();
    expect(screen.getByText("Spoken safety")).toBeInTheDocument();
    expect(screen.getByText("Written safety")).toBeInTheDocument();
    expect(screen.getByText("Current-use rights")).toBeInTheDocument();
    expect(screen.getByText("Playback integrity")).toBeInTheDocument();
    expect(screen.getByRole("region", { name: "Audience airworthiness" })).toHaveTextContent(/all ages/i);
    expect(screeningReads).toBe(1);
  });

  it("resolves one bounded page and focuses only the selected question", async () => {
    const secondHash = "b".repeat(64);
    let catalogReads = 0;
    server.use(
      getFillerDecisionReviewsMockHandler({
        rows: [review, { ...review, id: "decision-2", clipHash: secondHash, question: "Is this a promo?" }],
        total: 2,
      }),
      getListFillerMockHandler(({ request }) => {
        catalogReads += 1;
        const hashes = new URL(request.url).searchParams.getAll("hashes");
        return {
          clips: hashes.map((hash, index) => ({ ...clip, hash, name: `Resolved clip ${index + 1}` })),
          total: hashes.length,
        };
      }),
    );
    render(<FillerReviewQueue />, { wrapper });

    expect(await screen.findByText("Resolved clip 1 · 30s")).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Is this a promo?" })).not.toBeInTheDocument();
    expect(screen.getByText("Resolved clip 2")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Review question 2: Is this a promo?" }));
    expect(await screen.findByText("Resolved clip 2 · 30s")).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Is this a soda commercial?" })).not.toBeInTheDocument();
    expect(catalogReads).toBe(1);
  });

  it("uses the server cursor to page through a large queue", async () => {
    const pageOne = Array.from({ length: 10 }, (_, index) => ({
      ...review,
      id: `decision-${index + 1}`,
      clipHash: (index + 1).toString(16).padStart(64, "0"),
      question: `Question ${index + 1}`,
      createdAt: `2026-08-${String(25 - index).padStart(2, "0")}T12:00:00Z`,
    }));
    const pageTwo = [
      {
        ...review,
        id: "decision-11",
        clipHash: "b".repeat(64),
        question: "Question 11",
        createdAt: "2026-08-15T12:00:00Z",
      },
      {
        ...review,
        id: "decision-12",
        clipHash: "c".repeat(64),
        question: "Question 12",
        createdAt: "2026-08-14T12:00:00Z",
      },
    ];
    const requests: URL[] = [];
    server.use(
      http.get("*/v1/filler/decisions/reviews", ({ request }) => {
        const url = new URL(request.url);
        requests.push(url);
        return HttpResponse.json({
          rows: url.searchParams.has("beforeId") ? pageTwo : pageOne,
          total: 12,
        });
      }),
      getListFillerMockHandler(({ request }) => {
        const hashes = new URL(request.url).searchParams.getAll("hashes");
        return { clips: hashes.map((hash) => ({ ...clip, hash })), total: hashes.length };
      }),
    );
    render(<FillerReviewQueue />, { wrapper });

    expect(await screen.findByText(/Page 1 of 2/)).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Next page" }));
    expect(await screen.findByText(/Page 2 of 2/)).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Question 11" })).toBeInTheDocument();
    expect(requests.at(-1)?.searchParams.get("limit")).toBe("10");
    expect(requests.at(-1)?.searchParams.get("beforeId")).toBe("decision-10");
    expect(requests.at(-1)?.searchParams.get("beforeAt")).toBe(pageOne.at(-1)?.createdAt);

    await userEvent.click(screen.getByRole("button", { name: "Previous page" }));
    expect(await screen.findByText(/Page 1 of 2/)).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Question 1" })).toBeInTheDocument();
  });

  it("requires exact playback before recording any semantic answer", async () => {
    server.use(getFillerDecisionReviewsMockHandler({ rows: [review], total: 1 }));
    render(<FillerReviewQueue />, { wrapper });

    expect(await screen.findByRole("button", { name: "Record as not filler" })).toBeDisabled();
    await userEvent.click(screen.getByRole("button", { name: "Correct answer" }));
    await userEvent.click(screen.getByLabelText("It is not filler"));
    await userEvent.type(screen.getByLabelText("Correction"), "This is programme content");
    expect(screen.getByRole("button", { name: "Save correction" })).toBeDisabled();
    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));

    await userEvent.click(screen.getByRole("button", { name: "Review evidence" }));
    await userEvent.click(screen.getByRole("button", { name: "Play exact clip" }));
    await userEvent.click(screen.getByRole("button", { name: "Close player" }));
    expect(screen.getByRole("button", { name: "Record as filler" })).toBeDisabled();

    await userEvent.click(screen.getByRole("button", { name: "Play exact clip" }));
    fireEvent.playing(document.querySelector("video") as HTMLVideoElement);
    await userEvent.click(screen.getByRole("button", { name: "Close player" }));
    expect(screen.getByRole("button", { name: "Record as not filler" })).toBeEnabled();
  });

  it("records skip for now without inventing an accept or reject answer", async () => {
    const bodies: unknown[] = [];
    server.use(
      getFillerDecisionReviewsMockHandler({ rows: [review], total: 1 }),
      getActOnFillerDecisionMockHandler(async ({ request }) => {
        bodies.push(await request.json());
        return { id: "action-skip" };
      }),
    );
    render(<FillerReviewQueue />, { wrapper });

    await userEvent.click(await screen.findByRole("button", { name: "Skip for now" }));

    await waitFor(() =>
      expect(bodies).toEqual([{ actionId: expect.any(String), kind: "abandon", reason: "skip for now" }]),
    );
    expect(await screen.findByText("You're caught up for now")).toBeInTheDocument();
    expect(screen.getByText(/did not treat them as accepted or rejected/i)).toBeInTheDocument();
  });

  it("keeps the caught-up confirmation when invalidation refetches an empty queue", async () => {
    const actions: unknown[] = [];
    let resolvePostSkipRefetch!: () => void;
    const postSkipRefetch = new Promise<void>((resolve) => {
      resolvePostSkipRefetch = resolve;
    });
    let reviewRequests = 0;
    server.use(
      http.get("*/v1/filler/decisions/reviews", () => {
        reviewRequests += 1;
        if (reviewRequests === 1) return HttpResponse.json({ rows: [review], total: 1 });
        resolvePostSkipRefetch();
        return HttpResponse.json({ rows: [], total: 0 });
      }),
      getActOnFillerDecisionMockHandler(async ({ request }) => {
        actions.push(await request.json());
        return { id: "action-skip" };
      }),
    );
    render(<FillerReviewQueue />, { wrapper });

    const skip = await screen.findByRole("button", { name: "Skip for now" });
    await userEvent.click(skip);

    await waitFor(() => expect(actions).toHaveLength(1));
    await postSkipRefetch;
    await waitFor(() => expect(screen.getByText("You're caught up for now")).toBeInTheDocument());
    expect(screen.queryByText("Nothing needs your attention")).not.toBeInTheDocument();
    expect(screen.getByText(/did not treat them as accepted or rejected/i)).toBeInTheDocument();
  });

  it("records a correction as a distinct append-only action", async () => {
    const bodies: unknown[] = [];
    server.use(
      getFillerDecisionReviewsMockHandler({ rows: [review], total: 1 }),
      getActOnFillerDecisionMockHandler(async ({ request }) => {
        bodies.push(await request.json());
        return { id: "action-1" };
      }),
    );
    render(<FillerReviewQueue />, { wrapper });

    await prepareForPositiveDecision();
    await userEvent.click(await screen.findByRole("button", { name: "Correct answer" }));
    expect(screen.getByLabelText("Correction")).toHaveFocus();
    await userEvent.click(screen.getByLabelText("It is filler"));
    await userEvent.type(screen.getByLabelText("Correction"), "This is a soda commercial");
    await userEvent.click(screen.getByRole("button", { name: "Save correction" }));

    await waitFor(() => expect(bodies).toHaveLength(1));
    expect(bodies[0]).toMatchObject({
      kind: "correct",
      correctedVerdict: "admit",
      answer: "This is a soda commercial",
    });
  });

  it("records a shadow filler answer without claiming that it filed the clip", async () => {
    const bodies: Record<string, unknown>[] = [];
    server.use(
      getFillerDecisionReviewsMockHandler({
        rows: [{ ...review, question: "Which product is this commercial advertising?" }],
        total: 1,
      }),
      getActOnFillerDecisionMockHandler(async ({ request }) => {
        bodies.push((await request.json()) as Record<string, unknown>);
        return { id: "action-admit" };
      }),
    );
    render(<FillerReviewQueue />, { wrapper });

    await prepareForPositiveDecision();
    expect(screen.queryByRole("button", { name: "Confirm for library" })).not.toBeInTheDocument();
    await userEvent.click(await screen.findByRole("button", { name: "Record as filler" }));

    await waitFor(() => expect(bodies).toHaveLength(1));
    expect(bodies[0]).toMatchObject({ actionId: expect.any(String), kind: "admit" });
    expect(bodies[0]).not.toHaveProperty("answer");
  });

  it("renders a healthy zero-work state", async () => {
    server.use(getFillerDecisionReviewsMockHandler({ rows: [], total: 0 }));
    render(<FillerReviewQueue />, { wrapper });
    expect(await screen.findByText("Nothing needs your attention")).toBeInTheDocument();
  });

  it("shows all five screens and keeps positive confirmation closed when evidence is unavailable", async () => {
    server.use(
      getFillerDecisionReviewsMockHandler({ rows: [review], total: 1 }),
      getGetFillerScreeningMockHandler({
        state: "unavailable",
        reasonCode: "screening_evidence_drift",
        clipHash: HASH,
        subjectSha256: "2".repeat(64),
        evidenceSha256: "3".repeat(64),
        axes: [],
      }),
    );
    render(<FillerReviewQueue />, { wrapper });

    await userEvent.click(await screen.findByRole("button", { name: "Review evidence" }));
    expect(await screen.findByText("Screening evidence unavailable")).toBeInTheDocument();
    expect(screen.getByText(/screening evidence drift/i)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Record as filler" })).toBeDisabled();
  });

  it("fails closed if an applied review arrives before terminal catalog effects exist", async () => {
    server.use(
      getFillerDecisionReviewsMockHandler({ rows: [{ ...review, applicationMode: "applied" }], total: 1 }),
    );
    render(<FillerReviewQueue />, { wrapper });

    expect(await screen.findByText("Applied review unavailable")).toBeInTheDocument();
    expect(screen.getByText(/terminal catalog effect/i)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Confirm for library" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Correct" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Reject" })).toBeDisabled();
  });
});
