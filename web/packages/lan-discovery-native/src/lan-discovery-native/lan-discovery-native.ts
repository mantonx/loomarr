import type {
  DiscoveredServer,
  ServerDiscovery,
  ServerDiscoverySnapshot,
} from "@loomarr/core/server-discovery";
import { NativeEventEmitter, NativeModules, Platform } from "react-native";

type NativeDiscoveryModule = {
  addListener(event: string): void;
  removeListeners(count: number): void;
  start(): void;
  stop(): void;
};

type NativeServer = DiscoveredServer & { protocol: string };
const nativeModule = NativeModules.LoomarrLanDiscovery as NativeDiscoveryModule | undefined;
const discoveryTimeoutMs = 30_000;

const createNativeServerDiscovery = (): ServerDiscovery => {
  let snapshot: ServerDiscoverySnapshot = {
    servers: [],
    status: Platform.OS === "android" && nativeModule ? "idle" : "unavailable",
  };
  const listeners = new Set<() => void>();
  let subscriptions: Array<{ remove(): void }> = [];
  let timeout: ReturnType<typeof setTimeout> | undefined;
  let generation = 0;
  let running = false;
  const serversById = new Map<string, DiscoveredServer>();
  const publish = (next: ServerDiscoverySnapshot) => {
    snapshot = next;
    for (const listener of listeners) listener();
  };
  const stop = () => {
    generation += 1;
    if (timeout !== undefined) clearTimeout(timeout);
    timeout = undefined;
    for (const subscription of subscriptions) subscription.remove();
    subscriptions = [];
    if (running) nativeModule?.stop();
    running = false;
  };
  const discoveredServers = () => {
    const byURL = new Map<string, DiscoveredServer>();
    for (const server of serversById.values()) {
      const normalizedURL = server.url.replace(/\/+$/, "");
      if (!byURL.has(normalizedURL)) byURL.set(normalizedURL, { ...server, url: normalizedURL });
    }
    return [...byURL.values()].sort((left, right) => left.name.localeCompare(right.name));
  };

  return {
    snapshot: () => snapshot,
    start() {
      if (!nativeModule || Platform.OS !== "android") {
        publish({
          error: "Automatic discovery is unavailable on this platform.",
          servers: [],
          status: "unavailable",
        });
        return;
      }
      stop();
      serversById.clear();
      const activeGeneration = ++generation;
      publish({ servers: [], status: "searching" });
      const events = new NativeEventEmitter(nativeModule);
      subscriptions = [
        events.addListener("loomarrDiscoveryFound", (server: NativeServer) => {
          if (activeGeneration !== generation) return;
          if (server.protocol !== "1" || !server.id || !server.name || !server.url) return;
          const next = { id: server.id, name: server.name, url: server.url.replace(/\/+$/, "") };
          const previous = serversById.get(server.id);
          if (previous?.name === next.name && previous.url === next.url) return;
          serversById.set(server.id, next);
          const servers = discoveredServers();
          if (
            servers.length === snapshot.servers.length &&
            servers.every((candidate, index) => {
              const current = snapshot.servers[index];
              return (
                current?.id === candidate.id &&
                current.name === candidate.name &&
                current.url === candidate.url
              );
            })
          )
            return;
          publish({
            servers,
            status: "searching",
          });
        }),
        events.addListener("loomarrDiscoveryLost", ({ id }: { id?: string }) => {
          if (activeGeneration !== generation) return;
          if (!id) return;
          serversById.delete(id);
          publish({ ...snapshot, servers: discoveredServers() });
        }),
        events.addListener("loomarrDiscoveryError", () => {
          if (activeGeneration !== generation) return;
          const servers = snapshot.servers;
          stop();
          publish({
            error: "Couldn't search this network. You can still enter the address manually.",
            servers,
            status: "unavailable",
          });
        }),
      ];
      running = true;
      try {
        nativeModule.start();
      } catch {
        stop();
        publish({
          error: "Couldn't search this network. You can still enter the address manually.",
          servers: [],
          status: "unavailable",
        });
        return;
      }
      if (activeGeneration !== generation) return;
      timeout = setTimeout(() => {
        if (activeGeneration !== generation) return;
        const servers = snapshot.servers;
        stop();
        publish(
          servers.length > 0
            ? { servers, status: "idle" }
            : {
                error: "Couldn't find a Loomarr server. You can still enter the address manually.",
                servers,
                status: "unavailable",
              },
        );
      }, discoveryTimeoutMs);
    },
    stop,
    subscribe(listener) {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
  };
};

export { createNativeServerDiscovery };
