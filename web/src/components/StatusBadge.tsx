import { BellOff, CircleCheck, CircleDot } from "lucide-react";

import type { IgnoreUntil, IssueStatus } from "@/api";
import { ignoreUntilLabel, type IgnoreLabelStyle } from "@/lib/ignoreUntil";
import { cn } from "@/lib/utils";

const VARIANTS: Record<IssueStatus, { label: string; className: string; Icon: typeof CircleDot }> = {
  unresolved: { label: "Unresolved", className: "text-status-unresolved", Icon: CircleDot },
  resolved: { label: "Resolved", className: "text-status-resolved bg-status-resolved-surface", Icon: CircleCheck },
  ignored: { label: "Ignored", className: "text-status-ignored", Icon: BellOff }
};

interface StatusBadgeProps {
  status: IssueStatus;
  ignoreUntil?: IgnoreUntil | null;
  ignoreStyle?: IgnoreLabelStyle;
  className?: string;
}

export function StatusBadge({ status, ignoreUntil = null, ignoreStyle = "compact", className }: StatusBadgeProps) {
  const variant = VARIANTS[status];
  const condition = status === "ignored" ? ignoreUntil : null;

  return (
    <span
      title={condition === null ? undefined : ignoreUntilLabel(condition, "full")}
      className={cn(
        "inline-flex items-center gap-1 rounded-md px-1.5 py-0.5 text-xs font-medium",
        variant.className,
        className
      )}
    >
      <variant.Icon className="size-3 shrink-0" aria-hidden="true" />
      {condition === null
        ? variant.label
        : ignoreStyle === "full"
          ? ignoreUntilLabel(condition, "full")
          : `${variant.label} · ${ignoreUntilLabel(condition, "compact")}`}
    </span>
  );
}
