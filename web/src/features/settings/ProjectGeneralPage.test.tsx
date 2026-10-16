import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

const signedIn = vi.hoisted(() => ({ role: "owner" as "owner" | "member" }));
const listed = vi.hoisted(() => ({ reload: vi.fn() }));

vi.mock("@/features/auth/SessionProvider", () => ({
  useSession: () => ({
    session: { state: "authenticated", user: { id: 1, name: "Ada", email: "ada@example.com", role: signedIn.role } }
  })
}));

vi.mock("@/features/projects/ProjectsProvider", () => ({
  useProjects: () => ({
    state: {
      status: "ready",
      projects: [{ id: 3, slug: "acme", name: "Acme", platform: "javascript", created_at: "2026-07-13T08:00:00Z" }]
    },
    reload: listed.reload
  })
}));

vi.mock("@/apiClient", () => ({
  api: { updateProject: vi.fn() }
}));

import { ApiError } from "@/api";
import { api } from "@/apiClient";

import { ProjectGeneralPage } from "./ProjectGeneralPage";

const updateProject = vi.mocked(api.updateProject);

// Radix Select opens on pointerdown and probes pointer capture and scrolling
// APIs that jsdom does not implement.
Element.prototype.hasPointerCapture ??= () => false;
Element.prototype.setPointerCapture ??= () => {};
Element.prototype.releasePointerCapture ??= () => {};
Element.prototype.scrollIntoView ??= () => {};

function renderPage(slug = "acme") {
  return render(
    <MemoryRouter initialEntries={[`/${slug}/settings/general`]}>
      <Routes>
        <Route path="/:slug/settings/general" element={<ProjectGeneralPage />} />
      </Routes>
    </MemoryRouter>
  );
}

function choosePlatform(label: string) {
  fireEvent.pointerDown(screen.getByLabelText("Platform"), { button: 0, ctrlKey: false, pointerType: "mouse" });
  fireEvent.click(screen.getByRole("option", { name: label }));
}

describe("ProjectGeneralPage", () => {
  beforeEach(() => {
    signedIn.role = "owner";
    updateProject.mockReset();
    listed.reload.mockReset().mockResolvedValue(undefined);
  });

  it("saves the edited name and platform under the unchanged slug and refreshes the shared list", async () => {
    updateProject.mockResolvedValue({ id: 3, slug: "acme", name: "Storefront", platform: "go", created_at: "2026-07-13T08:00:00Z" });

    renderPage();
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "Storefront" } });
    choosePlatform("Go");
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(updateProject).toHaveBeenCalledWith("acme", { name: "Storefront", platform: "go" }));
    expect(await screen.findByText("Saved.")).toBeDefined();
    expect(listed.reload).toHaveBeenCalledTimes(1);
  });

  it("saves a selected icon with other project settings", async () => {
    updateProject.mockResolvedValue({ id: 3, slug: "acme", name: "Acme", platform: "javascript", icon_emoji: "🚀", created_at: "2026-07-13T08:00:00Z" });
    renderPage();
    fireEvent.click(screen.getByRole("button", { name: "Use 🚀" }));
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(updateProject).toHaveBeenCalledWith("acme", { name: "Acme", platform: "javascript", icon_emoji: "🚀" }));
  });

  it("shows the server's error and does not claim the save", async () => {
    updateProject.mockRejectedValue(new ApiError(400, "invalid project"));

    renderPage();
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    expect((await screen.findByRole("alert")).textContent).toContain("invalid project");
    expect(screen.queryByText("Saved.")).toBeNull();
    expect(listed.reload).not.toHaveBeenCalled();
  });

  it("never offers the slug for editing", () => {
    renderPage();

    expect(screen.getByLabelText("Slug")).toHaveProperty("disabled", true);
  });

  it("shows a member the settings read-only with no save button", () => {
    signedIn.role = "member";

    renderPage();

    expect(screen.getByLabelText("Name")).toHaveProperty("disabled", true);
    expect(screen.queryByRole("button", { name: "Save" })).toBeNull();
  });

  it("renders not-found for a project outside the caller's list", () => {
    renderPage("secret");

    expect(screen.queryByLabelText("Name")).toBeNull();
    expect(screen.getByText(/not found/i)).toBeDefined();
  });
});
