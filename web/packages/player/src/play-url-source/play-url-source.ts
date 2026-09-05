import { getChannelPlayUrlUrl, getListChannelsUrl } from "@loomarr/api/endpoints/channels";
import type { ListChannelsOutputBody } from "@loomarr/api/models/listChannelsOutputBody";
import type { PlayURLOutputBody } from "@loomarr/api/models/playURLOutputBody";
import type { PlayerChannel, PlayerSourcePort } from "../player-source";

interface PlayUrlSourceOptions {
  /** Normalized URL of the paired Loomarr server. */
  baseUrl: string;
  /** Authenticated request function owned by the active client session. */
  fetch: typeof globalThis.fetch;
}

interface ChannelCatalogPort {
  list: (signal: AbortSignal) => Promise<readonly PlayerChannel[]>;
}

const trimLeadingSlashes = (value: string) => {
  let start = 0;
  while (start < value.length && value.charCodeAt(start) === 47) start += 1;
  return value.slice(start);
};

const trimTrailingSlashes = (value: string) => {
  let end = value.length;
  while (end > 0 && value.charCodeAt(end - 1) === 47) end -= 1;
  return value.slice(0, end);
};

const resolveStreamUrl = (
  baseUrl: string,
  response: Pick<PlayURLOutputBody, "relativeUrl" | "url">,
): string => {
  if (response.relativeUrl.trim()) {
    return `${trimTrailingSlashes(baseUrl)}/${trimLeadingSlashes(response.relativeUrl)}`;
  }
  if (response.url.trim()) return response.url;
  throw new Error("This Loomarr returned no stream address for the channel.");
};

const createPlayUrlSourcePort = ({ baseUrl, fetch: request }: PlayUrlSourceOptions): PlayerSourcePort => ({
  mint: async (channel, profile, signal) => {
    const response = await request(getChannelPlayUrlUrl(channel.id), {
      body: JSON.stringify(profile),
      headers: { "Content-Type": "application/json" },
      method: "POST",
      signal,
    });
    if (!response.ok) throw new Error(`Couldn't mint a play URL (${response.status}).`);

    const body = (await response.json()) as PlayURLOutputBody;
    const expiry = Date.parse(body.expiresAt);
    return {
      expiresAt: Number.isFinite(expiry) ? expiry : undefined,
      uri: resolveStreamUrl(baseUrl, body),
    };
  },
});

const createChannelCatalogPort = (request: typeof globalThis.fetch): ChannelCatalogPort => ({
  list: async (signal) => {
    const response = await request(getListChannelsUrl(), { method: "GET", signal });
    if (!response.ok) throw new Error(`Couldn't load channels (${response.status}).`);
    const body = (await response.json()) as ListChannelsOutputBody;
    return body.channels.map(({ id, inAppPlayable, name, number }) => ({
      id,
      inAppPlayable,
      name,
      number,
    }));
  },
});

export type { ChannelCatalogPort, PlayUrlSourceOptions };
export { createChannelCatalogPort, createPlayUrlSourcePort, resolveStreamUrl };
