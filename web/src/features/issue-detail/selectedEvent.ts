export const SELECTED_EVENT_PARAM = "event";

/** The event id in the URL, or null. Malformed values read as null rather than throwing. */
export function selectedEventFromParams(params: URLSearchParams): string | null {
  const value = params.get(SELECTED_EVENT_PARAM);
  if (value === null) {
    return null;
  }
  const trimmed = value.trim();
  return trimmed === "" ? null : trimmed;
}

/** Returns the params with the selection written, or removed when null. */
export function paramsWithSelectedEvent(params: URLSearchParams, eventId: string | null): URLSearchParams {
  const next = new URLSearchParams(params);
  if (eventId === null) {
    next.delete(SELECTED_EVENT_PARAM);
  } else {
    next.set(SELECTED_EVENT_PARAM, eventId);
  }
  return next;
}
