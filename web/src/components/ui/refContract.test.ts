import assert from "node:assert/strict";
import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";

/**
 * Guards the ref path that `asChild` depends on, for components this suite
 * cannot exercise: `button.test.tsx` proves the consequence on `Button` — the
 * only `components/ui` export used under an `asChild` trigger today — but a DOM
 * test only covers a trigger someone thought to write one for.
 *
 * Radix routes every `asChild` trigger's ref through `Slot` into the element it
 * wraps, and a Popper-backed layer uses that ref as its anchor. On React 19
 * `ref` is an ordinary prop, so it arrives inside `...props` and reaches the
 * element for free — unless the component drops the spread. That is the whole
 * failure mode now, and it is silent: an unanchored popper is parked at
 * `translate(0, -200%)`, off-screen, and reads as a dead click.
 *
 * `forwardRef` is checked from the other side. It is deprecated on React 19 and
 * upstream shadcn/ui does not use it, so any reappearance means a file has been
 * hand-edited away from upstream — the divergence that caused this incident in
 * the first place, when the project was on 18 and needed the wrapper.
 *
 * Deliberately textual: the alternative is importing `.tsx`, which the runner
 * this suite uses cannot transform. It is a coarse early warning for the next
 * component copied in, not a substitute for a DOM test.
 */

const UI_DIRECTORY = join(import.meta.dirname);
const SOURCE_ROOT = join(import.meta.dirname, "..", "..");

/** An `asChild` trigger and everything it slots, conditionals included. */
const AS_CHILD_BLOCK = /<([A-Z]\w*(?:Trigger|Anchor))\s+asChild[^>]*>([\s\S]*?)<\/\1>/g;

/** Counted separately so a block this file cannot parse fails loudly. */
const AS_CHILD_OPENING = /<[A-Z]\w*(?:Trigger|Anchor)\s+asChild/g;

const JSX_ELEMENT = /<([A-Z]\w*)/g;

function uiSourceFiles(): { file: string; source: string }[] {
  return readdirSync(UI_DIRECTORY)
    .filter((name) => name.endsWith(".tsx") && !name.endsWith(".test.tsx"))
    .map((file) => ({ file, source: readFileSync(join(UI_DIRECTORY, file), "utf8") }));
}

function tsxFilesUnder(directory: string): string[] {
  return readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    const path = join(directory, entry.name);
    if (entry.isDirectory()) {
      return tsxFilesUnder(path);
    }
    return entry.name.endsWith(".tsx") && !entry.name.endsWith(".test.tsx") ? [path] : [];
  });
}

/** Maps every component defined in `components/ui` to whether it spreads props. */
function uiComponentDeclarations(): Map<string, { file: string; spreadsProps: boolean }> {
  const declarations = new Map<string, { file: string; spreadsProps: boolean }>();

  for (const { file, source } of uiSourceFiles()) {
    // Both declaration forms in use: `function Button(` and `const Toaster = (`.
    for (const body of source.split(/^(?=(?:function|const) [A-Z])/m)) {
      const declared = /^(?:function ([A-Z]\w*)\(|const ([A-Z]\w*) = \()/.exec(body);
      if (declared === null) {
        continue;
      }
      const name = declared[1] ?? declared[2];
      declarations.set(name, { file, spreadsProps: body.includes("{...props}") });
    }
  }

  return declarations;
}

test("no components/ui file reintroduces forwardRef", () => {
  const offenders = uiSourceFiles()
    .filter(({ source }) => source.includes("forwardRef"))
    .map(({ file }) => file);

  assert.deepEqual(
    offenders,
    [],
    `forwardRef is deprecated on React 19 and absent upstream; these files diverge:\n${offenders.join("\n")}`
  );
});

test("every components/ui child of an asChild trigger spreads its props", () => {
  const declarations = uiComponentDeclarations();
  assert.ok(declarations.size > 0, "found no components/ui declarations to check");

  const offenders: string[] = [];
  const unparsed: string[] = [];
  let triggers = 0;

  for (const file of tsxFilesUnder(SOURCE_ROOT)) {
    const source = readFileSync(file, "utf8");

    const opened = [...source.matchAll(AS_CHILD_OPENING)].length;
    const blocks = [...source.matchAll(AS_CHILD_BLOCK)];
    triggers += opened;
    if (blocks.length !== opened) {
      unparsed.push(`${file}: matched ${blocks.length} of ${opened} asChild triggers`);
    }

    for (const [, trigger, slotted] of blocks) {
      // A trigger may slot two components behind a conditional, so check them all.
      for (const [, child] of slotted.matchAll(JSX_ELEMENT)) {
        const declaration = declarations.get(child);
        if (declaration === undefined) {
          continue; // Not one of ours — a DOM element or a feature component.
        }
        if (!declaration.spreadsProps) {
          offenders.push(`${child} (${declaration.file}) under <${trigger} asChild> in ${file}`);
        }
      }
    }
  }

  assert.ok(triggers > 0, "matched no asChild triggers — has the JSX pattern changed?");
  assert.deepEqual(
    unparsed,
    [],
    `could not resolve what these asChild triggers slot, so they went unchecked:\n${unparsed.join("\n")}`
  );
  assert.deepEqual(
    offenders,
    [],
    `components used with asChild but not spreading props, so ref cannot reach the element:\n${offenders.join("\n")}`
  );
});
