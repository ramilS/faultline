/**
 * Formats the affected-users count shown as the `UsersFacet` section's
 * description. `saturated` comes straight from the wire
 * (`issueUsersResponse.saturated` in `internal/api/users_facet.go`) — the
 * frontend never hardcodes `ingest.MaxUsersPerIssue` to decide this itself,
 * so `total` is rendered as a floor ("N+") only when the server says so.
 */
export function affectedUsersLabel(total: number, saturated: boolean): string {
  const noun = total === 1 ? "user" : "users";
  const count = saturated ? `${total.toLocaleString()}+` : total.toLocaleString();
  return `${count} ${noun}`;
}
