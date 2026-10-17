import type { EventOrder, EventSummary } from "../../api.ts";

/**
 * A cursor-paginated walk over one issue's events, anchored at one end.
 *
 * `GET /api/v1/issues/{id}/events` only pages away from the anchor, so a step
 * back toward it is a move through what has already been loaded rather than a
 * request. "Further" therefore means older in a newest-first walk and newer in
 * an oldest-first one; the two directions are otherwise identical, which is why
 * everything here is expressed relative to the anchor rather than to time.
 */
export interface EventPager {
  readonly events: readonly EventSummary[];
  readonly nextCursor: string | null;
  readonly selectedIndex: number;
  readonly order: EventOrder;
}

export function pagerFrom(
  events: readonly EventSummary[],
  nextCursor: string | null,
  order: EventOrder = "newest"
): EventPager {
  return { events, nextCursor, selectedIndex: 0, order };
}

/**
 * Restarts the walk from the first occurrence, discarding the loaded window.
 *
 * A cursor is not portable between orders: the keyset comparison flips with the
 * sort, so the two cursors describe positions in differently-ordered streams.
 * Splicing their pages into one list would need a total ordering across both
 * that the endpoint does not expose, so switching direction drops what is held
 * and refetches. Returns the pager unchanged when it already runs in that
 * direction, which is how a caller tells "no refetch needed" from "the window
 * was discarded".
 */
export function beginOldestWalk(pager: EventPager): EventPager {
  return walkingIn(pager, "oldest");
}

/** Restarts the walk from the most recent event. See `beginOldestWalk`. */
export function beginNewestWalk(pager: EventPager): EventPager {
  return walkingIn(pager, "newest");
}

function walkingIn(pager: EventPager, order: EventOrder): EventPager {
  if (pager.order === order) {
    return pager;
  }
  return { events: [], nextCursor: null, selectedIndex: 0, order };
}

/**
 * Appends the next page, dropping ids already held.
 *
 * Cursor paging runs against a table that is still receiving events, so the
 * same row can come back twice; without the guard it would appear twice in the
 * walk and React would see duplicate keys.
 */
export function appendPage(
  pager: EventPager,
  events: readonly EventSummary[],
  nextCursor: string | null
): EventPager {
  const known = new Set(pager.events.map((event) => event.event_id));
  const fresh = events.filter((event) => !known.has(event.event_id));
  return { ...pager, events: [...pager.events, ...fresh], nextCursor };
}

export function selectedEvent(pager: EventPager): EventSummary | null {
  return pager.events[pager.selectedIndex] ?? null;
}

export function canSelectNearer(pager: EventPager): boolean {
  return pager.selectedIndex > 0;
}

export function canSelectFurther(pager: EventPager): boolean {
  return pager.selectedIndex + 1 < pager.events.length || pager.nextCursor !== null;
}

/** True when stepping further would run past the loaded window and needs a fetch. */
export function needsFurtherPage(pager: EventPager): boolean {
  return pager.selectedIndex + 1 >= pager.events.length && pager.nextCursor !== null;
}

export function selectNearer(pager: EventPager): EventPager {
  if (!canSelectNearer(pager)) {
    return pager;
  }
  return { ...pager, selectedIndex: pager.selectedIndex - 1 };
}

export function selectFurther(pager: EventPager): EventPager {
  if (pager.selectedIndex + 1 >= pager.events.length) {
    return pager;
  }
  return { ...pager, selectedIndex: pager.selectedIndex + 1 };
}

/** Jumps back to the event the walk is anchored at: the newest or the oldest. */
export function selectAnchor(pager: EventPager): EventPager {
  return pager.selectedIndex === 0 ? pager : { ...pager, selectedIndex: 0 };
}

/** Whether the walk has already loaded the event with `eventId`. */
export function holdsEvent(pager: EventPager, eventId: string): boolean {
  return pager.events.some((event) => event.event_id === eventId);
}

/** Selects the event with `eventId`, or leaves the walk alone if it is not loaded. */
export function selectEventId(pager: EventPager, eventId: string): EventPager {
  const index = pager.events.findIndex((event) => event.event_id === eventId);
  if (index < 0) {
    return pager;
  }
  return { ...pager, selectedIndex: index };
}

/**
 * Names where the selection sits relative to the anchor: "Newest", "2nd newest",
 * "Oldest", "2nd oldest".
 *
 * A bare "Event #1" for the newest event reads as "the first time this
 * happened" and means the opposite, so the anchor is always spelled out.
 */
export function positionLabel(pager: EventPager): string {
  if (selectedEvent(pager) === null) {
    return "";
  }
  if (pager.selectedIndex === 0) {
    return pager.order === "oldest" ? "Oldest" : "Newest";
  }
  return `${ordinal(pager.selectedIndex + 1)} ${pager.order}`;
}

/** English ordinal for a positive position: 1st, 2nd, 3rd, 4th, 11th, 21st. */
export function ordinal(position: number): string {
  // 11, 12 and 13 take "th" despite ending in 1, 2 and 3, which is what naive
  // last-digit suffixing gets wrong ("11st"). The exception repeats every
  // hundred, so it is the last two digits that decide it.
  const lastTwoDigits = position % 100;
  if (lastTwoDigits >= 11 && lastTwoDigits <= 13) {
    return `${position}th`;
  }
  switch (position % 10) {
    case 1:
      return `${position}st`;
    case 2:
      return `${position}nd`;
    case 3:
      return `${position}rd`;
    default:
      return `${position}th`;
  }
}
