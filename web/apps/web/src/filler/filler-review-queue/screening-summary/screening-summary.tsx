import type { FillerScreeningDTO } from "@loomarr/api/models/fillerScreeningDTO";
import { formatRelative } from "@loomarr/core/format";
import { Badge, type BadgeProps } from "@/components/ui/badge";
import { RightsReview } from "../rights-review";

const AXIS_LABELS = {
  visual_safety: "Visual safety",
  spoken_safety: "Spoken safety",
  written_safety: "Written safety",
  rights: "Current-use rights",
  playback_integrity: "Playback integrity",
} as const;

const humanize = (value: string) => value.replaceAll("_", " ");

const outcomeVariant = (outcome?: string): BadgeProps["variant"] => {
  if (outcome === "pass") return "signal";
  if (outcome === "reject") return "onair";
  return "caution";
};

const shortDigest = (digest: string) => `${digest.slice(0, 10)}…`;

const ScreeningSummary = ({ summary }: { summary: FillerScreeningDTO }) => {
  if (summary.state !== "available") {
    return (
      <div className="rounded-md border border-caution/35 bg-caution/5 p-3">
        <div className="flex flex-wrap items-center gap-2">
          <Badge variant="caution">Held</Badge>
          <p className="font-medium text-sm">
            {summary.state === "not_screened"
              ? "Screening has not run yet"
              : "Screening evidence unavailable"}
          </p>
        </div>
        <p className="mt-1 text-muted-foreground text-xs">
          {humanize(summary.reasonCode ?? "screening evidence unavailable")}. This clip cannot be confirmed
          for the library until the server can reproduce its evidence and exact playback bytes.
        </p>
      </div>
    );
  }

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <Badge variant={outcomeVariant(summary.outcome)}>{summary.outcome}</Badge>
          <span className="font-medium text-sm">Five independent screens</span>
        </div>
        {summary.assessedAt ? (
          <span className="text-muted-foreground text-xs">Assessed {formatRelative(summary.assessedAt)}</span>
        ) : null}
      </div>

      <ul className="divide-y divide-border overflow-hidden rounded-md border border-border">
        {summary.axes.map((axis) => (
          <li key={axis.axis} className="flex flex-wrap items-center gap-3 px-3 py-2.5">
            <Badge variant={outcomeVariant(axis.outcome)}>{axis.outcome}</Badge>
            <span className="min-w-36 font-medium text-sm">{AXIS_LABELS[axis.axis]}</span>
            <span className="min-w-0 flex-1 text-muted-foreground text-xs">{humanize(axis.reasonCode)}</span>
            <span className="font-mono text-2xs text-muted-foreground" title={axis.evidenceSha256}>
              {shortDigest(axis.evidenceSha256)}
            </span>
          </li>
        ))}
      </ul>

      {summary.rightsReview ? (
        <RightsReview
          clipHash={summary.clipHash}
          review={summary.rightsReview}
          screeningAssessedAt={summary.assessedAt}
        />
      ) : null}

      {summary.airworthiness ? (
        <section aria-label="Audience airworthiness" className="rounded-md border border-border p-3">
          <div className="flex flex-wrap items-center gap-2">
            <Badge variant={outcomeVariant(summary.airworthiness.verdict)}>
              {summary.airworthiness.verdict}
            </Badge>
            <p className="font-medium text-sm">Audience profile: {humanize(summary.airworthiness.profile)}</p>
          </div>
          {summary.airworthiness.observedFlags.length > 0 ? (
            <ul className="mt-3 flex flex-wrap gap-1.5" aria-label="Observed audience flags">
              {summary.airworthiness.observedFlags.map((flag) => (
                <li key={flag}>
                  <Badge variant="neutral">{humanize(flag)}</Badge>
                </li>
              ))}
            </ul>
          ) : (
            <p className="mt-2 text-muted-foreground text-xs">No audience-suitability flags observed.</p>
          )}
          {summary.airworthiness.triggers.length > 0 ? (
            <ul className="mt-3 space-y-1.5" aria-label="Decisive audience triggers">
              {summary.airworthiness.triggers.map((trigger) => (
                <li key={trigger.observationId} className="text-muted-foreground text-xs">
                  {humanize(trigger.flag)} · {trigger.axis} · {(trigger.startMs / 1000).toFixed(1)}–
                  {(trigger.endMs / 1000).toFixed(1)}s · {trigger.effect}
                </li>
              ))}
            </ul>
          ) : null}
        </section>
      ) : null}
    </div>
  );
};

export { ScreeningSummary };
