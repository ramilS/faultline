import { Waypoints } from "lucide-react";
import { useCallback } from "react";

import { api } from "@/apiClient";
import { EmptyState } from "@/components/EmptyState";
import { ErrorState } from "@/components/ErrorState";
import { Skeleton } from "@/components/ui/skeleton";
import { layoutBars, type TimelineBar } from "@/timelineLayout";
import { cn } from "@/lib/utils";

import { DetailSection } from "./DetailSection";
import { useAsyncResource } from "@/hooks/useAsyncResource";

interface RequestTimelineProps {
  eventId: string;
  requestId: string;
}

export function RequestTimeline({ eventId, requestId }: RequestTimelineProps) {
  const load = useCallback(() => api.getTimeline(eventId), [eventId]);
  const { state, reload } = useAsyncResource(load);

  return (
    <DetailSection
      id="section-timeline"
      title="Request timeline"
      description={<span className="font-mono break-all">{requestId}</span>}
    >
      {state.status === "loading" && (
        <div className="space-y-2">
          {[0, 1, 2].map((row) => (
            <Skeleton key={row} className="h-6 w-full" />
          ))}
        </div>
      )}

      {state.status === "failed" && <ErrorState className="py-8" message={state.message} onRetry={reload} />}

      {state.status === "ready" && state.value.length === 0 && (
        <EmptyState
          className="py-8"
          icon={Waypoints}
          title="No spans"
          description="Nothing was traced for this request. Spans arrive on the envelope alongside the event."
        />
      )}

      {state.status === "ready" && state.value.length > 0 && <SpanBars bars={layoutBars(state.value)} />}
    </DetailSection>
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
