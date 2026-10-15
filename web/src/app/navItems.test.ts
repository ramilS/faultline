import assert from "node:assert/strict";
import { test } from "node:test";

import { globalNavItems, projectNavItems } from "./navItems.ts";

test("the account entry appears for both roles; the users entry is owner-only", () => {
  assert.deepEqual(
    globalNavItems("member").map((item) => item.to),
    ["/issues", "/projects", "/settings/account"]
  );
  assert.deepEqual(
    globalNavItems("owner").map((item) => item.to),
    ["/issues", "/projects", "/settings/account", "/settings/users", "/settings/groups", "/settings/data-scrubbing"]
  );
});

// With no grandfathering group, a member reaches nothing until an owner
// builds one for them — so a member must never see a link to the one screen
// that builds them (no link to a screen the server will 403).
test("the groups entry is owner-only", () => {
  assert.equal(
    globalNavItems("member").some((item) => item.to === "/settings/groups"),
    false
  );
  assert.equal(
    globalNavItems("owner").some((item) => item.to === "/settings/groups"),
    true
  );
});

test("the instance-wide data scrubbing entry is owner-only", () => {
  assert.equal(
    globalNavItems("member").some((item) => item.to === "/settings/data-scrubbing"),
    false
  );
  assert.equal(
    globalNavItems("owner").some((item) => item.to === "/settings/data-scrubbing"),
    true
  );
});

// Notification channels are owner-only on the server, listing included: a
// webhook URL can itself be a credential.
test("the project notifications entry is owner-only and sits before General", () => {
  assert.equal(
    projectNavItems("shop", "member").some((item) => item.to === "/shop/settings/notifications"),
    false
  );
  assert.deepEqual(
    projectNavItems("shop", "owner").slice(-2).map((item) => item.to),
    ["/shop/settings/notifications", "/shop/settings/general"]
  );
});
