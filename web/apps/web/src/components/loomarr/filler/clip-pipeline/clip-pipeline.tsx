import type { IncomingPipelineDTO } from "@loomarr/api/models/incomingPipelineDTO";
import { Check, Loader2, Minus, X } from "lucide-react";
import { Progress } from "@/components/ui/progress";
import { cn } from "@/lib/utils";
import type { ClipPipelineProps } from "./clip-pipeline.type";

// ClipPipeline — where one clip is in the ten-rung ingest pipeline (§10 V51b/V51e/V61).
//
// ⚠ **The copy lives on the frontend, keyed by stage id; the SEQUENCE lives on the server.** The
// backend sends stable ids (`probe`, `transcode`, …) — the §11 refusal-code precedent — and this
// map turns them into what an operator reads. A rung the server adds still renders, under its raw
// id, rather than vanishing from a ladder that claims to be complete.
const COPY: Record<string, { active: string; label: string }> = {
  probe: { label: "Check the file", active: "Checking the file" },
  transcode: { label: "Level the sound", active: "Levelling the sound" },
  split: { label: "Find the ads inside", active: "Finding the ads inside" },
  screen: { label: "Check broadcast safety", active: "Checking broadcast safety" },
  language: { label: "Check the language", active: "Checking the language" },
  transcribe: { label: "Listen", active: "Listening" },
  tag: { label: "Work out what it is", active: "Working out what it is" },
  vision: { label: "Look at the picture", active: "Looking at the picture" },
  admission: { label: "Record the admission check", active: "Recording the admission check" },
  score: { label: "Score it", active: "Scoring it" },
};

// ⚠ The fallback is the raw id, never a placeholder like "Unknown stage". A rung this build has
// no copy for is a rung a NEWER backend added, and `transcode` tells an operator (and a bug
// report) more than "Unknown stage" ever could.
const copyFor = (id: string) => COPY[id] ?? { label: id, active: id };

// ⚠ `queued` is distinct from BOTH `running` and `upcoming`, and the distinction was found by
// looking at 85 real clips rather than by a test. The runner enrols every clip at `probe/queued`
// and works one at a time, so collapsing queued into running drew the entire queue as though all
// 85 were being processed at once — the same "it looks like something it isn't" inversion V51b
// exists to remove, pointing the other way. It is not `upcoming` either: an enrolled clip has been
// admitted to the pipeline, which a rung the clip has not reached has not.
type RungStatus = "done" | "failed" | "queued" | "running" | "skipped" | "upcoming";

// resolve reads one rung's state off the row: the visited ladder if it has been reached, the
// clip's current position if it is the one running, and `upcoming` otherwise.
//
// ⚠ The CURRENT stage is read from `row.stage`/`row.status` rather than from `row.stages`,
// because the runner records a rung's entry only once it resolves. A stage mid-run has no record
// yet, so trusting the visited ladder alone would draw the rung the clip is actually working on
// as if nothing had started.
const resolve = (id: string, row: IncomingPipelineDTO): { note?: string; status: RungStatus } => {
  if (id === row.stage) {
    if (row.status === "done") return { status: "done" };
    if (row.status === "failed") return { status: "failed" };
    if (row.status === "skipped") return { status: "skipped" };
    if (row.status === "queued") return { status: "queued" };
    return { status: "running" };
  }
  const seen = (row.stages ?? []).find((s) => s.stage === id);
  if (!seen) return { status: "upcoming" };
  if (seen.status === "skipped") return { note: seen.note, status: "skipped" };
  if (seen.status === "failed") return { note: seen.note, status: "failed" };
  return { status: "done" };
};

// The pip palette. ⚠ `tune` for in-progress, NOT `suggest` — §2.1 reserves `suggest` as THE AI
// colour, and only four of these ten rungs may use AI. Spending the AI colour on "something is
// happening" would leave nothing to mean "a model decided this".
const PIP: Record<RungStatus, string> = {
  done: "bg-lock",
  failed: "bg-onair-300",
  // ⚠ Neutral and STILL. Not `tune` (that means work is happening now) and deliberately not
  // `signal` amber either, which this app's palette reserves for "a human is needed" — 85 amber
  // rows would read as a queue demanding attention, which is the opposite of what this section
  // says about itself.
  queued: "bg-static-500",
  running: "bg-tune motion-safe:animate-pulse",
  skipped: "bg-static-400",
  upcoming: "bg-static-700",
};

// The sentence a screen reader gets per rung. Colour is never the only signal.
const SPOKEN: Record<RungStatus, string> = {
  done: "done",
  failed: "failed",
  queued: "waiting",
  running: "in progress",
  skipped: "skipped",
  upcoming: "not started",
};

const ICON: Record<RungStatus, typeof Check | null> = {
  done: Check,
  failed: X,
  // No icon: a queued rung shows the same neutral dot as an unreached one, because the honest
  // difference between them is the label, not a symbol claiming activity.
  queued: null,
  running: Loader2,
  skipped: Minus,
  upcoming: null,
};

const ICON_TONE: Record<RungStatus, string> = {
  done: "text-lock",
  failed: "text-onair-300",
  queued: "text-static-500",
  running: "text-tune motion-safe:animate-spin",
  skipped: "text-static-400",
  upcoming: "text-static-700",
};

const ClipPipeline = ({ row, name, ladder, variant = "strip", className }: ClipPipelineProps) => {
  // ⚠ The two variants take DIFFERENT accessible names even though they describe the same clip.
  // A row renders both — the strip in the header, the list inside the disclosure — so one shared
  // name would put two identically-named lists in the tree, and "Progress for Coca-Cola 1985"
  // twice is indistinguishable to anyone navigating by landmark.
  const listName = variant === "strip" ? `Progress for ${name}` : `Stage detail for ${name}`;

  // ⚠ ONE `<ol>` with an accessible name, and the pips themselves `aria-hidden`. The alternative —
  // a tooltip or a `title=` per pip — is 9 × 40 = 360 focusable elements on a full queue, and a
  // bare `title` on a 6px span is the "green axe ≠ operable" trap this codebase has already paid
  // for. The keyboard path to the detail is the row's Disclosure trigger, not the strip.
  if (variant === "strip") {
    return (
      <ol aria-label={listName} className={cn("flex items-center gap-1", className)}>
        {ladder.map((id) => {
          const { status } = resolve(id, row);
          return (
            <li key={id} className="flex">
              <span className="sr-only">{`${copyFor(id).label}: ${SPOKEN[status]}`}</span>
              <span aria-hidden className={cn("block h-1.5 w-4 rounded-full", PIP[status])} />
            </li>
          );
        })}
      </ol>
    );
  }

  return (
    <ol aria-label={listName} className={cn("flex flex-col gap-1.5", className)}>
      {ladder.map((id) => {
        const { note, status } = resolve(id, row);
        const Icon = ICON[status];
        const { active, label } = copyFor(id);
        return (
          <li key={id} className="flex items-start gap-2 text-sm">
            <span className="flex size-4 shrink-0 items-center justify-center pt-0.5">
              {Icon ? (
                <Icon className={cn("size-3.5", ICON_TONE[status])} aria-hidden />
              ) : (
                <span aria-hidden className="block size-1.5 rounded-full bg-static-700" />
              )}
            </span>
            {/* ⚠ `role="alert"` sits on the CONTENT, never on the `<li>`. A role on the list item
                REPLACES its implicit `listitem` role, so the parent `<ol>` then has a direct child
                that is not a list item — axe `list`, serious, and the strip's own accessible
                structure quietly gone. Found in CI after the contrast fix uncovered it: adding
                ARIA is itself a way to introduce a violation, which is why the announcement is
                scoped to a wrapper instead. Still per-rung, not per-list: one clip failing must
                announce, forty rungs quietly succeeding must not. */}
            <span className="min-w-0 flex-1" {...(status === "failed" ? { role: "alert" } : {})}>
              {/* ⚠ `static-400`, NOT `static-500`, for both. The tokens file states the rule on the
                  swatch itself — static-500 is "DISABLED-only + decorative glyphs (2.94:1 — fails
                  for info text)" — and a rung label is info text. The first draft used it for
                  `upcoming` to make an unreached rung recede, and axe caught it as a SERIOUS
                  colour-contrast violation in CI. static-400 is the dimmest legal info-text tone.
                  ⚠ So `upcoming` and `skipped` now share a colour, and that is fine: they are told
                  apart by the "— skipped (reason)" suffix, which is TEXT. Colour was never allowed
                  to be the only signal (§5.3), so leaning on it here was the actual mistake. */}
              <span className={cn((status === "upcoming" || status === "skipped") && "text-static-400")}>
                {status === "running" ? active : label}
              </span>
              {/* ⚠ The skip REASON is inline, not a tooltip and not omitted. A stage that silently
                  does not happen reads as broken — "Listen — skipped" invites a bug report,
                  "Listen — skipped (the description already says enough)" answers it. */}
              {status === "skipped" && note && <span className="text-static-400"> — skipped ({note})</span>}
              {status === "skipped" && !note && <span className="text-static-400"> — skipped</span>}
              {status === "failed" && note && <span className="text-onair-300"> — {note}</span>}
              <span className="sr-only">{`: ${SPOKEN[status]}`}</span>

              {/* ⚠ The bar renders ONLY when this rung actually measured something. `progress` is
                  -1 when the stage cannot measure itself, and only transcode can — Whisper and an
                  LLM turn are single opaque calls. A 0%-wide bar would read as "no progress"
                  rather than "no measurement", which is a different and false claim; the identical
                  distinction is recorded for `confidence === 0` on the ConfidenceMeter. Never
                  synthesise one from elapsed time. */}
              {status === "running" && row.progress >= 0 && (
                <Progress value={row.progress} label={`${active} — ${row.progress}%`} className="mt-1.5" />
              )}
            </span>
          </li>
        );
      })}
    </ol>
  );
};

export { ClipPipeline, copyFor };
