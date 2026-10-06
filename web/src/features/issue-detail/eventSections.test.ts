import assert from "node:assert/strict";
import { test } from "node:test";

import { buildEventSections } from "./eventSections.ts";
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

function ids(event: EventDetail): readonly string[] {
  const fields = readPayloadFields(event.payload);
  return buildEventSections(event, fields, buildHighlights(event, fields)).map((section) => section.id);
}

const FULL_PAYLOAD = {
  user: { email: "a@example.com" },
  request: { method: "GET", url: "https://example.com" },
  tags: { env: "prod" },
  contexts: { os: { name: "Linux" } },
  sdk: { name: "sentry.python", version: "1.0" },
  extra: { foo: "bar" },
  modules: { django: "4.0" }
};

test("an event with everything present yields every section in DOM order", () => {
  const event = baseEvent({
    payload: FULL_PAYLOAD,
    request_id: "req-1",
    symbolicated_exception: { type: "TimeoutError", value: "upstream timed out", cause: null, frames: [] }
  });

  assert.deepEqual(ids(event), [
    "section-highlights",
    "section-breadcrumbs",
    "section-stack-trace",
    "section-user",
    "section-request",
    "section-tags",
    "section-contexts",
    "section-sdk",
    "section-extra",
    "section-modules",
    "section-timeline",
    "section-raw-payload"
  ]);
});

test("an event with none of the optional payload sections yields only the unconditional ones", () => {
  const event = baseEvent({
    payload: {},
    release: null,
    environment: "",
    request_id: null,
    symbolicated_exception: null
  });

  assert.deepEqual(ids(event), ["section-breadcrumbs", "section-stack-trace", "section-raw-payload"]);
});

test("breadcrumbs and stack trace appear even when the event has no exception and no breadcrumbs", () => {
  const event = baseEvent({ payload: {}, symbolicated_exception: null });

  assert.ok(ids(event).includes("section-breadcrumbs"));
  assert.ok(ids(event).includes("section-stack-trace"));
});

test("raw payload appears unconditionally", () => {
  assert.ok(ids(baseEvent()).includes("section-raw-payload"));
});

test("highlights appears only when buildHighlights has content", () => {
  assert.ok(!ids(baseEvent({ release: null, environment: "" })).includes("section-highlights"));
  assert.ok(ids(baseEvent({ environment: "production" })).includes("section-highlights"));
});

test("user appears only when the payload carries a non-empty user", () => {
  assert.ok(!ids(baseEvent({ payload: {} })).includes("section-user"));
  assert.ok(!ids(baseEvent({ payload: { user: {} } })).includes("section-user"));
  assert.ok(ids(baseEvent({ payload: { user: { email: "a@example.com" } } })).includes("section-user"));
});

test("request appears only when the payload carries a non-empty request", () => {
  assert.ok(!ids(baseEvent({ payload: {} })).includes("section-request"));
  assert.ok(!ids(baseEvent({ payload: { request: {} } })).includes("section-request"));
  assert.ok(ids(baseEvent({ payload: { request: { method: "GET" } } })).includes("section-request"));
});

test("tags appears only when the payload carries at least one tag", () => {
  assert.ok(!ids(baseEvent({ payload: {} })).includes("section-tags"));
  assert.ok(ids(baseEvent({ payload: { tags: { env: "prod" } } })).includes("section-tags"));
});

test("contexts appears only when the payload carries at least one non-empty context", () => {
  assert.ok(!ids(baseEvent({ payload: {} })).includes("section-contexts"));
  assert.ok(!ids(baseEvent({ payload: { contexts: { trace: { op: "http" } } } })).includes("section-contexts"));
  assert.ok(ids(baseEvent({ payload: { contexts: { os: { name: "Linux" } } } })).includes("section-contexts"));
});

test("sdk appears only when the payload names a reporting SDK", () => {
  assert.ok(!ids(baseEvent({ payload: {} })).includes("section-sdk"));
  assert.ok(ids(baseEvent({ payload: { sdk: { name: "sentry.python", version: "1.0" } } })).includes("section-sdk"));
});

test("additional data appears only when the payload carries at least one extra entry", () => {
  assert.ok(!ids(baseEvent({ payload: {} })).includes("section-extra"));
  assert.ok(ids(baseEvent({ payload: { extra: { foo: "bar" } } })).includes("section-extra"));
});

test("dependencies appears only when the payload lists at least one module", () => {
  assert.ok(!ids(baseEvent({ payload: {} })).includes("section-modules"));
  assert.ok(ids(baseEvent({ payload: { modules: { django: "4.0" } } })).includes("section-modules"));
});

test("request timeline appears only when the event carries a request id", () => {
  assert.ok(!ids(baseEvent({ request_id: null })).includes("section-timeline"));
  assert.ok(ids(baseEvent({ request_id: "req-1" })).includes("section-timeline"));
});
