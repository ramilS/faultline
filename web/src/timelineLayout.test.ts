import { test } from "node:test";
import assert from "node:assert/strict";

import { layoutBars } from "./timelineLayout.ts";
import type { Span } from "./api.ts";

function span(overrides: Partial<Span>): Span {
  return {
    span_id: "s",
    parent_span_id: null,
    name: "n",
    start_time: "2026-07-15T12:00:00Z",
    duration_ms: 0,
    status: "ok",
    attributes: {},
    ...overrides,
  };
}

test("layoutBars returns empty for no spans", () => {
  assert.deepEqual(layoutBars([]), []);
});

test("layoutBars positions spans on a shared axis", () => {
  const bars = layoutBars([
    span({ span_id: "root", start_time: "2026-07-15T12:00:00.000Z", duration_ms: 100 }),
    span({ span_id: "child", start_time: "2026-07-15T12:00:00.050Z", duration_ms: 25 }),
  ]);
  assert.equal(bars[0].offsetPercent, 0);
  assert.equal(bars[0].widthPercent, 100);
  assert.ok(Math.abs(bars[1].offsetPercent - 50) < 0.5);
  assert.ok(Math.abs(bars[1].widthPercent - 25) < 0.5);
});

test("spans sharing a span id each render at their own start", () => {
  // span_id is client-supplied text with no unique constraint and ingest does
  // not deduplicate, so a hand-rolled instrumentation reusing "db" is normal
  // input. Keying the layout on it would drop one bar and draw the survivor at
  // the other one's start.
  const bars = layoutBars([
    span({ span_id: "root", start_time: "2026-08-11T10:00:00.000Z", duration_ms: 5000 }),
    span({ span_id: "db", start_time: "2026-08-11T10:00:00.100Z", duration_ms: 40 }),
    span({ span_id: "db", start_time: "2026-08-11T10:00:03.000Z", duration_ms: 900 }),
  ]);

  assert.equal(bars.length, 3);
  assert.deepEqual(
    bars.map((bar) => bar.offsetPercent),
    [0, 2, 60],
  );
});

test("layoutBars collapses a single instant span to full width", () => {
  const bars = layoutBars([span({ span_id: "only", duration_ms: 0 })]);
  assert.equal(bars[0].offsetPercent, 0);
  assert.equal(bars[0].widthPercent, 100);
});
