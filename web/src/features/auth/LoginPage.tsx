import { AlertCircle, Eye, EyeOff, Loader2 } from "lucide-react";
import { useEffect, useState, type FormEvent } from "react";
import { Navigate, useSearchParams } from "react-router-dom";

import { ApiError, fetchAuthProviders, type AuthProviders } from "@/api";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { messageFor } from "@/errors";
import { useSession } from "@/features/auth/SessionProvider";
import { ThemeToggle } from "@/features/theme/ThemeToggle";
import { useDocumentTitle } from "@/hooks/useDocumentTitle";
import { safeNextPath } from "@/lib/nextPath";

const DEFAULT_LANDING = "/projects";

function failureMessage(caught: unknown): string {
  if (caught instanceof ApiError) {
    // Deliberately does not distinguish unknown email from wrong password.
    if (caught.status === 401) {
      return "Invalid email or password.";
    }
    if (caught.status === 429) {
      const wait = caught.retryAfterSeconds;
      return wait === null
        ? "Too many sign-in attempts. Wait a moment and try again."
        : `Too many sign-in attempts. Try again in ${wait} second${wait === 1 ? "" : "s"}.`;
    }
  }
  return messageFor(caught);
}

export function LoginPage() {
  const { session, signIn } = useSession();
  useDocumentTitle("Sign in");
  const [searchParams] = useSearchParams();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [passwordVisible, setPasswordVisible] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [providers, setProviders] = useState<AuthProviders | null>(null);

  const destination = safeNextPath(searchParams.get("next")) ?? DEFAULT_LANDING;

  // A failed request must leave `providers` at null rather than some partial
  // or error state: null already means "render no SSO control", which is the
  // only safe default for a deployment we couldn't ask.
  useEffect(() => {
    let active = true;
    fetchAuthProviders()
      .then((result) => {
        if (active) {
          setProviders(result);
        }
      })
      .catch(() => {
        // Swallowed deliberately: a broken /auth/providers call must not
        // block or break the password login form, it should just mean no
        // SSO control is offered.
      });
    return () => {
      active = false;
    };
  }, []);

  if (session.state === "authenticated") {
    return <Navigate to={destination} replace />;
  }

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setSubmitting(true);
    setError(null);
    try {
      await signIn({ email, password });
    } catch (caught) {
      setError(failureMessage(caught));
      setPassword("");
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <main className="relative flex min-h-dvh items-center justify-center p-6">
      <div className="my-8 w-full max-w-sm">
        <div className="mb-8">
          <h1 className="text-2xl font-semibold tracking-tight">Faultline</h1>
          <p className="text-muted-foreground mt-1 text-sm">Sign in to continue.</p>
        </div>

        <form className="space-y-4" onSubmit={submit} noValidate>
          {error !== null && (
            <Alert variant="destructive" role="alert">
              <AlertCircle className="size-4" aria-hidden="true" />
              <AlertDescription>{error}</AlertDescription>
            </Alert>
          )}

          <div className="space-y-2">
            <Label htmlFor="email">Email</Label>
            <Input
              id="email"
              name="email"
              type="email"
              autoComplete="email"
              autoFocus
              required
              value={email}
              onChange={(event) => setEmail(event.target.value)}
            />
          </div>

          <div className="space-y-2">
            <Label htmlFor="password">Password</Label>
            <div className="relative">
              <Input
                id="password"
                name="password"
                type={passwordVisible ? "text" : "password"}
                autoComplete="current-password"
                required
                value={password}
                onChange={(event) => setPassword(event.target.value)}
                className="pr-10"
              />
              <button
                type="button"
                onClick={() => setPasswordVisible((visible) => !visible)}
                aria-label={passwordVisible ? "Hide password" : "Show password"}
                aria-pressed={passwordVisible}
                className="text-muted-foreground hover:text-foreground absolute inset-y-0 right-0 flex w-10 items-center justify-center rounded-md"
              >
                {passwordVisible ? (
                  <EyeOff className="size-4" aria-hidden="true" />
                ) : (
                  <Eye className="size-4" aria-hidden="true" />
                )}
              </button>
            </div>
          </div>

          <Button type="submit" className="w-full" disabled={submitting}>
            {submitting && <Loader2 className="size-4 animate-spin" aria-hidden="true" />}
            {submitting ? "Signing in…" : "Sign in"}
          </Button>
        </form>

        {providers?.sso?.enabled === true && (
          <>
            <div className="text-muted-foreground my-6 text-center text-xs">or</div>
            <Button asChild variant="outline" className="w-full">
              <a href="/api/v1/auth/sso/start">Sign in with Google</a>
            </Button>
          </>
        )}

        <p className="text-muted-foreground mt-6 text-xs">
          Faultline has no public registration. Ask an owner of this instance to create your account.
        </p>
      </div>
      <div className="absolute top-4 right-4">
        <ThemeToggle variant="icon" />
      </div>
    </main>
  );
}
