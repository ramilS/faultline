import assert from "node:assert/strict";
import { test } from "node:test";

import { projectSlugFromPath, slugFromName } from "./projectSlug.ts";

const cases: ReadonlyArray<[string, string | null, string]> = [
  ["/faultline/issues", "faultline", "stream"],
  ["/faultline/issues/123", "faultline", "issue detail"],
  ["/faultline/settings/keys", "faultline", "settings"],
  ["/my-app-2/issues", "my-app-2", "slug with digits and dashes"],
  ["/projects", null, "the projects index is not a project"],
  ["/login", null, "login is not a project"],
  ["/", null, "root"],
  ["", null, "empty path"],
  ["/faultline", "faultline", "bare project path"]
];

for (const [path, expected, reason] of cases) {
  test(`${JSON.stringify(path)} -> ${JSON.stringify(expected)} (${reason})`, () => {
    assert.equal(projectSlugFromPath(path), expected);
  });
}

test("reserved segments never resolve to a project", () => {
  // Otherwise the shell would show "Ingest keys" for /projects and link to
  // /projects/settings/keys, which does not exist.
  for (const reserved of ["/projects", "/projects/", "/login", "/login?next=/x"]) {
    assert.equal(projectSlugFromPath(reserved), null, reserved);
  }
});

test("a trailing slash does not change the slug", () => {
  assert.equal(projectSlugFromPath("/faultline/"), "faultline");
});

const slugCases: ReadonlyArray<[string, string, string]> = [
  ["Shop App", "shop-app", "spaces become hyphens"],
  ["  Shop   App  ", "shop-app", "runs of separators collapse and edges trim"],
  ["Shop_App v2.0", "shop-app-v2-0", "punctuation is a separator"],
  ["Café Résumé", "cafe-resume", "diacritics are stripped, not dropped with their letter"],
  ["--Shop--", "shop", "no leading or trailing hyphen, which the server rejects"],
  ["Магазин", "", "a name with no Latin letters yields nothing rather than a guess"],
  ["", "", "empty"]
];

for (const [name, expected, reason] of slugCases) {
  test(`slugFromName(${JSON.stringify(name)}) -> ${JSON.stringify(expected)} (${reason})`, () => {
    assert.equal(slugFromName(name), expected);
  });
}

test("slugFromName stays within the server's 64 character limit without ending on a hyphen", () => {
  const slug = slugFromName(`${"a".repeat(63)} b`);

  assert.ok(slug.length <= 64);
  assert.ok(!slug.endsWith("-"));
});
