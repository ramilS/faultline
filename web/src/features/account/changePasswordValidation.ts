import { passwordError } from "../users/userFormValidation.ts";

export interface ChangePasswordFieldErrors {
  readonly currentPassword?: string;
  readonly newPassword?: string;
  readonly confirmPassword?: string;
}

export type ChangePasswordResult =
  | { readonly ok: true; readonly currentPassword: string; readonly newPassword: string }
  | { readonly ok: false; readonly errors: ChangePasswordFieldErrors };

/**
 * Client-side validation for the account screen's password form. The current
 * password is only checked for presence here — whether it is actually
 * correct is a question only the server can answer (auth.Administration.
 * ChangeOwnPassword verifies it against the stored hash). The new password
 * reuses userFormValidation's rune-counting rule rather than a second copy
 * of the 12-code-point minimum.
 */
export function parseChangePasswordInput(
  currentPassword: string,
  newPassword: string,
  confirmPassword: string
): ChangePasswordResult {
  const currentPasswordError = currentPassword === "" ? "Enter your current password." : null;
  const newPasswordError = passwordError(newPassword);
  // Skipped when the new password itself is already invalid, so a single
  // mistake does not surface two errors that both point at the same fix.
  const confirmPasswordError =
    newPasswordError === null && confirmPassword !== newPassword ? "Passwords do not match." : null;

  if (currentPasswordError !== null || newPasswordError !== null || confirmPasswordError !== null) {
    return {
      ok: false,
      errors: {
        ...(currentPasswordError === null ? {} : { currentPassword: currentPasswordError }),
        ...(newPasswordError === null ? {} : { newPassword: newPasswordError }),
        ...(confirmPasswordError === null ? {} : { confirmPassword: confirmPasswordError })
      }
    };
  }

  return { ok: true, currentPassword, newPassword };
}
