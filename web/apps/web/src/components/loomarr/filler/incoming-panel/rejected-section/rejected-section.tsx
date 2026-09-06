import type { IncomingRejectDTO } from "@loomarr/api/models/incomingRejectDTO";
import { pluralize } from "@loomarr/core/format";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Caption } from "@/components/ui/caption";

// The audit half of REFUSAL (§10 V51b/V51e) — the sibling of "filed without asking".
//
// ⚠ **Not optional, and `filler.reject.unidentified` is why.** That setting is ON by default and
// tombstones a clip nothing could identify — but "we could not identify it" is a different claim
// from "it is not a commercial", and a wordless station ident is exactly the case where they come
// apart while §10 calls a silent advert some of the best filler there is. A default that can
// refuse good clips has to show its work.
//
// ⚠ **Reconcile with V40, which says scan-time rejects get "no badge, no review step".** That
// still holds and is a different boundary: V40 refuses files at the SCAN edge, before they are
// catalogued, where a listing of every skipped file in a media folder would be noise. These
// happen after cataloguing, to clips Loomarr took in and then decided against — and those it owes
// an account of.

// The wording lives here because the server sends a stable CODE (§11's refusal-code precedent):
// a server sentence cannot be translated, cannot link to the setting that caused it, and freezes
// our copy into an API response.
//
// ⚠ The fallback is the raw code, never "Unknown reason". A code this build has no copy for comes
// from a newer backend, and `sliver` tells an operator — and a bug report — something.
const REASON: Record<string, string> = {
  black_content: "the picture is almost entirely black",
  duplicate: "Loomarr already has this one",
  language: "the speech is in another language",
  no_audio: "it has no sound",
  no_video: "it has no picture",
  screening: "a broadcast-safety check refused it",
  silent_content: "the audio track is almost entirely silent",
  sliver: "it's a fragment, too short to be an advert",
  too_short: "it's shorter than the floor",
  unidentified: "nothing in it said what it was",
  unplayable: "it couldn't be converted to something playable",
  unprobeable: "the file couldn't be read",
};

const RejectedSection = ({
  rows,
  onRestore,
  onRetryFailure,
  busyHash,
}: {
  rows: IncomingRejectDTO[];
  onRestore?: (clip: IncomingRejectDTO) => void;
  onRetryFailure?: (clip: IncomingRejectDTO) => void;
  busyHash?: string;
}) => (
  <section className="flex flex-col gap-3">
    <div className="flex flex-col gap-0.5">
      <h2 className="font-medium text-sm">Loomarr didn't use {pluralize(rows.length, "clip")}</h2>
      <Caption>These were downloaded and then turned down. The files are still where they were.</Caption>
    </div>
    <ul className="flex flex-col gap-2">
      {rows.map((clip) => (
        <li key={clip.hash} className="flex flex-wrap items-center gap-4 rounded-lg border border-border p-4">
          <div className="flex min-w-0 flex-1 flex-col gap-1">
            <span className="truncate font-medium text-sm">{clip.name || clip.hash}</span>
            <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
              <Badge variant="neutral">{REASON[clip.reason] ?? clip.reason}</Badge>
              {/* The measured fact — "8.2s; the floor is 10.0s" — is what makes a refusal arguable
                  rather than an assertion. */}
              {clip.detail && <Caption>{clip.detail}</Caption>}
            </div>
          </div>
          {/* ⚠ The override appears ONLY for a soft reject, and the server decides which those are
              (`RejectReason.Soft`, mirrored onto `restorable`). A clip with no audio track gets no
              button, because restoring it puts silence in a break — a control that cannot work is
              worse than no control, and deriving the list a second time here is the drift class
              this codebase keeps finding. */}
          {clip.recovery === "retry" && onRetryFailure ? (
            <Button
              variant="outline"
              size="sm"
              disabled={busyHash === clip.hash}
              onClick={() => onRetryFailure(clip)}
              title="Retry the failed work while preserving completed upstream stages."
            >
              Retry {clip.retryStage ?? clip.stage}
            </Button>
          ) : clip.restorable && onRestore ? (
            <Button
              variant="outline"
              size="sm"
              disabled={busyHash === clip.hash}
              onClick={() => onRestore(clip)}
              title="Put it back in the catalog. Loomarr will stop refusing it."
            >
              Use it anyway
            </Button>
          ) : null}
        </li>
      ))}
    </ul>
  </section>
);

export { RejectedSection };
