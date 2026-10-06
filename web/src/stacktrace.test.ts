import { test } from "node:test";
import assert from "node:assert/strict";

import { displayFunction, displayLocation, displayPosition, flattenCauses } from "./stacktrace.ts";
import type { SymbolicatedException, SymbolicatedFrame } from "./api.ts";

const mappedFrame: SymbolicatedFrame = {
  generated: { function: "a", filename: "https://cdn.test/app.js", line: 1, column: 481, in_app: true },
  original: { function: "checkout", filename: "src/checkout.ts", line: 42, column: 9, in_app: true },
  context: [],
};

const unmappedFrame: SymbolicatedFrame = {
  generated: { function: "b", filename: "https://cdn.test/app.js", line: 1, column: 481, in_app: false },
  original: null,
  context: [],
};

test("prefers original source locations and preserves generated fallback", () => {
  assert.equal(displayLocation(mappedFrame), "src/checkout.ts:42:9");
  assert.equal(displayLocation(unmappedFrame), "https://cdn.test/app.js:1:481");
});

// A JVM frame has a line but no column; the server sends column 0 for it, and
// "Cart.java:102:0" would read as if the column were real.
test("omits a zero column from the location", () => {
  const jvmFrame: SymbolicatedFrame = {
    generated: { function: "c", filename: "SourceFile", line: 12, column: 0, in_app: true },
    original: { function: "com.acme.shop.Cart.load", filename: "Cart.java", line: 102, column: 0, in_app: true },
    context: [],
  };
  assert.equal(displayLocation(jvmFrame), "Cart.java:102");
  assert.equal(displayPosition(jvmFrame.generated), "SourceFile:12");
  assert.equal(displayPosition(mappedFrame.generated), "https://cdn.test/app.js:1:481");
});

test("prefers original function name and falls back to generated", () => {
  assert.equal(displayFunction(mappedFrame), "checkout");
  assert.equal(displayFunction(unmappedFrame), "b");

  const emptyOriginalFunction: SymbolicatedFrame = {
    ...mappedFrame,
    original: { ...mappedFrame.original!, function: "" },
  };
  assert.equal(displayFunction(emptyOriginalFunction), "a");
});

test("flattens the cause chain in received order", () => {
  const root: SymbolicatedException = {
    type: "TypeError",
    value: "outer",
    frames: [],
    cause: {
      type: "RangeError",
      value: "middle",
      frames: [],
      cause: { type: "Error", value: "root", frames: [], cause: null },
    },
  };

  const chain = flattenCauses(root);
  assert.deepEqual(chain.map((exception) => exception.type), ["TypeError", "RangeError", "Error"]);
  assert.deepEqual(chain.map((exception) => exception.value), ["outer", "middle", "root"]);
});

test("flattens a single exception without a cause", () => {
  const only: SymbolicatedException = { type: "Error", value: "solo", frames: [], cause: null };
  assert.deepEqual(flattenCauses(only), [only]);
});
