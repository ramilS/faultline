import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";

import { PLATFORMS, platformGroups, platformLabel } from "./platforms.ts";

const GO_PLATFORM_CONSTANT = /^\s*Platform\w+\s*=\s*"([^"]+)"/gm;

function goAllowList(): string[] {
  const source = readFileSync(new URL("../../../../internal/project/model.go", import.meta.url), "utf8");
  return Array.from(source.matchAll(GO_PLATFORM_CONSTANT), (match) => match[1]);
}

test("the picker offers exactly the platforms the backend accepts", () => {
  const backend = goAllowList();

  assert.notEqual(backend.length, 0, "no Platform constants found in internal/project/model.go");
  assert.deepEqual(PLATFORMS.map((platform) => platform.value).sort(), backend.sort());
});

test("javascript stays the first option because it is the picker default", () => {
  assert.equal(PLATFORMS[0].value, "javascript");
});

test("platformLabel returns the picker label for every platform", () => {
  for (const { value, label } of PLATFORMS) {
    assert.equal(platformLabel(value), label);
  }
  assert.equal(platformLabel("kotlin-multiplatform"), "Kotlin Multiplatform");
  assert.equal(platformLabel("jvm-spring"), "JVM · Spring");
});

test("platformLabel shows an unrecognised stored value as it is", () => {
  assert.equal(platformLabel("cobol"), "cobol");
});

test("every platform belongs to a named group", () => {
  for (const { value, group } of PLATFORMS) {
    assert.notEqual(group.trim(), "", `${value} has no group`);
  }
});

test("platformGroups keeps each group in one contiguous run, in catalogue order", () => {
  const groups = platformGroups();

  assert.equal(new Set(groups.map((entry) => entry.group)).size, groups.length, "a group is split in two runs");
  assert.deepEqual(
    groups.flatMap((entry) => entry.platforms.map((platform) => platform.value)),
    PLATFORMS.map((platform) => platform.value)
  );
});

test("platformGroups puts NestJS under JavaScript and Ktor under JVM", () => {
  const byGroup = new Map(platformGroups().map((entry) => [entry.group, entry.platforms.map((p) => p.value)]));

  assert.deepEqual(byGroup.get("JavaScript"), ["javascript", "nestjs"]);
  assert.ok(byGroup.get("JVM")?.includes("jvm-ktor"));
});
