import { vi } from "vitest";

const hlsMock = { supported: false, instances: [] as unknown[] };

class MockHls {
  static isSupported = () => hlsMock.supported;
  static Events = {
    ERROR: "error",
    FRAG_BUFFERED: "fragBuffered",
    LEVEL_UPDATED: "levelUpdated",
    MANIFEST_PARSED: "manifestParsed",
  };
  static ErrorTypes = { MEDIA_ERROR: "mediaError", NETWORK_ERROR: "networkError" };

  media: HTMLMediaElement | null = null;
  url: string | null = null;
  config: unknown;
  liveSyncPosition: number | null = 98;
  playingDate: Date | null = null;
  attachMedia = vi.fn((media: HTMLMediaElement) => {
    this.media = media;
  });
  destroy = vi.fn(() => {
    this.media = null;
  });
  loadSource = vi.fn((url: string) => {
    this.url = url;
  });
  off = vi.fn();
  on = vi.fn();
  recoverMediaError = vi.fn();
  startLoad = vi.fn();
  stopLoad = vi.fn();
  transferMedia = vi.fn(() => null);

  constructor(config: unknown) {
    this.config = config;
    hlsMock.instances.push(this);
  }
}

export { hlsMock };
export default MockHls;
