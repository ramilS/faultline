export const MAX_VAR_ENTRIES = 50;
export const MAX_VAR_TEXT = 1000;
export const MAX_VAR_DEPTH = 6;

export type VarDescription =
  | { readonly kind: "scalar"; readonly text: string; readonly truncated: number }
  | { readonly kind: "collection"; readonly summary: string };

export interface VarEntry {
  readonly key: string;
  readonly value: unknown;
}

function isCollection(value: unknown): value is Record<string, unknown> | unknown[] {
  return typeof value === "object" && value !== null;
}

function plural(count: number, noun: string): string {
  return `${count} ${noun}${count === 1 ? "" : "s"}`;
}

/**
 * How one variable value reads collapsed. Strings stay exactly as the SDK sent
 * them (Python and Ruby already send a repr), cut at MAX_VAR_TEXT so a
 * megabyte-long value cannot freeze the page.
 */
export function describeVar(value: unknown): VarDescription {
  if (Array.isArray(value)) {
    return { kind: "collection", summary: `${value.length === 0 ? "[]" : "[…]"} ${plural(value.length, "item")}` };
  }
  if (isCollection(value)) {
    const count = Object.keys(value).length;
    return { kind: "collection", summary: `${count === 0 ? "{}" : "{…}"} ${plural(count, "key")}` };
  }
  const text = typeof value === "string" ? value : String(value);
  if (text.length <= MAX_VAR_TEXT) {
    return { kind: "scalar", text, truncated: 0 };
  }
  return { kind: "scalar", text: text.slice(0, MAX_VAR_TEXT), truncated: text.length - MAX_VAR_TEXT };
}

export function varEntries(value: unknown): { entries: readonly VarEntry[]; omitted: number } {
  if (!isCollection(value)) {
    return { entries: [], omitted: 0 };
  }
  if (Array.isArray(value)) {
    const entries = value.slice(0, MAX_VAR_ENTRIES).map((item: unknown, index) => ({ key: String(index), value: item }));
    return { entries, omitted: value.length - entries.length };
  }
  const keys = Object.keys(value);
  const entries = keys.slice(0, MAX_VAR_ENTRIES).map((key) => ({ key, value: value[key] }));
  return { entries, omitted: keys.length - entries.length };
}
