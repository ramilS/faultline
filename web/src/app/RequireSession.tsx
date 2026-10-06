import { Loader2, ServerCrash } from "lucide-react";
import type { ReactNode } from "react";
import { Navigate, useLocation } from "react-router-dom";

import { useSession } from "@/features/auth/SessionProvider";
import { Button } from "@/components/ui/button";
import { ThemeToggle } from "@/features/theme/ThemeToggle";

/**
 * Gates the authenticated area of the dashboard.
 *
 * This decides what to *render*, never what is permitted. The server's
 * `RequireSession` middleware (`internal/api/middleware.go:25`) remains the
 * only authorization boundary, and every screen must still expect a 401.
 */
export function RequireSession({ children }: { children: ReactNode }) {
  const { session, retryProbe } = useSession();
  const location = useLocation();

  // Rendering children optimistically would fire a burst of requests that all
  // 401 — the behaviour this whole phase exists to remove.
  if (session.state === "loading") {
    return (
      <div className="flex min-h-dvh items-center justify-center" role="status" aria-live="polite">
        <Loader2 className="text-muted-foreground size-5 animate-spin" aria-hidden="true" />
        <span className="sr-only">Checking your session…</span>
      </div>
    );
  }

  // A failing server is not a sign-out. Sending the administrator to a login
  // form that cannot succeed would hide the real fault.
  if (session.state === "unavailable") {
    return (
      <div className="relative flex min-h-dvh items-center justify-center p-6">
        <div className="absolute top-4 right-4">
          <ThemeToggle variant="icon" />
        </div>
        <div className="max-w-md text-center">
          <ServerCrash className="text-muted-foreground mx-auto size-8" aria-hidden="true" />
          <h1 className="mt-4 text-lg font-semibold">Faultline is unreachable</h1>
          <p className="text-muted-foreground mt-2 text-sm">{session.message}</p>
          <Button className="mt-6" onClick={retryProbe}>
            Try again
          </Button>
        </div>
      </div>
    );
  }

  if (session.state === "anonymous") {
    const intended = `${location.pathname}${location.search}`;
    return <Navigate to={`/login?next=${encodeURIComponent(intended)}`} replace />;
  }

  return <>{children}</>;
}
