import assert from "node:assert/strict";
import { test } from "node:test";

import { createIssueViewTracker } from "./issueViews.ts";

function deferred(): { promise: Promise<void>; resolve: () => void; reject: (reason: unknown) => void } {
  let resolve!: () => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<void>((onResolve, onReject) => {
    resolve = onResolve;
    reject = onReject;
  });
  return { promise, resolve, reject };
}

test("settled waits for a view still in flight", async () => {
  const response = deferred();
  const tracker = createIssueViewTracker(() => response.promise);
  tracker.record(7);

  let settled = false;
  const waiting = tracker.settled().then(() => {
    settled = true;
  });
  await Promise.resolve();
  assert.equal(settled, false);

  response.resolve();
  await waiting;
  assert.equal(settled, true);
});

test("a failed view settles quietly instead of rejecting", async () => {
  const response = deferred();
  const tracker = createIssueViewTracker(() => response.promise);
  tracker.record(7);

  response.reject(new Error("503"));
  await assert.doesNotReject(tracker.settled());
});

test("each record posts the issue it was given", async () => {
  const posted: number[] = [];
  const tracker = createIssueViewTracker(async (issueId) => {
    posted.push(issueId);
  });
  tracker.record(3);
  tracker.record(5);
  await tracker.settled();
  assert.deepEqual(posted, [3, 5]);
});
