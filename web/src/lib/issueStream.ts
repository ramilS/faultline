import type { IssuePage, IssueSummary } from "../api.ts";

export interface IssueStream {
  /** Identity of the filter set this accumulation belongs to (see `filtersKey`). */
  readonly key: string;
  readonly issues: readonly IssueSummary[];
  /** Cursor for the next page; `null` once the server says the stream is exhausted. */
  readonly cursor: string | null;
}

export function emptyStream(key: string): IssueStream {
  return { key, issues: [], cursor: null };
}

export function hasMore(stream: IssueStream): boolean {
  return stream.cursor !== null;
}

/**
 * Folds one page into an accumulation.
 *
 * A page fetched under a different key belongs to a different query, so it
 * replaces the accumulation instead of extending it. Appending would splice
 * results from the previous filter set into the new list — the pre-redesign UI
 * avoided this only by never paginating at all.
 */
export function appendPage(current: IssueStream, page: IssuePage, key: string): IssueStream {
  const base = current.key === key ? current.issues : [];
  return { key, issues: withoutDuplicates(base, page.issues), cursor: page.next_cursor };
}

/**
 * Applies server-confirmed issue rows in place, leaving the rest of the
 * accumulation untouched.
 *
 * Used after a bulk action so the loaded pages survive; refetching would reset
 * the stream and discard every page past the first.
 */
export function replaceIssues(stream: IssueStream, updated: readonly IssueSummary[]): IssueStream {
  if (updated.length === 0) {
    return stream;
  }
  const byId = new Map(updated.map((issue) => [issue.id, issue]));
  return { ...stream, issues: stream.issues.map((issue) => byId.get(issue.id) ?? issue) };
}

/**
 * Cursor pages can overlap when new events reorder rows between requests. A
 * repeated issue would collide as a React key and be counted twice by bulk
 * selection, so the first occurrence wins.
 */
function withoutDuplicates(
  base: readonly IssueSummary[],
  incoming: readonly IssueSummary[]
): readonly IssueSummary[] {
  const seen = new Set(base.map((issue) => issue.id));
  return [...base, ...incoming.filter((issue) => !seen.has(issue.id))];
}
