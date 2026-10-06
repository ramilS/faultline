import { useCallback, useEffect, useEffectEvent, useMemo, useState } from "react";
import { useSearchParams } from "react-router-dom";

import type { IssueLevel, IssueSort, IssueStatus } from "@/api";
import type { TimeRangeValue } from "@/features/issues/TimeRangePicker";
import { useDebounce } from "@/hooks/useDebounce";
import {
  changesUrl,
  CLEARED_FILTERS,
  filtersFromSearchParams,
  searchParamsFromFilters,
  type StreamFilters,
  type TagFilter
} from "@/lib/issueFilters";

const SEARCH_DEBOUNCE_MS = 300;

export interface UrlFilterControls {
  /** What the URL currently says, and therefore what is fetched. */
  readonly filters: StreamFilters;
  /** What the search input currently holds, ahead of the debounce. */
  readonly queryDraft: string;
  readonly setQueryDraft: (value: string) => void;
  readonly setStatuses: (value: readonly IssueStatus[]) => void;
  readonly setLevels: (value: readonly IssueLevel[]) => void;
  readonly setAssigned: (value: string) => void;
  readonly setEnvironments: (value: readonly string[]) => void;
  readonly setReleases: (value: readonly string[]) => void;
  readonly setTags: (value: readonly TagFilter[]) => void;
  readonly setSort: (value: IssueSort) => void;
  readonly setTimeRange: (value: TimeRangeValue) => void;
  readonly clear: () => void;
}

/**
 * Keeps the stream's filters in the URL, so a filtered view is a shareable
 * link and the back button moves between filter sets.
 *
 * The search box holds a local draft and reaches the URL on a trailing
 * debounce; every other control here is a discrete pick (a checkbox, a facet
 * value, a preset) and writes immediately, the same way status already did
 * before this file grew the rest of the filter set.
 */
export function useUrlFilters(): UrlFilterControls {
  const [searchParams, setSearchParams] = useSearchParams();
  const filters = useMemo(() => filtersFromSearchParams(searchParams), [searchParams]);

  const [queryDraft, setQueryDraft] = useState(filters.query);
  const debouncedQuery = useDebounce(queryDraft, SEARCH_DEBOUNCE_MS);

  const write = useCallback(
    (next: StreamFilters, replace: boolean) => {
      if (!changesUrl(filters, next)) {
        return;
      }
      setSearchParams(searchParamsFromFilters(next), { replace });
    },
    [filters, setSearchParams]
  );

  // The URL wins over a draft the user has stopped editing, so the back
  // button and a pasted link both restore the search input.
  useEffect(() => {
    setQueryDraft(filters.query);
  }, [filters.query]);

  /*
    The debounced query writer fires on the debounced text and nothing else.

    Listing `filters` as a dependency would re-commit a stale draft the instant
    the URL changed from anywhere else: "Clear filters" and the back button
    would each be undone in the same tick, because the debounced value still
    holds the text that was just cleared. `useEffectEvent` is that distinction —
    it reads the current `write` and `filters` without reacting to them, which
    is what this hook previously hand-rolled with a pair of refs and an effect
    to keep them fresh.

    `replace` is true because typing must not push a history entry per debounce,
    or the back button would undo the search one word at a time.
  */
  const commitQuery = useEffectEvent((query: string) => {
    write({ ...filters, query }, true);
  });

  useEffect(() => {
    commitQuery(debouncedQuery);
  }, [debouncedQuery]);

  const setStatuses = useCallback((statuses: readonly IssueStatus[]) => write({ ...filters, statuses }, false), [filters, write]);
  const setLevels = useCallback((levels: readonly IssueLevel[]) => write({ ...filters, levels }, false), [filters, write]);
  const setAssigned = useCallback((assigned: string) => write({ ...filters, assigned }, false), [filters, write]);
  const setEnvironments = useCallback(
    (environments: readonly string[]) => write({ ...filters, environments }, false),
    [filters, write]
  );
  const setReleases = useCallback((releases: readonly string[]) => write({ ...filters, releases }, false), [filters, write]);
  const setTags = useCallback((tags: readonly TagFilter[]) => write({ ...filters, tags }, false), [filters, write]);
  const setSort = useCallback((sort: IssueSort) => write({ ...filters, sort }, false), [filters, write]);
  const setTimeRange = useCallback((range: TimeRangeValue) => write({ ...filters, ...range }, false), [filters, write]);

  const clear = useCallback(() => write(CLEARED_FILTERS, false), [write]);

  return {
    filters,
    queryDraft,
    setQueryDraft,
    setStatuses,
    setLevels,
    setAssigned,
    setEnvironments,
    setReleases,
    setTags,
    setSort,
    setTimeRange,
    clear
  };
}
