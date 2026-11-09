import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

const signedIn = vi.hoisted(() => ({ role: "owner" as "owner" | "member" }));
const listed = vi.hoisted(() => ({
  reload: vi.fn(),
  links: { source_link_template: "", source_stack_root: "", source_root: "" }
}));

vi.mock("@/features/auth/SessionProvider", () => ({
  useSession: () => ({
    session: { state: "authenticated", user: { id: 1, name: "Ada", email: "ada@example.com", role: signedIn.role } }
  })
}));

vi.mock("@/features/projects/ProjectsProvider", () => ({
  useProjects: () => ({
    state: {
      status: "ready",
      projects: [{ id: 3, slug: "acme", name: "Acme", platform: "javascript", created_at: "2026-07-13T08:00:00Z", ...listed.links }]
    },
    reload: listed.reload
  })
}));

vi.mock("@/apiClient", () => ({
  api: { updateProject: vi.fn(), updateSourceLinks: vi.fn() }
}));

import { ApiError } from "@/api";
import { api } from "@/apiClient";

import { ProjectGeneralPage } from "./ProjectGeneralPage";

const updateProject = vi.mocked(api.updateProject);
const updateSourceLinks = vi.mocked(api.updateSourceLinks);

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
    listed.links = { source_link_template: "", source_stack_root: "", source_root: "" };
    updateProject.mockReset();
    updateSourceLinks.mockReset();
    listed.reload.mockReset().mockResolvedValue(undefined);
  });

  it("saves the edited name and platform under the unchanged slug and refreshes the shared list", async () => {
    updateProject.mockResolvedValue({ id: 3, slug: "acme", name: "Storefront", platform: "go", source_link_template: "", source_stack_root: "", source_root: "", created_at: "2026-07-13T08:00:00Z" });

    renderPage();
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "Storefront" } });
    choosePlatform("Go");
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(updateProject).toHaveBeenCalledWith("acme", { name: "Storefront", platform: "go" }));
    expect(await screen.findByText("Saved.")).toBeDefined();
    expect(listed.reload).toHaveBeenCalledTimes(1);
  });

  it("saves a selected icon with other project settings", async () => {
    updateProject.mockResolvedValue({ id: 3, slug: "acme", name: "Acme", platform: "javascript", icon_emoji: "🚀", source_link_template: "", source_stack_root: "", source_root: "", created_at: "2026-07-13T08:00:00Z" });
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

  describe("source code links", () => {
    const template = "https://github.com/acme/shop/blob/main/{path}#L{line}";

    it("saves the template and both roots and refreshes the shared list", async () => {
      updateSourceLinks.mockResolvedValue({
        id: 3, slug: "acme", name: "Acme", platform: "javascript", created_at: "2026-07-13T08:00:00Z",
        source_link_template: template, source_stack_root: "/app/", source_root: "src/"
      });

      renderPage();
      fireEvent.change(screen.getByLabelText("Link template"), { target: { value: template } });
      fireEvent.change(screen.getByLabelText("Stack root"), { target: { value: "/app/" } });
      fireEvent.change(screen.getByLabelText("Source root"), { target: { value: "src/" } });
      fireEvent.click(screen.getByRole("button", { name: "Save source links" }));

      await waitFor(() =>
        expect(updateSourceLinks).toHaveBeenCalledWith("acme", {
          source_link_template: template,
          source_stack_root: "/app/",
          source_root: "src/"
        })
      );
      expect(await screen.findByText("Saved.")).toBeDefined();
      expect(listed.reload).toHaveBeenCalledTimes(1);
      expect(updateProject).not.toHaveBeenCalled();
    });

    it("previews the link a sample frame under the stack root would get", () => {
      renderPage();
      fireEvent.change(screen.getByLabelText("Link template"), { target: { value: template } });
      fireEvent.change(screen.getByLabelText("Stack root"), { target: { value: "/app/" } });
      fireEvent.change(screen.getByLabelText("Source root"), { target: { value: "src/" } });

      const preview = screen.getByRole("link", { name: /blob\/main\/src/ });
      expect(preview.getAttribute("href")).toBe("https://github.com/acme/shop/blob/main/src/shop/cart.py#L42");
      expect(preview.getAttribute("target")).toBe("_blank");
      expect(preview.getAttribute("rel")).toBe("noopener noreferrer");
      expect(screen.getByText(/\/app\/shop\/cart\.py:42/)).toBeDefined();
    });

    it("explains why a template that cannot become a link has no preview", () => {
      renderPage();
      fireEvent.change(screen.getByLabelText("Link template"), { target: { value: "javascript:alert(1)//{path}" } });

      expect(screen.queryByRole("link")).toBeNull();
      expect(screen.getByText(/absolute https:\/\/ or http:\/\/ URL containing \{path\}/)).toBeDefined();
    });

    it("shows the server's message naming the rejected field and keeps the form as typed", async () => {
      updateSourceLinks.mockRejectedValue(new ApiError(400, "source_link_template must contain {path}"));

      renderPage();
      fireEvent.change(screen.getByLabelText("Link template"), { target: { value: "https://github.com/acme/shop" } });
      fireEvent.click(screen.getByRole("button", { name: "Save source links" }));

      expect((await screen.findByRole("alert")).textContent).toContain("source_link_template must contain {path}");
      expect(screen.queryByText("Saved.")).toBeNull();
      expect(listed.reload).not.toHaveBeenCalled();
      expect(screen.getByLabelText("Link template")).toHaveProperty("value", "https://github.com/acme/shop");
    });

    it("shows a member the configured setting read-only with no save button", () => {
      signedIn.role = "member";
      listed.links = { source_link_template: template, source_stack_root: "/app/", source_root: "src/" };

      renderPage();

      expect(screen.getByLabelText("Link template")).toHaveProperty("value", template);
      expect(screen.getByLabelText("Link template")).toHaveProperty("disabled", true);
      expect(screen.getByLabelText("Stack root")).toHaveProperty("disabled", true);
      expect(screen.getByLabelText("Source root")).toHaveProperty("disabled", true);
      expect(screen.queryByRole("button", { name: "Save source links" })).toBeNull();
    });
  });
});
