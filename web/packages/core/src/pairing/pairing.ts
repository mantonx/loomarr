import {
  getDeviceListUrl,
  getDevicePairPollUrl,
  getDevicePairStartUrl,
  getDeviceRevokeCurrentUrl,
} from "@loomarr/api/endpoints/auth";

import type {
  PairingCredential,
  PairingPoll,
  PairingSessionOptions,
  PairingState,
  PairingTransport,
} from "./pairing.type";

const DEFAULT_PAIRING_TTL_SECONDS = 10 * 60;
const DEFAULT_POLL_SECONDS = 5;

const normalizeServerUrl = (input: string): string | undefined => {
  const value = input.trim();
  if (!value) return undefined;
  try {
    const url = new URL(value);
    if (url.protocol !== "http:" && url.protocol !== "https:") return undefined;
    if (url.username || url.password || url.search || url.hash) return undefined;
    const pathname = url.pathname.replace(/\/+$/, "");
    return `${url.protocol}//${url.host}${pathname}`;
  } catch {
    return undefined;
  }
};

const pairingLifetimeSeconds = (expiresAt: string, serverDate: string | undefined): number => {
  const expiry = Date.parse(expiresAt);
  const serverNow = serverDate ? Date.parse(serverDate) : Number.NaN;
  if (!Number.isFinite(expiry) || !Number.isFinite(serverNow)) return DEFAULT_PAIRING_TTL_SECONDS;
  return Math.max(0, Math.floor((expiry - serverNow) / 1_000));
};

const sleepWithAbort = (milliseconds: number, signal: AbortSignal) =>
  new Promise<void>((resolve, reject) => {
    const timer = setTimeout(resolve, milliseconds);
    signal.addEventListener(
      "abort",
      () => {
        clearTimeout(timer);
        reject(new Error("Pairing cancelled"));
      },
      { once: true },
    );
  });

class PairingHttpError extends Error {
  constructor(
    readonly status: number,
    message: string,
  ) {
    super(message);
    this.name = "PairingHttpError";
  }
}

type PairingFetch = typeof globalThis.fetch;
type PairingKeyValueStorage = {
  deleteItem(key: string): Promise<void>;
  getItem(key: string): Promise<string | null>;
  setItem(key: string, value: string): Promise<void>;
};
const CREDENTIAL_KEY = "loomarr.paired-device.v1";

const isCredential = (value: unknown): value is PairingCredential => {
  if (!value || typeof value !== "object") return false;
  const candidate = value as Partial<PairingCredential>;
  return (
    typeof candidate.deviceName === "string" &&
    typeof candidate.serverUrl === "string" &&
    normalizeServerUrl(candidate.serverUrl) === candidate.serverUrl &&
    typeof candidate.token === "string" &&
    candidate.token.length > 0
  );
};

const createPairingCredentialStore = (storage: PairingKeyValueStorage) => ({
  clear: () => storage.deleteItem(CREDENTIAL_KEY),
  async read() {
    const encoded = await storage.getItem(CREDENTIAL_KEY);
    if (!encoded) return undefined;
    try {
      const value: unknown = JSON.parse(encoded);
      if (isCredential(value)) return value;
    } catch {
      // Corrupt local credentials are recoverable: clear them and start a new pairing.
    }
    await storage.deleteItem(CREDENTIAL_KEY);
    return undefined;
  },
  write: (credential: PairingCredential) => storage.setItem(CREDENTIAL_KEY, JSON.stringify(credential)),
});
const readJson = async <T>(response: Response): Promise<T> => {
  const body = (await response.json()) as T;
  if (!response.ok)
    throw new PairingHttpError(response.status, `Pairing request failed (${response.status})`);
  return body;
};

const createPairingTransport = (serverUrl: string, fetcher: PairingFetch = fetch): PairingTransport => ({
  async start(deviceName, signal) {
    const response = await fetcher(`${serverUrl}${getDevicePairStartUrl()}`, {
      body: JSON.stringify({ deviceName }),
      headers: { "Content-Type": "application/json", "X-Loomarr-Csrf": "1" },
      method: "POST",
      signal,
    });
    return { body: await readJson(response), serverDate: response.headers.get("Date") ?? undefined };
  },
  async poll(deviceCode, signal): Promise<PairingPoll> {
    const response = await fetcher(`${serverUrl}${getDevicePairPollUrl()}`, {
      body: JSON.stringify({ deviceCode }),
      headers: { "Content-Type": "application/json", "X-Loomarr-Csrf": "1" },
      method: "POST",
      signal,
    });
    if (response.status === 428) return { status: "pending" };
    if (response.status === 404) return { status: "expired" };
    return { body: await readJson(response), status: "paired" };
  },
});

const validatePairingCredential = async (
  credential: PairingCredential,
  signal: AbortSignal,
  fetcher: PairingFetch = fetch,
): Promise<boolean> => {
  try {
    const response = await fetcher(`${credential.serverUrl}${getDeviceListUrl()}`, {
      headers: { Authorization: `Bearer ${credential.token}` },
      method: "GET",
      signal,
    });
    return response.status !== 401;
  } catch {
    // Network/server failures are not logout signals. Only an authoritative 401 revokes locally.
    return true;
  }
};

const revokePairingCredential = async (
  credential: PairingCredential,
  signal: AbortSignal,
  fetcher: PairingFetch = fetch,
): Promise<void> => {
  const response = await fetcher(`${credential.serverUrl}${getDeviceRevokeCurrentUrl()}`, {
    headers: {
      Authorization: `Bearer ${credential.token}`,
      "X-Loomarr-Csrf": "1",
    },
    method: "DELETE",
    signal,
  });
  if (!response.ok)
    throw new PairingHttpError(response.status, `Disconnect request failed (${response.status})`);
};

const createAuthenticatedFetch =
  (
    credential: PairingCredential,
    revoked: () => Promise<void> | void,
    fetcher: PairingFetch = fetch,
  ): PairingFetch =>
  async (input, init = {}) => {
    const rawUrl = typeof input === "string" || input instanceof URL ? input.toString() : input.url;
    const requestUrl = new URL(
      rawUrl.startsWith("/") ? `${credential.serverUrl}${rawUrl}` : rawUrl,
      `${credential.serverUrl}/`,
    );
    if (requestUrl.origin !== new URL(credential.serverUrl).origin)
      throw new Error("Authenticated Loomarr requests cannot leave the paired server origin");
    const request = typeof input === "string" || input instanceof URL ? undefined : input;
    const headers = new Headers(init.headers ?? request?.headers);
    headers.set("Authorization", `Bearer ${credential.token}`);
    const method = (init.method ?? request?.method ?? "GET").toUpperCase();
    if (method !== "GET" && method !== "HEAD") headers.set("X-Loomarr-Csrf", "1");
    // A Request already carries an absolute URL, so it can pass through after the origin check.
    // Reconstructing it as RequestInit is both unnecessary and not portable: React Native and DOM
    // intentionally expose different stream body types for that overload.
    const normalizedInput = request ?? requestUrl.toString();
    const response = await fetcher(normalizedInput, { ...init, headers, method });
    if (response.status === 401) await revoked();
    return response;
  };

class PairingSession {
  private abortController: AbortController | undefined;
  private readonly listeners = new Set<(state: PairingState) => void>();
  private state: PairingState = { status: "loading" };
  constructor(private readonly options: PairingSessionOptions) {}
  snapshot = () => this.state;
  subscribe = (listener: (state: PairingState) => void) => {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  };
  private emit(state: PairingState) {
    this.state = state;
    for (const listener of this.listeners) listener(state);
  }
  async initialize(serverUrlInput: string | undefined) {
    this.stop();
    const saved = await this.options.store.read();
    if (saved) {
      if (this.options.validateCredential) {
        const controller = new AbortController();
        this.abortController = controller;
        const valid = await this.options.validateCredential(saved, controller.signal);
        if (!valid) {
          await this.options.store.clear();
          this.emit({ serverUrl: saved.serverUrl, status: "revoked" });
          return;
        }
      }
      this.emit({ ...saved, status: "paired" });
      return;
    }
    const serverUrl = normalizeServerUrl(serverUrlInput ?? "");
    if (!serverUrl) {
      this.emit({ status: "needs-server" });
      return;
    }
    await this.begin(serverUrl);
  }
  async pair(serverUrlInput: string) {
    const serverUrl = normalizeServerUrl(serverUrlInput);
    if (!serverUrl) {
      this.emit({
        message: "Enter a complete http:// or https:// Loomarr address.",
        retryable: true,
        status: "failed",
      });
      return;
    }
    await this.begin(serverUrl);
  }
  chooseServer() {
    this.stop();
    this.emit({ status: "needs-server" });
  }
  async revoked() {
    const current = this.state;
    await this.options.store.clear();
    if (current.status === "paired") this.emit({ serverUrl: current.serverUrl, status: "revoked" });
  }
  async disconnect() {
    const current = this.state;
    if (current.status !== "paired") return;
    this.stop();
    const controller = new AbortController();
    this.abortController = controller;
    try {
      const credential: PairingCredential = {
        deviceName: current.deviceName,
        serverUrl: current.serverUrl,
        token: current.token,
      };
      await (this.options.revokeCredential ?? revokePairingCredential)(credential, controller.signal);
    } catch (error) {
      if (!(error instanceof PairingHttpError) || error.status !== 401) throw error;
    } finally {
      if (this.abortController === controller) this.abortController = undefined;
    }
    await this.options.store.clear();
    this.emit({ serverUrl: current.serverUrl, status: "revoked" });
  }
  stop() {
    this.abortController?.abort();
    this.abortController = undefined;
  }
  private async begin(serverUrl: string) {
    this.stop();
    const controller = new AbortController();
    this.abortController = controller;
    this.emit({ status: "loading" });
    const transport = this.options.createTransport(serverUrl);
    try {
      while (!controller.signal.aborted) {
        const pairing = await transport.start(this.options.deviceName, controller.signal);
        const lifetime = pairingLifetimeSeconds(pairing.body.expiresAt, pairing.serverDate);
        this.emit({
          deviceCode: pairing.body.deviceCode,
          expiresAtMs: (this.options.now ?? Date.now)() + lifetime * 1_000,
          serverUrl,
          status: "awaiting-approval",
          userCode: pairing.body.userCode,
          verificationUri: `${serverUrl.replace(/^https?:\/\//, "")}/pair`,
          verificationUriComplete: `${serverUrl}/pair?code=${encodeURIComponent(pairing.body.userCode)}`,
        });
        const outcome = await this.pollUntilSettled(
          transport,
          pairing.body.deviceCode,
          pairing.body.interval,
          controller.signal,
        );
        if (outcome.status === "expired") continue;
        const credential: PairingCredential = {
          deviceName: outcome.body.deviceName,
          serverUrl,
          token: outcome.body.token,
        };
        await this.options.store.write(credential);
        if (!controller.signal.aborted) this.emit({ ...credential, status: "paired" });
        return;
      }
    } catch (error) {
      if (controller.signal.aborted) return;
      this.emit({
        message: error instanceof Error ? error.message : "Loomarr could not be reached.",
        retryable: true,
        serverUrl,
        status: "failed",
      });
    }
  }
  private async pollUntilSettled(
    transport: PairingTransport,
    deviceCode: string,
    intervalSeconds: number,
    signal: AbortSignal,
  ): Promise<Exclude<PairingPoll, { status: "pending" }>> {
    while (true) {
      await (this.options.sleep ?? sleepWithAbort)(
        Math.max(1, intervalSeconds || DEFAULT_POLL_SECONDS) * 1_000,
        signal,
      );
      try {
        const result = await transport.poll(deviceCode, signal);
        if (result.status !== "pending") return result;
      } catch (error) {
        if (signal.aborted) throw error;
      }
    }
  }
}

export {
  createAuthenticatedFetch,
  createPairingCredentialStore,
  createPairingTransport,
  normalizeServerUrl,
  PairingHttpError,
  PairingSession,
  pairingLifetimeSeconds,
  revokePairingCredential,
  validatePairingCredential,
};
