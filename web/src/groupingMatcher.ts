// The grouping-rule matcher is a small DSL: "<field>:<pattern>", with the tag
// field further namespaced as "tags.<key>:<pattern>". This module is the only
// place that composes or decomposes that string, so the panel's <select> +
// inputs can never hand the server a malformed matcher.

export type MatcherField = "error.type" | "message" | "logger" | "tags" | "fingerprint";

export interface MatcherInputs {
  field: MatcherField;
  pattern: string;
  tagKey: string;
}

const KNOWN_FIELDS: readonly MatcherField[] = ["error.type", "message", "logger", "fingerprint"];

function isKnownField(value: string): value is MatcherField {
  return (KNOWN_FIELDS as readonly string[]).includes(value);
}

function matcherFieldPrefix(field: MatcherField, tagKey: string): string {
  return field === "tags" ? `tags.${tagKey}` : field;
}

// Returns a user-facing validation message, or null when the inputs compose
// into a well-formed matcher. Callers must check this before building the
// matcher string for submission — it is what stops "tags.:value" ever
// reaching the server.
export function validateMatcherInputs({ field, pattern, tagKey }: MatcherInputs): string | null {
  if (field === "tags" && tagKey.trim() === "") {
    return "Tag key is required when the field is Tag.";
  }
  if (pattern.trim() === "") {
    return "Pattern is required.";
  }
  return null;
}

// quoteIfNeeded wraps pattern in double quotes when it contains whitespace
// and isn't already quoted — the server rejects an unquoted pattern
// containing whitespace (matcher.go's unquotePattern), so the docs' own
// headline example, message:"payment failed*", would otherwise be
// unenterable through this form: an admin typing "payment failed" for
// Message got a bare 400 with no hint that quoting was required. Quoting is
// safe even for an already-glob'd pattern like payment failed* — the server
// unquotes before parsing the glob, so "payment failed*" still yields a
// prefix match.
function quoteIfNeeded(pattern: string): string {
  const alreadyQuoted = pattern.startsWith('"') && pattern.endsWith('"') && pattern.length >= 2;
  if (alreadyQuoted || !/\s/.test(pattern)) {
    return pattern;
  }
  return `"${pattern}"`;
}

export function buildMatcher(inputs: MatcherInputs): string {
  return `${matcherFieldPrefix(inputs.field, inputs.tagKey)}:${quoteIfNeeded(inputs.pattern)}`;
}

// Preview text for the form, valid even while the inputs are incomplete
// (unlike buildMatcher, which callers must only use once validated). Mirrors
// buildMatcher's quoting so the preview shows exactly what submission sends.
export function previewMatcher({ field, pattern, tagKey }: MatcherInputs): string {
  const prefix = field === "tags" ? `tags.${tagKey || "<key>"}` : field;
  return `${prefix}:${pattern ? quoteIfNeeded(pattern) : "<pattern>"}`;
}

// mergeAliasTemplatePattern matches the literal template merge mints for its
// generated fingerprint-alias rules (grouping.MergeTemplate server-side:
// "merged,issue-<targetID>"). It is how the panel tells a merge-generated
// rule apart from one an admin wrote by hand — there is no separate "kind"
// column, the template shape itself is the signal.
const mergeAliasTemplatePattern = /^merged,issue-\d+$/;

// isMergeAliasRule reports whether template is one merge minted (rather than
// user-authored). Such a rule is load-bearing for a specific merge's future
// routing: deleting it can permanently strand later events from that merge's
// original signal (see the delete-confirmation copy in
// features/settings/DeleteRuleDialog.tsx).
export function isMergeAliasRule(template: string): boolean {
  return mergeAliasTemplatePattern.test(template);
}

// canEditGroupingRule reports whether a rule's row may be opened in the edit
// form. A merge alias row must answer false: the merge overwrote the target
// issue's fingerprint in place, so this row is the only place its post-merge
// routing lives — there is no tombstone left to redirect from if an edit
// changes or breaks it. This is the single
// source of truth features/settings/RuleRow.tsx gates the edit form on, kept alongside
// isMergeAliasRule so the two can never drift apart.
export function canEditGroupingRule(template: string): boolean {
  return !isMergeAliasRule(template);
}

export function parseMatcher(matcher: string): MatcherInputs {
  const separatorIndex = matcher.indexOf(":");
  const rawField = separatorIndex === -1 ? matcher : matcher.slice(0, separatorIndex);
  const pattern = separatorIndex === -1 ? "" : matcher.slice(separatorIndex + 1);
  if (rawField.startsWith("tags.")) {
    return { field: "tags", tagKey: rawField.slice("tags.".length), pattern };
  }
  if (isKnownField(rawField)) {
    return { field: rawField, tagKey: "", pattern };
  }
  // Defensive fallback: a matcher outside the fields this form knows about
  // (e.g. added server-side later) is shown verbatim as a "message" pattern
  // rather than silently losing data on edit.
  return { field: "message", tagKey: "", pattern: matcher };
}
