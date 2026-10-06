import { useState } from "react";

import type { GroupingRule } from "@/api";
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

interface DeleteRuleDialogProps {
  slug: string;
  /** The rule awaiting confirmation, or null when the dialog is closed. */
  target: GroupingRule | null;
  onClose: () => void;
  onDeleted: () => Promise<void>;
}

export function DeleteRuleDialog({ slug, target, onClose, onDeleted }: DeleteRuleDialogProps) {
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const close = () => {
    setError(null);
    onClose();
  };

  const remove = async (rule: GroupingRule) => {
    setError(null);
    setSubmitting(true);
    try {
      await api.deleteGroupingRule(slug, rule.id);
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
            <DialogTitle>Delete this grouping rule?</DialogTitle>
            <DialogDescription>
              <span className="font-mono break-all">{target.matcher}</span> will stop forcing events into{" "}
              <span className="font-mono break-all">{target.template}</span>. Future matching events fall back to
              automatic grouping and will land in different issues; already-grouped events are not regrouped.
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
              {submitting ? "Deleting…" : "Delete rule"}
            </Button>
          </DialogFooter>
        </DialogContent>
      )}
    </Dialog>
  );
}
