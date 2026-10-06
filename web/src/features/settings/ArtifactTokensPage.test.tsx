import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

const signedIn = vi.hoisted(() => ({ role: "owner" as "owner" | "member" }));

vi.mock("@/features/auth/SessionProvider", () => ({
  useSession: () => ({
    session: { state: "authenticated", user: { id: 1, name: "Ada", email: "ada@example.com", role: signedIn.role } }
  })
}));

vi.mock("@/lib/clipboard", () => ({ copyToClipboard: vi.fn() }));

vi.mock("@/apiClient", () => ({
  api: {
    listArtifactTokens: vi.fn(),
    createArtifactToken: vi.fn(),
    revokeArtifactToken: vi.fn()
  }
}));

import { ApiError, type ArtifactToken } from "@/api";
import { api } from "@/apiClient";

import { ArtifactTokensPage } from "./ArtifactTokensPage";

const listArtifactTokens = vi.mocked(api.listArtifactTokens);
const createArtifactToken = vi.mocked(api.createArtifactToken);
const revokeArtifactToken = vi.mocked(api.revokeArtifactToken);

function buildToken(overrides: Partial<ArtifactToken> = {}): ArtifactToken {
  return {
    id: 3,
    project_id: 1,
    name: "gradle",
    token_prefix: "fla_ab12",
    created_at: "2026-09-28T08:00:00Z",
    last_used_at: null,
    revoked_at: null,
    ...overrides
  };
}

function renderPage() {
  return render(
    <MemoryRouter initialEntries={["/acme/settings/artifact-tokens"]}>
      <Routes>
        <Route path="/:slug/settings/artifact-tokens" element={<ArtifactTokensPage />} />
        <Route path="/projects" element={<p>projects screen</p>} />
      </Routes>
    </MemoryRouter>
  );
}

describe("ArtifactTokensPage", () => {
  beforeEach(() => {
    signedIn.role = "owner";
    listArtifactTokens.mockReset();
    createArtifactToken.mockReset();
    revokeArtifactToken.mockReset();
  });

  it("lists each token by name and prefix, never a raw value", async () => {
    listArtifactTokens.mockResolvedValue([buildToken({ name: "ci-release", token_prefix: "fla_zz99" })]);

    renderPage();

    expect(await screen.findByText("ci-release")).toBeDefined();
    expect(screen.getByText("fla_zz99…")).toBeDefined();
    expect(listArtifactTokens).toHaveBeenCalledWith("acme");
  });

  it("says a token that has never been used has never been used", async () => {
    listArtifactTokens.mockResolvedValue([buildToken({ last_used_at: null })]);

    renderPage();

    expect(await screen.findByText("Never")).toBeDefined();
  });

  it("offers an owner the first token when there are none", async () => {
    listArtifactTokens.mockResolvedValue([]);

    renderPage();

    expect(await screen.findByText("No artifact tokens yet")).toBeDefined();
    expect(screen.getAllByRole("button", { name: "New artifact token" }).length).toBeGreaterThan(0);
  });

  it("reveals the raw token once after creating it and refreshes the list", async () => {
    listArtifactTokens.mockResolvedValueOnce([]).mockResolvedValueOnce([buildToken({ name: "gradle" })]);
    createArtifactToken.mockResolvedValue({ token: buildToken({ name: "gradle" }), raw_token: "fla_the_raw_secret" });
    renderPage();
    await screen.findByText("No artifact tokens yet");

    fireEvent.click(screen.getAllByRole("button", { name: "New artifact token" })[0]);
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "  gradle  " } });
    fireEvent.click(screen.getByRole("button", { name: "Create token" }));

    expect(await screen.findByText("fla_the_raw_secret")).toBeDefined();
    expect(createArtifactToken).toHaveBeenCalledWith("acme", "gradle");
    await waitFor(() => expect(listArtifactTokens).toHaveBeenCalledTimes(2));
    expect(await screen.findByText("fla_ab12…")).toBeDefined();
  });

  it("asks for a name instead of sending a blank one", async () => {
    listArtifactTokens.mockResolvedValue([]);
    renderPage();
    await screen.findByText("No artifact tokens yet");

    fireEvent.click(screen.getAllByRole("button", { name: "New artifact token" })[0]);
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "   " } });
    fireEvent.click(screen.getByRole("button", { name: "Create token" }));

    expect(await screen.findByRole("alert")).toBeDefined();
    expect(createArtifactToken).not.toHaveBeenCalled();
  });

  it("shows the server's reason when creation fails, without revealing anything", async () => {
    listArtifactTokens.mockResolvedValue([]);
    createArtifactToken.mockRejectedValue(new ApiError(500, "internal server error", null));
    renderPage();
    await screen.findByText("No artifact tokens yet");

    fireEvent.click(screen.getAllByRole("button", { name: "New artifact token" })[0]);
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "gradle" } });
    fireEvent.click(screen.getByRole("button", { name: "Create token" }));

    expect(await screen.findByText("internal server error")).toBeDefined();
    expect(screen.queryByText("New artifact token created")).toBeNull();
  });

  it("revokes only after confirmation, then refreshes the list", async () => {
    listArtifactTokens
      .mockResolvedValueOnce([buildToken({ id: 3 })])
      .mockResolvedValueOnce([buildToken({ id: 3, revoked_at: "2026-09-29T08:00:00Z" })]);
    revokeArtifactToken.mockResolvedValue(undefined);
    renderPage();

    fireEvent.click(await screen.findByRole("button", { name: "Revoke gradle" }));
    expect(revokeArtifactToken).not.toHaveBeenCalled();
    fireEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Revoke token" }));

    await waitFor(() => expect(revokeArtifactToken).toHaveBeenCalledWith("acme", 3));
    expect(await screen.findByText("Revoked")).toBeDefined();
    expect(screen.queryByRole("button", { name: "Revoke gradle" })).toBeNull();
  });

  it("leaves a revoked token with no action", async () => {
    listArtifactTokens.mockResolvedValue([buildToken({ revoked_at: "2026-09-29T08:00:00Z" })]);

    renderPage();

    expect(await screen.findByText("Revoked")).toBeDefined();
    expect(screen.queryByRole("button", { name: /Revoke/ })).toBeNull();
  });

  it("lets a member read the list but neither create nor revoke, and says why", async () => {
    signedIn.role = "member";
    listArtifactTokens.mockResolvedValue([buildToken()]);

    renderPage();

    expect(await screen.findByText("gradle")).toBeDefined();
    expect(screen.queryByRole("button", { name: /New artifact token/ })).toBeNull();
    expect(screen.queryByRole("button", { name: /Revoke/ })).toBeNull();
    expect(screen.getByText(/managed by an owner/)).toBeDefined();
  });

  it("renders the not-found screen when the project is outside the caller's scope", async () => {
    listArtifactTokens.mockRejectedValue(new ApiError(404, "project not found", null));

    renderPage();

    expect(await screen.findByText("Page not found")).toBeDefined();
  });
});
