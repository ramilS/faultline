import assert from "node:assert/strict";
import { test } from "node:test";

import type { IssuePage, IssueSummary } from "../api.ts";
import { appendPage, emptyStream, hasMore, replaceIssues } from "./issueStream.ts";

function issue(id: number, overrides: Partial<IssueSummary> = {}): IssueSummary {
  return {
    id,
    project_slug: "web",
    fingerprint: `fp${id}`,
    title: `Issue ${id}`,
    culprit: `module.fn${id}`,
    level: "error",
    status: "unresolved",
    assignee: null,
    times_seen: id,
    users_seen: 1,
    first_seen: "2026-07-25T08:46:29Z",
    last_seen: "2026-07-26T08:38:20Z",
    counts_24h: [],
    counts_14d: [],
    ...overrides
  };
}

function page(issues: readonly IssueSummary[], nextCursor: string | null): IssuePage {
  return { issues: [...issues], next_cursor: nextCursor };
}

const KEY = "status=all";
const OTHER_KEY = "q=cart";

test("an empty stream has nothing loaded and nothing to load", () => {
  const stream = emptyStream(KEY);

  assert.deepEqual(stream, { key: KEY, issues: [], cursor: null });
  assert.equal(hasMore(stream), false);
});

test("the first page populates the stream and carries its cursor", () => {
  const stream = appendPage(emptyStream(KEY), page([issue(1), issue(2)], "cursor-1"), KEY);

  assert.deepEqual(
    stream.issues.map((row) => row.id),
    [1, 2]
  );
  assert.equal(stream.cursor, "cursor-1");
  assert.equal(hasMore(stream), true);
});

test("a second page is appended after the first", () => {
  const first = appendPage(emptyStream(KEY), page([issue(1), issue(2)], "cursor-1"), KEY);
  const second = appendPage(first, page([issue(3), issue(4)], "cursor-2"), KEY);

  assert.deepEqual(
    second.issues.map((row) => row.id),
    [1, 2, 3, 4]
  );
  assert.equal(second.cursor, "cursor-2");
});

test("a terminal next_cursor of null ends the stream", () => {
  const first = appendPage(emptyStream(KEY), page([issue(1)], "cursor-1"), KEY);
  const last = appendPage(first, page([issue(2)], null), KEY);

  assert.deepEqual(
    last.issues.map((row) => row.id),
    [1, 2]
  );
  assert.equal(last.cursor, null);
  assert.equal(hasMore(last), false);
});

test("changing filters resets the accumulation instead of appending", () => {
  // The pre-redesign list silently truncated instead of paginating; the bug
  // this replaces it with would be splicing the old query's rows into the new
  // result set.
  const loaded = appendPage(
    appendPage(emptyStream(KEY), page([issue(1), issue(2)], "cursor-1"), KEY),
    page([issue(3)], "cursor-2"),
    KEY
  );

  const refiltered = appendPage(loaded, page([issue(9)], null), OTHER_KEY);

  assert.equal(refiltered.key, OTHER_KEY);
  assert.deepEqual(
    refiltered.issues.map((row) => row.id),
    [9]
  );
  assert.equal(refiltered.cursor, null);
});

test("an overlapping page does not duplicate rows", () => {
  const first = appendPage(emptyStream(KEY), page([issue(1), issue(2)], "cursor-1"), KEY);
  const overlapping = appendPage(first, page([issue(2), issue(3)], null), KEY);

  assert.deepEqual(
    overlapping.issues.map((row) => row.id),
    [1, 2, 3]
  );
});

test("appending never mutates the stream it was given", () => {
  const first = appendPage(emptyStream(KEY), page([issue(1)], "cursor-1"), KEY);
  const before = first.issues;

  appendPage(first, page([issue(2)], null), KEY);

  assert.equal(first.issues, before);
  assert.deepEqual(
    first.issues.map((row) => row.id),
    [1]
  );
  assert.equal(first.cursor, "cursor-1");
});

test("replaceIssues swaps only the rows it was given", () => {
  const stream = appendPage(emptyStream(KEY), page([issue(1), issue(2), issue(3)], null), KEY);

  const updated = replaceIssues(stream, [issue(2, { status: "resolved" })]);

  assert.deepEqual(
    updated.issues.map((row) => [row.id, row.status]),
    [
      [1, "unresolved"],
      [2, "resolved"],
      [3, "unresolved"]
    ]
  );
  assert.equal(updated.cursor, stream.cursor);
  assert.equal(updated.key, stream.key);
});

test("replaceIssues leaves the original stream untouched", () => {
  const stream = appendPage(emptyStream(KEY), page([issue(1)], null), KEY);

  replaceIssues(stream, [issue(1, { status: "ignored" })]);

  assert.equal(stream.issues[0].status, "unresolved");
});

test("replaceIssues ignores rows that are not loaded", () => {
  const stream = appendPage(emptyStream(KEY), page([issue(1)], null), KEY);

  const updated = replaceIssues(stream, [issue(42, { status: "resolved" })]);

  assert.deepEqual(
    updated.issues.map((row) => row.id),
    [1]
  );
});

test("replaceIssues with nothing to apply returns the same stream", () => {
  const stream = appendPage(emptyStream(KEY), page([issue(1)], null), KEY);

  assert.equal(replaceIssues(stream, []), stream);
});
