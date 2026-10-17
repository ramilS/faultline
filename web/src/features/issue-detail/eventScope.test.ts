import assert from "node:assert/strict";
import { test } from "node:test";

import {
  describeEventScope,
  eventScopeFilters,
  eventScopeFromParams,
  paramsWithEnvironment,
  paramsWithEventScope,
  type EventScope
} from "./eventScope.ts";

/** Locale-independent stand-in for the screens' `absoluteTime`. */
const asIs = (iso: string): string => iso;

/** The scope that narrows nothing — this test's own fixture, not a production export. */
const UNSCOPED_EVENTS: EventScope = { environment: "", start: "", end: "" };

test("a scope survives a round trip through the query string", () => {
  const scope = { environment: "staging", start: "2026-09-01T00:00:00.000Z", end: "2026-09-08T00:00:00.000Z" };

  const params = paramsWithEventScope(new URLSearchParams(), scope);

  assert.deepEqual(eventScopeFromParams(params), scope);
});

test("the parameter names are the ones the events endpoint already reads", () => {
  const params = paramsWithEventScope(new URLSearchParams(), {
    environment: "staging",
    start: "2026-09-01T00:00:00.000Z",
    end: "2026-09-08T00:00:00.000Z"
  });

  assert.equal(params.toString(), "environment=staging&start=2026-09-01T00%3A00%3A00.000Z&end=2026-09-08T00%3A00%3A00.000Z");
});

test("an unscoped value writes no parameters and clears any it finds", () => {
  const params = paramsWithEventScope(new URLSearchParams("environment=staging&start=x&event=evt-1"), UNSCOPED_EVENTS);

  assert.equal(params.toString(), "event=evt-1");
});

test("blank and missing parameters read as unscoped", () => {
  assert.deepEqual(eventScopeFromParams(new URLSearchParams("environment=%20%20")), UNSCOPED_EVENTS);
  assert.deepEqual(eventScopeFromParams(new URLSearchParams()), UNSCOPED_EVENTS);
});

test("only non-empty fields reach the request", () => {
  assert.deepEqual(eventScopeFilters({ environment: "staging", start: "", end: "2026-09-08T00:00:00.000Z" }), {
    environment: "staging",
    start: undefined,
    end: "2026-09-08T00:00:00.000Z"
  });
});

test("an unscoped list is described as unscoped rather than as a filtered one", () => {
  assert.equal(describeEventScope(UNSCOPED_EVENTS, asIs), null);
});

test("the description names the environment, the window, or both", () => {
  assert.equal(describeEventScope({ environment: "staging", start: "", end: "" }, asIs), "Only events in staging.");
  assert.equal(
    describeEventScope({ environment: "", start: "A", end: "B" }, asIs),
    "Only events received between A and B."
  );
  assert.equal(
    describeEventScope({ environment: "staging", start: "A", end: "B" }, asIs),
    "Only events in staging received between A and B."
  );
});

test("a half-open window is described by the bound it actually has", () => {
  assert.equal(describeEventScope({ environment: "", start: "A", end: "" }, asIs), "Only events received after A.");
  assert.equal(describeEventScope({ environment: "", start: "", end: "B" }, asIs), "Only events received before B.");
});

test("writing the environment alone leaves every other parameter as it was", () => {
  const params = new URLSearchParams("event=e1&start=2026-09-01T00:00:00Z");

  assert.equal(paramsWithEnvironment(params, "production").toString(), "event=e1&start=2026-09-01T00%3A00%3A00Z&environment=production");
  assert.equal(
    paramsWithEnvironment(new URLSearchParams("environment=production&event=e1"), "").toString(),
    "event=e1"
  );
  assert.equal(paramsWithEnvironment(new URLSearchParams("environment=staging"), "production").toString(), "environment=production");
});
