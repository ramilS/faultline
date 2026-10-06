import assert from "node:assert/strict";
import { test } from "node:test";

import { safeNextPath } from "./nextPath.ts";

const accepted: ReadonlyArray<[string, string]> = [
  ["/projects", "/projects"],
  ["/faultline/issues/123", "/faultline/issues/123"],
  ["/faultline/issues?status=unresolved&q=null", "/faultline/issues?status=unresolved&q=null"],
  ["/faultline/issues/123#frame-4", "/faultline/issues/123#frame-4"],
  ["/", "/"]
];

for (const [input, expected] of accepted) {
  test(`accepts same-origin absolute path ${input}`, () => {
    assert.equal(safeNextPath(input), expected);
  });
}

// Each of these would navigate the administrator off-origin while the login
// page still looks legitimate. They are the whole reason this module exists.
const rejected: ReadonlyArray<[string, string]> = [
  ["//evil.com", "protocol-relative URL"],
  ["//evil.com/faultline/issues", "protocol-relative URL with a plausible path"],
  ["/\\evil.com", "backslash is normalised to a slash by browsers"],
  ["\\\\evil.com", "double backslash"],
  ["https://evil.com", "absolute URL"],
  ["http://evil.com", "absolute URL, plain scheme"],
  ["javascript:alert(1)", "script scheme"],
  ["JaVaScRiPt:alert(1)", "script scheme, mixed case"],
  ["data:text/html,<script>", "data scheme"],
  ["evil.com", "schemeless host"],
  ["projects", "relative path"],
  ["/\tprojects", "embedded control character"],
  ["/pro\njects", "embedded newline"],
  ["", "empty string"]
];

for (const [input, reason] of rejected) {
  test(`rejects ${JSON.stringify(input)} — ${reason}`, () => {
    assert.equal(safeNextPath(input), null);
  });
}

test("rejects a missing parameter", () => {
  assert.equal(safeNextPath(null), null);
});

test("rejects a path that exceeds the length ceiling", () => {
  assert.equal(safeNextPath(`/${"a".repeat(2048)}`), null);
});
