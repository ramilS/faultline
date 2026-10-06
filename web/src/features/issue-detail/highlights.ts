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
 * A fixed, honest one-glance summary — not Sentry's project-configurable
 * Highlights. Every entry here is a field this dashboard already parses
 * elsewhere; this just puts the handful most useful for triage together,
 * first, on the panel.
 *
 * `fields` is the event's payload already read once by
 * {@link readPayloadFields}; this function re-reads nothing from
 * `event.payload` that `fields` already carries.
 */
export function buildHighlights(event: EventDetail, fields: EventPayloadFields): readonly Highlight[] {
  const highlights: Highlight[] = [];

  if (event.symbolicated_exception !== null) {
    const { type, value } = event.symbolicated_exception;
    const composed = value ? `${type}: ${value}` : type;
    if (composed !== "") {
      highlights.push({ label: "Exception", value: composed });
    }
  }

  // Environment is only included when non-empty. EventNavigator.tsx already
  // defends against empty environment on the read path, so this field is not
  // guaranteed non-empty here either; omitting it from a summary reads better
  // than rendering a blank row.
  if (event.environment !== "") {
    highlights.push({ label: "Environment", value: event.environment });
  }
  if (event.release !== null) {
    highlights.push({ label: "Release", value: event.release });
  }

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
