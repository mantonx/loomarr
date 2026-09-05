import type { ApproveOutputBody, MeBody, ProposalDTO } from "@loomarr/api";
import {
  getApproveProposalMockHandler,
  getGetProposalJobMockHandler,
  getMeMockHandler,
  getSubmitProposalMockHandler,
} from "@loomarr/api/msw";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { me } from "@/test/fixtures/users";
import { server } from "@/test/msw/server";
import { RouterHarness } from "@/test/story-utils";
import type { SuggestionRun } from "../use-suggestion-run/use-suggestion-run.type";
import { ChannelSuggestPanel } from "./channel-suggest-panel";

// The failed-run case is driven by a hook override: the failure arrives as an SSE `failed`
// phase, which the panel test's isolated (provider-less) render can't emit. useSuggestionRun's
// own test proves the phase→failed mapping; this proves the panel RENDERS that failure instead
// of silently dropping back to the describe form (the reported bug). Default: delegate to the
// REAL hook so every other test in this file keeps exercising the true flow through MSW.
let runOverride: SuggestionRun | undefined;
vi.mock("../use-suggestion-run", async (importActual) => {
  const actual = await importActual<typeof import("../use-suggestion-run")>();
  const useReal = actual.useSuggestionRun;
  // biome-ignore lint/correctness/useHookAtTopLevel: mock replacement hook delegating to the real one, not a conditional component hook.
  return { useSuggestionRun: () => runOverride ?? useReal() };
});

const failedRun = (over: Partial<SuggestionRun> = {}): SuggestionRun => ({
  phase: "failed",
  round: undefined,
  proposal: undefined,
  failure: { code: "generation_failed", message: "Loomarr couldn't generate this channel. Try again later." },
  actions: ["retry", "check_ai"],
  isRunning: false,
  failed: true,
  error: undefined,
  start: vi.fn(),
  retry: vi.fn(),
  reset: vi.fn(),
  ...over,
});

// The panel reuses the whole Suggest flow (useSuggestionRun → GenerationProgress →
// ProposalReview), so its test mirrors suggest-workspace's harness: an admin auth/me, a POST
// /v1/proposals that returns a jobId, a /v1/proposals list that yields a submitted proposal
// matched on that jobId, and a stubbed EventSource (jsdom has none — the phases ride SSE, the
// proposal rides the list). The panel needs a router (it navigates on approve) + a query
// client + the events provider is absent in isolation (the listener is then a no-op, exactly
// as suggest-workspace notes).
// ⚠ `local` is REQUIRED on MeBody and this fixture omitted it.
const ADMIN: MeBody = me();
const MEMBER: MeBody = me({ role: "member" });

const PROPOSAL: ProposalDTO = {
  id: "p-1",
  jobId: "job-1",
  status: "submitted",
  proposal: {
    intent: { description: "80s teen comedies" },
    lineup: [
      { mediaType: "movie", tmdbId: 9377, name: "Ferris Bueller's Day Off", year: 1986, inLibrary: true },
    ],
    acquisitions: [],
    // ⚠ `alternates` and `scores` are REQUIRED on Proposal and this fixture had neither. The
    // panel renders ProposalReview, which reads `scores` for the fit summary — so the review was
    // being exercised against a proposal the server could not have produced.
    alternates: [],
    scores: { themeFit: 0.9, availabilityRatio: 1, eraBalance: 0.7, overall: 0.85 },
    rationale: "Grounded against your library.",
    trace: { version: 1, surfacedTotal: 0, recordedTotal: 0, truncated: false, candidates: [] },
  },
};

// ⚠ `u.includes("/v1/proposals") || u.includes("/v1/proposals")` — the SAME condition twice, the
// second branch unreachable. It is the second duplicated-`/v1/proposals` branch this migration has
// found (`test/reachability` had the other), and neither could have been noticed: dead code in a
// stub produces no symptom at all.
//
// ⚠ `u.includes("/approve")` also matches `POST /v1/proposals/approve`, the BULK route. The
// member-gate test below asserts NO approve call fires — an assertion whose whole value depends on
// naming the right endpoint.
const stubSuggest = (
  opts: { proposals?: ProposalDTO[]; me?: MeBody; approveBody?: ApproveOutputBody } = {},
) => {
  const approvals: string[] = [];
  const submissions: unknown[] = [];

  server.use(
    getMeMockHandler(opts.me ?? ADMIN),
    // Approve — returns the created channel's id (what the panel navigates to).
    getApproveProposalMockHandler(({ params }) => {
      approvals.push(String(params.id));
      return opts.approveBody ?? { channelId: "ch_new123", enqueued: 0, status: "approved" };
    }),
    getSubmitProposalMockHandler(async ({ request }) => {
      submissions.push(await request.json());
      return { jobId: "job-1" };
    }),
    getGetProposalJobMockHandler(() => {
      const proposal = opts.proposals?.[0];
      return {
        version: 1,
        jobId: "job-1",
        milestone: proposal ? "awaiting_approval" : "generating",
        intent: { description: "80s teen comedies" },
        attempts: [
          {
            version: 1,
            number: 1,
            status: proposal ? "succeeded" : "running",
            startedAt: "2026-08-22T12:00:00Z",
          },
        ],
        proposal: proposal
          ? { id: proposal.id, status: proposal.status, proposal: proposal.proposal }
          : undefined,
        actions: proposal ? ["review"] : ["wait"],
        createdAt: "2026-08-22T12:00:00Z",
        updatedAt: "2026-08-22T12:00:00Z",
      };
    }),
  );

  return { approvals, submissions };
};

const renderPanel = (onCreated: (id: string) => void) => {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <RouterHarness
      content={
        <QueryClientProvider client={client}>
          <ChannelSuggestPanel onCreated={onCreated} />
        </QueryClientProvider>
      }
    />,
  );
};

describe("ChannelSuggestPanel", () => {
  beforeEach(() => {
    runOverride = undefined; // default every test back to the real hook
    window.sessionStorage.clear();
  });

  it("submits the typed intent to start a run", async () => {
    const user = userEvent.setup();
    const { submissions } = stubSuggest();
    renderPanel(() => {});

    await user.type(await screen.findByLabelText("Channel intent"), "80s teen comedies");
    await user.click(screen.getByRole("button", { name: /suggest a lineup/i }));

    await waitFor(() => {
      expect(submissions).toHaveLength(1);
      expect(submissions[0]).toMatchObject({ description: "80s teen comedies" });
    });
  });

  it("preserves the intent and links AI settings when submission is not configured", async () => {
    const user = userEvent.setup();
    stubSuggest();
    server.use(
      http.post("*/v1/proposals", () =>
        HttpResponse.json(
          {
            type: "feature_not_configured",
            title: "AI isn't set up",
            detail: "Connect an AI provider in Settings → AI to build channels from a sentence.",
          },
          { status: 501 },
        ),
      ),
    );
    renderPanel(() => {});

    const intent = await screen.findByLabelText("Channel intent");
    await user.type(intent, "Saturday morning cartoons");
    await user.click(screen.getByRole("button", { name: /suggest a lineup/i }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      /configured AI provider.*tool-capable lineup model/i,
    );
    expect(screen.getByRole("link", { name: /open ai settings/i })).toHaveAttribute("href", "/settings/ai");
    expect(intent).toHaveValue("Saturday morning cartoons");
  });

  // Moved here when `/suggest` folded into the Guide header (§12) and its route-level suite
  // went away. Worth keeping as its own case: `runtimeTargetMin` was in the shared schema and
  // consumed by the scorer for a long time with NO way to set it, so this pins that the
  // constraints disclosure actually reaches the wire — under the wire's field names.
  it("submits the constraints behind the disclosure, under the wire's field names", async () => {
    const user = userEvent.setup();
    const { submissions } = stubSuggest();
    renderPanel(() => {});

    await user.type(await screen.findByLabelText("Channel intent"), "90s action movies");
    await user.click(screen.getByRole("button", { name: /add constraints/i }));
    await user.type(screen.getByLabelText(/target runtime/i), "180");
    await user.click(screen.getByRole("button", { name: /suggest a lineup/i }));

    await waitFor(() => {
      expect(submissions).toHaveLength(1);
      expect(submissions[0]).toMatchObject({
        description: "90s action movies",
        runtimeTargetMin: 180,
      });
    });
  });

  it("shows the grounded proposal inline once the run produces one", async () => {
    const user = userEvent.setup();
    stubSuggest({ proposals: [PROPOSAL] });
    renderPanel(() => {});

    await user.type(await screen.findByLabelText("Channel intent"), "80s teen comedies");
    await user.click(screen.getByRole("button", { name: /suggest a lineup/i }));

    // The reused ProposalReview renders the lineup — no navigation away from the panel.
    expect(await screen.findByText("Ferris Bueller's Day Off")).toBeInTheDocument();
  });

  it("explains an auto-approved result without offering the misleading Start over action", async () => {
    const user = userEvent.setup();
    const { approvals } = stubSuggest({ proposals: [{ ...PROPOSAL, status: "approved" }] });
    const onCreated = vi.fn();
    renderPanel(onCreated);

    await user.type(await screen.findByLabelText("Channel intent"), "80s teen comedies");
    await user.click(screen.getByRole("button", { name: /suggest a lineup/i }));

    expect(await screen.findByText("Ferris Bueller's Day Off")).toBeInTheDocument();
    expect(screen.getByText(/automatically approved/i)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /start over/i })).not.toBeInTheDocument();
    expect(approvals).toEqual([]);
    expect(onCreated).not.toHaveBeenCalled();

    await user.click(screen.getByRole("button", { name: /create another/i }));

    expect(await screen.findByLabelText("Channel intent")).toHaveValue("");
    expect(approvals).toEqual([]);
    expect(onCreated).not.toHaveBeenCalled();
  });

  it("approving hands the new channel id to onCreated (the list navigates to it)", async () => {
    const user = userEvent.setup();
    stubSuggest({
      proposals: [PROPOSAL],
      approveBody: { channelId: "ch_new123", enqueued: 0, status: "approved" },
    });
    const onCreated = vi.fn();
    renderPanel(onCreated);

    await user.type(await screen.findByLabelText("Channel intent"), "80s teen comedies");
    await user.click(screen.getByRole("button", { name: /suggest a lineup/i }));
    await user.click(await screen.findByRole("button", { name: /approve/i }));

    await waitFor(() => expect(onCreated).toHaveBeenCalledWith("ch_new123"));
  });

  it("a member's approve is inert — no approve call fires (approval is admin-only, §7)", async () => {
    const user = userEvent.setup();
    // ProposalReview renders the Approve button off the proposal STATUS (same as /suggest);
    // the gate is that a member's onApprove is undefined, so clicking it does nothing — and
    // the server would 403 anyway. Assert the panel never fires the approve POST for a member.
    const { approvals } = stubSuggest({ proposals: [PROPOSAL], me: MEMBER });
    const onCreated = vi.fn();
    renderPanel(onCreated);

    await user.type(await screen.findByLabelText("Channel intent"), "80s teen comedies");
    await user.click(screen.getByRole("button", { name: /suggest a lineup/i }));
    await user.click(await screen.findByRole("button", { name: /approve/i }));

    // No approve POST, no navigation — the control is wired to nothing for a member.
    // ⚠ `approvals` is fed only by `POST /v1/proposals/:id/approve` — the per-proposal route the
    // panel would call. The old `includes("/approve")` would also have matched the BULK route.
    expect(approvals).toEqual([]);
    expect(onCreated).not.toHaveBeenCalled();
  });

  // The reported bug: describe a channel, the job fails (e.g. no AI provider), and the panel
  // silently dropped back to an empty describe form — no error, no way to tell what happened.
  it("surfaces a failed run with a message and a retry instead of a silent empty form", async () => {
    const retry = vi.fn();
    runOverride = failedRun({ retry });
    stubSuggest();
    renderPanel(() => {});

    // The failure is shown (GenerationProgress' failed step is an alert), with guidance…
    expect(await screen.findByRole("alert")).toBeInTheDocument();
    expect(screen.getByText(/couldn't generate this channel/i)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /check ai settings/i })).toHaveAttribute("href", "/settings/ai");
    // …and the describe form is NOT rendered underneath it (the silent-drop bug).
    expect(screen.queryByLabelText("Channel intent")).not.toBeInTheDocument();

    // Try again re-submits the preserved Intent through a fresh caller-owned Job.
    await userEvent.click(screen.getByRole("button", { name: /try again/i }));
    expect(retry).toHaveBeenCalled();
  });
});
