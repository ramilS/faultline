import assert from "node:assert/strict";
import { test } from "node:test";

import { isHighlightable, tokenizeSourceLines, type SourceToken } from "./sourceSyntax.ts";

function kindsOf(line: readonly SourceToken[]): string[] {
  return line.filter((token) => token.text.trim() !== "").map((token) => `${token.kind}:${token.text}`);
}

test("isHighlightable: JavaScript-family files, however the extension is cased", () => {
  for (const name of ["app.js", "src/App.TSX", "lib/index.mjs", "a.cjs", "view.jsx", "x.ts"]) {
    assert.equal(isHighlightable(name), true, name);
  }
});

test("isHighlightable: ignores a query string or fragment on a bundled asset URL", () => {
  assert.equal(isHighlightable("https://cdn.example.com/app.js?v=3"), true);
  assert.equal(isHighlightable("https://cdn.example.com/app.js#L4"), true);
});

test("isHighlightable: leaves other languages plain rather than mis-colouring them", () => {
  for (const name of ["Exporter.java", "Main.kt", "view.swift", "script.py", "", "Makefile", "app.js.map"]) {
    assert.equal(isHighlightable(name), false, name);
  }
});

test("tokenizeSourceLines: returns one list of tokens per input line", () => {
  const lines = ["const a = 1;", "", "return a;"];

  assert.equal(tokenizeSourceLines(lines).length, lines.length);
});

test("tokenizeSourceLines: rejoining a line's tokens gives back exactly the source text", () => {
  const lines = ["  const total = await charge({ id: order.id })", "\tif (a < b && c > d) {", "  // note: <b>bold</b>"];

  const tokenised = tokenizeSourceLines(lines);

  tokenised.forEach((line, index) => {
    assert.equal(line.map((token) => token.text).join(""), lines[index]);
  });
});

test("tokenizeSourceLines: tells keywords, strings and comments apart", () => {
  const [line] = tokenizeSourceLines(['return "done"; // finished']);

  const kinds = kindsOf(line);
  assert.ok(kinds.includes("keyword:return"), kinds.join(" | "));
  assert.ok(kinds.some((kind) => kind.startsWith("string:")), kinds.join(" | "));
  assert.ok(kinds.includes("comment:// finished"), kinds.join(" | "));
});

test("tokenizeSourceLines: a block comment that spans lines is a comment on every line it covers", () => {
  const [first, second, third] = tokenizeSourceLines(["/* starts here", "   still inside", "   ends */ run()"]);

  assert.ok(kindsOf(first).every((kind) => kind.startsWith("comment:")), kindsOf(first).join(" | "));
  assert.ok(kindsOf(second).every((kind) => kind.startsWith("comment:")), kindsOf(second).join(" | "));
  assert.ok(kindsOf(third)[0].startsWith("comment:"), kindsOf(third).join(" | "));
});

test("tokenizeSourceLines: markup in source stays text, never something a renderer could interpret", () => {
  const hostile = '<img src=x onerror="alert(1)">';

  const [line] = tokenizeSourceLines([`const x = "${hostile}";`]);

  assert.equal(line.map((token) => token.text).join(""), `const x = "${hostile}";`);
  assert.ok(line.every((token) => typeof token.text === "string"));
});

test("tokenizeSourceLines: no lines, no tokens", () => {
  assert.deepEqual(tokenizeSourceLines([]), []);
});
