import type { PullDTO } from "@loomarr/api";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { PullCard } from "./pull-card";

const pull = (over: Partial<PullDTO> = {}): PullDTO => ({
  id: "pull_1",
  title: "Top up the 1990s",
  reason: "Saturday Mornings falls back to bumpers.",
  proposedBy: "ada",
  status: "pending",
  estimateClips: 52,
  candidateCount: 2,
  intent: {
    version: "filler-acquisition-intent/v1",
    rights: "prefer_declared",
    count: 2,
    catalogReason: "Saturday Mornings falls back to bumpers.",
  },
  rejected: [],
  sources: [],
  createdAt: "2026-08-01T12:00:00Z",
  plan: [
    {
      sourceId: "classic",
      candidateId: "candidate_classic",
      tag: "archive",
      name: "Classic TV commercials",
      why: "Era match",
      estimateClips: 40,
      dropped: false,
    },
    {
      sourceId: "psa",
      candidateId: "candidate_psa",
      tag: "archive",
      name: "Public service",
      why: "Break variety",
      estimateClips: 12,
      dropped: false,
    },
  ],
  ...over,
});

describe("PullCard", () => {
  // ⚠ The card's whole job is to make "nothing is downloading yet" legible. An operator who
  // thinks the fetch already started has no reason to read the plan.
  it("says nothing downloads until the pull is approved", () => {
    render(<PullCard pull={pull()} onApprove={() => {}} onDismiss={() => {}} />);

    expect(screen.getByText(/nothing downloads until you approve/i)).toBeInTheDocument();
  });

  // "Approve this" without a reason is a button, not a decision.
  it("shows why the pull was proposed", () => {
    render(<PullCard pull={pull()} onApprove={() => {}} onDismiss={() => {}} />);

    expect(screen.getByText("Saturday Mornings falls back to bumpers.")).toBeInTheDocument();
    expect(screen.getByText("Top up the 1990s")).toBeInTheDocument();
  });

  it("keeps excluded candidate evidence inspectable", async () => {
    render(
      <PullCard
        pull={pull({
          rejected: [
            {
              candidateId: "candidate_low",
              sourceId: "classic",
              provider: "archive",
              remoteId: "low",
              name: "Low-quality reel",
              disposition: "quality_below_floor",
              detail: "remote height 240p is below the floor",
            },
          ],
        })}
        onApprove={() => {}}
        onDismiss={() => {}}
      />,
    );

    await userEvent.click(screen.getByText(/1 other candidate considered and excluded/i));
    expect(screen.getByText("Low-quality reel")).toBeInTheDocument();
    expect(screen.getByText(/quality below floor: remote height 240p/i)).toBeInTheDocument();
  });

  it("keeps skipped source decisions inspectable", async () => {
    render(
      <PullCard
        pull={pull({
          sources: [
            {
              sourceId: "disabled-local",
              provider: "archive",
              label: "Local ads",
              disposition: "disabled",
              candidateCount: 0,
              detail: "registered source is disabled",
            },
          ],
        })}
        onApprove={() => {}}
        onDismiss={() => {}}
      />,
    );

    await userEvent.click(screen.getByText(/1 registered source not searched/i));
    expect(screen.getByText("Local ads")).toBeInTheDocument();
    expect(screen.getByText(/disabled: registered source is disabled/i)).toBeInTheDocument();
  });

  // ⚠ Estimates are rendered AS estimates. What a source yields depends on what is still there
  // and what deduplicates, so an exact-looking number becomes a bug report about a forecast.
  it("renders per-source counts as approximate", () => {
    render(<PullCard pull={pull()} onApprove={() => {}} onDismiss={() => {}} />);

    expect(screen.getByText("~40 clips")).toBeInTheDocument();
  });

  // ⚠ Dropping is held locally and sent with the decision. A per-click PATCH would make
  // "half-approved" a state the gate has to reason about.
  it("sends dropped rows with the approval, in one act", async () => {
    const onApprove = vi.fn();
    render(<PullCard pull={pull()} onApprove={onApprove} onDismiss={() => {}} />);

    await userEvent.click(screen.getByRole("button", { name: /leave public service out/i }));
    expect(onApprove).not.toHaveBeenCalled();

    await userEvent.type(screen.getByLabelText("Notes for this pull"), "reviewed by programming");
    await userEvent.click(screen.getByRole("button", { name: "Approve pull" }));

    expect(onApprove).toHaveBeenCalledWith({ dropCandidateIds: ["candidate_psa"], note: "reviewed by programming" });
  });

  it("presents notes as annotations without changing the included candidates", async () => {
    const onApprove = vi.fn();
    render(<PullCard pull={pull()} onApprove={onApprove} onDismiss={() => {}} />);

    expect(screen.getByPlaceholderText("Optional annotation for your records")).toBeInTheDocument();
    expect(screen.getByText("Optional annotation for your records. This does not change what is downloaded.")).toBeInTheDocument();

    await userEvent.type(screen.getByLabelText("Notes for this pull"), "keep the selected mix");
    await userEvent.click(screen.getByRole("button", { name: "Approve pull" }));

    expect(onApprove).toHaveBeenCalledWith({ dropCandidateIds: [], note: "keep the selected mix" });
  });

  it("lets a dropped row be put back before committing", async () => {
    const onApprove = vi.fn();
    render(<PullCard pull={pull()} onApprove={onApprove} onDismiss={() => {}} />);

    await userEvent.click(screen.getByRole("button", { name: /leave public service out/i }));
    await userEvent.click(screen.getByRole("button", { name: /put public service back/i }));
    await userEvent.click(screen.getByRole("button", { name: "Approve pull" }));

    expect(onApprove).toHaveBeenCalledWith({ dropCandidateIds: [], note: "" });
  });

  // The server refuses an all-dropped approval rather than recording one that fetched nothing.
  // Saying so before the round trip is kinder than a 409.
  it("refuses to approve once every source is left out", async () => {
    render(<PullCard pull={pull()} onApprove={() => {}} onDismiss={() => {}} />);

    await userEvent.click(screen.getByRole("button", { name: /leave classic tv commercials out/i }));
    await userEvent.click(screen.getByRole("button", { name: /leave public service out/i }));

    expect(screen.getByRole("button", { name: "Approve pull" })).toBeDisabled();
    expect(screen.getByText(/nothing to fetch. dismiss it instead/i)).toBeInTheDocument();
  });

  it("dismisses without approving", async () => {
    const onApprove = vi.fn();
    const onDismiss = vi.fn();
    render(<PullCard pull={pull()} onApprove={onApprove} onDismiss={onDismiss} />);

    await userEvent.click(screen.getByRole("button", { name: "Not now" }));

    expect(onDismiss).toHaveBeenCalledOnce();
    expect(onApprove).not.toHaveBeenCalled();
  });

  it("says a decision is in flight rather than looking inert", () => {
    render(<PullCard pull={pull()} onApprove={() => {}} onDismiss={() => {}} deciding />);

    expect(screen.getByRole("button", { name: "Starting…" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Not now" })).toBeDisabled();
  });

  // huma types every Go slice as nullable, so the generated DTO says `plan: ... | null` even
  // though the handler always sends []. Rendering must survive the null.
  it("survives a null plan", () => {
    render(
      <PullCard
        pull={{ ...pull(), plan: null as unknown as PullDTO["plan"] }}
        onApprove={() => {}}
        onDismiss={() => {}}
      />,
    );

    expect(screen.getByRole("button", { name: "Approve pull" })).toBeDisabled();
  });
});
