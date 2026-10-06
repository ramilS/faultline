import { ShieldAlert } from "lucide-react";
import type { ReactNode } from "react";

import { EmptyState } from "@/components/EmptyState";
import { useSession } from "@/features/auth/SessionProvider";

interface RequireOwnerProps {
  children: ReactNode;
  /**
   * What the caller was denied, named the way the screen's own copy names it
   * — "user accounts", "groups". Interpolated into "Only an owner can manage
   * {subject}.", so pick a phrase that reads naturally there.
   */
  subject?: string;
}

/**
 * Gates an owner-only screen with an explanation, not a redirect.
 *
 * A member who follows a shared link to an owner-only screen must
 * see why, not bounce silently to /projects — that reads as a broken link.
 * The nav entry is filtered separately (`globalNavItems`) so a member never
 * finds this screen by clicking; this guard exists for the member who
 * arrives anyway, by URL or bookmark.
 *
 * Guards two screens today — `/settings/users` and `/settings/groups` — so
 * the denial names its subject rather than assuming "user accounts" for
 * both; the caller passes `subject` for anything but the users screen.
 *
 * This decides what to *render*, never what is permitted — the server's
 * owner-only checks (`internal/api/users.go`) remain the only authorization
 * boundary.
 */
export function RequireOwner({ children, subject = "user accounts" }: RequireOwnerProps) {
  const { session } = useSession();

  if (session.state === "authenticated" && session.user.role === "owner") {
    return <>{children}</>;
  }

  return (
    <EmptyState
      icon={ShieldAlert}
      title="Owner access required"
      description={`Only an owner can manage ${subject}. Ask an owner to make the change you need, or to grant you the owner role.`}
    />
  );
}
