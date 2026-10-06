import assert from "node:assert/strict";
import { test } from "node:test";

import { paramsWithSelectedEvent, selectedEventFromParams } from "./selectedEvent.ts";

test("a missing event param reads as no selection", () => {
  assert.equal(selectedEventFromParams(new URLSearchParams("")), null);
});

test("an event id round-trips through the params", () => {
  const id = "0b5f2f4e-1f3a-4c9e-9a1e-2b7c8d9e0f11";
  const params = paramsWithSelectedEvent(new URLSearchParams(""), id);
  assert.equal(selectedEventFromParams(params), id);
});

test("writing null removes the param rather than emptying it", () => {
  const params = paramsWithSelectedEvent(new URLSearchParams("event=abc"), null);
  // An `event=` left behind would round-trip as a selection of the empty
  // string, which matches no event and would strand the panel on a skeleton.
  assert.equal(params.has("event"), false);
});

test("an empty or blank event param reads as no selection", () => {
  assert.equal(selectedEventFromParams(new URLSearchParams("event=")), null);
  assert.equal(selectedEventFromParams(new URLSearchParams("event=%20%20")), null);
});

test("other params survive a selection write", () => {
  const params = paramsWithSelectedEvent(new URLSearchParams("statsPeriod=7d"), "abc");
  assert.equal(params.get("statsPeriod"), "7d");
});
