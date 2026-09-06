import {
  type FillerClipEvent,
  type FillerIncomingOutputBody,
  fillerApi,
  type IncomingClipDTO,
  type IncomingPipelineDTO,
} from "@loomarr/api";
import type { EventHandlers } from "@loomarr/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook } from "@testing-library/react";
import { createElement, type ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import { useClipPipeline } from "./use-clip-pipeline";

// The SSE merge is the one place the Incoming tab writes to the cache instead of reading it, so
// every rule it enforces is a way the queue can lie to an operator. They are tested through the
// REAL cache — seed the query, hand the hook a frame, read the query back — because the bugs
// worth catching here are all "what ended up in the cache", not "was setQueryData called".
//
// ⚠ The listener is mocked rather than provided. `useLoomarrEventListener` outside its provider
// is a deliberate no-op, so a test that rendered the real one would exercise nothing and pass.
const mocks = vi.hoisted(() => ({ handlers: undefined as EventHandlers | undefined }));
vi.mock("@/events/events-provider", () => ({
  useLoomarrEventListener: (h: EventHandlers) => {
    mocks.handlers = h;
  },
}));

const KEY = fillerApi.getFillerIncomingQueryKey();

// ⚠ The pipeline block is NESTED on the clip. The merge writes into it while spreading the clip,
// so a test whose fixture flattened the two would pass against a merge that clobbered the parent.
const row = (over: Partial<IncomingPipelineDTO> = {}): IncomingClipDTO => ({
  hash: "hash-cola",
  path: "cola.mp4",
  name: "Coca-Cola 1985",
  kind: "commercial",
  durationMs: 31_000,
  reason: "Loomarr is still working on this one.",
  pipeline: {
    stage: "transcode",
    status: "running",
    lifecycle: "in_progress",
    progress: 80,
    // ⚠ Seeded non-empty on purpose: `stages` is the VISITED ladder and the frame carries no
    // value for it, so a merge that replaced the block wholesale would silently empty the
    // expanded detail list. Nothing else in this file would notice.
    stages: [{ stage: "probe", status: "done", at: "2026-08-08T10:00:00Z" }],
    updatedAt: "2026-08-08T10:01:00Z",
    ...over,
  },
});

const frame = (over: Partial<FillerClipEvent> = {}): FillerClipEvent => ({
  hash: "hash-cola",
  stage: "transcode",
  status: "running",
  progress: 90,
  disposition: "running",
  ...over,
});

const body = (clips: IncomingClipDTO[]): FillerIncomingOutputBody => ({
  overview: {
    runnable: 0,
    inProgress: clips.length,
    scheduled: 0,
    needsDecision: 0,
    recoverable: 0,
    admitted: 0,
    rejected: 0,
    dismissed: 0,
  },
  clips,
  clipsTotal: clips.length,
  decisionsTotal: clips.filter((clip) => clip.needsDecision).length,
  reels: [],
  reelsTotal: 0,
  rejected: [],
  rejectedTotal: 0,
  recentlyFiled: [],
  recentlyFiledTotal: 0,
  stageOrder: [
    "probe",
    "transcode",
    "split",
    "screen",
    "language",
    "transcribe",
    "tag",
    "vision",
    "admission",
    "score",
  ],
  total: 0,
});

const setup = (clips: IncomingClipDTO[]) => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  client.setQueryData(KEY, { data: body(clips), status: 200 });
  const invalidate = vi.spyOn(client, "invalidateQueries").mockResolvedValue();
  const wrapper = ({ children }: { children: ReactNode }) =>
    createElement(QueryClientProvider, { client }, children);
  renderHook(() => useClipPipeline(), { wrapper });

  const send = (f: FillerClipEvent) => mocks.handlers?.onFillerClip?.(f);
  const rows = () => (client.getQueryData(KEY) as { data: FillerIncomingOutputBody }).data.clips ?? [];
  return { invalidate, rows, send };
};

describe("useClipPipeline", () => {
  it("paints a running frame onto the row it names", () => {
    const { rows, send } = setup([row()]);

    send(frame({ progress: 90 }));

    expect(rows()[0]?.pipeline).toMatchObject({ progress: 90, stage: "transcode" });
  });

  // ⚠ The frame carries no `stages`, `attempts` or `updatedAt`. Spreading it over the row would
  // blank the ladder the expanded view renders — and the row would still look right collapsed.
  it("leaves the visited ladder alone, because the frame does not carry one", () => {
    const { rows, send } = setup([row()]);

    send(frame({ progress: 95 }));

    expect(rows()[0]?.pipeline?.stages).toEqual([
      { stage: "probe", status: "done", at: "2026-08-08T10:00:00Z" },
    ]);
  });

  // Rule 2. A row assembled from a frame is missing every field the detail view needs, so a clip
  // that just entered the pipeline has to come from the GET.
  it("invalidates for an unknown hash instead of inserting a half-built row", () => {
    const { invalidate, rows, send } = setup([row()]);

    send(frame({ hash: "hash-never-seen" }));

    expect(rows()).toHaveLength(1);
    expect(invalidate).toHaveBeenCalledWith({ queryKey: KEY });
  });

  // Rule 3. `events.ts` invalidates the whole /v1/filler prefix on a terminal frame — merging
  // here would paint one frame of a state the server is already replacing.
  it("ignores a terminal frame, which the invalidation path owns", () => {
    const { rows, send } = setup([row()]);

    send(frame({ disposition: "filed", stage: "score", status: "done", progress: 100 }));

    expect(rows()[0]?.pipeline).toMatchObject({ stage: "transcode", progress: 80 });
  });

  it("refuses a late frame that would drag the percentage backwards inside one rung", () => {
    const { rows, send } = setup([row({ progress: 80 })]);

    send(frame({ progress: 12 }));

    expect(rows()[0]?.pipeline?.progress).toBe(80);
  });

  // ⚠ THE case the narrow rule exists for. A stage that fails and retries re-runs on the SAME
  // rung with progress reset to 0, so a guard that compared progress alone would pin the row at
  // "failed at 80%" while the transcode had actually restarted from the beginning. Guarding only
  // when stage AND status are both unchanged is what lets the restart through.
  it("lets a retry through, even though its progress resets to zero", () => {
    const { rows, send } = setup([row({ progress: 80, status: "failed" })]);

    send(frame({ progress: 0, status: "running" }));

    expect(rows()[0]?.pipeline).toMatchObject({ progress: 0, status: "running" });
  });

  // The sanctioned re-tag/re-split path (Rewind) moves a clip BACKWARD on purpose. A strictly
  // advance-only guard would blank the whole re-run until something forced a refetch.
  it("lets Rewind move the clip back up the ladder", () => {
    const { rows, send } = setup([row({ stage: "tag", status: "running", progress: 50 })]);

    send(frame({ stage: "split", status: "queued", progress: 0 }));

    expect(rows()[0]?.pipeline).toMatchObject({ stage: "split", status: "queued", progress: 0 });
  });

  // -1 is the "this rung cannot measure itself" sentinel, not a small number. Republishing it
  // must not read as going backwards from -1.
  it("accepts an unmeasurable rung republishing its sentinel", () => {
    const { rows, send } = setup([row({ stage: "transcribe", progress: -1 })]);

    send(frame({ stage: "transcribe", progress: -1, name: "Renamed" }));

    expect(rows()[0]).toMatchObject({ name: "Renamed" });
    expect(rows()[0]?.pipeline?.progress).toBe(-1);
  });
});
