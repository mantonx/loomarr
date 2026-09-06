import type { IncomingClipDTO } from "@loomarr/api/models/incomingClipDTO";
import { formatClipDuration } from "@loomarr/core/format";
import { Button } from "@/components/ui/button";
import { Caption } from "@/components/ui/caption";
import { Disclosure } from "@/components/ui/disclosure";
import { Image } from "@/components/ui/image";
import { ClipPipeline, copyFor } from "../../clip-pipeline";

// One row of the Incoming conveyor for a clip the MACHINE still owns (§10 V51b/V51e).
//
// ⚠ Its sibling is the decision row in `incoming-panel.tsx`, and they sit in ONE list rather than
// two sections. They were two lists, over overlapping populations, and the same clip rendered in
// both — a row demanding a decision above a row captioned "nothing here needs you". A clip is at
// one place on the belt; which row it gets is `needsDecision`.
//
// ⚠ **Collapsed by default, and the collapsed summary is the thing that advances.** Forty clips ×
// ten rungs is 400 lines of moving text. The pip strip carries the stage-by-stage watch for the
// whole queue at once; expanding gives the named ladder, the skip reasons and the percentage. Rows
// are never auto-expanded on a frame — that makes the queue jump under the cursor of someone
// reading it.

// The sentence under the name. ⚠ Deliberately the ACTIVE voice for a running rung ("Working out
// what it is") and not a status word: the queue's job is to answer "is anything happening", and
// "tag / running" answers it in the machine's vocabulary rather than the operator's.
const sentenceFor = (row: { stage: string; status: string }): string => {
  const { active, label } = copyFor(row.stage);
  if (row.status === "running") return active;
  if (row.status === "queued") return `Waiting to ${label.toLowerCase()}`;
  if (row.status === "failed") return `${label} — failed, retrying`;
  return label;
};

const PreparingRow = ({
  clip,
  ladder,
  busy,
  onRetryStage,
}: {
  clip: IncomingClipDTO;
  ladder: string[];
  busy?: boolean;
  onRetryStage?: (clip: IncomingClipDTO, stage: string) => void;
}) => {
  const name = clip.name || clip.hash;
  const pipeline = clip.pipeline;
  if (!pipeline) return null;

  return (
    <li>
      <Disclosure className="rounded-lg border border-border">
        <div className="flex flex-wrap items-center gap-3 p-3">
          {/* ⚠ The trigger is a DISCRETE chevron rather than the whole row, because the row carries
              its own controls. CollapsibleSection wraps its header in one <button>, which would
              make anything else in here unreachable by keyboard. */}
          <Disclosure.Trigger label={`Show what is happening to ${name}`} />

          {/* ⚠ Rendered only when the row actually HAS an artwork record. A clip still at `probe`
              has none, and drawing an image on the strength of the hash alone would show a broken
              one for exactly the newest rows — the ones an operator is most likely watching.
              That reasoning is unchanged from V51e; only what answers "is there artwork?" moved.

              ⚠ `thumbImage`, not `thumbnail`: V52 phase 8 retired that field and the
              /v1/filler/thumb route it addressed. A clip on this belt is the LEAST likely to have retired-ok
              been adopted yet — it is being prepared right now — so the absent case is the common
              one here, and it renders as no image rather than a placeholder. */}
          {clip.thumbImage && (
            <Image
              image={clip.thumbImage}
              alt=""
              sizes="3.5rem"
              className="h-8 w-14 shrink-0 rounded object-cover"
            />
          )}

          <div className="flex min-w-0 flex-1 flex-col gap-0.5">
            <div className="flex flex-wrap items-baseline gap-x-2">
              <span className="truncate font-medium text-sm">{name}</span>
              {/* ⚠ formatClipDuration, NOT formatDuration — the latter is minute-granular because
                  it formats programme runtimes, and every filler clip would render "0m". */}
              {clip.durationMs > 0 && (
                <Caption className="tabular-nums">{formatClipDuration(clip.durationMs)}</Caption>
              )}
            </div>
            <Caption>{sentenceFor(pipeline)}</Caption>
          </div>

          <ClipPipeline row={pipeline} name={name} ladder={ladder} className="shrink-0" />
        </div>

        <Disclosure.Panel className="border-border border-t p-3">
          <ClipPipeline row={pipeline} name={name} ladder={ladder} variant="list" />
          {pipeline.recovery === "retry" && onRetryStage ? (
            <div className="mt-3 flex flex-wrap items-center gap-3 border-border border-t pt-3">
              <p className="min-w-0 flex-1 text-muted-foreground text-xs">
                Fixed the cause? Retry this stage now instead of waiting for its backoff.
              </p>
              <Button
                variant="outline"
                size="sm"
                disabled={busy}
                onClick={() => onRetryStage(clip, pipeline.retryStage ?? pipeline.stage)}
              >
                Retry {copyFor(pipeline.stage).label.toLowerCase()} now
              </Button>
            </div>
          ) : null}
        </Disclosure.Panel>
      </Disclosure>
    </li>
  );
};

export { PreparingRow, sentenceFor };
