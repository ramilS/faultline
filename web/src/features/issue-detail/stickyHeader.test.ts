import assert from "node:assert/strict";
import { test } from "node:test";

import { pinnedHeightValue, SECTION_NAV_TOP, SECTION_SCROLL_MARGIN } from "./stickyHeader.ts";

test("a measured height is published in pixels", () => {
  assert.equal(pinnedHeightValue(212), "212px");
});

test("an unlaid-out band publishes nothing, so the fallback survives", () => {
  assert.equal(pinnedHeightValue(0), null);
  assert.equal(pinnedHeightValue(-1), null);
  assert.equal(pinnedHeightValue(Number.NaN), null);
});

test("the section bar sticks under the pinned header, else under the shell's top bar", () => {
  assert.equal(SECTION_NAV_TOP, "var(--issue-header-height, var(--app-top-bar-height, 0px))");
});

test("a section's scroll margin clears the header and the section bar", () => {
  assert.equal(
    SECTION_SCROLL_MARGIN,
    "calc(var(--issue-header-height, var(--app-top-bar-height, 0px)) + var(--issue-section-nav-height, 2.75rem) + 0.5rem)"
  );
});
