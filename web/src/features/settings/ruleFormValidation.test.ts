import assert from "node:assert/strict";
import { test } from "node:test";

import type { MatcherInputs } from "../../groupingMatcher.ts";
import { hasRuleFormError, ruleFormErrors } from "./ruleFormValidation.ts";

const tagInputs: MatcherInputs = { field: "tags", pattern: "prod", tagKey: "env" };

test("reports no error for a complete tag rule", () => {
  const errors = ruleFormErrors(tagInputs, "billing,{tags.env}");
  assert.deepEqual(errors, { tagKey: null, pattern: null, template: null });
  assert.equal(hasRuleFormError(errors), false);
});

test("attributes a missing tag key to the tag key field only", () => {
  const errors = ruleFormErrors({ ...tagInputs, tagKey: "" }, "billing");
  assert.equal(errors.tagKey, "Tag key is required when the field is Tag.");
  assert.equal(errors.pattern, null);
});

test("attributes a missing pattern to the pattern field only", () => {
  const errors = ruleFormErrors({ ...tagInputs, pattern: "   " }, "billing");
  assert.equal(errors.pattern, "Pattern is required.");
  assert.equal(errors.tagKey, null);
});

test("reports both matcher fields when both are missing", () => {
  const errors = ruleFormErrors({ field: "tags", pattern: "", tagKey: "" }, "billing");
  assert.equal(errors.tagKey, "Tag key is required when the field is Tag.");
  assert.equal(errors.pattern, "Pattern is required.");
});

test("never asks for a tag key on a field that has no tag key", () => {
  const errors = ruleFormErrors({ field: "logger", pattern: "", tagKey: "" }, "billing");
  assert.equal(errors.tagKey, null);
  assert.equal(errors.pattern, "Pattern is required.");
});

test("requires a template that is more than whitespace", () => {
  const errors = ruleFormErrors(tagInputs, "  ");
  assert.equal(errors.template, "Template is required.");
  assert.equal(hasRuleFormError(errors), true);
});
