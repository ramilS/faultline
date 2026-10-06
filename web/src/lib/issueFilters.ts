import type { IssueFilters, IssueLevel, IssueSort, IssueStatus, IssueSummary } from "../api.ts";

// Deliberately not api.ts's `StatsPeriod`: that name belongs to the per-issue
// sparkline endpoint's narrower set (24h/14d/90d). This one is the list
// endpoint's `statsPeriod` parameter, which also accepts 7d/30d — structurally
// compatible with api.ts's `WindowPeriod` field of the same shape, but kept
// as its own type here so importing the wrong `StatsPeriod` can't happen.
export const STATS_PERIODS = ["24h", "7d", "14d", "30d", "90d"] as const;
export type StatsPeriod = (typeof STATS_PERIODS)[number];

export const ISSUE_STATUSES: readonly IssueStatus[] = ["unresolved", "resolved", "ignored"];

export const ISSUE_LEVELS: readonly IssueLevel[] = ["fatal", "error", "warning", "info", "debug"];

export const ISSUE_SORTS: readonly IssueSort[] = ["last_seen", "times_seen", "first_seen", "users_seen"];

/**
 * Labels for the sort dropdown, keyed so TypeScript enforces one label per
 * `IssueSort` member. IssueFilterBar derives its options by mapping
 * ISSUE_SORTS through this instead of keeping its own hardcoded list — the
 * two drifting apart is exactly how `users_seen` went missing from the
 * dropdown despite being a valid sort. `Record<IssueSort, string>` turns a
 * future fifth sort with no label into a compile error instead of a blank
 * dropdown entry. Must match the labels IssueTable's SortableHead columns
 * already show, so the table header and the dropdown agree.
 */
export const SORT_LABELS: Record<IssueSort, string> = {
  last_seen: "Last seen",
  times_seen: "Events",
  first_seen: "First seen",
  users_seen: "Users"
};

export interface TagFilter {
  readonly key: string;
  readonly value: string;
}

export interface StreamFilters {
  readonly statuses: readonly IssueStatus[];
  readonly levels: readonly IssueLevel[];
  /** "me", "none", or an email; "" means no assignee filter. */
  readonly assigned: string;
  readonly environments: readonly string[];
  readonly releases: readonly string[];
  readonly tags: readonly TagFilter[];
  /** "" alongside a populated start/end means an absolute range is active. */
  readonly statsPeriod: StatsPeriod | "";
  readonly start: string;
  readonly end: string;
  readonly query: string;
  readonly sort: IssueSort;
}

export const DEFAULT_FILTERS: StreamFilters = {
  statuses: ["unresolved"],
  levels: [],
  assigned: "",
  environments: [],
  releases: [],
  tags: [],
  statsPeriod: "14d",
  start: "",
  end: "",
  query: "",
  sort: "last_seen"
};

/**
 * `!` (leading) and `*` (anywhere) are wire-format syntax for negation and
 * wildcarding in every filter value the server accepts. A stored environment,
 * release, or tag value that happens to contain one literally must be
 * escaped before it leaves the app, or the server reads it as an operator
 * instead of data. The same escaping applies to autocomplete's prefix query,
 * since the facet-values endpoint runs it through the identical LIKE-pattern
 * translation — an unescaped "*" there matches everything instead of nothing.
 */
export function escapeFilterValue(value: string): string {
  const starsEscaped = value.replace(/\*/g, "\\*");
  return starsEscaped.startsWith("!") ? `\\${starsEscaped}` : starsEscaped;
}

/** Inverse of escapeFilterValue, for redisplaying a value read back from the URL. */
export function unescapeFilterValue(value: string): string {
  const bangUnescaped = value.startsWith("\\!") ? value.slice(1) : value;
  return bangUnescaped.replace(/\\\*/g, "*");
}

function isIssueStatus(value: string): value is IssueStatus {
  return (ISSUE_STATUSES as readonly string[]).includes(value);
}

function isIssueLevel(value: string): value is IssueLevel {
  return (ISSUE_LEVELS as readonly string[]).includes(value);
}

function isStatsPeriod(value: string): value is StatsPeriod {
  return (STATS_PERIODS as readonly string[]).includes(value);
}

/** Order-independent: two checkbox groups with the same members are the same set. */
function sameStatusSet(a: readonly IssueStatus[], b: readonly IssueStatus[]): boolean {
  if (a.length !== b.length) {
    return false;
  }
  const set = new Set(a);
  return b.every((status) => set.has(status));
}

/**
 * Sorts by position in a fixed vocabulary, so two checkbox groups with the
 * same members always serialise in the same order regardless of click order.
 * Without this, unchecking and rechecking status boxes in a different
 * sequence would change `filtersKey` for an identical filter and discard the
 * pages already loaded for no reason.
 */
function byVocabularyOrder<T>(vocabulary: readonly T[]): (a: T, b: T) => number {
  const position = new Map(vocabulary.map((item, index) => [item, index]));
  return (a, b) => (position.get(a) ?? 0) - (position.get(b) ?? 0);
}

/** Same rationale as byVocabularyOrder, for arbitrary facet values with no fixed vocabulary to sort by. */
function sortedValues(values: readonly string[]): readonly string[] {
  return [...values].sort();
}

function sortedTags(tags: readonly TagFilter[]): readonly TagFilter[] {
  return [...tags].sort((a, b) => serializeTagParam(a).localeCompare(serializeTagParam(b)));
}

/** "key:value", matching the `tag=key:value` query parameter the API reads. */
function serializeTagParam(tag: TagFilter): string {
  return `${tag.key}:${escapeFilterValue(tag.value)}`;
}

/** Falls back to null on a malformed pair, rather than throwing: a hand-edited URL is data, not a crash. */
function parseTagParam(raw: string): TagFilter | null {
  const separator = raw.indexOf(":");
  if (separator <= 0 || separator === raw.length - 1) {
    return null;
  }
  return { key: raw.slice(0, separator), value: unescapeFilterValue(raw.slice(separator + 1)) };
}

export function parseIssueSort(raw: string | null): IssueSort {
  return ISSUE_SORTS.find((candidate) => candidate === raw) ?? DEFAULT_FILTERS.sort;
}

/**
 * URL marker for an explicitly all-time window. Not a `StatsPeriod` — the
 * server never sees this value, since `windowRequestFields` omits the window
 * field entirely for `statsPeriod === ""` with no start/end — it exists only
 * so "the user cleared the window" round-trips through the URL distinctly
 * from "no param was ever set", which `parseWindow` already treats as the
 * 14d default. Without this marker, writing an all-time filter set to the
 * URL would omit the param entirely, and reading it back would silently
 * reintroduce the 14d default.
 */
const ALL_TIME_MARKER = "all";

/** The all-time window: no `statsPeriod` preset and no absolute range. */
export const ALL_TIME_WINDOW: Pick<StreamFilters, "statsPeriod" | "start" | "end"> = {
  statsPeriod: "",
  start: "",
  end: ""
};

/**
 * `start`/`end` win over `statsPeriod` when both are present: the two are
 * mutually exclusive on the wire, and an absolute range is the more specific
 * of the two to have survived into the URL.
 */
function parseWindow(params: URLSearchParams): Pick<StreamFilters, "statsPeriod" | "start" | "end"> {
  const start = params.get("start") ?? "";
  const end = params.get("end") ?? "";
  if (start !== "" && end !== "") {
    return { statsPeriod: "", start, end };
  }
  const period = params.get("statsPeriod");
  if (period === ALL_TIME_MARKER) {
    return { ...ALL_TIME_WINDOW };
  }
  return { statsPeriod: period !== null && isStatsPeriod(period) ? period : DEFAULT_FILTERS.statsPeriod, start: "", end: "" };
}

function writeWindowParams(params: URLSearchParams, filters: StreamFilters): void {
  if (filters.start !== "" && filters.end !== "") {
    params.set("start", filters.start);
    params.set("end", filters.end);
    return;
  }
  if (filters.statsPeriod === "") {
    params.set("statsPeriod", ALL_TIME_MARKER);
    return;
  }
  if (filters.statsPeriod !== DEFAULT_FILTERS.statsPeriod) {
    params.set("statsPeriod", filters.statsPeriod);
  }
}

/** Included only when a window is actually set: the server never defaults one. */
function windowRequestFields(filters: StreamFilters): Pick<IssueFilters, "statsPeriod" | "start" | "end"> {
  if (filters.start !== "" && filters.end !== "") {
    return { start: filters.start, end: filters.end };
  }
  if (filters.statsPeriod !== "") {
    return { statsPeriod: filters.statsPeriod };
  }
  return {};
}

export function filtersFromSearchParams(params: URLSearchParams): StreamFilters {
  const statuses = params.getAll("status").filter(isIssueStatus);
  const tags = params
    .getAll("tag")
    .map(parseTagParam)
    .filter((tag): tag is TagFilter => tag !== null);

  return {
    statuses: statuses.length > 0 ? statuses : DEFAULT_FILTERS.statuses,
    levels: params.getAll("level").filter(isIssueLevel),
    assigned: params.get("assigned") ?? "",
    // A hand-edited or stale URL can carry `environment=`; an empty value
    // must not survive to the request, which the server answers with 400.
    environments: params.getAll("environment").map(unescapeFilterValue).filter((value) => value !== ""),
    releases: params.getAll("release").map(unescapeFilterValue).filter((value) => value !== ""),
    tags,
    query: params.get("q") ?? "",
    sort: parseIssueSort(params.get("sort")),
    ...parseWindow(params)
  };
}

/**
 * Only values that differ from the default are written.
 *
 * Text is written verbatim rather than trimmed, because this runs while the
 * user is still typing: rewriting "cart " to "cart" in the URL would feed a
 * shortened value back into the input and swallow the space.
 */
export function searchParamsFromFilters(filters: StreamFilters): URLSearchParams {
  const params = new URLSearchParams();
  if (!sameStatusSet(filters.statuses, DEFAULT_FILTERS.statuses)) {
    for (const status of [...filters.statuses].sort(byVocabularyOrder(ISSUE_STATUSES))) {
      params.append("status", status);
    }
  }
  for (const level of [...filters.levels].sort(byVocabularyOrder(ISSUE_LEVELS))) {
    params.append("level", level);
  }
  if (filters.assigned !== "") {
    params.set("assigned", filters.assigned);
  }
  // Guards against an empty string reaching the wire regardless of how it
  // got into `filters` — the server answers an empty value with 400.
  for (const environment of sortedValues(filters.environments)) {
    if (environment !== "") {
      params.append("environment", escapeFilterValue(environment));
    }
  }
  for (const release of sortedValues(filters.releases)) {
    if (release !== "") {
      params.append("release", escapeFilterValue(release));
    }
  }
  for (const tag of sortedTags(filters.tags)) {
    params.append("tag", serializeTagParam(tag));
  }
  if (filters.query.trim() !== "") {
    params.set("q", filters.query);
  }
  if (filters.sort !== DEFAULT_FILTERS.sort) {
    params.set("sort", filters.sort);
  }
  writeWindowParams(params, filters);
  return params;
}

/**
 * Stable identity of the request a filter set describes.
 *
 * Normalised, so deleting a trailing space the user just typed does not count
 * as a new query and discard the pages already loaded.
 */
export function filtersKey(filters: StreamFilters): string {
  return searchParamsFromFilters({ ...filters, query: filters.query.trim() }).toString();
}

/**
 * Whether writing `next` would actually change the URL.
 *
 * Guards the debounced writers: a draft of only spaces serialises to nothing,
 * so comparing the draft against the parsed-back value would never agree and
 * would rewrite the URL on every render.
 */
export function changesUrl(current: StreamFilters, next: StreamFilters): boolean {
  return searchParamsFromFilters(current).toString() !== searchParamsFromFilters(next).toString();
}

/** Identity of a project's filtered stream: the same key means the same request. */
export function streamKey(slug: string, filters: StreamFilters): string {
  return `${slug}?${filtersKey(filters)}`;
}

export function isDefaultFilters(filters: StreamFilters): boolean {
  return filtersKey(filters) === "";
}

/**
 * The filter set `useUrlFilters.clear()` writes: every filter at its default
 * except the window, which resets to all-time rather than the dashboard's
 * 14d default. A "Clear filters" that left the 14d window in place would be a
 * no-op for the exact case it exists to fix — a project whose only issues are
 * older than 14 days.
 */
export const CLEARED_FILTERS: StreamFilters = { ...DEFAULT_FILTERS, ...ALL_TIME_WINDOW };

/**
 * Whether "Clear filters" would still change anything. `isFilteredEmptyState`
 * stays true after a clear (all-time is itself a non-default filter, by
 * design — see CLEARED_FILTERS), so the empty state keeps reading as
 * "no issues match" rather than flipping to onboarding. This is the other
 * half: once `filters` already equals `CLEARED_FILTERS`, a second press
 * writes the same URL and does nothing, so the action is hidden instead of
 * offering a control that silently no-ops.
 */
export function hasFiltersToClear(filters: StreamFilters): boolean {
  return changesUrl(filters, CLEARED_FILTERS);
}

/**
 * Whether every filter except the time window matches DEFAULT_FILTERS.
 * `isFilteredEmptyState` uses this to reason about the window separately,
 * since an all-time window (unlike every other non-default window) is
 * treated as no filter at all rather than a narrowing one.
 */
function matchesDefaultsOutsideWindow(filters: StreamFilters): boolean {
  return filtersKey({ ...filters, ...ALL_TIME_WINDOW }) === filtersKey({ ...DEFAULT_FILTERS, ...ALL_TIME_WINDOW });
}

function isAllTimeWindow(filters: Pick<StreamFilters, "statsPeriod" | "start" | "end">): boolean {
  return (
    filters.statsPeriod === ALL_TIME_WINDOW.statsPeriod &&
    filters.start === ALL_TIME_WINDOW.start &&
    filters.end === ALL_TIME_WINDOW.end
  );
}

/**
 * Whether the stream's empty state should read as "no issues match these
 * filters" (true) rather than the first-run "no issues yet" onboarding
 * (false). Keyed on the untouched-default identity rather than the window
 * alone: the dashboard's own 14d default technically narrows results too,
 * but for a share link nobody has touched yet, "no events ever" is the far
 * more likely explanation than "no events in the last 14 days" — and unlike
 * the onboarding branch, the filtered branch's "Clear filters" action is a
 * real escape hatch only once a filter has actually been applied.
 *
 * All-time is the one window exempt from that reasoning: with every other
 * filter left at its default, an all-time window is the widest possible view
 * of the project, so zero results means the project has no issues at all —
 * not that a filter narrowed them out. `CLEARED_FILTERS` (all-time, nothing
 * else set) therefore reads as onboarding here, even though it still counts
 * as a non-default filter set for `hasFiltersToClear` and the URL. A narrower
 * explicit window (7d/30d/90d, or an absolute range) keeps the original
 * share-link reasoning above and still reads as filtered.
 */
export function isFilteredEmptyState(filters: StreamFilters): boolean {
  if (!matchesDefaultsOutsideWindow(filters)) {
    return true;
  }
  return !isDefaultFilters(filters) && !isAllTimeWindow(filters);
}

const WINDOW_COLUMN_LABELS: Record<StatsPeriod, string> = {
  "24h": "24h",
  "7d": "7d",
  "14d": "14d",
  "30d": "30d",
  "90d": "90d"
};

/**
 * Short label for the issue table's sparkline column header. Must track the
 * active window instead of the "24h" the column showed back when the
 * sparkline itself was always a fixed 24-hour one (see Sparkline's counts vs
 * counts_24h split) — a stale label would assert something the data no
 * longer means.
 */
export function windowColumnLabel(filters: Pick<StreamFilters, "statsPeriod" | "start" | "end">): string {
  if (filters.start !== "" && filters.end !== "") {
    return "Range";
  }
  if (filters.statsPeriod === "") {
    return "All time";
  }
  return WINDOW_COLUMN_LABELS[filters.statsPeriod as StatsPeriod];
}

/**
 * Escapes each value and drops blanks before handing the array to the
 * request, then omits the field entirely if nothing is left — the same
 * "no empty value reaches the wire" guard `searchParamsFromFilters` applies,
 * kept in one place so both writers can't drift out of sync.
 */
function buildValueListField(
  field: "environments" | "releases",
  values: readonly string[]
): Pick<IssueFilters, "environments" | "releases"> {
  const escaped = values.filter((value) => value !== "").map(escapeFilterValue);
  return escaped.length > 0 ? { [field]: escaped } : {};
}

/**
 * `period_count` is the in-window figure; `times_seen` is lifetime. Showing
 * only the lifetime number beside a window-filtered list is exactly what the
 * windowed-counts work upstream (see api.ts's IssueSummary) exists to avoid,
 * so this prefers `period_count` and falls back to `times_seen` only for a
 * windowless response, which has no `period_count` at all.
 */
export function eventCount(issue: Pick<IssueSummary, "period_count" | "times_seen">): number {
  return issue.period_count ?? issue.times_seen;
}

/**
 * A windowed list request (any statsPeriod, including the dashboard's own
 * 14d default, or an absolute range) populates `counts` and omits the legacy
 * `counts_24h`; a windowless one is the reverse. Reading `counts_24h`
 * unconditionally would be `undefined` for the now-common windowed case and
 * crash Sparkline's `.length` access.
 */
export function sparklineCounts(issue: Pick<IssueSummary, "counts" | "counts_24h">): readonly number[] {
  return issue.counts ?? issue.counts_24h ?? [];
}

export function sparklineWindow(issue: Pick<IssueSummary, "counts">): string {
  return issue.counts !== undefined ? "the selected time range" : "24 hours";
}

/** `period_count` is windowed; keep `times_seen` reachable as a hover detail rather than dropping it. */
export function eventCountTitle(issue: Pick<IssueSummary, "period_count" | "times_seen">): string | undefined {
  return issue.period_count !== undefined ? `${issue.times_seen.toLocaleString()} total` : undefined;
}

/**
 * Computes the status set after toggling one checkbox. A status filter with
 * nothing checked would ask the API for "every status" while the UI reads as
 * "no status", so toggling off the last remaining status is a no-op —
 * selecting every status explicitly is still reachable by checking all three.
 */
export function nextStatuses(current: readonly IssueStatus[], toggled: IssueStatus): readonly IssueStatus[] {
  const checked = current.includes(toggled);
  if (checked && current.length === 1) {
    return current;
  }
  return checked ? current.filter((candidate) => candidate !== toggled) : [...current, toggled];
}

/**
 * The request always carries a window — a trailing `statsPeriod` (14d by
 * default) or an absolute `start`/`end` — even though the URL hides
 * `statsPeriod` when it is unchanged from the default: the server never
 * defaults one, so omitting it here would silently switch the dashboard to
 * all-time.
 */
export function issueRequestFrom(filters: StreamFilters, limit: number): IssueFilters {
  const query = filters.query.trim();
  return {
    ...(filters.statuses.length > 0 ? { statuses: [...filters.statuses] } : {}),
    ...(filters.levels.length > 0 ? { levels: [...filters.levels] } : {}),
    ...(filters.assigned !== "" ? { assigned: filters.assigned } : {}),
    ...buildValueListField("environments", filters.environments),
    ...buildValueListField("releases", filters.releases),
    ...(filters.tags.length > 0 ? { tags: filters.tags.map(serializeTagParam) } : {}),
    ...(query === "" ? {} : { query }),
    sort: filters.sort,
    limit,
    ...windowRequestFields(filters)
  };
}

/**
 * How many filters beyond search, time window and sort are narrowing the
 * stream. Those three stay on screen at every width; the rest can be folded
 * away on a phone, so the fold has to say whether it is hiding anything. The
 * default status selection is not a filter the user chose, so it is not
 * counted.
 */
export function secondaryFilterCount(filters: StreamFilters): number {
  const statusChanged =
    filters.statuses.length !== DEFAULT_FILTERS.statuses.length ||
    filters.statuses.some((status) => !DEFAULT_FILTERS.statuses.includes(status));

  return (
    filters.levels.length +
    filters.environments.length +
    filters.releases.length +
    filters.tags.length +
    (filters.assigned === "" ? 0 : 1) +
    (statusChanged ? 1 : 0)
  );
}
