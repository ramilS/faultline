import { fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";

const listed = vi.hoisted(() => ({ projects: [] as Array<{ id: number; slug: string; name: string }> }));

vi.mock("@/features/projects/ProjectsProvider", () => ({
  useProjects: () => ({ state: { status: "ready", projects: listed.projects }, reload: vi.fn() })
}));

import { ProjectSwitcher } from "./ProjectSwitcher";

Element.prototype.scrollIntoView ??= () => {};

function openSwitcher(projects: typeof listed.projects) {
  listed.projects = projects;
  render(
    <MemoryRouter initialEntries={["/issues"]}>
      <ProjectSwitcher />
    </MemoryRouter>
  );
  fireEvent.click(screen.getByRole("combobox", { name: "Switch project" }));
}

describe("project switcher", () => {
  it("lists unique names without slugs", () => {
    openSwitcher([
      { id: 1, slug: "android-prod", name: "Android" },
      { id: 2, slug: "web", name: "Web" }
    ]);
    expect(screen.getByRole("option", { name: "Android" })).toBeDefined();
    expect(screen.getByRole("option", { name: "Web" })).toBeDefined();
    expect(screen.queryByText("android-prod")).toBeNull();
    expect(screen.queryByText("web")).toBeNull();
  });

  it("shows the slug only for projects that share a name", () => {
    openSwitcher([
      { id: 1, slug: "android-prod", name: "Android" },
      { id: 2, slug: "android-beta", name: "Android" },
      { id: 3, slug: "web", name: "Web" }
    ]);
    expect(screen.getByText("android-prod")).toBeDefined();
    expect(screen.getByText("android-beta")).toBeDefined();
    expect(screen.queryByText("web")).toBeNull();
  });

  it("finds a project by its slug", () => {
    openSwitcher([
      { id: 1, slug: "android-prod", name: "Android" },
      { id: 2, slug: "web", name: "Web" }
    ]);
    fireEvent.change(screen.getByPlaceholderText("Find project…"), { target: { value: "android-prod" } });
    expect(screen.getByRole("option", { name: "Android" })).toBeDefined();
    expect(screen.queryByRole("option", { name: "Web" })).toBeNull();
  });
});
