import { SugarHigh, tokenize } from "sugar-high";

export type SourceTokenKind = "keyword" | "string" | "comment" | "plain";

export interface SourceToken {
  readonly kind: SourceTokenKind;
  readonly text: string;
}

const HIGHLIGHTED_EXTENSIONS = new Set(["js", "jsx", "mjs", "cjs", "ts", "tsx"]);

/**
 * Whether the tokenizer knows this file's language. It only understands the
 * JavaScript family, and colouring Kotlin or Swift by JavaScript's rules would
 * be wrong in ways that look right — so anything else is shown plain. A bundled
 * asset's URL can carry a query string or fragment, which is not part of the
 * name.
 */
export function isHighlightable(filename: string): boolean {
  const name = filename.split(/[?#]/)[0];
  const dot = name.lastIndexOf(".");
  return dot !== -1 && HIGHLIGHTED_EXTENSIONS.has(name.slice(dot + 1).toLowerCase());
}

/**
 * Splits source lines into tokens, one list per line, losslessly: a line's
 * tokens joined back together are exactly the line. The lines are tokenised as
 * one block so a comment or template string that spans lines is understood as
 * such, and the result is plain data for the caller to render as elements —
 * never HTML, because these lines come from event payloads and are untrusted.
 */
export function tokenizeSourceLines(lines: readonly string[]): SourceToken[][] {
  const result: SourceToken[][] = lines.map(() => []);
  let lineIndex = 0;

  for (const [type, text] of tokenize(lines.join("\n"))) {
    const kind = kindOf(type);
    const parts = text.split("\n");
    parts.forEach((part, partIndex) => {
      if (partIndex > 0) {
        lineIndex += 1;
      }
      if (part !== "" && lineIndex < result.length) {
        result[lineIndex].push({ kind, text: part });
      }
    });
  }

  return result;
}

function kindOf(type: number): SourceTokenKind {
  switch (SugarHigh.TokenTypes[type]) {
    case "keyword":
      return "keyword";
    case "string":
      return "string";
    case "comment":
      return "comment";
    default:
      return "plain";
  }
}
