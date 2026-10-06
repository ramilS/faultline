import { Inbox } from "lucide-react";
import { useCallback, useState } from "react";
import { Link, useParams, useSearchParams } from "react-router-dom";
import { toast } from "sonner";

import type { EventOrder, EventSummary } from "@/api";
import { api } from "@/apiClient";
import { EmptyState } from "@/components/EmptyState";
import { ErrorState } from "@/components/ErrorState";
import { LevelBadge } from "@/components/LevelBadge";
import { PageHeader } from "@/components/PageHeader";
import { STACKED_ROWS_BELOW_MD } from "@/components/stackedTable";
import { RelativeTime } from "@/components/RelativeTime";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { messageFor } from "@/errors";
import { useAsyncResource } from "@/hooks/useAsyncResource";
import { useDocumentTitle } from "@/hooks/useDocumentTitle";
import { absoluteTime } from "@/lib/time";

import { describeEventScope, eventScopeFilters, eventScopeFromParams, type EventScope } from "./eventScope";
import { issueIdFromParam } from "./issueIdParam";
import { NotAnIssue } from "./NotAnIssue";
import { paramsWithSelectedEvent } from "./selectedEvent";

export function IssueEventsPage() {
  const { slug, issueId } = useParams<{ slug: string; issueId: string }>();
  const [searchParams] = useSearchParams();
  const numericId = issueIdFromParam(issueId);

  if (slug === undefined || numericId === null) {
    return <NotAnIssue slug={slug} issueId={issueId} />;
  }

  // The scope the pager was showing when "View all" was clicked. Read from
  // the URL rather than re-picked here: this page has no filter bar, so an
  // unread scope would silently widen the list the user asked to expand.
  const scope = eventScopeFromParams(searchParams);

  // Keyed so that navigating between issues remounts the pagination state
  // below rather than showing one issue's tail appended to another's.
  return <EventsList key={numericId} slug={slug} issueId={numericId} scope={scope} />;
}

const EVENT_ORDERS: ReadonlyArray<{ value: EventOrder; label: string }> = [
  { value: "newest", label: "Newest first" },
  { value: "oldest", label: "Oldest first" }
];

/** The first page's own type, so the accumulator cannot drift from the client. */
type EventsPage = Awaited<ReturnType<typeof api.listIssueEvents>>;

interface AppendedPages {
  /** Identity of the first page these were appended to — never its contents. */
  readonly source: EventsPage | null;
  readonly events: readonly EventSummary[];
  readonly cursor: string | null;
}

const NO_APPENDED_PAGES: AppendedPages = { source: null, events: [], cursor: null };

interface EventsListProps {
  slug: string;
  issueId: number;
  scope: EventScope;
}

function EventsList({ slug, issueId, scope }: EventsListProps) {
  useDocumentTitle(`All events · issue #${issueId}`);
  const { environment, start, end } = scope;
  // Newest first: the list is opened to find what just happened, and paging
  // forward from the first occurrence would bury it behind every earlier page.
  const [order, setOrder] = useState<EventOrder>("newest");
  const loadFirstPage = useCallback(
    () => api.listIssueEvents(issueId, { order, ...eventScopeFilters({ environment, start, end }) }),
    [issueId, order, environment, start, end]
  );
  const { state, reload } = useAsyncResource(loadFirstPage);
  const firstPage = state.status === "ready" ? state.value : null;

  // Pages fetched via "Load more", kept separate from the resource above so
  // that a retry of the first page (which re-runs `loadFirstPage`) cannot be
  // confused with appending.
  const [appended, setAppended] = useState<AppendedPages>(NO_APPENDED_PAGES);
  const [loadingMore, setLoadingMore] = useState(false);

  /*
    Derived while rendering rather than synced by an effect. An effect commits
    the first page's rows once before the cursor exists, so "Load more" arrives
    a frame late on every load — and a test that waits for a row can observe
    that window, which is how this surfaced.

    Comparing the first page's identity also discards a "load more" response
    that lands after the first page was refetched. The effect could not: it
    reset the accumulator, and the late response then appended its events to a
    page they do not belong to.
  */
  const pages: AppendedPages =
    appended.source === firstPage
      ? appended
      : { source: firstPage, events: [], cursor: firstPage?.next_cursor ?? null };

  const loadMore = useCallback(async () => {
    if (pages.cursor === null || loadingMore) {
      return;
    }
    setLoadingMore(true);
    try {
      const page = await api.listIssueEvents(issueId, {
        order,
        cursor: pages.cursor,
        ...eventScopeFilters({ environment, start, end })
      });
      setAppended({ source: firstPage, events: [...pages.events, ...page.events], cursor: page.next_cursor });
    } catch (caught) {
      // A failed "load more" must not discard the events already shown, so it
      // surfaces as a toast rather than replacing the table with an error —
      // matches EventNavigator's handling of the same failure mode.
      toast.error(messageFor(caught));
    } finally {
      setLoadingMore(false);
    }
  }, [issueId, firstPage, pages, loadingMore, order, environment, start, end]);

  if (state.status === "loading") {
    return (
      <div className="space-y-4 p-4 sm:p-6">
        <Skeleton className="h-6 w-48" />
        <Skeleton className="h-96 w-full" />
      </div>
    );
  }

  if (state.status === "failed") {
    return <ErrorState message={state.message} onRetry={reload} />;
  }

  // `state` is narrowed to "ready" by the two guard clauses above, so
  // `state.value` is safe here without re-asserting what was already checked.
  const events = [...state.value.events, ...pages.events];
  const scopeDescription = describeEventScope(scope, absoluteTime);
  // Exception events carry no message of their own, so for most issues the
  // column would be a wall of dashes.
  const showMessage = events.some((event) => event.message.trim() !== "");

  return (
    <div className="space-y-4">
      <PageHeader
        title="All events"
        description={
          <>
            <Link to={`/${slug}/issues/${issueId}`} className="hover:text-foreground underline">
              Back to issue #{issueId}
            </Link>
            {scopeDescription !== null && <span className="ml-2">{scopeDescription}</span>}
          </>
        }
      />

      {events.length === 0 ? (
        <EmptyState
          className="py-16"
          icon={Inbox}
          title="No events"
          description={
            scopeDescription === null
              ? "This issue has no stored events. They may have aged out of retention."
              : "No stored event matches the filters carried over from the issue page."
          }
        />
      ) : (
        <div className="space-y-4 p-4 pt-0 sm:p-6 sm:pt-0">
          <div className="flex items-center gap-1" role="group" aria-label="Event order">
            {EVENT_ORDERS.map((option) => (
              <Button
                key={option.value}
                variant={option.value === order ? "secondary" : "ghost"}
                size="xs"
                aria-pressed={option.value === order}
                onClick={() => setOrder(option.value)}
              >
                {option.label}
              </Button>
            ))}
          </div>

          <Table className={STACKED_ROWS_BELOW_MD}>
            <TableHeader>
              <TableRow>
                <TableHead>Event</TableHead>
                <TableHead>Level</TableHead>
                {showMessage && <TableHead>Message</TableHead>}
                <TableHead className="max-md:hidden">Environment</TableHead>
                <TableHead className="max-lg:hidden">Release</TableHead>
                <TableHead>Occurred</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {events.map((event) => (
                <TableRow key={event.event_id}>
                  <TableCell>
                    <Link
                      to={`/${slug}/issues/${issueId}?${paramsWithSelectedEvent(new URLSearchParams(), event.event_id).toString()}`}
                      className="hover:text-foreground font-mono text-xs underline"
                    >
                      {event.event_id}
                    </Link>
                  </TableCell>
                  <TableCell>
                    <LevelBadge level={event.level} />
                  </TableCell>
                  {showMessage && (
                    <TableCell className="max-w-0 min-w-40">
                      <span className="block truncate text-sm" title={event.message}>
                        {event.message.trim() === "" ? "—" : event.message}
                      </span>
                    </TableCell>
                  )}
                  <TableCell className="font-mono text-xs max-md:hidden">{event.environment || "—"}</TableCell>
                  <TableCell className="font-mono text-xs max-lg:hidden">{event.release ?? "—"}</TableCell>
                  <TableCell className="whitespace-nowrap">
                    {/* Printed, not only in a tooltip: an event log is read for
                        exact times, and touch screens have no hover. */}
                    <time dateTime={event.timestamp} className="text-xs">
                      {absoluteTime(event.timestamp)}
                    </time>{" "}
                    <RelativeTime iso={event.timestamp} className="text-muted-foreground text-xs" />
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>

          {pages.cursor !== null && (
            <Button variant="outline" size="sm" disabled={loadingMore} onClick={() => void loadMore()}>
              Load more
            </Button>
          )}
        </div>
      )}
    </div>
  );
}
