import type { GuideLayout } from "@loomarr/core/guide";
import { LoomarrProvider } from "@loomarr/design-system";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";

import { GuideExperience, GuideSurface } from "../index";

const layout = {
  channels: [
    {
      airings: [
        {
          channelId: "springfield",
          clippedStartMs: 0,
          clippedStopMs: 1_800_000,
          endsAfterWindow: false,
          isOnNow: true,
          progressRatio: 0.5,
          scheduleBlockId: "bart",
          source: {
            description: "Bart cares for an injured bird.",
            episode: 3,
            genres: ["Animation", "Comedy"],
            kind: "program",
            rating: "TV-PG",
            scheduleBlockId: "bart",
            season: 10,
            series: "The Simpsons",
            startMs: 0,
            stopMs: 1_800_000,
            title: "Bart the Mother",
            year: 1998,
          },
          startRatio: 0,
          startsBeforeWindow: false,
          widthRatio: 0.5,
        },
      ],
      source: {
        airings: [],
        channelId: "springfield",
        name: "Springfield Classics",
        number: 1,
        pendingCount: 0,
        status: "live",
      },
    },
  ],
  fromMs: 0,
  source: { channels: [], fromMs: 0, timezone: "UTC", toMs: 3_600_000 },
  timezone: "UTC",
  toMs: 3_600_000,
} satisfies GuideLayout;

const selection = { anchorMs: 900_000, channelId: "springfield", scheduleBlockId: "bart" };

const markup = () =>
  renderToStaticMarkup(
    <LoomarrProvider>
      <GuideSurface layout={layout} onSelectionChange={vi.fn()} selection={selection} />
    </LoomarrProvider>,
  );

describe("GuideSurface", () => {
  it("renders authoritative channel, programme, episode, and detail facts", () => {
    const output = markup();
    expect(output).toContain("Springfield Classics");
    expect(output).toContain("Bart the Mother");
    expect(output).toContain("S10E03");
    expect(output).toContain("1998 · TV-PG · Animation · Comedy");
    expect(output).toContain("12:00 AM");
  });

  it("publishes one labelled tuning action and disables empty optional filters", () => {
    const output = markup();
    expect(output).toContain("Springfield Classics, The Simpsons · Bart the Mother, 12:00 AM–12:30 AM");
    expect(output).toContain('aria-label="Favourites channels"');
    expect(output).toContain('aria-label="Recent channels"');
    expect(output.match(/aria-disabled="true"/g)).toHaveLength(2);
  });

  it("preserves pressed-button semantics for the selected TV filter", () => {
    const output = renderToStaticMarkup(
      <LoomarrProvider>
        <GuideSurface
          density="tv"
          filter="recent"
          filters={[
            { label: "All", value: "all" },
            { disabled: true, label: "Favourites", value: "favourites" },
            { label: "Recent", value: "recent" },
          ]}
          layout={layout}
          onSelectionChange={vi.fn()}
          selection={selection}
        />
      </LoomarrProvider>,
    );

    expect(output).toMatch(/aria-label="Recent channels"[^>]*aria-pressed="true"/);
  });

  it("bounds TV rows around focus while preserving detail and artwork fallback", () => {
    const [baseChannel] = layout.channels;
    if (!baseChannel) throw new Error("expected a Guide fixture Channel");
    const newsChannel = {
      ...baseChannel,
      airings: baseChannel.airings.map((airing) => ({
        ...airing,
        channelId: "news",
        scheduleBlockId: "headlines",
        source: {
          ...airing.source,
          scheduleBlockId: "headlines",
          series: "Evening News",
          title: "Headlines",
        },
      })),
      source: {
        ...baseChannel.source,
        channelId: "news",
        name: "Loomarr News",
        number: 2,
      },
    };
    const output = renderToStaticMarkup(
      <LoomarrProvider>
        <GuideSurface
          channelWindow={{ end: 2, positionLabel: "2 of 2", start: 1 }}
          density="tv"
          layout={{ ...layout, channels: [...layout.channels, newsChannel] }}
          onSelectionChange={vi.fn()}
          selection={{ anchorMs: 900_000, channelId: "news", scheduleBlockId: "headlines" }}
        />
      </LoomarrProvider>,
    );

    expect(output).not.toContain("Springfield Classics");
    expect(output).toContain("Loomarr News");
    expect(output).toContain("Evening News");
    expect(output).toContain("All · 2");
    expect(output).toContain("2 of 2");
    expect(output).toContain("NO ART");
  });

  it.each([
    ["loading", "Loading channels"],
    ["empty", "No channels on air"],
    ["error", "Guide unavailable"],
    ["offline", "You&#x27;re offline"],
  ] as const)("owns the %s guide state", (state, title) => {
    const output = renderToStaticMarkup(
      <LoomarrProvider>
        <GuideExperience onRetry={vi.fn()} state={state} />
      </LoomarrProvider>,
    );
    expect(output).toContain(title);
    expect(output.includes("Try again")).toBe(state === "error" || state === "offline");
  });
});
