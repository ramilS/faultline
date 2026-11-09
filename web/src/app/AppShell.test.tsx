import { render, screen, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";

import type { Project } from "@/api";

const projects: Project[] = [
  {
    id: 1,
    slug: "muslicast-ios",
    name: "Muslicast iOS",
    platform: "apple-ios",
    source_link_template: "",
    source_stack_root: "",
    source_root: "",
    created_at: "2026-09-01T00:00:00Z"
  }
];

vi.mock("@/features/projects/ProjectsProvider", () => ({
  useProjects: () => ({ state: { status: "ready", projects }, reload: vi.fn() })
}));

vi.mock("@/features/auth/SessionProvider", () => ({
  useSession: () => ({
    session: { state: "authenticated", user: { id: 1, role: "owner", email: "owner@example.com" } },
    signOut: vi.fn()
  })
}));

vi.mock("@/features/theme/ThemeProvider", () => ({
  useTheme: () => ({ preference: "light", resolved: "light", setPreference: vi.fn() })
}));

import { AppShell } from "./AppShell";

describe("AppShell", () => {
  it("heads the project's navigation with its name, not its slug", () => {
    render(
      <MemoryRouter initialEntries={["/muslicast-ios/issues"]}>
        <AppShell />
      </MemoryRouter>
    );

    const nav = screen.getByRole("navigation", { name: "Main" });
    expect(within(nav).getByText("Muslicast iOS")).toBeDefined();
    expect(within(nav).queryByText("muslicast-ios")).toBeNull();
  });
});
