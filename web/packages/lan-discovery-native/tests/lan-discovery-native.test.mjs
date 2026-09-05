import { afterEach, describe, expect, it, vi } from "vitest";

const native = vi.hoisted(() => ({
  callbacks: new Map(),
  listeners: new Map(),
  removers: new Map(),
  start: vi.fn(),
  stop: vi.fn(),
}));

vi.mock("react-native", () => ({
  NativeEventEmitter: class {
    addListener(event, listener) {
      const remove = vi.fn(() => native.listeners.delete(event));
      native.callbacks.set(event, listener);
      native.listeners.set(event, listener);
      native.removers.set(event, remove);
      return { remove };
    }
  },
  NativeModules: { LoomarrLanDiscovery: { start: native.start, stop: native.stop } },
  Platform: { OS: "android" },
}));

import { createNativeServerDiscovery } from "../index.ts";

const emit = (event, payload) => native.listeners.get(event)?.(payload);

describe("native LAN server discovery", () => {
  afterEach(() => {
    vi.useRealTimers();
    native.start.mockClear();
    native.stop.mockClear();
    native.listeners.clear();
    native.callbacks.clear();
    native.removers.clear();
  });

  it("ends an empty bounded search with a manual-address fallback and discards late events", () => {
    vi.useFakeTimers();
    const discovery = createNativeServerDiscovery();
    const changes = vi.fn();
    discovery.subscribe(changes);

    discovery.start();
    vi.advanceTimersByTime(29_999);
    expect(discovery.snapshot().status).toBe("searching");
    vi.advanceTimersByTime(1);

    expect(discovery.snapshot()).toMatchObject({ status: "unavailable", servers: [] });
    expect(discovery.snapshot().error).toContain("enter the address manually");
    emit("loomarrDiscoveryFound", { id: "late", name: "Late", protocol: "1", url: "http://late.local" });
    expect(discovery.snapshot().servers).toEqual([]);
    expect(changes).toHaveBeenCalledTimes(2);
  });

  it("ends a bounded search idle when it retains discovered choices without a false error", () => {
    vi.useFakeTimers();
    const discovery = createNativeServerDiscovery();

    discovery.start();
    emit("loomarrDiscoveryFound", {
      id: "server",
      name: "Loomarr",
      protocol: "1",
      url: "http://loomarr.local",
    });
    emit("loomarrDiscoveryFound", {
      id: "server",
      name: "Loomarr",
      protocol: "1",
      url: "http://loomarr.local",
    });
    vi.advanceTimersByTime(29_999);
    expect(discovery.snapshot().status).toBe("searching");
    vi.advanceTimersByTime(1);

    expect(discovery.snapshot()).toMatchObject({ status: "idle", servers: [{ id: "server" }] });
    expect(discovery.snapshot().error).toBeUndefined();
    expect(discovery.snapshot().servers).toEqual([
      { id: "server", name: "Loomarr", url: "http://loomarr.local" },
    ]);
  });

  it("de-duplicates updates, preserves IPv4 and IPv6 URLs, and removes lost services", () => {
    const discovery = createNativeServerDiscovery();
    const changes = vi.fn();
    discovery.subscribe(changes);

    discovery.start();
    emit("loomarrDiscoveryFound", {
      id: "ipv6",
      name: "Upstairs",
      protocol: "1",
      url: "https://[fe80::1]:8443",
    });
    emit("loomarrDiscoveryFound", {
      id: "ipv4",
      name: "Downstairs",
      protocol: "1",
      url: "http://192.168.1.20:8080",
    });
    emit("loomarrDiscoveryFound", {
      id: "ipv4",
      name: "Downstairs",
      protocol: "1",
      url: "http://192.168.1.20:8080",
    });
    emit("loomarrDiscoveryFound", {
      id: "ipv4",
      name: "Den",
      protocol: "1",
      url: "http://192.168.1.21:8080",
    });
    emit("loomarrDiscoveryFound", {
      id: "udp-copy",
      name: "Den via container",
      protocol: "1",
      url: "http://192.168.1.21:8080/",
    });

    expect(discovery.snapshot().servers).toEqual([
      { id: "ipv4", name: "Den", url: "http://192.168.1.21:8080" },
      { id: "ipv6", name: "Upstairs", url: "https://[fe80::1]:8443" },
    ]);
    expect(changes).toHaveBeenCalledTimes(4);

    emit("loomarrDiscoveryLost", { id: "ipv6" });
    expect(discovery.snapshot().servers).toEqual([
      { id: "ipv4", name: "Den", url: "http://192.168.1.21:8080" },
    ]);

    emit("loomarrDiscoveryLost", { id: "ipv4" });
    expect(discovery.snapshot().servers).toEqual([
      { id: "udp-copy", name: "Den via container", url: "http://192.168.1.21:8080" },
    ]);
  });

  it("stops native browsing and preserves resolved choices after a discovery error", () => {
    const discovery = createNativeServerDiscovery();

    discovery.start();
    emit("loomarrDiscoveryFound", {
      id: "server",
      name: "Loomarr",
      protocol: "1",
      url: "http://loomarr.local",
    });
    emit("loomarrDiscoveryError", { code: 3 });

    expect(native.stop).toHaveBeenCalledTimes(1);
    expect(discovery.snapshot()).toMatchObject({
      status: "unavailable",
      servers: [{ id: "server", name: "Loomarr", url: "http://loomarr.local" }],
    });
    expect(discovery.snapshot().error).toContain("enter the address manually");
  });

  it("stops a browse that reports a native error synchronously during startup", () => {
    vi.useFakeTimers();
    const discovery = createNativeServerDiscovery();
    native.start.mockImplementationOnce(() => emit("loomarrDiscoveryError", { code: 3 }));

    discovery.start();

    expect(native.stop).toHaveBeenCalledTimes(1);
    expect(discovery.snapshot()).toMatchObject({ status: "unavailable", servers: [] });
    expect(vi.getTimerCount()).toBe(0);
  });

  it("cleans up listeners and prior timeouts across stop and restart", () => {
    vi.useFakeTimers();
    const discovery = createNativeServerDiscovery();

    discovery.start();
    const oldFound = native.listeners.get("loomarrDiscoveryFound");
    discovery.stop();
    discovery.start();
    oldFound?.({ id: "old", name: "Old", protocol: "1", url: "http://old.local" });
    vi.advanceTimersByTime(30_000);

    expect(native.stop).toHaveBeenCalledTimes(2);
    expect(native.removers.get("loomarrDiscoveryFound")).toHaveBeenCalled();
    expect(discovery.snapshot()).toMatchObject({ status: "unavailable", servers: [] });
  });

  it("invalidates callbacks captured before native startup fails", () => {
    const discovery = createNativeServerDiscovery();
    const changes = vi.fn();
    discovery.subscribe(changes);
    native.start.mockImplementationOnce(() => {
      throw new Error("native startup failed");
    });

    discovery.start();
    const failedFound = native.callbacks.get("loomarrDiscoveryFound");
    failedFound?.({ id: "late", name: "Late", protocol: "1", url: "http://late.local" });

    expect(discovery.snapshot()).toMatchObject({ status: "unavailable", servers: [] });
    expect(discovery.snapshot().error).toContain("enter the address manually");
    expect(changes).toHaveBeenCalledTimes(2);
  });
});
