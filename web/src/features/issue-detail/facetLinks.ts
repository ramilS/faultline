import { ALL_TIME_WINDOW, DEFAULT_FILTERS, searchParamsFromFilters, type StreamFilters } from "../../lib/issueFilters.ts";

/**
 * Builds a stream path for one facet dimension, overriding only that
 * dimension and the window on top of the defaults. Everything else —
 * escaping, parameter ordering, default-omission — comes from
 * `searchParamsFromFilters`, the stream's single source of truth for a URL;
 * hand-writing the query string here would drift from it silently.
 */
function streamPathForFilters(slug: string, overrides: Partial<StreamFilters>): string {
  const filters: StreamFilters = { ...DEFAULT_FILTERS, ...ALL_TIME_WINDOW, ...overrides };
  const query = searchParamsFromFilters(filters).toString();
  return query === "" ? `/${slug}/issues` : `/${slug}/issues?${query}`;
}

/** Links to the stream filtered to one custom tag's value, e.g. `browser:safari`. */
export function streamPathForTag(slug: string, key: string, value: string): string {
  return streamPathForFilters(slug, { tags: [{ key, value }] });
}

/** Links to the stream filtered to one environment. */
export function streamPathForEnvironment(slug: string, environment: string): string {
  return streamPathForFilters(slug, { environments: [environment] });
}

/** Links to the stream filtered to one release. */
export function streamPathForRelease(slug: string, release: string): string {
  return streamPathForFilters(slug, { releases: [release] });
}

/**
 * Links to the stream windowed to exactly one chart bucket, rather than the
 * all-time window the other facet links open: a bucket link is answering
 * "what happened at 14:00", so widening it to all-time would surface events
 * the bar does not represent. `start`/`end` win over `statsPeriod` on the
 * wire (see `writeWindowParams`), so overriding just the two is enough — no
 * `statsPeriod` reaches the URL alongside them.
 */
export function streamPathForBucket(slug: string, bucketStart: string, bucketEnd: string): string {
  return streamPathForFilters(slug, { start: bucketStart, end: bucketEnd });
}
