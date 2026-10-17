import assert from "node:assert/strict";
import { test } from "node:test";

import { parseEvent } from "./parsedEvent.ts";
import type { EventDetail } from "../../api.ts";

function baseEvent(payload: unknown): EventDetail {
  return {
    event_id: "e1",
    timestamp: "2026-09-09T10:00:00Z",
    received_at: "2026-09-09T10:00:01Z",
    level: "error",
    environment: "production",
    release: "1.2.3",
    message: "boom",
    request_id: null,
    payload,
    symbolicated_exception: null
  };
}

test("the nav, the highlights and the panel fields all describe the same parse", () => {
  const parsed = parseEvent(
    baseEvent({
      user: { email: "a@example.com" },
      tags: { transaction: "GET /orders" },
      sdk: { name: "sentry.python", version: "1.0" }
    })
  );
  const ids = parsed.sections.map((section) => section.id);

  assert.equal(parsed.fields.user?.email, "a@example.com");
  assert.ok(ids.includes("section-tags"), "a payload with tags must offer the Tags anchor");
  assert.ok(ids.includes("section-sdk"), "a payload with an sdk must offer the SDK anchor");
  assert.ok(parsed.highlights.some((highlight) => highlight.label === "Transaction"));
});

test("highlights are a strip above the trace, never a jump-nav section", () => {
  const parsed = parseEvent(baseEvent({ user: { email: "a@example.com" } }));

  assert.ok(parsed.highlights.length > 0);
  assert.equal(
    parsed.sections.some((section) => section.id === "section-highlights"),
    false
  );
});

test("a payload that is not an object parses to empty views rather than throwing", () => {
  const parsed = parseEvent(baseEvent("not an object"));

  assert.equal(parsed.fields.user, null);
  assert.deepEqual(parsed.fields.tags, []);
  assert.deepEqual(
    parsed.sections.map((section) => section.id),
    ["section-stack-trace", "section-breadcrumbs", "section-raw-payload"]
  );
});
