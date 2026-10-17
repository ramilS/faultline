import { Bug } from "lucide-react";
import { useCallback, useMemo } from "react";

import type { EventDetail, EventSummary } from "@/api";
import { api } from "@/apiClient";
import { EmptyState } from "@/components/EmptyState";
import { ErrorState } from "@/components/ErrorState";
import { Skeleton } from "@/components/ui/skeleton";

import { Breadcrumbs } from "./Breadcrumbs";
import { DetailSection } from "./DetailSection";
import { EventContextPanels } from "./EventContextPanels";
import { EventSectionNav } from "./EventSectionNav";
import { HighlightsPanel } from "./HighlightsPanel";
import { parseEvent } from "./parsedEvent";
import { RawPayload } from "./RawPayload";
import { RequestTimeline } from "./RequestTimeline";
import { StackTrace } from "./StackTrace";
import { useAsyncResource } from "@/hooks/useAsyncResource";

interface EventPanelsProps {
  /** The event to show. Null before the navigator's first page lands. */
  selectedEvent: EventSummary | null;
}

/**
 * The panels that need a full stored event: symbolicated trace, request
 * timeline, raw payload.
 *
 * `selectedEvent` is null only before the navigator's first page lands —
 * there is nothing to fetch yet, so this renders the skeleton itself rather
 * than starting a request. Once an event is selected, `LoadedEventPanels` is
 * keyed on its id so switching events remounts the resource instead of
 * patching a load meant for a different event.
 */
export function EventPanels({ selectedEvent }: EventPanelsProps) {
  if (selectedEvent === null) {
    return <Skeleton className="h-64 w-full" />;
  }

  return <LoadedEventPanels key={selectedEvent.event_id} eventId={selectedEvent.event_id} />;
}

interface LoadedEventPanelsProps {
  eventId: string;
}

function LoadedEventPanels({ eventId }: LoadedEventPanelsProps) {
  const load = useCallback(() => api.getEvent(eventId), [eventId]);
  const { state, reload } = useAsyncResource(load);

  if (state.status === "loading") {
    return <Skeleton className="h-64 w-full" />;
  }

  if (state.status === "failed") {
    return (
      <DetailSection title="Event detail">
        <ErrorState className="py-8" message={state.message} onRetry={reload} />
      </DetailSection>
    );
  }

  return <EventPanelStack event={state.value} />;
}

interface EventPanelStackProps {
  event: EventDetail;
}

/**
 * The panel stack for one loaded event.
 *
 * Split from the resource above so the payload is parsed where the event is
 * known to exist: the nav, the highlights strip and the context panels all
 * read the same untrusted maps, and every reader enumerates before it caps,
 * so parsing per consumer re-walks an attacker-sized `tags`/`extra` map once
 * per consumer, on every render. One memoized parse feeds all three.
 */
function EventPanelStack({ event }: EventPanelStackProps) {
  const parsed = useMemo(() => parseEvent(event), [event]);

  return (
    <>
      <EventSectionNav sections={parsed.sections} />

      <HighlightsPanel highlights={parsed.highlights} />

      {event.symbolicated_exception === null ? (
        <DetailSection id="section-stack-trace" title="Stack trace">
          <EmptyState
            className="py-8"
            icon={Bug}
            title="No exception"
            description="This event was captured as a message rather than an exception, so there is no trace to symbolicate."
          />
        </DetailSection>
      ) : (
        <StackTrace exception={event.symbolicated_exception} />
      )}

      <Breadcrumbs event={event} />

      <EventContextPanels fields={parsed.fields} />

      {event.request_id !== null && (
        <RequestTimeline eventId={event.event_id} requestId={event.request_id} />
      )}

      <RawPayload payload={event.payload} />
    </>
  );
}
