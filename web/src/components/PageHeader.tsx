import type { ReactNode } from "react";

import { cn } from "@/lib/utils";

interface PageHeaderProps {
  title: ReactNode;
  description?: ReactNode;
  /** Primary action for the page. Keep it to one. */
  children?: ReactNode;
  className?: string;
}

/**
 * The consistent top band of every screen. Sticky so the page identity and its
 * primary action stay reachable while a long issue list scrolls.
 */
export function PageHeader({ title, description, children, className }: PageHeaderProps) {
  return (
    <header
      className={cn(
        // Only sticky where it has the viewport to itself; on small screens the
        // shell's own top bar occupies top-0 and the two would overlap.
        "bg-background/80 z-10 flex items-start justify-between gap-4 border-b px-4 py-4 backdrop-blur sm:px-6 lg:sticky lg:top-0",
        className
      )}
    >
      <div className="min-w-0">
        <h1 className="truncate text-lg font-semibold tracking-tight">{title}</h1>
        {description !== undefined && <p className="text-muted-foreground mt-0.5 text-sm">{description}</p>}
      </div>
      {children !== undefined && <div className="flex shrink-0 items-center gap-2">{children}</div>}
    </header>
  );
}
