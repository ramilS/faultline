import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

// LoginPage's failureMessage() branches on `caught instanceof ApiError` to
// produce the 401/429 copy, so this must preserve the real ApiError rather
// than replace the whole module — a bare vi.mock("@/api") would break that
// `instanceof` check silently.
vi.mock("@/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/api")>()),
  fetchAuthProviders: vi.fn()
}));

// SessionProvider probes `/auth/me` on mount through the shared apiClient.
// Mocking it here is the smallest way to render a real SessionProvider
// (LoginPage needs the real one, not a stubbed useSession, since it also
// reads `session.state` to redirect an already-authenticated visitor) without
// making a real network call in jsdom.
vi.mock("@/apiClient", () => ({
  api: { me: vi.fn().mockRejectedValue(new Error("not signed in")) },
  setUnauthorizedListener: vi.fn()
}));

// LoginPage renders ThemeToggle unconditionally, which needs a ThemeProvider
// that in turn calls `window.matchMedia` — jsdom does not implement it. The
// toggle is not under test here, so it is stubbed out rather than standing up
// a ThemeProvider (and a matchMedia polyfill) just to satisfy it.
vi.mock("@/features/theme/ThemeToggle", () => ({
  ThemeToggle: () => null
}));

import { ApiError, fetchAuthProviders, type AuthProviders } from "@/api";
import { SessionProvider } from "@/features/auth/SessionProvider";

import { LoginPage } from "./LoginPage";

const mockedFetchAuthProviders = vi.mocked(fetchAuthProviders);

function renderLoginPage() {
  return render(
    <MemoryRouter initialEntries={["/login"]}>
      <SessionProvider>
        <LoginPage />
      </SessionProvider>
    </MemoryRouter>
  );
}

function mockProviders(value: { sso: { enabled: boolean } }) {
  mockedFetchAuthProviders.mockResolvedValue(value);
}

function mockProvidersFailure() {
  mockedFetchAuthProviders.mockRejectedValue(new ApiError(500, "boom"));
}

describe("LoginPage", () => {
  beforeEach(() => {
    mockedFetchAuthProviders.mockReset();
  });

  it("offers SSO when the deployment has it configured", async () => {
    mockProviders({ sso: { enabled: true } });
    renderLoginPage();

    const link = await screen.findByRole("link", { name: /sign in with google/i });
    expect(link.getAttribute("href")).toBe("/api/v1/auth/sso/start");
  });

  it("shows no SSO control when the deployment has none", async () => {
    mockProviders({ sso: { enabled: false } });
    renderLoginPage();

    await screen.findByLabelText(/email/i);
    expect(screen.queryByRole("link", { name: /sign in with google/i })).toBeNull();
  });

  // A 200 whose body is missing `sso` is not hypothetical: the endpoint is
  // unauthenticated, so a proxy or a future response shape can produce one.
  // The effect's .catch() cannot help here — the request succeeded — so a
  // read of `providers.sso.enabled` throws during render and takes the
  // password form down with it, which is the opposite of what the swallowed
  // rejection above is for.
  it("keeps the password form when the providers response omits sso", async () => {
    mockedFetchAuthProviders.mockResolvedValue({} as AuthProviders);
    renderLoginPage();

    await screen.findByLabelText(/email/i);
    expect(screen.queryByRole("link", { name: /sign in with google/i })).toBeNull();
    expect(screen.getByRole("button", { name: /^sign in$/i })).not.toBeNull();
  });

  it("shows no SSO control when the providers request fails", async () => {
    mockProvidersFailure();
    renderLoginPage();

    await screen.findByLabelText(/email/i);
    expect(screen.queryByRole("link", { name: /sign in with google/i })).toBeNull();
  });
});
