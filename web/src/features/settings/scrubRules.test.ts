import assert from "node:assert/strict";
import { test } from "node:test";

import { DEFAULT_SAMPLE, isPresetAdded, presetRule, SCRUB_PRESETS, validateRules } from "./scrubRules.ts";

test("a preset counts as added by its pattern and replacement, not its name", () => {
  const slack = SCRUB_PRESETS.find((preset) => preset.name === "Slack token")!;
  const renamed = { ...presetRule(slack), name: "Our Slack bot" };

  assert.equal(isPresetAdded([], slack), false);
  assert.equal(isPresetAdded([renamed], slack), true);
  assert.equal(isPresetAdded([{ ...renamed, replacement: "x" }], slack), false);
  assert.equal(isPresetAdded([{ ...renamed, pattern: "xoxb-.+" }], slack), false);
});

test("the default sample carries a value for every preset and a phone field", () => {
  const sample = JSON.parse(DEFAULT_SAMPLE) as { user: { phone: string }; breadcrumbs: string[] };

  assert.deepEqual(sample.breadcrumbs, SCRUB_PRESETS.map((preset) => preset.example));
  assert.equal(typeof sample.user.phone, "string");
});

const valid = { name: "ok", pattern: "p", replacement: "" };

const VALIDATION_CASES: { title: string; rules: { name: string; description?: string; pattern: string; replacement: string }[]; expected: string | null }[] = [
  { title: "accepts an empty list", rules: [], expected: null },
  { title: "accepts a rule at every limit", rules: [{ name: "n".repeat(64), pattern: "p".repeat(512), replacement: "r".repeat(128) }], expected: null },
  { title: "accepts 25 rules", rules: Array.from({ length: 25 }, () => valid), expected: null },
  { title: "rejects 26 rules", rules: Array.from({ length: 26 }, () => valid), expected: "At most 25 rules are allowed." },
  { title: "rejects a blank name", rules: [valid, { ...valid, name: "   " }], expected: "Rule 2: name is required." },
  { title: "rejects a name over 64 bytes", rules: [{ ...valid, name: "n".repeat(65) }], expected: "Rule 1: name can be at most 64 bytes." },
  { title: "measures the name in UTF-8 bytes", rules: [{ ...valid, name: "я".repeat(33) }], expected: "Rule 1: name can be at most 64 bytes." },
  { title: "accepts a multi-byte name of 64 bytes", rules: [{ ...valid, name: "я".repeat(32) }], expected: null },
  { title: "rejects a blank pattern", rules: [valid, valid, { ...valid, pattern: "" }], expected: "Rule 3: pattern is required." },
  { title: "rejects a pattern over 512 bytes", rules: [valid, { ...valid, pattern: "p".repeat(513) }], expected: "Rule 2: pattern can be at most 512 bytes." },
  { title: "accepts a description of 200 bytes", rules: [{ ...valid, description: "d".repeat(200) }], expected: null },
  { title: "rejects a description over 200 bytes", rules: [valid, { ...valid, description: "d".repeat(201) }], expected: "Rule 2: description can be at most 200 bytes." },
  { title: "rejects a replacement over 128 bytes", rules: [{ ...valid, replacement: "r".repeat(129) }], expected: "Rule 1: replacement can be at most 128 bytes." },
  { title: "accepts 8 group references", rules: [{ ...valid, replacement: "$1$2${3}$4$5$6$7${name}" }], expected: null },
  { title: "rejects 9 group references", rules: [{ ...valid, replacement: "$1$2$3$4$5$6$7$8$9" }], expected: "Rule 1: replacement can reference at most 8 groups." },
  { title: "does not count $$ as a group reference", rules: [{ ...valid, replacement: "$$1$$2$$3$$4$$5$$6$$7$$8$$9" }], expected: null },
];

for (const { title, rules, expected } of VALIDATION_CASES) {
  test(`validateRules ${title}`, () => {
    assert.equal(validateRules(rules), expected);
  });
}

test("validateRules counts each unbraced Unicode name as one reference and rejects more than eight", () => {
  for (const reference of ["$1é", "$é1", "$名前"]) {
    const eight = { name: "x", pattern: "a", replacement: reference.repeat(8) };
    const nine = { name: "x", pattern: "a", replacement: reference.repeat(9) };
    assert.equal(validateRules([eight]), null, reference);
    assert.equal(validateRules([nine]), "Rule 1: replacement can reference at most 8 groups.", reference);
  }
});
