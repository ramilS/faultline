import { Plus } from "lucide-react";
import { useState, type FormEvent, type ReactNode } from "react";

import type { CreatedArtifactToken } from "@/api";
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
import { messageFor } from "@/errors";

/** The server's limit on a token name. */
const MAX_NAME_LENGTH = 100;

interface CreateArtifactTokenDialogProps {
  slug: string;
  /** Receives the one-time raw token; the caller decides how to reveal it. */
  onCreated: (created: CreatedArtifactToken) => Promise<void>;
  trigger?: ReactNode;
}

export function CreateArtifactTokenDialog({ slug, onCreated, trigger }: CreateArtifactTokenDialogProps) {
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const close = () => {
    setOpen(false);
    setName("");
    setError(null);
  };

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (name.trim() === "") {
      setError("Give the token a name so you can tell it apart later.");
      return;
    }

    setError(null);
    setSubmitting(true);
    try {
      const created = await api.createArtifactToken(slug, name.trim());
      await onCreated(created);
      close();
    } catch (caught) {
      setError(messageFor(caught));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={(next) => (next ? setOpen(true) : close())}>
      <DialogTrigger asChild>
        {trigger ?? (
          <Button size="sm">
            <Plus aria-hidden="true" />
            New artifact token
          </Button>
        )}
      </DialogTrigger>
      <DialogContent>
        <form onSubmit={submit} noValidate>
          <DialogHeader>
            <DialogTitle>New artifact token</DialogTitle>
            <DialogDescription>
              Name it for what will use it, such as the CI job, so a token can be revoked without guessing whose it is.
            </DialogDescription>
          </DialogHeader>

          <div className="space-y-2 py-4">
            <Label htmlFor="artifact-token-name">Name</Label>
            <Input
              id="artifact-token-name"
              value={name}
              onChange={(event) => setName(event.target.value)}
              maxLength={MAX_NAME_LENGTH}
              placeholder="gradle"
              aria-invalid={error !== null}
              aria-describedby={error === null ? undefined : "artifact-token-name-error"}
              autoFocus
            />
            {error !== null && (
              <p id="artifact-token-name-error" role="alert" className="text-destructive text-sm">
                {error}
              </p>
            )}
          </div>

          <DialogFooter>
            <Button type="button" variant="outline" onClick={close}>
              Cancel
            </Button>
            <Button type="submit" disabled={submitting}>
              {submitting ? "Creating…" : "Create token"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
