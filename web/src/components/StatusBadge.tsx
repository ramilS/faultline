import { BellOff, CircleCheck, CircleDot } from "lucide-react";

import type { IssueStatus } from "@/api";
import { cn } from "@/lib/utils";

const VARIANTS: Record<IssueStatus, { label: string; className: string; Icon: typeof CircleDot }> = {
  unresolved: { label: "Unresolved", className: "text-status-unresolved", Icon: CircleDot },
  resolved: { label: "Resolved", className: "text-status-resolved bg-status-resolved-surface", Icon: CircleCheck },
  ignored: { label: "Ignored", className: "text-status-ignored", Icon: BellOff }
};

export function StatusBadge({ status, className }: { status: IssueStatus; className?: string }) {
  const variant = VARIANTS[status];

  return (
    <span
      className={cn(
        "inline-flex items-center gap-1 rounded-md px-1.5 py-0.5 text-xs font-medium",
        variant.className,
        className
      )}
    >
      <variant.Icon className="size-3 shrink-0" aria-hidden="true" />
      {variant.label}
    </span>
  );
}
