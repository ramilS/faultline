import assert from "node:assert/strict";
import { test } from "node:test";

import {
  isThemePreference,
  readStoredPreference,
  resolveTheme,
  writeStoredPreference,
  type PreferenceStore,
  type ResolvedTheme,
  type ThemePreference
} from "./theme.ts";

// Hardcoded rather than imported: importing the constant would assert that the
// module agrees with itself. This pins the key that shipped preferences live under.
const STORAGE_KEY = "faultline.theme";

function storeHolding(value: string | null): PreferenceStore {
  return { getItem: () => value, setItem: () => {} };
}

const deniedStore: PreferenceStore = {
  getItem: () => {
    throw new Error("access denied");
  },
  setItem: () => {
    throw new Error("access denied");
  }
};

// The whole point of the feature: an explicit choice must ignore the OS. The
// two `light`/`dark` rows with a flipped system flag are the regression guard.
const resolutions: ReadonlyArray<[ThemePreference, boolean, ResolvedTheme]> = [
  ["light", false, "light"],
  ["light", true, "light"],
  ["dark", false, "dark"],
  ["dark", true, "dark"],
  ["system", false, "light"],
  ["system", true, "dark"]
];

for (const [preference, prefersDark, expected] of resolutions) {
  test(`resolves ${preference} with systemPrefersDark=${prefersDark} to ${expected}`, () => {
    assert.equal(resolveTheme(preference, prefersDark), expected);
  });
}

for (const preference of ["light", "dark", "system"] as const) {
  test(`accepts ${preference} as a stored preference`, () => {
    assert.equal(isThemePreference(preference), true);
    assert.equal(readStoredPreference(storeHolding(preference)), preference);
  });
}

const rejected: ReadonlyArray<[unknown, string]> = [
  ["blue", "a value from no known version"],
  ["Dark", "wrong case"],
  ["", "empty string"],
  [null, "absent key"],
  [undefined, "undefined"],
  [0, "a number"],
  [{}, "an object"]
];

for (const [value, reason] of rejected) {
  test(`rejects ${JSON.stringify(value)} — ${reason}`, () => {
    assert.equal(isThemePreference(value), false);
  });
}

test("falls back to system when the key is absent", () => {
  assert.equal(readStoredPreference(storeHolding(null)), "system");
});

test("falls back to system when the stored value is unrecognised", () => {
  assert.equal(readStoredPreference(storeHolding("blue")), "system");
});

// Safari with site data disabled throws on the property access, which
// browserStorage() turns into null; these two cover the consequence.
test("falls back to system when there is no storage at all", () => {
  assert.equal(readStoredPreference(null), "system");
});

test("falls back to system when reading throws", () => {
  assert.equal(readStoredPreference(deniedStore), "system");
});

test("writes the exact key and value, once", () => {
  const writes: Array<readonly [string, string]> = [];
  const store: PreferenceStore = {
    getItem: () => null,
    setItem: (key, value) => {
      writes.push([key, value]);
    }
  };

  writeStoredPreference(store, "dark");

  assert.deepEqual(writes, [[STORAGE_KEY, "dark"]]);
});

// A failed write costs persistence and nothing else. If it propagated, clicking
// Dark in a private window would throw out of the click handler.
test("does not throw when writing is denied", () => {
  assert.doesNotThrow(() => writeStoredPreference(deniedStore, "dark"));
});

test("does not throw when there is no storage to write to", () => {
  assert.doesNotThrow(() => writeStoredPreference(null, "dark"));
});
