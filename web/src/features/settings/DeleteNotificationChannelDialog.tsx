import { useState } from "react";

import type { NotificationChannel } from "@/api";
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

interface DeleteNotificationChannelDialogProps {
  slug: string;
  /** The channel awaiting confirmation, or null when the dialog is closed. */
  target: NotificationChannel | null;
  onClose: () => void;
  onDeleted: () => Promise<void>;
}

export function DeleteNotificationChannelDialog({ slug, target, onClose, onDeleted }: DeleteNotificationChannelDialogProps) {
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const close = () => {
    setError(null);
    onClose();
  };

  const remove = async (channel: NotificationChannel) => {
    setError(null);
    setSubmitting(true);
    try {
      await api.deleteNotificationChannel(slug, channel.id);
      await onDeleted();
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
            <DialogTitle>Delete this notification channel?</DialogTitle>
            <DialogDescription>
              <span className="font-medium">{target.name}</span> stops receiving notifications, including any still
              waiting to be retried. To pause it instead, edit it and turn it off.
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
            <Button type="button" variant="destructive" disabled={submitting} onClick={() => void remove(target)}>
              {submitting ? "Deleting…" : "Delete channel"}
            </Button>
          </DialogFooter>
        </DialogContent>
      )}
    </Dialog>
  );
}
