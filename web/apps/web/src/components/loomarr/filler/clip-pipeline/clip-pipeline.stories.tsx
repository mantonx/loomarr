import type { IncomingPipelineDTO } from "@loomarr/api";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { ClipPipeline } from "./clip-pipeline";

// ClipPipeline — where one clip is in the ten-rung ingest pipeline (§10 V51b/V51e). The strip
// is the stage-by-stage watch for forty rows at once; the list is what expanding one gives you.
const LADDER = [
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
];

const at = (over: Partial<IncomingPipelineDTO> = {}): IncomingPipelineDTO => ({
  stage: "tag",
  status: "running",
  lifecycle: "in_progress",
  progress: -1,
  stages: [
    { stage: "probe", status: "done", at: "2026-08-08T10:00:00Z" },
    { stage: "transcode", status: "done", at: "2026-08-08T10:00:20Z" },
    {
      stage: "split",
      status: "skipped",
      note: "it is a single advert, not a compilation",
      at: "2026-08-08T10:00:21Z",
    },
    { stage: "language", status: "done", at: "2026-08-08T10:00:40Z" },
    {
      stage: "transcribe",
      status: "skipped",
      note: "the description already says enough",
      at: "2026-08-08T10:00:41Z",
    },
  ],
  updatedAt: "2026-08-08T10:01:00Z",
  ...over,
});

const meta = {
  title: "Filler/ClipPipeline",
  component: ClipPipeline,
  args: { ladder: LADDER, name: "Coca-Cola 1985", row: at() },
  parameters: { layout: "padded" },
} satisfies Meta<typeof ClipPipeline>;

type Story = StoryObj<typeof meta>;

// The collapsed summary. ⚠ Eight pips regardless of how far the clip has got — the ladder comes
// from the server's `stageOrder`, not from the visited records, so the operator can see how much
// is left rather than watching a strip grow.
const Strip: Story = {};

// ⚠ Every rung of the strip, at every state, in one image. Four clips is what forty looks like
// in miniature, and the point is that the states are distinguishable at a glance without colour
// being the only signal.
const StripStates: Story = {
  render: () => (
    <div className="flex flex-col gap-3">
      {[
        { label: "just enrolled", row: at({ stage: "probe", status: "queued", stages: [] }) },
        {
          label: "measuring, mid-transcode",
          row: at({
            stage: "transcode",
            progress: 62,
            status: "running",
            stages: [{ stage: "probe", status: "done", at: "" }],
          }),
        },
        { label: "working out what it is", row: at() },
        {
          label: "a rung failed",
          row: at({
            stage: "vision",
            status: "failed",
            stages: [...(at().stages ?? []), { stage: "tag", status: "done", at: "" }],
          }),
        },
      ].map(({ label, row }) => (
        <div key={label} className="flex items-center gap-4">
          <span className="w-48 shrink-0 text-static-400 text-xs">{label}</span>
          <ClipPipeline row={row} name={label} ladder={LADDER} />
        </div>
      ))}
    </div>
  ),
};

// The expanded ladder. ⚠ Skips carry their REASON inline: a stage that silently does not happen
// reads as broken, and "Listen — skipped" invites the bug report that "(the description already
// says enough)" answers.
const List: Story = { args: { variant: "list" } };

// ⚠ The bar appears ONLY on a rung that measured something. Transcode is the only stage that can
// — ffmpeg reports out_time against a known duration — so a running `tag` shows the spinner alone.
// A 0%-wide bar would claim "no progress" where the truth is "no measurement".
const ListWithAMeasuredRung: Story = {
  args: {
    variant: "list",
    row: at({
      progress: 62,
      stage: "transcode",
      stages: [{ stage: "probe", status: "done", at: "" }],
      status: "running",
    }),
  },
};

const ListWithAFailure: Story = {
  args: {
    variant: "list",
    row: at({
      stage: "vision",
      status: "failed",
      stages: [...(at().stages ?? []), { stage: "tag", status: "done", at: "" }],
    }),
  },
};

export default meta;
export { List, ListWithAFailure, ListWithAMeasuredRung, Strip, StripStates };
