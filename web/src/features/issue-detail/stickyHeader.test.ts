import assert from "node:assert/strict";
import { test } from "node:test";

import { SECTION_SCROLL_MARGIN, stickyHeaderHeightValue } from "./stickyHeader.ts";

test("a measured height is published in pixels", () => {
  assert.equal(stickyHeaderHeightValue(212), "212px");
});

test("an unlaid-out header publishes nothing, so the fallback survives", () => {
  assert.equal(stickyHeaderHeightValue(0), null);
  assert.equal(stickyHeaderHeightValue(-1), null);
  assert.equal(stickyHeaderHeightValue(Number.NaN), null);
});

test("a section's scroll margin falls back to a real offset when nothing was measured", () => {
  assert.equal(SECTION_SCROLL_MARGIN, "calc(var(--issue-header-height, 5rem) + 0.5rem)");
});
