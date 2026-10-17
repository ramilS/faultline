import { ChevronRight, Footprints } from "lucide-react";
import { useMemo, useState } from "react";

import type { EventDetail } from "@/api";
import { EmptyState } from "@/components/EmptyState";
import { JsonTree } from "@/components/JsonTree";
import { LevelBadge } from "@/components/LevelBadge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";

import { buildBreadcrumbRows, filterBreadcrumbRows, offsetFromEvent, type BreadcrumbRow } from "./breadcrumbRows";
import { DetailSection } from "./DetailSection";
import { readBreadcrumbs } from "./payloadReaders";

interface BreadcrumbsProps {
  event: EventDetail;
}

/**
 * Rows shown inline, counting the closing row. The steps nearest the error
 * are nearly always the ones that explain it; the rest of the trail is one
 * click away rather than a long scroll between the trace and the context
 * panels below it.
 */
const PREVIEW_ROWS = 5;

/**
 * What the user was doing before the error, oldest first, ending with the
 * exception itself so the trail reads to a conclusion rather than stopping
 * mid-air. Sits below the stack trace: the trace says where it broke, this
 * says what led there.
 */
export function Breadcrumbs({ event }: BreadcrumbsProps) {
  const [allOpen, setAllOpen] = useState(false);
  const rows = useMemo(
    () =>
      buildBreadcrumbRows(readBreadcrumbs(event.payload), {
        timestamp: event.timestamp,
        level: event.level,
        message: event.message
      }),
    [event]
  );
  // The closing row is always present, so a trail with no breadcrumbs is one row.
  const crumbCount = rows.length - 1;
  const preview = rows.slice(-PREVIEW_ROWS);

  return (
    <DetailSection
      id="section-breadcrumbs"
      title="Breadcrumbs"
      action={
        rows.length > PREVIEW_ROWS ? (
          <Button variant="ghost" size="xs" onClick={() => setAllOpen(true)}>
            View all {crumbCount}
          </Button>
        ) : undefined
      }
    >
      {crumbCount === 0 ? (
        <EmptyState
          className="py-8"
          icon={Footprints}
          title="No breadcrumbs"
          description="Either breadcrumb collection wasn't enabled in the SDK, or this event predates the SDK version that started sending them."
        />
      ) : (
        <BreadcrumbList rows={preview} eventTimestamp={event.timestamp} />
      )}

      <AllBreadcrumbsDialog open={allOpen} onOpenChange={setAllOpen} rows={rows} eventTimestamp={event.timestamp} />
    </DetailSection>
  );
}

interface AllBreadcrumbsDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  rows: readonly BreadcrumbRow[];
  eventTimestamp: string;
}

function AllBreadcrumbsDialog({ open, onOpenChange, rows, eventTimestamp }: AllBreadcrumbsDialogProps) {
  const [query, setQuery] = useState("");
  const shown = filterBreadcrumbRows(rows, query);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="flex max-h-[calc(100dvh-2rem)] flex-col sm:max-w-3xl">
        <DialogHeader>
          <DialogTitle>Breadcrumbs</DialogTitle>
          <DialogDescription>Everything the SDK recorded before this event, oldest first.</DialogDescription>
        </DialogHeader>
        <Input
          aria-label="Filter breadcrumbs"
          placeholder="Category, level or message"
          value={query}
          onChange={(change) => setQuery(change.target.value)}
        />
        <div className="min-h-0 flex-1 overflow-y-auto">
          {shown.length === 0 ? (
            <p className="text-muted-foreground py-6 text-center text-sm">No breadcrumbs match “{query.trim()}”.</p>
          ) : (
            <BreadcrumbList rows={shown} eventTimestamp={eventTimestamp} />
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}

function BreadcrumbList({ rows, eventTimestamp }: { rows: readonly BreadcrumbRow[]; eventTimestamp: string }) {
  return (
    <ol className="overflow-hidden rounded-md border">
      {rows.map((row) => (
        <BreadcrumbListItem key={row.key} row={row} eventTimestamp={eventTimestamp} />
      ))}
    </ol>
  );
}

function BreadcrumbListItem({ row, eventTimestamp }: { row: BreadcrumbRow; eventTimestamp: string }) {
  const [open, setOpen] = useState(false);
  const hasData = row.data !== undefined;
  const offset = offsetFromEvent(row.timestamp, eventTimestamp);

  return (
    <li className={cn("border-t first:border-t-0", row.isClosingRow && "bg-muted/30")}>
      <div className="flex flex-wrap items-baseline gap-x-2 gap-y-1 px-3 py-2 text-sm">
        <span
          className="text-muted-foreground w-16 shrink-0 font-mono text-xs tabular-nums"
          title={row.timestamp ?? undefined}
        >
          {offset ?? "—"}
        </span>
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
