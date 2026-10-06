import assert from "node:assert/strict";
import { test } from "node:test";

import { issueIdFromParam } from "./issueIdParam.ts";

test("a positive integer segment is the issue id", () => {
  assert.equal(issueIdFromParam("42"), 42);
});

test("zero and negatives are not issue ids", () => {
  assert.equal(issueIdFromParam("0"), null);
  assert.equal(issueIdFromParam("-1"), null);
});

test("a non-integer segment is not an issue id", () => {
  assert.equal(issueIdFromParam("events"), null);
  assert.equal(issueIdFromParam("4.5"), null);
  assert.equal(issueIdFromParam(""), null);
  assert.equal(issueIdFromParam(undefined), null);
});
