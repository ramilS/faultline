import { useCallback, useEffect, useState } from "react";

import { ApiError, type Group } from "@/api";
import { isNotFound, messageFor } from "@/errors";
import { toggleSelection } from "@/features/groups/memberSelection";
import { useAsyncResource, type AsyncState } from "@/hooks/useAsyncResource";

interface SelectionData<TItem> {
  readonly items: TItem[];
  readonly selectedIds: number[];
}

interface UseSelectionDialogArgs<TItem> {
  /** The group under edit, or null when the dialog is closed. */
  group: Group | null;
  /** Loads the candidate items plus the group's current selection among them. */
  load: (group: Group) => Promise<SelectionData<TItem>>;
  /** Issues the PUT. The applied set the server echoes back is not used for
   * rendering — the dialog closes on success and the caller's own `onUpdated`
   * re-fetches, so a server-side dedup never has a request-body value to
   * disagree with. */
  replace: (groupId: number, selectedIds: number[]) => Promise<number[]>;
  /** Shown when the server rejects the submitted set as 400 (a selected id no longer exists). */
  conflictMessage: string;
  onClose: () => void;
  onUpdated: () => void;
  onMissing: () => void;
}

export interface SelectionDialogController<TItem> {
  readonly state: AsyncState<SelectionData<TItem>>;
  reload: () => void;
  readonly selected: number[];
  toggle: (id: number) => void;
  readonly submitError: string | null;
  readonly submitting: boolean;
  submit: () => void;
  close: () => void;
}

/**
 * The shared shape behind `GroupMembersDialog` and `GroupProjectsDialog`:
 * load the candidates and the current selection, let the caller toggle
 * entries locally, then PUT the complete set on submit.
 *
 * Two behaviours the two call sites both depend on and must not regress:
 * - `close` resets the selection explicitly rather than relying on the
 *   parent nulling `group` to force a refetch that clears it — an unsaved
 *   selection surviving a cancel would reappear on the next open as if it
 *   were the stored membership, silently granting or revoking access.
 * - A 404 on submit means the group itself is gone; that funnels to
 *   `onMissing` (which the caller uses to refresh its list), never treated
 *   as a plain field error.
 */
export function useSelectionDialog<TItem>({
  group,
  load,
  replace,
  conflictMessage,
  onClose,
  onUpdated,
  onMissing
}: UseSelectionDialogArgs<TItem>): SelectionDialogController<TItem> {
  const loadForGroup = useCallback(async (): Promise<SelectionData<TItem>> => {
    if (group === null) {
      return { items: [], selectedIds: [] };
    }
    return load(group);
  }, [group, load]);

  const { state, reload } = useAsyncResource<SelectionData<TItem>>(loadForGroup);
  const [selected, setSelected] = useState<number[]>([]);
  const [submitError, setSubmitError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  // Adopt the fetched selection once per load. After that, toggling is local
  // state the effect must not stomp back to the server's last-known set.
  useEffect(() => {
    if (state.status === "ready") {
      setSelected(state.value.selectedIds);
    }
  }, [state]);

  const close = useCallback(() => {
    setSelected([]);
    setSubmitError(null);
    onClose();
  }, [onClose]);

  const toggle = useCallback((id: number) => {
    setSelected((current) => toggleSelection(current, id));
  }, []);

  const submit = useCallback(() => {
    if (group === null) {
      return;
    }
    setSubmitError(null);
    setSubmitting(true);
    void (async () => {
      try {
        await replace(group.id, selected);
        onUpdated();
        close();
      } catch (caught) {
        if (isNotFound(caught)) {
          onMissing();
          close();
          return;
        }
        if (caught instanceof ApiError && caught.status === 400) {
          setSubmitError(conflictMessage);
          return;
        }
        setSubmitError(messageFor(caught));
      } finally {
        setSubmitting(false);
      }
    })();
  }, [group, selected, replace, onUpdated, close, onMissing, conflictMessage]);

  return {
    state,
    reload: () => void reload(),
    selected,
    toggle,
    submitError,
    submitting,
    submit,
    close
  };
}
