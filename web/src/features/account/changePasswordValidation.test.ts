import assert from "node:assert/strict";
import { test } from "node:test";

import { parseChangePasswordInput } from "./changePasswordValidation.ts";

test("accepts a matching pair of passwords that meet the length rule", () => {
  const result = parseChangePasswordInput("the-real-one-12", "a-new-password-12", "a-new-password-12");

  assert.deepEqual(result, { ok: true, currentPassword: "the-real-one-12", newPassword: "a-new-password-12" });
});

test("rejects an empty current password", () => {
  const result = parseChangePasswordInput("", "a-new-password-12", "a-new-password-12");

  assert.equal(result.ok, false);
  if (!result.ok) {
    assert.equal(result.errors.currentPassword, "Enter your current password.");
    assert.equal(result.errors.newPassword, undefined);
  }
});

test("rejects a new password shorter than 12 code points, reusing userFormValidation's rule", () => {
  const result = parseChangePasswordInput("the-real-one-12", "short", "short");

  assert.equal(result.ok, false);
  if (!result.ok) {
    assert.equal(result.errors.newPassword, "Enter at least 12 characters.");
  }
});

test("rejects a confirmation that does not match the new password", () => {
  const result = parseChangePasswordInput("the-real-one-12", "a-new-password-12", "a-different-one-12");

  assert.equal(result.ok, false);
  if (!result.ok) {
    assert.equal(result.errors.confirmPassword, "Passwords do not match.");
  }
});

test("does not also flag confirmation mismatch when the new password is already invalid", () => {
  const result = parseChangePasswordInput("the-real-one-12", "short", "different");

  assert.equal(result.ok, false);
  if (!result.ok) {
    assert.equal(result.errors.confirmPassword, undefined);
  }
});

test("counts runes, not UTF-16 code units, at the length boundary", () => {
  // U+1F600 is outside the BMP (a surrogate pair in UTF-16, one code point).
  // 11 of them is one code point short of the minimum.
  const justShort = "\u{1F600}".repeat(11);
  const exact = "\u{1F600}".repeat(12);

  const short = parseChangePasswordInput("the-real-one-12", justShort, justShort);
  assert.equal(short.ok, false);

  const exactResult = parseChangePasswordInput("the-real-one-12", exact, exact);
  assert.equal(exactResult.ok, true);
});
