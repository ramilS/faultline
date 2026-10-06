import assert from "node:assert/strict";
import { test } from "node:test";

import { toggleSelection } from "./memberSelection.ts";

// The PUT sends the complete set, so the dialog's job is to compute that set
// correctly. Every bug here silently grants or revokes access.
test("adds an id that was not selected", () => {
  assert.deepEqual(toggleSelection([4], 9), [4, 9]);
});

test("removes an id that was selected", () => {
  assert.deepEqual(toggleSelection([4, 9], 4), [9]);
});

test("never yields a duplicate", () => {
  assert.deepEqual(toggleSelection([4, 9], 9), [4]);
  assert.deepEqual(toggleSelection([], 4), [4]);
});

test("does not mutate the input", () => {
  const selected = [4];
  toggleSelection(selected, 9);
  assert.deepEqual(selected, [4]);
});
