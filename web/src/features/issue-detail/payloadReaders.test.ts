import assert from "node:assert/strict";
import { test } from "node:test";

import {
  MAX_RENDERED_BREADCRUMBS,
  MAX_RENDERED_CONTEXT_FIELDS,
  MAX_RENDERED_CONTEXTS,
  MAX_RENDERED_EXTRA_ENTRIES,
  MAX_RENDERED_MODULES,
  MAX_RENDERED_REQUEST_HEADERS,
  MAX_RENDERED_TAGS,
  MAX_RENDERED_VALUE_CHARS,
  REDACTED,
  TRUNCATION_SUFFIX,
  SENSITIVE_REQUEST_HEADERS,
  isSafeHref,
  readBreadcrumbs,
  readContexts,
  readExtra,
  readModules,
  readRequest,
  readSdk,
  readServerName,
  readTags,
  readTransaction,
  readUser
} from "./payloadReaders.ts";

test("reads the native envelope's bare breadcrumb array", () => {
  const crumbs = readBreadcrumbs({
    breadcrumbs: [{ timestamp: "2026-08-11T10:00:00Z", category: "http", level: "info", message: "GET /cart" }]
  });
  assert.equal(crumbs.length, 1);
  assert.equal(crumbs[0].category, "http");
  assert.equal(crumbs[0].message, "GET /cart");
});

test("reads the Sentry envelope's wrapped breadcrumb object", () => {
  const crumbs = readBreadcrumbs({
    breadcrumbs: { values: [{ category: "ui.click", message: "button#pay" }] }
  });
  assert.equal(crumbs.length, 1);
  assert.equal(crumbs[0].category, "ui.click");
});

test("a payload with no breadcrumbs reads as an empty list", () => {
  assert.deepEqual(readBreadcrumbs({}), []);
  assert.deepEqual(readBreadcrumbs(null), []);
  assert.deepEqual(readBreadcrumbs("not an object"), []);
  assert.deepEqual(readBreadcrumbs({ breadcrumbs: 42 }), []);
});

test("a breadcrumb of an unexpected shape survives as data rather than vanishing", () => {
  const crumbs = readBreadcrumbs({ breadcrumbs: [{ weird: { nested: true } }] });
  // Dropping it would hide evidence at exactly the moment someone is looking
  // for evidence. It renders as raw JSON instead.
  assert.equal(crumbs.length, 1);
  assert.equal(crumbs[0].message, "");
  assert.deepEqual(crumbs[0].data, { weird: { nested: true } });
});

test("breadcrumbs are bounded before render", () => {
  const many = Array.from({ length: 500 }, (_, index) => ({ message: `crumb ${index}` }));
  const crumbs = readBreadcrumbs({ breadcrumbs: many });
  // The ingest cap is per event and per protocol; a stored payload from an
  // older or hand-crafted client is not bound by today's constant.
  assert.equal(crumbs.length, MAX_RENDERED_BREADCRUMBS);
  // The most recent are the ones that matter, so the tail is kept.
  assert.equal(crumbs[crumbs.length - 1].message, "crumb 499");
});

test("a numeric breadcrumb timestamp reads as an instant, not as nothing", () => {
  // @sentry/core stamps every breadcrumb with dateTimestampInSeconds(), a
  // float of seconds since epoch — the primary protocol never sends a string
  // here, and dropping it leaves the trail with no chronology at all.
  const crumbs = readBreadcrumbs({
    breadcrumbs: { values: [{ timestamp: 1786528800.123, category: "http", message: "GET /cart" }] }
  });
  assert.equal(crumbs[0].timestamp, "2026-08-12T10:00:00.123Z");
});

test("an unusable breadcrumb timestamp reads as absent rather than throwing", () => {
  assert.equal(readBreadcrumbs({ breadcrumbs: [{ timestamp: Number.NaN }] })[0].timestamp, null);
  assert.equal(readBreadcrumbs({ breadcrumbs: [{ timestamp: Number.POSITIVE_INFINITY }] })[0].timestamp, null);
  assert.equal(readBreadcrumbs({ breadcrumbs: [{ timestamp: 1e18 }] })[0].timestamp, null);
  assert.equal(readBreadcrumbs({ breadcrumbs: [{ timestamp: { at: 1 } }] })[0].timestamp, null);
});

test("a non-string breadcrumb message is coerced, not dropped", () => {
  const crumbs = readBreadcrumbs({ breadcrumbs: [{ message: 42 }] });
  assert.equal(crumbs[0].message, "42");
});

test("readUser reports empty when every field is missing", () => {
  assert.equal(readUser({ user: {} })?.isEmpty, true);
  assert.equal(readUser({}), null);
});

test("readUser reads both ip_address and ip", () => {
  assert.equal(readUser({ user: { ip_address: "203.0.113.5" } })?.ipAddress, "203.0.113.5");
  assert.equal(readUser({ user: { ip: "203.0.113.5" } })?.ipAddress, "203.0.113.5");
});

test("readRequest flattens headers into a stable order", () => {
  const request = readRequest({ request: { method: "POST", url: "https://x/y", headers: { B: "2", A: "1" } } });
  assert.deepEqual(request?.headers, [
    { name: "A", value: "1" },
    { name: "B", value: "2" }
  ]);
});

test("request headers are bounded before render", () => {
  const many: Record<string, string> = {};
  for (let index = 0; index < 500; index++) {
    many[`header-${String(index).padStart(3, "0")}`] = String(index);
  }
  const request = readRequest({ request: { headers: many } });
  assert.equal(request?.headers.length, MAX_RENDERED_REQUEST_HEADERS);
  // Headers have no time order the way breadcrumbs do; the bound is applied
  // to wire order before the alphabetical sort, so the earliest-sent header
  // names are the ones retained.
  assert.equal(request?.headers[0].name, "header-000");
  assert.equal(
    request?.headers.some((header) => header.name === "header-499"),
    false
  );
});

test("sensitive headers are redacted, case-insensitively, with the name kept", () => {
  const request = readRequest({
    request: { headers: { authorization: "Bearer live-token", "X-API-KEY": "abc", Accept: "application/json" } }
  });
  const byName = new Map(request?.headers.map((header) => [header.name.toLowerCase(), header.value]));

  assert.equal(byName.get("authorization"), REDACTED);
  assert.equal(byName.get("x-api-key"), REDACTED);
  // A non-sensitive header must survive, or the panel is useless.
  assert.equal(byName.get("accept"), "application/json");
});

test("redaction survives surrounding whitespace in the stored header name", () => {
  // A proxy or SDK emitting a nonstandard key like " Authorization" still
  // carries a live credential; matching must not be defeated by whitespace
  // nobody would call malicious, just nonstandard.
  const request = readRequest({
    request: { headers: { " Authorization": "Bearer live-token", "Proxy-Authorization ": "Basic live-creds" } }
  });
  const byName = new Map(request?.headers.map((header) => [header.name.trim().toLowerCase(), header.value]));

  assert.equal(byName.get("authorization"), REDACTED);
  assert.equal(byName.get("proxy-authorization"), REDACTED);
});

test("the redaction list is exported so a caller can assert its exact contents", () => {
  assert.deepEqual(
    [...SENSITIVE_REQUEST_HEADERS].sort(),
    ["authorization", "cookie", "proxy-authorization", "set-cookie", "x-api-key"]
  );
});

test("isSafeHref admits only http and https", () => {
  assert.equal(isSafeHref("https://example.com"), true);
  assert.equal(isSafeHref("http://example.com"), true);
  // A breadcrumb URL is attacker-supplied. One click from execution is not a
  // risk worth a convenience.
  assert.equal(isSafeHref("javascript:alert(1)"), false);
  assert.equal(isSafeHref("data:text/html,<script>"), false);
  assert.equal(isSafeHref("//example.com"), false);
  assert.equal(isSafeHref("not a url"), false);
});

test("isSafeHref rejects a scheme hidden behind control characters", () => {
  // The URL parser strips interior tabs and newlines before parsing, so this
  // normalises to javascript:. Pinned because a regression to prefix matching
  // ("starts with http://") would newly accept it.
  assert.equal(isSafeHref("java\tscript:alert(1)"), false);
  assert.equal(isSafeHref("java\nscript:alert(1)"), false);
});

test("readContexts reads os/runtime/device as separate context cards", () => {
  const contexts = readContexts({
    contexts: {
      os: { name: "Linux", version: "6.8.0" },
      runtime: { name: "CPython", version: "3.12.4" },
      device: { arch: "x86_64" }
    }
  });
  assert.equal(contexts.length, 3);
  const os = contexts.find((c) => c.name === "os");
  assert.deepEqual(os?.fields, [
    { label: "name", value: "Linux" },
    { label: "version", value: "6.8.0" }
  ]);
});

test("readContexts excludes the trace context — RequestTimeline owns that", () => {
  const contexts = readContexts({ contexts: { trace: { trace_id: "abc" }, os: { name: "Linux" } } });
  assert.equal(contexts.some((c) => c.name === "trace"), false);
  assert.equal(contexts.length, 1);
});

test("a context whose value is not an object is skipped, not thrown", () => {
  assert.deepEqual(readContexts({ contexts: { os: "not an object" } }), []);
});

test("a payload with no contexts reads as an empty list", () => {
  assert.deepEqual(readContexts({}), []);
  assert.deepEqual(readContexts(null), []);
});

test("the number of context cards is bounded to the head of wire order", () => {
  const contextsInput = Object.fromEntries(
    Array.from({ length: 50000 }, (_, i) => [`ctx${i}`, { field: "value" }])
  );
  const contexts = readContexts({ contexts: contextsInput });
  assert.equal(contexts.length, MAX_RENDERED_CONTEXTS);
  assert.equal(contexts[0].name, "ctx0");
  assert.equal(contexts[contexts.length - 1].name, `ctx${MAX_RENDERED_CONTEXTS - 1}`);
  assert.equal(
    contexts.some((c) => c.name === "ctx49999"),
    false
  );
});

test("the fields within one context card are bounded to the head of wire order", () => {
  const fieldsInput = Object.fromEntries(Array.from({ length: 200000 }, (_, i) => [`field${i}`, `v${i}`]));
  const contexts = readContexts({ contexts: { os: fieldsInput } });
  const os = contexts.find((c) => c.name === "os");
  assert.equal(os?.fields.length, MAX_RENDERED_CONTEXT_FIELDS);
  assert.equal(os?.fields[0].label, "field0");
  assert.equal(os?.fields[os.fields.length - 1].label, `field${MAX_RENDERED_CONTEXT_FIELDS - 1}`);
});

test("readSdk reads name and version", () => {
  const sdk = readSdk({ sdk: { name: "sentry.python", version: "2.14.0" } });
  assert.deepEqual(sdk, { name: "sentry.python", version: "2.14.0" });
});

test("readSdk is null when the sdk key is absent or empty", () => {
  assert.equal(readSdk({}), null);
  assert.equal(readSdk({ sdk: { name: "", version: "" } }), null);
});

test("readExtra reads primitive and object values", () => {
  const extra = readExtra({ extra: { retries: 3, config: { flag: true } } });
  assert.deepEqual(extra, [
    { key: "retries", value: "3" },
    { key: "config", value: '{"flag":true}' }
  ]);
});

test("readExtra is bounded to the head of wire order, not merely truncated to a count", () => {
  const many = Object.fromEntries(Array.from({ length: 80 }, (_, i) => [`k${i}`, i]));
  const extra = readExtra({ extra: many });
  assert.equal(extra.length, MAX_RENDERED_EXTRA_ENTRIES);
  assert.equal(extra[0].key, "k0");
  assert.equal(extra[extra.length - 1].key, `k${MAX_RENDERED_EXTRA_ENTRIES - 1}`);
  assert.equal(
    extra.some((entry) => entry.key === "k79"),
    false
  );
});

test("readExtra is empty when extra is absent", () => {
  assert.deepEqual(readExtra({}), []);
});

test("readTags reads the object form", () => {
  assert.deepEqual(readTags({ tags: { environment: "production", plan: "pro" } }), [
    { key: "environment", value: "production" },
    { key: "plan", value: "pro" }
  ]);
});

test("readTags reads the array-of-pairs form", () => {
  assert.deepEqual(readTags({ tags: [["environment", "production"]] }), [
    { key: "environment", value: "production" }
  ]);
});

test("a malformed pair is skipped, not thrown", () => {
  assert.deepEqual(readTags({ tags: [["only-one-element"], ["ok", "value"]] }), [{ key: "ok", value: "value" }]);
});

test("readTags is empty when tags is absent", () => {
  assert.deepEqual(readTags({}), []);
});

test("readTags bounds the object form to the head of wire order", () => {
  const many = Object.fromEntries(Array.from({ length: 200000 }, (_, i) => [`tag${i}`, `v${i}`]));
  const tags = readTags({ tags: many });
  assert.equal(tags.length, MAX_RENDERED_TAGS);
  assert.equal(tags[0].key, "tag0");
  assert.equal(tags[tags.length - 1].key, `tag${MAX_RENDERED_TAGS - 1}`);
  assert.equal(
    tags.some((t) => t.key === "tag199999"),
    false
  );
});

test("readTags bounds the array-of-pairs form to the head of wire order", () => {
  const many = Array.from({ length: 200000 }, (_, i) => [`tag${i}`, `v${i}`]);
  const tags = readTags({ tags: many });
  assert.equal(tags.length, MAX_RENDERED_TAGS);
  assert.equal(tags[0].key, "tag0");
  assert.equal(tags[tags.length - 1].key, `tag${MAX_RENDERED_TAGS - 1}`);
  assert.equal(
    tags.some((t) => t.key === "tag199999"),
    false
  );
});

/**
 * The two top-level-or-tag readers take the tags the caller already read, so
 * these helpers hand them the tags of the very payload under test — exactly
 * what `readPayloadFields` does in production.
 */
function transactionIn(payload: unknown): string {
  return readTransaction(payload, readTags(payload));
}

function serverNameIn(payload: unknown): string {
  return readServerName(payload, readTags(payload));
}

test("readTransaction reads the top-level field over a same-named tag", () => {
  assert.equal(transactionIn({ transaction: "GET /orders", tags: { transaction: "stale" } }), "GET /orders");
});

test("readTransaction falls back to the tag when the top-level field is absent", () => {
  assert.equal(transactionIn({ tags: { transaction: "GET /orders" } }), "GET /orders");
});

test("readTransaction is empty when neither the top-level field nor the tag is present", () => {
  assert.equal(transactionIn({}), "");
});

test("readTransaction never throws on a payload that is not an object", () => {
  assert.equal(transactionIn(null), "");
  assert.equal(transactionIn("not an object"), "");
  assert.equal(transactionIn(undefined), "");
});

test("readServerName reads the top-level field over a same-named tag", () => {
  assert.equal(serverNameIn({ server_name: "web-1", tags: { server_name: "stale" } }), "web-1");
});

test("readServerName falls back to the tag when the top-level field is absent", () => {
  assert.equal(serverNameIn({ tags: { server_name: "web-1" } }), "web-1");
});

test("readServerName is empty when neither the top-level field nor the tag is present", () => {
  assert.equal(serverNameIn({}), "");
});

test("readServerName never throws on a payload that is not an object", () => {
  assert.equal(serverNameIn(null), "");
  assert.equal(serverNameIn("not an object"), "");
  assert.equal(serverNameIn(undefined), "");
});

test("readModules reads a name/version map, sorted by name", () => {
  assert.deepEqual(readModules({ modules: { zlib: "1.3", requests: "2.31.0" } }), [
    { name: "requests", version: "2.31.0" },
    { name: "zlib", version: "1.3" }
  ]);
});

test("readModules is empty when modules is absent or not an object", () => {
  assert.deepEqual(readModules({}), []);
  assert.deepEqual(readModules({ modules: "not an object" }), []);
});

test("readModules bounds wire order BEFORE the alphabetical sort, not after", () => {
  // 200 entries whose names sort after "aaa", plus one more entry
  // alphabetically first ("aaa-late") appended LAST in wire order. If the
  // bound were applied after sorting, "aaa-late" would survive (it sorts
  // first); the correct, wire-order-first bound drops it because it arrived
  // after the cap was already full.
  const modulesInput: Record<string, string> = {};
  for (let index = 0; index < MAX_RENDERED_MODULES; index++) {
    modulesInput[`mod${String(index).padStart(4, "0")}`] = "1.0.0";
  }
  modulesInput["aaa-late"] = "9.9.9";

  const modules = readModules({ modules: modulesInput });
  assert.equal(modules.length, MAX_RENDERED_MODULES);
  assert.equal(
    modules.some((m) => m.name === "aaa-late"),
    false
  );
  // What survives is still sorted alphabetically.
  assert.equal(modules[0].name, "mod0000");
});

test("a value longer than the cap is cut short and visibly marked", () => {
  const oversized = "a".repeat(MAX_RENDERED_VALUE_CHARS + 500);

  const [tag] = readTags({ tags: { note: oversized } });

  assert.equal(tag.value, "a".repeat(MAX_RENDERED_VALUE_CHARS) + TRUNCATION_SUFFIX);
});

test("a value at the cap is left exactly as stored", () => {
  const exact = "a".repeat(MAX_RENDERED_VALUE_CHARS);

  const [tag] = readTags({ tags: { note: exact } });

  assert.equal(tag.value, exact);
});

test("an oversized serialised extra value is cut short and visibly marked", () => {
  const [entry] = readExtra({ extra: { blob: { padding: "b".repeat(MAX_RENDERED_VALUE_CHARS + 500) } } });

  assert.equal(entry.value.length, MAX_RENDERED_VALUE_CHARS + TRUNCATION_SUFFIX.length);
  assert.ok(entry.value.endsWith(TRUNCATION_SUFFIX));
});

test("an oversized header value is cut short before it reaches the panel", () => {
  const request = readRequest({ request: { headers: { "x-trace": "c".repeat(MAX_RENDERED_VALUE_CHARS + 1) } } });

  assert.equal(request?.headers[0].value, "c".repeat(MAX_RENDERED_VALUE_CHARS) + TRUNCATION_SUFFIX);
});

test("a context with no name is skipped rather than drawn as an untitled card", () => {
  assert.deepEqual(readContexts({ contexts: { "": { a: "b" }, "   ": { c: "d" } } }), []);
  assert.deepEqual(readContexts({ contexts: { "": { a: "b" }, os: { name: "Linux" } } }), [
    { name: "os", fields: [{ label: "name", value: "Linux" }] }
  ]);
});

test("a truncated URL is never linkified — it points somewhere the payload did not name", () => {
  const long = `https://example.com/${"p".repeat(MAX_RENDERED_VALUE_CHARS)}`;
  const [tag] = readTags({ tags: { url: long } });

  assert.ok(tag.value.endsWith(TRUNCATION_SUFFIX));
  assert.equal(isSafeHref(tag.value), false);
  assert.equal(isSafeHref("https://example.com/short"), true);
});
