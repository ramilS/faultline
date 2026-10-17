import type { EventDetail } from "../../api.ts";
import { readServerName, readTransaction, type EventPayloadFields, type EventUser } from "./payloadReaders.ts";

export interface Highlight {
  label: string;
  value: string;
}

/** First non-empty user identity field, in the order a person would recognize someone by. */
function userIdentity(user: EventUser | null): string | null {
  if (user === null || user.isEmpty) {
    return null;
  }
  return user.email || user.username || user.id || user.ipAddress;
}

/**
 * The facts about who and where that the page shows nowhere above the trace:
 * the exception already heads the issue header and the trace itself, and the
 * environment and release already sit on the event bar, so repeating them
 * here would spend the strip on things the reader has just read.
 *
 * `fields` is the event's payload already read once by
 * {@link readPayloadFields}; this function re-reads nothing from
 * `event.payload` that `fields` already carries.
 */
export function buildHighlights(event: EventDetail, fields: EventPayloadFields): readonly Highlight[] {
  const highlights: Highlight[] = [];

  const identity = userIdentity(fields.user);
  if (identity !== null) {
    highlights.push({ label: "User", value: identity });
  }

  const transaction = readTransaction(event.payload, fields.tags);
  if (transaction !== "") {
    highlights.push({ label: "Transaction", value: transaction });
  }
  const serverName = readServerName(event.payload, fields.tags);
  if (serverName !== "") {
    highlights.push({ label: "Server", value: serverName });
  }

  return highlights;
}
