import assert from "node:assert/strict";
import { test } from "node:test";

import type { IgnoreUntil } from "../api.ts";
import { ignoreUntilLabel } from "./ignoreUntil.ts";

const NOW = new Date("2026-11-09T12:00:00Z");
const formatDate = (iso: string) => `<${iso}>`;

const cases: ReadonlyArray<{ name: string; ignore: IgnoreUntil; full: string; compact: string }> = [
  {
    name: "events, short of the target",
    ignore: { kind: "events", until: null, count: 100, so_far: 37 },
    full: "Ignored until 100 more events · 37 so far",
    compact: "37 of 100 events"
  },
  {
    name: "events, one short",
    ignore: { kind: "events", until: null, count: 100, so_far: 99 },
    full: "Ignored until 100 more events · 99 so far",
    compact: "99 of 100 events"
  },
  {
    name: "users",
    ignore: { kind: "users", until: null, count: 10, so_far: 0 },
    full: "Ignored until 10 more users · 0 so far",
    compact: "0 of 10 users"
  },
  {
    name: "a duration still running",
    ignore: { kind: "duration", until: "2026-11-10T12:00:00Z", count: null, so_far: null },
    full: "Ignored until <2026-11-10T12:00:00Z>",
    compact: "until <2026-11-10T12:00:00Z>"
  },
  {
    name: "a duration that ended while the issue stayed quiet",
    ignore: { kind: "duration", until: "2026-11-08T12:00:00Z", count: null, so_far: null },
    full: "Ignored · comes back on its next event",
    compact: "back on next event"
  }
];

for (const { name, ignore, full, compact } of cases) {
  test(`ignoreUntilLabel: ${name}`, () => {
    assert.equal(ignoreUntilLabel(ignore, "full", NOW, formatDate), full);
    assert.equal(ignoreUntilLabel(ignore, "compact", NOW, formatDate), compact);
  });
}
