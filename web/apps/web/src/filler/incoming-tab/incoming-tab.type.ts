// The Incoming tab owns everything it needs, so it takes nothing but the one thing the SHELL
// already knows and it cannot re-derive without a second request: whether tagging a clip should
// open the shared dialog. Everything else — the queue, the filing mutations, the busy row — is
// this tab's own concern and lives inside it.
interface IncomingTabProps {
  // onEditTags opens the shell's ClipTagDialog. The dialog is shared with the Catalog tab (one
  // clip, one editor, wherever you reached it from), so the shell owns it and this hands the
  // clip's identity up rather than mounting a second copy.
  //
  // ⚠ The HASH, not the path (§10 V54). The shell resolves the clip by identity — the Catalog's
  // rows are keyed that way — so handing it a path matched nothing and no dialog opened. The
  // parameter was named `path` and typed `string`, so neither the compiler nor the test that
  // asserted `toHaveBeenCalledWith(ASK.path)` could see the mismatch.
  onEditTags: (hash: string) => void;

  /** Exact clips already represented by the semantic review cards above this conveyor. */
  excludedHashes?: ReadonlySet<string>;

  /** Semantic exceptions rendered above the intake conveyor, included in its shared status. */
  semanticReviewCount?: number;
}

export type { IncomingTabProps };
