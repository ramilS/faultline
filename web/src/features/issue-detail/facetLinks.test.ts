import assert from "node:assert/strict";
import { test } from "node:test";

import { streamPathForBucket, streamPathForEnvironment, streamPathForRelease, streamPathForTag } from "./facetLinks.ts";

test("a tag facet links to the stream filtered by that tag", () => {
  const path = streamPathForTag("web", "browser", "safari");
  assert.equal(path, "/web/issues?tag=browser%3Asafari&statsPeriod=all");
});

test("a wildcard in a facet value is escaped, not sent as an operator", () => {
  // A stored value of "v2*beta" is data. Unescaped it becomes a wildcard and
  // silently matches issues that have nothing to do with the one being read.
  const path = streamPathForRelease("web", "v2*beta");
  assert.ok(path.includes("release=v2%5C*beta"), path);
});

test("a leading bang in a facet value is escaped", () => {
  const path = streamPathForEnvironment("web", "!staging");
  assert.ok(path.includes("environment=%5C%21staging"), path);
});

test("a facet link opens an all-time window", () => {
  // The stream defaults to 14d. A facet on an issue whose last event is older
  // than that would link to an empty stream, which reads as "this filter finds
  // nothing" rather than "you are looking outside the window".
  assert.ok(streamPathForTag("web", "browser", "safari").includes("statsPeriod=all"));
});

test("a facet link keeps the default status filter rather than widening it", () => {
  const path = streamPathForTag("web", "browser", "safari");
  assert.equal(path.includes("status="), false);
});

test("a bucket links to an absolute window covering exactly that bucket", () => {
  const path = streamPathForBucket("web", "2026-08-11T14:00:00Z", "2026-08-11T15:00:00Z");
  assert.ok(path.includes("start=2026-08-11T14%3A00%3A00Z"), path);
  assert.ok(path.includes("end=2026-08-11T15%3A00%3A00Z"), path);
  // start/end win over statsPeriod on the wire, so writing both would be
  // ambiguous to a reader even though the parser resolves it.
  assert.equal(path.includes("statsPeriod="), false);
});
