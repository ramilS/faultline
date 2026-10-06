import assert from "node:assert/strict";
import { test } from "node:test";

import { validateGroupName } from "./groupFormValidation.ts";

test("rejects a blank name", () => {
  assert.equal(validateGroupName(""), "Name is required");
  assert.equal(validateGroupName("   "), "Name is required");
});

test("rejects a name over the server's limit", () => {
  assert.equal(validateGroupName("a".repeat(65)), "Name must be 64 characters or fewer");
});

test("accepts a name at the limit", () => {
  assert.equal(validateGroupName("a".repeat(64)), null);
});

// The client mirrors the server's 64-character limit so the user is told
// before the round trip, not after a 400.
test("accepts a trimmable name and reports no error", () => {
  assert.equal(validateGroupName("  backend  "), null);
});
