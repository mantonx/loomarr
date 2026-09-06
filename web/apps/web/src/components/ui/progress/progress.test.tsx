import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { Progress } from "./progress";

// The whole reason this primitive exists rather than a styled div per caller: the ARIA contract
// for "busy" versus "0 percent" is one line of difference and the wrong one is a false claim
// about a task nobody measured.
describe("Progress", () => {
  it("reports a measurement it was given", () => {
    render(<Progress value={62} label="Levelling the sound" />);

    const bar = screen.getByRole("progressbar", { name: "Levelling the sound" });
    expect(bar).toHaveAttribute("aria-valuenow", "62");
    expect(bar).toHaveAttribute("aria-valuemin", "0");
    expect(bar).toHaveAttribute("aria-valuemax", "100");
  });

  // ⚠ THE assertion. Omitting `value` must announce "busy" — carrying `aria-valuenow` at all
  // would state a measurement, and Whisper and an LLM turn are single opaque calls with none to
  // state. Passing 0 to mean "not measured yet" is the mistake this makes impossible to hide.
  it("omits aria-valuenow entirely when there is nothing to measure", () => {
    render(<Progress label="Listening" />);

    const bar = screen.getByRole("progressbar", { name: "Listening" });
    expect(bar).not.toHaveAttribute("aria-valuenow");
    // min/max without now would describe a range nothing sits in, so the trio travels as a unit.
    expect(bar).not.toHaveAttribute("aria-valuemin");
    expect(bar).not.toHaveAttribute("aria-valuemax");
  });

  it("distinguishes a real zero from no measurement", () => {
    render(<Progress value={0} label="Levelling the sound" />);

    expect(screen.getByRole("progressbar")).toHaveAttribute("aria-valuenow", "0");
  });

  // A caller doing its own arithmetic can hand over 140 or -5; the bar clamps rather than
  // rendering a fill wider than its track.
  it("clamps a value outside the range", () => {
    const { rerender } = render(<Progress value={140} label="x" />);
    expect(screen.getByRole("progressbar")).toHaveAttribute("aria-valuenow", "100");

    rerender(<Progress value={-5} label="x" />);
    expect(screen.getByRole("progressbar")).toHaveAttribute("aria-valuenow", "0");
  });

  // ⚠ Never an `aria-live` region. Forty clips × ten rungs would mount a chorus of them; the
  // surface owns exactly one `role="status"` instead (frontend-design §5.3).
  it("is not a live region", () => {
    render(<Progress value={50} label="x" />);

    const bar = screen.getByRole("progressbar");
    expect(bar).not.toHaveAttribute("aria-live");
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
  });
});
