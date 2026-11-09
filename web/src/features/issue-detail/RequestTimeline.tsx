import { useCallback } from "react";

import { api } from "@/apiClient";
import { ErrorState } from "@/components/ErrorState";
import { Skeleton } from "@/components/ui/skeleton";
import { useAsyncResource } from "@/hooks/useAsyncResource";
import { layoutBars, type TimelineBar } from "@/timelineLayout";
import { cn } from "@/lib/utils";

import { DetailSection, EmptySectionNote } from "./DetailSection";
import { SECTION_SCROLL_MARGIN } from "./stickyHeader";

interface RequestTimelineProps {
  eventId: string;
  requestId: string;
}

export function RequestTimeline({ eventId, requestId }: RequestTimelineProps) {
  const load = useCallback(() => api.getTimeline(eventId), [eventId]);
  const { state, reload } = useAsyncResource(load);

  const empty = state.status === "ready" && state.value.length === 0;

  // `useActiveSection` observes the element it finds when the section list
  // changes, which a finished fetch does not do: the anchor stays the same
  // element across the swap from the loading card to the empty note.
  return (
    <div id="section-timeline" style={{ scrollMarginTop: SECTION_SCROLL_MARGIN }}>
      {empty ? (
        <EmptySectionNote>
          No spans traced for request <span className="font-mono break-all">{requestId}</span> — spans arrive on the
          envelope alongside the event.
        </EmptySectionNote>
      ) : (
        <DetailSection title="Request timeline" description={<span className="font-mono break-all">{requestId}</span>}>
          {state.status === "loading" && (
            <div className="space-y-2">
              {[0, 1, 2].map((row) => (
                <Skeleton key={row} className="h-6 w-full" />
              ))}
            </div>
          )}

          {state.status === "failed" && <ErrorState className="py-8" message={state.message} onRetry={reload} />}

          {state.status === "ready" && <SpanBars bars={layoutBars(state.value)} />}
        </DetailSection>
      )}
    </div>
  );
}

/** Non-ok spans are the reason to open a timeline; give them the severity colour. */
function barClasses(status: string): string {
  return status === "ok" || status === "" ? "bg-foreground/70" : "bg-level-error";
}

function SpanBars({ bars }: { bars: readonly TimelineBar[] }) {
  return (
    <ul aria-label="Request timeline" className="space-y-1">
      {bars.map((bar) => (
        <li key={bar.span.span_id} className="grid grid-cols-[minmax(0,10rem)_1fr_auto] items-center gap-3">
          <span className="font-mono text-xs break-words" style={{ paddingLeft: `${bar.depth * 0.75}rem` }}>
            {bar.span.name}
          </span>
          <span className="bg-muted relative block h-4 rounded-sm">
            <span
              className={cn("absolute inset-y-0 rounded-sm", barClasses(bar.span.status))}
              style={{ left: `${bar.offsetPercent}%`, width: `${bar.widthPercent}%` }}
            />
          </span>
          <span className="text-muted-foreground text-xs tabular-nums">{bar.span.duration_ms.toFixed(1)}ms</span>
        </li>
      ))}
    </ul>
  );
}
