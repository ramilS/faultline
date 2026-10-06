import { test } from "node:test";
import assert from "node:assert/strict";

import {
  buildMatcher,
  canEditGroupingRule,
  isMergeAliasRule,
  parseMatcher,
  previewMatcher,
  validateMatcherInputs,
} from "./groupingMatcher.ts";

test("buildMatcher composes a non-tag field and pattern", () => {
  assert.equal(buildMatcher({ field: "error.type", pattern: "TypeError", tagKey: "" }), "error.type:TypeError");
});

test("buildMatcher namespaces a tag field under the tag key", () => {
  assert.equal(buildMatcher({ field: "tags", pattern: "prod", tagKey: "env" }), "tags.env:prod");
});

test("validateMatcherInputs rejects a tag field with no key", () => {
  assert.equal(
    validateMatcherInputs({ field: "tags", pattern: "prod", tagKey: "" }),
    "Tag key is required when the field is Tag.",
  );
});

test("validateMatcherInputs rejects an empty pattern regardless of field", () => {
  assert.equal(
    validateMatcherInputs({ field: "logger", pattern: "", tagKey: "" }),
    "Pattern is required.",
  );
  assert.equal(
    validateMatcherInputs({ field: "tags", pattern: "", tagKey: "env" }),
    "Pattern is required.",
  );
});

test("validateMatcherInputs accepts well-formed non-tag and tag inputs", () => {
  assert.equal(validateMatcherInputs({ field: "message", pattern: "boom", tagKey: "" }), null);
  assert.equal(validateMatcherInputs({ field: "tags", pattern: "prod", tagKey: "env" }), null);
});

test("a tag rule can never be built with a missing key once validated", () => {
  const inputs = { field: "tags" as const, pattern: "prod", tagKey: "" };
  assert.notEqual(validateMatcherInputs(inputs), null);
  // Demonstrates why callers must gate buildMatcher on validateMatcherInputs:
  // called directly, it would otherwise emit the malformed "tags.:prod".
  assert.equal(buildMatcher(inputs), "tags.:prod");
});

test("parseMatcher recovers field, tag key, and pattern from a tag matcher", () => {
  assert.deepEqual(parseMatcher("tags.env:prod"), { field: "tags", tagKey: "env", pattern: "prod" });
});

test("parseMatcher recovers a plain field matcher", () => {
  assert.deepEqual(parseMatcher("logger:billing"), { field: "logger", tagKey: "", pattern: "billing" });
});

test("parseMatcher preserves patterns containing colons", () => {
  assert.deepEqual(parseMatcher("message:GET :8080/health"), { field: "message", tagKey: "", pattern: "GET :8080/health" });
});

test("parseMatcher falls back to a message pattern for an unrecognized field", () => {
  assert.deepEqual(parseMatcher("weird-legacy-matcher"), { field: "message", tagKey: "", pattern: "weird-legacy-matcher" });
});

test("buildMatcher and parseMatcher round-trip for every known field", () => {
  const cases: Array<{ field: "error.type" | "message" | "logger" | "fingerprint"; pattern: string }> = [
    { field: "error.type", pattern: "TypeError" },
    { field: "message", pattern: "boom" },
    { field: "logger", pattern: "billing" },
    { field: "fingerprint", pattern: "a".repeat(64) },
  ];
  for (const { field, pattern } of cases) {
    const matcher = buildMatcher({ field, pattern, tagKey: "" });
    assert.deepEqual(parseMatcher(matcher), { field, tagKey: "", pattern });
  }
});

test("buildMatcher and parseMatcher round-trip for a tag field", () => {
  const matcher = buildMatcher({ field: "tags", pattern: "prod", tagKey: "service" });
  assert.deepEqual(parseMatcher(matcher), { field: "tags", tagKey: "service", pattern: "prod" });
});

test("previewMatcher shows placeholders for missing tag key and pattern", () => {
  assert.equal(previewMatcher({ field: "tags", pattern: "", tagKey: "" }), "tags.<key>:<pattern>");
  assert.equal(previewMatcher({ field: "logger", pattern: "billing", tagKey: "" }), "logger:billing");
});

// I-6 regression: the server rejects an unquoted pattern containing
// whitespace (matcher.go's unquotePattern), but the documentation's own
// headline example is message:"payment failed*" — an admin typing
// "payment failed" for Message must not get a bare 400.
test("buildMatcher quotes a pattern containing whitespace", () => {
  assert.equal(
    buildMatcher({ field: "message", pattern: "payment failed*", tagKey: "" }),
    'message:"payment failed*"',
  );
});

test("buildMatcher does not double-quote a pattern the caller already quoted", () => {
  assert.equal(
    buildMatcher({ field: "message", pattern: '"payment failed*"', tagKey: "" }),
    'message:"payment failed*"',
  );
});

test("buildMatcher leaves a whitespace-free pattern unquoted", () => {
  assert.equal(buildMatcher({ field: "message", pattern: "boom", tagKey: "" }), "message:boom");
});

test("previewMatcher mirrors buildMatcher's quoting so the preview matches what submission sends", () => {
  assert.equal(
    previewMatcher({ field: "message", pattern: "payment failed*", tagKey: "" }),
    'message:"payment failed*"',
  );
});

// I-4 regression: the panel must be able to tell a merge-generated alias rule
// apart from a hand-written one, purely from its template shape (there is no
// separate "kind" column server-side), so it can label it system-managed and
// refuse to let it be deleted like an ordinary rule.
test("isMergeAliasRule recognizes the literal template merge mints", () => {
  assert.equal(isMergeAliasRule("merged,issue-42"), true);
  assert.equal(isMergeAliasRule("merged,issue-1"), true);
});

test("isMergeAliasRule rejects hand-written templates, including similar-looking ones", () => {
  assert.equal(isMergeAliasRule("db-timeout,{tags.service}"), false);
  assert.equal(isMergeAliasRule("merged,issue-"), false);
  assert.equal(isMergeAliasRule("merged,issue-42,extra"), false);
  assert.equal(isMergeAliasRule("issue-42"), false);
});

// Regression: the pre-redesign rules panel wired its row summary's onClick straight to
// `edit` unconditionally, so a merge-alias row -- including a rule-derived
// merge *target*, whose row is the only place the post-merge fingerprint
// mapping lives -- could still be opened and resaved in the edit form,
// silently breaking the routing merge just established and reopening the
// duplicate-issue bug merge exists to prevent.
// canEditGroupingRule is the single source of truth the panel now gates the
// edit form on; this pins its contract independent of the panel's own JSX.
test("canEditGroupingRule refuses merge-alias templates and allows hand-written ones", () => {
  assert.equal(canEditGroupingRule("merged,issue-42"), false);
  assert.equal(canEditGroupingRule("merged,issue-1"), false);
  assert.equal(canEditGroupingRule("db-timeout,{tags.service}"), true);
  assert.equal(canEditGroupingRule("payments,payment-failure"), true);
});
