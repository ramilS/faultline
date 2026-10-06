import { Eye, EyeOff } from "lucide-react";
import { useState, type FormEvent } from "react";
import { toast } from "sonner";

import { ApiError } from "@/api";
import { api } from "@/apiClient";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { type ChangePasswordFieldErrors, parseChangePasswordInput } from "@/features/account/changePasswordValidation";
import { messageFor } from "@/errors";

const NO_FIELD_ERRORS: ChangePasswordFieldErrors = {};

interface PasswordFieldProps {
  id: string;
  label: string;
  value: string;
  onChange: (value: string) => void;
  error?: string;
  helpText?: string;
  autoComplete: "current-password" | "new-password";
  autoFocus?: boolean;
}

/**
 * One labeled password input with its own show/hide toggle, matching
 * SetPasswordDialog and CreateUserDialog. Local to this file: nothing else
 * needs a three-field password form.
 */
function PasswordField({ id, label, value, onChange, error, helpText, autoComplete, autoFocus }: PasswordFieldProps) {
  const [visible, setVisible] = useState(false);
  const helpId = `${id}-help`;
  const errorId = `${id}-error`;

  return (
    <div className="space-y-2">
      <Label htmlFor={id}>{label}</Label>
      <div className="relative">
        <Input
          id={id}
          type={visible ? "text" : "password"}
          autoComplete={autoComplete}
          value={value}
          onChange={(event) => onChange(event.target.value)}
          className="pr-10"
          aria-invalid={error !== undefined}
          aria-describedby={error === undefined ? (helpText === undefined ? undefined : helpId) : errorId}
          autoFocus={autoFocus}
        />
        <button
          type="button"
          onClick={() => setVisible((current) => !current)}
          aria-label={visible ? `Hide ${label.toLowerCase()}` : `Show ${label.toLowerCase()}`}
          aria-pressed={visible}
          className="text-muted-foreground hover:text-foreground absolute inset-y-0 right-0 flex w-10 items-center justify-center rounded-md"
        >
          {visible ? <EyeOff className="size-4" aria-hidden="true" /> : <Eye className="size-4" aria-hidden="true" />}
        </button>
      </div>
      {error !== undefined ? (
        <p id={errorId} role="alert" className="text-destructive text-xs">
          {error}
        </p>
      ) : (
        helpText !== undefined && (
          <p id={helpId} className="text-muted-foreground text-xs">
            {helpText}
          </p>
        )
      )}
    </div>
  );
}

/**
 * Self-service password change for the signed-in user, backed by
 * POST /api/v1/auth/password (internal/api/users.go's ChangeOwnPassword).
 *
 * Unlike an owner's SetPasswordDialog reset, this verifies the current
 * password server-side before writing — see
 * auth.Administration.ChangeOwnPassword's doc comment for why that check
 * exists. A wrong current password comes back as 403 and is shown under that
 * field rather than as a form-level error, since it is the one server error
 * this form can attribute to a specific field.
 *
 * A successful change revokes every other session the account has open and
 * keeps this one (see auth.Administration.ChangeOwnPassword). AccountPage
 * says so directly, next to this form: a user acting because they suspect
 * another session is compromised needs to know the change actually ends it.
 */
export function ChangePasswordForm() {
  const [currentPassword, setCurrentPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [fieldErrors, setFieldErrors] = useState<ChangePasswordFieldErrors>(NO_FIELD_ERRORS);
  const [submitError, setSubmitError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    const parsed = parseChangePasswordInput(currentPassword, newPassword, confirmPassword);
    if (!parsed.ok) {
      setFieldErrors(parsed.errors);
      setSubmitError(null);
      return;
    }

    setFieldErrors(NO_FIELD_ERRORS);
    setSubmitError(null);
    setSubmitting(true);
    try {
      await api.changeOwnPassword(parsed.currentPassword, parsed.newPassword);
      toast.success("Password updated.");
      setCurrentPassword("");
      setNewPassword("");
      setConfirmPassword("");
    } catch (caught) {
      if (caught instanceof ApiError && caught.status === 403) {
        setFieldErrors({ currentPassword: "Current password is incorrect." });
      } else {
        setSubmitError(messageFor(caught));
      }
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <form onSubmit={(event) => void submit(event)} aria-label="Change password" className="max-w-sm space-y-4">
      {submitError !== null && (
        <p role="alert" className="text-destructive text-sm">
          {submitError}
        </p>
      )}

      <PasswordField
        id="account-current-password"
        label="Current password"
        value={currentPassword}
        onChange={setCurrentPassword}
        error={fieldErrors.currentPassword}
        autoComplete="current-password"
        autoFocus
      />

      <PasswordField
        id="account-new-password"
        label="New password"
        value={newPassword}
        onChange={setNewPassword}
        error={fieldErrors.newPassword}
        helpText="At least 12 characters."
        autoComplete="new-password"
      />

      <PasswordField
        id="account-confirm-password"
        label="Confirm new password"
        value={confirmPassword}
        onChange={setConfirmPassword}
        error={fieldErrors.confirmPassword}
        autoComplete="new-password"
      />

      <Button type="submit" disabled={submitting}>
        {submitting ? "Saving…" : "Update password"}
      </Button>
    </form>
  );
}
