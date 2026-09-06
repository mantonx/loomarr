import * as fillerApi from "@loomarr/api/endpoints/filler";
import * as settingsApi from "@loomarr/api/endpoints/settings";
import { toProblem } from "@loomarr/api/mutator";
import { unwrap } from "@loomarr/api/unwrap";
import { formatBytes, formatRelative, pluralize } from "@loomarr/core/format";
import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import { useState } from "react";
import { toast } from "sonner";
import { useAuth } from "@/auth/use-auth";
import { EmptyState } from "@/components/loomarr/feedback/empty-state";
import { PoolHealth } from "@/components/loomarr/filler/pool-health";
import { WatchPill } from "@/components/loomarr/filler/watch-pill";
import { PageHeader } from "@/components/loomarr/shell/page-header";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { NavTabs } from "@/components/ui/nav-tabs";
import { useDocumentTitle } from "@/lib/use-document-title";
import { ClipTagDialog } from "../clip-tag-dialog";
import { FillerCatalog } from "../filler-catalog";
import { FillerManage } from "../filler-manage";
import { FillerOverview } from "../filler-overview";
import { FillerReviewQueue } from "../filler-review-queue";
import type { FillerSearch } from "../filler-search";
import { IncomingTab } from "../incoming-tab";
import { SourcesTab } from "../sources-tab";
import { TaxonomyTab } from "../taxonomy-tab";
import { useFillerInvalidate } from "../use-filler-invalidate";
import type { FillerPageProps } from "./filler-page.type";

// FillerPage is the route-level composition root. It owns only state shared across destinations:
// navigation counts, installation readiness, the watch/health summary, and the one tag editor
// opened by both Library and Incoming. Destination-specific behavior stays in its own module.
const FillerPage = ({ tab }: FillerPageProps) => {
  useDocumentTitle("Filler");
  const navigate = useNavigate();
  const { isAdmin } = useAuth();
  const [tagging, setTagging] = useState<string>();

  // Preserve Library's deep-linkable state when an operator leaves and returns. The shell only
  // carries the opaque route state; FillerCatalog owns its interpretation and mutations.
  const search = useSearch({ strict: false }) as Partial<FillerSearch>;
  const catalogSearch = {
    ...(search.q ? { q: search.q } : {}),
    ...(search.kind ? { kind: search.kind } : {}),
    ...(search.audience ? { audience: search.audience } : {}),
    ...(search.taxon ? { taxon: search.taxon } : {}),
    ...(search.unclassified ? { unclassified: true } : {}),
    ...(search.withoutAxis ? { withoutAxis: search.withoutAxis } : {}),
    ...(search.untagged ? { untagged: true } : {}),
    ...(search.view && search.view !== "grid" ? { view: search.view } : {}),
    ...(search.page && search.page > 1 ? { page: search.page } : {}),
    ...(search.parent ? { parent: search.parent } : {}),
  };

  const settings = settingsApi.useSettingsList();
  const fillerConfigured = Boolean(unwrap(settings.data, (body) => body.features)?.filler);
  const watch = unwrap(fillerApi.useFillerWatch().data, (body) => body);
  const poolQuery = fillerApi.useFillerPool({ query: { enabled: tab === "library" } });
  const pool = unwrap(poolQuery.data, (body) => body);
  const reviewsQuery = fillerApi.useFillerDecisionReviews({ limit: 100 }, { query: { enabled: isAdmin } });
  const reviewRows = unwrap(reviewsQuery.data, (body) => body.rows) ?? [];
  const reviewTotal = unwrap(reviewsQuery.data, (body) => body.total) ?? 0;
  const reviewHashes = new Set(reviewRows.map((review) => review.clipHash));

  // Shared exact-identity editor. Always resolving by hash avoids coupling the shell to whichever
  // Library page happens to be mounted, and includeHeld is required for Incoming clips.
  const taggingQuery = fillerApi.useListFiller(
    { hashes: tagging ? [tagging] : [], includeHeld: true, includeComposites: true, limit: 1 },
    { query: { enabled: Boolean(tagging) } },
  );
  const taggingClip = unwrap(taggingQuery.data, (body) => body.clips[0]);
  const { invalidateLifecycle } = useFillerInvalidate();

  const proposePull = fillerApi.useProposeFillerPull({
    mutation: {
      onSuccess: () => {
        toast.success("Pull proposed", {
          description: "Nothing is downloading yet. Approve it in the queue to start.",
        });
      },
      onError: (error) => {
        const problem = toProblem(error);
        toast.error(problem.title ?? "Couldn't plan a pull", {
          ...(problem.detail ? { description: problem.detail } : {}),
        });
      },
    },
  });

  if (!fillerConfigured) {
    return (
      <div className="flex h-full min-h-0 flex-col">
        <PageHeader title="Filler" description={<FillerDescription />} />
        <div className="min-h-0 flex-1 overflow-auto p-6">
          <EmptyState
            title="No filler folder configured"
            description="Add a folder of commercials, bumpers, and station IDs under Settings → Defaults. Loomarr indexes it for scheduling; point Tunarr at the same folder so it can play the clips."
            {...(isAdmin
              ? {
                  action: {
                    label: "Open filler defaults",
                    onClick: () => navigate({ to: "/filler/settings" }),
                  },
                }
              : {})}
          />
        </div>
      </div>
    );
  }

  const statusLine = watch
    ? [
        `${watch.sourcesOn} of ${watch.sourcesTotal} sources on`,
        pluralize(watch.clips, "clip"),
        ...(watch.held > 0 ? [`${watch.held} waiting`] : []),
        ...(watch.lastScanAt ? [`last scan ${formatRelative(watch.lastScanAt)}`] : []),
      ].join(" · ")
    : "";

  return (
    <div className="flex h-full min-h-0 flex-col">
      <PageHeader
        title="Filler"
        description={<FillerDescription />}
        actions={watch && <WatchPill status={statusLine} health={watch.health} />}
      />
      <div className="flex min-h-0 flex-1 flex-col gap-6 overflow-auto p-6">
        {pool && tab === "library" ? (
          <PoolHealth
            pool={pool}
            {...(isAdmin ? { onProposePull: () => proposePull.mutate({ data: {} }) } : {})}
            proposing={proposePull.isPending}
          />
        ) : null}

        {watch?.autoFetch?.stoppedBy && tab === "library" ? (
          <Card className="flex flex-wrap items-center gap-3 border-caution/40 bg-caution/5 p-4">
            <div className="min-w-0 flex-1">
              <p className="font-medium text-sm">Automatic fetching is paused</p>
              <p className="mt-0.5 text-muted-foreground text-sm">
                {watch.autoFetch.stoppedBy === "catalog"
                  ? `${watch.autoFetch.catalogClips.toLocaleString()} of ${(watch.autoFetch.maxCatalog ?? 0).toLocaleString()} catalog clips are in use.`
                  : `${formatBytes(watch.autoFetch.diskBytes ?? 0)} of ${formatBytes(watch.autoFetch.maxDiskBytes ?? 0)} filler storage is in use.`}{" "}
                Manual queueing still works; curate the library or raise this ceiling to resume unattended
                fetching.
              </p>
            </div>
            {isAdmin ? (
              <Button variant="outline" size="sm" render={<Link to="/filler/settings" />}>
                Review limits
              </Button>
            ) : null}
          </Card>
        ) : null}

        <NavTabs
          label="Filler sections"
          linkComponent={Link}
          tabs={[
            { id: "overview", label: "Overview", to: "/filler" },
            ...(isAdmin
              ? [
                  { id: "sources", label: "Sources", to: "/filler/sources" },
                  { id: "incoming", label: "Incoming", to: "/filler/incoming", count: reviewTotal },
                ]
              : []),
            {
              id: "library",
              label: "Library",
              to: "/filler/library",
              search: catalogSearch,
              count: watch?.clips,
            },
            { id: "manage", label: "Manage", to: "/filler/manage" },
          ]}
          activeId={tab === "taxonomy" ? "manage" : tab}
        />

        {tab === "overview" ? (
          <FillerOverview />
        ) : tab === "incoming" && isAdmin ? (
          <div className="flex flex-col gap-8">
            <FillerReviewQueue hideEmpty />
            <IncomingTab
              onEditTags={setTagging}
              excludedHashes={reviewHashes}
              semanticReviewCount={reviewRows.length}
            />
          </div>
        ) : tab === "manage" ? (
          <FillerManage />
        ) : tab === "sources" ? (
          <SourcesTab />
        ) : tab === "taxonomy" ? (
          <TaxonomyTab isAdmin={isAdmin} />
        ) : (
          <FillerCatalog
            isAdmin={isAdmin}
            onEditTags={setTagging}
            onProposePull={() => proposePull.mutate({ data: {} })}
          />
        )}

        {taggingClip ? (
          <ClipTagDialog
            key={taggingClip.hash}
            clip={taggingClip}
            onClose={() => setTagging(undefined)}
            onSaved={() => {
              setTagging(undefined);
              invalidateLifecycle();
            }}
          />
        ) : null}
      </div>
    </div>
  );
};

const FillerDescription = () => (
  <>
    Loomarr continuously sources, prepares, and rotates commercials, bumpers, and station IDs. Each channel{" "}
    <Link to="/guide" className="text-signal underline-offset-2 hover:underline">
      uses its own grounded selection
    </Link>{" "}
    from the shared library.
  </>
);

export { FillerPage };
