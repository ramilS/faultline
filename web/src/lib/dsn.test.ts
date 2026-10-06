import assert from "node:assert/strict";
import { test } from "node:test";

import { buildDsn } from "./dsn.ts";

const key = { public_key: "a".repeat(64), project_id: 42 };

test("builds a DSN in the form every Sentry SDK expects", () => {
  assert.equal(buildDsn(key, "https://faultline.example.com"), `https://${"a".repeat(64)}@faultline.example.com/42`);
});

test("keeps a non-default port, which local development always has", () => {
  assert.equal(buildDsn(key, "http://localhost:8080"), `http://${"a".repeat(64)}@localhost:8080/42`);
});

test("ignores any path on the origin", () => {
  assert.equal(buildDsn(key, "https://faultline.example.com/settings/keys"), `https://${"a".repeat(64)}@faultline.example.com/42`);
});

test("throws on an unusable origin rather than rendering a broken DSN", () => {
  assert.throws(() => buildDsn(key, "not a url"));
});
