import { ApiError } from "./api.ts";

export function messageFor(error: unknown): string {
  if (error instanceof ApiError) {
    return error.message;
  }
  if (error instanceof Error) {
    return error.message;
  }
  return "unexpected error";
}

/**
 * True only for the access-boundary 404: a project-scoped resource the
 * caller's group membership does not reach answers 404, identically to a
 * resource that never existed (spec: out-of-scope reads are 404, not 403, so
 * a member cannot learn a project exists from the response).
 *
 * A page uses this to choose its failure chrome — the existing not-found
 * route for a 404, `ErrorState` with Retry for everything else, since a 500
 * or a dropped connection is worth retrying and a 404 from the access
 * boundary never will be.
 */
export function isNotFound(error: unknown): boolean {
  return error instanceof ApiError && error.status === 404;
}
