import { createApi, type FetchLike } from "./api";

type UnauthorizedListener = () => void;

let notifyUnauthorized: UnauthorizedListener | null = null;

/**
 * Registers the single handler invoked when any call is rejected as
 * unauthenticated. Session expiry is handled here rather than at each call
 * site, because a per-call-site approach guarantees gaps: a screen added later
 * would silently render an authenticated shell against a dead cookie.
 */
export function setUnauthorizedListener(listener: UnauthorizedListener | null): void {
  notifyUnauthorized = listener;
}

// Both of these interpret 401 themselves. On login it means "wrong
// credentials" and belongs inline under the form; on the probe it is the
// normal anonymous answer, and the route guard already acts on it. Escalating
// either one to a global redirect would bounce the user mid-typing.
const SELF_HANDLED_401 = new Set(["/api/v1/auth/login", "/api/v1/auth/me"]);

const sessionAwareFetch: FetchLike = async (url, init) => {
  const response = await fetch(url, init);
  if (response.status === 401 && !SELF_HANDLED_401.has(url)) {
    notifyUnauthorized?.();
  }
  return response;
};

// A single shared client instance: every component closes over the same
// object rather than each constructing its own via createApi().
export const api = createApi(sessionAwareFetch);
