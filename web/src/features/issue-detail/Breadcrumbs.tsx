import { ChevronRight, Footprints } from "lucide-react";
import { useState } from "react";

import type { EventDetail } from "@/api";
import { EmptyState } from "@/components/EmptyState";
import { JsonTree } from "@/components/JsonTree";
import { LevelBadge } from "@/components/LevelBadge";
import { RelativeTime } from "@/components/RelativeTime";
import { cn } from "@/lib/utils";

import { buildBreadcrumbRows, type BreadcrumbRow } from "./breadcrumbRows";
import { DetailSection } from "./DetailSection";
import { readBreadcrumbs } from "./payloadReaders";

interface BreadcrumbsProps {
  event: EventDetail;
}

/**
 * What the user was doing before the error, oldest first, ending with the
 * exception itself so the trail reads to a conclusion rather than stopping
 * mid-air. Sits above the stack trace: the trace says where it broke, this
 * says what led there.
 */
export function Breadcrumbs({ event }: BreadcrumbsProps) {
  const breadcrumbs = readBreadcrumbs(event.payload);

  return (
    <DetailSection id="section-breadcrumbs" title="Breadcrumbs">
      {breadcrumbs.length === 0 ? (
        <EmptyState
          className="py-8"
          icon={Footprints}
          title="No breadcrumbs"
          description="Either breadcrumb collection wasn't enabled in the SDK, or this event predates the SDK version that started sending them."
        />
      ) : (
        <ol className="overflow-hidden rounded-md border">
          {buildBreadcrumbRows(breadcrumbs, {
            timestamp: event.timestamp,
            level: event.level,
            message: event.message
          }).map((row) => (
            <BreadcrumbListItem key={row.key} row={row} />
          ))}
        </ol>
      )}
    </DetailSection>
  );
}

function BreadcrumbListItem({ row }: { row: BreadcrumbRow }) {
  const [open, setOpen] = useState(false);
  const hasData = row.data !== undefined;

  return (
    <li className={cn("border-t first:border-t-0", row.isClosingRow && "bg-muted/30")}>
      <div className="flex flex-wrap items-baseline gap-x-2 gap-y-1 px-3 py-2 text-sm">
        {row.timestamp === null ? (
          <span className="text-muted-foreground w-10 shrink-0 text-xs">—</span>
        ) : (
          <RelativeTime iso={row.timestamp} className="text-muted-foreground shrink-0 text-xs" />
        )}
        <LevelBadge level={row.level} />
        <span className="text-muted-foreground shrink-0 font-mono text-xs">
          {row.category === "" ? "—" : row.category}
        </span>
        <span className="min-w-0 flex-1 break-words">
          {row.messageHref !== null ? (
            <a href={row.messageHref} target="_blank" rel="noreferrer" className="underline underline-offset-2">
              {row.message}
            </a>
          ) : (
            row.message
          )}
        </span>
        {hasData && (
          <button
            type="button"
            onClick={() => setOpen((value) => !value)}
            aria-expanded={open}
            className="text-muted-foreground hover:text-foreground ml-auto flex shrink-0 items-center gap-1 text-xs transition-colors"
          >
            <ChevronRight className={cn("size-3 shrink-0 transition-transform", open && "rotate-90")} aria-hidden="true" />
            data
          </button>
        )}
      </div>
      {hasData && open && (
        <div className="mx-3 mb-2 max-h-64 overflow-auto rounded-md border p-2">
          <JsonTree value={row.data} />
        </div>
      )}
    </li>
  );
}
