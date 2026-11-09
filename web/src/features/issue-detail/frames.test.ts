import assert from "node:assert/strict";
import { test } from "node:test";

import type { SourceLinkSetting, SymbolicatedException, SymbolicatedFrame } from "../../api.ts";
import {
  defaultExpandedFrame,
  frameRows,
  hasSourceLocation,
  initialFrameFilter,
  isInAppFrame,
  sourceLink,
  stackTraceText
} from "./frames.ts";

function frame(inApp: boolean, filename = "src/checkout.ts", fn = "render"): SymbolicatedFrame {
  return {
    generated: { function: fn, filename, line: 1, column: 1, in_app: inApp },
    original: null,
    context: [],
    vars: null
  };
}

function withSource(base: SymbolicatedFrame): SymbolicatedFrame {
  return { ...base, context: [{ line: 1, text: "boom()", focus: true }] };
}

function exception(frames: SymbolicatedFrame[]): SymbolicatedException {
  return { type: "TypeError", value: "boom", frames, cause: null };
}

test("folding does not mutate the source array", () => {
  const frames = [frame(true), frame(false)];

  frameRows(frames, "in-app");

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
    context: [],
    vars: null
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

test("marks each folded vendor run in its place under the in-app filter", () => {
  const chain = [exception([frame(false), frame(false), frame(true), frame(false)])];

  assert.equal(
    stackTraceText(chain, "in-app"),
    "TypeError: boom\n  … 2 vendor frames\n  at render (src/checkout.ts:1:1)\n  … 1 vendor frame"
  );
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

test("the in-app view folds each run of consecutive vendor frames into one row, in place", () => {
  const frames = [frame(false, "a", "v0"), frame(true), frame(false, "a", "v2"), frame(false, "a", "v3"), frame(true)];

  const rows = frameRows(frames, "in-app").map((row) =>
    row.kind === "frame" ? `frame ${row.index}` : `run ${row.start}+${row.frames.length}`
  );

  assert.deepEqual(rows, ["run 0+1", "frame 1", "run 2+2", "frame 4"]);
});

test("the full view lists every frame at its own position", () => {
  const frames = [frame(false), frame(true), frame(false)];

  assert.deepEqual(
    frameRows(frames, "all").map((row) => (row.kind === "frame" ? row.index : -1)),
    [0, 1, 2]
  );
});

test("a trailing vendor run is kept rather than dropped", () => {
  const rows = frameRows([frame(true), frame(false), frame(false)], "in-app");

  assert.deepEqual(rows.at(-1), { kind: "vendor-run", start: 1, frames: [frame(false), frame(false)] });
});

test("a trace opens on the innermost in-app frame that has source", () => {
  const frames = [withSource(frame(false)), frame(true), withSource(frame(true)), withSource(frame(true))];

  assert.equal(defaultExpandedFrame(frames), 2);
});

test("a trace with no in-app source opens on the innermost frame that has any", () => {
  assert.equal(defaultExpandedFrame([frame(false), withSource(frame(false)), frame(true)]), 1);
});

test("a frame that sent local variables opens like one with source", () => {
  const withVars: SymbolicatedFrame = { ...frame(true), vars: { user_id: 42 } };

  assert.equal(defaultExpandedFrame([frame(false), frame(true), withVars]), 2);
});

test("a trace with no source anywhere opens nothing", () => {
  assert.equal(defaultExpandedFrame([frame(true), frame(false)]), null);
});

const GITHUB = "https://github.com/acme/shop/blob/main/{path}#L{line}";

function located(filename: string, line: number, inApp = true, original: { filename: string; line: number } | null = null): SymbolicatedFrame {
  return {
    generated: { function: "load", filename, line, column: 1, in_app: inApp },
    original: original === null ? null : { function: "load", filename: original.filename, line: original.line, column: 1, in_app: inApp },
    context: [],
    vars: null
  };
}

function setting(template: string, stackRoot = "", sourceRoot = ""): SourceLinkSetting {
  return { source_link_template: template, source_stack_root: stackRoot, source_root: sourceRoot };
}

const SOURCE_LINK_CASES: ReadonlyArray<{
  name: string;
  frame: SymbolicatedFrame;
  release: string | null;
  setting: SourceLinkSetting;
  expected: string | null;
}> = [
  {
    name: "stack root removed and source root prepended",
    frame: located("/app/shop/cart.py", 88),
    release: null,
    setting: setting(GITHUB, "/app/", "src/"),
    expected: "https://github.com/acme/shop/blob/main/src/shop/cart.py#L88"
  },
  {
    name: "a path outside the stack root is used as is after the source root",
    frame: located("/usr/lib/python3/json.py", 12),
    release: null,
    setting: setting(GITHUB, "/app/", "src/"),
    expected: "https://github.com/acme/shop/blob/main/src/usr/lib/python3/json.py#L12"
  },
  {
    name: "a library frame outside the app gets no link",
    frame: located("/usr/lib/python3/json.py", 12, false),
    release: null,
    setting: setting(GITHUB, "/app/", "src/"),
    expected: null
  },
  {
    name: "a stack root matches whole directories only",
    frame: located("/application/cart.py", 7),
    release: null,
    setting: setting(GITHUB, "/app"),
    expected: "https://github.com/acme/shop/blob/main/application/cart.py#L7"
  },
  {
    name: "no roots keeps the reported path",
    frame: located("shop/cart.py", 88),
    release: null,
    setting: setting(GITHUB),
    expected: "https://github.com/acme/shop/blob/main/shop/cart.py#L88"
  },
  {
    name: "a source root without a trailing slash still separates the path",
    frame: located("/app/shop/cart.py", 88),
    release: null,
    setting: setting(GITHUB, "/app", "src"),
    expected: "https://github.com/acme/shop/blob/main/src/shop/cart.py#L88"
  },
  {
    name: "the original position wins over the generated one",
    frame: located("https://cdn.example/assets/app.js", 1, true, { filename: "src/checkout.ts", line: 42 }),
    release: null,
    setting: setting(GITHUB),
    expected: "https://github.com/acme/shop/blob/main/src/checkout.ts#L42"
  },
  {
    name: "the release is substituted percent-encoded",
    frame: located("shop/cart.py", 88),
    release: "shop@1.2.3+build/7",
    setting: setting("https://git.example/shop/-/blob/{release}/{path}#L{line}"),
    expected: "https://git.example/shop/-/blob/shop%401.2.3%2Bbuild%2F7/shop/cart.py#L88"
  },
  {
    name: "a template using the release gives no link for an event without one",
    frame: located("shop/cart.py", 88),
    release: null,
    setting: setting("https://git.example/shop/-/blob/{release}/{path}"),
    expected: null
  },
  {
    name: "a template without the release ignores a missing release",
    frame: located("shop/cart.py", 88),
    release: null,
    setting: setting("https://git.example/shop/-/blob/main/{path}"),
    expected: "https://git.example/shop/-/blob/main/shop/cart.py"
  },
  {
    name: "path segments are percent-encoded but keep their slashes",
    frame: located("/app/my shop/#cart?.py", 3),
    release: null,
    setting: setting(GITHUB, "/app/"),
    expected: "https://github.com/acme/shop/blob/main/my%20shop/%23cart%3F.py#L3"
  },
  {
    name: "every placeholder occurrence is substituted",
    frame: located("a.py", 5),
    release: null,
    setting: setting("https://git.example/{path}?file={path}&l={line}#L{line}"),
    expected: "https://git.example/a.py?file=a.py&l=5#L5"
  },
  {
    name: "a setting that is not configured gives no link",
    frame: located("/app/shop/cart.py", 88),
    release: "1.0",
    setting: setting("", "/app/", "src/"),
    expected: null
  },
  {
    name: "a frame without a file gives no link",
    frame: located("", 88),
    release: null,
    setting: setting(GITHUB),
    expected: null
  },
  {
    name: "a frame without a line gives no link",
    frame: located("shop/cart.py", 0),
    release: null,
    setting: setting(GITHUB),
    expected: null
  },
  {
    name: "a template that is not http(s) never becomes a link",
    frame: located("shop/cart.py", 88),
    release: null,
    setting: setting("javascript:alert(1)//{path}"),
    expected: null
  }
];

for (const testCase of SOURCE_LINK_CASES) {
  test(`sourceLink: ${testCase.name}`, () => {
    assert.equal(sourceLink(testCase.frame, testCase.release, testCase.setting), testCase.expected);
  });
}
