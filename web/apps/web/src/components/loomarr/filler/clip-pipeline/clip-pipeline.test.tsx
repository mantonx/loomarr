import type { IncomingPipelineDTO } from "@loomarr/api";
import { render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { ClipPipeline } from "./clip-pipeline";
import type { ClipPipelineVariant } from "./clip-pipeline.type";

// The pipeline strip is the only thing telling an operator that forty downloaded clips are being
// worked on rather than stuck. Every assertion here is a way it could say something untrue.
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

// ⚠ `name` is a PROP, not a field on the row: the pipeline block carries no identity or display
// text, because it is nested inside the clip that already has both.
const show = (row: IncomingPipelineDTO, opts: { ladder?: string[]; variant?: ClipPipelineVariant } = {}) =>
  render(
    <ClipPipeline
      row={row}
      name="Coca-Cola 1985"
      ladder={opts.ladder ?? LADDER}
      {...(opts.variant ? { variant: opts.variant } : {})}
    />,
  );

const at = (over: Partial<IncomingPipelineDTO> = {}): IncomingPipelineDTO => ({
  stage: "tag",
  status: "running",
  lifecycle: "in_progress",
  // -1 is the "this rung cannot measure itself" sentinel, and it is the DEFAULT here on purpose:
  // most rungs cannot measure themselves, so a fixture defaulting to a real percentage would make
  // the measured case look like the ordinary one.
  progress: -1,
  stages: [
    { stage: "probe", status: "done", at: "2026-08-08T10:00:00Z" },
    { stage: "transcode", status: "done", at: "2026-08-08T10:00:20Z" },
    {
      stage: "split",
      status: "skipped",
      note: "it is a single advert, not a compilation",
      at: "2026-08-08T10:00:21Z",
    },
    { stage: "language", status: "done", at: "2026-08-08T10:00:40Z" },
    {
      stage: "transcribe",
      status: "skipped",
      note: "the description already says enough",
      at: "2026-08-08T10:00:41Z",
    },
  ],
  updatedAt: "2026-08-08T10:01:00Z",
  ...over,
});

describe("ClipPipeline — strip", () => {
  // ⚠ THE rule the whole component is shaped around. `row.stages` is the VISITED ladder, so a
  // strip built from it would have five pips here and eight at the end — an operator could never
  // see how far there is left to go, and the bar would appear to grow rather than fill.
  it("draws one pip per LADDER rung, not per visited record", () => {
    show(at());

    expect(screen.getAllByRole("listitem")).toHaveLength(LADDER.length);
  });

  it("names every rung and its state, because colour is never the only signal", () => {
    show(at());

    const list = screen.getByRole("list", { name: "Progress for Coca-Cola 1985" });
    expect(within(list).getByText("Check the file: done")).toBeInTheDocument();
    expect(within(list).getByText("Find the ads inside: skipped")).toBeInTheDocument();
    expect(within(list).getByText("Work out what it is: in progress")).toBeInTheDocument();
    // Rungs the clip has not reached are "not started", NOT absent and NOT "done".
    expect(within(list).getByText("Score it: not started")).toBeInTheDocument();
  });

  // ⚠ FOUND BY LOOKING, NOT BY TESTING. The runner enrols every clip at `probe/queued` and works
  // one at a time, so on a real catalog of 85 this said "in progress" 85 times over — a queue that
  // claimed to be doing everything at once while doing one thing. `queued` is its own state:
  // neither `running` (nothing is happening yet) nor `upcoming` (it HAS been admitted).
  //
  // The fixtures all used `status: "running"` for the current rung, which is why nothing caught it.
  it("says a queued rung is waiting, not in progress", () => {
    show(at({ stage: "probe", status: "queued", stages: [] }));

    expect(screen.getByText("Check the file: waiting")).toBeInTheDocument();
    expect(screen.queryByText("Check the file: in progress")).not.toBeInTheDocument();
    // …and the rungs behind it stay `not started`, so the two remain distinguishable.
    expect(screen.getByText("Level the sound: not started")).toBeInTheDocument();
  });

  // The runner records a rung only once it RESOLVES, so the stage mid-run has no visited record.
  // Reading state from `row.stages` alone would draw the rung actually being worked on as if
  // nothing had started — the queue would look stalled at exactly the moment it is busiest.
  it("reads the current rung from the row's own position, not from the visited ladder", () => {
    show(at({ stage: "vision", status: "running" }));

    expect(screen.getByText("Look at the picture: in progress")).toBeInTheDocument();
  });
});

describe("ClipPipeline — list", () => {
  it("puts the skip REASON inline, so a stage that did not happen does not read as broken", () => {
    show(at(), { variant: "list" });

    expect(screen.getByText(/the description already says enough/)).toBeInTheDocument();
  });

  it("uses the active voice for the rung being worked on", () => {
    show(at(), { variant: "list" });

    expect(screen.getByText("Working out what it is")).toBeInTheDocument();
    // …and the plain label for one that has not started.
    expect(screen.getByText("Score it")).toBeInTheDocument();
  });

  // ⚠ A 0-width bar reads as "no progress" rather than "no measurement" — a different and false
  // claim. Only transcode can measure itself; Whisper and an LLM turn are single opaque calls.
  it("renders NO bar for a running rung that cannot measure itself", () => {
    show(at({ progress: -1 }), { variant: "list" });

    expect(screen.queryByRole("progressbar")).not.toBeInTheDocument();
  });

  it("renders a bar only for the rung that measured one", () => {
    show(at({ progress: 62, stage: "transcode" }), { variant: "list" });

    const bar = screen.getByRole("progressbar");
    expect(bar).toHaveAttribute("aria-valuenow", "62");
  });

  // Scoped to the failed rung, not the list: one clip failing must announce, forty rungs quietly
  // succeeding must not.
  it("announces a failure on the rung that failed", () => {
    show(at({ stage: "vision", status: "failed" }), { variant: "list" });

    expect(within(screen.getByRole("alert")).getByText("Look at the picture")).toBeInTheDocument();
  });

  // ⚠ FOUND BY axe IN CI, AFTER a jsdom suite that was fully green — so it is asserted here now,
  // where it costs a second instead of an 18-minute Playwright cycle.
  //
  // `role="alert"` was on the `<li>` itself. A role on a list item REPLACES its implicit
  // `listitem` role, so the `<ol>` ended up with a direct child that was not a list item: axe
  // `list`, serious, and the ladder's accessible structure quietly gone. The announcement lives on
  // a wrapper inside the `<li>` instead. **Adding ARIA is itself a way to introduce a violation.**
  it("keeps every direct child of the ladder a list item, even the failed one", () => {
    show(at({ stage: "vision", status: "failed" }), { variant: "list" });

    const list = screen.getByRole("list", { name: /Stage detail/ });
    for (const child of Array.from(list.children)) {
      expect(child.tagName).toBe("LI");
      // ⚠ The tag is not enough: an explicit role on an <li> overrides the implicit one, which is
      // exactly the bug. Assert the element still has no role attribute of its own.
      expect(child.getAttribute("role")).toBeNull();
    }
    // …and the announcement survives the move.
    expect(screen.getByRole("alert")).toBeInTheDocument();
  });

  // ⚠ A newer backend adding a rung must not blank it out of a ladder that claims to be complete.
  // The raw id tells an operator — and a bug report — more than "Unknown stage" would.
  it("falls back to the server's own id for a stage this build has no copy for", () => {
    show(at(), { ladder: [...LADDER, "fingerprint"], variant: "list" });

    expect(screen.getByText("fingerprint")).toBeInTheDocument();
  });
});
