import { ChevronLeft, ChevronRight, Inbox } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { toast } from "sonner";

import type { EventOrder, EventSummary } from "@/api";
import { api } from "@/apiClient";
import { EmptyState } from "@/components/EmptyState";
import { ErrorState } from "@/components/ErrorState";
import { LevelBadge } from "@/components/LevelBadge";
import { RelativeTime } from "@/components/RelativeTime";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { messageFor } from "@/errors";
import type { TimeRangeValue } from "@/features/issues/TimeRangePicker";

import { CopyButton } from "./CopyButton";
import { DetailSection } from "./DetailSection";
import {
  appendPage,
  beginNewestWalk,
  beginOldestWalk,
  canSelectFurther,
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
        if (active) {
          const loaded = pagerFrom(page.events, page.next_cursor);
          const landing = landingEventId.current;
          setState({ status: "ready", pager: landing === null ? loaded : selectEventId(loaded, landing) });
        }
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
    <DetailSection
      title="Events"
      action={
        state.status === "ready" && state.pager.events.length > 0 ? (
          <div className="flex items-center gap-3">
            <Controls
              pager={state.pager}
              fetching={fetching}
              onNearer={() => commitSelection(selectNearer(state.pager))}
              onFurther={() => void goFurther()}
              onAnchor={(order) => void goAnchor(order)}
            />
            {/* Resolved against this route (`/:slug/issues/:issueId`), so it
                lands on the child `events` route. Never `../events`: with
                `relative="path"` that pops the `:issueId` segment and
                resolves to `/:slug/issues/events`, which the detail route
                itself matches with issueId="events".

                The active scope rides along in the query string so the full
                list is never quietly wider than the pager it was opened
                from. */}
            <Link to={allEventsPath(scope)} className="text-muted-foreground hover:text-foreground text-xs">
              View all
            </Link>
          </div>
        ) : undefined
      }
    >
      {state.status === "loading" && <Skeleton className="h-20 w-full" />}

      {state.status === "failed" && (
        <ErrorState
          className="py-8"
          message={state.message}
          onRetry={() => setAttempt((current) => current + 1)}
        />
      )}

      {state.status === "ready" && selected === null && (
        <EmptyState
          className="py-8"
          icon={Inbox}
          title="No events"
          description="This issue has no stored events. They may have aged out of retention."
        />
      )}

      {state.status === "ready" && selected !== null && (
        <EventSummaryCard event={selected} position={positionLabel(state.pager)} />
      )}
    </DetailSection>
  );
}

interface ControlsProps {
  pager: EventPager;
  fetching: boolean;
  onNearer: () => void;
  onFurther: () => void;
  onAnchor: (order: EventOrder) => void;
}

function Controls({ pager, fetching, onNearer, onFurther, onAnchor }: ControlsProps) {
  // The chevrons follow the walk, not the clock, so their labels have to name
  // the direction the current order actually moves in.
  const walkingOldest = pager.order === "oldest";
  const nearerLabel = walkingOldest ? "Older event" : "Newer event";
  const furtherLabel = walkingOldest ? "Newer event" : "Older event";

  return (
    <div className="flex items-center gap-1">
      <Button
        variant="ghost"
        size="xs"
        onClick={() => onAnchor("newest")}
        disabled={fetching || (!walkingOldest && !canSelectNearer(pager))}
      >
        Latest
      </Button>
      <Button
        variant="ghost"
        size="xs"
        onClick={() => onAnchor("oldest")}
        disabled={fetching || (walkingOldest && !canSelectNearer(pager))}
      >
        Oldest
      </Button>
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
    </div>
  );
}

function EventSummaryCard({ event, position }: { event: EventSummary; position: string }) {
  return (
    <dl className="grid grid-cols-2 gap-x-6 gap-y-2 text-sm">
      <Field label={position} className="col-span-2">
        <span className="flex items-center gap-1">
          <span className="font-mono text-xs break-all">{event.event_id}</span>
          <CopyButton value={event.event_id} label="Event ID" />
        </span>
      </Field>
      <Field label="Level">
        <LevelBadge level={event.level} />
      </Field>
      <Field label="Occurred">
        <RelativeTime iso={event.timestamp} />
      </Field>
      <Field label="Received">
        <RelativeTime iso={event.received_at} />
      </Field>
      <Field label="Environment">
        <span className="font-mono text-xs break-all">{event.environment === "" ? "—" : event.environment}</span>
      </Field>
      <Field label="Release">
        <span className="font-mono text-xs break-all">{event.release ?? "—"}</span>
      </Field>
      {/* Exception events usually carry no message of their own — the exception
          is the message — so the field is left out rather than shown empty. */}
      {event.message.trim() !== "" && (
        <Field label="Message" className="col-span-2">
          <span className="break-words">{event.message}</span>
        </Field>
      )}
    </dl>
  );
}

function Field({ label, children, className }: { label: string; children: ReactNode; className?: string }) {
  return (
    <div className={className}>
      <dt className="text-muted-foreground text-xs font-medium tracking-wide uppercase">{label}</dt>
      <dd className="mt-0.5">{children}</dd>
    </div>
  );
}
