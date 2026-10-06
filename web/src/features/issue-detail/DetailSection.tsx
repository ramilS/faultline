import type { ReactNode } from "react";

import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { cn } from "@/lib/utils";

import { SECTION_SCROLL_MARGIN } from "./stickyHeader";

interface DetailSectionProps {
  title: ReactNode;
  description?: ReactNode;
  /** Controls scoped to this section — a period switch, a filter toggle. */
  action?: ReactNode;
  children: ReactNode;
  className?: string;
  contentClassName?: string;
  /** Scroll-anchor id, for jump-to-section navigation. */
  id?: string;
}

/** The one card shape every panel on the issue page uses, so they line up. */
export function DetailSection({
  title,
  description,
  action,
  children,
  className,
  contentClassName,
  id
}: DetailSectionProps) {
  return (
    // The scroll margin tracks the issue header's measured height rather
    // than a literal: see stickyHeader.ts. Inline because the value is a
    // calc() over a runtime custom property, not one of Tailwind's steps.
    <Card id={id} className={cn("gap-4 py-4", className)} style={{ scrollMarginTop: SECTION_SCROLL_MARGIN }}>
      <CardHeader className="px-4">
        <CardTitle className="text-sm">{title}</CardTitle>
        {description !== undefined && <CardDescription className="text-xs">{description}</CardDescription>}
        {action !== undefined && <CardAction>{action}</CardAction>}
      </CardHeader>
      <CardContent className={cn("px-4", contentClassName)}>{children}</CardContent>
    </Card>
  );
}
