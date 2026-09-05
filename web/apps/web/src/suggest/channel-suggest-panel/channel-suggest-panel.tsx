import * as proposalsApi from "@loomarr/api/endpoints/proposals";
import { toProblem } from "@loomarr/api/mutator";
import { useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useAuth } from "@/auth/use-auth";
import { ProposalReview } from "@/components/loomarr/ai/proposal-review";
import { ErrorState } from "@/components/loomarr/feedback/error-state";
import { GenerationProgress } from "@/components/loomarr/feedback/generation-progress";
import { Button, buttonVariants } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { IntentForm } from "../intent-form";
import { useElapsed } from "../use-elapsed";
import { useSuggestionRun } from "../use-suggestion-run";
import type { ChannelSuggestPanelProps } from "./channel-suggest-panel.type";

// ChannelSuggestPanel — origination, inline in the Guide header (§12). The create path IS
// describing a channel: IntentForm → useSuggestionRun → GenerationProgress → ProposalReview,
// and on approval it hands the new channel id back so the Guide navigates to it. It does NOT
// fork the flow or the approval gate: approve is the same admin-only useApproveProposal, and a
// member sees the review without the controls (§7/§11).
//
// This is now the app's ONLY origination surface — the standalone `/suggest` page folded away
// once this panel moved onto the channels surface. Nothing was stranded with it: the
// cross-user approval queue had already moved into `/queue`'s tabs (V27).
//
// One expanding surface over useSuggestionRun's three states: idle → describe form; running →
// live phases; a landed proposal → review with Approve/Deny. A successful approve or "Start
// another" resets back to the form.
const ChannelSuggestPanel = ({ onCreated, initialIntent, className }: ChannelSuggestPanelProps) => {
  const { isAdmin, user } = useAuth();
  const queryClient = useQueryClient();
  const run = useSuggestionRun();
  const elapsed = useElapsed(run.isRunning);
  const runProblem = run.error == null ? undefined : toProblem(run.error);
  const aiUnconfigured = runProblem?.type === "feature_not_configured";

  const approve = proposalsApi.useApproveProposal({
    mutation: {
      onSuccess: (res) => {
        void queryClient.invalidateQueries({ queryKey: proposalsApi.getListProposalsQueryKey() });
        // Approval atomically created (or patched) the local channel and returned its required
        // id — navigate there so the operator lands on the channel it just committed.
        if (res.status === 200) {
          run.reset();
          onCreated(res.data.channelId);
        }
      },
    },
  });
  const deny = proposalsApi.useDenyProposal({
    mutation: {
      onSuccess: () => {
        void queryClient.invalidateQueries({ queryKey: proposalsApi.getListProposalsQueryKey() });
        run.reset();
      },
    },
  });

  const proposal = run.proposal;

  return (
    <section className={cn("flex flex-col gap-4 rounded-lg border border-border p-4", className)}>
      <div>
        <h2 className="font-semibold text-lg">Add a channel</h2>
        <p className="text-muted-foreground text-sm">
          Describe the channel you want. Loomarr grounds every pick against your library and TMDB, then you
          review and approve before anything is built.
        </p>
      </div>

      {/* Idle — the describe form (with optional constraints). Suppressed while a run is in
          flight OR has failed: a failed run shows the failure below with its own way back,
          so falling through to a blank form here would swallow the error the user needs. */}
      {!run.isRunning && !run.failed && !proposal && (
        <IntentForm initialDescription={initialIntent} onSubmit={run.start} submitting={run.isRunning} />
      )}

      {run.error != null &&
        (aiUnconfigured ? (
          <div role="alert" className="rounded-lg border border-border bg-muted/40 p-4">
            <p className="font-medium">Connect AI to describe a channel</p>
            <p className="mt-1 text-muted-foreground text-sm">
              This needs a configured AI provider and a selected tool-capable lineup model. Your description
              is still here, so you can return and submit it after setup.
            </p>
            <Link to="/settings/ai" className={buttonVariants({ variant: "link", size: "sm" })}>
              Open AI settings
            </Link>
          </div>
        ) : (
          <ErrorState error={run.error} />
        ))}

      {/* Running — the live generation phases. */}
      {/* Before the first frame lands the model is already loading and thinking, so
          "reasoning" is the honest default. It used to fall back to "searching", which
          announced a library search that had not started and could not be the slow part. */}
      {run.isRunning && (
        <GenerationProgress phase={run.phase ?? "reasoning"} round={run.round} elapsedSeconds={elapsed} />
      )}

      {/* Failed — the job started but errored mid-flight (e.g. the AI provider is
          unreachable). GenerationProgress renders the failed step; we add the way back the
          component itself has no opinion on. Most failures here are an unconfigured/unreachable
          AI provider, so the hint points there. */}
      {run.failed && (
        <div className="flex flex-col gap-3">
          <GenerationProgress phase="failed" round={run.round} elapsedSeconds={elapsed} />
          <p className="text-muted-foreground text-sm">
            {run.failure?.message ?? "The run didn't finish. Try again in a moment."}
          </p>
          <div>
            {run.actions.includes("retry") && (
              <Button variant="outline" size="sm" onClick={run.retry}>
                Try again
              </Button>
            )}
            {run.actions.includes("edit") && (
              <Button variant="ghost" size="sm" onClick={run.reset}>
                Edit request
              </Button>
            )}
            {run.actions.includes("check_ai") && (
              <Link to="/settings/ai" className={buttonVariants({ variant: "link", size: "sm" })}>
                Check AI settings
              </Link>
            )}
          </div>
        </div>
      )}

      {/* A proposal landed — review + approve/deny (approve navigates to the new channel). */}
      {proposal && (
        <div className="flex flex-col gap-4">
          <ProposalReview
            proposal={proposal.proposal}
            status={proposal.status}
            busy={approve.isPending || deny.isPending}
            onApprove={isAdmin ? () => approve.mutate({ id: proposal.id, data: {} }) : undefined}
            onDeny={isAdmin ? (reason) => deny.mutate({ id: proposal.id, data: { reason } }) : undefined}
          />
          {(approve.error ?? deny.error) != null && (
            <p className="text-onair-300 text-sm">
              {toProblem(approve.error ?? deny.error).title ?? "That didn't go through. Try again."}
            </p>
          )}
          {proposal.status === "approved" ? (
            <div className="flex flex-col items-start gap-2">
              <p role="status" className="text-lock text-sm">
                {user?.autoApprove
                  ? "Automatically approved using your account setting. The channel has already been created."
                  : "This proposal is already approved. The channel has already been created."}
              </p>
              <Button variant="outline" size="sm" onClick={run.reset}>
                Create another
              </Button>
            </div>
          ) : (
            <Button variant="outline" size="sm" className="w-fit" onClick={run.reset}>
              Start over
            </Button>
          )}
        </div>
      )}
    </section>
  );
};

export { ChannelSuggestPanel };
