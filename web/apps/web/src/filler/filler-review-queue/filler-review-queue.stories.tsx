import type { FillerDecisionReviewsOutputBody, FillerScreeningDTO } from "@loomarr/api";
import type { Decorator, Meta, StoryObj } from "@storybook/react-vite";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { withRouter } from "@/test/story-utils";
import { FillerReviewQueue } from "./filler-review-queue";

const row = (index: number) => ({
  id: `decision-${index}`,
  clipHash: `${"a".repeat(63)}${index % 10}`,
  applicationMode: "shadow" as const,
  createdAt: new Date().toISOString(),
  question: index % 2 === 0 ? "Is this a soda commercial?" : "Is this a programme promo?",
  reasonCodes: ["brand_category_conflict"],
  evidenceRefs: ["transcript", "frame-2"],
  conflicts: [
    {
      claim: "product category",
      values: index % 2 === 0 ? ["Mountain Dew", "unknown"] : ["network promo", "commercial"],
      evidenceRefs: ["transcript", "frame-2"],
      resolved: false,
    },
  ],
});

const screeningFor = (clipHash: string): FillerScreeningDTO => ({
  state: "available",
  clipHash,
  outcome: "pass",
  assessedAt: new Date().toISOString(),
  subjectSha256: "b".repeat(64),
  evidenceSha256: "c".repeat(64),
  axes: ["visual_safety", "spoken_safety", "written_safety", "rights", "playback_integrity"].map(
    (axis, index) => ({
      axis: axis as FillerScreeningDTO["axes"][number]["axis"],
      outcome: "pass",
      reasonCode: axis === "rights" ? "rights_verified" : "policy_clear",
      evidenceSha256: String(index + 1).repeat(64),
    }),
  ),
  rightsReview: {
    sourceId: "archive:classic-commercials",
    acquisitionId: "acq-mountain-dew-17",
    sourceMasterSha256: "d".repeat(64),
    policySha256: "4".repeat(64),
    use: "filler_broadcast",
    canRecord: true,
    currentGrant: {
      sha256: "e".repeat(64),
      sourceId: "archive:classic-commercials",
      acquisitionId: "acq-mountain-dew-17",
      sourceMasterSha256: "d".repeat(64),
      policySha256: "4".repeat(64),
      use: "filler_broadcast",
      status: "authorized",
      withdrawal: "clear",
      evidenceSha256: "f".repeat(64),
      actorId: "admin-1",
      effectiveAt: "2026-09-03T18:00:00Z",
      recordedAt: "2026-09-03T18:00:00Z",
    },
  },
  airworthiness: {
    schemaVersion: 1,
    contractVersion: "filler-airworthiness-decision-v1",
    subjectSha256: "b".repeat(64),
    profile: "all_ages",
    policyVersion: "filler-airworthiness-policy-v1",
    vocabularyVersion: "filler-airworthiness-vocabulary-v1",
    verdict: "pass",
    reasonCodes: ["airworthiness_evidence_satisfied"],
    observedFlags: [],
    heldAxes: [],
    triggers: [],
    evidenceSha256s: ["6".repeat(64), "7".repeat(64), "8".repeat(64)],
    authoritySha256: "9".repeat(64),
    decisionSha256: "0".repeat(64),
  },
});

const withReviews =
  (body: FillerDecisionReviewsOutputBody, screening?: FillerScreeningDTO): Decorator =>
  (Story) => {
    window.fetch = (async (input, init) => {
      const request = input instanceof Request ? input : new Request(input, init);
      const url = new URL(request.url);
      const response = (value: unknown) =>
        new Response(JSON.stringify(value), { status: 200, headers: { "content-type": "application/json" } });
      if (url.pathname === "/v1/filler/decisions/reviews") return response(body);
      if (url.pathname === "/v1/filler/screening") {
        const hash = url.searchParams.get("hash") ?? body.rows[0]?.clipHash ?? "";
        return response(screening ?? screeningFor(hash));
      }
      if (url.pathname === "/v1/filler") {
        const hashes = url.searchParams.getAll("hashes");
        return response({
          clips: hashes.map((hash, index) => ({
            hash,
            name:
              index === 0
                ? "Mountain Dew commercial"
                : index % 2 === 0
                  ? `Soda commercial ${index + 1}`
                  : `Station promo ${index + 1}`,
            kind: "commercial",
            durationMs: 30_000,
            tagged: true,
            aiTagged: true,
            playCount: 0,
            playsCounted: true,
          })),
          total: hashes.length,
        });
      }
      if (request.method === "POST") return response({ id: "story-action" });
      return new Response(null, { status: 404 });
    }) as typeof fetch;
    return (
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <Story />
      </QueryClientProvider>
    );
  };

const withReviewWidth: Decorator = (Story) => (
  <div style={{ width: 760, maxWidth: "calc(100vw - 32px)" }}>
    <Story />
  </div>
);

const meta = {
  title: "Filler/DecisionReviewQueue",
  component: FillerReviewQueue,
  decorators: [withReviewWidth, withRouter("/filler/incoming")],
} satisfies Meta<typeof FillerReviewQueue>;

export default meta;
type Story = StoryObj<typeof meta>;

export const GenuineReview: Story = { decorators: [withReviews({ rows: [row(1)], total: 1 })] };
export const Empty: Story = { decorators: [withReviews({ rows: [], total: 0 })] };
export const LargeQueue: Story = {
  decorators: [withReviews({ rows: Array.from({ length: 10 }, (_, index) => row(index)), total: 24 })],
  play: async ({ canvas }) => {
    await canvas.findByText("Station promo 10");
  },
};
export const Correction: Story = {
  decorators: [withReviews({ rows: [row(2)], total: 1 })],
  play: async ({ canvas, userEvent }) => {
    await userEvent.click(await canvas.findByRole("button", { name: "Correct answer" }));
    await canvas.findByLabelText("Correction");
  },
};

export const FiveAxisEvidence: Story = {
  decorators: [withReviews({ rows: [row(2)], total: 1 })],
  play: async ({ canvas, userEvent }) => {
    await userEvent.click(await canvas.findByRole("button", { name: "Review evidence" }));
    await canvas.findByText("Playback integrity");
  },
};

export const EvidenceDrift: Story = {
  decorators: [
    withReviews(
      { rows: [row(3)], total: 1 },
      {
        state: "unavailable",
        reasonCode: "screening_evidence_drift",
        clipHash: row(3).clipHash,
        subjectSha256: "b".repeat(64),
        evidenceSha256: "c".repeat(64),
        axes: [],
      },
    ),
  ],
  play: async ({ canvas, userEvent }) => {
    await userEvent.click(await canvas.findByRole("button", { name: "Review evidence" }));
    await canvas.findByText("Screening evidence unavailable");
  },
};

export const RightsRemediation: Story = {
  decorators: [
    withReviews(
      { rows: [row(4)], total: 1 },
      {
        ...screeningFor(row(4).clipHash),
        outcome: "hold",
        axes: screeningFor(row(4).clipHash).axes.map((axis) =>
          axis.axis === "rights" ? { ...axis, outcome: "hold", reasonCode: "rights_unknown" } : axis,
        ),
        rightsReview: {
          sourceId: "archive:classic-commercials",
          acquisitionId: "acq-mountain-dew-17",
          sourceMasterSha256: "d".repeat(64),
          policySha256: "4".repeat(64),
          use: "filler_broadcast",
          canRecord: true,
        },
      },
    ),
  ],
  play: async ({ canvas, userEvent }) => {
    await userEvent.click(await canvas.findByRole("button", { name: "Review evidence" }));
    await userEvent.click(await canvas.findByRole("button", { name: "Review rights" }));
    await canvas.findByLabelText("Private review file");
  },
};

const AnchoringPrototype = () => (
  <div className="space-y-4">
    <div>
      <h1 className="font-semibold text-xl">Review-ordering comparison</h1>
      <p className="text-muted-foreground text-sm">
        Prototype only. Production uses the evidence-first ordering on the left.
      </p>
    </div>
    <div className="grid gap-4 md:grid-cols-2">
      <Card className="p-5">
        <Badge variant="signal">Evidence first</Badge>
        <div className="mt-4 rounded-md border border-caution/35 bg-caution/5 p-3">
          <p className="font-medium text-sm">Conflicting product category</p>
          <p className="mt-1 text-muted-foreground text-sm">Mountain Dew · unknown</p>
        </div>
        <h2 className="mt-4 font-semibold text-lg">Is this a soda commercial?</h2>
        <div className="mt-4 flex gap-2">
          <Button>Accept</Button>
          <Button variant="outline">Correct</Button>
          <Button variant="ghost">Reject</Button>
        </div>
      </Card>
      <Card className="p-5">
        <Badge variant="caution">Proposal visible</Badge>
        <p className="mt-4 text-muted-foreground text-sm">Loomarr's proposed answer</p>
        <p className="font-semibold text-lg">Probably a soda commercial</p>
        <div className="mt-4 rounded-md border border-caution/35 bg-caution/5 p-3">
          <p className="font-medium text-sm">Conflicting product category</p>
          <p className="mt-1 text-muted-foreground text-sm">Mountain Dew · unknown</p>
        </div>
        <h2 className="mt-4 font-semibold text-lg">Is this a soda commercial?</h2>
        <div className="mt-4 flex gap-2">
          <Button>Accept</Button>
          <Button variant="outline">Correct</Button>
          <Button variant="ghost">Reject</Button>
        </div>
      </Card>
    </div>
  </div>
);

export const AnchoringComparison: Story = {
  render: () => <AnchoringPrototype />,
};
