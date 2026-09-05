import * as channelsApi from "@loomarr/api/endpoints/channels";
import { toProblem } from "@loomarr/api/mutator";
import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { MoreVertical, Pause, Pencil, Play, Trash2, Tv } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { useDeleteConfirm } from "../use-delete-confirm";
import type { ChannelRowMenu as ChannelRowMenuProps } from "./channel-row-menu.type";

// swallow — stop a click from bubbling to the row's <Link> (the whole card navigates). The
// TRIGGER lives inside that link, so without this, opening the menu would also route to the
// channel. preventDefault covers the Link's own navigation; stopPropagation covers any ancestor
// handler.
//
// ⚠ The menu ITEMS no longer need this, and that is a consequence of the port rather than an
// oversight: the popup is portalled to <body>, so a click on an item is not inside the link's
// subtree and has nothing to bubble through. Only the trigger is still nested.
const swallow = (e: React.MouseEvent) => {
  e.preventDefault();
  e.stopPropagation();
};

// ChannelRowMenu — the per-row ⋮ menu on the channels list: pause/resume and delete without
// opening the channel. Pause/resume is reversible → a single click. Delete is irreversible →
// a two-step confirm (arm, then execute) via the shared useDeleteConfirm hook — the SAME gate
// the detail page's ChannelDangerZone uses, so the confirm flow lives in one place.
//
// ⚠ THE PANEL IS STILL PORTALLED, and that is not a preference — it is the only thing that works.
// On the guide, this menu renders inside a virtualized row whose `<li>` carries
// `transform: translateY(...)`. A non-`none` transform CREATES A STACKING CONTEXT, so the panel's
// z-index was scoped to its own row and every later row painted straight over it: the menu
// rendered as a ~40px sliver with its labels overpainted, and no z-index value could have fixed
// it. The grid's `overflow-auto` clipped what survived. The difference now is that the primitive
// owns the portal instead of this file.
//
// ⚠ WHAT THIS FILE USED TO CARRY, and no longer does (V50b). It hand-rolled the app's only
// `createPortal`, measured the trigger with `getBoundingClientRect` in a layout effect, clamped
// both axes against hardcoded `PANEL_W = 236` / `PANEL_MAX_H = 210` constants, flipped upward when
// the bottom would overflow, rendered `invisible` until measured to avoid a 0,0 flash, listened to
// capture-phase `scroll`/`resize` to DISMISS itself (a portalled panel cannot follow its anchor),
// and hand-wrote `role="menu"` / `role="menuitem"` / `aria-haspopup` / `aria-expanded` plus a
// full-bleed <button> backdrop for outside-click. All of that is the positioner's job.
//
// Two of those were latent bugs rather than merely verbose: the pixel constants went stale the
// moment the menu's content changed (the confirm panel is the tallest state, and nothing enforced
// the 210), and dismissing on scroll was a workaround for an anchor the panel could not track —
// the popup now FOLLOWS the trigger instead of closing. The menu also gains what it never had:
// arrow-key navigation, typeahead, a focus trap, focus restore to the trigger, and Escape.
const ChannelRowMenu = ({ channel }: ChannelRowMenuProps) => {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const { confirming, arm, reset: resetConfirm } = useDeleteConfirm();
  const paused = channel.status === "paused";

  const invalidate = () => {
    void queryClient.invalidateQueries({ queryKey: channelsApi.getListChannelsQueryKey() });
    // Guide rows come from their own time-windowed read model. Passing the prefix key
    // invalidates every currently cached window, so a successful delete disappears from
    // the view that issued it instead of waiting for a reload or a later SSE frame.
    void queryClient.invalidateQueries({ queryKey: channelsApi.getChannelGuideQueryKey() });
  };

  const update = channelsApi.useUpdateChannel({
    mutation: {
      onSuccess: () => {
        invalidate();
        toast.success(paused ? "Channel resumed" : "Channel paused");
        resetConfirm();
      },
      onError: (e) => {
        invalidate();
        toast.error(toProblem(e).title ?? "Couldn't update the channel");
      },
    },
  });

  const del = channelsApi.useDeleteChannel({
    mutation: {
      onSuccess: () => {
        invalidate();
        toast.success("Channel deleted");
        resetConfirm();
      },
      onError: (e) => toast.error(toProblem(e).title ?? "Couldn't delete the channel"),
    },
  });

  const busy = update.isPending || del.isPending;

  // Guide rows intentionally carry only timeline data, not the editable channel resource.
  // Read the resource at click time so the PATCH is based on the revision the operator is
  // actually changing, rather than inventing a revision or trusting an old guide snapshot.
  const togglePaused = async () => {
    const current = await channelsApi.getChannel(channel.id);
    if (current.status !== 200) {
      toast.error("Couldn't read the latest channel");
      return;
    }
    update.mutate({
      id: channel.id,
      data: { revision: current.data.revision, status: paused ? "building" : "paused" },
    });
  };

  return (
    <div className="relative shrink-0">
      {/* Closing always disarms the confirm: reopening the menu on a channel you nearly deleted
          should not present the armed Delete button again. */}
      <DropdownMenu
        onOpenChange={(next) => {
          if (!next) resetConfirm();
        }}
      >
        <DropdownMenuTrigger
          render={
            <Button
              variant="ghost"
              size="icon"
              className="size-8"
              aria-label={`Actions for ${channel.name}`}
              onClick={swallow}
            />
          }
        >
          <MoreVertical className="size-4" aria-hidden />
        </DropdownMenuTrigger>

        <DropdownMenuContent align="start" className="w-59 gap-0.5 p-1.5">
          {/* Watch — FIRST, matching the mock's guide menu: the everyday reason to open a channel
              from the guide is to watch it. Routes straight to the Watch surface (§9.1); a paused
              channel still opens it (the surface shows the "off air" poster and explains, rather
              than the menu guessing). */}
          <DropdownMenuItem
            onClick={() => void navigate({ to: "/channels/$id/watch", params: { id: channel.id } })}
          >
            <Tv className="size-4 text-signal" aria-hidden />
            Watch
          </DropdownMenuItem>

          {/* Edit channel — the row's own click target opens the channel too, but a menu whose only
              options are Pause and Delete reads as a destructive-actions menu. */}
          <DropdownMenuItem
            onClick={() => void navigate({ to: "/channels/$id", params: { id: channel.id } })}
          >
            <Pencil className="size-4 text-static-400" aria-hidden />
            Edit channel
          </DropdownMenuItem>

          {/* Pause / Resume — reversible, single click. */}
          <DropdownMenuItem disabled={busy} onClick={() => void togglePaused()}>
            {paused ? <Play className="size-4" aria-hidden /> : <Pause className="size-4" aria-hidden />}
            {paused ? "Resume" : "Pause"}
          </DropdownMenuItem>

          {/* Delete — a FULL removal (purge: the channel leaves the list AND Tunarr), so a deleted
              row actually disappears rather than lingering as a "detached" record. Irreversible →
              a two-step confirm, same gate as ChannelDangerZone.
              ⚠ `closeOnClick={false}`: arming must leave the menu open, or the confirm it arms
              would unmount in the same tick. */}
          {!confirming ? (
            <DropdownMenuItem
              disabled={busy}
              closeOnClick={false}
              onClick={arm}
              className="text-onair-300 hover:bg-onair-tint-15 data-[highlighted]:bg-onair-tint-15 data-[highlighted]:text-onair-300"
            >
              <Trash2 className="size-4" aria-hidden />
              Delete…
            </DropdownMenuItem>
          ) : (
            <div className="flex flex-col gap-2 rounded border border-onair-tint-15 bg-onair-tint-15 p-2">
              <p className="text-xs">Delete {channel.name} for good? This can't be undone.</p>
              <div className="flex items-center gap-2">
                <Button
                  variant="destructive"
                  size="sm"
                  disabled={busy}
                  // purge: a Delete from the LIST should remove the channel outright, not leave a
                  // detached record behind (the maintainer's choice). The detail-page danger zone
                  // still offers detach-vs-purge for the finer distinction.
                  onClick={() => del.mutate({ id: channel.id, params: { purge: true } })}
                >
                  Delete
                </Button>
                <Button variant="ghost" size="sm" disabled={busy} onClick={resetConfirm}>
                  Cancel
                </Button>
              </div>
            </div>
          )}
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  );
};

export { ChannelRowMenu };
