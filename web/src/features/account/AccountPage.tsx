import { PageHeader } from "@/components/PageHeader";
import { ChangePasswordForm } from "@/features/account/ChangePasswordForm";
import { useSession } from "@/features/auth/SessionProvider";
import { useDocumentTitle } from "@/hooks/useDocumentTitle";

const MEMBER_ROLE_EXPLANATION =
  "Members can read and triage. Ingest keys, grouping rules and users are managed by an owner.";

/**
 * Every signed-in user's own account screen: read-only identity, the role
 * and what it permits, and self-service password change. Reachable by both
 * roles (`globalNavItems`) and carries no owner-only control to hide — the
 * "hide or explain" rule for owner-only screens is satisfied here by stating, for a member, what
 * the role does and does not permit, which is what makes the *absence* of
 * the Users nav entry legible rather than a hidden failure.
 */
export function AccountPage() {
  const { session } = useSession();
  useDocumentTitle("Account");

  if (session.state !== "authenticated") {
    // AccountPage only ever renders inside RequireSession, so this is
    // unreachable in practice; it keeps the type honest without a cast.
    return null;
  }

  const { user } = session;

  return (
    <>
      <PageHeader
        title="Account"
        description={user.role === "member" ? MEMBER_ROLE_EXPLANATION : undefined}
      />

      <div className="max-w-sm space-y-8 p-4 sm:p-6">
        <section className="space-y-3" aria-labelledby="account-details-heading">
          <h2 id="account-details-heading" className="text-sm font-medium">
            Your details
          </h2>
          <dl className="space-y-2 text-sm">
            <div className="flex justify-between gap-4">
              <dt className="text-muted-foreground">Name</dt>
              <dd className="text-right">{user.name}</dd>
            </div>
            <div className="flex justify-between gap-4">
              <dt className="text-muted-foreground">Email</dt>
              <dd className="text-right font-mono break-all">{user.email}</dd>
            </div>
            <div className="flex justify-between gap-4">
              <dt className="text-muted-foreground">Role</dt>
              <dd className="text-right capitalize">{user.role}</dd>
            </div>
          </dl>
        </section>

        <section className="space-y-3" aria-labelledby="change-password-heading">
          <h2 id="change-password-heading" className="text-sm font-medium">
            Change password
          </h2>
          <p className="text-muted-foreground text-sm">
            Changing your password signs you out everywhere else. This session stays open.
          </p>
          <ChangePasswordForm />
        </section>
      </div>
    </>
  );
}
