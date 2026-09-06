import type { IncomingClipDTO, IncomingReelDTO, IncomingRejectDTO } from "@loomarr/api";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactElement } from "react";
import { describe, expect, it, vi } from "vitest";
import { RouterHarness } from "@/test/story-utils";
import { IncomingPanel } from "./incoming-panel";

// ⚠ Two render helpers, and the split is deliberate rather than untidy.
//
// Only the REEL rows contain a TanStack `Link`, which needs a RouterProvider. The harness that
// supplies one mounts ASYNC, so every query in a test using it must be findBy* — a synchronous
// getByText runs before the router has rendered and fails with "unable to find", which reads as
// a component bug and is not one (coverage-meter's tests record the same trap, and this file's
// first draft walked straight into it).
//
// Ask-only tests contain no Link, so they render plainly and can assert synchronously. Wrapping
// them in the harness too would make every assertion in the file async for no reason.
const renderAsks = (ui: ReactElement) => render(ui);
const renderReels = (ui: ReactElement) => render(<RouterHarness content={ui} initialPath="/filler" />);

// `d` marks a fixture as one the MACHINE has handed over — the row that carries decision controls.
// ⚠ Explicit on every fixture rather than defaulted, because `needsDecision` is exactly what the
// two-list version got wrong: a clip is on one end of the belt or the other, never assumed.
const d = (c: IncomingClipDTO): IncomingClipDTO => ({ ...c, needsDecision: true });

const guessed: IncomingClipDTO = {
  path: "1988/toys.mp4",
  hash: "hash-guessed",
  name: "toys.mp4",
  from: "archive",
  durationMs: 30_000,
  kind: "commercial",
  audience: "kids",
  category: "toys",
  suggestedEra: 1988,
  reason: "The year isn't written anywhere in this clip's name or description, so Loomarr guessed it.",
};

const untagged: IncomingClipDTO = {
  path: "mystery.mp4",
  hash: "hash-untagged",
  name: "mystery.mp4",
  durationMs: 25_000,
  kind: "commercial",
  reason: "Loomarr couldn't work out what this is, so it will only match broadly.",
};

const reel: IncomingReelDTO = {
  proposalId: "sp_1",
  clipHash: "a3f9000000000000000000000000000000000000000000000000000000000001",
  clipName: "1987 Saturday morning block",
  segments: 12,
  needsAttention: 3,
  createdAt: "2026-08-01T12:00:00Z",
};

describe("IncomingPanel", () => {
  // ⚠ The two asks are different QUESTIONS and get different affordances. A guessed era has a
  // proposed answer to confirm; an untagged clip has nothing to confirm, so offering "Looks
  // right" there would ask an operator to approve something they were never shown.
  it("offers confirm only for a clip that carries a guess", () => {
    renderAsks(
      <IncomingPanel
        clips={[d(guessed), d(untagged)]}
        reels={[]}
        onConfirmEra={() => {}}
        onEditTags={() => {}}
      />,
    );

    expect(screen.getAllByRole("button", { name: "Looks right" })).toHaveLength(1);
    expect(screen.getByRole("button", { name: "Not right" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Add tags" })).toBeInTheDocument();
  });

  it("shows the guess as a guess, distinct from a confirmed tag", () => {
    renderAsks(<IncomingPanel clips={[d(guessed)]} reels={[]} />);

    expect(screen.getByText("guessed 1988")).toBeInTheDocument();
    expect(screen.getByText("kids")).toBeInTheDocument();
  });

  // ⚠ There is no confidence bar, and there must not be one until something measures it. The
  // reason the server derived is what an operator gets instead.
  it("explains why each clip is waiting, and shows no confidence score", () => {
    const { container } = renderAsks(<IncomingPanel clips={[d(guessed), d(untagged)]} reels={[]} />);

    expect(screen.getByText(guessed.reason)).toBeInTheDocument();
    expect(screen.getByText(untagged.reason)).toBeInTheDocument();
    expect(container.textContent).not.toMatch(/\d+%/);
  });

  it("names the source so a bad one is identifiable across a long queue", () => {
    renderAsks(<IncomingPanel clips={[d(guessed)]} reels={[]} />);

    expect(screen.getByText("from archive")).toBeInTheDocument();
  });

  it("reports the full queue while rendering only the bounded page", () => {
    renderAsks(<IncomingPanel clips={[d(guessed)]} clipsTotal={137} decisionsTotal={120} reels={[]} />);

    expect(screen.getByText("120 clips need a decision")).toBeInTheDocument();
    expect(screen.getByText("17 more clips still being prepared, further down.")).toBeInTheDocument();
    expect(screen.getByText("Showing the first 1 of 137 incoming clips.")).toBeInTheDocument();
  });

  it("passes the whole clip to the handlers, not just its path", async () => {
    const onConfirmEra = vi.fn();
    renderAsks(<IncomingPanel clips={[d(guessed)]} reels={[]} onConfirmEra={onConfirmEra} />);

    await userEvent.click(screen.getByRole("button", { name: "Looks right" }));

    // The caller needs the whole ask (audience included) to build a safe PATCH: the server
    // writes era and audience unconditionally, so a confirm carrying only the era would wipe
    // audience. `category` rides along on the DTO too, but it's a derived shadow — the actual
    // PATCH (built in `incoming-tab.tsx`) never sends it.
    expect(onConfirmEra).toHaveBeenCalledWith(d(guessed));
  });

  // One row disables, not the whole list — a page that greys out entirely while a single
  // confirm lands reads as having frozen.
  it("disables only the row being written", () => {
    renderAsks(
      <IncomingPanel
        clips={[d(guessed), d(untagged)]}
        reels={[]}
        busyPath={guessed.path}
        onEditTags={() => {}}
      />,
    );

    expect(screen.getByRole("button", { name: "Not right" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Add tags" })).toBeEnabled();
  });

  it("says how much work a compilation is before it is opened", async () => {
    renderReels(<IncomingPanel clips={[]} reels={[reel]} />);

    expect(await screen.findByText(reel.clipName)).toBeInTheDocument();
    expect(screen.getByText("12 clips found · 3 need a look")).toBeInTheDocument();
  });

  it("links a compilation to its review route", async () => {
    renderReels(<IncomingPanel clips={[]} reels={[reel]} />);

    const link = await screen.findByRole("link", { name: "Review cuts" });
    expect(link).toHaveAttribute("href", "/filler/splits/sp_1");
  });

  it("says nothing needs you when both halves are empty", () => {
    renderAsks(<IncomingPanel clips={[]} reels={[]} />);

    expect(screen.getByText("Nothing needs you")).toBeInTheDocument();
  });
});

// --- V38: confidence + the filing decisions ---

describe("IncomingPanel confidence and filing", () => {
  const ask = (over: Partial<IncomingClipDTO> = {}): IncomingClipDTO => ({
    path: "a.mp4",
    hash: "hash-a",
    name: "Toy ad",
    durationMs: 30_000,
    kind: "commercial",
    reason: "Loomarr couldn't work out what this is, so it will only match broadly.",
    ...over,
  });

  // ⚠ The bar is REAL now (V38) — the "no confidence bar" rule was retired when the tagger
  // started measuring one. The number is grounding-capped, never the model's self-report.
  it("renders the score, and says nothing when there is none", () => {
    const { rerender } = render(<IncomingPanel clips={[d(ask({ confidence: 45 }))]} reels={[]} />);
    expect(screen.getByLabelText(/confidence 45 out of 100/i)).toBeInTheDocument();

    // ⚠ 0 means NEVER SCORED, not "no confidence". A 0-width bar would state something false
    // about a clip the tagger simply has not reached.
    rerender(<IncomingPanel clips={[d(ask({ confidence: 0 }))]} reels={[]} />);
    expect(screen.queryByLabelText(/confidence/i)).not.toBeInTheDocument();
  });

  // ⚠ "File all as suggested" commits each clip's OWN era. It is offered only when something
  // HAS a suggestion — otherwise the label promises something the action would not do.
  it("offers File all as suggested only when a clip carries a guess", async () => {
    const onFileAllAsSuggested = vi.fn();
    const { rerender } = render(
      <IncomingPanel clips={[d(ask())]} reels={[]} onFileAllAsSuggested={onFileAllAsSuggested} />,
    );
    expect(screen.queryByRole("button", { name: /file all as suggested/i })).not.toBeInTheDocument();

    rerender(
      <IncomingPanel
        clips={[d(ask({ suggestedEra: 1985 }))]}
        reels={[]}
        onFileAllAsSuggested={onFileAllAsSuggested}
      />,
    );
    await userEvent.click(screen.getByRole("button", { name: /file all as suggested/i }));
    expect(onFileAllAsSuggested).toHaveBeenCalledOnce();
  });

  // ⚠ THE audit half. Auto-filing is on by default, so an operator must be able to see what was
  // filed without them — and it renders even when nothing is waiting, because that is exactly
  // the install where "nothing needs you" would otherwise be the whole story.
  it("shows what was filed without asking, and offers the undo", async () => {
    const onSendBack = vi.fn();
    const filed = ask({ path: "auto.mp4", name: "Auto ad", confidence: 88, autoFiled: true });
    render(<IncomingPanel clips={[]} reels={[]} recentlyFiled={[filed]} onSendBack={onSendBack} />);

    expect(screen.getByText(/filed 1 clip without asking/i)).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: /send it back/i }));
    expect(onSendBack).toHaveBeenCalledWith(filed);
  });
});

// The pipeline half (§10 V51b/V51e) — what the machine is still working on.
describe("IncomingPanel — being prepared", () => {
  const LADDER = [
    "probe",
    "transcode",
    "split",
    "screen",
    "language",
    "transcribe",
    "tag",
    "vision",
    "admission",
    "score",
  ];

  // ⚠ No `needsDecision`: the machine still owns these. The pipeline block is NESTED on the clip
  // rather than living in a parallel array — which is what stopped the same clip appearing twice.
  const row = (over: Partial<IncomingClipDTO> = {}): IncomingClipDTO => ({
    hash: "hash-cola",
    path: "cola.mp4",
    name: "Coca-Cola 1985",
    kind: "commercial",
    durationMs: 31_000,
    reason: "Loomarr is still working on this one.",
    pipeline: {
      stage: "tag",
      status: "running",
      lifecycle: "in_progress",
      progress: -1,
      stages: [{ stage: "probe", status: "done", at: "2026-08-08T10:00:00Z" }],
      updatedAt: "2026-08-08T10:01:00Z",
    },
    ...over,
  });

  // ⚠ The heading names the WORK, not the list length. With nothing handed over, the belt says
  // what it is doing — and does NOT say "nothing needs you", because that empty state is about an
  // empty belt. Saying it above 85 moving rows is what the two-list version did, and it is the
  // sentence that made the page contradict itself.
  it("says it is preparing, and does not claim the queue is empty", () => {
    renderAsks(<IncomingPanel clips={[row()]} reels={[]} stageOrder={LADDER} />);

    expect(screen.getByRole("heading", { name: /preparing 1 clip/i })).toBeInTheDocument();
    expect(screen.queryByText("Nothing needs you")).not.toBeInTheDocument();
    // …and nothing claims a decision is owed.
    expect(screen.queryByText(/needs? a decision/i)).not.toBeInTheDocument();
  });

  // The other end: once the machine hands one over, the heading counts the DECISIONS, not the belt.
  it("counts only what needs a decision, not the whole belt", () => {
    renderAsks(
      <IncomingPanel
        clips={[d(row({ hash: "done-1", name: "Handed over" })), row()]}
        reels={[]}
        stageOrder={LADDER}
      />,
    );

    expect(screen.getByRole("heading", { name: /1 clip needs a decision/i })).toBeInTheDocument();
    expect(screen.getByText(/1 more clip still being prepared/i)).toBeInTheDocument();
  });

  // The active voice, in the operator's vocabulary — not "tag / running".
  it("says what is happening to the clip in words", () => {
    renderAsks(<IncomingPanel clips={[row()]} reels={[]} stageOrder={LADDER} />);

    expect(screen.getByRole("status")).toHaveTextContent("Coca-Cola 1985: Working out what it is");
  });

  // ⚠ EXACTLY ONE live region for the section, carrying the most recent transition. One per row —
  // or per rung — is the "chorus of live regions" frontend-design §5.3 forbids: forty clips
  // announcing themselves is unusable, not forty times as useful.
  it("announces through exactly one live region, whatever the queue's length", () => {
    renderAsks(
      <IncomingPanel
        clips={[row(), row({ hash: "b", name: "Pepsi 1984" }), row({ hash: "c", name: "Fanta 1991" })]}
        reels={[]}
        stageOrder={LADDER}
      />,
    );

    expect(screen.getAllByRole("status")).toHaveLength(1);
  });

  // Collapsed by default: forty clips × ten rungs is 400 lines of moving text.
  //
  // ⚠ Asserted through the ACCESSIBILITY TREE, not `queryByText`. `hiddenUntilFound` leaves the
  // panel in the DOM on purpose — that is what lets find-in-page reach a collapsed row and open
  // it — so a text query finds the detail whether or not it is exposed, and the first draft of
  // this test passed against a panel that was never collapsed at all. Role queries skip what is
  // hidden from assistive tech, which is the property actually being claimed.
  it("keeps the stage-by-stage detail out of the accessibility tree until it is opened", async () => {
    renderAsks(<IncomingPanel clips={[row()]} reels={[]} stageOrder={LADDER} />);
    const detail = { name: "Stage detail for Coca-Cola 1985" } as const;

    expect(screen.queryByRole("list", detail)).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: /show what is happening to Coca-Cola/i }));
    expect(screen.getByRole("list", detail)).toBeInTheDocument();
  });
});

// The audit half of refusal (§10 V51b/V51e).
describe("IncomingPanel — rejected", () => {
  const reject = (over: Partial<IncomingRejectDTO> = {}): IncomingRejectDTO => ({
    hash: "hash-mystery",
    name: "clip_0042.mp4",
    reason: "unidentified",
    detail: "no era, audience, tag, brand, transcript or on-screen text",
    restorable: true,
    stage: "score",
    at: "2026-08-08T09:00:00Z",
    ...over,
  });

  // The server sends a CODE; the wording is the frontend's (§11's refusal-code precedent).
  it("turns the refusal code into something an operator can read, with the measured detail", () => {
    renderAsks(<IncomingPanel clips={[]} reels={[]} rejected={[reject()]} />);

    expect(screen.getByText("nothing in it said what it was")).toBeInTheDocument();
    expect(screen.getByText(/no era, audience, tag/)).toBeInTheDocument();
  });

  it("offers the override for a soft refusal", async () => {
    const onRestore = vi.fn();
    const clip = reject();
    renderAsks(<IncomingPanel clips={[]} reels={[]} rejected={[clip]} onRestore={onRestore} />);

    await userEvent.click(screen.getByRole("button", { name: /use it anyway/i }));
    expect(onRestore).toHaveBeenCalledWith(clip);
  });

  // ⚠ THE asymmetry. Restoring a clip with no audio puts silence in a break, so there is no
  // answer a human could give — and a control that cannot work is worse than no control. The
  // server owns which refusals are soft; this only renders what it said.
  it("offers NO override for a hard refusal", () => {
    renderAsks(
      <IncomingPanel
        clips={[]}
        reels={[]}
        rejected={[reject({ reason: "no_audio", restorable: false })]}
        onRestore={vi.fn()}
      />,
    );

    expect(screen.getByText("it has no sound")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /use it anyway/i })).not.toBeInTheDocument();
  });

  // A code this build has no copy for comes from a NEWER backend. The raw code tells an operator —
  // and a bug report — something; "Unknown reason" tells nobody anything.
  it("falls back to the server's own code rather than inventing a placeholder", () => {
    renderAsks(<IncomingPanel clips={[]} reels={[]} rejected={[reject({ reason: "fingerprint_clash" })]} />);

    expect(screen.getByText("fingerprint_clash")).toBeInTheDocument();
  });
});
