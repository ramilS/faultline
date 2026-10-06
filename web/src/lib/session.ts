import { ApiError, type UserRole } from "../api.ts";
import { messageFor } from "../errors.ts";

export interface SessionUser {
  id: number;
  name: string;
  email: string;
  role: UserRole;
}

/**
 * The four states the dashboard can be in with respect to authentication.
 *
 * `unavailable` exists so a failing server is not reported as a sign-out.
 * Collapsing it into `anonymous` would send the administrator to a login form
 * that cannot succeed and would hide the actual fault.
 */
export type Session =
  | { readonly state: "loading" }
  | { readonly state: "authenticated"; readonly user: SessionUser }
  | { readonly state: "anonymous" }
  | { readonly state: "unavailable"; readonly message: string };

export type ProbeOutcome =
  | { readonly kind: "user"; readonly user: SessionUser }
  | { readonly kind: "unauthorized" }
  | { readonly kind: "failed"; readonly message: string };

export function classifyProbeFailure(error: unknown): ProbeOutcome {
  if (error instanceof ApiError && error.status === 401) {
    return { kind: "unauthorized" };
  }
  return { kind: "failed", message: messageFor(error) };
}

export function sessionFromProbe(outcome: ProbeOutcome): Session {
  switch (outcome.kind) {
    case "user":
      return { state: "authenticated", user: outcome.user };
    case "unauthorized":
      return { state: "anonymous" };
    case "failed":
      return { state: "unavailable", message: outcome.message };
  }
}
