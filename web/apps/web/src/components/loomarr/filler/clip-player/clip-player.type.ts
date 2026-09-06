import type { ClipDTO } from "@loomarr/api/models/clipDTO";

// The clip data is the orval-generated ClipDTO (§12) — no hand-written mirror.
interface ClipPlayerProps {
  // The clip to play. `null` closes the dialog — a single nullable prop rather than a separate
  // `open` boolean, because the two can otherwise disagree: `open` with no clip renders an empty
  // player, and a clip with `open=false` keeps a <video> mounted and buffering off-screen.
  clip: ClipDTO | null;
  // Called when the dialog closes by any route — Escape, the close button, or the overlay.
  onClose: () => void;
  // Called only once the browser reports that this clip's media is actually playing. Opening the
  // dialog or requesting autoplay is not sufficient evidence because playback may fail.
  onPlaybackStart?: () => void;
  className?: string;
}

export type { ClipPlayerProps };
