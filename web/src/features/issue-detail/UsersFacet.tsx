import { Users } from "lucide-react";
import { useCallback } from "react";

import type { AffectedUser } from "@/api";
import { api } from "@/apiClient";
import { EmptyState } from "@/components/EmptyState";
import { ErrorState } from "@/components/ErrorState";
import { RelativeTime } from "@/components/RelativeTime";
import { Skeleton } from "@/components/ui/skeleton";
import { useAsyncResource } from "@/hooks/useAsyncResource";

import { affectedUsersLabel } from "./affectedUsers";
import { DetailSection } from "./DetailSection";

interface UsersFacetProps {
  issueId: number;
}

const KEY_LABELS: Record<AffectedUser["key"], string> = {
  id: "ID",
  email: "Email",
  username: "Username",
  ip: "IP address"
};

/**
 * Distinct identities seen across this issue's events — the "how many
 * people, not how many times" panel. Its own `DetailSection` with its own
 * async fetch rather than a row nested in `TagFacets`: unlike environments,
 * releases, and tags, this data isn't already on the loaded `IssueDetail`,
 * so folding it in would make `TagFacets`' own empty-state check lie about
 * what it covers.
 */
export function UsersFacet({ issueId }: UsersFacetProps) {
  const load = useCallback(() => api.listIssueUsers(issueId), [issueId]);
  const { state, reload } = useAsyncResource(load);

  // The description is a constant rather than the count: DetailSection
  // renders CardDescription only when description !== undefined, so a count
  // that arrives with the data would grow the card header by a line mid-load.
  // The count lives in the content area instead, where a shift between the
  // skeleton and the data is expected anyway — and where it sits directly
  // above the rows it describes.
  return (
    <DetailSection title="Users affected" description="Distinct identities seen across this issue's events.">
      {state.status === "loading" && <Skeleton className="h-24 w-full" />}

      {state.status === "failed" && <ErrorState className="py-8" message={state.message} onRetry={reload} />}

      {/* No count above this one, deliberately: "0 users" would say less than
          "No user context" does, and the header's Users stat already renders
          the zero. Do not add one back for symmetry with the branch below. */}
      {state.status === "ready" && state.value.users.length === 0 && (
        <EmptyState
          className="py-8"
          icon={Users}
          title="No user context"
          description="Events for this issue carried no id, email, username, or IP address."
        />
      )}

      {state.status === "ready" && state.value.users.length > 0 && (
        <>
          {/* The total and the rows are independent facts, never "N of M":
              total comes from issues.users_seen and the rows from
              issue_users in two separate non-transactional reads, so a
              concurrent ingest flush can briefly make the row count exceed
              the total. Any arithmetic between them would read as absurd. */}
          <p className="text-muted-foreground mb-2 text-xs tabular-nums">
            {affectedUsersLabel(state.value.total, state.value.saturated)}
          </p>
          {/* Rendered in the order the server returned it — the endpoint
              already orders by times_seen, and re-sorting here would just be
              an extra place for that ordering to drift out of sync. */}
          <ul className="space-y-1.5">
            {state.value.users.map((user) => (
              <UserRow key={`${user.key}:${user.value}`} user={user} />
            ))}
          </ul>
        </>
      )}
    </DetailSection>
  );
}

function UserRow({ user }: { user: AffectedUser }) {
  return (
    <li className="hover:bg-accent -mx-1.5 grid grid-cols-[auto_1fr_auto] items-baseline gap-x-3 rounded-md px-1.5 py-0.5 transition-colors">
      <span className="text-muted-foreground text-xs font-medium tracking-wide uppercase">
        {KEY_LABELS[user.key]}
      </span>
      {/* Text, not a link: there is no `?user:` filter in issueFilters.ts to
          send it to, and an email here is attacker-supplied — see
          payloadReaders.ts for why it must never become a mailto: link. */}
      <span className="font-mono text-xs break-all">{user.value}</span>
      <span className="text-muted-foreground text-xs tabular-nums">{user.times_seen}</span>
      <span className="text-muted-foreground col-span-3 text-[11px]">
        last seen <RelativeTime iso={user.last_seen} />
      </span>
    </li>
  );
}
