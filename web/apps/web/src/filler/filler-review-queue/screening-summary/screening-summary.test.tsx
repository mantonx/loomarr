import type { FillerScreeningDTO } from "@loomarr/api";
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { ScreeningSummary } from "./screening-summary";

const HASH = "a".repeat(64);

const unavailable = (reasonCode: string): FillerScreeningDTO => ({
  state: "unavailable",
  reasonCode,
  clipHash: HASH,
  subjectSha256: "b".repeat(64),
  evidenceSha256: "c".repeat(64),
  axes: [],
});

describe("ScreeningSummary", () => {
  it("explains an evidence hold without inventing a safety answer", () => {
    render(<ScreeningSummary summary={unavailable("screening_evidence_drift")} />);

    expect(screen.getByText("Screening evidence unavailable")).toBeInTheDocument();
    expect(screen.getByText(/cannot be confirmed for the library/i)).toBeInTheDocument();
    expect(screen.queryByText(/safe/i)).not.toBeInTheDocument();
  });

  it("distinguishes a clip that has never been screened", () => {
    render(
      <ScreeningSummary
        summary={{ state: "not_screened", reasonCode: "screening_not_attached", clipHash: HASH, axes: [] }}
      />,
    );

    expect(screen.getByText("Screening has not run yet")).toBeInTheDocument();
  });
});
