import { useCallback, useState } from "react";

import type { ManagedUser } from "@/api";
import { api } from "@/apiClient";
import { ErrorState } from "@/components/ErrorState";
import { PageHeader } from "@/components/PageHeader";
import { STACKED_ROWS_BELOW_MD } from "@/components/stackedTable";
import { Skeleton } from "@/components/ui/skeleton";
import { Table, TableBody, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useSession } from "@/features/auth/SessionProvider";
import { CreateUserDialog } from "@/features/users/CreateUserDialog";
import { DeactivateUserDialog } from "@/features/users/DeactivateUserDialog";
import { ReactivateUserDialog } from "@/features/users/ReactivateUserDialog";
import { SetPasswordDialog } from "@/features/users/SetPasswordDialog";
import { UserRow } from "@/features/users/UserRow";
import { useAsyncResource } from "@/hooks/useAsyncResource";
import { useDocumentTitle } from "@/hooks/useDocumentTitle";

/**
 * Owner-only account administration. Reachable only through `RequireOwner`
 * (see `web/src/app/RequireOwner.tsx`), which is the actual access gate —
 * this component assumes it is already an owner's screen.
 *
 * No empty state: the signed-in owner is always in the list, so `ready` with
 * zero rows cannot happen — unlike KeysPage, which this screen otherwise
 * mirrors for loading and failure.
 */
export function UsersPage() {
  const { session } = useSession();
  useDocumentTitle("Users");
  const { state, reload } = useAsyncResource<ManagedUser[]>(useCallback(() => api.listUsers(), []));
  const [passwordTarget, setPasswordTarget] = useState<ManagedUser | null>(null);
  const [deactivateTarget, setDeactivateTarget] = useState<ManagedUser | null>(null);
  const [reactivateTarget, setReactivateTarget] = useState<ManagedUser | null>(null);

  const selfId = session.state === "authenticated" ? session.user.id : null;

  return (
    <>
      <PageHeader title="Users" description="Manage who can sign in and what they can do.">
        <CreateUserDialog onCreated={() => void reload({ silent: true })} />
      </PageHeader>

      <div className="space-y-4 p-4 sm:p-6">
        {state.status === "loading" && (
          <div className="space-y-2">
            {[0, 1, 2].map((row) => (
              <Skeleton key={row} className="h-12 w-full" />
            ))}
          </div>
        )}

        {state.status === "failed" && <ErrorState message={state.message} onRetry={() => void reload()} />}

        {state.status === "ready" && (
          <Table className={STACKED_ROWS_BELOW_MD}>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Email</TableHead>
                <TableHead>Role</TableHead>
                <TableHead>Status</TableHead>
                <TableHead className="w-0 text-right">
                  <span className="sr-only">Actions</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {state.value.map((user) => (
                <UserRow
                  key={user.id}
                  user={user}
                  isSelf={user.id === selfId}
                  onRoleChanged={() => void reload({ silent: true })}
                  onSetPasswordRequested={setPasswordTarget}
                  onDeactivateRequested={setDeactivateTarget}
                  onReactivateRequested={setReactivateTarget}
                />
              ))}
            </TableBody>
          </Table>
        )}
      </div>

      <SetPasswordDialog target={passwordTarget} onClose={() => setPasswordTarget(null)} />
      <DeactivateUserDialog
        target={deactivateTarget}
        onClose={() => setDeactivateTarget(null)}
        onDeactivated={() => void reload({ silent: true })}
      />
      <ReactivateUserDialog
        target={reactivateTarget}
        onClose={() => setReactivateTarget(null)}
        onReactivated={() => void reload({ silent: true })}
      />
    </>
  );
}
