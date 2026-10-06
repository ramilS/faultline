// Mirrors the server bounds in internal/scrub/scrub.go. Checking them here
// turns an opaque 400 into a message beside the field that caused it.
export const MAX_SCRUB_FIELDS = 50;
export const MAX_SCRUB_FIELD_LENGTH = 64; // bytes

export type FieldListResult =
  | { readonly ok: true; readonly fields: string[] }
  | { readonly ok: false; readonly error: string };

/** One field name per line or comma-separated; blanks dropped, duplicates ignored case-insensitively. */
export function parseFieldList(text: string): FieldListResult {
  const seen = new Set<string>();
  const fields: string[] = [];
  for (const entry of text.split(/[\n,]/)) {
    const name = entry.trim();
    const key = name.toLowerCase();
    if (name === "" || seen.has(key)) {
      continue;
    }
    // The server bounds bytes, not UTF-16 units, so a Cyrillic or CJK name is measured as UTF-8.
    if (new TextEncoder().encode(name).length > MAX_SCRUB_FIELD_LENGTH) {
      return { ok: false, error: `Field names can be at most ${MAX_SCRUB_FIELD_LENGTH} bytes.` };
    }
    seen.add(key);
    fields.push(name);
  }
  if (fields.length > MAX_SCRUB_FIELDS) {
    return { ok: false, error: `At most ${MAX_SCRUB_FIELDS} fields are allowed.` };
  }
  return { ok: true, fields };
}

export function formatFieldList(fields: readonly string[]): string {
  return fields.join("\n");
}
