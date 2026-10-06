import { Ban, CircleCheck } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";

import type { ManagedUser, UserRole } from "@/api";
import { api } from "@/apiClient";
import { RelativeTime } from "@/components/RelativeTime";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { TableCell, TableRow } from "@/components/ui/table";
import { messageFor } from "@/errors";
import { cn } from "@/lib/utils";

interface UserRowProps {
  user: ManagedUser;
  /** The signed-in user's own row: no Deactivate or Set-password action. */
  isSelf: boolean;
  onRoleChanged: () => void;
  onSetPasswordRequested: (user: ManagedUser) => void;
  onDeactivateRequested: (user: ManagedUser) => void;
  onReactivateRequested: (user: ManagedUser) => void;
}

export function UserRow({
  user,
  isSelf,
  onRoleChanged,
  onSetPasswordRequested,
  onDeactivateRequested,
  onReactivateRequested
}: UserRowProps) {
  const [changingRole, setChangingRole] = useState(false);
  const deactivatedAt = user.deactivated_at;
  const isDeactivated = deactivatedAt !== null;

  const changeRole = async (role: UserRole) => {
    if (role === user.role) {
      return;
    }
    setChangingRole(true);
    try {
      await api.patchUser(user.id, { role });
      onRoleChanged();
    } catch (caught) {
      // The last-owner guard (409) and any other rejection both surface the
      // server's own message, which already names the problem — "cannot
      // remove the last owner" is not a generic failure.
      toast.error(`Could not change ${user.email}'s role`, { description: messageFor(caught) });
    } finally {
      setChangingRole(false);
    }
  };

  return (
    <TableRow className={cn(isDeactivated && "text-muted-foreground")}>
      <TableCell className="font-medium">
        {user.name}
        {isSelf && <span className="text-muted-foreground font-normal"> (you)</span>}
      </TableCell>

      <TableCell className="max-w-0 truncate" title={user.email}>
        {user.email}
      </TableCell>

      <TableCell>
        <Select value={user.role} onValueChange={(value) => void changeRole(value as UserRole)} disabled={changingRole}>
          <SelectTrigger size="sm" aria-label={`Role for ${user.email}`} className="w-28">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="owner">Owner</SelectItem>
            <SelectItem value="member">Member</SelectItem>
          </SelectContent>
        </Select>
      </TableCell>

      <TableCell>
        {deactivatedAt !== null ? (
          <span className="flex items-center gap-2">
            <Badge variant="outline">
              <Ban aria-hidden="true" />
              Deactivated
            </Badge>
            <RelativeTime iso={deactivatedAt} className="text-muted-foreground text-xs" />
          </span>
        ) : (
          <Badge variant="outline">
            <CircleCheck className="text-status-resolved" aria-hidden="true" />
            Active
          </Badge>
        )}
      </TableCell>

      <TableCell className="text-right">
        {/* The signed-in user's own row offers no action here — the server
            rejects both self-deactivation and self-password-reset by design
            — so the cell is empty rather than a disabled control with no
            stated reason. */}
        {!isSelf && isDeactivated && (
          <div className="flex justify-end gap-2">
            <Button
              type="button"
              size="sm"
              onClick={() => onReactivateRequested(user)}
              aria-label={`Reactivate ${user.email}`}
            >
              Reactivate
            </Button>
          </div>
        )}
        {!isSelf && !isDeactivated && (
          <div className="flex justify-end gap-2">
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => onSetPasswordRequested(user)}
              aria-label={`Set password for ${user.email}`}
            >
              Set password
            </Button>
            <Button
              type="button"
              variant="outline"
              size="sm"
              className="text-destructive hover:text-destructive"
              onClick={() => onDeactivateRequested(user)}
              aria-label={`Deactivate ${user.email}`}
            >
              Deactivate
            </Button>
          </div>
        )}
      </TableCell>
    </TableRow>
  );
}
