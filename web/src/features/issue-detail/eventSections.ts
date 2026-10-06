import type { EventDetail } from "../../api.ts";
import type { Highlight } from "./highlights.ts";
import type { EventPayloadFields, HttpRequest } from "./payloadReaders.ts";

export interface EventSection {
  readonly id: string;
  readonly label: string;
}

/**
 * True when a `request` key was present but carried no field worth showing.
 * Lives here rather than beside the panel so the nav entry and the panel that
 * entry points at cannot disagree: `EventContextPanels` imports this one.
 */
export function isRequestEmpty(request: HttpRequest): boolean {
  return request.method === "" && request.url === "" && request.query === "" && request.headers.length === 0;
}

/**
 * The event panel's jump-nav entries, in the exact order those panels render.
 *
 * Takes the payload already read by {@link readPayloadFields} and the
 * highlights already built from it, so the nav costs no second parse of an
 * event the panels below it are about to render from.
 *
 * Every panel below always renders a card except the ones gated here: those
 * either render nothing at all when their data is absent (Highlights, User,
 * Request, Tags, Contexts, SDK, Additional data, Dependencies, Request
 * timeline) or don't exist in this dashboard's event model to be conditional
 * (Breadcrumbs, Stack trace, and Raw payload always render — the first two
 * as empty-state cards, the last unconditionally). A section belongs here
 * exactly when its card is in the DOM, so each condition below must mirror
 * the panel's own render guard rather than approximate it.
 */
export function buildEventSections(
  event: EventDetail,
  fields: EventPayloadFields,
  highlights: readonly Highlight[]
): readonly EventSection[] {
  const { user, request, contexts, sdk, tags, extra, modules } = fields;

  return [
    ...(highlights.length > 0 ? [{ id: "section-highlights", label: "Highlights" }] : []),
    { id: "section-breadcrumbs", label: "Breadcrumbs" },
    { id: "section-stack-trace", label: "Stack trace" },
    ...(user !== null && !user.isEmpty ? [{ id: "section-user", label: "User" }] : []),
    ...(request !== null && !isRequestEmpty(request) ? [{ id: "section-request", label: "Request" }] : []),
    ...(tags.length > 0 ? [{ id: "section-tags", label: "Tags" }] : []),
    ...(contexts.length > 0 ? [{ id: "section-contexts", label: "Contexts" }] : []),
    ...(sdk !== null ? [{ id: "section-sdk", label: "SDK" }] : []),
    ...(extra.length > 0 ? [{ id: "section-extra", label: "Additional data" }] : []),
    ...(modules.length > 0 ? [{ id: "section-modules", label: "Dependencies" }] : []),
    ...(event.request_id !== null ? [{ id: "section-timeline", label: "Request timeline" }] : []),
    { id: "section-raw-payload", label: "Raw payload" }
  ];
}
