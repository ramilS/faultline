import assert from "node:assert/strict";
import { test } from "node:test";

import { affectedUsersLabel } from "./affectedUsers.ts";

test("no users affected", () => {
  assert.equal(affectedUsersLabel(0, false), "0 users");
});

test("singular at exactly one", () => {
  assert.equal(affectedUsersLabel(1, false), "1 user");
});

test("plural begins at exactly two", () => {
  assert.equal(affectedUsersLabel(2, false), "2 users");
});

test("a small unsaturated count is plural", () => {
  assert.equal(affectedUsersLabel(3, false), "3 users");
});

test("a large unsaturated count still gets a thousands separator", () => {
  assert.equal(affectedUsersLabel(25000, false), `${(25000).toLocaleString()} users`);
});

test("saturated at the cap renders a floor, not an exact figure", () => {
  assert.equal(affectedUsersLabel(10000, true), `${(10000).toLocaleString()}+ users`);
});

test("a different saturated total renders its own floor with its own separator", () => {
  assert.equal(affectedUsersLabel(12345, true), `${(12345).toLocaleString()}+ users`);
});
