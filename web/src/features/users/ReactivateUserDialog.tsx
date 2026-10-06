import { useState } from "react";

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
import { messageFor } from "@/errors";

interface ReactivateUserDialogProps {
  /** The user awaiting confirmation, or null when the dialog is closed. */
  target: ManagedUser | null;
  onClose: () => void;
  onReactivated: () => void;
}

export function ReactivateUserDialog({ target, onClose, onReactivated }: ReactivateUserDialogProps) {
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const close = () => {
    setError(null);
    onClose();
  };

  const reactivate = async (user: ManagedUser) => {
    setError(null);
    setSubmitting(true);
    try {
      await api.patchUser(user.id, { deactivated: false });
      onReactivated();
      close();
    } catch (caught) {
      setError(messageFor(caught));
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
          <DialogHeader>
            <DialogTitle>Reactivate {target.name}?</DialogTitle>
            <DialogDescription>
              <span className="font-mono break-all">{target.email}</span> will be able to sign in again. This does
              not restore their previous sessions — they will need to sign in fresh.
            </DialogDescription>
          </DialogHeader>

          {error !== null && (
            <p role="alert" className="text-destructive text-sm">
              {error}
            </p>
          )}

          <DialogFooter>
            <Button type="button" variant="outline" onClick={close}>
              Cancel
            </Button>
            <Button type="button" disabled={submitting} onClick={() => void reactivate(target)}>
              {submitting ? "Reactivating…" : "Reactivate"}
            </Button>
          </DialogFooter>
        </DialogContent>
      )}
    </Dialog>
  );
}
