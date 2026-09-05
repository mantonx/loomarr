import { describe, expect, it, vi } from "vitest";

import { createServerVersionSource } from "./system-version";

describe("server version source", () => {
  it("uses the generated route and marks modified builds", async () => {
    const request = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ dirty: true, ready: true, version: "v0.4.2" }), {
        status: 200,
      }),
    );
    const signal = new AbortController().signal;

    await expect(createServerVersionSource(request).load(signal)).resolves.toBe("v0.4.2 (modified)");
    expect(request).toHaveBeenCalledWith("/v1/system/version", { method: "GET", signal });
  });

  it("keeps optional identity failure separate from channel availability", async () => {
    const request = vi.fn().mockResolvedValue(new Response(null, { status: 503 }));
    await expect(createServerVersionSource(request).load(new AbortController().signal)).rejects.toThrow(
      "Couldn't read the server version (503).",
    );
  });
});
