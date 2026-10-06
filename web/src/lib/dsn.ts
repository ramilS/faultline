import type { Key } from "../api.ts";

/**
 * Builds the DSN a Sentry SDK uses to reach this Faultline instance.
 *
 * The origin is the browser's own. The dashboard and the ingest endpoint are
 * served by the same binary, so the origin that reached this page is exactly
 * the one an SDK must post to — including a scheme or host rewritten by a
 * reverse proxy, which no server-side setting could infer reliably.
 */
export function buildDsn(key: Pick<Key, "public_key" | "project_id">, origin: string): string {
  const url = new URL(origin);
  return `${url.protocol}//${key.public_key}@${url.host}/${key.project_id}`;
}
