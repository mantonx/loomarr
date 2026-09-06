import * as fillerApi from "@loomarr/api/endpoints/filler";
import type { FillerDecisionActivityWireDTOKind } from "@loomarr/api/models/fillerDecisionActivityWireDTOKind";
import { unwrap } from "@loomarr/api/unwrap";
import { formatRelative, pluralize } from "@loomarr/core/format";
import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { useAuth } from "@/auth/use-auth";
import { ErrorState } from "@/components/loomarr/feedback/error-state";
import { Badge, type BadgeProps } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Disclosure } from "@/components/ui/disclosure";

const ACTIVITY_LABELS: Record<FillerDecisionActivityWireDTOKind, string> = {
  automatic_admit: "Admitted automatically",
  automatic_reject: "Rejected automatically",
  review_requested: "Asked for review",
  review_admit: "Accepted after review",
  review_reject: "Rejected after review",
  correction: "Corrected",
  review_abandoned: "Skipped for later",
  restore: "Restored",
  reversal: "Reversed",
};

type ActivityPresentation = { label: string; variant: BadgeProps["variant"] };

const activityPresentation = (
  kind: FillerDecisionActivityWireDTOKind,
  applicationMode: unknown,
): ActivityPresentation => {
  if (kind === "automatic_admit" || kind === "automatic_reject") {
    if (applicationMode === "shadow") {
      return {
        label: kind === "automatic_admit" ? "Would admit (shadow)" : "Would reject (shadow)",
        variant: "caution",
      };
    }
    if (applicationMode !== "applied") {
      return { label: "Decision mode unavailable", variant: "caution" };
    }
  }
  return {
    label: ACTIVITY_LABELS[kind],
    variant:
      kind === "automatic_admit" || kind === "review_admit"
        ? "signal"
        : kind === "automatic_reject" || kind === "review_reject"
          ? "neutral"
          : "caution",
  };
};

const FillerManage = () => {
  const { isAdmin } = useAuth();
  const [diagnosticsOpen, setDiagnosticsOpen] = useState(false);
  const activityQuery = fillerApi.useFillerDecisionActivity({ limit: 100 });
  const diagnosticsQuery = fillerApi.useFillerDecisionDiagnostics(
    { limit: 100 },
    { query: { enabled: isAdmin && diagnosticsOpen } },
  );
  const activity = unwrap(activityQuery.data, (body) => body);
  const diagnostics = unwrap(diagnosticsQuery.data, (body) => body);

  return (
    <div className="flex flex-col gap-6">
      <section aria-labelledby="manage-tools-heading">
        <div className="mb-3">
          <h2 id="manage-tools-heading" className="font-semibold text-lg">
            Manage filler
          </h2>
          <p className="text-muted-foreground text-sm">
            Everyday operation is automatic. Open these only when you want to change how it works.
          </p>
        </div>
        <div className="grid gap-3 md:grid-cols-2">
          <Card className="p-4">
            <h3 className="font-medium">Automation</h3>
            <p className="mt-1 text-muted-foreground text-sm">
              Adjust bounded processing, storage, and acquisition defaults.
            </p>
            {isAdmin ? (
              <Button className="mt-4" size="sm" variant="outline" render={<Link to="/filler/settings" />}>
                Automation settings
              </Button>
            ) : null}
          </Card>
          <Card className="p-4">
            <h3 className="font-medium">Taxonomy</h3>
            <p className="mt-1 text-muted-foreground text-sm">
              Inspect the grounded vocabulary used for classification and matching.
            </p>
            <Button className="mt-4" size="sm" variant="outline" render={<Link to="/filler/taxonomy" />}>
              Open taxonomy
            </Button>
          </Card>
        </div>
      </section>

      <section aria-labelledby="activity-heading">
        <div className="mb-3">
          <h2 id="activity-heading" className="font-semibold text-lg">
            Activity
          </h2>
          <p className="text-muted-foreground text-sm">
            Automatic outcomes and later corrections, newest first.
          </p>
        </div>
        {activityQuery.error ? (
          <ErrorState error={activityQuery.error} onRetry={() => activityQuery.refetch()} />
        ) : null}
        {!activity && !activityQuery.error ? (
          <Card aria-live="polite" className="p-4">
            Loading activity…
          </Card>
        ) : null}
        {activity?.rows.length === 0 ? (
          <Card className="p-4 text-muted-foreground text-sm">
            No admission decisions have been recorded yet.
          </Card>
        ) : null}
        {activity?.rows.length ? (
          <div className="overflow-hidden rounded-lg border border-border">
            {activity.rows.map((row) => {
              const presentation = activityPresentation(row.kind, row.applicationMode);
              return (
                <div
                  key={row.id}
                  className="flex flex-wrap items-center gap-3 border-border border-b p-3 last:border-b-0"
                >
                  <Badge variant={presentation.variant}>{presentation.label}</Badge>
                  <span className="min-w-0 flex-1 truncate font-mono text-muted-foreground text-xs">
                    Clip {row.clipHash.slice(0, 12)}…
                  </span>
                  <span className="text-muted-foreground text-xs">{formatRelative(row.createdAt)}</span>
                </div>
              );
            })}
          </div>
        ) : null}
      </section>

      {isAdmin ? (
        <Disclosure open={diagnosticsOpen} onOpenChange={setDiagnosticsOpen}>
          <Card className="overflow-hidden" id="diagnostics">
            <div className="flex flex-wrap items-center gap-3 p-4">
              <div className="min-w-0 flex-1">
                <h2 className="font-semibold text-lg">Diagnostics</h2>
                <p className="text-muted-foreground text-sm">
                  Operational holds, recovery, retries, and processing details.
                </p>
              </div>
              {diagnostics ? (
                <Badge variant={diagnostics.total > 0 ? "caution" : "neutral"}>
                  {pluralize(diagnostics.total, "current hold")}
                </Badge>
              ) : null}
              <Disclosure.Trigger label={`${diagnosticsOpen ? "Hide" : "Show"} filler diagnostics`} />
            </div>
            <Disclosure.Panel className="space-y-5 border-border border-t p-4">
              {diagnosticsOpen ? (
                <>
                  {diagnosticsQuery.error ? (
                    <ErrorState error={diagnosticsQuery.error} onRetry={() => diagnosticsQuery.refetch()} />
                  ) : null}
                  {diagnostics?.rows.length === 0 ? (
                    <p className="text-muted-foreground text-sm">No current processing holds.</p>
                  ) : null}
                  {diagnostics?.rows.map((row) => (
                    <div
                      key={row.id}
                      className="flex flex-wrap items-start gap-3 rounded-md border border-border p-3"
                    >
                      <Badge variant={row.retryable ? "caution" : "lock"}>
                        {row.retryable ? "Retryable" : "Repair needed"}
                      </Badge>
                      <div className="min-w-0 flex-1">
                        <p className="font-medium text-sm">{row.code.replaceAll("_", " ")}</p>
                        <p className="mt-1 text-muted-foreground text-xs">
                          Recovery: {row.recovery.replaceAll("_", " ")} · clip {row.clipHash.slice(0, 10)}…
                        </p>
                      </div>
                      <span className="text-muted-foreground text-xs">{formatRelative(row.createdAt)}</span>
                    </div>
                  ))}
                </>
              ) : null}
            </Disclosure.Panel>
          </Card>
        </Disclosure>
      ) : null}
    </div>
  );
};

export { FillerManage };
