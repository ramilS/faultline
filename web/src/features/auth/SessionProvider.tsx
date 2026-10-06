import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { toast } from "sonner";

import type { Credentials } from "@/api";
import { api, setUnauthorizedListener } from "@/apiClient";
import { messageFor } from "@/errors";
import { classifyProbeFailure, sessionFromProbe, type Session } from "@/lib/session";

interface SessionContextValue {
  readonly session: Session;
  signIn: (credentials: Credentials) => Promise<void>;
  signOut: () => Promise<void>;
  retryProbe: () => void;
}

const SessionContext = createContext<SessionContextValue | null>(null);

export function SessionProvider({ children }: { children: ReactNode }) {
  const [session, setSession] = useState<Session>({ state: "loading" });
  const [probeToken, setProbeToken] = useState(0);

  useEffect(() => {
    let active = true;
    setSession({ state: "loading" });

    void (async () => {
      try {
        const user = await api.me();
        if (active) {
          setSession(sessionFromProbe({ kind: "user", user }));
        }
      } catch (caught) {
        if (active) {
          setSession(sessionFromProbe(classifyProbeFailure(caught)));
        }
      }
    })();

    return () => {
      active = false;
    };
  }, [probeToken]);

  // Any 401 from any other endpoint means the cookie died mid-session.
  useEffect(() => {
    setUnauthorizedListener(() => setSession({ state: "anonymous" }));
    return () => setUnauthorizedListener(null);
  }, []);

  const signIn = useCallback(async (credentials: Credentials) => {
    // Errors propagate: the login form distinguishes 401 from 429 and needs
    // the ApiError, so this must not collapse them into a session state.
    const user = await api.login(credentials);
    setSession({ state: "authenticated", user });
  }, []);

  const signOut = useCallback(async () => {
    try {
      await api.logout();
    } catch (caught) {
      // The server clears the cookie before it can fail
      // (`internal/api/auth.go:128`), so this browser is signed out either
      // way — but the server-side session row may survive until it expires.
      // That is worth saying rather than swallowing.
      toast.error("Signed out locally, but the server could not revoke the session.", {
        description: messageFor(caught)
      });
    } finally {
      // Leaving the UI "signed in" against a cleared cookie would be the
      // zombie-session bug, so this runs on both paths.
      setSession({ state: "anonymous" });
    }
  }, []);

  const retryProbe = useCallback(() => setProbeToken((token) => token + 1), []);

  const value = useMemo<SessionContextValue>(
    () => ({ session, signIn, signOut, retryProbe }),
    [session, signIn, signOut, retryProbe]
  );

  return <SessionContext value={value}>{children}</SessionContext>;
}

export function useSession(): SessionContextValue {
  const value = useContext(SessionContext);
  if (value === null) {
    throw new Error("useSession must be used within a SessionProvider");
  }
  return value;
}
