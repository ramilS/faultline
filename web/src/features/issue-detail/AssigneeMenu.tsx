import { ChevronDown, UserRound } from "lucide-react";
import { useState } from "react";

import type { AssignableUser, IssueSummary } from "@/api";
import { api } from "@/apiClient";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger
} from "@/components/ui/dropdown-menu";
import { messageFor } from "@/errors";

const UNASSIGNED = "unassigned";

type Teammates =
  | { readonly status: "idle" }
  | { readonly status: "loading" }
  | { readonly status: "ready"; readonly users: readonly AssignableUser[] }
  | { readonly status: "failed"; readonly message: string };

interface AssigneeMenuProps {
  slug: string;
  assignee: IssueSummary["assignee"];
  currentUserId: number | null;
  /** True while another change to the issue is in flight. */
  disabled: boolean;
  /** `null` unassigns. Not called when the pick is the current assignee. */
  onAssign: (assigneeId: number | null) => void;
}

/**
 * Who holds the issue, and the way to hand it to someone else.
 *
 * Teammates load when the menu opens rather than with the page: most issue
 * views never touch assignment, and the list is per project. A failed load is
 * retried on the next open instead of being remembered as empty.
 */
export function AssigneeMenu({ slug, assignee, currentUserId, disabled, onAssign }: AssigneeMenuProps) {
  const [teammates, setTeammates] = useState<Teammates>({ status: "idle" });

  const load = async () => {
    setTeammates({ status: "loading" });
    try {
      setTeammates({ status: "ready", users: await api.listAssignableUsers(slug) });
    } catch (caught) {
      setTeammates({ status: "failed", message: messageFor(caught) });
    }
  };

  const handleOpenChange = (open: boolean) => {
    if (open && (teammates.status === "idle" || teammates.status === "failed")) {
      void load();
    }
  };

  const select = (value: string) => {
    const next = value === UNASSIGNED ? null : Number(value);
    if (next !== (assignee?.id ?? null)) {
      onAssign(next);
    }
  };

  return (
    <DropdownMenu onOpenChange={handleOpenChange}>
      <DropdownMenuTrigger asChild>
        <Button variant="outline" size="sm" disabled={disabled}>
          <UserRound aria-hidden="true" />
          Assignee: {assignee?.name ?? "Unassigned"}
          <ChevronDown className="text-muted-foreground" aria-hidden="true" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="max-h-72 min-w-56 overflow-y-auto">
        {teammates.status === "loading" && (
          <DropdownMenuLabel className="text-muted-foreground font-normal">Loading teammates…</DropdownMenuLabel>
        )}
        {teammates.status === "failed" && (
          <DropdownMenuLabel role="alert" className="text-level-error font-normal">
            Could not load teammates: {teammates.message}
          </DropdownMenuLabel>
        )}
        {teammates.status === "ready" && (
          <DropdownMenuRadioGroup value={assignee === null ? UNASSIGNED : String(assignee.id)} onValueChange={select}>
            <DropdownMenuRadioItem value={UNASSIGNED}>Unassigned</DropdownMenuRadioItem>
            {withCurrentAssignee(teammates.users, assignee).map((user) => (
              <DropdownMenuRadioItem key={user.id} value={String(user.id)}>
                {user.id === currentUserId ? `${user.name} (you)` : user.name}
              </DropdownMenuRadioItem>
            ))}
          </DropdownMenuRadioGroup>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

/**
 * Someone deactivated or removed from the project since being assigned is not
 * offered by the server, yet still holds the issue. Dropping them from the list
 * would show a radio group with nothing selected.
 */
function withCurrentAssignee(
  users: readonly AssignableUser[],
  assignee: IssueSummary["assignee"]
): readonly AssignableUser[] {
  if (assignee === null || users.some((user) => user.id === assignee.id)) {
    return users;
  }
  return [assignee, ...users];
}
