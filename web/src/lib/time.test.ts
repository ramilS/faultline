import assert from "node:assert/strict";
import { test } from "node:test";

import { absoluteTime, axisLabel, compactAge } from "./time.ts";

const now = new Date("2026-07-26T12:00:00.000Z");
const ago = (ms: number) => new Date(now.getTime() - ms).toISOString();

const SECOND = 1000;
const MINUTE = 60 * SECOND;
const HOUR = 60 * MINUTE;
const DAY = 24 * HOUR;

const cases: ReadonlyArray<[string, string, string]> = [
  [ago(0), "now", "the present instant"],
  [ago(5 * SECOND), "5s", "seconds"],
  [ago(59 * SECOND), "59s", "just under a minute"],
  [ago(60 * SECOND), "1m", "the minute boundary"],
  [ago(90 * SECOND), "1m", "rounds down rather than up"],
  [ago(59 * MINUTE), "59m", "just under an hour"],
  [ago(HOUR), "1h", "the hour boundary"],
  [ago(23 * HOUR), "23h", "just under a day"],
  [ago(DAY), "1d", "the day boundary"],
  [ago(6 * DAY), "6d", "under a week"],
  [ago(7 * DAY), "1w", "the week boundary"],
  [ago(60 * DAY), "8w", "weeks keep counting past a month"]
];

for (const [iso, expected, reason] of cases) {
  test(`compactAge renders ${expected} — ${reason}`, () => {
    assert.equal(compactAge(iso, now), expected);
  });
}

test("a future timestamp reads as now rather than a negative age", () => {
  // Server and browser clocks drift; "-3s" in an issue table looks like a bug.
  assert.equal(compactAge(ago(-3 * SECOND), now), "now");
});

test("an unparseable timestamp yields a dash instead of Invalid Date", () => {
  assert.equal(compactAge("not-a-date", now), "—");
  assert.equal(compactAge("", now), "—");
});

test("absoluteTime returns a parseable, human-readable instant", () => {
  const rendered = absoluteTime("2026-07-26T12:00:00.000Z");
  assert.notEqual(rendered, "—");
  assert.ok(rendered.includes("2026"), `expected a year in ${rendered}`);
});

test("absoluteTime degrades on bad input rather than throwing", () => {
  assert.equal(absoluteTime("not-a-date"), "—");
});

test("axisLabel names an hourly bucket by its time of day", () => {
  assert.equal(axisLabel("2026-09-08T14:00:00Z", "hour", "UTC"), "14:00");
});

test("axisLabel names a daily bucket by its date, without a year or time", () => {
  assert.equal(axisLabel("2026-09-08T00:00:00Z", "day", "UTC"), "Sep 8");
});

test("axisLabel reports an unparseable instant with the placeholder, not 'Invalid Date'", () => {
  assert.equal(axisLabel("not a date", "hour", "UTC"), "—");
});
