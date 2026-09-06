import type { PullDTO } from "@loomarr/api/models/pullDTO";

interface PullCardProps {
  // The server's answer, verbatim (contract 1:1).
  pull: PullDTO;
  // Approve carries candidate exclusions and an annotation for the approval record.
  // The exclusions control what is fetched; the note does not change the selection.
  // This is the commit point: it is the only path on which a pull downloads anything.
  onApprove: (edits: { dropCandidateIds: string[]; note: string }) => void;
  onDismiss: () => void;
  // A decision is in flight, so both buttons say so rather than looking inert.
  deciding?: boolean;
  className?: string;
}

export type { PullCardProps };
