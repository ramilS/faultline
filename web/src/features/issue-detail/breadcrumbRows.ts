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
