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

interface DeactivateUserDialogProps {
  /** The user awaiting confirmation, or null when the dialog is closed. */
  target: ManagedUser | null;
  onClose: () => void;
  onDeactivated: () => void;
}

export function DeactivateUserDialog({ target, onClose, onDeactivated }: DeactivateUserDialogProps) {
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const close = () => {
    setError(null);
    onClose();
  };

  const deactivate = async (user: ManagedUser) => {
    setError(null);
    setSubmitting(true);
    try {
      await api.patchUser(user.id, { deactivated: true });
      onDeactivated();
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
            <DialogTitle>Deactivate {target.name}?</DialogTitle>
            <DialogDescription>
              <span className="font-mono break-all">{target.email}</span> will be signed out of every active
              session immediately and will not be able to sign in again. Their account and its history are not
              deleted.
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
            <Button type="button" variant="destructive" disabled={submitting} onClick={() => void deactivate(target)}>
              {submitting ? "Deactivating…" : "Deactivate"}
            </Button>
          </DialogFooter>
        </DialogContent>
      )}
    </Dialog>
  );
}
