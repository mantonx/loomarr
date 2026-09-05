// @vitest-environment jsdom

import { PairingSession } from "@loomarr/core/pairing";
import type { ServerDiscovery } from "@loomarr/core/server-discovery";
import { LoomarrProvider } from "@loomarr/design-system";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";

import { PairingShell } from "../index";

(
  globalThis as typeof globalThis & {
    IS_REACT_ACT_ENVIRONMENT: boolean;
  }
).IS_REACT_ACT_ENVIRONMENT = true;

const createTestContainer = () =>
  (
    globalThis as unknown as {
      document: { createElement: (tagName: string) => Parameters<typeof createRoot>[0] };
    }
  ).document.createElement("div");

const awaitingSession = async () => {
  const session = new PairingSession({
    createTransport: () => ({
      poll: vi.fn(async () => ({ status: "pending" as const })),
      start: vi.fn(async () => ({
        body: {
          deviceCode: "device-code",
          expiresAt: new Date(Date.now() + 600_000).toISOString(),
          interval: 5,
          userCode: "WMQJ-QVFJ",
        },
        serverDate: new Date().toUTCString(),
      })),
    }),
    deviceName: "Living Room TV",
    sleep: (_milliseconds, signal) =>
      new Promise((_resolve, reject) => {
        signal.addEventListener("abort", () => reject(new Error("Pairing stopped")));
      }),
    store: {
      clear: vi.fn(async () => undefined),
      read: vi.fn(async () => undefined),
      write: vi.fn(async () => undefined),
    },
  });
  const pairing = session.pair("https://loomarr.projectguacamole.com");
  await vi.waitFor(() => expect(session.snapshot().status).toBe("awaiting-approval"));
  return { pairing, session };
};

describe("TV pairing offer", () => {
  it("offers discovered servers before the manual-address fallback", async () => {
    const session = new PairingSession({
      createTransport: vi.fn(),
      deviceName: "Living Room TV",
      store: {
        clear: vi.fn(async () => undefined),
        read: vi.fn(async () => undefined),
        write: vi.fn(async () => undefined),
      },
    });
    await session.initialize(undefined);
    const discovery: ServerDiscovery = {
      snapshot: () => ({
        servers: [{ id: "living-room", name: "Loomarr on media-box", url: "http://192.0.2.10:8080" }],
        status: "searching",
      }),
      start: vi.fn(),
      stop: vi.fn(),
      subscribe: () => () => undefined,
    };

    const markup = renderToStaticMarkup(
      <LoomarrProvider theme="dark">
        <PairingShell
          allowServerEntry
          density="tv"
          discovery={discovery}
          discoveryForeground
          renderPaired={() => null}
          session={session}
        />
      </LoomarrProvider>,
    );

    expect(markup).toContain("Find your Loomarr server");
    expect(markup).toContain("Connect to Loomarr on media-box");
    expect(markup).toContain("http://192.0.2.10:8080");
    expect(markup).toContain("Enter address manually");
    expect(markup).not.toContain("EXPO_PUBLIC_LOOMARR_URL");
  });

  it("browses only while the unpaired connection screen is foregrounded", async () => {
    const session = new PairingSession({
      createTransport: vi.fn(),
      deviceName: "Living Room TV",
      store: {
        clear: vi.fn(async () => undefined),
        read: vi.fn(async () => undefined),
        write: vi.fn(async () => undefined),
      },
    });
    await session.initialize(undefined);
    const discoverySnapshot = { servers: [], status: "searching" as const };
    const discovery: ServerDiscovery = {
      snapshot: () => discoverySnapshot,
      start: vi.fn(),
      stop: vi.fn(),
      subscribe: () => () => undefined,
    };
    const container = createTestContainer();
    const root = createRoot(container);
    const render = (discoveryForeground: boolean) =>
      root.render(
        <LoomarrProvider theme="dark">
          <PairingShell
            allowServerEntry
            density="tv"
            discovery={discovery}
            discoveryForeground={discoveryForeground}
            renderPaired={() => null}
            session={session}
          />
        </LoomarrProvider>,
      );

    act(() => render(true));
    expect(discovery.start).toHaveBeenCalledOnce();
    expect(discovery.stop).not.toHaveBeenCalled();

    act(() => render(false));
    expect(discovery.stop).toHaveBeenCalled();

    act(() => root.unmount());
  });

  it("keeps manual address entry available when automatic discovery times out", async () => {
    const session = new PairingSession({
      createTransport: vi.fn(),
      deviceName: "Living Room TV",
      store: {
        clear: vi.fn(async () => undefined),
        read: vi.fn(async () => undefined),
        write: vi.fn(async () => undefined),
      },
    });
    await session.initialize(undefined);
    const discovery: ServerDiscovery = {
      snapshot: () => ({
        error: "Couldn't find a Loomarr server. You can still enter the address manually.",
        servers: [],
        status: "unavailable",
      }),
      start: vi.fn(),
      stop: vi.fn(),
      subscribe: () => () => undefined,
    };

    const markup = renderToStaticMarkup(
      <LoomarrProvider theme="dark">
        <PairingShell
          allowServerEntry
          density="tv"
          discovery={discovery}
          discoveryForeground
          renderPaired={() => null}
          session={session}
        />
      </LoomarrProvider>,
    );

    expect(markup).toContain("Couldn&#x27;t find a Loomarr server");
    expect(markup).toContain("Enter address manually");
  });

  it("keeps the Loomarr mark in the living-room QR code", async () => {
    const { pairing, session } = await awaitingSession();

    const markup = renderToStaticMarkup(
      <LoomarrProvider theme="dark">
        <PairingShell density="tv" renderPaired={() => null} session={session} />
      </LoomarrProvider>,
    );

    // The screen-level brand mark, QR matrix, and protected QR centre mark are separate SVGs.
    expect(markup.match(/<svg/g)).toHaveLength(3);

    session.stop();
    await pairing;
  });
});
