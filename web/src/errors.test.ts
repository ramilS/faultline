import assert from "node:assert/strict";
import { test } from "node:test";

import { ApiError } from "./api.ts";
import { isNotFound, messageFor } from "./errors.ts";

test("messageFor reads the message off an ApiError", () => {
  assert.equal(messageFor(new ApiError(404, "project not found")), "project not found");
});

test("messageFor reads the message off a plain Error", () => {
  assert.equal(messageFor(new Error("network down")), "network down");
});

test("messageFor falls back for a non-Error throw", () => {
  assert.equal(messageFor("boom"), "unexpected error");
  assert.equal(messageFor(undefined), "unexpected error");
});

// A page uses this to pick its failure chrome: the not-found route for a 404,
// ErrorState-with-Retry for everything else. Every branch here is one that
// choice depends on.
test("isNotFound is true only for a 404 ApiError", () => {
  assert.equal(isNotFound(new ApiError(404, "not found")), true);
});

test("isNotFound is false for a non-404 ApiError", () => {
  assert.equal(isNotFound(new ApiError(500, "server error")), false);
  assert.equal(isNotFound(new ApiError(403, "forbidden")), false);
});

test("isNotFound is false for a non-ApiError failure", () => {
  assert.equal(isNotFound(new Error("network down")), false);
  assert.equal(isNotFound("boom"), false);
});
