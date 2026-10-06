import { useCallback } from "react";

import type { Group, ManagedUser } from "@/api";
import { api } from "@/apiClient";
import { ErrorState } from "@/components/ErrorState";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle
} from "@/components/ui/dialog";
import { Skeleton } from "@/components/ui/skeleton";
import { useSelectionDialog } from "@/features/groups/useSelectionDialog";

interface GroupMembersDialogProps {
  /** The group under edit, or null when the dialog is closed. */
  group: Group | null;
  onClose: () => void;
  onUpdated: () => void;
  onMissing: () => void;
}

/**
 * Every user checked here is a user who can reach every project this group
 * covers — the PUT this dialog issues sends the complete membership set, so
 * `toggleSelection` (the tested unit behind `useSelectionDialog`) is the only
 * thing standing between a misclick and a silently granted or revoked access.
 */
export function GroupMembersDialog({ group, onClose, onUpdated, onMissing }: GroupMembersDialogProps) {
  const load = useCallback(async (target: Group) => {
    const [users, detail] = await Promise.all([api.listUsers(), api.getGroup(target.id)]);
    return { items: users, selectedIds: detail.user_ids };
  }, []);

  const dialog = useSelectionDialog<ManagedUser>({
    group,
    load,
    replace: (id, userIds) => api.replaceGroupMembers(id, userIds),
    conflictMessage: "One or more selected users no longer exist. Refresh and try again.",
    onClose,
    onUpdated,
    onMissing
  });

  return (
    <Dialog
      open={group !== null}
      onOpenChange={(next) => {
        if (!next) dialog.close();
      }}
    >
      {group !== null && (
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Members of {group.name}</DialogTitle>
            <DialogDescription>Everyone checked reaches every project this group covers.</DialogDescription>
          </DialogHeader>

          {dialog.state.status === "loading" && (
            <div className="space-y-2 py-2">
              {[0, 1, 2].map((row) => (
                <Skeleton key={row} className="h-8 w-full" />
              ))}
            </div>
          )}

          {dialog.state.status === "failed" && (
            <ErrorState message={dialog.state.message} onRetry={dialog.reload} />
          )}

          {dialog.state.status === "ready" && (
            <div className="max-h-72 space-y-1 overflow-y-auto py-2">
              {dialog.state.value.items.length === 0 && (
                <p className="text-muted-foreground text-sm">There are no users to add yet.</p>
              )}
              {dialog.state.value.items.map((user) => (
                <label
                  key={user.id}
                  className="hover:bg-accent/40 flex items-center gap-2 rounded-md px-2 py-1.5 text-sm"
                >
                  <Checkbox
                    checked={dialog.selected.includes(user.id)}
                    onCheckedChange={() => dialog.toggle(user.id)}
                    aria-label={`Include ${user.email}`}
                  />
                  <span className="truncate font-medium">{user.name}</span>
                  <span className="text-muted-foreground truncate text-xs">{user.email}</span>
                </label>
              ))}
            </div>
          )}

          {dialog.submitError !== null && (
            <p role="alert" className="text-destructive text-sm">
              {dialog.submitError}
            </p>
          )}

          <DialogFooter>
            <Button type="button" variant="outline" onClick={dialog.close}>
              Cancel
            </Button>
            <Button
              type="button"
              disabled={dialog.submitting || dialog.state.status !== "ready"}
              onClick={dialog.submit}
            >
              {dialog.submitting ? "Saving…" : "Save"}
            </Button>
          </DialogFooter>
        </DialogContent>
      )}
    </Dialog>
  );
}
