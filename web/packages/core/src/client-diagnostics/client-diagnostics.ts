import type { ClientBatch } from "@loomarr/api/models/clientBatch";
import type { ClientObservation as GeneratedClientObservation } from "@loomarr/api/models/clientObservation";

type ClientObservation = Omit<GeneratedClientObservation, "occurredAt"> & { occurredAt?: number };
type AcceptedObservation = GeneratedClientObservation;
type SendBatch = (events: AcceptedObservation[]) => Promise<void>;
type ClientDiagnosticsIdentity = Pick<ClientBatch, "clientVersion" | "platform" | "source">;

const QUEUE_LIMIT = 100;
const BATCH_LIMIT = 20;
const FLUSH_MS = 2_000;
const errorEvents = new Set<GeneratedClientObservation["event"]>([
  "client.error_boundary",
  "client.unhandled_error",
  "client.api_failed",
  "player.media_error",
]);

const defaultIdentity: ClientDiagnosticsIdentity = {
  clientVersion: "embedded",
  platform: "unknown_web",
  source: "web",
};

const boundObservations = (observations: AcceptedObservation[]): AcceptedObservation[] => {
  const bounded = [...observations];
  while (bounded.length > QUEUE_LIMIT) {
    const routine = bounded.findIndex(({ event }) => !errorEvents.has(event));
    bounded.splice(routine >= 0 ? routine : 0, 1);
  }
  return bounded;
};

class ClientDiagnosticsReporter {
  private readonly queue: AcceptedObservation[] = [];
  private timer?: ReturnType<typeof setTimeout>;
  private sending = false;
  private disposed = false;
  private identity: ClientDiagnosticsIdentity;

  constructor(
    private readonly sendBatch: SendBatch,
    identity: ClientDiagnosticsIdentity = defaultIdentity,
  ) {
    this.identity = { ...identity, clientVersion: identity.clientVersion.slice(0, 64) };
  }

  setVersion(version: string | undefined) {
    if (version) this.identity = { ...this.identity, clientVersion: version.slice(0, 64) };
  }

  record(observation: ClientObservation) {
    if (this.disposed) return;
    const accepted = { ...observation, occurredAt: observation.occurredAt ?? Date.now() };
    this.replaceQueue([...this.queue, accepted]);
    this.schedule();
  }

  async flush() {
    if (this.sending || this.queue.length === 0) return;
    this.sending = true;
    const batch = this.queue.splice(0, BATCH_LIMIT);
    try {
      await this.sendBatch(batch);
    } catch {
      this.replaceQueue([...batch, ...this.queue]);
    } finally {
      this.sending = false;
      if (this.queue.length > 0 && !this.disposed) this.schedule();
    }
  }

  dispose() {
    this.disposed = true;
    if (this.timer !== undefined) clearTimeout(this.timer);
    this.timer = undefined;
  }

  wireBatch(events: AcceptedObservation[]): ClientBatch {
    return { ...this.identity, events };
  }

  private replaceQueue(observations: AcceptedObservation[]) {
    this.queue.splice(0, this.queue.length, ...boundObservations(observations));
  }

  private schedule() {
    if (this.disposed || this.timer !== undefined) return;
    this.timer = setTimeout(() => {
      this.timer = undefined;
      void this.flush();
    }, FLUSH_MS);
  }
}

export type { AcceptedObservation, ClientDiagnosticsIdentity, ClientObservation, SendBatch };
export { ClientDiagnosticsReporter };
