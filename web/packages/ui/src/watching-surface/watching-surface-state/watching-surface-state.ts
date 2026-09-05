import type { PlayerSnapshot } from "@loomarr/player";

const behindLabel = (seconds: number) => {
  const rounded = Math.max(0, Math.round(seconds));
  const minutes = Math.floor(rounded / 60);
  const remainder = String(rounded % 60).padStart(2, "0");
  return `${minutes}:${remainder} behind`;
};

const playbackMessage = (snapshot: PlayerSnapshot, loadError?: string) =>
  loadError ??
  (snapshot.status === "empty"
    ? "No playable channels are on this Loomarr yet."
    : snapshot.status === "idle"
      ? "Choose a channel from the Guide or Surf."
      : snapshot.status === "failed"
        ? snapshot.error
        : undefined);

export { behindLabel, playbackMessage };
