import { describe, expect, it, vi } from "vitest";
import {
  createAuthenticatedFetch,
  createPairingCredentialStore,
  normalizeServerUrl,
  PairingHttpError,
  PairingSession,
  pairingLifetimeSeconds,
} from "./pairing";
import type { PairingCredential, PairingPoll, PairingTransport } from "./pairing.type";

const memoryStore = (credential?: PairingCredential) => ({
  clear: vi.fn(async () => {
    credential = undefined;
  }),
  read: vi.fn(async () => credential),
  write: vi.fn(async (next: PairingCredential) => {
    credential = next;
  }),
});

describe("pairing contract", () => {
  it("returns a failed attempt to clean server selection without retaining a code", async () => {
    const session = new PairingSession({
      createTransport: () => ({
        poll: vi.fn(),
        start: vi.fn(async () => {
          throw new Error("unreachable");
        }),
      }),
      deviceName: "Living room",
      store: memoryStore(),
    });
    await session.pair("http://loomarr.local:8080");
    expect(session.snapshot()).toMatchObject({ status: "failed" });

    session.chooseServer();

    expect(session.snapshot()).toEqual({ status: "needs-server" });
  });
  it("accepts only origin-like http addresses", () => {
    expect(normalizeServerUrl(" https://loomarr.media/// ")).toBe("https://loomarr.media");
    expect(normalizeServerUrl("https://user:secret@loomarr.media")).toBeUndefined();
    expect(normalizeServerUrl("file:///tmp/loomarr")).toBeUndefined();
    expect(normalizeServerUrl("loomarr.media")).toBeUndefined();
  });
  it("uses the server clock for the countdown and fails to the bounded TTL", () => {
    expect(pairingLifetimeSeconds("2026-08-24T12:10:00Z", "Sun, 24 Aug 2026 12:00:00 GMT")).toBe(600);
    expect(pairingLifetimeSeconds("bad", undefined)).toBe(600);
  });
  it("drops corrupt stored credentials instead of exposing a token-shaped partial value", async () => {
    let value: string | null = JSON.stringify({ serverUrl: "https://loomarr.media", token: "" });
    const store = createPairingCredentialStore({
      deleteItem: vi.fn(async () => {
        value = null;
      }),
      getItem: vi.fn(async () => value),
      setItem: vi.fn(async (_key, next) => {
        value = next;
      }),
    });
    expect(await store.read()).toBeUndefined();
    expect(value).toBeNull();
  });
  it("keeps device credentials on the paired origin and revokes only on an authoritative 401", async () => {
    const credential = {
      deviceName: "Shield",
      serverUrl: "https://loomarr.media/household",
      token: "secret",
    };
    const revoked = vi.fn(async () => {});
    const fetcher = vi.fn(async () => new Response(null, { status: 401 }));
    const authenticatedFetch = createAuthenticatedFetch(credential, revoked, fetcher);

    await authenticatedFetch("/v1/guide", { method: "POST" });

    expect(fetcher).toHaveBeenCalledOnce();
    const calls = fetcher.mock.calls as unknown as Array<[RequestInfo | URL, RequestInit | undefined]>;
    const [url, init] = calls[0] ?? [];
    expect(url).toBe("https://loomarr.media/household/v1/guide");
    expect(new Headers(init?.headers).get("Authorization")).toBe("Bearer secret");
    expect(new Headers(init?.headers).get("X-Loomarr-Csrf")).toBe("1");
    expect(revoked).toHaveBeenCalledOnce();

    await expect(authenticatedFetch("https://tracker.example/collect")).rejects.toThrow(
      "cannot leave the paired server origin",
    );
    expect(fetcher).toHaveBeenCalledOnce();
  });
  it("does not revoke a device for an accepted or unavailable request", async () => {
    const credential = { deviceName: "iPhone", serverUrl: "https://loomarr.media", token: "secret" };
    const revoked = vi.fn(async () => {});
    const acceptedFetch = createAuthenticatedFetch(
      credential,
      revoked,
      vi.fn(async () => new Response(null, { status: 503 })),
    );
    await acceptedFetch("/v1/guide");
    expect(revoked).not.toHaveBeenCalled();

    const unavailableFetch = createAuthenticatedFetch(
      credential,
      revoked,
      vi.fn(async () => {
        throw new Error("offline");
      }),
    );
    await expect(unavailableFetch("/v1/guide")).rejects.toThrow("offline");
    expect(revoked).not.toHaveBeenCalled();
  });
  it("preserves an authenticated Request object after validating its origin", async () => {
    const credential = { deviceName: "iPhone", serverUrl: "https://loomarr.media", token: "secret" };
    const request = new Request("https://loomarr.media/v1/guide", { method: "POST" });
    const fetcher = vi.fn(async () => new Response(null, { status: 204 }));

    await createAuthenticatedFetch(credential, vi.fn(), fetcher)(request);

    const calls = fetcher.mock.calls as unknown as Array<[RequestInfo | URL, RequestInit | undefined]>;
    expect(calls[0]?.[0]).toBe(request);
    expect(new Headers(calls[0]?.[1]?.headers).get("Authorization")).toBe("Bearer secret");
    expect(new Headers(calls[0]?.[1]?.headers).get("X-Loomarr-Csrf")).toBe("1");
  });
  it("clears only an authoritatively rejected saved credential", async () => {
    const credential = { deviceName: "Shield", serverUrl: "https://loomarr.media", token: "revoked" };
    const store = memoryStore(credential);
    const session = new PairingSession({
      createTransport: () => {
        throw new Error("must not start a pairing");
      },
      deviceName: "Shield",
      store,
      validateCredential: vi.fn(async () => false),
    });
    await session.initialize(undefined);
    expect(store.clear).toHaveBeenCalledOnce();
    expect(session.snapshot()).toEqual({ serverUrl: credential.serverUrl, status: "revoked" });
  });
  it("revokes the server credential before clearing a deliberate disconnect", async () => {
    const credential = { deviceName: "Shield", serverUrl: "https://loomarr.media", token: "secret" };
    const store = memoryStore(credential);
    const revokeCredential = vi.fn(async () => {});
    const session = new PairingSession({
      createTransport: () => {
        throw new Error("must not start a pairing");
      },
      deviceName: "Shield",
      revokeCredential,
      store,
      validateCredential: vi.fn(async () => true),
    });
    await session.initialize(undefined);
    await session.disconnect();

    expect(revokeCredential).toHaveBeenCalledWith(credential, expect.any(AbortSignal));
    expect(store.clear).toHaveBeenCalledOnce();
    expect(session.snapshot()).toEqual({ serverUrl: credential.serverUrl, status: "revoked" });
  });
  it("retains the credential when deliberate server revocation fails", async () => {
    const credential = { deviceName: "Shield", serverUrl: "https://loomarr.media", token: "secret" };
    const store = memoryStore(credential);
    const session = new PairingSession({
      createTransport: () => {
        throw new Error("must not start a pairing");
      },
      deviceName: "Shield",
      revokeCredential: vi.fn(async () => {
        throw new PairingHttpError(503, "Server unavailable");
      }),
      store,
      validateCredential: vi.fn(async () => true),
    });
    await session.initialize(undefined);

    await expect(session.disconnect()).rejects.toThrow("Server unavailable");
    expect(store.clear).not.toHaveBeenCalled();
    expect(session.snapshot()).toEqual({ ...credential, status: "paired" });
  });
  it("clears an already-unauthorized credential during deliberate disconnect", async () => {
    const credential = { deviceName: "Shield", serverUrl: "https://loomarr.media", token: "dead" };
    const store = memoryStore(credential);
    const session = new PairingSession({
      createTransport: () => {
        throw new Error("must not start a pairing");
      },
      deviceName: "Shield",
      revokeCredential: vi.fn(async () => {
        throw new PairingHttpError(401, "Already revoked");
      }),
      store,
      validateCredential: vi.fn(async () => true),
    });
    await session.initialize(undefined);
    await session.disconnect();

    expect(store.clear).toHaveBeenCalledOnce();
    expect(session.snapshot()).toEqual({ serverUrl: credential.serverUrl, status: "revoked" });
  });
  it("persists the credential before publishing paired", async () => {
    const events: string[] = [];
    const store = memoryStore();
    store.write.mockImplementationOnce(async () => {
      events.push("stored");
    });
    const outcomes: PairingPoll[] = [
      { status: "pending" },
      { body: { deviceName: "Living room", token: "secret" }, status: "paired" },
    ];
    const transport: PairingTransport = {
      poll: vi.fn(async (): Promise<PairingPoll> => outcomes.shift() ?? { status: "pending" }),
      start: vi.fn(async () => ({
        body: {
          deviceCode: "device-secret",
          expiresAt: "2026-08-24T12:10:00Z",
          interval: 5,
          userCode: "BCDF-GHJK",
        },
        serverDate: "Sun, 24 Aug 2026 12:00:00 GMT",
      })),
    };
    const session = new PairingSession({
      createTransport: () => transport,
      deviceName: "Shield",
      now: () => 1_000,
      sleep: async () => {},
      store,
    });
    session.subscribe((state) => {
      events.push(state.status);
    });
    await session.initialize("https://loomarr.media/");
    expect(events).toEqual(["loading", "awaiting-approval", "stored", "paired"]);
    expect(session.snapshot()).toEqual({
      deviceName: "Living room",
      serverUrl: "https://loomarr.media",
      status: "paired",
      token: "secret",
    });
  });
  it("pairs a clean install again after an authoritative 401 revokes its credential", async () => {
    const store = memoryStore();
    let pairingAttempt = 0;
    const transport: PairingTransport = {
      poll: vi.fn(
        async (): Promise<PairingPoll> => ({
          body: { deviceName: "Living room", token: `token-${pairingAttempt}` },
          status: "paired",
        }),
      ),
      start: vi.fn(async () => {
        pairingAttempt += 1;
        return {
          body: {
            deviceCode: `device-${pairingAttempt}`,
            expiresAt: "2026-08-24T12:10:00Z",
            interval: 1,
            userCode: `CODE-${pairingAttempt}`,
          },
          serverDate: "Sun, 24 Aug 2026 12:00:00 GMT",
        };
      }),
    };
    const session = new PairingSession({
      createTransport: () => transport,
      deviceName: "Loomarr TV",
      sleep: async () => {},
      store,
    });

    await session.initialize("https://loomarr.media");
    const firstCredential = session.snapshot();
    expect(firstCredential).toEqual({
      deviceName: "Living room",
      serverUrl: "https://loomarr.media",
      status: "paired",
      token: "token-1",
    });
    if (firstCredential.status !== "paired") throw new Error("expected the first fresh pairing");

    await createAuthenticatedFetch(
      firstCredential,
      () => session.revoked(),
      vi.fn(async () => new Response(null, { status: 401 })),
    )("/v1/guide");
    expect(store.clear).toHaveBeenCalledOnce();
    expect(session.snapshot()).toEqual({ serverUrl: "https://loomarr.media", status: "revoked" });

    await session.pair("https://loomarr.media");
    expect(transport.start).toHaveBeenCalledTimes(2);
    expect(store.write).toHaveBeenCalledTimes(2);
    expect(session.snapshot()).toEqual({
      deviceName: "Living room",
      serverUrl: "https://loomarr.media",
      status: "paired",
      token: "token-2",
    });
  });
  it("mints a fresh code after expiry and clears a revoked credential", async () => {
    const store = memoryStore();
    let starts = 0;
    const transport: PairingTransport = {
      poll: vi.fn(
        async (): Promise<PairingPoll> =>
          starts === 1
            ? { status: "expired" }
            : { body: { deviceName: "Shield", token: "token" }, status: "paired" },
      ),
      start: vi.fn(async () => {
        starts += 1;
        return {
          body: { deviceCode: `secret-${starts}`, expiresAt: "bad", interval: 1, userCode: `CODE-${starts}` },
          serverDate: undefined,
        };
      }),
    };
    const session = new PairingSession({
      createTransport: () => transport,
      deviceName: "Shield",
      sleep: async () => {},
      store,
    });
    await session.pair("https://loomarr.media");
    await session.revoked();
    expect(transport.start).toHaveBeenCalledTimes(2);
    expect(store.clear).toHaveBeenCalledOnce();
    expect(session.snapshot()).toEqual({ serverUrl: "https://loomarr.media", status: "revoked" });
  });
});
