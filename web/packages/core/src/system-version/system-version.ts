import { getSystemVersionUrl } from "@loomarr/api/endpoints/system";
import type { SystemVersionOutputBody } from "@loomarr/api/models/systemVersionOutputBody";

interface ServerVersionSource {
  load: (signal: AbortSignal) => Promise<string>;
}

const createServerVersionSource = (request: typeof globalThis.fetch): ServerVersionSource => ({
  load: async (signal) => {
    const response = await request(getSystemVersionUrl(), { method: "GET", signal });
    if (!response.ok) throw new Error(`Couldn't read the server version (${response.status}).`);
    const version = (await response.json()) as SystemVersionOutputBody;
    return `${version.version}${version.dirty ? " (modified)" : ""}`;
  },
});

export type { ServerVersionSource };
export { createServerVersionSource };
