import assert from "node:assert/strict";
import { test } from "node:test";

import { previewRules, SCRUB_PRESETS, validateRules } from "./scrubRules.ts";

const telegram = SCRUB_PRESETS.find((preset) => preset.label === "Telegram bot token")!.rule;
const token = "123456789:AAEhBP0av28fsJz-2kqEs_a1b2c3d4e5f6g";

test("previewRules applies the Telegram preset to a bot URL", () => {
  assert.deepEqual(previewRules([telegram], `https://api.telegram.org/bot${token}/sendMessage`), { output: "https://api.telegram.org/bot[Filtered]/sendMessage", errors: [] });
});

test("previewRules expands group references the way the server does", () => {
  const rule = { name: "x", pattern: "(acct-)\\d+", replacement: "${1}[Filtered]" };
  assert.deepEqual(previewRules([rule], "acct-42"), { output: "acct-[Filtered]", errors: [] });
});

test("previewRules treats $$ as a literal dollar sign and an empty replacement as [Filtered]", () => {
  assert.deepEqual(previewRules([{ name: "x", pattern: "k\\d+", replacement: "$$1" }], "k9"), { output: "$1", errors: [] });
  assert.deepEqual(previewRules([{ name: "x", pattern: "k\\d+", replacement: "" }], "k9"), { output: "[Filtered]", errors: [] });
});

test("previewRules expands group 0 to the whole match and leaves empty braces literal", () => {
  assert.deepEqual(previewRules([{ name: "x", pattern: "k\\d+", replacement: "$0!" }], "k9"), { output: "k9!", errors: [] });
  assert.deepEqual(previewRules([{ name: "x", pattern: "k\\d+", replacement: "<${0}>" }], "k9"), { output: "<k9>", errors: [] });
  assert.deepEqual(previewRules([{ name: "x", pattern: "k\\d+", replacement: "${}" }], "k9"), { output: "${}", errors: [] });
});

test("previewRules applies the rules in list order", () => {
  const rules = [
    { name: "first", pattern: "alpha", replacement: "beta" },
    { name: "second", pattern: "beta", replacement: "gamma" },
  ];
  assert.deepEqual(previewRules(rules, "alpha"), { output: "gamma", errors: [] });
  assert.deepEqual(previewRules([...rules].reverse(), "alpha"), { output: "beta", errors: [] });
});

test("previewRules expands named groups", () => {
  const rule = { name: "x", pattern: "(?<p>bot)\\d+", replacement: "${p}[Filtered]" };
  assert.deepEqual(previewRules([rule], "bot123"), { output: "bot[Filtered]", errors: [] });
});

test("previewRules expands groups that did not participate to nothing", () => {
  const rule = { name: "x", pattern: "a(b)?c", replacement: "<${1}>" };
  assert.deepEqual(previewRules([rule], "ac"), { output: "<>", errors: [] });
});

test("previewRules keeps a trailing lone dollar literal", () => {
  assert.deepEqual(previewRules([{ name: "x", pattern: "k\\d+", replacement: "cost$" }], "k9"), {
    output: "cost$",
    errors: [],
  });
});

test("previewRules skips a rule with an empty pattern", () => {
  assert.deepEqual(previewRules([{ name: "blank", pattern: "", replacement: "X" }], "abc"), { output: "abc", errors: [] });
});

test("previewRules reports each unparsable pattern and still applies the other rules", () => {
  const rules = [
    { name: "bad", pattern: "(", replacement: "" },
    { name: "good", pattern: "secret", replacement: "" },
  ];
  assert.deepEqual(previewRules(rules, "a secret"), {
    output: "a [Filtered]",
    errors: ['"bad": the browser cannot parse this pattern; the server checks it on save.'],
  });
});

const PRESET_EXAMPLES = [
  {
    label: "Telegram bot token",
    example: "https://api.telegram.org/bot123456789:AAEhBP0av28fsJz-2kqEs_a1b2c3d4e5f6g/getMe",
    expected: "https://api.telegram.org/bot[Filtered]/getMe",
  },
  {
    label: "AWS access key ID",
    example: "key AKIAIOSFODNN7EXAMPLE in config",
    expected: "key [Filtered] in config",
  },
  {
    label: "GitHub token",
    example: "token ghp_abcdefghijklmnopqrstuvwxyz0123456789 leaked",
    expected: "token [Filtered] leaked",
  },
];

test("the example table covers every preset", () => {
  assert.deepEqual(
    PRESET_EXAMPLES.map((entry) => entry.label),
    SCRUB_PRESETS.map((preset) => preset.label),
  );
});

for (const { label, example, expected } of PRESET_EXAMPLES) {
  test(`the ${label} preset rewrites its example exactly`, () => {
    const preset = SCRUB_PRESETS.find((candidate) => candidate.label === label)!;
    assert.deepEqual(previewRules([preset.rule], example), { output: expected, errors: [] });
  });
}

const valid = { name: "ok", pattern: "p", replacement: "" };

const VALIDATION_CASES: { title: string; rules: { name: string; pattern: string; replacement: string }[]; expected: string | null }[] = [
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

const expandOn = (pattern: string, replacement: string, sample: string) => previewRules([{ name: "x", pattern, replacement }], sample);

test("previewRules reads an unbraced name as the longest run of Unicode letters, digits and underscore, like Go", () => {
  assert.deepEqual(expandOn("(acct-)\\d+", "$1é", "acct-42"), { output: "", errors: [] });
  assert.deepEqual(expandOn("(acct-)\\d+", "${1}é", "acct-42"), { output: "acct-é", errors: [] });
  assert.deepEqual(expandOn("(acct-)\\d+", "$名", "acct-42"), { output: "", errors: [] });
  assert.deepEqual(expandOn("(acct-)\\d+", "$1x", "acct-42"), { output: "", errors: [] });
  assert.deepEqual(expandOn("(acct-)\\d+", "[$1]", "acct-42"), { output: "[acct-]", errors: [] });
});

test("previewRules still ends an unbraced group number at a character outside the name set", () => {
  assert.deepEqual(expandOn("(acct-)\\d+", "$1", "acct-42"), { output: "acct-", errors: [] });
  assert.deepEqual(expandOn("(acct-)\\d+", "$1-x", "acct-42"), { output: "acct--x", errors: [] });
  assert.deepEqual(expandOn("(acct-)\\d+", "$1 x", "acct-42"), { output: "acct- x", errors: [] });
});

test("previewRules reuses the reference pattern across matches without losing state", () => {
  assert.deepEqual(expandOn("(a)(b)?", "$1é|$$|${1}", "a a"), { output: "|$|a |$|a", errors: [] });
});

test("validateRules counts each unbraced Unicode name as one reference and rejects more than eight", () => {
  for (const reference of ["$1é", "$é1", "$名前"]) {
    const eight = { name: "x", pattern: "a", replacement: reference.repeat(8) };
    const nine = { name: "x", pattern: "a", replacement: reference.repeat(9) };
    assert.equal(validateRules([eight]), null, reference);
    assert.equal(validateRules([nine]), "Rule 1: replacement can reference at most 8 groups.", reference);
  }
});
