import { QueryClient } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import { invalidateByPrefix, openEventStream } from "./events";
import type { EventStreamMessage } from "./events.type";

describe("invalidateByPrefix", () => {
  it("invalidates only queries whose URL key matches the prefix", async () => {
    const qc = new QueryClient();
    // Seed three cached queries keyed like orval's fetch client ([url, params]).
    await qc.prefetchQuery({ queryKey: ["/v1/titles", { state: "wanted" }], queryFn: () => "t" });
    await qc.prefetchQuery({ queryKey: ["/v1/channels"], queryFn: () => "c" });

    invalidateByPrefix(qc, "/v1/titles");

    const titles = qc.getQueryState(["/v1/titles", { state: "wanted" }]);
    const channels = qc.getQueryState(["/v1/channels"]);
    expect(titles?.isInvalidated).toBe(true);
    expect(channels?.isInvalidated).toBe(false);
  });
});

describe("openEventStream", () => {
  it("uses an injected platform stream and ignores malformed latency frames", () => {
    const listeners = new Map<string, (event: EventStreamMessage) => void>();
    const close = vi.fn();
    const onChannel = vi.fn();
    const stop = openEventStream({ onChannel }, "http://loomarr.test/v1/events", (url) => {
      expect(url).toBe("http://loomarr.test/v1/events");
      return {
        addEventListener: (type, listener) => listeners.set(type, listener),
        close,
      };
    });

    listeners.get("channel")?.({ data: '{"channelId":"seven","status":"live"}' });
    listeners.get("channel")?.({ data: "not-json" });
    expect(onChannel).toHaveBeenCalledOnce();
    expect(onChannel).toHaveBeenCalledWith({ channelId: "seven", status: "live" });

    stop();
    expect(close).toHaveBeenCalledOnce();
  });
});
