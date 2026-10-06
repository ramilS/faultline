import type { ScrubRule } from "@/api";

// Mirrors the server bound in internal/scrub/rule.go.
export const MAX_SCRUB_RULES = 25;
export const MAX_SCRUB_RULE_NAME_BYTES = 64;
export const MAX_SCRUB_RULE_PATTERN_BYTES = 512;
export const MAX_SCRUB_RULE_REPLACEMENT_BYTES = 128;
export const MAX_SCRUB_REPLACEMENT_REFERENCES = 8;

// JS regexes can backtrack catastrophically where the server's RE2 cannot, so
// bounding the input only protects the owner's own tab.
export const MAX_SAMPLE_LENGTH = 2000;

export interface ScrubPreset {
  readonly label: string;
  readonly rule: ScrubRule;
}

export const SCRUB_PRESETS: readonly ScrubPreset[] = [
  {
    label: "Telegram bot token",
    rule: { name: "Telegram bot token", pattern: "bot\\d{6,}:[A-Za-z0-9_-]{30,}", replacement: "bot[Filtered]" }
  },
  {
    label: "AWS access key ID",
    rule: { name: "AWS access key ID", pattern: "\\b(?:AKIA|ASIA)[A-Z0-9]{16}\\b", replacement: "" }
  },
  {
    label: "GitHub token",
    rule: { name: "GitHub token", pattern: "\\bgh[pousr]_[A-Za-z0-9]{36,}\\b", replacement: "" }
  }
];

export interface PreviewResult {
  readonly output: string;
  readonly errors: string[];
}

const GROUP_REFERENCE = /\$(\$|\{([^}]*)\}|([\p{L}\p{Nd}_]+))/gu;

/**
 * Applies the rules in order to sample text. The server, not this preview, is
 * authoritative: it uses RE2, which differs from the browser's engine. A rule
 * the browser cannot parse is reported and skipped so the others still apply.
 */
export function previewRules(rules: readonly ScrubRule[], sample: string): PreviewResult {
  let output = sample;
  const errors: string[] = [];
  for (const rule of rules) {
    if (rule.pattern === "") {
      continue;
    }
    let pattern: RegExp;
    try {
      pattern = new RegExp(rule.pattern, "g");
    } catch {
      errors.push(`"${rule.name}": the browser cannot parse this pattern; the server checks it on save.`);
      continue;
    }
    const template = rule.replacement === "" ? "[Filtered]" : rule.replacement;
    output = output.replace(pattern, (...args: unknown[]) => expandTemplate(template, args));
  }
  return { output, errors };
}

const utf8Length = (text: string) => new TextEncoder().encode(text).length;

function countGroupReferences(template: string): number {
  return [...template.matchAll(GROUP_REFERENCE)].filter((match) => match[1] !== "$").length;
}

/** Mirrors the server bounds so an oversized rule is explained here instead of by an opaque 400. */
export function validateRules(rules: readonly ScrubRule[]): string | null {
  if (rules.length > MAX_SCRUB_RULES) {
    return `At most ${MAX_SCRUB_RULES} rules are allowed.`;
  }
  for (const [index, rule] of rules.entries()) {
    const prefix = `Rule ${index + 1}:`;
    if (rule.name.trim() === "") {
      return `${prefix} name is required.`;
    }
    if (utf8Length(rule.name.trim()) > MAX_SCRUB_RULE_NAME_BYTES) {
      return `${prefix} name can be at most ${MAX_SCRUB_RULE_NAME_BYTES} bytes.`;
    }
    if (rule.pattern === "") {
      return `${prefix} pattern is required.`;
    }
    if (utf8Length(rule.pattern) > MAX_SCRUB_RULE_PATTERN_BYTES) {
      return `${prefix} pattern can be at most ${MAX_SCRUB_RULE_PATTERN_BYTES} bytes.`;
    }
    if (utf8Length(rule.replacement) > MAX_SCRUB_RULE_REPLACEMENT_BYTES) {
      return `${prefix} replacement can be at most ${MAX_SCRUB_RULE_REPLACEMENT_BYTES} bytes.`;
    }
    if (countGroupReferences(rule.replacement) > MAX_SCRUB_REPLACEMENT_REFERENCES) {
      return `${prefix} replacement can reference at most ${MAX_SCRUB_REPLACEMENT_REFERENCES} groups.`;
    }
  }
  return null;
}

function expandTemplate(template: string, args: unknown[]): string {
  const offsetAt = args.findIndex((value) => typeof value === "number");
  const captures = args.slice(1, offsetAt) as (string | undefined)[];
  const last = args[args.length - 1];
  const named = typeof last === "object" && last !== null ? (last as Record<string, string | undefined>) : {};
  return template.replace(GROUP_REFERENCE, (reference, escaped: string, braced: string | undefined, bare: string | undefined) => {
    if (escaped === "$") {
      return "$";
    }
    if (braced === "") {
      return reference;
    }
    const name = braced ?? bare ?? "";
    if (!/^\d+$/.test(name)) {
      return named[name] ?? "";
    }
    return Number(name) === 0 ? String(args[0]) : (captures[Number(name) - 1] ?? "");
  });
}
