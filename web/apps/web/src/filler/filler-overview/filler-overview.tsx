import * as fillerApi from "@loomarr/api/endpoints/filler";
import type { FillerDecisionOverviewDTO } from "@loomarr/api/models/fillerDecisionOverviewDTO";
import { unwrap } from "@loomarr/api/unwrap";
import { formatDuration, pluralize } from "@loomarr/core/format";
import { Link } from "@tanstack/react-router";
import { ErrorState } from "@/components/loomarr/feedback/error-state";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";

type Action = {
  title: string;
  description: string;
  label: string;
  to: "/filler/incoming" | "/filler/manage";
};

// This maps a server-owned action enum to presentation only. Priority and health are never
// reconstructed from counts or detail feeds in the browser (§10 V63).
const decisionAction = (overview: FillerDecisionOverviewDTO): Action | undefined => {
  switch (overview.nextAction) {
    case "none":
      return undefined;
    case "repair_processing":
      return {
        title: "Filler processing needs repair",
        description: `${pluralize(overview.actionCount ?? 0, "item")} cannot progress until its processing setup is repaired.`,
        label: "Open diagnostics",
        to: "/filler/manage",
      };
    case "retry_processing":
      return {
        title: "Some filler can be retried",
        description: `${pluralize(overview.actionCount ?? 0, "item")} hit a recoverable processing problem.`,
        label: "Open diagnostics",
        to: "/filler/manage",
      };
    case "review_decisions":
      return {
        title: "A few clips need your judgment",
        description: `${pluralize(overview.actionCount ?? 0, "clip")} could not be classified safely without a person.`,
        label: "Review clips",
        to: "/filler/incoming",
      };
  }
};

const FillerOverview = () => {
  const decisionQuery = fillerApi.useFillerDecisionOverview();
  // Readiness still owns channel coverage. It no longer owns the admission-health answer or
  // ranked action rendered above it.
  const readinessQuery = fillerApi.useFillerReadiness();
  const overview = unwrap(decisionQuery.data, (body) => body);
  const readiness = unwrap(readinessQuery.data, (body) => body);

  if (decisionQuery.error) {
    return <ErrorState error={decisionQuery.error} onRetry={() => decisionQuery.refetch()} />;
  }
  if (!overview) {
    return (
      <Card aria-live="polite" className="p-6">
        <p className="font-medium">Checking filler health…</p>
        <p className="mt-1 text-muted-foreground text-sm">Reading the latest durable admission decisions.</p>
      </Card>
    );
  }

  const action = decisionAction(overview);

  return (
    <div className="flex flex-col gap-6">
      <Card className={overview.healthy ? "border-signal/35 p-5" : "border-caution/40 p-5"}>
        <div className="flex flex-col items-start gap-4 sm:flex-row">
          <div className="min-w-0 flex-1">
            <Badge variant={overview.healthy ? "signal" : "caution"}>
              {overview.healthy ? "Working automatically" : "Action recommended"}
            </Badge>
            <h2 className="mt-3 font-semibold text-xl">{action?.title ?? "Filler is working on its own"}</h2>
            <p className="mt-1 max-w-3xl text-muted-foreground text-sm">
              {action?.description ??
                "Recent clips were admitted or rejected automatically, and nothing needs your attention."}
            </p>
          </div>
          {action ? <Button render={<Link to={action.to} />}>{action.label}</Button> : null}
        </div>
      </Card>

      <section aria-labelledby="admission-summary-heading">
        <div className="mb-3">
          <h2 id="admission-summary-heading" className="font-semibold text-lg">
            Admission summary
          </h2>
          <p className="text-muted-foreground text-sm">Current outcomes, kept separate by the server.</p>
        </div>
        <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
          <Card className="p-4">
            <p className="text-muted-foreground text-sm">Admitted</p>
            <p className="mt-1 font-semibold text-2xl tabular-nums">{overview.counts.admitted}</p>
            <p className="mt-1 text-muted-foreground text-xs">Ready for the compatible filing gate</p>
          </Card>
          <Card className="p-4">
            <p className="text-muted-foreground text-sm">Rejected automatically</p>
            <p className="mt-1 font-semibold text-2xl tabular-nums">{overview.counts.rejected}</p>
            <p className="mt-1 text-muted-foreground text-xs">Normal outcomes, not chores</p>
          </Card>
          <Card className="p-4">
            <p className="text-muted-foreground text-sm">Needs judgment</p>
            <p className="mt-1 font-semibold text-2xl tabular-nums">{overview.counts.unresolvedReviews}</p>
            <p className="mt-1 text-muted-foreground text-xs">Semantic questions only</p>
          </Card>
          <Card className="p-4">
            <p className="text-muted-foreground text-sm">Processing holds</p>
            <p className="mt-1 font-semibold text-2xl tabular-nums">{overview.counts.operational}</p>
            <p className="mt-1 text-muted-foreground text-xs">
              {pluralize(overview.counts.retryable, "retryable item")}
            </p>
          </Card>
        </div>
      </section>

      <section aria-labelledby="coverage-heading">
        <div className="mb-3 flex flex-wrap items-end justify-between gap-3">
          <div>
            <h2 id="coverage-heading" className="font-semibold text-lg">
              Channel coverage
            </h2>
            <p className="text-muted-foreground text-sm">
              Playable time and variety remain separate from admission health.
            </p>
          </div>
          <Button variant="outline" size="sm" render={<Link to="/filler/library" />}>
            Browse library
          </Button>
        </div>
        {readiness?.pool.channels.length ? (
          <div className="grid gap-3 lg:grid-cols-2">
            {readiness.pool.channels.map((channel) => (
              <Card key={channel.channelId} className="p-4">
                <div className="flex items-start justify-between gap-3">
                  <div className="min-w-0">
                    <Link
                      className="font-medium hover:underline"
                      to="/channels/$id/filler"
                      params={{ id: channel.channelId }}
                    >
                      {channel.number} · {channel.name}
                    </Link>
                    <p className="mt-1 text-muted-foreground text-sm">
                      {formatDuration(channel.durationMs)} playable · {pluralize(channel.total, "clip")}
                    </p>
                  </div>
                  <Badge
                    variant={
                      channel.level === "exact"
                        ? "signal"
                        : channel.level === "bumper_card"
                          ? "lock"
                          : "caution"
                    }
                  >
                    {channel.level === "bumper_card" ? "Bumper only" : channel.level.replace("_", " ")}
                  </Badge>
                </div>
                <p className="mt-3 text-muted-foreground text-xs">
                  {channel.categories} {channel.categories === 1 ? "category" : "categories"} ·{" "}
                  {pluralize(channel.brands, "brand")}
                </p>
              </Card>
            ))}
          </div>
        ) : (
          <Card className="p-4 text-muted-foreground text-sm">
            {readinessQuery.error
              ? "Channel coverage is temporarily unavailable."
              : "No live channels need filler coverage yet."}
          </Card>
        )}
      </section>
    </div>
  );
};

export { decisionAction, FillerOverview };
