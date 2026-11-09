import type { ScrubRule } from "@/api";

import presets from "./scrubPresets.json" with { type: "json" };

// Mirrors the server bound in internal/scrub/rule.go.
export const MAX_SCRUB_RULES = 25;
export const MAX_SCRUB_RULE_NAME_BYTES = 64;
export const MAX_SCRUB_RULE_PATTERN_BYTES = 512;
export const MAX_SCRUB_RULE_REPLACEMENT_BYTES = 128;
export const MAX_SCRUB_REPLACEMENT_REFERENCES = 8;
export const MAX_SCRUB_RULE_DESCRIPTION_BYTES = 200;

export const MAX_SAMPLE_LENGTH = 16_000;

export interface ScrubPreset {
  readonly name: string;
  readonly description: string;
  readonly pattern: string;
  readonly replacement: string;
  readonly example: string;
}

export const SCRUB_PRESETS: readonly ScrubPreset[] = presets;

export function presetRule(preset: ScrubPreset): ScrubRule {
  return { name: preset.name, description: preset.description, pattern: preset.pattern, replacement: preset.replacement };
}

export function isPresetAdded(rules: readonly ScrubRule[], preset: ScrubPreset): boolean {
  return rules.some((rule) => rule.pattern === preset.pattern && rule.replacement === preset.replacement);
}

export const DEFAULT_SAMPLE = JSON.stringify(
  {
    message: "Checkout failed for order 1042",
    user: { phone: "+1 555 0100" },
    breadcrumbs: SCRUB_PRESETS.map((preset) => preset.example)
  },
  null,
  2
);

const GROUP_REFERENCE = /\$(\$|\{([^}]*)\}|([\p{L}\p{Nd}_]+))/gu;

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
    if (utf8Length((rule.description ?? "").trim()) > MAX_SCRUB_RULE_DESCRIPTION_BYTES) {
      return `${prefix} description can be at most ${MAX_SCRUB_RULE_DESCRIPTION_BYTES} bytes.`;
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
