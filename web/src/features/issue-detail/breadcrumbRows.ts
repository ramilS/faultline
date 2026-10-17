import type { Breadcrumb } from "./payloadReaders.ts";
import { isSafeHref } from "./payloadReaders.ts";

export interface BreadcrumbRow {
  readonly key: string;
  /** True for the single trailing row that marks where the exception landed. */
  readonly isClosingRow: boolean;
  readonly timestamp: string | null;
  readonly category: string;
  readonly level: string;
  readonly message: string;
  /** Set only when `message` is admitted by {@link isSafeHref}; null renders as plain text. */
  readonly messageHref: string | null;
  /** Anything the breadcrumb carried beyond timestamp/category/level/message. */
  readonly data: unknown;
}

export interface ClosingEvent {
  readonly timestamp: string;
  readonly level: string;
  readonly message: string;
}

/**
 * Oldest-first breadcrumb rows followed by a closing row for the event's own
 * exception, so the sequence reads to a conclusion rather than stopping
 * mid-air.
 */
export function buildBreadcrumbRows(
  breadcrumbs: readonly Breadcrumb[],
  closingEvent: ClosingEvent
): readonly BreadcrumbRow[] {
  const rows = breadcrumbs.map((crumb, index) => toRow(`breadcrumb-${index}`, crumb, false));
  const closingRow = toRow(
    "closing-event",
    { timestamp: closingEvent.timestamp, category: "exception", level: closingEvent.level, message: closingEvent.message, data: undefined },
    true
  );
  return [...rows, closingRow];
}

function toRow(
  key: string,
  crumb: { timestamp: string | null; category: string; level: string; message: string; data: unknown },
  isClosingRow: boolean
): BreadcrumbRow {
  return {
    key,
    isClosingRow,
    timestamp: crumb.timestamp,
    category: crumb.category,
    level: crumb.level,
    message: crumb.message,
    messageHref: isSafeHref(crumb.message) ? crumb.message : null,
    data: crumb.data
  };
}

const SECOND = 1000;
const MINUTE = 60 * SECOND;
const HOUR = 60 * MINUTE;

/**
 * How long before the error a row happened, as "−2.1s" or "−3m 12s".
 *
 * Measured from the error rather than from now: "4 minutes ago" on every row
 * of a trail that took two seconds says nothing, while the gap to the error is
 * what tells a reader which step led into it. A row stamped after the event
 * (clock skew between the SDK's breadcrumb and event stamps) reads with a
 * plus rather than being clamped to zero. Null when either instant is unusable.
 */
export function offsetFromEvent(timestamp: string | null, eventTimestamp: string): string | null {
  if (timestamp === null) {
    return null;
  }
  const gap = Date.parse(eventTimestamp) - Date.parse(timestamp);
  if (Number.isNaN(gap)) {
    return null;
  }
  const size = Math.abs(gap);
  if (size === 0) {
    return "0s";
  }
  const sign = gap > 0 ? "−" : "+";
  if (size < SECOND) {
    return `${sign}${size}ms`;
  }
  if (size < MINUTE) {
    return `${sign}${(size / SECOND).toFixed(1)}s`;
  }
  if (size < HOUR) {
    return `${sign}${Math.floor(size / MINUTE)}m ${Math.floor((size % MINUTE) / SECOND)}s`;
  }
  return `${sign}${Math.floor(size / HOUR)}h ${Math.floor((size % HOUR) / MINUTE)}m`;
}

/** Rows whose category, level or message contains `query`, case-insensitively. A blank query keeps every row. */
export function filterBreadcrumbRows(rows: readonly BreadcrumbRow[], query: string): readonly BreadcrumbRow[] {
  const needle = query.trim().toLowerCase();
  if (needle === "") {
    return rows;
  }
  return rows.filter((row) =>
    [row.category, row.level, row.message].some((field) => field.toLowerCase().includes(needle))
  );
}
