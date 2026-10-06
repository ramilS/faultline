import assert from "node:assert/strict";
import { test } from "node:test";

import { projectsEmptyState } from "./projectsEmptyState.ts";

test("offers creation to an owner with no projects", () => {
  const state = projectsEmptyState("owner");
  assert.equal(state.canCreate, true);
});

// With no grandfathering group, an empty list is the normal first experience
// for a new member. The owner copy invites them to create a project, which the
// server will refuse — so the two roles must not share one empty state.
test("tells a member with no access to ask an owner, and offers no create action", () => {
  const state = projectsEmptyState("member");
  assert.equal(state.canCreate, false);
  assert.match(state.title, /access/i);
  assert.match(state.description, /owner/i);
});

test("the two roles do not share copy", () => {
  assert.notEqual(projectsEmptyState("owner").title, projectsEmptyState("member").title);
});
