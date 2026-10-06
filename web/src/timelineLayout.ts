import type { Span } from "./api";

export interface TimelineBar {
  span: Span;
  offsetPercent: number;
  widthPercent: number;
  depth: number;
}

// Depth is clamped here; a chain nested deeper than this renders at the same
// indent instead of pushing bars off the panel.
export const MAX_TIMELINE_DEPTH = 6;

// layoutBars positions each span on a shared time axis spanning the earliest
// start to the latest end. A zero-width total collapses to full-width bars so a
// single instant still renders. Spans are reordered so each span's descendants
// immediately follow it, and annotated with their nesting depth.
export function layoutBars(spans: Span[]): TimelineBar[] {
  if (spans.length === 0) {
    return [];
  }
  const indexBySpanId = indexSpanIds(spans);
  const starts = spans.map((span) => Date.parse(span.start_time));
  const ends = spans.map((span, index) => starts[index] + span.duration_ms);
  const min = starts.reduce((acc, value) => (value < acc ? value : acc), starts[0]);
  const max = ends.reduce((acc, value) => (value > acc ? value : acc), ends[0]);
  const total = max - min;

  return orderByHierarchy(spans, indexBySpanId).map((index) => {
    const span = spans[index];
    const offset = total === 0 ? 0 : ((starts[index] - min) / total) * 100;
    const width = total === 0 ? 100 : Math.max((span.duration_ms / total) * 100, 1);
    return { span, offsetPercent: offset, widthPercent: width, depth: depthOf(index, spans, indexBySpanId) };
  });
}

/**
 * Maps each span id to the position of its *first* occurrence.
 *
 * Everything below keys on array position rather than on span_id, because
 * span_id is a client-supplied column with no unique constraint and ingest
 * does not deduplicate spans: two spans really can share one id. Keying on the
 * id collapses them, dropping one bar and drawing the survivor at the other's
 * start time. Only parent references still go through the id, and there the
 * first occurrence wins — the one choice that is stable however the response
 * happens to be ordered.
 */
function indexSpanIds(spans: readonly Span[]): ReadonlyMap<string, number> {
  const indexBySpanId = new Map<string, number>();
  spans.forEach((span, index) => {
    if (!indexBySpanId.has(span.span_id)) {
      indexBySpanId.set(span.span_id, index);
    }
  });
  return indexBySpanId;
}

/**
 * Depth of a span within its trace, found by walking parent_span_id up to a
 * root.
 *
 * Walks with a visited set rather than a plain "follow the parent reference"
 * loop or recursion: span ids are client-supplied, so a payload can claim
 * a->b->a. Nothing else here bounds that walk — the depth cap below is
 * applied only to the final result, not to how far the walk goes — so
 * without the visited set this loop would run forever instead of hanging on
 * to a stack. Tracking visited positions lets it detect the repeat and treat
 * the span as a root instead.
 */
function depthOf(start: number, spans: readonly Span[], indexBySpanId: ReadonlyMap<string, number>): number {
  const visited = new Set<number>();
  let index = start;
  let depth = 0;
  for (;;) {
    const parentId = spans[index].parent_span_id;
    if (parentId === null) {
      break;
    }
    if (visited.has(index)) {
      return 0;
    }
    visited.add(index);
    const parent = indexBySpanId.get(parentId);
    if (parent === undefined) {
      break;
    }
    depth += 1;
    index = parent;
  }
  return Math.min(depth, MAX_TIMELINE_DEPTH);
}

/**
 * Orders span positions so each span's descendants immediately follow it,
 * matching how a trace waterfall reads. A span whose parent is missing or part
 * of a cycle is treated as a root, and every position is emitted exactly once,
 * so nothing traced is silently dropped.
 */
function orderByHierarchy(spans: readonly Span[], indexBySpanId: ReadonlyMap<string, number>): number[] {
  const parentOf = spans.map((span) =>
    span.parent_span_id === null ? undefined : indexBySpanId.get(span.parent_span_id),
  );
  const childrenOf = new Map<number, number[]>();
  parentOf.forEach((parent, index) => {
    if (parent === undefined) {
      return;
    }
    const siblings = childrenOf.get(parent) ?? [];
    siblings.push(index);
    childrenOf.set(parent, siblings);
  });

  const visited = new Set<number>();
  const ordered: number[] = [];

  function visit(index: number): void {
    if (visited.has(index)) {
      return;
    }
    visited.add(index);
    ordered.push(index);
    for (const child of childrenOf.get(index) ?? []) {
      visit(child);
    }
  }

  spans.forEach((_, index) => {
    if (parentOf[index] === undefined) {
      visit(index);
    }
  });
  // A cycle (a's parent is b, b's parent is a) has no member that qualifies as
  // a root above, so the pass over roots never reaches either one. Emit any
  // spans still unvisited, in their original order, so a cyclic payload still
  // renders every span instead of vanishing.
  spans.forEach((_, index) => visit(index));

  return ordered;
}
