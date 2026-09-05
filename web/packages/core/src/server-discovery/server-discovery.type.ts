type DiscoveredServer = {
  id: string;
  name: string;
  url: string;
};

type ServerDiscoverySnapshot = {
  error?: string;
  servers: readonly DiscoveredServer[];
  status: "idle" | "searching" | "unavailable";
};

type ServerDiscovery = {
  snapshot(): ServerDiscoverySnapshot;
  start(): void;
  stop(): void;
  subscribe(listener: () => void): () => void;
};

export type { DiscoveredServer, ServerDiscovery, ServerDiscoverySnapshot };
