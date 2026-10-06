import { useCallback } from "react";

import type { Group, Project } from "@/api";
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

interface GroupProjectsDialogProps {
  /** The group under edit, or null when the dialog is closed. */
  group: Group | null;
  onClose: () => void;
  onUpdated: () => void;
  onMissing: () => void;
}

/**
 * Every project checked here is reachable by every member of this group —
 * the PUT this dialog issues sends the complete project set, so
 * `toggleSelection` (the tested unit behind `useSelectionDialog`) is the only
 * thing standing between a misclick and a silently granted or revoked access.
 */
export function GroupProjectsDialog({ group, onClose, onUpdated, onMissing }: GroupProjectsDialogProps) {
  const load = useCallback(async (target: Group) => {
    const [projects, detail] = await Promise.all([api.listProjects(), api.getGroup(target.id)]);
    return { items: projects, selectedIds: detail.project_ids };
  }, []);

  const dialog = useSelectionDialog<Project>({
    group,
    load,
    replace: (id, projectIds) => api.replaceGroupProjects(id, projectIds),
    conflictMessage: "One or more selected projects no longer exist. Refresh and try again.",
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
            <DialogTitle>Projects for {group.name}</DialogTitle>
            <DialogDescription>Every member of this group reaches everything checked here.</DialogDescription>
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
                <p className="text-muted-foreground text-sm">There are no projects to add yet.</p>
              )}
              {dialog.state.value.items.map((project) => (
                <label
                  key={project.id}
                  className="hover:bg-accent/40 flex items-center gap-2 rounded-md px-2 py-1.5 text-sm"
                >
                  <Checkbox
                    checked={dialog.selected.includes(project.id)}
                    onCheckedChange={() => dialog.toggle(project.id)}
                    aria-label={`Include ${project.name}`}
                  />
                  <span className="truncate font-medium">{project.name}</span>
                  <span className="text-muted-foreground truncate font-mono text-xs">{project.slug}</span>
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
