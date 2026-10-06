import assert from "node:assert/strict";
import { test } from "node:test";

import { parseCreateUserInput, passwordError, PASSWORD_MINIMUM_CODE_POINTS, type CreateUserResult } from "./userFormValidation.ts";

function errorsOf(result: CreateUserResult): { name?: string; email?: string; password?: string } {
  assert.equal(result.ok, false, "expected the input to be rejected");
  return result.ok ? {} : result.errors;
}

test("accepts valid input and trims name and email", () => {
  const result = parseCreateUserInput(" Ada Lovelace ", " ada@example.com ", "a very long password", "member");
  assert.deepEqual(result, {
    ok: true,
    value: { name: "Ada Lovelace", email: "ada@example.com", password: "a very long password", role: "member" }
  });
});

test("rejects an empty name", () => {
  assert.equal(errorsOf(parseCreateUserInput("", "ada@example.com", "a very long password", "member")).name, "Enter a name.");
});

test("rejects a name that is only whitespace", () => {
  assert.equal(errorsOf(parseCreateUserInput("   ", "ada@example.com", "a very long password", "member")).name, "Enter a name.");
});

test("rejects an empty email", () => {
  assert.equal(errorsOf(parseCreateUserInput("Ada", "", "a very long password", "member")).email, "Enter an email address.");
});

const invalidEmails = ["not-an-email", "missing-domain@", "@missing-local.com", "spaces in@example.com"];

for (const email of invalidEmails) {
  test(`rejects an invalid email shape: ${email}`, () => {
    assert.equal(
      errorsOf(parseCreateUserInput("Ada", email, "a very long password", "member")).email,
      "Enter a valid email address."
    );
  });
}

test("rejects a password shorter than the server minimum", () => {
  const errors = errorsOf(parseCreateUserInput("Ada", "ada@example.com", "short", "member"));
  assert.equal(errors.password, `Enter at least ${PASSWORD_MINIMUM_CODE_POINTS} characters.`);
});

test("accepts a password at exactly the minimum length", () => {
  const result = parseCreateUserInput("Ada", "ada@example.com", "x".repeat(PASSWORD_MINIMUM_CODE_POINTS), "member");
  assert.equal(result.ok, true);
});

test("rejects a password one character below the minimum — pins the boundary, not just the middle of the range", () => {
  // Guards against an off-by-one weakening (`< 12` becoming `< 11`) that a
  // mid-range case like "short" would never catch.
  assert.equal(
    passwordError("x".repeat(PASSWORD_MINIMUM_CODE_POINTS - 1)),
    `Enter at least ${PASSWORD_MINIMUM_CODE_POINTS} characters.`
  );
});

test("accepts a password one character above the minimum", () => {
  assert.equal(passwordError("x".repeat(PASSWORD_MINIMUM_CODE_POINTS + 1)), null);
});

test("counts Unicode code points, not UTF-16 units, so astral-plane characters are not undercounted", () => {
  // Each of these emoji is a single code point outside the BMP (a UTF-16
  // surrogate pair), so .length would read 24 here though there are only 12
  // code points — exactly the server's minimum. This is the same rule the
  // server enforces: internal/auth/password.go counts runes, and a Go rune
  // is a Unicode code point, so a client counting UTF-16 units (or bytes)
  // would disagree with the server right at this boundary.
  const password = "\u{1F600}".repeat(PASSWORD_MINIMUM_CODE_POINTS);
  assert.equal(password.length, PASSWORD_MINIMUM_CODE_POINTS * 2);
  assert.equal(passwordError(password), null);
});

test("rejects one astral-plane character short of the minimum — the UTF-16 boundary a naive .length would miss", () => {
  // 11 code points, but 22 UTF-16 units — a `.length < 12` implementation
  // would wrongly accept this. It must still be rejected.
  const password = "\u{1F600}".repeat(PASSWORD_MINIMUM_CODE_POINTS - 1);
  assert.equal(password.length, (PASSWORD_MINIMUM_CODE_POINTS - 1) * 2);
  assert.equal(passwordError(password), `Enter at least ${PASSWORD_MINIMUM_CODE_POINTS} characters.`);
});

test("rejects one multi-byte (but single-UTF-16-unit) character short of the minimum — the byte-count boundary", () => {
  // "é" here is one Unicode code point that is 2 bytes in UTF-8 and 1
  // UTF-16 unit. A client counting UTF-8 bytes would read this as 22 and
  // wrongly accept it; the server counts runes (code points), so this must
  // be rejected at 11.
  const password = "é".repeat(PASSWORD_MINIMUM_CODE_POINTS - 1);
  assert.equal(passwordError(password), `Enter at least ${PASSWORD_MINIMUM_CODE_POINTS} characters.`);
});

test("accepts exactly the minimum in multi-byte (non-surrogate-pair) characters", () => {
  const password = "é".repeat(PASSWORD_MINIMUM_CODE_POINTS);
  assert.equal(passwordError(password), null);
});

test("reports every failing field at once so the form does not fail twice", () => {
  const errors = errorsOf(parseCreateUserInput("", "", "short", "member"));
  assert.equal(errors.name, "Enter a name.");
  assert.equal(errors.email, "Enter an email address.");
  assert.equal(errors.password, `Enter at least ${PASSWORD_MINIMUM_CODE_POINTS} characters.`);
});

test("omits the error key for a field that is valid", () => {
  const errors = errorsOf(parseCreateUserInput("", "ada@example.com", "a very long password", "member"));
  assert.equal(errors.email, undefined);
  assert.equal("email" in errors, false);
});

test("preserves the requested role in a valid result", () => {
  const result = parseCreateUserInput("Ada", "ada@example.com", "a very long password", "owner");
  assert.equal(result.ok, true);
  assert.equal(result.ok && result.value.role, "owner");
});
