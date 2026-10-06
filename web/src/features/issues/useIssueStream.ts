import { useCallback, useEffect, useRef, useState } from "react";

import type { IssueSummary } from "@/api";
import { api } from "@/apiClient";
import { isNotFound, messageFor } from "@/errors";
import { issueRequestFrom, streamKey, type StreamFilters } from "@/lib/issueFilters";
import { appendPage, emptyStream, replaceIssues, type IssueStream } from "@/lib/issueStream";

const PAGE_SIZE = 50;

export type StreamPhase =
  | { readonly kind: "loading" }
  | { readonly kind: "ready" }
  | {
      readonly kind: "failed";
      readonly message: string;
      /** The failure is the access-boundary 404 — render the not-found route, not Retry. */
      readonly notFound: boolean;
    };

export interface IssueStreamController {
  readonly phase: StreamPhase;
  readonly stream: IssueStream;
  readonly loadingMore: boolean;
  /** A failed "load more" must not blank the pages already on screen. */
  readonly loadMoreError: string | null;
  readonly loadMore: () => void;
  readonly reload: () => void;
  readonly applyUpdates: (issues: readonly IssueSummary[]) => void;
}

export function useIssueStream(slug: string, filters: StreamFilters): IssueStreamController {
  const [phase, setPhase] = useState<StreamPhase>({ kind: "loading" });
  const [loadingMore, setLoadingMore] = useState(false);
  const [loadMoreError, setLoadMoreError] = useState<string | null>(null);
  const [reloadToken, setReloadToken] = useState(0);

  // The reload token is part of the accumulation key so a retry starts the
  // stream over instead of folding page one into the pages already loaded.
  const key = `${streamKey(slug, filters)}#${reloadToken}`;
  const [stream, setStream] = useState<IssueStream>(() => emptyStream(key));
  const activeKey = useRef(key);

  useEffect(() => {
    let active = true;
    activeKey.current = key;
    setPhase({ kind: "loading" });
    setLoadingMore(false);
    setLoadMoreError(null);

    api
      .listIssues(slug, issueRequestFrom(filters, PAGE_SIZE))
      .then((page) => {
        if (!active) {
          return;
        }
        setStream((current) => appendPage(current, page, key));
        setPhase({ kind: "ready" });
      })
      .catch((caught: unknown) => {
        if (!active) {
          return;
        }
        setPhase({ kind: "failed", message: messageFor(caught), notFound: isNotFound(caught) });
      });

    return () => {
      active = false;
    };
    // `key` is the complete identity of `filters`: two filter objects sharing a
    // key describe the same request. Depending on the object instead would
    // refetch on every unrelated re-render.
  }, [slug, key]);

  const loadMore = useCallback(() => {
    const cursor = stream.cursor;
    if (cursor === null || loadingMore) {
      return;
    }
    const requestKey = key;
    setLoadingMore(true);
    setLoadMoreError(null);

    api
      .listIssues(slug, { ...issueRequestFrom(filters, PAGE_SIZE), cursor })
      .then((page) => {
        // Filters changed while this page was in flight. `appendPage` would
        // treat the stale key as a new query and replace the current results
        // with it, so the response is dropped instead.
        if (activeKey.current !== requestKey) {
          return;
        }
        setStream((current) => appendPage(current, page, requestKey));
      })
      .catch((caught: unknown) => {
        if (activeKey.current === requestKey) {
          setLoadMoreError(messageFor(caught));
        }
      })
      .finally(() => {
        if (activeKey.current === requestKey) {
          setLoadingMore(false);
        }
      });
  }, [slug, key, filters, stream.cursor, loadingMore]);

  const reload = useCallback(() => setReloadToken((token) => token + 1), []);

  const applyUpdates = useCallback((issues: readonly IssueSummary[]) => {
    setStream((current) => replaceIssues(current, issues));
  }, []);

  return { phase, stream, loadingMore, loadMoreError, loadMore, reload, applyUpdates };
}
