import { useState } from "react";

import type { ArtifactToken } from "@/api";
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

import { tokenPrefixLabel } from "./artifactTokenLabel";

interface RevokeArtifactTokenDialogProps {
  slug: string;
  /** The token awaiting confirmation, or null when the dialog is closed. */
  target: ArtifactToken | null;
  onClose: () => void;
  onRevoked: (revokedTokenId: number) => Promise<void>;
}

export function RevokeArtifactTokenDialog({ slug, target, onClose, onRevoked }: RevokeArtifactTokenDialogProps) {
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const close = () => {
    setError(null);
    onClose();
  };

  const revoke = async (token: ArtifactToken) => {
    setError(null);
    setSubmitting(true);
    try {
      await api.revokeArtifactToken(slug, token.id);
      await onRevoked(token.id);
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
            <DialogTitle>Revoke this artifact token?</DialogTitle>
            <DialogDescription>
              Uploads using <span className="font-medium">{target.name}</span> (
              <span className="font-mono">{tokenPrefixLabel(target.token_prefix)}</span>) will be rejected from the moment you
              confirm. Revoking cannot be undone — uploads resume only with a new token set in whatever
              uses this one.
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
              {submitting ? "Revoking…" : "Revoke token"}
            </Button>
          </DialogFooter>
        </DialogContent>
      )}
    </Dialog>
  );
}
