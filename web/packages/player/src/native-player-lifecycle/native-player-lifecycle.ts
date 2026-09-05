import type { PlayerController } from "../player-controller";

interface NativeLifecycleTransport {
  resume: () => void;
  suspend: () => void;
}

interface NativePlayerLifecycleOptions {
  controller: Pick<PlayerController, "getSnapshot" | "pause" | "retry">;
  /** Refreshes and reconciles the authoritative playable Channel catalog. */
  refresh: () => Promise<void>;
  transport: NativeLifecycleTransport;
}

interface NativePlayerLifecycle {
  enterBackground: () => void;
  enterForeground: () => Promise<void>;
}

const createNativePlayerLifecycle = ({
  controller,
  refresh,
  transport,
}: NativePlayerLifecycleOptions): NativePlayerLifecycle => {
  let transition = 0;

  return {
    enterBackground: () => {
      transition += 1;
      controller.pause();
      transport.suspend();
    },
    enterForeground: async () => {
      transition += 1;
      const currentTransition = transition;
      transport.resume();
      try {
        await refresh();
      } catch (error) {
        if (currentTransition !== transition) return;
        throw error;
      }
      if (currentTransition !== transition) return;
      const current = controller.getSnapshot();
      if (current.channel && current.status === "paused") await controller.retry();
    },
  };
};

export type { NativeLifecycleTransport, NativePlayerLifecycle, NativePlayerLifecycleOptions };
export { createNativePlayerLifecycle };
