import { test } from "node:test";
import assert from "node:assert/strict";

import { mergeConfirmationMessage } from "./issueMerge.ts";

test("mergeConfirmationMessage states the target title and that it is a one-way move", () => {
  const message = mergeConfirmationMessage(5, "NullPointerException in checkout");
  assert.match(message, /5 events/);
  assert.match(message, /"NullPointerException in checkout"/);
  assert.match(message, /cannot be split apart again/);
});

test("mergeConfirmationMessage uses singular \"event\" for a single occurrence", () => {
  const message = mergeConfirmationMessage(1, "Target issue");
  assert.match(message, /1 event\b/);
  assert.doesNotMatch(message, /1 events/);
});
