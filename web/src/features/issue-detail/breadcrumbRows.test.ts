import assert from "node:assert/strict";
import { test } from "node:test";

import { buildBreadcrumbRows } from "./breadcrumbRows.ts";
import type { Breadcrumb } from "./payloadReaders.ts";

const CLOSING_EVENT = { timestamp: "2026-08-11T10:05:00Z", level: "error", message: "TypeError: boom" };

function crumb(overrides: Partial<Breadcrumb> = {}): Breadcrumb {
  return { timestamp: null, category: "", level: "info", message: "", data: undefined, ...overrides };
}

test("orders breadcrumbs oldest first and appends a closing row for the exception", () => {
  const rows = buildBreadcrumbRows(
    [crumb({ message: "first" }), crumb({ message: "second" })],
    CLOSING_EVENT
  );

  assert.equal(rows.length, 3);
  assert.equal(rows[0].message, "first");
  assert.equal(rows[0].isClosingRow, false);
  assert.equal(rows[1].message, "second");
  assert.equal(rows[2].message, "TypeError: boom");
  assert.equal(rows[2].isClosingRow, true);
  assert.equal(rows[2].level, "error");
});

test("the closing row is labelled as an exception, not a breadcrumb category", () => {
  const rows = buildBreadcrumbRows([crumb()], CLOSING_EVENT);
  assert.equal(rows[rows.length - 1].category, "exception");
});

test("a message that is a safe http(s) url becomes a link target", () => {
  const rows = buildBreadcrumbRows([crumb({ message: "https://example.com/cart" })], CLOSING_EVENT);
  assert.equal(rows[0].messageHref, "https://example.com/cart");
});

test("a message that looks dangerous or is not a url renders as text, not a link", () => {
  const rows = buildBreadcrumbRows(
    [
      crumb({ message: "javascript:alert(1)" }),
      crumb({ message: "GET /cart" }),
      crumb({ message: "" })
    ],
    CLOSING_EVENT
  );
  assert.equal(rows[0].messageHref, null);
  assert.equal(rows[1].messageHref, null);
  assert.equal(rows[2].messageHref, null);
});

test("row data passes through untouched so the disclosure can render it", () => {
  const rows = buildBreadcrumbRows([crumb({ data: { retries: 3 } })], CLOSING_EVENT);
  assert.deepEqual(rows[0].data, { retries: 3 });
});

test("the closing row never carries a data disclosure", () => {
  const rows = buildBreadcrumbRows([crumb()], CLOSING_EVENT);
  assert.equal(rows[rows.length - 1].data, undefined);
});

test("keys are stable and unique across breadcrumb rows and the closing row", () => {
  const rows = buildBreadcrumbRows([crumb(), crumb()], CLOSING_EVENT);
  const keys = rows.map((row) => row.key);
  assert.equal(new Set(keys).size, keys.length);
});
