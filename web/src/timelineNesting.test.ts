import { test } from "node:test";
import assert from "node:assert/strict";

import { layoutBars, MAX_TIMELINE_DEPTH, type TimelineBar } from "./timelineLayout.ts";
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

function depthOf(bars: readonly TimelineBar[], spanId: string): number {
  const bar = bars.find((candidate) => candidate.span.span_id === spanId);
  assert.ok(bar, `expected a bar for span ${spanId}`);
  return bar.depth;
}

test("a root span is depth 0 and its child is depth 1", () => {
  const bars = layoutBars([
    span({ span_id: "a", parent_span_id: null }),
    span({ span_id: "b", parent_span_id: "a" }),
  ]);
  assert.equal(depthOf(bars, "a"), 0);
  assert.equal(depthOf(bars, "b"), 1);
});

test("a span whose parent is missing is treated as a root", () => {
  // Spans and events arrive on separate envelopes and spans expire after 7
  // days while events last 30, so a dangling parent is normal, not corrupt.
  const bars = layoutBars([span({ span_id: "b", parent_span_id: "gone" })]);
  assert.equal(depthOf(bars, "b"), 0);
});

test("a parent cycle does not hang the layout", () => {
  // Span ids are client-supplied. A payload claiming a->b->a must render, not
  // spin: this runs on the main thread while someone is debugging an outage.
  const bars = layoutBars([
    span({ span_id: "a", parent_span_id: "b" }),
    span({ span_id: "b", parent_span_id: "a" }),
  ]);
  assert.equal(bars.length, 2);
});

test("children follow their parent in render order", () => {
  const bars = layoutBars([
    span({ span_id: "a", parent_span_id: null, start_time: "2026-08-11T10:00:00Z" }),
    span({ span_id: "c", parent_span_id: null, start_time: "2026-08-11T10:00:02Z" }),
    span({ span_id: "b", parent_span_id: "a", start_time: "2026-08-11T10:00:01Z" }),
  ]);
  assert.deepEqual(bars.map((bar) => bar.span.span_id), ["a", "b", "c"]);
});

test("a child of a duplicated span id binds to the first occurrence", () => {
  // Two spans can share an id, so "the parent" is ambiguous. First occurrence
  // wins: it is the only choice that is stable under a reordered response, and
  // it keeps every span rendered exactly once.
  const bars = layoutBars([
    span({ span_id: "root", parent_span_id: null, name: "root" }),
    span({ span_id: "db", parent_span_id: "root", name: "db-first" }),
    span({ span_id: "db", parent_span_id: "root", name: "db-second" }),
    span({ span_id: "child", parent_span_id: "db", name: "child" }),
  ]);

  assert.deepEqual(bars.map((bar) => bar.span.name), ["root", "db-first", "child", "db-second"]);
  assert.deepEqual(bars.map((bar) => bar.depth), [0, 1, 2, 1]);
});

test("depth is capped so a deep trace cannot indent off the panel", () => {
  const chain = Array.from({ length: 40 }, (_, index) =>
    span({ span_id: `s${index}`, parent_span_id: index === 0 ? null : `s${index - 1}` }),
  );
  const bars = layoutBars(chain);
  assert.ok(Math.max(...bars.map((bar) => bar.depth)) <= MAX_TIMELINE_DEPTH);
});
