import type { ClipDTO } from "@loomarr/api";
import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ClipPlayer } from "./clip-player";

// The clip player dialog (V39). Thin by design — the playing is `VideoPlayer`'s job and is tested
// there — so these cover what is actually THIS component's: the open/closed rule, the media URL,
// and the dialog's accessible name.

const clip: ClipDTO = {
  name: "Frosted Flakes",
  kind: "commercial",
  durationMs: 30_000,
  era: 1990,
  audience: "kids",
  category: "food",
  tagged: true,
  aiTagged: false,
  playCount: 0,
  playsCounted: true,
  hash: "hash-intro",
  tunarrProgramId: "clip-1",
};

describe("ClipPlayer", () => {
  // ⚠ **A null clip must mount NOTHING.** `open` is derived from the clip rather than passed
  // separately precisely so the two cannot disagree — and the <video> must be unmounted while
  // closed, or a paused element sits there holding a connection open for the life of the page.
  it("renders nothing without a clip", () => {
    const { container } = render(<ClipPlayer clip={null} onClose={() => {}} />);
    expect(container.querySelector("video")).toBeNull();
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("opens on a clip and plays its media URL", () => {
    render(<ClipPlayer clip={clip} onClose={() => {}} />);

    const video = document.querySelector("video");
    // Built from the clip's HASH via clipMediaURL — per-segment encoded as belt-and-braces,
    // though a hash needs no escaping the way a slash-bearing path once did.
    expect(video).toHaveAttribute("src", "/v1/filler/media/hash-intro");
    // Autoplay is safe here: reaching this dialog required clicking a play button, so the gesture
    // that permits it has already happened.
    expect(video).toHaveAttribute("autoplay");
  });

  it("reports playback only after the media element starts playing", () => {
    const onPlaybackStart = vi.fn();
    render(<ClipPlayer clip={clip} onClose={() => {}} onPlaybackStart={onPlaybackStart} />);

    expect(onPlaybackStart).not.toHaveBeenCalled();
    fireEvent.playing(document.querySelector("video") as HTMLVideoElement);
    expect(onPlaybackStart).toHaveBeenCalledOnce();
  });

  // ⚠ Radix REQUIRES a dialog title and warns loudly without one; it is also what names the modal
  // for a screen reader. It is sr-only because the VISIBLE title renders inside the player's own
  // overlay — announcing both would read the clip's name twice.
  it("names the dialog for a screen reader without duplicating the visible title", () => {
    render(<ClipPlayer clip={clip} onClose={() => {}} />);

    expect(screen.getByRole("dialog", { name: "Playing Frosted Flakes" })).toBeInTheDocument();
    // The visible one is the player's overlay title — one element, not two.
    expect(screen.getAllByText("Frosted Flakes")).toHaveLength(1);
  });

  it("closes through the dialog's own close control", async () => {
    const onClose = vi.fn();
    render(<ClipPlayer clip={clip} onClose={onClose} />);

    screen.getByRole("button", { name: "Close player" }).click();
    expect(onClose).toHaveBeenCalled();
  });
});
