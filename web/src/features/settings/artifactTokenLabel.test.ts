import assert from "node:assert/strict";
import { test } from "node:test";

import { tokenPrefixLabel } from "./artifactTokenLabel.ts";

test("a short prefix is shown whole, marked as a prefix", () => {
  assert.equal(tokenPrefixLabel("fla_ab12"), "fla_ab12…");
});

test("a long prefix is cut to what is enough to tell tokens apart", () => {
  assert.equal(tokenPrefixLabel("s6hXKAHeboYMwGp6qasSQRaS0jcAcNEQxxF4M3RU_UM"), "s6hXKAHeboYM…");
});

test("a prefix exactly at the limit is not cut", () => {
  assert.equal(tokenPrefixLabel("abcdefghijkl"), "abcdefghijkl…");
});
