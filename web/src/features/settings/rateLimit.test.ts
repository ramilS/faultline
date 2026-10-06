import assert from "node:assert/strict";
import { test } from "node:test";

import {
  MAX_RATE_LIMIT_BURST,
  MAX_RATE_LIMIT_RPS,
  parseRateLimit,
  type RateLimitResult
} from "./rateLimit.ts";

function errorsOf(result: RateLimitResult): { rps?: string; burst?: string } {
  assert.equal(result.ok, false, "expected the inputs to be rejected");
  return result.ok ? {} : result.errors;
}

test("accepts in-range integers and maps them to the create-key payload", () => {
  const result = parseRateLimit("200", "1000");
  assert.deepEqual(result, { ok: true, value: { rate_limit_rps: 200, rate_limit_burst: 1000 } });
});

test("tolerates surrounding whitespace", () => {
  const result = parseRateLimit(" 5 ", " 10 ");
  assert.deepEqual(result, { ok: true, value: { rate_limit_rps: 5, rate_limit_burst: 10 } });
});

test("accepts the exact upper bounds the server allows", () => {
  const result = parseRateLimit(String(MAX_RATE_LIMIT_RPS), String(MAX_RATE_LIMIT_BURST));
  assert.equal(result.ok, true);
});

const nonIntegerCases: ReadonlyArray<[string, string]> = [
  ["an empty", ""],
  ["a fractional", "12.5"],
  ["a negative", "-1"],
  ["a non-numeric", "fast"]
];

for (const [name, input] of nonIntegerCases) {
  test(`rejects ${name} sustained rate`, () => {
    assert.equal(errorsOf(parseRateLimit(input, "1000")).rps, "Enter a whole number.");
  });
}

test("rejects zero, which the server treats as invalid rather than unlimited", () => {
  assert.equal(errorsOf(parseRateLimit("0", "1000")).rps, "Enter a number between 1 and 10,000.");
});

test("rejects a rate above the server maximum", () => {
  const errors = errorsOf(parseRateLimit(String(MAX_RATE_LIMIT_RPS + 1), "1000"));
  assert.equal(errors.rps, "Enter a number between 1 and 10,000.");
});

test("rejects a burst above the server maximum", () => {
  const errors = errorsOf(parseRateLimit("200", String(MAX_RATE_LIMIT_BURST + 1)));
  assert.equal(errors.burst, "Enter a number between 1 and 100,000.");
});

test("reports both fields at once so the form does not fail twice", () => {
  const errors = errorsOf(parseRateLimit("", ""));
  assert.equal(errors.rps, "Enter a whole number.");
  assert.equal(errors.burst, "Enter a whole number.");
});

test("omits the error key for the field that is valid", () => {
  const errors = errorsOf(parseRateLimit("200", "nope"));
  assert.equal(errors.rps, undefined);
  assert.equal(errors.burst, "Enter a whole number.");
});
