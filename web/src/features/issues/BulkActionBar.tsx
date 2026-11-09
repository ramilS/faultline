import { CircleCheck, X } from "lucide-react";

import type { IgnoreCondition } from "@/api";
import { IgnoreMenu } from "@/components/IgnoreMenu";
import { Button } from "@/components/ui/button";

export interface BulkActionBarProps {
  selectedCount: number;
  busy: boolean;
  error: string | null;
  onResolve: () => void;
  onIgnore: (condition: IgnoreCondition | null) => void;
  onClear: () => void;
}

export function BulkActionBar({ selectedCount, busy, error, onResolve, onIgnore, onClear }: BulkActionBarProps) {
  return (
    <>
      {/* Always mounted. A live region inserted at the same moment as its text
          is frequently not announced, so the count lives here permanently and
          the visible bar below is ordinary content. */}
      <p aria-live="polite" className="sr-only">
        {selectedCount === 0
          ? "No issues selected"
          : `${selectedCount} ${selectedCount === 1 ? "issue" : "issues"} selected`}
      </p>

      {selectedCount > 0 && (
        <div className="bg-card sticky bottom-0 z-10 flex flex-wrap items-center gap-2 border-t px-4 py-3 sm:px-6">
          <span className="text-sm font-medium">
            {selectedCount} selected
          </span>

          <Button variant="outline" size="sm" onClick={onResolve} disabled={busy}>
            <CircleCheck className="size-3.5" aria-hidden="true" />
            Resolve
          </Button>
          <IgnoreMenu disabled={busy} onIgnore={onIgnore} />
          <Button variant="ghost" size="icon-sm" onClick={onClear} disabled={busy} aria-label="Clear selection">
            <X className="size-4" aria-hidden="true" />
          </Button>

          {error !== null && (
            <p role="alert" className="text-level-error ml-auto text-sm">
              {error}
            </p>
          )}
        </div>
      )}
    </>
  );
}
