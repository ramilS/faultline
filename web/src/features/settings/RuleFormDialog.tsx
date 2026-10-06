import type { GroupingRule } from "@/api";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle
} from "@/components/ui/dialog";
import { RuleForm } from "@/features/settings/RuleForm";

interface RuleFormDialogProps {
  slug: string;
  open: boolean;
  /** null creates a rule; a rule edits it. */
  rule: GroupingRule | null;
  onClose: () => void;
  onSaved: () => Promise<void>;
}

export function RuleFormDialog({ slug, open, rule, onClose, onSaved }: RuleFormDialogProps) {
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
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{rule === null ? "New grouping rule" : "Edit grouping rule"}</DialogTitle>
            <DialogDescription>
              A rule overrides automatic grouping for the events it matches, forcing them into one issue.
            </DialogDescription>
          </DialogHeader>

          {/* Remounting per target resets the form from the rule being edited. */}
          <RuleForm key={rule?.id ?? "new"} slug={slug} rule={rule} onSaved={onSaved} onCancel={onClose} />
        </DialogContent>
      )}
    </Dialog>
  );
}
