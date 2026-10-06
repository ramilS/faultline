import assert from "node:assert/strict";
import { test } from "node:test";

import { MAX_SCRUB_FIELDS, MAX_SCRUB_FIELD_LENGTH, formatFieldList, parseFieldList } from "./scrubFields.ts";

test("parseFieldList accepts one name per line or comma-separated, trimming blanks", () => {
  assert.deepEqual(parseFieldList(" ssn \n\npin, iban "), { ok: true, fields: ["ssn", "pin", "iban"] });
});

test("parseFieldList drops case-insensitive duplicates, keeping the first spelling", () => {
  assert.deepEqual(parseFieldList("SSN\nssn"), { ok: true, fields: ["SSN"] });
});

test("parseFieldList rejects a name over the server limit", () => {
  assert.equal(parseFieldList("x".repeat(MAX_SCRUB_FIELD_LENGTH + 1)).ok, false);
});

test("parseFieldList rejects more entries than the server accepts", () => {
  const text = Array.from({ length: MAX_SCRUB_FIELDS + 1 }, (_, index) => `f${index}`).join("\n");
  assert.equal(parseFieldList(text).ok, false);
});

test("parseFieldList treats empty text as an empty list", () => {
  assert.deepEqual(parseFieldList(""), { ok: true, fields: [] });
});

test("parseFieldList measures names in UTF-8 bytes, as the server does", () => {
  const thirtyThreeCyrillic = "ж".repeat(33);
  assert.equal(thirtyThreeCyrillic.length, 33);
  assert.equal(parseFieldList(thirtyThreeCyrillic).ok, false);
  assert.equal(parseFieldList("ж".repeat(32)).ok, true);
});

test("formatFieldList round-trips through parseFieldList", () => {
  assert.deepEqual(parseFieldList(formatFieldList(["ssn", "pin"])), { ok: true, fields: ["ssn", "pin"] });
});
