// Mirrors internal/issue/predicate.go's MaxAbsoluteWindow: the server rejects
// a longer absolute range with a generic 400 that would otherwise just blank
// the issue list, so the picker catches it first and names the actual limit.
export const MAX_CUSTOM_RANGE_DAYS = 90;
const MAX_CUSTOM_RANGE_MS = MAX_CUSTOM_RANGE_DAYS * 24 * 60 * 60 * 1000;

export function startOfDayRfc3339(dateInput: string): string {
  return `${dateInput}T00:00:00.000Z`;
}

export function endOfDayRfc3339(dateInput: string): string {
  return `${dateInput}T23:59:59.999Z`;
}

export interface CustomRange {
  readonly start: string;
  readonly end: string;
}

export type CustomRangeResult = { readonly ok: true; readonly range: CustomRange } | { readonly ok: false; readonly error: string };

/**
 * Builds the [start, end] instant pair for a custom date-input range, or
 * reports that the span exceeds the cap.
 *
 * `start` is midnight and `end` is one millisecond before the next midnight,
 * so a `MAX_CUSTOM_RANGE_DAYS`-*calendar-day* span (inclusive on both ends)
 * measures as `MAX_CUSTOM_RANGE_DAYS` days minus 1ms — under the cap — while
 * one calendar day more measures over it. That happens to agree with the
 * server's `w.End.Sub(w.Start) > MaxAbsoluteWindow` check
 * (internal/issue/predicate.go), which is why this stays an exact `>` rather
 * than any padding — see timeRange.test.ts for the pin.
 */
export function buildCustomRange(fromDraft: string, toDraft: string): CustomRangeResult {
  const start = startOfDayRfc3339(fromDraft);
  const end = endOfDayRfc3339(toDraft);
  if (Date.parse(end) - Date.parse(start) > MAX_CUSTOM_RANGE_MS) {
    return { ok: false, error: `Range cannot exceed ${MAX_CUSTOM_RANGE_DAYS} days` };
  }
  return { ok: true, range: { start, end } };
}
