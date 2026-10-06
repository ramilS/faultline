import { useCallback, useEffect, useState } from "react";

import type { IssueStatus, IssueSummary } from "@/api";
import { api } from "@/apiClient";
import { messageFor } from "@/errors";

const NOTHING_SELECTED: ReadonlySet<number> = new Set();

export interface BulkTriage {
  readonly selectedIds: ReadonlySet<number>;
  readonly busy: boolean;
  readonly error: string | null;
  readonly setSelected: (id: number, selected: boolean) => void;
  readonly setAllSelected: (ids: readonly number[], selected: boolean) => void;
  readonly clear: () => void;
  readonly apply: (status: IssueStatus) => void;
}

/**
 * Selection plus the bulk status change it feeds.
 *
 * There is no bulk endpoint, so this is one PATCH per issue. They are settled
 * rather than raced: a single failure must not discard the rows that did
 * succeed, and the rows that failed stay selected so the action can be retried.
 */
export function useBulkTriage(
  resetKey: string,
  applyUpdates: (issues: readonly IssueSummary[]) => void
): BulkTriage {
  const [selectedIds, setSelectedIds] = useState<ReadonlySet<number>>(NOTHING_SELECTED);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // A selection made under one filter set does not describe the rows shown
  // under another.
  useEffect(() => {
    setSelectedIds(NOTHING_SELECTED);
    setError(null);
  }, [resetKey]);

  const setSelected = useCallback((id: number, selected: boolean) => {
    setSelectedIds((current) => {
      const next = new Set(current);
      if (selected) {
        next.add(id);
      } else {
        next.delete(id);
      }
      return next;
    });
  }, []);

  const setAllSelected = useCallback((ids: readonly number[], selected: boolean) => {
    setSelectedIds(selected ? new Set(ids) : NOTHING_SELECTED);
  }, []);

  const clear = useCallback(() => {
    setSelectedIds(NOTHING_SELECTED);
    setError(null);
  }, []);

  const apply = useCallback(
    (status: IssueStatus) => {
      const ids = [...selectedIds];
      if (ids.length === 0 || busy) {
        return;
      }
      setBusy(true);
      setError(null);

      void Promise.allSettled(ids.map((id) => api.patchIssue(id, { status }))).then((results) => {
        applyUpdates(results.flatMap((result) => (result.status === "fulfilled" ? [result.value.issue] : [])));

        const failures = results.flatMap((result) =>
          result.status === "rejected" ? [messageFor(result.reason)] : []
        );
        setSelectedIds(new Set(ids.filter((_, index) => results[index].status === "rejected")));
        setError(
          failures.length === 0
            ? null
            : `${failures.length} of ${ids.length} could not be updated: ${failures[0]}`
        );
        setBusy(false);
      });
    },
    [applyUpdates, busy, selectedIds]
  );

  return { selectedIds, busy, error, setSelected, setAllSelected, clear, apply };
}
