import type { ListIssueEventsOptions } from "../../api.ts";

/**
 * The scope one issue's event list is narrowed to: an environment and an
 * absolute time window.
 *
 * Two screens show an issue's events — the detail page's pager and the full
 * "all events" page it links to. The scope travels between them in the URL,
 * so the second list can never be silently wider than the first.
 *
 * Absolute bounds only, never a `statsPeriod`: see `eventWindow.ts` for why a
 * relative period must be frozen before it reaches a paged walk.
 */
export interface EventScope {
  /** "" means every environment. */
  readonly environment: string;
  /** RFC3339, or "" for no lower bound. */
  readonly start: string;
  /** RFC3339, or "" for no upper bound. */
  readonly end: string;
}

/**
 * Parameter names deliberately identical to the ones the issue stream already
 * writes (`web/src/lib/issueFilters.ts`) and the events endpoint already
 * reads (`parseEventWindow`, internal/api/issues.go) — one vocabulary for one
 * concept, rather than a third spelling private to this link.
 */
export const ENVIRONMENT_PARAM = "environment";
export const START_PARAM = "start";
export const END_PARAM = "end";

function readParam(params: URLSearchParams, name: string): string {
  return params.get(name)?.trim() ?? "";
}

/** The scope in the URL. Missing or blank values read as unscoped rather than throwing. */
export function eventScopeFromParams(params: URLSearchParams): EventScope {
  return {
    environment: readParam(params, ENVIRONMENT_PARAM),
    start: readParam(params, START_PARAM),
    end: readParam(params, END_PARAM)
  };
}

/** Returns the params with the scope written, each empty field removed. */
export function paramsWithEventScope(params: URLSearchParams, scope: EventScope): URLSearchParams {
  const next = new URLSearchParams(params);
  for (const [name, value] of [
    [ENVIRONMENT_PARAM, scope.environment],
    [START_PARAM, scope.start],
    [END_PARAM, scope.end]
  ] as const) {
    if (value === "") {
      next.delete(name);
    } else {
      next.set(name, value);
    }
  }
  return next;
}

/**
 * The `environment`/`start`/`end` portion of a `listIssueEvents` request.
 *
 * Shared by every call site on both screens so that dropping a filter from
 * one of them is a diff to this function, not a state that quietly compiles.
 */
export function eventScopeFilters(scope: EventScope): Pick<ListIssueEventsOptions, "environment" | "start" | "end"> {
  return {
    environment: scope.environment || undefined,
    start: scope.start || undefined,
    end: scope.end || undefined
  };
}

/**
 * A sentence naming the active scope, or null when nothing is narrowed.
 *
 * `formatInstant` is injected rather than imported so this stays a pure
 * module with a locale-independent test; the screens pass `absoluteTime`.
 */
export function describeEventScope(scope: EventScope, formatInstant: (iso: string) => string): string | null {
  const clauses: string[] = [];
  if (scope.environment !== "") {
    clauses.push(`in ${scope.environment}`);
  }
  if (scope.start !== "" && scope.end !== "") {
    clauses.push(`received between ${formatInstant(scope.start)} and ${formatInstant(scope.end)}`);
  } else if (scope.start !== "") {
    clauses.push(`received after ${formatInstant(scope.start)}`);
  } else if (scope.end !== "") {
    clauses.push(`received before ${formatInstant(scope.end)}`);
  }
  return clauses.length === 0 ? null : `Only events ${clauses.join(" ")}.`;
}

/** Returns the params with only the environment written, or removed when "" — the other scope fields untouched. */
export function paramsWithEnvironment(params: URLSearchParams, environment: string): URLSearchParams {
  const next = new URLSearchParams(params);
  if (environment === "") {
    next.delete(ENVIRONMENT_PARAM);
  } else {
    next.set(ENVIRONMENT_PARAM, environment);
  }
  return next;
}
