import { afterEach, describe, expect, it, vi } from "vitest";
import { ClientDiagnosticsReporter, type SendBatch } from "./client-diagnostics";

const deferred = () => {
  let reject!: (error: unknown) => void;
  const promise = new Promise<void>((_resolve, onReject) => {
    reject = onReject;
  });
  return { promise, reject };
};

afterEach(() => vi.useRealTimers());

describe("ClientDiagnosticsReporter", () => {
  it("batches a generated-contract identity without blocking the caller", async () => {
    vi.useFakeTimers();
    const send = vi.fn<SendBatch>(async () => undefined);
    const reporter = new ClientDiagnosticsReporter(send, {
      clientVersion: "0.0.1",
      platform: "shield_tv",
      source: "android_tv",
    });

    reporter.record({
      channelId: "seven",
      event: "player.ready",
      playbackSessionId: "native-1",
      transport: "native_hls",
    });
    expect(send).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(2_000);

    const events = send.mock.calls[0]?.[0] ?? [];
    expect(reporter.wireBatch(events)).toMatchObject({
      clientVersion: "0.0.1",
      events: [{ channelId: "seven", event: "player.ready", occurredAt: expect.any(Number) }],
      platform: "shield_tv",
      source: "android_tv",
    });
  });

  it("retains errors ahead of routine events when its queue is saturated", async () => {
    const send = vi.fn<SendBatch>(async () => undefined);
    const reporter = new ClientDiagnosticsReporter(send);
    reporter.record({ event: "client.unhandled_error", errorClass: "error", surface: "root" });
    for (let index = 0; index < 100; index++) {
      reporter.record({
        channelId: `channel_${index}`,
        event: "player.attached",
        playbackSessionId: "native-1",
        transport: "native_hls",
      });
    }

    for (let index = 0; index < 5; index++) await reporter.flush();
    const sent = send.mock.calls.flatMap(([events]) => events);
    expect(sent).toHaveLength(100);
    expect(sent.some(({ event }) => event === "client.unhandled_error")).toBe(true);
    reporter.dispose();
  });

  it("restores a failed batch without overflowing when new observations fill the queue", async () => {
    const first = deferred();
    const send = vi
      .fn<SendBatch>()
      .mockImplementationOnce(() => first.promise)
      .mockResolvedValue(undefined);
    const reporter = new ClientDiagnosticsReporter(send);
    reporter.record({ event: "client.api_failed", httpStatus: 502, requestId: "request_1" });
    for (let index = 0; index < 19; index++) {
      reporter.record({
        channelId: `old_${index}`,
        event: "player.attached",
        playbackSessionId: "native-1",
        transport: "native_hls",
      });
    }

    const failedFlush = reporter.flush();
    for (let index = 0; index < 100; index++) {
      reporter.record({
        channelId: `new_${index}`,
        event: "player.attached",
        playbackSessionId: "native-1",
        transport: "native_hls",
      });
    }
    first.reject(new Error("offline"));
    await failedFlush;
    for (let index = 0; index < 5; index++) await reporter.flush();

    const retried = send.mock.calls.slice(1).flatMap(([events]) => events);
    expect(retried).toHaveLength(100);
    expect(retried.some(({ event }) => event === "client.api_failed")).toBe(true);
    reporter.dispose();
  });

  it("stops accepting or scheduling observations after disposal", async () => {
    vi.useFakeTimers();
    const send = vi.fn<SendBatch>(async () => undefined);
    const reporter = new ClientDiagnosticsReporter(send);
    reporter.dispose();
    reporter.record({ event: "client.unhandled_error", errorClass: "error", surface: "root" });

    await vi.runAllTimersAsync();
    expect(send).not.toHaveBeenCalled();
  });
});
