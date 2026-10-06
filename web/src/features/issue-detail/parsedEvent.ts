import type { EventDetail } from "../../api.ts";
import { buildEventSections, type EventSection } from "./eventSections.ts";
import { buildHighlights, type Highlight } from "./highlights.ts";
import { readPayloadFields, type EventPayloadFields } from "./payloadReaders.ts";

/** Everything the event panel stack renders, derived from one pass over the payload. */
export interface ParsedEvent {
  readonly fields: EventPayloadFields;
  readonly highlights: readonly Highlight[];
  readonly sections: readonly EventSection[];
}

/**
 * Reads one stored event into every view its panels need.
 *
 * The panel stack has three independent consumers of the same payload — the
 * jump nav, the Highlights card and the context panels. Called per consumer,
 * the readers re-enumerate an attacker-sized `tags`/`extra`/`contexts` map
 * once each; this composes them once so the caller can memoize a single
 * result per event.
 */
export function parseEvent(event: EventDetail): ParsedEvent {
  const fields = readPayloadFields(event.payload);
  const highlights = buildHighlights(event, fields);
  return { fields, highlights, sections: buildEventSections(event, fields, highlights) };
}
