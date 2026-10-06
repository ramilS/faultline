import assert from "node:assert/strict";
import { test } from "node:test";

import { levelVariant } from "./levelVariant.ts";

// The ingest edge accepts exactly these five (`internal/ingest/envelope.go`).
// Every arm is covered, including the fallback, because a missed arm ships
// wrong severity copy rather than failing loudly.
const cases: ReadonlyArray<[string, string, string]> = [
  ["fatal", "Fatal", "text-level-fatal"],
  ["error", "Error", "text-level-error"],
  ["warning", "Warning", "text-level-warning"],
  ["info", "Info", "text-level-info"],
  ["debug", "Debug", "text-level-debug"]
];

for (const [level, label, colorClass] of cases) {
  test(`maps ${level} to its own label and colour`, () => {
    const variant = levelVariant(level);
    assert.equal(variant.label, label);
    assert.ok(variant.className.includes(colorClass), `expected ${colorClass} in ${variant.className}`);
  });
}

test("an unknown level falls back without throwing and keeps the raw value visible", () => {
  const variant = levelVariant("catastrophe");
  assert.equal(variant.label, "catastrophe");
  assert.ok(variant.className.includes("text-level-info"));
});

test("an empty level falls back to a readable placeholder", () => {
  assert.equal(levelVariant("").label, "Unknown");
});

test("level matching is case-insensitive", () => {
  assert.equal(levelVariant("ERROR").label, "Error");
  assert.equal(levelVariant("Warning").label, "Warning");
});

test("every known level resolves to a distinct colour class", () => {
  const classes = cases.map(([level]) => levelVariant(level).className);
  assert.equal(new Set(classes).size, classes.length, "severities must be visually distinguishable");
});
