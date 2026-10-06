import assert from "node:assert/strict";
import { test } from "node:test";

import {
  ALL_TIME_WINDOW,
  changesUrl,
  CLEARED_FILTERS,
  DEFAULT_FILTERS,
  escapeFilterValue,
  eventCount,
  eventCountTitle,
  filtersFromSearchParams,
  filtersKey,
  hasFiltersToClear,
  isDefaultFilters,
  isFilteredEmptyState,
  ISSUE_SORTS,
  issueRequestFrom,
  nextStatuses,
  parseIssueSort,
  searchParamsFromFilters,
  secondaryFilterCount,
  SORT_LABELS,
  sparklineCounts,
  sparklineWindow,
  streamKey,
  unescapeFilterValue,
  windowColumnLabel,
  type StreamFilters
} from "./issueFilters.ts";

// This repo's test runner is node:test, not vitest — see web/package.json's
// "test" script. Keeping every filter test on node:test/assert avoids adding
// a second test framework as a dependency just for this file.

const roundTrips: ReadonlyArray<[string, StreamFilters]> = [
  ["defaults", DEFAULT_FILTERS],
  [
    "every field set",
    {
      statuses: ["ignored"],
      levels: ["fatal", "error"],
      assigned: "me",
      environments: ["production", "staging"],
      releases: ["1.2.3"],
      tags: [{ key: "tenant", value: "acme" }],
      statsPeriod: "30d",
      start: "",
      end: "",
      query: "cart total",
      sort: "times_seen"
    }
  ],
  ["statuses only", { ...DEFAULT_FILTERS, statuses: ["resolved", "ignored"] }],
  ["levels only", { ...DEFAULT_FILTERS, levels: ["warning"] }],
  ["assigned only", { ...DEFAULT_FILTERS, assigned: "none" }],
  ["environments only", { ...DEFAULT_FILTERS, environments: ["staging"] }],
  ["releases only", { ...DEFAULT_FILTERS, releases: ["2.0.0"] }],
  ["tags only", { ...DEFAULT_FILTERS, tags: [{ key: "browser", value: "chrome" }] }],
  ["query only", { ...DEFAULT_FILTERS, query: "TypeError" }],
  ["sort only", { ...DEFAULT_FILTERS, sort: "first_seen" }],
  ["sort only (users_seen)", { ...DEFAULT_FILTERS, sort: "users_seen" }],
  ["statsPeriod only", { ...DEFAULT_FILTERS, statsPeriod: "90d" }],
  ["absolute range", { ...DEFAULT_FILTERS, statsPeriod: "", start: "2026-07-01T00:00:00Z", end: "2026-07-08T00:00:00Z" }],
  ["all time (cleared window)", { ...DEFAULT_FILTERS, statsPeriod: "", start: "", end: "" }],
  ["query needing escaping", { ...DEFAULT_FILTERS, query: "a&b=c d/e?f#g" }],
  ["values containing a literal wildcard and bang", { ...DEFAULT_FILTERS, environments: ["*staging*"], releases: ["!hotfix"] }]
];

for (const [name, filters] of roundTrips) {
  test(`round-trips through search params: ${name}`, () => {
    assert.deepEqual(filtersFromSearchParams(searchParamsFromFilters(filters)), filters);
  });
}

test("default filters serialise to an empty query string", () => {
  // A shared link to the default view must not carry ?status=unresolved&sort=last_seen&...,
  // which would also make two identical views look like different URLs.
  assert.equal(searchParamsFromFilters(DEFAULT_FILTERS).toString(), "");
});

test("missing params yield the defaults", () => {
  assert.deepEqual(filtersFromSearchParams(new URLSearchParams()), DEFAULT_FILTERS);
});

test("a whitespace-only search is not written to the URL", () => {
  const params = searchParamsFromFilters({ ...DEFAULT_FILTERS, query: "   " });
  assert.equal(params.toString(), "");
});

test("text is written verbatim so a trailing space survives one debounce cycle", () => {
  const params = searchParamsFromFilters({ ...DEFAULT_FILTERS, query: "cart " });
  assert.equal(params.get("q"), "cart ");
});

test("unknown sort values fall back to the default", () => {
  assert.equal(parseIssueSort("sideways"), "last_seen");
  assert.equal(parseIssueSort(null), "last_seen");
});

test("users_seen is an accepted sort and round-trips through parseIssueSort", () => {
  assert.ok(ISSUE_SORTS.includes("users_seen"));
  assert.equal(parseIssueSort("users_seen"), "users_seen");
});

test("every accepted sort has a non-empty dropdown label", () => {
  // The dropdown derives its options from ISSUE_SORTS through SORT_LABELS, so
  // this is the invariant that IssueFilterBar's old hardcoded SORT_OPTIONS
  // list violated for users_seen: the parser accepted a sort the dropdown had
  // no label for.
  for (const sort of ISSUE_SORTS) {
    assert.ok(SORT_LABELS[sort].length > 0, `expected a non-empty label for sort "${sort}"`);
  }
});

test("users_seen's dropdown label matches the Users column header", () => {
  assert.equal(SORT_LABELS.users_seen, "Users");
});

test("an unknown status or level in the URL is dropped rather than crashing", () => {
  const params = new URLSearchParams({ status: "bogus", level: "sideways" });
  assert.deepEqual(filtersFromSearchParams(params), DEFAULT_FILTERS);
});

test("clearing every status checkbox falls back to the default rather than an empty set", () => {
  // The URL can't tell "never touched" apart from "explicitly cleared to
  // nothing" once zero `status` params are written either way, so the filter
  // bar keeps at least one status checked and this is the read-side half of
  // that contract.
  assert.deepEqual(filtersFromSearchParams(new URLSearchParams()).statuses, ["unresolved"]);
});

test("explicitly selecting every status is distinguishable from the untouched default", () => {
  const params = searchParamsFromFilters({ ...DEFAULT_FILTERS, statuses: ["unresolved", "resolved", "ignored"] });
  assert.deepEqual(params.getAll("status"), ["unresolved", "resolved", "ignored"]);
});

test("multi-value levels serialise in canonical vocabulary order, not click order", () => {
  // Canonical, not insertion, order: filtersKey/streamKey are used as cache
  // keys (see useIssueStream, IssueStreamPage's bulk-triage key), so two
  // level selections with the same members but picked in a different order
  // must serialise identically or they'd wrongly look like different streams.
  const params = searchParamsFromFilters({ ...DEFAULT_FILTERS, levels: ["error", "fatal"] });

  assert.deepEqual(params.getAll("level"), ["fatal", "error"]);
  assert.deepEqual(filtersFromSearchParams(params).levels, ["fatal", "error"]);
});

test("omits the default period from the URL but still requests it", () => {
  const params = searchParamsFromFilters(DEFAULT_FILTERS);
  assert.equal(params.has("statsPeriod"), false);

  const request = issueRequestFrom(DEFAULT_FILTERS, 50);
  assert.equal(request.statsPeriod, "14d");
});

test("an absolute range is mutually exclusive with statsPeriod on the wire", () => {
  const filters: StreamFilters = { ...DEFAULT_FILTERS, statsPeriod: "", start: "2026-07-01T00:00:00Z", end: "2026-07-08T00:00:00Z" };

  const params = searchParamsFromFilters(filters);
  assert.equal(params.has("statsPeriod"), false);
  assert.equal(params.get("start"), "2026-07-01T00:00:00Z");
  assert.equal(params.get("end"), "2026-07-08T00:00:00Z");

  const request = issueRequestFrom(filters, 50);
  assert.equal("statsPeriod" in request, false);
  assert.equal(request.start, "2026-07-01T00:00:00Z");
  assert.equal(request.end, "2026-07-08T00:00:00Z");
});

test("a start without a matching end is treated as no absolute range", () => {
  const params = new URLSearchParams({ start: "2026-07-01T00:00:00Z" });
  assert.deepEqual(filtersFromSearchParams(params).statsPeriod, "14d");
});

test("selecting all time writes the statsPeriod=all URL marker and round-trips back to it", () => {
  const filters: StreamFilters = { ...DEFAULT_FILTERS, ...ALL_TIME_WINDOW };

  const params = searchParamsFromFilters(filters);
  assert.equal(params.get("statsPeriod"), "all");
  assert.equal(params.has("start"), false);
  assert.equal(params.has("end"), false);
  assert.deepEqual(filtersFromSearchParams(params), filters);
});

test("all time never reaches the server: the request omits every window field", () => {
  const filters: StreamFilters = { ...DEFAULT_FILTERS, ...ALL_TIME_WINDOW };

  const request = issueRequestFrom(filters, 50);
  assert.equal("statsPeriod" in request, false);
  assert.equal("start" in request, false);
  assert.equal("end" in request, false);
});

test("assigned is omitted from the URL and the request when empty", () => {
  const params = searchParamsFromFilters({ ...DEFAULT_FILTERS, assigned: "" });
  assert.equal(params.has("assigned"), false);

  const request = issueRequestFrom({ ...DEFAULT_FILTERS, assigned: "" }, 50);
  assert.equal("assigned" in request, false);
});

test("a real assigned value is forwarded to both the URL and the request", () => {
  assert.equal(searchParamsFromFilters({ ...DEFAULT_FILTERS, assigned: "me" }).get("assigned"), "me");
  assert.equal(issueRequestFrom({ ...DEFAULT_FILTERS, assigned: "me" }, 50).assigned, "me");
});

test("escapeFilterValue escapes a leading bang and every literal star", () => {
  assert.equal(escapeFilterValue("!important"), "\\!important");
  assert.equal(escapeFilterValue("50%*off"), "50%\\*off");
  assert.equal(escapeFilterValue("!a*b*c"), "\\!a\\*b\\*c");
  assert.equal(escapeFilterValue("production"), "production");
});

test("unescapeFilterValue reverses escapeFilterValue", () => {
  for (const raw of ["!important", "50%*off", "!a*b*c", "production", "a!b"]) {
    assert.equal(unescapeFilterValue(escapeFilterValue(raw)), raw);
  }
});

test("a literal wildcard or bang in a selected facet value is escaped before it reaches the API", () => {
  const request = issueRequestFrom({ ...DEFAULT_FILTERS, environments: ["*staging*"], releases: ["!hotfix"] }, 50);
  assert.deepEqual(request.environments, ["\\*staging\\*"]);
  assert.deepEqual(request.releases, ["\\!hotfix"]);
});

test("a literal wildcard or bang in a tag value is escaped, but the key is left alone", () => {
  const request = issueRequestFrom({ ...DEFAULT_FILTERS, tags: [{ key: "promo", value: "*vip!" }] }, 50);
  assert.deepEqual(request.tags, ["promo:\\*vip!"]);
});

test("the API request trims text and omits blank fields", () => {
  const request = issueRequestFrom({ ...DEFAULT_FILTERS, statuses: [], query: "  cart  " }, 25);
  assert.deepEqual(request, { query: "cart", sort: "last_seen", limit: 25, statsPeriod: "14d" });
});

test("filters differing only by untrimmed whitespace share one key", () => {
  assert.equal(filtersKey({ ...DEFAULT_FILTERS, query: "cart " }), filtersKey({ ...DEFAULT_FILTERS, query: "cart" }));
});

test("filters describing different requests have different keys", () => {
  const keys = new Set([
    filtersKey(DEFAULT_FILTERS),
    filtersKey({ ...DEFAULT_FILTERS, statuses: ["resolved"] }),
    filtersKey({ ...DEFAULT_FILTERS, query: "cart" }),
    filtersKey({ ...DEFAULT_FILTERS, sort: "times_seen" }),
    filtersKey({ ...DEFAULT_FILTERS, environments: ["production"] }),
    filtersKey({ ...DEFAULT_FILTERS, statsPeriod: "90d" }),
    filtersKey({ ...DEFAULT_FILTERS, levels: ["error"] }),
    filtersKey({ ...DEFAULT_FILTERS, assigned: "me" }),
    filtersKey({ ...DEFAULT_FILTERS, tags: [{ key: "tenant", value: "acme" }] })
  ]);

  assert.equal(keys.size, 9);
});

test("a draft that serialises to nothing does not count as a URL change", () => {
  assert.equal(changesUrl(DEFAULT_FILTERS, { ...DEFAULT_FILTERS, query: "   " }), false);
});

test("a real edit counts as a URL change", () => {
  assert.equal(changesUrl(DEFAULT_FILTERS, { ...DEFAULT_FILTERS, query: "cart" }), true);
  assert.equal(changesUrl(DEFAULT_FILTERS, { ...DEFAULT_FILTERS, statuses: ["resolved"] }), true);
  assert.equal(changesUrl(DEFAULT_FILTERS, DEFAULT_FILTERS), false);
});

test("a trailing space the user just typed does reach the URL", () => {
  assert.equal(changesUrl({ ...DEFAULT_FILTERS, query: "cart" }, { ...DEFAULT_FILTERS, query: "cart " }), true);
});

test("the same filters under different projects are different streams", () => {
  assert.notEqual(streamKey("checkout-web", DEFAULT_FILTERS), streamKey("billing", DEFAULT_FILTERS));
  assert.equal(streamKey("checkout-web", DEFAULT_FILTERS), streamKey("checkout-web", DEFAULT_FILTERS));
});

test("only the default filter set is reported as default", () => {
  assert.equal(isDefaultFilters(DEFAULT_FILTERS), true);
  assert.equal(isDefaultFilters({ ...DEFAULT_FILTERS, query: "  " }), true);
  assert.equal(isDefaultFilters({ ...DEFAULT_FILTERS, query: "cart" }), false);
  assert.equal(isDefaultFilters({ ...DEFAULT_FILTERS, statuses: ["resolved"] }), false);
});

test("clearing resets the window to all time rather than reapplying the 14d default", () => {
  assert.equal(CLEARED_FILTERS.statsPeriod, "");
  assert.equal(CLEARED_FILTERS.start, "");
  assert.equal(CLEARED_FILTERS.end, "");
  // A genuinely empty filter set: distinct from the untouched default, which
  // still carries the 14d window — otherwise "Clear filters" on a project
  // whose only issues are older than 14 days would be a visible no-op.
  assert.notEqual(filtersKey(CLEARED_FILTERS), filtersKey(DEFAULT_FILTERS));
});

test("isFilteredEmptyState: the untouched default view selects the first-run onboarding branch", () => {
  assert.equal(isFilteredEmptyState(DEFAULT_FILTERS), false);
});

test("isFilteredEmptyState: any applied filter selects the no-match branch", () => {
  assert.equal(isFilteredEmptyState({ ...DEFAULT_FILTERS, statuses: ["resolved"] }), true);
  assert.equal(isFilteredEmptyState({ ...DEFAULT_FILTERS, statsPeriod: "90d" }), true);
});

test("isFilteredEmptyState: an all-time window with nothing else applied is still the widest view, not a dead-end filter", () => {
  // CLEARED_FILTERS is all-time with every other filter at its default. Zero
  // issues there means the project has no issues at all, not that a filter
  // narrowed them out — it must read as onboarding, the same as
  // DEFAULT_FILTERS, even though CLEARED_FILTERS is a distinct, non-default
  // filter set for hasFiltersToClear and the URL (see the test above).
  assert.equal(isFilteredEmptyState(CLEARED_FILTERS), false);
  // Applying a genuine filter on top of all-time must still read as filtered.
  assert.equal(isFilteredEmptyState({ ...CLEARED_FILTERS, statuses: ["resolved"] }), true);
});

test("hasFiltersToClear: a filter set that differs from CLEARED_FILTERS still has something to clear", () => {
  assert.equal(hasFiltersToClear(DEFAULT_FILTERS), true);
  assert.equal(hasFiltersToClear({ ...DEFAULT_FILTERS, statuses: ["resolved"] }), true);
  assert.equal(hasFiltersToClear({ ...CLEARED_FILTERS, statsPeriod: "90d" }), true);
});

test("hasFiltersToClear: pressing clear a second time has nothing left to do", () => {
  // Regression guard for the no-op second press: once the URL already reads
  // as CLEARED_FILTERS, writing CLEARED_FILTERS again would not change
  // anything, so the "Clear filters" action must report there is nothing
  // left to clear rather than staying visible and doing nothing.
  assert.equal(hasFiltersToClear(CLEARED_FILTERS), false);
});

// windowColumnLabel takes a bare { statsPeriod, start, end } triple, not a
// full StreamFilters — every case below only sets those three.
type WindowInput = Pick<StreamFilters, "statsPeriod" | "start" | "end">;

const windowColumnLabelCases: ReadonlyArray<[string, WindowInput, string]> = [
  ["24h preset", { statsPeriod: "24h", start: "", end: "" }, "24h"],
  ["7d preset", { statsPeriod: "7d", start: "", end: "" }, "7d"],
  ["14d preset", { statsPeriod: "14d", start: "", end: "" }, "14d"],
  ["30d preset", { statsPeriod: "30d", start: "", end: "" }, "30d"],
  ["90d preset", { statsPeriod: "90d", start: "", end: "" }, "90d"],
  ["an absolute range", { statsPeriod: "", start: "2026-07-01T00:00:00Z", end: "2026-07-08T00:00:00Z" }, "Range"],
  ["all time (cleared window)", { statsPeriod: "", start: "", end: "" }, "All time"]
];

for (const [name, input, expected] of windowColumnLabelCases) {
  test(`windowColumnLabel: ${name} renders "${expected}"`, () => {
    assert.equal(windowColumnLabel(input), expected);
  });
}

const eventCountCases: ReadonlyArray<[string, Parameters<typeof eventCount>[0], number]> = [
  ["a windowed response with period_count present", { period_count: 3, times_seen: 120 }, 3],
  // period_count: 0 is a live state, not an absent one: trailing-window
  // membership is last_seen >= now()-Trailing while the count comes from the
  // hourly aggregate, so a listed issue can legitimately have zero in-window
  // events. `??` must keep 0 rather than falling through to the lifetime
  // times_seen figure.
  ["a windowed response with zero events in the window", { period_count: 0, times_seen: 120 }, 0],
  ["a windowless response with only times_seen", { times_seen: 120 }, 120]
];

for (const [name, issue, expected] of eventCountCases) {
  test(`eventCount: ${name} returns ${expected}`, () => {
    assert.equal(eventCount(issue), expected);
  });
}

const sparklineCountsCases: ReadonlyArray<[string, Parameters<typeof sparklineCounts>[0], readonly number[]]> = [
  ["a windowed response prefers counts", { counts: [1, 2, 3], counts_24h: [9, 9, 9] }, [1, 2, 3]],
  ["a windowless response falls back to counts_24h", { counts: undefined, counts_24h: [1, 2] }, [1, 2]],
  ["neither field present yields an empty sparkline rather than a crash", { counts: undefined, counts_24h: undefined }, []]
];

for (const [name, issue, expected] of sparklineCountsCases) {
  test(`sparklineCounts: ${name}`, () => {
    assert.deepEqual(sparklineCounts(issue), expected);
  });
}

test("sparklineWindow labels a windowed response by the selected range, not a fixed 24 hours", () => {
  assert.equal(sparklineWindow({ counts: [1, 2, 3] }), "the selected time range");
});

test("sparklineWindow falls back to 24 hours for a windowless response", () => {
  assert.equal(sparklineWindow({ counts: undefined }), "24 hours");
});

test("eventCountTitle surfaces the lifetime total only when period_count narrowed the visible number", () => {
  assert.equal(eventCountTitle({ period_count: 3, times_seen: 120 }), "120 total");
});

test("eventCountTitle is absent for a windowless response, where the visible number is already lifetime", () => {
  assert.equal(eventCountTitle({ period_count: undefined, times_seen: 120 }), undefined);
});

test("nextStatuses: checking an unselected status adds it", () => {
  assert.deepEqual(nextStatuses(["unresolved"], "resolved"), ["unresolved", "resolved"]);
});

test("nextStatuses: unchecking one of several selected statuses removes only that one", () => {
  assert.deepEqual(nextStatuses(["unresolved", "resolved"], "unresolved"), ["resolved"]);
});

test("nextStatuses: unchecking the last remaining status is a no-op, not an empty set", () => {
  // The write-side counterpart of "clearing every status checkbox falls back
  // to the default rather than an empty set" above: an empty status set would
  // ask the API for every status while the checkboxes show none checked.
  assert.deepEqual(nextStatuses(["unresolved"], "unresolved"), ["unresolved"]);
});

test("secondaryFilterCount: the defaults count as no filters, so the phone toggle does not claim any", () => {
  assert.equal(secondaryFilterCount(DEFAULT_FILTERS), 0);
});

test("secondaryFilterCount: search, time window and sort are visible without the toggle, so they are not counted", () => {
  const filters: StreamFilters = { ...DEFAULT_FILTERS, query: "timeout", statsPeriod: "24h", sort: "times_seen" };

  assert.equal(secondaryFilterCount(filters), 0);
});

test("secondaryFilterCount: each level, environment, release and tag value counts once", () => {
  const filters: StreamFilters = {
    ...DEFAULT_FILTERS,
    levels: ["error", "fatal"],
    environments: ["production"],
    releases: ["2.4.1", "2.4.0"],
    tags: [{ key: "browser", value: "Safari" }]
  };

  assert.equal(secondaryFilterCount(filters), 6);
});

test("secondaryFilterCount: an assignee filter counts as one, and so does a changed status selection", () => {
  assert.equal(secondaryFilterCount({ ...DEFAULT_FILTERS, assigned: "me" }), 1);
  assert.equal(secondaryFilterCount({ ...DEFAULT_FILTERS, statuses: ["unresolved", "resolved"] }), 1);
  assert.equal(secondaryFilterCount({ ...DEFAULT_FILTERS, statuses: [] }), 1);
});
