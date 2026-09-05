import { toProblem } from "@loomarr/api/mutator";
import {
  type BrowserHlsPlayerOptions,
  type BrowserPlayerStatus,
  type UseBrowserHlsPlayer,
  useBrowserHlsPlayer,
} from "@loomarr/player/browser";
import { useCallback, useMemo } from "react";
import { clientDiagnostics } from "@/diagnostics/client-reporter";
import { mintChannelPlaySource } from "../channel-play-url";
import { markTunePhase, type TuneAttempt } from "../tuner-timing";

type PlayerStatus = BrowserPlayerStatus;
type UseHlsPlayer = UseBrowserHlsPlayer;

const useHlsPlayer = (channelId: string, attempt?: TuneAttempt): UseHlsPlayer => {
  const mintSource = useCallback(
    (signal: AbortSignal) => mintChannelPlaySource(channelId, signal),
    [channelId],
  );
  const errorMessage = useCallback((error: unknown) => {
    const problem = toProblem(error);
    return problem.detail ?? problem.title ?? "Couldn't start this channel.";
  }, []);
  const recordDiagnostic = useCallback(
    (observation: Parameters<typeof clientDiagnostics.record>[0]) => clientDiagnostics.record(observation),
    [],
  );
  const browserAttempt = useMemo<BrowserHlsPlayerOptions["attempt"]>(
    () =>
      attempt
        ? {
            markPhase: (phase) => markTunePhase(attempt, phase),
            playURL: attempt.playURL,
          }
        : undefined,
    [attempt],
  );

  return useBrowserHlsPlayer({
    attempt: browserAttempt,
    channelId,
    errorMessage,
    mintSource,
    recordDiagnostic,
  });
};

export type { PlayerStatus, UseHlsPlayer };
export { useHlsPlayer };
