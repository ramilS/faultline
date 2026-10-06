import assert from "node:assert/strict";
import { test } from "node:test";

import { buildHighlights } from "./highlights.ts";
import { readPayloadFields } from "./payloadReaders.ts";
import type { EventDetail } from "../../api.ts";

function baseEvent(overrides: Partial<EventDetail> = {}): EventDetail {
  return {
    event_id: "e1",
    timestamp: "2026-09-09T10:00:00Z",
    received_at: "2026-09-09T10:00:01Z",
    level: "error",
    environment: "production",
    release: "1.2.3",
    message: "boom",
    request_id: null,
    payload: {},
    symbolicated_exception: null,
    ...overrides
  };
}

/** buildHighlights takes the payload already read; production reads it once per event. */
function highlightsFor(event: EventDetail) {
  return buildHighlights(event, readPayloadFields(event.payload));
}

test("buildHighlights includes environment and release", () => {
  const highlights = highlightsFor(baseEvent());
  assert.deepEqual(
    highlights.filter((h) => h.label === "Environment" || h.label === "Release"),
    [
      { label: "Environment", value: "production" },
      { label: "Release", value: "1.2.3" }
    ]
  );
});

test("buildHighlights includes the exception type when symbolicated", () => {
  const highlights = highlightsFor(
    baseEvent({ symbolicated_exception: { type: "TimeoutError", value: "upstream timed out", cause: null, frames: [] } })
  );
  assert.deepEqual(highlights.find((h) => h.label === "Exception"), {
    label: "Exception",
    value: "TimeoutError: upstream timed out"
  });
});

test("buildHighlights includes the user identity when present and non-empty", () => {
  const highlights = highlightsFor(baseEvent({ payload: { user: { email: "a@example.com" } } }));
  assert.deepEqual(highlights.find((h) => h.label === "User"), { label: "User", value: "a@example.com" });
});

test("buildHighlights omits Release when the event has none", () => {
  const highlights = highlightsFor(baseEvent({ release: null }));
  assert.equal(highlights.some((h) => h.label === "Release"), false);
});

test("buildHighlights omits Environment when the event has none", () => {
  const highlights = highlightsFor(baseEvent({ environment: "" }));
  assert.equal(highlights.some((h) => h.label === "Environment"), false);
});

test("buildHighlights returns an empty array when the event has no exception, release, environment, or user", () => {
  const highlights = highlightsFor(
    baseEvent({ symbolicated_exception: null, release: null, environment: "", payload: {} })
  );
  assert.equal(highlights.length, 0);
});

test("buildHighlights omits Exception when the symbolicated exception has neither a type nor a value", () => {
  const highlights = highlightsFor(
    baseEvent({ symbolicated_exception: { type: "", value: "", cause: null, frames: [] } })
  );
  assert.equal(highlights.some((h) => h.label === "Exception"), false);
});

test("buildHighlights prefers the top-level transaction and server_name fields over same-named tags", () => {
  const highlights = highlightsFor(
    baseEvent({
      payload: {
        transaction: "GET /orders",
        server_name: "web-1",
        tags: { transaction: "stale", server_name: "stale" }
      }
    })
  );
  assert.deepEqual(highlights.find((h) => h.label === "Transaction"), { label: "Transaction", value: "GET /orders" });
  assert.deepEqual(highlights.find((h) => h.label === "Server"), { label: "Server", value: "web-1" });
});

test("buildHighlights falls back to the transaction/server_name tags when the top-level fields are absent", () => {
  const highlights = highlightsFor(baseEvent({ payload: { tags: { transaction: "GET /orders", server_name: "web-1" } } }));
  assert.deepEqual(highlights.find((h) => h.label === "Transaction"), { label: "Transaction", value: "GET /orders" });
  assert.deepEqual(highlights.find((h) => h.label === "Server"), { label: "Server", value: "web-1" });
});

test("buildHighlights omits Transaction and Server when neither the field nor the tag is present", () => {
  const highlights = highlightsFor(baseEvent({ payload: {} }));
  assert.equal(highlights.some((h) => h.label === "Transaction"), false);
  assert.equal(highlights.some((h) => h.label === "Server"), false);
});

test("buildHighlights does not throw when the payload is not an object", () => {
  assert.doesNotThrow(() => highlightsFor(baseEvent({ payload: "not an object" })));
  assert.doesNotThrow(() => highlightsFor(baseEvent({ payload: null })));
});

test("buildHighlights places Transaction and Server after User", () => {
  const highlights = highlightsFor(
    baseEvent({ payload: { user: { email: "a@example.com" }, transaction: "GET /orders", server_name: "web-1" } })
  );
  const labels = highlights.map((h) => h.label);
  assert.ok(labels.indexOf("User") < labels.indexOf("Transaction"));
  assert.ok(labels.indexOf("Transaction") < labels.indexOf("Server"));
});
