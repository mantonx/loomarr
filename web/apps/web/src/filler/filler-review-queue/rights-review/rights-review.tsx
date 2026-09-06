import * as fillerApi from "@loomarr/api/endpoints/filler";
import { toProblem } from "@loomarr/api/mutator";
import { formatRelative } from "@loomarr/core/format";
import { useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { toast } from "sonner";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import type { RightsReviewProps } from "./rights-review.type";

const MAX_EVIDENCE_BYTES = 10 * 1024 * 1024;

type ReviewDecision = "authorized" | "prohibited" | "unknown" | "withdrawn";

const shortDigest = (digest: string) => `${digest.slice(0, 10)}…`;

const fileSHA256 = async (file: File) => {
  const digest = await crypto.subtle.digest("SHA-256", await file.arrayBuffer());
  return [...new Uint8Array(digest)].map((byte) => byte.toString(16).padStart(2, "0")).join("");
};

const RightsReview = ({ clipHash, review, screeningAssessedAt }: RightsReviewProps) => {
  const queryClient = useQueryClient();
  const [editing, setEditing] = useState(false);
  const [decision, setDecision] = useState<ReviewDecision>("authorized");
  const [validUntil, setValidUntil] = useState("");
  const [evidenceName, setEvidenceName] = useState("");
  const [evidenceSHA256, setEvidenceSHA256] = useState("");
  const [evidenceError, setEvidenceError] = useState("");
  const [hashing, setHashing] = useState(false);
  const [recorded, setRecorded] = useState(false);

  const record = fillerApi.useRecordFillerRightsGrant({
    mutation: {
      onSuccess: () => {
        setEditing(false);
        setEvidenceName("");
        setEvidenceSHA256("");
        setRecorded(true);
        toast.success("Rights decision recorded", {
          description: "The existing screen is unchanged. Re-run screening before this clip can advance.",
        });
        void queryClient.invalidateQueries({
          queryKey: fillerApi.getGetFillerScreeningQueryKey({ hash: clipHash }),
        });
      },
      onError: (error) => {
        const problem = toProblem(error);
        toast.error(problem.title ?? "The rights decision could not be recorded", {
          ...(problem.detail ? { description: problem.detail } : {}),
        });
      },
    },
  });

  const rewind = fillerApi.useRewindFillerClip({
    mutation: {
      onSuccess: () => {
        setRecorded(false);
        toast.success("Screening queued again", {
          description: "The clip stays held while every configured screen produces new evidence.",
        });
      },
      onError: (error) => {
        const problem = toProblem(error);
        toast.error(problem.title ?? "Screening could not be queued", {
          ...(problem.detail ? { description: problem.detail } : {}),
        });
      },
    },
  });

  const current = review.currentGrant;
  const grantNewerThanScreen = Boolean(
    current && screeningAssessedAt && Date.parse(current.recordedAt) > Date.parse(screeningAssessedAt),
  );
  const chooseEvidence = async (file?: File) => {
    setEvidenceError("");
    setEvidenceName("");
    setEvidenceSHA256("");
    if (!file) return;
    if (file.size === 0 || file.size > MAX_EVIDENCE_BYTES) {
      setEvidenceError("Choose a non-empty review file no larger than 10 MB.");
      return;
    }
    setHashing(true);
    try {
      setEvidenceSHA256(await fileSHA256(file));
      setEvidenceName(file.name);
    } catch {
      setEvidenceError("This browser could not fingerprint that file.");
    } finally {
      setHashing(false);
    }
  };

  const submit = () => {
    if (!evidenceSHA256) return;
    const withdrawn = decision === "withdrawn";
    const now = withdrawn ? new Date().toISOString() : undefined;
    record.mutate({
      data: {
        sourceId: review.sourceId,
        acquisitionId: review.acquisitionId,
        sourceMasterSha256: review.sourceMasterSha256,
        policySha256: review.policySha256,
        status: withdrawn ? "prohibited" : decision,
        withdrawal: withdrawn ? "withdrawn" : decision === "unknown" ? "unknown" : "clear",
        evidenceSha256: evidenceSHA256,
        ...(validUntil && decision === "authorized"
          ? { validUntil: new Date(`${validUntil}T23:59:59.999Z`).toISOString() }
          : {}),
        ...(withdrawn ? { effectiveAt: now, withdrawnAt: now } : {}),
        ...(current ? { supersedesSha256: current.sha256 } : {}),
      },
    });
  };

  return (
    <section aria-label="Current-use rights review" className="rounded-md border border-border p-3">
      <div className="flex flex-wrap items-start gap-3">
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <p className="font-medium text-sm">Current-use rights</p>
            <Badge variant="neutral">{current ? `Recorded: ${current.status}` : "No current grant"}</Badge>
          </div>
          <p className="mt-1 text-muted-foreground text-xs">
            {review.sourceId} · acquisition {review.acquisitionId}
            {current ? ` · recorded ${formatRelative(current.recordedAt)}` : ""}
            {current?.validUntil ? ` · expires ${formatRelative(current.validUntil)}` : ""}
            {current?.withdrawnAt ? ` · withdrawn ${formatRelative(current.withdrawnAt)}` : ""}
          </p>
          <p className="mt-1 font-mono text-2xs text-muted-foreground">
            Master {shortDigest(review.sourceMasterSha256)} · policy {shortDigest(review.policySha256)}
          </p>
        </div>
        {review.canRecord ? (
          <Button variant="outline" size="sm" onClick={() => setEditing((open) => !open)}>
            {editing ? "Cancel rights review" : current ? "Replace rights decision" : "Review rights"}
          </Button>
        ) : null}
      </div>

      {!review.canRecord ? (
        <p className="mt-3 text-caution text-sm">The rights registry is unavailable on this installation.</p>
      ) : null}

      {recorded || grantNewerThanScreen ? (
        <div className="mt-3 flex flex-wrap items-center gap-3 rounded-md border border-caution/35 bg-caution/5 p-3">
          <p className="min-w-0 flex-1 text-xs">
            The rights authority is newer than this immutable screen. Re-running may use configured models,
            compute, and provider budget; the clip remains held until it finishes.
          </p>
          <Button
            variant="outline"
            size="sm"
            disabled={rewind.isPending}
            onClick={() => rewind.mutate({ data: { hash: clipHash, from: "screen" } })}
          >
            Re-run screening
          </Button>
        </div>
      ) : null}

      {editing ? (
        <form
          className="mt-4 space-y-4 border-border border-t pt-4"
          onSubmit={(event) => {
            event.preventDefault();
            submit();
          }}
        >
          <div>
            <Label htmlFor={`rights-decision-${clipHash}`}>Decision</Label>
            <Select value={decision} onValueChange={(value) => setDecision(value as ReviewDecision)}>
              <SelectTrigger id={`rights-decision-${clipHash}`} className="mt-1 w-full sm:w-72">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="authorized">Authorized for filler broadcast</SelectItem>
                <SelectItem value="prohibited">Prohibited from filler broadcast</SelectItem>
                <SelectItem value="unknown">Evidence is still inconclusive</SelectItem>
                {current ? <SelectItem value="withdrawn">Withdraw the current rights</SelectItem> : null}
              </SelectContent>
            </Select>
          </div>

          {decision === "authorized" ? (
            <div className="max-w-72">
              <Label htmlFor={`rights-expiry-${clipHash}`}>Valid through (optional)</Label>
              <Input
                id={`rights-expiry-${clipHash}`}
                type="date"
                className="mt-1"
                value={validUntil}
                onChange={(event) => setValidUntil(event.target.value)}
              />
            </div>
          ) : null}

          <div>
            <Label htmlFor={`rights-evidence-${clipHash}`}>Private review file</Label>
            <Input
              id={`rights-evidence-${clipHash}`}
              type="file"
              className="mt-1 max-w-xl"
              onChange={(event) => void chooseEvidence(event.target.files?.[0])}
            />
            <p className="mt-1 text-muted-foreground text-xs">
              Loomarr fingerprints up to 10 MB in this browser. The file is not uploaded or stored.
            </p>
            {hashing ? <p className="mt-1 text-muted-foreground text-xs">Fingerprinting file…</p> : null}
            {evidenceName && evidenceSHA256 ? (
              <p className="mt-1 text-signal text-xs">
                {evidenceName} · {shortDigest(evidenceSHA256)}
              </p>
            ) : null}
            {evidenceError ? (
              <p role="alert" className="mt-1 text-onair-300 text-xs">
                {evidenceError}
              </p>
            ) : null}
          </div>

          <div className="rounded-md border border-caution/35 bg-caution/5 p-3 text-xs">
            This appends an immutable rights decision. It does not change the existing screen or admit the
            clip.
          </div>
          <Button type="submit" disabled={!evidenceSHA256 || hashing || record.isPending}>
            {current ? "Append replacement decision" : "Record rights decision"}
          </Button>
        </form>
      ) : null}
    </section>
  );
};

export { fileSHA256, RightsReview };
