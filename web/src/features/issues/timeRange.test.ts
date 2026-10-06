import assert from "node:assert/strict";
import { test } from "node:test";

import { buildCustomRange, endOfDayRfc3339, MAX_CUSTOM_RANGE_DAYS, startOfDayRfc3339 } from "./timeRange.ts";

const MAX_CUSTOM_RANGE_MS = MAX_CUSTOM_RANGE_DAYS * 24 * 60 * 60 * 1000;

/** dateInput for the calendar day `daysFromEpoch` days after 2026-01-01. */
function isoDate(daysFromEpoch: number): string {
  const date = new Date("2026-01-01T00:00:00.000Z");
  date.setUTCDate(date.getUTCDate() + daysFromEpoch);
  return date.toISOString().slice(0, 10);
}

test("startOfDayRfc3339 anchors to midnight UTC", () => {
  assert.equal(startOfDayRfc3339("2026-07-01"), "2026-07-01T00:00:00.000Z");
});

test("endOfDayRfc3339 anchors to one millisecond before the next midnight UTC", () => {
  assert.equal(endOfDayRfc3339("2026-07-01"), "2026-07-01T23:59:59.999Z");
});

test(`a ${MAX_CUSTOM_RANGE_DAYS}-calendar-day span (inclusive on both ends) is accepted`, () => {
  const from = isoDate(0);
  const to = isoDate(MAX_CUSTOM_RANGE_DAYS - 1);

  const result = buildCustomRange(from, to);

  assert.equal(result.ok, true);
  if (!result.ok) {
    return;
  }
  assert.equal(result.range.start, startOfDayRfc3339(from));
  assert.equal(result.range.end, endOfDayRfc3339(to));
});

// Pins the boundary subtlety directly: start is T00:00:00.000Z and end is
// T23:59:59.999Z, so a MAX_CUSTOM_RANGE_DAYS-calendar-day span measures as
// MAX_CUSTOM_RANGE_DAYS days minus 1ms, not an exact multiple of a day. That
// happens to agree with the server's `w.End.Sub(w.Start) > MaxAbsoluteWindow`
// check (internal/issue/predicate.go), which uses a strict `>` against the
// exact same duration — nothing else pins that the two stay in agreement.
test(`the accepted ${MAX_CUSTOM_RANGE_DAYS}-day span measures as exactly the cap minus one millisecond`, () => {
  const result = buildCustomRange(isoDate(0), isoDate(MAX_CUSTOM_RANGE_DAYS - 1));

  assert.equal(result.ok, true);
  if (!result.ok) {
    return;
  }
  const spanMs = Date.parse(result.range.end) - Date.parse(result.range.start);
  assert.equal(spanMs, MAX_CUSTOM_RANGE_MS - 1);
});

test(`a span one calendar day past the cap is rejected, naming the limit`, () => {
  const result = buildCustomRange(isoDate(0), isoDate(MAX_CUSTOM_RANGE_DAYS));

  assert.equal(result.ok, false);
  if (result.ok) {
    return;
  }
  assert.match(result.error, new RegExp(`\\b${MAX_CUSTOM_RANGE_DAYS} days\\b`));
});
