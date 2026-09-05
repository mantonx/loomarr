// @vitest-environment jsdom

import { LoomarrProvider } from "@loomarr/design-system";
import type { PlayerSnapshot } from "@loomarr/player";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";

import { WatchingSurface } from "../index";

(
  globalThis as typeof globalThis & {
    IS_REACT_ACT_ENVIRONMENT: boolean;
  }
).IS_REACT_ACT_ENVIRONMENT = true;

const channel = { id: "seven", inAppPlayable: true, name: "Science Fiction", number: 7 };
const createTestContainer = () =>
  (
    globalThis as unknown as {
      document: { createElement: (tagName: string) => Parameters<typeof createRoot>[0] };
    }
  ).document.createElement("div");
const playing: PlayerSnapshot = {
  attemptId: 4,
  catalog: [channel],
  channel,
  livePlayback: { lagSeconds: 0, mode: "live", noticeRevision: 0, viewerTimeMs: 1_777_777_777_000 },
  previousChannelId: "six",
  recentChannelIds: ["six"],
  status: "playing",
  tuneReason: "step",
};

const renderSurface = (
  snapshot: PlayerSnapshot,
  options: {
    chromeVisible?: boolean;
    controlsVisible?: boolean;
    density?: "touch" | "tv";
    loading?: boolean;
    loadError?: string;
    schedule?: Parameters<typeof WatchingSurface>[0]["schedule"];
  } = {},
) =>
  renderToStaticMarkup(
    <LoomarrProvider>
      <WatchingSurface
        chromeVisible={options.chromeVisible}
        controlsVisible={options.controlsVisible}
        density={options.density ?? "tv"}
        loading={options.loading}
        loadError={options.loadError}
        onChannelDown={vi.fn()}
        onChannelUp={vi.fn()}
        onDismissControls={vi.fn()}
        onGoLive={vi.fn()}
        onOpenGuide={vi.fn()}
        onOpenSurf={vi.fn()}
        onPause={vi.fn()}
        onPlay={vi.fn()}
        onPrevious={vi.fn()}
        onRetry={vi.fn()}
        onShowControls={vi.fn()}
        player={<div data-player="one-native-player" />}
        schedule={options.schedule}
        snapshot={snapshot}
      />
    </LoomarrProvider>,
  );

describe("WatchingSurface", () => {
  it("dismisses TV identity and playbar five seconds after activity despite live programme updates", () => {
    vi.useFakeTimers();
    const container = createTestContainer();
    const root = createRoot(container);
    const firstDismiss = vi.fn();
    const progressUpdateDismiss = vi.fn();
    const render = (progressPercent: number, controlsActivityKey: number, onDismissControls: () => void) =>
      root.render(
        <LoomarrProvider>
          <WatchingSurface
            controlsActivityKey={controlsActivityKey}
            density="tv"
            onChannelDown={vi.fn()}
            onChannelUp={vi.fn()}
            onDismissControls={onDismissControls}
            onGoLive={vi.fn()}
            onOpenGuide={vi.fn()}
            onOpenSurf={vi.fn()}
            onPause={vi.fn()}
            onPlay={vi.fn()}
            onPrevious={vi.fn()}
            onRetry={vi.fn()}
            onShowControls={vi.fn()}
            player={<div />}
            schedule={{
              now: { progressPercent, timeLabel: "9:00 PM–9:30 PM", title: "The Current Frontier" },
            }}
            snapshot={playing}
          />
        </LoomarrProvider>,
      );

    act(() => render(42, 0, firstDismiss));
    act(() => vi.advanceTimersByTime(4_999));
    act(() => render(43, 0, progressUpdateDismiss));
    act(() => vi.advanceTimersByTime(1));

    expect(firstDismiss).not.toHaveBeenCalled();
    expect(progressUpdateDismiss).toHaveBeenCalledOnce();
    act(() => root.unmount());
    vi.useRealTimers();
  });

  it("starts a fresh TV dismissal window only when handled activity changes", () => {
    vi.useFakeTimers();
    const container = createTestContainer();
    const root = createRoot(container);
    const dismiss = vi.fn();
    const render = (controlsActivityKey: number) =>
      root.render(
        <LoomarrProvider>
          <WatchingSurface
            controlsActivityKey={controlsActivityKey}
            density="tv"
            onChannelDown={vi.fn()}
            onChannelUp={vi.fn()}
            onDismissControls={dismiss}
            onGoLive={vi.fn()}
            onOpenGuide={vi.fn()}
            onOpenSurf={vi.fn()}
            onPause={vi.fn()}
            onPlay={vi.fn()}
            onPrevious={vi.fn()}
            onRetry={vi.fn()}
            onShowControls={vi.fn()}
            player={<div />}
            snapshot={playing}
          />
        </LoomarrProvider>,
      );

    act(() => render(0));
    act(() => vi.advanceTimersByTime(4_999));
    act(() => render(1));
    act(() => vi.advanceTimersByTime(1));
    expect(dismiss).not.toHaveBeenCalled();
    act(() => vi.advanceTimersByTime(4_999));
    expect(dismiss).toHaveBeenCalledOnce();
    act(() => root.unmount());
    vi.useRealTimers();
  });

  it("keeps one player mounted behind the TV presentation and quiet remote hints", () => {
    const output = renderSurface(playing);
    expect(output).toContain("one-native-player");
    expect(output).toContain("SCIENCE FICTION");
    expect(output).toContain("▲▼ tune");
    expect(output).toContain("◀ channels");
    expect(output).toContain("0–9 jump");
    expect(output).toContain("OK guide");
    expect(output).toContain('aria-label="Open programme guide"');
    expect(output).not.toContain("Previous");
    expect(output).not.toContain("Channel −");
    expect(output).not.toContain("Channel +");
  });

  it("renders authoritative current and next programme identity with live progress", () => {
    const output = renderSurface(playing, {
      schedule: {
        next: { timeLabel: "9:30 PM", title: "The Next Frontier" },
        now: {
          badge: { label: "On now", tone: "live" },
          episodeLabel: "S1 E4",
          facts: ["2026", "TV-14"],
          progressPercent: 42,
          timeLabel: "9:00 PM–9:30 PM",
          title: "The Current Frontier",
        },
      },
    });

    expect(output).toContain("The Current Frontier");
    expect(output).toContain("S1 E4");
    expect(output).toContain("2026");
    expect(output).not.toContain("TV-14");
    expect(output).toContain("Up next · 9:30 PM — The Next Frontier");
    expect(output).toContain("42%");
  });

  it("presents tuning and recoverable playback failures without replacing Channel identity", () => {
    expect(renderSurface({ ...playing, status: "tuning" })).toContain("Tuning…");
    const failed = renderSurface({ ...playing, error: "The stream could not be decoded.", status: "failed" });
    expect(failed).toContain("SCIENCE FICTION");
    expect(failed).toContain("The stream could not be decoded.");
    expect(failed).toContain("Retry");
  });

  it("distinguishes paused and behind-live playback and exposes the live-edge action", () => {
    const paused = renderSurface({
      ...playing,
      livePlayback: { lagSeconds: 83, mode: "paused", noticeRevision: 0, viewerTimeMs: 1_777_777_694_000 },
      status: "paused",
    });
    expect(paused).toContain("Paused · 1:23 behind");
    expect(paused).toContain("Play");
    expect(paused).toContain("Go Live");

    const behind = renderSurface({
      ...playing,
      livePlayback: { lagSeconds: 23, mode: "behind", noticeRevision: 0, viewerTimeMs: 1_777_777_754_000 },
    });
    expect(behind).toContain("0:23 behind");
    expect(behind).not.toContain("Paused ·");
  });

  it("states empty Channel data separately from an authoritative catalog failure", () => {
    const empty: PlayerSnapshot = { catalog: [], recentChannelIds: [], status: "empty" };
    expect(renderSurface(empty)).toContain("No playable channels");
    const failedLoad = renderSurface(empty, { loadError: "Could not load channels." });
    expect(failedLoad).toContain("Could not load channels.");
    expect(failedLoad).toContain("Retry");
  });

  it("does not announce an empty catalog before the authoritative request resolves", () => {
    const empty: PlayerSnapshot = { catalog: [], recentChannelIds: [], status: "empty" };
    const output = renderSurface(empty, { loading: true });

    expect(output).toContain("Loading channels");
    expect(output).not.toContain("No playable channels");
    expect(output).not.toContain("Open programme guide");
    expect(output).not.toContain("Up/Down tune");
  });

  it("keeps playback mounted while transient journeys hide all Watching chrome", () => {
    const output = renderSurface(playing, { chromeVisible: false });
    expect(output).toContain("one-native-player");
    expect(output).not.toContain("Science Fiction");
    expect(output).not.toContain("Open programme guide");
  });

  it("keeps control visibility in presentation state instead of the playback snapshot", () => {
    const output = renderSurface(playing, { controlsVisible: false });
    expect(output).toContain("one-native-player");
    expect(output).toContain("Open programme guide");
    expect(output).not.toContain("Science Fiction");
    expect(output).not.toContain("Up/Down tune");
  });

  it("keeps explicit playback and tune actions in the touch presentation", () => {
    const output = renderSurface(playing, { density: "touch" });
    expect(output).toContain("Previous");
    expect(output).toContain("Channel −");
    expect(output).toContain("Guide");
    expect(output).toContain("Surf");
    expect(output).toContain("Pause");
    expect(output).toContain("Channel +");
  });
});
