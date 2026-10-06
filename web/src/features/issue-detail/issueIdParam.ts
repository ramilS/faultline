/**
 * The issue id in a route segment, or null when that segment is not one.
 *
 * Shared by the detail page and the all-events page so the two cannot
 * disagree about what counts as an issue: an id is a positive integer, and
 * `/acme/issues/0/events` must be refused before it reaches the API rather
 * than by whatever the server happens to answer.
 */
export function issueIdFromParam(raw: string | undefined): number | null {
  const parsed = Number(raw);
  return Number.isInteger(parsed) && parsed > 0 ? parsed : null;
}
