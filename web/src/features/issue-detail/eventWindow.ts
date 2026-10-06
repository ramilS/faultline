import type { StatsPeriod } from "../../lib/issueFilters.ts";
import type { TimeRangeValue } from "../issues/TimeRangePicker.tsx";

/**
 * Milliseconds for each preset, mirroring `parseStatsPeriod` in
 * `internal/api/issues.go` exactly (24h, 7*24h, 14*24h, 30*24h, 90*24h) —
 * that function is the other place these durations are asserted, and the
 * two must agree or a frozen window here would describe a different span
 * than the label the user picked.
 */
const PERIOD_DURATIONS_MS: Record<StatsPeriod, number> = {
  "24h": 24 * 60 * 60 * 1000,
  "7d": 7 * 24 * 60 * 60 * 1000,
  "14d": 14 * 24 * 60 * 60 * 1000,
  "30d": 30 * 24 * 60 * 60 * 1000,
  "90d": 90 * 24 * 60 * 60 * 1000
};

/**
 * Freezes a relative preset into an absolute `start`/`end` pair, at `at`.
 *
 * The events endpoint resolves a trailing `statsPeriod` against wall-clock
 * `now()` on every request (see `internal/api/issues.go`'s
 * `parseEventWindow`, and `windowClause` in
 * `internal/storage/postgres/issue_query.go` which does the same for the
 * issue list). The event pager instead issues several requests across one
 * walk — the initial load, then a page per "further" or "latest"/"oldest"
 * step — so a relative window sent on every request would slide forward
 * between them: a user who pages, walks away, then pages again would see the
 * window's start creep past events the walk had not yet reached, silently
 * dropping them. Resolving once here and holding the result in state (rather
 * than recomputing it on every request) keeps the walked result set stable
 * for the lifetime of one walk.
 *
 * An already-absolute value (a custom range, or the all-time window with no
 * preset and no bounds) passes through unchanged — there is nothing relative
 * left to freeze.
 */
export function resolveTimeRange(value: TimeRangeValue, at: Date): TimeRangeValue {
  if (value.statsPeriod === "" || value.start !== "" || value.end !== "") {
    return value;
  }
  const end = at;
  const start = new Date(end.getTime() - PERIOD_DURATIONS_MS[value.statsPeriod]);
  return { statsPeriod: "", start: start.toISOString(), end: end.toISOString() };
}
