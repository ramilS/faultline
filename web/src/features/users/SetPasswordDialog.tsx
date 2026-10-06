import { Eye, EyeOff } from "lucide-react";
import { useState, type FormEvent } from "react";
import { toast } from "sonner";

import type { ManagedUser } from "@/api";
import { api } from "@/apiClient";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { messageFor } from "@/errors";
import { passwordError } from "@/features/users/userFormValidation";

interface SetPasswordDialogProps {
  /** The user awaiting a new password, or null when the dialog is closed. */
  target: ManagedUser | null;
  onClose: () => void;
}

export function SetPasswordDialog({ target, onClose }: SetPasswordDialogProps) {
  const [password, setPassword] = useState("");
  const [passwordVisible, setPasswordVisible] = useState(false);
  const [fieldError, setFieldError] = useState<string | null>(null);
  const [submitError, setSubmitError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const close = () => {
    setPassword("");
    setPasswordVisible(false);
    setFieldError(null);
    setSubmitError(null);
    onClose();
  };

  const submit = async (event: FormEvent, user: ManagedUser) => {
    event.preventDefault();
    const error = passwordError(password);
    if (error !== null) {
      setFieldError(error);
      setSubmitError(null);
      return;
    }

    setFieldError(null);
    setSubmitError(null);
    setSubmitting(true);
    try {
      await api.setUserPassword(user.id, password);
      toast.success(`Password updated for ${user.email}.`);
      close();
    } catch (caught) {
      setSubmitError(messageFor(caught));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Dialog
      open={target !== null}
      onOpenChange={(next) => {
        if (!next) {
          close();
        }
      }}
    >
      {target !== null && (
        <DialogContent>
          <form onSubmit={(event) => void submit(event, target)}>
            <DialogHeader>
              <DialogTitle>Set a new password for {target.name}</DialogTitle>
              <DialogDescription>
                <span className="font-mono break-all">{target.email}</span> will need this password the next
                time they sign in, and every session they currently have open is signed out.
              </DialogDescription>
            </DialogHeader>

            <div className="space-y-4 py-4">
              {submitError !== null && (
                <p role="alert" className="text-destructive text-sm">
                  {submitError}
                </p>
              )}

              <div className="space-y-2">
                <Label htmlFor="set-user-password">New password</Label>
                <div className="relative">
                  <Input
                    id="set-user-password"
                    type={passwordVisible ? "text" : "password"}
                    autoComplete="new-password"
                    value={password}
                    onChange={(event) => setPassword(event.target.value)}
                    className="pr-10"
                    aria-invalid={fieldError !== null}
                    aria-describedby={fieldError === null ? "set-user-password-help" : "set-user-password-error"}
                    autoFocus
                  />
                  <button
                    type="button"
                    onClick={() => setPasswordVisible((visible) => !visible)}
                    aria-label={passwordVisible ? "Hide password" : "Show password"}
                    aria-pressed={passwordVisible}
                    className="text-muted-foreground hover:text-foreground absolute inset-y-0 right-0 flex w-10 items-center justify-center rounded-md"
                  >
                    {passwordVisible ? (
                      <EyeOff className="size-4" aria-hidden="true" />
                    ) : (
                      <Eye className="size-4" aria-hidden="true" />
                    )}
                  </button>
                </div>
                {fieldError !== null ? (
                  <p id="set-user-password-error" role="alert" className="text-destructive text-xs">
                    {fieldError}
                  </p>
                ) : (
                  <p id="set-user-password-help" className="text-muted-foreground text-xs">
                    At least 12 characters.
                  </p>
                )}
              </div>
            </div>

            <DialogFooter>
              <Button type="button" variant="outline" onClick={close}>
                Cancel
              </Button>
              <Button type="submit" disabled={submitting}>
                {submitting ? "Saving…" : "Set password"}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      )}
    </Dialog>
  );
}
