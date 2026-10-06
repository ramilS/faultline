import assert from "node:assert/strict";
import { test } from "node:test";

import { ApiError } from "../api.ts";
import { classifyProbeFailure, sessionFromProbe, type SessionUser } from "./session.ts";

const user: SessionUser = { id: 7, name: "Administrator", email: "admin@example.com", role: "owner" };

test("a successful probe yields an authenticated session carrying the user", () => {
  assert.deepEqual(sessionFromProbe({ kind: "user", user }), { state: "authenticated", user });
});

test("401 yields anonymous, not an error state", () => {
  // Regression guard: treating "not signed in" as a failure would show a
  // server-error screen where a login form belongs.
  assert.deepEqual(sessionFromProbe(classifyProbeFailure(new ApiError(401, "authentication required"))), {
    state: "anonymous"
  });
});

test("500 yields unavailable rather than anonymous", () => {
  // Guessing "anonymous" here would bounce the administrator to a login form
  // that cannot succeed, hiding the real fault.
  assert.deepEqual(sessionFromProbe(classifyProbeFailure(new ApiError(500, "internal server error"))), {
    state: "unavailable",
    message: "internal server error"
  });
});

test("403 origin rejection yields unavailable — it is a deployment fault, not a sign-out", () => {
  assert.deepEqual(sessionFromProbe(classifyProbeFailure(new ApiError(403, "origin not allowed"))), {
    state: "unavailable",
    message: "origin not allowed"
  });
});

test("a network failure yields unavailable", () => {
  const session = sessionFromProbe(classifyProbeFailure(new TypeError("Failed to fetch")));
  assert.equal(session.state, "unavailable");
});

test("a non-Error rejection still produces a usable message", () => {
  const session = sessionFromProbe(classifyProbeFailure("boom"));
  assert.deepEqual(session, { state: "unavailable", message: "unexpected error" });
});

test("only 401 maps to unauthorized", () => {
  for (const status of [400, 403, 404, 429, 500, 502, 503]) {
    assert.notEqual(
      classifyProbeFailure(new ApiError(status, "x")).kind,
      "unauthorized",
      `status ${status} must not be read as a sign-out`
    );
  }
});
