import { absoluteTime, compactAge } from "@/lib/time";
import { cn } from "@/lib/utils";

/**
 * A compact age with the exact instant available on hover and to assistive
 * technology, so density never costs precision.
 */
export function RelativeTime({ iso, className }: { iso: string; className?: string }) {
  return (
    <time dateTime={iso} title={absoluteTime(iso)} className={cn("whitespace-nowrap", className)}>
      {compactAge(iso)}
    </time>
  );
}
