import type { EventStreamFactory, EventStreamMessage, EventStreamPort } from "@loomarr/core/events";

interface NativeEventRequest {
  abort: () => void;
  onerror: ((event: ProgressEvent<EventTarget>) => unknown) | null;
  onreadystatechange: ((event: Event) => unknown) | null;
  open: (method: string, url: string, async: boolean) => void;
  readyState: number;
  responseText: string;
  send: () => void;
  setRequestHeader: (name: string, value: string) => void;
  status: number;
}

interface NativeEventStreamOptions {
  clearTimer?: (timer: ReturnType<typeof setTimeout>) => void;
  createRequest?: () => NativeEventRequest;
  headers?: Readonly<Record<string, string>>;
  onUnauthorized?: () => Promise<void> | void;
  reconnectMs?: number;
  setTimer?: (callback: () => void, delayMs: number) => ReturnType<typeof setTimeout>;
}

interface ParsedEventFrame {
  data: string;
  type: string;
}

const parseEventFrames = (raw: string): { frames: ParsedEventFrame[]; rest: string } => {
  const normalized = raw.replaceAll("\r\n", "\n").replaceAll("\r", "\n");
  const blocks = normalized.split("\n\n");
  const rest = blocks.pop() ?? "";
  const frames = blocks.flatMap((block) => {
    let type = "message";
    const data: string[] = [];
    for (const line of block.split("\n")) {
      if (line.startsWith(":")) continue;
      const separator = line.indexOf(":");
      const field = separator < 0 ? line : line.slice(0, separator);
      const value = separator < 0 ? "" : line.slice(separator + 1).replace(/^ /, "");
      if (field === "event") type = value || "message";
      else if (field === "data") data.push(value);
    }
    return data.length ? [{ data: data.join("\n"), type }] : [];
  });
  return { frames, rest };
};

const createNativeEventStream = (
  url: string,
  {
    clearTimer = clearTimeout,
    createRequest = () => new XMLHttpRequest(),
    headers = {},
    onUnauthorized,
    reconnectMs = 5_000,
    setTimer = setTimeout,
  }: NativeEventStreamOptions = {},
): EventStreamPort => {
  const listeners = new Map<string, Set<(event: EventStreamMessage) => void>>();
  let request: NativeEventRequest | undefined;
  let reconnectTimer: ReturnType<typeof setTimeout> | undefined;
  let closed = false;
  let buffer = "";
  let processedLength = 0;

  const dispatch = (type: string, data: string) => {
    for (const listener of listeners.get(type) ?? []) listener({ data });
  };
  const close = () => {
    if (closed) return;
    closed = true;
    if (reconnectTimer !== undefined) {
      clearTimer(reconnectTimer);
      reconnectTimer = undefined;
    }
    request?.abort();
    request = undefined;
  };
  const scheduleReconnect = () => {
    if (closed || reconnectTimer !== undefined) return;
    reconnectTimer = setTimer(() => {
      reconnectTimer = undefined;
      connect();
    }, reconnectMs);
  };
  const readFrames = (responseText: string) => {
    if (responseText.length < processedLength) {
      processedLength = 0;
      buffer = "";
    }
    buffer += responseText.slice(processedLength);
    processedLength = responseText.length;
    const parsed = parseEventFrames(buffer);
    buffer = parsed.rest;
    for (const frame of parsed.frames) dispatch(frame.type, frame.data);
  };
  const connect = () => {
    if (closed) return;
    buffer = "";
    processedLength = 0;
    const next = createRequest();
    request = next;
    next.open("GET", url, true);
    for (const [name, value] of Object.entries(headers)) next.setRequestHeader(name, value);
    next.setRequestHeader("Accept", "text/event-stream");
    next.setRequestHeader("Cache-Control", "no-cache");
    next.onreadystatechange = () => {
      if (closed || request !== next || (next.readyState !== 3 && next.readyState !== 4)) return;
      if (next.status >= 200 && next.status < 300) {
        readFrames(next.responseText);
        if (next.readyState === 4) scheduleReconnect();
        return;
      }
      if (next.readyState !== 4) return;
      if (next.status === 0) {
        scheduleReconnect();
        return;
      }
      if (next.status === 401 || next.status === 403) {
        close();
        void onUnauthorized?.();
        return;
      }
      scheduleReconnect();
    };
    next.onerror = scheduleReconnect;
    next.send();
  };

  reconnectTimer = setTimer(() => {
    reconnectTimer = undefined;
    connect();
  }, 0);
  return {
    addEventListener: (type, listener) => {
      const typeListeners = listeners.get(type) ?? new Set();
      typeListeners.add(listener);
      listeners.set(type, typeListeners);
    },
    close,
  };
};

const createNativeEventStreamFactory =
  (options: NativeEventStreamOptions): EventStreamFactory =>
  (url) =>
    createNativeEventStream(url, options);

export type { NativeEventRequest, NativeEventStreamOptions, ParsedEventFrame };
export { createNativeEventStream, createNativeEventStreamFactory, parseEventFrames };
