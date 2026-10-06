import assert from "node:assert/strict";
import { test } from "node:test";

import type { EventSummary } from "../../api.ts";
import {
  appendPage,
  beginNewestWalk,
  beginOldestWalk,
  canSelectFurther,
  canSelectNearer,
  needsFurtherPage,
  ordinal,
  pagerFrom,
  positionLabel,
  selectAnchor,
  selectEventId,
  selectFurther,
  selectNearer,
  selectedEvent
} from "./eventPager.ts";

function event(id: string): EventSummary {
  return {
    event_id: id,
    timestamp: "2026-07-26T00:00:00Z",
    received_at: "2026-07-26T00:00:01Z",
    level: "error",
    environment: "production",
    release: null,
    message: "boom"
  };
}

test("a fresh pager selects the anchor event and walks newest-first", () => {
  const pager = pagerFrom([event("a"), event("b")], "cursor-1");

  assert.equal(selectedEvent(pager)?.event_id, "a");
  assert.equal(pager.order, "newest");
  assert.equal(canSelectNearer(pager), false);
  assert.equal(canSelectFurther(pager), true);
});

test("an empty pager has nothing selected and nowhere to go", () => {
  const pager = pagerFrom([], null);

  assert.equal(selectedEvent(pager), null);
  assert.equal(canSelectNearer(pager), false);
  assert.equal(canSelectFurther(pager), false);
});

test("stepping further then nearer returns to the starting event", () => {
  const pager = pagerFrom([event("a"), event("b")], null);

  const stepped = selectNearer(selectFurther(pager));

  assert.equal(selectedEvent(stepped)?.event_id, "a");
});

test("stepping past either end leaves the selection untouched", () => {
  const pager = pagerFrom([event("a")], null);

  assert.equal(selectNearer(pager), pager);
  assert.equal(selectFurther(pager), pager);
});

test("the last loaded event can still go further while a cursor remains", () => {
  const pager = selectFurther(pagerFrom([event("a"), event("b")], "cursor-1"));

  assert.equal(selectedEvent(pager)?.event_id, "b");
  assert.equal(canSelectFurther(pager), true);
  assert.equal(needsFurtherPage(pager), true);
});

test("the last loaded event is terminal once the cursor is exhausted", () => {
  const pager = selectFurther(pagerFrom([event("a"), event("b")], null));

  assert.equal(canSelectFurther(pager), false);
  assert.equal(needsFurtherPage(pager), false);
});

test("appending a page extends the walk without moving the selection", () => {
  const first = selectFurther(pagerFrom([event("a"), event("b")], "cursor-1"));

  const extended = appendPage(first, [event("c")], null);

  assert.deepEqual(
    extended.events.map((entry) => entry.event_id),
    ["a", "b", "c"]
  );
  assert.equal(extended.selectedIndex, 1);
  assert.equal(extended.nextCursor, null);
});

test("appending drops ids the walk already holds", () => {
  const first = pagerFrom([event("a"), event("b")], "cursor-1");

  const extended = appendPage(first, [event("b"), event("c")], null);

  assert.deepEqual(
    extended.events.map((entry) => entry.event_id),
    ["a", "b", "c"]
  );
});

test("appending preserves the order the walk runs in", () => {
  const restarted = beginOldestWalk(pagerFrom([event("a")], "cursor-1"));

  const extended = appendPage(restarted, [event("z")], "cursor-2");

  assert.equal(extended.order, "oldest");
});

test("appending does not mutate the pager it extends", () => {
  const first = pagerFrom([event("a")], "cursor-1");

  appendPage(first, [event("b")], null);

  assert.equal(first.events.length, 1);
  assert.equal(first.nextCursor, "cursor-1");
});

test("jumping to the anchor returns the same pager when already there", () => {
  const pager = pagerFrom([event("a"), event("b")], null);

  assert.equal(selectAnchor(pager), pager);
  assert.equal(selectAnchor(selectFurther(pager)).selectedIndex, 0);
});

test("selecting a loaded event id moves straight to it", () => {
  const pager = pagerFrom([event("a"), event("b"), event("c")], null);

  assert.equal(selectEventId(pager, "c").selectedIndex, 2);
});

test("selecting an event id the walk has not loaded leaves the selection alone", () => {
  // A link to an event outside the first page must not blank the panel; the
  // walk stays on its anchor and the URL keeps pointing where the user asked.
  const pager = pagerFrom([event("a"), event("b")], "cursor");

  assert.equal(selectEventId(pager, "missing"), pager);
});

test("beginOldestWalk marks the pager as walking from the first occurrence", () => {
  const pager = pagerFrom([event("a"), event("b")], "cursor");

  const oldest = beginOldestWalk(pager);

  assert.equal(oldest.order, "oldest");
  assert.equal(oldest.selectedIndex, 0);
  assert.deepEqual(oldest.events, []);
  // Switching direction discards the newest-first window rather than trying to
  // splice two differently-ordered cursors into one list, which cannot be
  // reconciled without a total ordering the endpoint does not expose.
  assert.equal(oldest.nextCursor, null);
});

test("beginNewestWalk discards an oldest-first window the same way", () => {
  const oldest = appendPage(beginOldestWalk(pagerFrom([], null)), [event("a"), event("b")], "cursor");

  const newest = beginNewestWalk(oldest);

  assert.equal(newest.order, "newest");
  assert.deepEqual(newest.events, []);
  assert.equal(newest.nextCursor, null);
});

test("beginning a walk already running in that direction changes nothing", () => {
  const newest = pagerFrom([event("a")], "cursor");
  const oldest = beginOldestWalk(newest);

  // Reference equality is how the caller tells "no refetch needed" from
  // "the window was discarded and must be reloaded".
  assert.equal(beginNewestWalk(newest), newest);
  assert.equal(beginOldestWalk(oldest), oldest);
});

test("a position label names the anchor rather than a bare number", () => {
  const newest = pagerFrom([event("a"), event("b"), event("c")], null);

  // "Event #1" for the newest event reads as "the first time this happened"
  // and means exactly the opposite.
  assert.equal(positionLabel(newest), "Newest");
  assert.equal(positionLabel(selectFurther(newest)), "2nd newest");
  assert.equal(positionLabel(selectFurther(selectFurther(newest))), "3rd newest");
});

test("a position label follows the walk direction", () => {
  const oldest = appendPage(beginOldestWalk(pagerFrom([], null)), [event("a"), event("b")], null);

  assert.equal(positionLabel(oldest), "Oldest");
  assert.equal(positionLabel(selectFurther(oldest)), "2nd oldest");
});

test("an empty walk has no position to label", () => {
  assert.equal(positionLabel(pagerFrom([], null)), "");
});

test("ordinals use th for the 11th to 13th rather than st, nd and rd", () => {
  const cases: ReadonlyArray<readonly [number, string]> = [
    [1, "1st"],
    [2, "2nd"],
    [3, "3rd"],
    [4, "4th"],
    [11, "11th"],
    [12, "12th"],
    [13, "13th"],
    [21, "21st"],
    [22, "22nd"],
    [23, "23rd"],
    [101, "101st"],
    [111, "111th"],
    [112, "112th"],
    [113, "113th"]
  ];

  for (const [position, expected] of cases) {
    assert.equal(ordinal(position), expected);
  }
});
