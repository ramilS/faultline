import { AlertTriangle, Bug, Info, OctagonAlert, Skull } from "lucide-react";

import { cn } from "@/lib/utils";
import { levelVariant } from "@/lib/levelVariant";

const ICONS: Record<string, typeof Info> = {
  fatal: Skull,
  error: OctagonAlert,
  warning: AlertTriangle,
  info: Info,
  debug: Bug
};

/**
 * Severity, carried by an icon and a word as well as colour — colour alone
 * would leave the distinction invisible to a colour-blind reader.
 */
export function LevelBadge({ level, className }: { level: string; className?: string }) {
  const variant = levelVariant(level);
  const Icon = ICONS[level.toLowerCase()] ?? Info;

  return (
    <span
      className={cn(
        "inline-flex items-center gap-1 rounded-md px-1.5 py-0.5 text-xs font-medium",
        variant.className,
        className
      )}
    >
      <Icon className="size-3 shrink-0" aria-hidden="true" />
      {variant.label}
    </span>
  );
}
