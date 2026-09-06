import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useHoldControls } from "./internal/hold-controls-context";
import { VideoPlayer } from "./video-player";

// The app's video surface (V39). Custom controls were a maintainer decision, which means the
// keyboard and ARIA behaviour native `<video controls>` would have given for free is this
// component's job — and therefore has to be pinned here.
//
// ⚠ jsdom implements no media pipeline: `play()` is not a function, `duration` is NaN, and nothing
// ever fires `timeupdate`. So these test the parts that are OURS — the semantics, the labels, the
// wiring — and leave "does it actually decode" to the browser, where it was verified live.
const SRC = "data:video/mp4;base64,AAAA";

describe("VideoPlayer", () => {
  it("renders a titled frame with the app's own controls", () => {
    render(<VideoPlayer src={SRC} title="Frosted Flakes" />);

    expect(screen.getByText("Frosted Flakes")).toBeInTheDocument();
    // Play/pause and mute, both named. Native controls would have supplied these.
    expect(screen.getByRole("button", { name: "Play" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Mute" })).toBeInTheDocument();
  });

  it("reports actual playback start from the media element", () => {
    const onPlaybackStart = vi.fn();
    const { container } = render(<VideoPlayer src={SRC} onPlaybackStart={onPlaybackStart} />);

    expect(onPlaybackStart).not.toHaveBeenCalled();
    fireEvent.playing(container.querySelector("video") as HTMLVideoElement);
    expect(onPlaybackStart).toHaveBeenCalledOnce();
  });

  // A player under a heading that already names the thing does not repeat it — and the whole top
  // overlay goes with it rather than rendering an empty scrim over the frame.
  it("renders no top-bar overlay without a title or leading", () => {
    const { container } = render(<VideoPlayer src={SRC} />);
    // The top scrim uses the linear-gradient utility; absent when there is nothing to put in it.
    expect(container.querySelector(".bg-linear-to-b")).toBeNull();
  });

  // ⚠ **The mute button's NAME tracks its state.** A control permanently labelled "Mute" lies as
  // soon as the video is muted, and a screen-reader user has no other way to know which way it
  // will go. (Play/pause has the same rule, but its state is driven by media events jsdom never
  // fires, so mute is where the rule is testable.)
  it("renames the mute button once muted", () => {
    render(<VideoPlayer src={SRC} />);

    fireEvent.click(screen.getByRole("button", { name: "Mute" }));
    expect(screen.getByRole("button", { name: "Unmute" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Mute" })).not.toBeInTheDocument();
  });

  // A non-live player (a clip) has NO seek bar (V47 removed it — clips are short previews). The only
  // slider present is the VOLUME control; there is no "Seek" slider. This pins that removal so a
  // seek bar cannot creep back in unnoticed.
  it("has no seek slider — only the volume slider", () => {
    render(<VideoPlayer src={SRC} title="Frosted Flakes" />);

    const sliders = screen.getAllByRole("slider");
    expect(sliders).toHaveLength(1);
    expect(sliders[0]).toHaveAttribute("aria-label", "Volume");
    // Fullscreen is the player's own control now (icon, no text).
    expect(screen.getByRole("button", { name: "Fullscreen" })).toBeInTheDocument();
  });

  // Before metadata arrives there is no honest time to show, so a clip's time readout is omitted
  // (not "0:00 / 0:00", which would assert a zero-length video). It appears once metadata loads —
  // which jsdom never fires, so the "absent until ready" half is what is testable here.
  it("shows no time readout until metadata loads", () => {
    render(<VideoPlayer src={SRC} />);
    expect(screen.queryByText(/\d+:\d+ \/ \d+:\d+/)).not.toBeInTheDocument();
    expect(screen.queryByText("–:–– / –:––")).not.toBeInTheDocument();
  });

  // `leading` is how a dialog puts its close button ON the frame rather than above it. The
  // primitive itself has no opinion about dismissal.
  it("renders a caller-supplied leading control", () => {
    render(<VideoPlayer src={SRC} leading={<button type="button">Close</button>} />);
    expect(screen.getByRole("button", { name: "Close" })).toBeInTheDocument();
  });

  it("lowers keyboard, page, and media channel keys to one live-tuner seam", () => {
    const onChannelStep = vi.fn();
    const { container } = render(<VideoPlayer src={SRC} live onChannelStep={onChannelStep} />);
    const video = container.querySelector("video") as HTMLVideoElement;

    fireEvent.keyDown(video, { key: "ArrowUp" });
    fireEvent.keyDown(video, { key: "PageDown" });
    fireEvent.keyDown(video, { key: "ChannelUp" });
    expect(onChannelStep.mock.calls.map(([direction]) => direction)).toEqual([1, -1, 1]);
  });

  it("does not steal channel-navigation keys from an interactive child", () => {
    const onChannelStep = vi.fn();
    render(
      <VideoPlayer
        src={SRC}
        live
        onChannelStep={onChannelStep}
        barControls={<button type="button">Menu</button>}
      />,
    );
    fireEvent.keyDown(screen.getByRole("button", { name: "Menu" }), { key: "ArrowUp" });
    expect(onChannelStep).not.toHaveBeenCalled();
  });

  it("waits for replacement loadeddata before observing the frame that clears a held poster", () => {
    const requestFrame = vi.fn(() => 1);
    Object.defineProperty(HTMLVideoElement.prototype, "requestVideoFrameCallback", {
      configurable: true,
      value: requestFrame,
    });
    const firstAttach = vi.fn(() => () => undefined);
    const replacementAttach = vi.fn(() => () => undefined);
    const { container, rerender } = render(<VideoPlayer attach={firstAttach} live />);
    const video = container.querySelector("video") as HTMLVideoElement;

    expect(requestFrame).not.toHaveBeenCalled();
    video.poster = "data:image/png;base64,held";
    fireEvent.loadedData(video);
    expect(video).not.toHaveAttribute("poster");
    expect(requestFrame).not.toHaveBeenCalled();

    rerender(<VideoPlayer attach={replacementAttach} live />);
    video.poster = "data:image/png;base64,held";
    expect(video).toHaveAttribute("poster");
    fireEvent.loadedData(video);
    expect(video).not.toHaveAttribute("poster");
    expect(requestFrame).not.toHaveBeenCalled();

    delete (HTMLVideoElement.prototype as Partial<HTMLVideoElement>).requestVideoFrameCallback;
  });

  it("keeps behind-live context and Go Live together in the playback toolbar", () => {
    const goLive = vi.fn();
    const { container } = render(
      <VideoPlayer
        src={SRC}
        live
        topBar={<span>CH 42</span>}
        liveTransport={{
          state: { mode: "behind", lagSeconds: 23, viewerTimeMs: 1_000, noticeRevision: 0 },
          play: vi.fn(),
          pause: vi.fn(),
          goLive,
        }}
      />,
    );

    const controls = screen.getByRole("group", { name: "Playback controls" });
    expect(within(controls).getByText("23s behind")).toBeInTheDocument();
    fireEvent.click(within(controls).getByRole("button", { name: "Go live" }));
    expect(goLive).toHaveBeenCalledOnce();

    const topBar = container.querySelector(".bg-linear-to-b") as HTMLElement;
    expect(within(topBar).queryByRole("button", { name: "Go live" })).not.toBeInTheDocument();
  });
});

// Auto-hide — the Emby/Jellyfin behaviour that native `<video controls>` gives free but custom
// controls must own. The controls OVERLAY the video and fade out during playback; the fade is the
// bottom control bar's opacity class (`opacity-100` shown ⇄ `opacity-0` hidden).
//
// ⚠ jsdom fires no media events on its own, but `fireEvent.play(video)` dispatches a real `play`
// event, which flips our `playing` state — that is what unlocks testing the hide path (hiding only
// happens while playing). Timers are faked so the IDLE/GRACE windows are deterministic.
describe("VideoPlayer auto-hide", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  // The bottom control bar is the scrim carrying the shown/hidden opacity — grab it by that gradient.
  const controlBar = (container: HTMLElement) => container.querySelector(".from-black\\/80") as HTMLElement;
  const frame = (container: HTMLElement) => container.querySelector(".group\\/player") as HTMLElement;

  const startPlaying = (container: HTMLElement) => {
    // Reveal (pointer over the frame), then start playback so the hide logic is armed.
    fireEvent.mouseMove(frame(container));
    fireEvent.play(container.querySelector("video") as HTMLVideoElement);
  };

  it("keeps the controls shown while the pointer rests on the frame", () => {
    const { container } = render(<VideoPlayer src={SRC} />);
    startPlaying(container);

    // The idle window elapses, but the pointer is still on the frame ⇒ stays shown.
    act(() => vi.advanceTimersByTime(3000));
    expect(controlBar(container)).toHaveClass("opacity-100");
  });

  it("hides the controls shortly after the pointer leaves the frame during playback", () => {
    const { container } = render(<VideoPlayer src={SRC} />);
    startPlaying(container);
    expect(controlBar(container)).toHaveClass("opacity-100");

    // Pointer leaves the frame ⇒ the short GRACE window, then hidden.
    fireEvent.mouseLeave(frame(container));
    act(() => vi.advanceTimersByTime(700));
    expect(controlBar(container)).toHaveClass("opacity-0");
  });

  it("re-hides after a hold clears while the pointer is off the frame (a closed menu)", () => {
    // A held control (open menu) keeps the bar shown even with the pointer gone; when it releases
    // (menu closed) the bar must hide again rather than linger — the behaviour this reproduces.
    let hold!: (on: boolean) => void;
    const Grabber = () => {
      hold = useHoldControls().hold;
      return null;
    };
    const { container } = render(<VideoPlayer src={SRC} barControls={<Grabber />} />);
    startPlaying(container);

    // Pointer off the frame, but a menu is open (held) ⇒ stays shown past the grace window.
    fireEvent.mouseLeave(frame(container));
    act(() => hold(true));
    act(() => vi.advanceTimersByTime(700));
    expect(controlBar(container)).toHaveClass("opacity-100");

    // Menu closes: the hold releases and, with the pointer still off the frame, the bar hides.
    act(() => hold(false));
    act(() => vi.advanceTimersByTime(700));
    expect(controlBar(container)).toHaveClass("opacity-0");
  });
});
