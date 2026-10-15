import type { NotificationChannel } from "@/api";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { NotificationChannelForm } from "@/features/settings/NotificationChannelForm";

interface NotificationChannelDialogProps {
  slug: string;
  open: boolean;
  /** null creates a channel; a channel edits it. */
  channel: NotificationChannel | null;
  onClose: () => void;
  onSaved: () => Promise<void>;
}

export function NotificationChannelDialog({ slug, open, channel, onClose, onSaved }: NotificationChannelDialogProps) {
  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) {
          onClose();
        }
      }}
    >
      {open && (
        <DialogContent className="sm:max-w-xl">
          <DialogHeader>
            <DialogTitle>{channel === null ? "New notification channel" : "Edit notification channel"}</DialogTitle>
            <DialogDescription>
              Faultline posts here when an issue first appears or a resolved one comes back.
            </DialogDescription>
          </DialogHeader>

          {/* Remounting per target resets the form from the channel being edited. */}
          <NotificationChannelForm key={channel?.id ?? "new"} slug={slug} channel={channel} onSaved={onSaved} onCancel={onClose} />
        </DialogContent>
      )}
    </Dialog>
  );
}
