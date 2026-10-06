import assert from "node:assert/strict";
import { test } from "node:test";

import type { SymbolicatedException, SymbolicatedFrame } from "../../api.ts";
import {
  hasSourceLocation,
  hiddenFrameCount,
  initialFrameFilter,
  isInAppFrame,
  stackTraceText,
  visibleFrames
} from "./frames.ts";

function frame(inApp: boolean, filename = "src/checkout.ts"): SymbolicatedFrame {
  return {
    generated: { function: "render", filename, line: 1, column: 1, in_app: inApp },
    original: null,
    context: []
  };
}

function exception(frames: SymbolicatedFrame[]): SymbolicatedException {
  return { type: "TypeError", value: "boom", frames, cause: null };
}

test("in-app filtering keeps only application frames", () => {
  const frames = [frame(true), frame(false), frame(true)];

  assert.equal(visibleFrames(frames, "in-app").length, 2);
  assert.equal(visibleFrames(frames, "all").length, 3);
  assert.equal(hiddenFrameCount(frames, "in-app"), 1);
  assert.equal(hiddenFrameCount(frames, "all"), 0);
});

test("filtering does not mutate the source array", () => {
  const frames = [frame(true), frame(false)];

  visibleFrames(frames, "in-app");

  assert.equal(frames.length, 2);
  assert.equal(isInAppFrame(frames[1]), false);
});

test("in-app is read from the generated position", () => {
  assert.equal(isInAppFrame(frame(true)), true);
  assert.equal(isInAppFrame(frame(false)), false);
});

test("a frame without a filename has no renderable source location", () => {
  assert.equal(hasSourceLocation(frame(true, "")), false);
  assert.equal(hasSourceLocation(frame(true, "src/a.ts")), true);
});

test("a frame resolved by a source map reports the original location", () => {
  const mapped: SymbolicatedFrame = {
    generated: { function: "a", filename: "", line: 1, column: 1, in_app: true },
    original: { function: "render", filename: "src/a.ts", line: 4, column: 2, in_app: true },
    context: []
  };

  assert.equal(hasSourceLocation(mapped), true);
});

test("a chain with any in-app frame opens filtered", () => {
  const chain = [exception([frame(false)]), exception([frame(false), frame(true)])];

  assert.equal(initialFrameFilter(chain), "in-app");
});

test("a chain of only vendor frames opens unfiltered rather than empty", () => {
  const chain = [exception([frame(false), frame(false)]), exception([frame(false)])];

  assert.equal(initialFrameFilter(chain), "all");
});

test("an empty chain opens unfiltered", () => {
  assert.equal(initialFrameFilter([]), "all");
});

test("renders an exception header and its frame as plain text", () => {
  const chain = [exception([frame(true)])];

  assert.equal(stackTraceText(chain, "all"), "TypeError: boom\n  at render (src/checkout.ts:1:1)");
});

test("omits the location from a frame that has none", () => {
  const chain = [exception([frame(true, "")])];

  assert.equal(stackTraceText(chain, "all"), "TypeError: boom\n  at render");
});

test("omits the colon and value when the exception carries none", () => {
  const bare: SymbolicatedException = { type: "Error", value: "", frames: [], cause: null };

  assert.equal(stackTraceText([bare], "all"), "Error");
});

test("filters vendor frames out of the text under the in-app filter", () => {
  const chain = [exception([frame(true), frame(false)])];

  assert.equal(stackTraceText(chain, "in-app"), "TypeError: boom\n  at render (src/checkout.ts:1:1)");
});

test("labels every exception after the first as a cause", () => {
  const chain = [exception([frame(true)]), exception([frame(true)])];

  assert.equal(
    stackTraceText(chain, "all"),
    "TypeError: boom\n  at render (src/checkout.ts:1:1)\n\nCaused by: TypeError: boom\n  at render (src/checkout.ts:1:1)"
  );
});

test("an empty chain renders no text", () => {
  assert.equal(stackTraceText([], "all"), "");
});
