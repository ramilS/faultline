import assert from "node:assert/strict";
import { test } from "node:test";

import { MAX_VAR_ENTRIES, MAX_VAR_TEXT, describeVar, varEntries } from "./frameVars.ts";

const DESCRIBE_CASES: ReadonlyArray<{ name: string; value: unknown; expected: ReturnType<typeof describeVar> }> = [
  { name: "a number", value: 42, expected: { kind: "scalar", text: "42", truncated: 0 } },
  { name: "a boolean", value: false, expected: { kind: "scalar", text: "false", truncated: 0 } },
  { name: "null", value: null, expected: { kind: "scalar", text: "null", truncated: 0 } },
  { name: "a string verbatim", value: "'<b>bold</b>'", expected: { kind: "scalar", text: "'<b>bold</b>'", truncated: 0 } },
  { name: "a filtered value", value: "[Filtered]", expected: { kind: "scalar", text: "[Filtered]", truncated: 0 } },
  { name: "an empty object", value: {}, expected: { kind: "collection", summary: "{} 0 keys" } },
  { name: "an object", value: { a: 1, b: 2 }, expected: { kind: "collection", summary: "{…} 2 keys" } },
  { name: "a one-key object", value: { a: 1 }, expected: { kind: "collection", summary: "{…} 1 key" } },
  { name: "an array", value: [1, 2, 3], expected: { kind: "collection", summary: "[…] 3 items" } },
  { name: "a one-item array", value: ["x"], expected: { kind: "collection", summary: "[…] 1 item" } }
];

for (const testCase of DESCRIBE_CASES) {
  test(`describeVar: ${testCase.name}`, () => {
    assert.deepEqual(describeVar(testCase.value), testCase.expected);
  });
}

test("a long string is cut at the text cap and reports how much was dropped", () => {
  const huge = "x".repeat(1 << 20);

  const described = describeVar(huge);

  assert.deepEqual(described, { kind: "scalar", text: "x".repeat(MAX_VAR_TEXT), truncated: (1 << 20) - MAX_VAR_TEXT });
});

test("entries keep the order the SDK sent and number array items", () => {
  assert.deepEqual(varEntries({ b: 1, a: 2 }), { entries: [{ key: "b", value: 1 }, { key: "a", value: 2 }], omitted: 0 });
  assert.deepEqual(varEntries(["x", "y"]), { entries: [{ key: "0", value: "x" }, { key: "1", value: "y" }], omitted: 0 });
});

test("entries past the cap are counted rather than listed", () => {
  const wide = Object.fromEntries(Array.from({ length: MAX_VAR_ENTRIES + 7 }, (_, index) => [`k${index}`, index]));

  const { entries, omitted } = varEntries(wide);

  assert.equal(entries.length, MAX_VAR_ENTRIES);
  assert.equal(entries.at(-1)?.key, `k${MAX_VAR_ENTRIES - 1}`);
  assert.equal(omitted, 7);
});

test("a scalar has no entries", () => {
  assert.deepEqual(varEntries("text"), { entries: [], omitted: 0 });
});
