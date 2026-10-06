import assert from "node:assert/strict";
import { test } from "node:test";

import type { Bucket } from "../../api.ts";
import { CHART_HEIGHT, CHART_WIDTH, layoutStatsChart } from "./statsChart.ts";

/** Matches the hourly spacing `buckets()` generates, so spacing-derived assertions stay meaningful. */
const HOUR_MS = 60 * 60 * 1000;

const buckets = (counts: number[]): Bucket[] =>
  counts.map((count, index) => ({ bucket: `2026-07-26T0${index}:00:00Z`, count }));

test("no buckets produce no bars", () => {
  const layout = layoutStatsChart([], HOUR_MS);

  assert.deepEqual(layout.bars, []);
  assert.equal(layout.peak, 0);
  assert.equal(layout.total, 0);
});

test("peak and total summarise the window", () => {
  const layout = layoutStatsChart(buckets([1, 7, 2]), HOUR_MS);

  assert.equal(layout.peak, 7);
  assert.equal(layout.total, 10);
});

test("the tallest bucket fills the chart and sits on the baseline", () => {
  const layout = layoutStatsChart(buckets([0, 4]), HOUR_MS);
  const tallest = layout.bars[1];

  assert.equal(tallest.height, CHART_HEIGHT);
  assert.equal(tallest.y, 0);
});

test("an empty bucket draws nothing at all", () => {
  const [empty] = layoutStatsChart(buckets([0, 100]), HOUR_MS).bars;

  assert.equal(empty.height, 0);
  assert.equal(empty.y, CHART_HEIGHT);
});

test("a bucket dwarfed by the peak still paints a visible sliver", () => {
  const [tiny] = layoutStatsChart(buckets([1, 100000]), HOUR_MS).bars;

  assert.ok(tiny.height >= 2, `expected a visible bar, got ${tiny.height}`);
});

test("an all-zero window draws a flat baseline rather than dividing by zero", () => {
  const layout = layoutStatsChart(buckets([0, 0, 0]), HOUR_MS);

  assert.equal(layout.peak, 0);
  assert.ok(layout.bars.every((bar) => bar.height === 0));
});

test("bars are evenly spaced across the viewBox and never overlap", () => {
  const layout = layoutStatsChart(buckets([1, 1, 1, 1]), HOUR_MS);

  assert.equal(layout.bars[0].x, 0);
  assert.equal(layout.bars[3].x, (CHART_WIDTH / 4) * 3);
  assert.ok(layout.bars[3].x + layout.bars[3].width <= CHART_WIDTH);
  for (let index = 1; index < layout.bars.length; index += 1) {
    const previous = layout.bars[index - 1];
    assert.ok(previous.x + previous.width <= layout.bars[index].x);
  }
});

test("a bar's window ends where the next bucket starts", () => {
  const layout = layoutStatsChart(
    [
      { bucket: "2026-08-11T14:00:00Z", count: 3 },
      { bucket: "2026-08-11T15:00:00Z", count: 5 }
    ],
    HOUR_MS
  );

  assert.equal(layout.bars[0].bucketEnd, "2026-08-11T15:00:00Z");
});

test("the final bar's window is derived from the bucket spacing, not left open", () => {
  const layout = layoutStatsChart(
    [
      { bucket: "2026-08-11T14:00:00Z", count: 3 },
      { bucket: "2026-08-11T15:00:00Z", count: 5 }
    ],
    HOUR_MS
  );

  // An open-ended last bucket would link to "everything since 15:00", which
  // includes events the bar does not represent.
  assert.equal(layout.bars[1].bucketEnd, "2026-08-11T16:00:00Z");
});

test("a single-bucket chart takes its window width from the period, since there is no spacing to infer from", () => {
  const layout = layoutStatsChart([{ bucket: "2026-08-11T14:00:00Z", count: 3 }], HOUR_MS);

  assert.equal(layout.bars[0].bucketEnd, "2026-08-11T15:00:00Z");
});
