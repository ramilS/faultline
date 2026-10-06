import { Eye, EyeOff, Plus } from "lucide-react";
import { useState, type FormEvent } from "react";

import type { UserRole } from "@/api";
import { api } from "@/apiClient";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { messageFor } from "@/errors";
import { parseCreateUserInput, type CreateUserFieldErrors } from "@/features/users/userFormValidation";

const NO_FIELD_ERRORS: CreateUserFieldErrors = {};

interface CreateUserDialogProps {
  onCreated: () => void;
}

export function CreateUserDialog({ onCreated }: CreateUserDialogProps) {
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [passwordVisible, setPasswordVisible] = useState(false);
  const [role, setRole] = useState<UserRole>("member");
  const [fieldErrors, setFieldErrors] = useState<CreateUserFieldErrors>(NO_FIELD_ERRORS);
  const [submitError, setSubmitError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const close = () => {
    setOpen(false);
    setName("");
    setEmail("");
    setPassword("");
    setPasswordVisible(false);
    setRole("member");
    setFieldErrors(NO_FIELD_ERRORS);
    setSubmitError(null);
  };

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    const parsed = parseCreateUserInput(name, email, password, role);
    if (!parsed.ok) {
      setFieldErrors(parsed.errors);
      setSubmitError(null);
      return;
    }

    setFieldErrors(NO_FIELD_ERRORS);
    setSubmitError(null);
    setSubmitting(true);
    try {
      await api.createUser(parsed.value);
      onCreated();
      close();
    } catch (caught) {
      // The server names no offending field on a validation failure — it
      // returns one generic "invalid user" message regardless of which
      // field caused it — so this is always a form-level error, never
      // mapped onto a specific input.
      setSubmitError(messageFor(caught));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={(next) => (next ? setOpen(true) : close())}>
      <DialogTrigger asChild>
        <Button size="sm">
          <Plus aria-hidden="true" />
          New user
        </Button>
      </DialogTrigger>
      <DialogContent>
        <form onSubmit={submit}>
          <DialogHeader>
            <DialogTitle>New user</DialogTitle>
            <DialogDescription>
              They can sign in immediately with the password you set here. Share it with them through a channel
              you trust — this dialog will not show it again.
            </DialogDescription>
          </DialogHeader>

          <div className="space-y-4 py-4">
            {submitError !== null && (
              <p role="alert" className="text-destructive text-sm">
                {submitError}
              </p>
            )}

            <div className="space-y-2">
              <Label htmlFor="new-user-name">Name</Label>
              <Input
                id="new-user-name"
                value={name}
                onChange={(event) => setName(event.target.value)}
                aria-invalid={fieldErrors.name !== undefined}
                aria-describedby={fieldErrors.name === undefined ? undefined : "new-user-name-error"}
                autoFocus
              />
              {fieldErrors.name !== undefined && (
                <p id="new-user-name-error" role="alert" className="text-destructive text-xs">
                  {fieldErrors.name}
                </p>
              )}
            </div>

            <div className="space-y-2">
              <Label htmlFor="new-user-email">Email</Label>
              <Input
                id="new-user-email"
                type="email"
                autoComplete="off"
                value={email}
                onChange={(event) => setEmail(event.target.value)}
                aria-invalid={fieldErrors.email !== undefined}
                aria-describedby={fieldErrors.email === undefined ? undefined : "new-user-email-error"}
              />
              {fieldErrors.email !== undefined && (
                <p id="new-user-email-error" role="alert" className="text-destructive text-xs">
                  {fieldErrors.email}
                </p>
              )}
            </div>

            <div className="space-y-2">
              <Label htmlFor="new-user-password">Password</Label>
              <div className="relative">
                <Input
                  id="new-user-password"
                  type={passwordVisible ? "text" : "password"}
                  autoComplete="new-password"
                  value={password}
                  onChange={(event) => setPassword(event.target.value)}
                  className="pr-10"
                  aria-invalid={fieldErrors.password !== undefined}
                  aria-describedby={fieldErrors.password === undefined ? "new-user-password-help" : "new-user-password-error"}
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
              {fieldErrors.password !== undefined ? (
                <p id="new-user-password-error" role="alert" className="text-destructive text-xs">
                  {fieldErrors.password}
                </p>
              ) : (
                <p id="new-user-password-help" className="text-muted-foreground text-xs">
                  At least 12 characters.
                </p>
              )}
            </div>

            <div className="space-y-2">
              <Label htmlFor="new-user-role">Role</Label>
              <Select value={role} onValueChange={(value) => setRole(value as UserRole)}>
                <SelectTrigger id="new-user-role" className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="member">Member</SelectItem>
                  <SelectItem value="owner">Owner</SelectItem>
                </SelectContent>
              </Select>
            </div>
          </div>

          <DialogFooter>
            <Button type="button" variant="outline" onClick={close}>
              Cancel
            </Button>
            <Button type="submit" disabled={submitting}>
              {submitting ? "Creating…" : "Create user"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
