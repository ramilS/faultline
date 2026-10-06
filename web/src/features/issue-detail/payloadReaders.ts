/**
 * Defensive readers for `EventDetail.payload`, which is untrusted JSON from
 * the public internet: every parser here is best-effort and never throws. A
 * shape a reader does not recognise renders as raw JSON rather than
 * disappearing, so evidence stays visible at exactly the moment someone is
 * looking for it.
 *
 * Two producers write this payload: the native Faultline envelope, where
 * `breadcrumbs` is a bare array, and the Sentry envelope, where it is
 * `{values: [...]}`. This module is the only place that knows either shape.
 */

/** The most breadcrumbs rendered from one payload, regardless of how many are stored. */
export const MAX_RENDERED_BREADCRUMBS = 100;

/** The most request headers rendered from one payload, regardless of how many are stored. */
export const MAX_RENDERED_REQUEST_HEADERS = 50;

/** The most context cards rendered from one payload. Ingest caps neither the number of contexts nor their fields, so this is a render-side judgement. */
export const MAX_RENDERED_CONTEXTS = 25;

/** The most fields rendered per context card. Ingest caps neither the number of contexts nor their fields, so this is a render-side judgement. */
export const MAX_RENDERED_CONTEXT_FIELDS = 50;

/** The most tags rendered from one payload. Mirrors the ingest cap `MaxTags = 50` at internal/ingest/envelope.go:23, so the renderer never truncates a payload that ingest admitted whole. */
export const MAX_RENDERED_TAGS = 50;

/** The most modules rendered from one payload. Mirrors the ingest cap `MaxFrames = 200` at internal/ingest/envelope.go:22. */
export const MAX_RENDERED_MODULES = 200;

/**
 * The most characters rendered from any single payload value.
 *
 * Every other cap in this module bounds how MANY values render; none bounds
 * how LONG one is. Ingest admits up to 20 MiB of decompressed envelope
 * (`MaxDecompressedBytes`, internal/ingest/envelope.go:19) and caps no
 * individual field, so a single `extra` value or header can be megabytes of
 * attacker-chosen text — enough to lock the tab laying it out in one `<dd>`.
 * 2 000 characters is an order of magnitude past the longest value worth
 * reading in place (a URL, a stack frame path, a user agent, a serialised
 * request body's first screen) and still lays out instantly.
 */
export const MAX_RENDERED_VALUE_CHARS = 2000;

/** Appended to a value cut short by {@link MAX_RENDERED_VALUE_CHARS}, so truncation is never silent. */
export const TRUNCATION_SUFFIX = "… (truncated)";

/** Shown in place of a sensitive header's value; the header name stays visible. */
export const REDACTED = "••••••";

/**
 * Header names whose value is a live credential rather than diagnostic data.
 * Lower-cased: matching against this set must lower-case (and trim — see
 * below) the candidate name too, since HTTP header names are case-insensitive
 * on the wire and a client sending `Authorization` or `AUTHORIZATION` must
 * redact the same as `authorization`.
 */
export const SENSITIVE_REQUEST_HEADERS = new Set([
  "authorization",
  "cookie",
  "set-cookie",
  "x-api-key",
  "proxy-authorization"
]);

/**
 * Normalizes a stored header name for matching against
 * {@link SENSITIVE_REQUEST_HEADERS} only — never for display. A proxy or
 * SDK emitting a nonstandard key like `" Authorization"` still carries a
 * live credential, and matching must survive that surrounding whitespace
 * or the redaction is trivially bypassed by a key nobody would call
 * malicious, just nonstandard.
 */
function normalizeHeaderNameForMatch(name: string): string {
  return name.trim().toLowerCase();
}

export interface Breadcrumb {
  timestamp: string | null;
  category: string;
  level: string;
  message: string;
  /** Anything the shape did not account for, rendered rather than dropped. */
  data: unknown;
}

export interface EventUser {
  id: string;
  email: string;
  username: string;
  ipAddress: string;
  /** True when every field is empty — the panel renders nothing rather than an empty card. */
  isEmpty: boolean;
}

export interface HttpRequest {
  method: string;
  url: string;
  query: string;
  headers: ReadonlyArray<{ name: string; value: string }>;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

/**
 * Cuts a display string to {@link MAX_RENDERED_VALUE_CHARS}, marking the cut
 * so the reader never pretends a truncated value was short.
 */
function boundLength(value: string): string {
  return value.length > MAX_RENDERED_VALUE_CHARS ? value.slice(0, MAX_RENDERED_VALUE_CHARS) + TRUNCATION_SUFFIX : value;
}

/** Coerces any value to a display string without throwing, empty for nullish, length-bounded. */
function readString(value: unknown): string {
  if (typeof value === "string") {
    return boundLength(value);
  }
  if (value === null || value === undefined) {
    return "";
  }
  if (typeof value === "number" || typeof value === "boolean") {
    return String(value);
  }
  return "";
}

function readBreadcrumbEntries(payload: Record<string, unknown>): unknown[] {
  const raw = payload.breadcrumbs;
  if (Array.isArray(raw)) {
    return raw;
  }
  if (isRecord(raw) && Array.isArray(raw.values)) {
    return raw.values;
  }
  return [];
}

/** Milliseconds either side of the epoch that `Date` can represent at all. */
const MAX_TIME_VALUE = 8.64e15;

/**
 * Reads a breadcrumb's instant from either wire form. The native envelope
 * sends an ISO string; `@sentry/core` stamps every breadcrumb with
 * `dateTimestampInSeconds()`, a float of *seconds* since the epoch, so the
 * numeric form is what the primary protocol actually produces. A value outside
 * the range `Date` can represent reads as absent rather than throwing.
 */
function readTimestamp(value: unknown): string | null {
  if (typeof value === "string") {
    return value;
  }
  if (typeof value !== "number" || !Number.isFinite(value)) {
    return null;
  }
  const milliseconds = value * 1000;
  return Math.abs(milliseconds) > MAX_TIME_VALUE ? null : new Date(milliseconds).toISOString();
}

function readBreadcrumb(entry: unknown): Breadcrumb {
  if (!isRecord(entry)) {
    return { timestamp: null, category: "", level: "", message: readString(entry), data: entry };
  }
  const { timestamp, category, level, message, ...rest } = entry;
  return {
    timestamp: readTimestamp(timestamp),
    category: readString(category),
    level: readString(level),
    message: readString(message),
    data: Object.keys(rest).length > 0 ? rest : undefined
  };
}

/** Reads breadcrumbs from either wire shape, tail-bounded to the most recent {@link MAX_RENDERED_BREADCRUMBS}. */
export function readBreadcrumbs(payload: unknown): readonly Breadcrumb[] {
  if (!isRecord(payload)) {
    return [];
  }
  const entries = readBreadcrumbEntries(payload);
  const bounded = entries.length > MAX_RENDERED_BREADCRUMBS ? entries.slice(-MAX_RENDERED_BREADCRUMBS) : entries;
  return bounded.map(readBreadcrumb);
}

/** Reads the user context, or null when the payload has none. */
export function readUser(payload: unknown): EventUser | null {
  if (!isRecord(payload) || !isRecord(payload.user)) {
    return null;
  }
  const user = payload.user;
  const id = readString(user.id);
  const email = readString(user.email);
  const username = readString(user.username);
  const ipAddress = readString(user.ip_address ?? user.ip);
  return { id, email, username, ipAddress, isEmpty: id === "" && email === "" && username === "" && ipAddress === "" };
}

/**
 * Bounds the header list before render. Headers carry no time order the way
 * breadcrumbs do, so there is no "most recent" end to keep; the headers a
 * client sends earliest tend to be the required/standard ones (Host,
 * User-Agent), so the bound is applied to wire order, before the alphabetical
 * sort below, rather than to an arbitrary alphabetical slice.
 */
function readHeaders(raw: unknown): ReadonlyArray<{ name: string; value: string }> {
  if (!isRecord(raw)) {
    return [];
  }
  const entries = Object.entries(raw);
  const bounded = entries.length > MAX_RENDERED_REQUEST_HEADERS ? entries.slice(0, MAX_RENDERED_REQUEST_HEADERS) : entries;
  return bounded
    .map(([name, value]) => ({
      // Displayed byte-exact as stored, even if that name has anomalous
      // whitespace: an unexpected key is itself evidence of what a proxy or
      // SDK actually sent, and trimming it for display would hide that.
      name,
      value: SENSITIVE_REQUEST_HEADERS.has(normalizeHeaderNameForMatch(name)) ? REDACTED : readString(value)
    }))
    .sort((a, b) => a.name.localeCompare(b.name));
}

/** Reads the HTTP request context, or null when the payload has none. */
export function readRequest(payload: unknown): HttpRequest | null {
  if (!isRecord(payload) || !isRecord(payload.request)) {
    return null;
  }
  const request = payload.request;
  return {
    method: readString(request.method),
    url: readString(request.url),
    query: readString(request.query_string),
    headers: readHeaders(request.headers)
  };
}

export interface EventContext {
  name: string;
  fields: ReadonlyArray<{ label: string; value: string }>;
}

/** One `tags` entry. Keys can repeat — the array-of-pairs wire shape permits it. */
export interface EventTag {
  key: string;
  value: string;
}

/** One developer-supplied `extra` entry, its value already flattened to a display string. */
export interface EventExtraEntry {
  key: string;
  value: string;
}

/** One `modules` entry: a dependency and the version the SDK reported. */
export interface EventModule {
  name: string;
  version: string;
}

/** Context names Sentry emits that other panels already own. */
const OWNED_CONTEXT_NAMES = new Set(["trace"]);

/**
 * Reads `contexts.*` (os/runtime/device/browser/...) as one card per context
 * name, excluding blank names and names another panel already renders
 * (currently just `trace`, which `RequestTimeline` reads for its own
 * purpose). Both the
 * number of cards and the fields within each card are head-bounded — the
 * bound is applied to wire order, before any filtering, the same choice
 * {@link readHeaders} documents for request headers.
 */
export function readContexts(payload: unknown): readonly EventContext[] {
  if (!isRecord(payload) || !isRecord(payload.contexts)) {
    return [];
  }
  const contextEntries = Object.entries(payload.contexts);
  const boundedContexts =
    contextEntries.length > MAX_RENDERED_CONTEXTS ? contextEntries.slice(0, MAX_RENDERED_CONTEXTS) : contextEntries;
  const contexts: EventContext[] = [];
  for (const [name, value] of boundedContexts) {
    // A nameless context has no card title to render — `{"contexts": {"": …}}`
    // is well-formed JSON and would otherwise draw a card headed by nothing.
    // Skipped rather than titled "Unknown": the fields are still in the raw
    // payload panel, where an anomaly like this is worth reading in context.
    if (name.trim() === "" || OWNED_CONTEXT_NAMES.has(name) || !isRecord(value)) {
      continue;
    }
    const fieldEntries = Object.entries(value);
    const boundedFields =
      fieldEntries.length > MAX_RENDERED_CONTEXT_FIELDS ? fieldEntries.slice(0, MAX_RENDERED_CONTEXT_FIELDS) : fieldEntries;
    const fields = boundedFields.map(([label, raw]) => ({ label, value: readString(raw) })).filter((field) => field.value !== "");
    if (fields.length > 0) {
      contexts.push({ name, fields });
    }
  }
  return contexts;
}

export interface EventSdk {
  name: string;
  version: string;
}

/** Reads the reporting SDK's name and version, or null when either is empty. */
export function readSdk(payload: unknown): EventSdk | null {
  if (!isRecord(payload) || !isRecord(payload.sdk)) {
    return null;
  }
  const name = readString(payload.sdk.name);
  const version = readString(payload.sdk.version);
  return name === "" && version === "" ? null : { name, version };
}

/** The most `extra` entries rendered from one payload. */
export const MAX_RENDERED_EXTRA_ENTRIES = 50;

function readExtraValue(value: unknown): string {
  if (typeof value === "string" || typeof value === "number" || typeof value === "boolean") {
    return readString(value);
  }
  if (value === null || value === undefined) {
    return "";
  }
  try {
    // Bounded after serialising rather than before: there is no way to ask
    // JSON.stringify to stop early, so the cap here is about what reaches the
    // DOM, not about what is built in memory.
    return boundLength(JSON.stringify(value));
  } catch {
    return "";
  }
}

/** Reads developer-supplied `extra` data, key-order preserved, head-bounded (the first {@link MAX_RENDERED_EXTRA_ENTRIES} entries in wire order are kept). */
export function readExtra(payload: unknown): readonly EventExtraEntry[] {
  if (!isRecord(payload) || !isRecord(payload.extra)) {
    return [];
  }
  const entries = Object.entries(payload.extra);
  const bounded = entries.length > MAX_RENDERED_EXTRA_ENTRIES ? entries.slice(0, MAX_RENDERED_EXTRA_ENTRIES) : entries;
  return bounded.map(([key, value]) => ({ key, value: readExtraValue(value) }));
}

function readTagPair(entry: unknown): EventTag | null {
  if (!Array.isArray(entry) || entry.length !== 2) {
    return null;
  }
  const key = readString(entry[0]);
  return key === "" ? null : { key, value: readString(entry[1]) };
}

/**
 * Reads `tags` from either wire shape: a plain object, or an array of pairs.
 * Head-bounded to the first {@link MAX_RENDERED_TAGS} entries in wire order,
 * in both shapes — the same wire-order-before-anything-else choice
 * {@link readHeaders} documents for request headers.
 */
export function readTags(payload: unknown): readonly EventTag[] {
  if (!isRecord(payload)) {
    return [];
  }
  const raw = payload.tags;
  if (isRecord(raw)) {
    const entries = Object.entries(raw);
    const bounded = entries.length > MAX_RENDERED_TAGS ? entries.slice(0, MAX_RENDERED_TAGS) : entries;
    return bounded.map(([key, value]) => ({ key, value: readString(value) }));
  }
  if (Array.isArray(raw)) {
    const bounded = raw.length > MAX_RENDERED_TAGS ? raw.slice(0, MAX_RENDERED_TAGS) : raw;
    return bounded.map(readTagPair).filter((pair): pair is EventTag => pair !== null);
  }
  return [];
}

/**
 * Reads a field Sentry places at the top level of the event payload — never
 * inside `tags` — falling back to a like-named tag for SDK versions that
 * emit it there instead. Never throws: a payload that is not an object, or
 * one with neither the top-level field nor the tag, reads as `""`.
 *
 * `tags` is supplied by the caller rather than re-read here: this payload is
 * parsed once per event (see {@link readPayloadFields}), and a reader that
 * re-entered `readTags` would re-enumerate an attacker-sized tag map for
 * every field that has a tag fallback.
 */
function readTopLevelOrTag(payload: unknown, tags: readonly EventTag[], key: string): string {
  const direct = isRecord(payload) ? readString(payload[key]) : "";
  if (direct !== "") {
    return direct;
  }
  return tags.find((tag) => tag.key === key)?.value ?? "";
}

/** Reads `transaction`, Sentry's top-level field for the route/handler name, falling back to a `transaction` tag. */
export function readTransaction(payload: unknown, tags: readonly EventTag[]): string {
  return readTopLevelOrTag(payload, tags, "transaction");
}

/** Reads `server_name`, Sentry's top-level field for the reporting host, falling back to a `server_name` tag. */
export function readServerName(payload: unknown, tags: readonly EventTag[]): string {
  return readTopLevelOrTag(payload, tags, "server_name");
}

/**
 * Reads `modules` (dependency name → version), sorted by name for stable
 * display. Head-bounded to the first {@link MAX_RENDERED_MODULES} entries in
 * wire order, applied BEFORE the sort below — {@link readHeaders} documents
 * the identical choice: sorting first and slicing would keep only the
 * alphabetically-earliest packages, a worse sample than what the SDK
 * actually sent first.
 */
export function readModules(payload: unknown): readonly EventModule[] {
  if (!isRecord(payload) || !isRecord(payload.modules)) {
    return [];
  }
  const entries = Object.entries(payload.modules);
  const bounded = entries.length > MAX_RENDERED_MODULES ? entries.slice(0, MAX_RENDERED_MODULES) : entries;
  return bounded.map(([name, version]) => ({ name, version: readString(version) })).sort((a, b) => a.name.localeCompare(b.name));
}

const SAFE_PROTOCOLS = new Set(["http:", "https:"]);

/**
 * True when the scheme is http or https and the value is whole. Everything
 * else is rendered as text.
 *
 * A value cut short by {@link MAX_RENDERED_VALUE_CHARS} is refused even when
 * it still parses: a truncated URL is a different URL, and linking one would
 * send the reader somewhere the payload never named.
 */
export function isSafeHref(value: string): boolean {
  if (value.endsWith(TRUNCATION_SUFFIX)) {
    return false;
  }
  try {
    return SAFE_PROTOCOLS.has(new URL(value).protocol);
  } catch {
    return false;
  }
}

/**
 * Every panel-facing view of one event's payload, read in a single pass.
 *
 * Each reader below walks the payload with `Object.entries` before applying
 * its own cap, so a reader called once per consumer enumerates an
 * attacker-sized map once per consumer. This type is what the panel stack
 * memoizes per event so that never happens: parse here, render from the
 * result.
 */
export interface EventPayloadFields {
  readonly user: EventUser | null;
  readonly request: HttpRequest | null;
  readonly contexts: readonly EventContext[];
  readonly sdk: EventSdk | null;
  readonly tags: readonly EventTag[];
  readonly extra: readonly EventExtraEntry[];
  readonly modules: readonly EventModule[];
}

/** Runs every payload reader exactly once. Never throws — see this module's header. */
export function readPayloadFields(payload: unknown): EventPayloadFields {
  return {
    user: readUser(payload),
    request: readRequest(payload),
    contexts: readContexts(payload),
    sdk: readSdk(payload),
    tags: readTags(payload),
    extra: readExtra(payload),
    modules: readModules(payload)
  };
}
