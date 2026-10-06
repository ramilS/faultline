import { Pencil, Plus, Trash2, UsersRound } from "lucide-react";
import { useCallback, useEffect, useState, type FormEvent } from "react";
import { toast } from "sonner";

import { ApiError, type Group } from "@/api";
import { api } from "@/apiClient";
import { EmptyState } from "@/components/EmptyState";
import { ErrorState } from "@/components/ErrorState";
import { PageHeader } from "@/components/PageHeader";
import { RelativeTime } from "@/components/RelativeTime";
import { STACKED_ROWS_BELOW_MD } from "@/components/stackedTable";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { messageFor } from "@/errors";
import { validateGroupName } from "@/features/groups/groupFormValidation";
import { GroupMembersDialog } from "@/features/groups/GroupMembersDialog";
import { GroupProjectsDialog } from "@/features/groups/GroupProjectsDialog";
import { useAsyncResource } from "@/hooks/useAsyncResource";
import { useDocumentTitle } from "@/hooks/useDocumentTitle";

/**
 * Owner-only group administration. Reachable only through `RequireOwner`
 * (see `web/src/app/RequireOwner.tsx`) and the owner-gated nav entry in
 * `navItems.ts` — this component assumes it is already an owner's screen.
 *
 * There is deliberately no grandfathering group: a fresh group starts with
 * zero members and zero projects, and that empty state renders like any
 * other row rather than an error (spec: an empty group is a normal state).
 */
export function GroupsPage() {
  useDocumentTitle("Groups");
  const { state, reload } = useAsyncResource<Group[]>(useCallback(() => api.listGroups(), []));
  const [formTarget, setFormTarget] = useState<GroupFormTarget | null>(null);
  const [membersTarget, setMembersTarget] = useState<Group | null>(null);
  const [projectsTarget, setProjectsTarget] = useState<Group | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<Group | null>(null);

  // A group can be deleted out from under an open dialog by another admin
  // session. There is nothing to reconcile in that case — the list itself is
  // the source of truth — so every "the group is gone" path funnels here
  // rather than showing a dead-end field error.
  const handleMissingGroup = useCallback(() => {
    toast.error("This group no longer exists.", { description: "Refreshing the list." });
    void reload({ silent: true });
  }, [reload]);

  return (
    <>
      <PageHeader title="Groups" description="A group grants its members access to every project it covers.">
        <Button size="sm" onClick={() => setFormTarget({ group: null })}>
          <Plus className="size-4" aria-hidden="true" />
          New group
        </Button>
      </PageHeader>

      <div className="space-y-4 p-4 sm:p-6">
        {state.status === "loading" && (
          <div className="space-y-2">
            {[0, 1, 2].map((row) => (
              <Skeleton key={row} className="h-12 w-full" />
            ))}
          </div>
        )}

        {state.status === "failed" && <ErrorState message={state.message} onRetry={() => void reload()} />}

        {state.status === "ready" && state.value.length === 0 && (
          <EmptyState
            icon={UsersRound}
            title="No groups yet"
            description="Create a group, then add members and projects to it so those members can reach them."
            action={
              <Button size="sm" onClick={() => setFormTarget({ group: null })}>
                <Plus className="size-4" aria-hidden="true" />
                New group
              </Button>
            }
          />
        )}

        {state.status === "ready" && state.value.length > 0 && (
          <Table className={STACKED_ROWS_BELOW_MD}>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Members</TableHead>
                <TableHead>Projects</TableHead>
                <TableHead>Created</TableHead>
                <TableHead className="w-0 text-right">
                  <span className="sr-only">Actions</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {state.value.map((group) => (
                <TableRow key={group.id}>
                  <TableCell className="font-medium">{group.name}</TableCell>
                  <TableCell>
                    <Button variant="outline" size="sm" onClick={() => setMembersTarget(group)}>
                      {group.member_count} {group.member_count === 1 ? "member" : "members"}
                    </Button>
                  </TableCell>
                  <TableCell>
                    <Button variant="outline" size="sm" onClick={() => setProjectsTarget(group)}>
                      {group.project_count} {group.project_count === 1 ? "project" : "projects"}
                    </Button>
                  </TableCell>
                  <TableCell>
                    <RelativeTime iso={group.created_at} className="text-muted-foreground text-xs" />
                  </TableCell>
                  <TableCell className="text-right">
                    <div className="flex justify-end gap-2">
                      <Button
                        type="button"
                        variant="outline"
                        size="sm"
                        onClick={() => setFormTarget({ group })}
                        aria-label={`Rename ${group.name}`}
                      >
                        <Pencil aria-hidden="true" />
                      </Button>
                      <Button
                        type="button"
                        variant="outline"
                        size="sm"
                        className="text-destructive hover:text-destructive"
                        onClick={() => setDeleteTarget(group)}
                        aria-label={`Delete ${group.name}`}
                      >
                        <Trash2 aria-hidden="true" />
                      </Button>
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </div>

      <GroupFormDialog
        target={formTarget}
        onClose={() => setFormTarget(null)}
        onSaved={() => void reload({ silent: true })}
        onMissing={handleMissingGroup}
      />
      <GroupMembersDialog
        group={membersTarget}
        onClose={() => setMembersTarget(null)}
        onUpdated={() => void reload({ silent: true })}
        onMissing={handleMissingGroup}
      />
      <GroupProjectsDialog
        group={projectsTarget}
        onClose={() => setProjectsTarget(null)}
        onUpdated={() => void reload({ silent: true })}
        onMissing={handleMissingGroup}
      />
      <DeleteGroupDialog
        target={deleteTarget}
        onClose={() => setDeleteTarget(null)}
        onDeleted={() => void reload({ silent: true })}
        onMissing={handleMissingGroup}
      />
    </>
  );
}

interface GroupFormTarget {
  /** null selects create mode; a group selects rename mode, pre-filled. */
  readonly group: Group | null;
}

interface GroupFormDialogProps {
  target: GroupFormTarget | null;
  onClose: () => void;
  onSaved: () => void;
  onMissing: () => void;
}

function GroupFormDialog({ target, onClose, onSaved, onMissing }: GroupFormDialogProps) {
  const [name, setName] = useState("");
  const [fieldError, setFieldError] = useState<string | null>(null);
  const [submitError, setSubmitError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const editing = target?.group ?? null;

  // Re-seed the field from the target every time a new one opens, so the
  // previous group's name (or a half-typed create) never flashes into view.
  useEffect(() => {
    setName(editing?.name ?? "");
    setFieldError(null);
    setSubmitError(null);
  }, [editing]);

  const close = () => {
    setSubmitError(null);
    setFieldError(null);
    onClose();
  };

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    const validationError = validateGroupName(name);
    if (validationError !== null) {
      setFieldError(validationError);
      return;
    }
    setFieldError(null);
    setSubmitError(null);
    setSubmitting(true);
    try {
      const trimmed = name.trim();
      if (editing !== null) {
        await api.renameGroup(editing.id, { name: trimmed });
      } else {
        await api.createGroup({ name: trimmed });
      }
      onSaved();
      close();
    } catch (caught) {
      // A group renamed/deleted underneath this dialog has nothing left to
      // rename — refetch the list instead of asking the user to fix a name
      // that no longer refers to anything.
      if (editing !== null && caught instanceof ApiError && caught.status === 404) {
        onMissing();
        close();
        return;
      }
      // Both a duplicate name (409) and a blank/invalid one (400) land here
      // as a message that stays on the dialog next to the field it concerns,
      // rather than a toast the user might miss before it disappears.
      setSubmitError(messageFor(caught));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Dialog
      open={target !== null}
      onOpenChange={(next) => {
        if (!next) close();
      }}
    >
      <DialogContent>
        <form onSubmit={submit}>
          <DialogHeader>
            <DialogTitle>{editing !== null ? `Rename ${editing.name}` : "New group"}</DialogTitle>
            <DialogDescription>
              {editing !== null
                ? "Its members and projects are unaffected."
                : "Add members and projects to it once it is created."}
            </DialogDescription>
          </DialogHeader>

          <div className="space-y-4 py-4">
            {submitError !== null && (
              <p role="alert" className="text-destructive text-sm">
                {submitError}
              </p>
            )}

            <div className="space-y-2">
              <Label htmlFor="group-name">Name</Label>
              <Input
                id="group-name"
                value={name}
                onChange={(event) => setName(event.target.value)}
                autoFocus
                aria-invalid={fieldError !== null}
                aria-describedby={fieldError === null ? undefined : "group-name-error"}
              />
              {fieldError !== null && (
                <p id="group-name-error" role="alert" className="text-destructive text-xs">
                  {fieldError}
                </p>
              )}
            </div>
          </div>

          <DialogFooter>
            <Button type="button" variant="outline" onClick={close}>
              Cancel
            </Button>
            <Button type="submit" disabled={submitting}>
              {submitting ? "Saving…" : editing !== null ? "Save" : "Create group"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

interface DeleteGroupDialogProps {
  target: Group | null;
  onClose: () => void;
  onDeleted: () => void;
  onMissing: () => void;
}

function DeleteGroupDialog({ target, onClose, onDeleted, onMissing }: DeleteGroupDialogProps) {
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const close = () => {
    setError(null);
    onClose();
  };

  const remove = async (group: Group) => {
    setError(null);
    setSubmitting(true);
    try {
      await api.deleteGroup(group.id);
      onDeleted();
      close();
    } catch (caught) {
      if (caught instanceof ApiError && caught.status === 404) {
        // Already gone is the outcome the user wanted, not a failure.
        onMissing();
        close();
        return;
      }
      setError(messageFor(caught));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Dialog
      open={target !== null}
      onOpenChange={(next) => {
        if (!next) close();
      }}
    >
      {target !== null && (
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Delete {target.name}?</DialogTitle>
            <DialogDescription>
              Its {target.member_count} {target.member_count === 1 ? "member" : "members"} immediately lose access
              to the {target.project_count} {target.project_count === 1 ? "project" : "projects"} it covered,
              unless another group also grants it. This cannot be undone.
            </DialogDescription>
          </DialogHeader>

          {error !== null && (
            <p role="alert" className="text-destructive text-sm">
              {error}
            </p>
          )}

          <DialogFooter>
            <Button type="button" variant="outline" onClick={close}>
              Cancel
            </Button>
            <Button type="button" variant="destructive" disabled={submitting} onClick={() => void remove(target)}>
              {submitting ? "Deleting…" : "Delete"}
            </Button>
          </DialogFooter>
        </DialogContent>
      )}
    </Dialog>
  );
}
