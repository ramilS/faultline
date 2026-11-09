import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

const projects = vi.hoisted(() => ({
  state: { status: "loading" } as
    | { status: "loading" }
    | { status: "ready"; projects: Array<{ id: number; slug: string; name: string }> }
}));

vi.mock("@/features/projects/ProjectsProvider", () => ({
  useProjects: () => ({ state: projects.state, reload: vi.fn() })
}));

import { useProjectName } from "./useProjectName";

function ProjectName({ slug }: { slug: string }) {
  return <span>{useProjectName(slug)}</span>;
}

describe("useProjectName", () => {
  it.each([
    { scenario: "a listed project", state: { status: "ready" as const, projects: [{ id: 1, slug: "android-prod", name: "Android" }] }, shown: "Android" },
    { scenario: "a slug outside the list", state: { status: "ready" as const, projects: [{ id: 1, slug: "web", name: "Web" }] }, shown: "android-prod" },
    { scenario: "projects still loading", state: { status: "loading" as const }, shown: "android-prod" }
  ])("shows $shown for $scenario", ({ state, shown }) => {
    projects.state = state;
    render(<ProjectName slug="android-prod" />);
    expect(screen.getByText(shown)).toBeDefined();
  });
});
