import { pluralize } from "@loomarr/core/format";
import { useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Caption } from "@/components/ui/caption";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";
import type { PullCardProps } from "./pull-card.type";

// PullCard — a proposed filler acquisition awaiting a human (V66).
//
// ⚠ **This card IS the approval gate's face.** §10 has said "the machine proposes, a human
// commits" since the starter pack shipped; until V35 there was no object to commit. Nothing
// downloads while this card sits here — approving is the only path that enqueues, and it goes
// through the ordinary ingest job rather than a downloader of its own.
//
// ⚠ **Dropping a row does not remove it from the record.** The plan keeps every row it was
// proposed with and marks the dropped ones, because "we approved this" is only meaningful next
// to what was proposed. That is a server property; this component's job is not to hide it.

const PullCard = ({ pull, onApprove, onDismiss, deciding, className }: PullCardProps) => {
  // Which rows the operator has struck out, held locally until they commit. ⚠ Not sent as they
  // click: a pull is decided in one act, and a per-click PATCH would make "half-approved" a
  // state the gate has to reason about.
  const [dropped, setDropped] = useState<ReadonlySet<string>>(new Set());
  const [note, setNote] = useState("");

  // ⚠ `?? []` because huma types every Go slice as nullable, so the generated DTO says
  // `PullPlanRowDTO[] | null` even though the handler always sends `[]`.
  const plan = pull.plan ?? [];
  const skippedSources = (pull.sources ?? []).filter((source) => source.disposition !== "enumerated");
  const candidateID = (row: (typeof plan)[number]) => row.candidateId || row.sourceId;
  const kept = plan.filter((row) => !dropped.has(candidateID(row)));

  const toggleDropped = (id: string) =>
    setDropped((prev) => {
      const next = new Set(prev);
      if (!next.delete(id)) next.add(id);
      return next;
    });

  return (
    <Card className={cn("flex flex-col gap-3 p-4", className)}>
      <div className="flex flex-wrap items-start gap-3">
        <div className="flex min-w-0 flex-1 flex-col gap-1">
          <div className="flex flex-wrap items-center gap-2">
            <Badge variant="lock">Filler pull</Badge>
            <span className="font-semibold text-sm">{pull.title}</span>
          </div>
          {pull.proposedBy && <Caption>proposed by {pull.proposedBy}</Caption>}
        </div>

        <div className="flex shrink-0 gap-2">
          <Button variant="outline" size="sm" disabled={deciding} onClick={onDismiss}>
            Not now
          </Button>
          <Button
            size="sm"
            // Nothing left to fetch is refused by the server rather than recorded as an
            // approval that fetched nothing; disabling here says so before the round trip.
            disabled={deciding || kept.length === 0}
            onClick={() => onApprove({ dropCandidateIds: [...dropped], note })}
          >
            {deciding ? "Starting…" : "Approve pull"}
          </Button>
        </div>
      </div>

      {/* Why this pull exists. "Approve this" without a reason is a button, not a decision. */}
      {pull.reason && (
        <p className="rounded-md bg-muted/40 p-3 text-muted-foreground text-sm">{pull.reason}</p>
      )}

      <ul className="flex flex-col gap-2">
        {plan.map((row) => {
          const id = candidateID(row);
          const isDropped = dropped.has(id);
          return (
            <li key={id} className={cn("flex flex-wrap items-center gap-3", isDropped && "opacity-50")}>
              <Badge variant="neutral">{row.tag}</Badge>
              <div className="min-w-0 flex-1">
                <p className={cn("truncate text-sm", isDropped && "line-through")}>{row.name}</p>
                {row.why && <Caption>{row.why}</Caption>}
              </div>
              {/* ⚠ An estimate, rendered as one. What a source yields depends on what is still
                  there, what deduplicates, and what the splitter makes of a compilation — a
                  number presented as exact becomes "Loomarr said 40 and downloaded 12". */}
              {row.estimateClips > 0 && (
                <Caption className="tabular-nums">~{row.estimateClips} clips</Caption>
              )}
              <Button
                variant="ghost"
                size="sm"
                disabled={deciding}
                onClick={() => toggleDropped(id)}
                aria-label={isDropped ? `Put ${row.name} back` : `Leave ${row.name} out`}
              >
                {isDropped ? "Put back" : "Leave out"}
              </Button>
            </li>
          );
        })}
      </ul>

      {(pull.rejected?.length ?? 0) > 0 && (
        <details className="rounded-md border border-border px-3 py-2 text-sm">
          <summary className="cursor-pointer text-muted-foreground">
            {pluralize(pull.rejected?.length ?? 0, "other candidate")} considered and excluded
          </summary>
          <ul className="mt-2 flex flex-col gap-1">
            {(pull.rejected ?? []).map((candidate) => (
              <li key={candidate.candidateId} className="flex flex-wrap gap-2">
                <span>{candidate.name}</span>
                <Caption>
                  {candidate.disposition.replaceAll("_", " ")}: {candidate.detail}
                </Caption>
              </li>
            ))}
          </ul>
        </details>
      )}

      {skippedSources.length > 0 && (
        <details className="rounded-md border border-border px-3 py-2 text-sm">
          <summary className="cursor-pointer text-muted-foreground">
            {pluralize(skippedSources.length, "registered source")} not searched
          </summary>
          <ul className="mt-2 flex flex-col gap-1">
            {skippedSources.map((source) => (
              <li key={source.sourceId} className="flex flex-wrap gap-2">
                <span>{source.label || source.sourceId}</span>
                <Caption>
                  {source.disposition.replaceAll("_", " ")}: {source.detail}
                </Caption>
              </li>
            ))}
          </ul>
        </details>
      )}

      <Input
        value={note}
        onChange={(e) => setNote(e.target.value)}
        placeholder="Optional annotation for your records"
        aria-label="Notes for this pull"
        disabled={deciding}
      />

      <Caption>Optional annotation for your records. This does not change what is downloaded.</Caption>

      <Caption>
        {kept.length === 0
          ? "Every candidate is left out, so there's nothing to fetch. Dismiss it instead."
          : `${pluralize(kept.length, "candidate")} will be fetched. Nothing downloads until you approve.`}
      </Caption>
    </Card>
  );
};

export { PullCard };
