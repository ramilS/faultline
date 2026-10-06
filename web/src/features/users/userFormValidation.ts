import type { CreateUserInput, UserRole } from "@/api";

// Mirrors the server's password rule (internal/auth/password.go): 12 Unicode
// code points, not UTF-16 code units, so a password containing characters
// outside the BMP is not undercounted client-side.
export const PASSWORD_MINIMUM_CODE_POINTS = 12;

const EMAIL_PATTERN = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

export interface CreateUserFieldErrors {
  readonly name?: string;
  readonly email?: string;
  readonly password?: string;
}

export type CreateUserResult =
  | { readonly ok: true; readonly value: CreateUserInput }
  | { readonly ok: false; readonly errors: CreateUserFieldErrors };

/**
 * The server names no offending field on a validation failure — it returns
 * a single generic "invalid user" message (`internal/api/users.go`). This
 * catches the common cases client-side so most users never see that
 * message; the server's own text is still shown verbatim for whatever slips
 * through (see CreateUserDialog).
 */
export function parseCreateUserInput(name: string, email: string, password: string, role: UserRole): CreateUserResult {
  const trimmedName = name.trim();
  const trimmedEmail = email.trim();

  const nameError = trimmedName === "" ? "Enter a name." : null;
  const emailError = emailFieldError(trimmedEmail);
  const passwordFieldError = passwordError(password);

  if (nameError !== null || emailError !== null || passwordFieldError !== null) {
    return {
      ok: false,
      errors: {
        ...(nameError === null ? {} : { name: nameError }),
        ...(emailError === null ? {} : { email: emailError }),
        ...(passwordFieldError === null ? {} : { password: passwordFieldError })
      }
    };
  }

  return { ok: true, value: { name: trimmedName, email: trimmedEmail, password, role } };
}

function emailFieldError(email: string): string | null {
  if (email === "") {
    return "Enter an email address.";
  }
  return EMAIL_PATTERN.test(email) ? null : "Enter a valid email address.";
}

/** Shared with SetPasswordDialog, which validates a password alone. */
export function passwordError(password: string): string | null {
  return codePointLength(password) < PASSWORD_MINIMUM_CODE_POINTS
    ? `Enter at least ${PASSWORD_MINIMUM_CODE_POINTS} characters.`
    : null;
}

function codePointLength(value: string): number {
  return [...value].length;
}
