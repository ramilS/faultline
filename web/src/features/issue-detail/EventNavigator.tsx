import { ChevronLeft, ChevronRight, Inbox } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { toast } from "sonner";

import type { EventOrder, EventSummary } from "@/api";
import { api } from "@/apiClient";
import { EmptyState } from "@/components/EmptyState";
import { ErrorState } from "@/components/ErrorState";
import { RelativeTime } from "@/components/RelativeTime";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { messageFor } from "@/errors";
import type { TimeRangeValue } from "@/features/issues/TimeRangePicker";
import { cn } from "@/lib/utils";

import { CopyButton } from "./CopyButton";
import {
  appendPage,
  beginNewestWalk,
  beginOldestWalk,
  canSelectFurther,
  holdsEvent,
  canSelectNearer,
  needsFurtherPage,
  pagerFrom,
  positionLabel,
  selectAnchor,
  selectEventId,
  selectFurther,
  selectNearer,
  selectedEvent,
  type EventPager
} from "./eventPager";
import { eventScopeFilters, paramsWithEventScope, type EventScope } from "./eventScope";
import { paramsWithSelectedEvent } from "./selectedEvent";

/** How many pages the first load walks looking for a linked event before giving up on it. */
const MAX_LANDING_PAGES = 10;

/** The active filters as the scope both this pager and the all-events page understand. */
function activeScope(environment: string, timeRange: TimeRangeValue): EventScope {
  return { environment, start: timeRange.start, end: timeRange.end };
}

/** The all-events route, relative to the detail route, carrying the active scope. */
function allEventsPath(scope: EventScope): string {
  const search = paramsWithEventScope(new URLSearchParams(), scope).toString();
  return search === "" ? "events" : `events?${search}`;
}

type NavigatorState =
  | { readonly status: "loading" }
  | { readonly status: "ready"; readonly pager: EventPager }
  | { readonly status: "failed"; readonly message: string };

interface EventNavigatorProps {
  issueId: number;
  /** The event id to land on when the walk first loads, from the URL. */
  initialEventId: string | null;
  /** Called whenever the walk lands on a different event. Must be stable. */
  onSelect: (event: EventSummary) => void;
  /** "" means every environment. */
  environment: string;
  /**
   * Already resolved to absolute bounds (or all-time) by the caller — see
   * `eventWindow.ts`'s `resolveTimeRange`. `statsPeriod` is carried only to
   * satisfy `TimeRangeValue`'s shape and is never read here: forwarding a
   * relative period to this component's own cursor-paginated requests would
   * let the window slide between them.
   */
  timeRange: TimeRangeValue;
}

/**
 * Steps through an issue's events from one end of the stream.
 *
 * The endpoint only pages away from that end, so further events are fetched on
 * demand and accumulated; stepping back walks through what is already held.
 * Switching ends restarts the walk — see `beginOldestWalk` for why the loaded
 * window cannot be carried across.
 */
export function EventNavigator({ issueId, initialEventId, onSelect, environment, timeRange }: EventNavigatorProps) {
  // Memoized so it can BE the dependency of every effect and callback below
  // rather than each of them restating the three fields it is made of.
  const scope = useMemo(() => activeScope(environment, timeRange), [environment, timeRange.start, timeRange.end]);
  const [state, setState] = useState<NavigatorState>({ status: "loading" });
  const [attempt, setAttempt] = useState(0);
  const [fetching, setFetching] = useState(false);
  const [, setSearchParams] = useSearchParams();

  // Captured once: a link to a specific event should land there, but the
  // walk must not jump the user's place every time the URL changes because
  // stepping through events writes it right back.
  const landingEventId = useRef(initialEventId);

  // A filter change is a request to see a different, newly-scoped walk, not
  // a way to reach the event the URL originally deep-linked to — that event
  // may not even fall inside the new scope. Clearing the landing target here
  // (skipping the mount so the deep link still lands the first time) makes
  // the effect below fall through to its landing === null branch and restart
  // at the anchor ("Latest"/"Oldest") instead of retrying the old target.
  const skippedMountForLanding = useRef(false);
  useEffect(() => {
    if (!skippedMountForLanding.current) {
      skippedMountForLanding.current = true;
      return;
    }
    landingEventId.current = null;
  }, [scope]);

  useEffect(() => {
    let active = true;
    setState({ status: "loading" });

    void (async () => {
      try {
        const page = await api.listIssueEvents(issueId, eventScopeFilters(scope));
        let loaded = pagerFrom(page.events, page.next_cursor);
        const landing = landingEventId.current;
        // A link from an alert names an event that may be several pages
        // behind the newest by the time it is opened, so the walk pages on
        // toward it — boundedly, since a flood can bury it arbitrarily deep.
        const stillLooking = () => landing !== null && !holdsEvent(loaded, landing) && loaded.nextCursor !== null;
        for (let pages = 1; stillLooking() && pages < MAX_LANDING_PAGES; pages++) {
          const next = await api.listIssueEvents(issueId, { cursor: loaded.nextCursor ?? undefined, ...eventScopeFilters(scope) });
          if (!active) {
            return;
          }
          loaded = appendPage(loaded, next.events, next.next_cursor);
        }
        if (!active) {
          return;
        }
        if (landing !== null && !holdsEvent(loaded, landing)) {
          toast.info("The linked event isn't in this list any more, so this shows the latest one instead.");
        }
        setState({ status: "ready", pager: landing === null ? loaded : selectEventId(loaded, landing) });
      } catch (caught) {
        if (active) {
          setState({ status: "failed", message: messageFor(caught) });
        }
      }
    })();

    return () => {
      active = false;
    };
    // A filter change restarts the walk exactly like a changed issueId
    // already does.
  }, [issueId, attempt, scope]);

  const selected = state.status === "ready" ? selectedEvent(state.pager) : null;

  useEffect(() => {
    if (selected !== null) {
      onSelect(selected);
    }
  }, [selected, onSelect]);

  // Stepping through events replaces the URL's selection rather than pushing
  // a history entry: the back button should not replay one event at a time,
  // the same reasoning useUrlFilters documents for its debounced writes.
  const commitSelection = useCallback(
    (pager: EventPager) => {
      setState({ status: "ready", pager });
      const event = selectedEvent(pager);
      if (event !== null) {
        setSearchParams((current) => paramsWithSelectedEvent(current, event.event_id), { replace: true });
      }
    },
    [setSearchParams]
  );

  const goFurther = useCallback(async () => {
    if (state.status !== "ready" || fetching) {
      return;
    }
    const { pager } = state;
    if (!needsFurtherPage(pager)) {
      commitSelection(selectFurther(pager));
      return;
    }

    setFetching(true);
    try {
      const page = await api.listIssueEvents(issueId, {
        cursor: pager.nextCursor ?? undefined,
        order: pager.order,
        ...eventScopeFilters(scope)
      });
      commitSelection(selectFurther(appendPage(pager, page.events, page.next_cursor)));
    } catch (caught) {
      // A failed "load more" must not discard the events already walked, so it
      // surfaces as a toast rather than replacing the panel with an error.
      toast.error(messageFor(caught));
    } finally {
      setFetching(false);
    }
  }, [state, fetching, issueId, commitSelection, scope]);

  const goAnchor = useCallback(
    async (order: EventOrder) => {
      if (state.status !== "ready" || fetching) {
        return;
      }
      const { pager } = state;
      const restarted = order === "oldest" ? beginOldestWalk(pager) : beginNewestWalk(pager);
      if (restarted === pager) {
        // Already walking from this end: the anchor is loaded, so jumping back
        // to it is a move within the window, not a request.
        commitSelection(selectAnchor(pager));
        return;
      }

      setFetching(true);
      try {
        const page = await api.listIssueEvents(issueId, {
          order,
          ...eventScopeFilters(scope)
        });
        commitSelection(appendPage(restarted, page.events, page.next_cursor));
      } catch (caught) {
        // The walk that was on screen is still in state and still valid, so a
        // failed turn leaves the user where they were.
        toast.error(messageFor(caught));
      } finally {
        setFetching(false);
      }
    },
    [state, fetching, issueId, commitSelection, scope]
  );

  return (
    <section aria-label="Event" className="bg-card rounded-xl border px-3 py-2">
      {state.status === "loading" && <Skeleton className="h-8 w-full" />}

      {state.status === "failed" && (
        <ErrorState
          className="py-6"
          message={state.message}
          onRetry={() => setAttempt((current) => current + 1)}
        />
      )}

      {state.status === "ready" && selected === null && (
        <EmptyState
          className="py-6"
          icon={Inbox}
          title="No events"
          description="This issue has no stored events. They may have aged out of retention."
        />
      )}

      {state.status === "ready" && selected !== null && (
        <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
          <AnchorTabs pager={state.pager} fetching={fetching} onAnchor={(order) => void goAnchor(order)} />
          <EventMeta event={selected} position={state.pager.selectedIndex > 0 ? positionLabel(state.pager) : null} />
          <div className="ml-auto flex items-center gap-1">
            <StepButtons
              pager={state.pager}
              fetching={fetching}
              onNearer={() => commitSelection(selectNearer(state.pager))}
              onFurther={() => void goFurther()}
            />
            {/* Resolved against this route (`/:slug/issues/:issueId`), so it
                lands on the child `events` route. Never `../events`: with
                `relative="path"` that pops the `:issueId` segment and
                resolves to `/:slug/issues/events`, which the detail route
                itself matches with issueId="events".

                The active scope rides along in the query string so the full
                list is never quietly wider than the pager it was opened
                from. */}
            <Link to={allEventsPath(scope)} className="text-muted-foreground hover:text-foreground ml-2 text-xs">
              View all
            </Link>
          </div>
        </div>
      )}
    </section>
  );
}

const ANCHORS: ReadonlyArray<{ order: EventOrder; label: string }> = [
  { order: "newest", label: "Latest" },
  { order: "oldest", label: "Oldest" }
];

interface AnchorTabsProps {
  pager: EventPager;
  fetching: boolean;
  onAnchor: (order: EventOrder) => void;
}

/**
 * Which end of the stream the walk is standing on. A tab reads as pressed only
 * while the selection is that end's own anchor event: three steps into the
 * newest-first walk the reader is no longer looking at the latest event, and a
 * pressed "Latest" would say they were.
 */
function AnchorTabs({ pager, fetching, onAnchor }: AnchorTabsProps) {
  const atAnchor = !canSelectNearer(pager);

  return (
    <div role="group" aria-label="Jump to event" className="bg-muted/40 flex rounded-md p-0.5">
      {ANCHORS.map((anchor) => {
        const pressed = atAnchor && pager.order === anchor.order;
        return (
          <button
            key={anchor.order}
            type="button"
            aria-pressed={pressed}
            disabled={fetching}
            onClick={() => onAnchor(anchor.order)}
            className={cn(
              "rounded-sm px-2.5 py-1 text-xs transition-colors disabled:opacity-50",
              pressed ? "bg-background text-foreground font-medium shadow-xs" : "text-muted-foreground hover:text-foreground"
            )}
          >
            {anchor.label}
          </button>
        );
      })}
    </div>
  );
}

/**
 * The selected event in one line. Its message is left to the issue header,
 * which already shows it whenever it differs from the title.
 */
function EventMeta({ event, position }: { event: EventSummary; position: string | null }) {
  return (
    <dl className="text-muted-foreground flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-xs">
      {position !== null && (
        <div>
          <dt className="sr-only">Position</dt>
          <dd className="text-foreground font-medium">{position}</dd>
        </div>
      )}
      <div className="flex items-center gap-1">
        <dt className="sr-only">Event ID</dt>
        <dd className="font-mono">{event.event_id}</dd>
        <CopyButton value={event.event_id} label="Event ID" />
      </div>
      <div title={`Received ${event.received_at}`}>
        <dt className="sr-only">Occurred</dt>
        <dd>
          <RelativeTime iso={event.timestamp} />
        </dd>
      </div>
      {event.environment !== "" && (
        <div>
          <dt className="sr-only">Environment</dt>
          <dd className="font-mono">{event.environment}</dd>
        </div>
      )}
      {event.release !== null && (
        <div>
          <dt className="sr-only">Release</dt>
          <dd className="font-mono">{event.release}</dd>
        </div>
      )}
    </dl>
  );
}

interface StepButtonsProps {
  pager: EventPager;
  fetching: boolean;
  onNearer: () => void;
  onFurther: () => void;
}

function StepButtons({ pager, fetching, onNearer, onFurther }: StepButtonsProps) {
  // The chevrons follow the walk, not the clock, so their labels have to name
  // the direction the current order actually moves in.
  const walkingOldest = pager.order === "oldest";
  const nearerLabel = walkingOldest ? "Older event" : "Newer event";
  const furtherLabel = walkingOldest ? "Newer event" : "Older event";

  return (
    <>
      <Button
        variant="outline"
        size="icon-sm"
        aria-label={nearerLabel}
        onClick={onNearer}
        disabled={!canSelectNearer(pager) || fetching}
      >
        <ChevronLeft aria-hidden="true" />
      </Button>
      <Button
        variant="outline"
        size="icon-sm"
        aria-label={furtherLabel}
        onClick={onFurther}
        disabled={!canSelectFurther(pager) || fetching}
      >
        <ChevronRight aria-hidden="true" />
      </Button>
    </>
  );
}
