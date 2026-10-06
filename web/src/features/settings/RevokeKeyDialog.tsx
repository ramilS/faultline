import { useState } from "react";

import type { Key } from "@/api";
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

interface RevokeKeyDialogProps {
  slug: string;
  /** The key awaiting confirmation, or null when the dialog is closed. */
  target: Key | null;
  onClose: () => void;
  onRevoked: (revokedKeyId: number) => Promise<void>;
}

export function RevokeKeyDialog({ slug, target, onClose, onRevoked }: RevokeKeyDialogProps) {
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const close = () => {
    setError(null);
    onClose();
  };

  const revoke = async (key: Key) => {
    setError(null);
    setSubmitting(true);
    try {
      await api.revokeKey(slug, key.id);
      await onRevoked(key.id);
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
            <DialogTitle>Revoke this ingest key?</DialogTitle>
            <DialogDescription>
              Every event sent with key <span className="font-mono break-all">{target.public_key}</span> will be rejected
              from the moment you confirm. Revoking cannot be undone — restoring ingestion means issuing a new
              key and redeploying anything that uses this one.
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
            <Button type="button" variant="destructive" disabled={submitting} onClick={() => void revoke(target)}>
              {submitting ? "Revoking…" : "Revoke key"}
            </Button>
          </DialogFooter>
        </DialogContent>
      )}
    </Dialog>
  );
}
