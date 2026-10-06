import assert from "node:assert/strict";
import { test } from "node:test";

import { ALL_TIME_WINDOW, STATS_PERIODS } from "../../lib/issueFilters.ts";
import { resolveTimeRange } from "./eventWindow.ts";

const AT = new Date("2026-09-09T12:00:00.000Z");

const PERIOD_MS: Record<(typeof STATS_PERIODS)[number], number> = {
  "24h": 24 * 60 * 60 * 1000,
  "7d": 7 * 24 * 60 * 60 * 1000,
  "14d": 14 * 24 * 60 * 60 * 1000,
  "30d": 30 * 24 * 60 * 60 * 1000,
  "90d": 90 * 24 * 60 * 60 * 1000
};

for (const period of STATS_PERIODS) {
  test(`resolves the "${period}" preset to an absolute window ending at "at"`, () => {
    const resolved = resolveTimeRange({ statsPeriod: period, start: "", end: "" }, AT);

    assert.equal(resolved.statsPeriod, "");
    assert.equal(resolved.end, AT.toISOString());
    assert.equal(Date.parse(resolved.end) - Date.parse(resolved.start), PERIOD_MS[period]);
  });
}

test("passes the all-time window through unchanged", () => {
  const resolved = resolveTimeRange({ ...ALL_TIME_WINDOW }, AT);

  assert.deepEqual(resolved, ALL_TIME_WINDOW);
});

test("passes an already-absolute custom range through unchanged", () => {
  const custom = { statsPeriod: "" as const, start: "2026-08-01T00:00:00.000Z", end: "2026-08-31T23:59:59.999Z" };

  const resolved = resolveTimeRange(custom, AT);

  assert.deepEqual(resolved, custom);
});

test("two calls at different instants for the same preset produce different absolute bounds", () => {
  const first = resolveTimeRange({ statsPeriod: "24h", start: "", end: "" }, AT);
  const later = resolveTimeRange({ statsPeriod: "24h", start: "", end: "" }, new Date(AT.getTime() + 60 * 60 * 1000));

  assert.notEqual(first.start, later.start);
  assert.notEqual(first.end, later.end);
});
