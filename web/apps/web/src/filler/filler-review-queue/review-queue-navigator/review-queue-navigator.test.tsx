import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { ReviewQueueNavigator } from "./review-queue-navigator";

const items = [
  { id: "one", question: "Is this a commercial?", subject: "Clip one" },
  { id: "two", question: "Is this a programme promo?", subject: "Clip two" },
];

const baseProps = {
  items,
  selectedID: "one",
  total: 12,
  pageNumber: 1,
  pageCount: 2,
  hasPreviousPage: false,
  hasNextPage: true,
  paging: false,
  onSelect: vi.fn(),
  onPreviousPage: vi.fn(),
  onNextPage: vi.fn(),
};

describe("ReviewQueueNavigator", () => {
  it("identifies the focused question and selects another without answering it", async () => {
    const onSelect = vi.fn();
    render(<ReviewQueueNavigator {...baseProps} onSelect={onSelect} />);

    expect(screen.getByText("12 questions waiting · Page 1 of 2")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Review question 1: Is this a commercial?" })).toHaveAttribute(
      "aria-current",
      "true",
    );
    await userEvent.click(
      screen.getByRole("button", { name: "Review question 2: Is this a programme promo?" }),
    );
    expect(onSelect).toHaveBeenCalledWith("two");
  });

  it("keeps cursor paging bounded by server availability and in-flight state", async () => {
    const onNextPage = vi.fn();
    const { rerender } = render(<ReviewQueueNavigator {...baseProps} onNextPage={onNextPage} />);

    expect(screen.getByRole("button", { name: "Previous page" })).toBeDisabled();
    await userEvent.click(screen.getByRole("button", { name: "Next page" }));
    expect(onNextPage).toHaveBeenCalledOnce();

    rerender(<ReviewQueueNavigator {...baseProps} paging onNextPage={onNextPage} />);
    expect(screen.getByRole("button", { name: "Next page" })).toBeDisabled();
  });
});
