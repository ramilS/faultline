import { render, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";

import type { Project } from "@/api";

vi.mock("@/apiClient", () => ({
  api: new Proxy({}, { get: () => vi.fn(() => new Promise<never>(() => undefined)) })
}));

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

import { IssueStreamPage } from "./IssueStreamPage";

describe("IssueStreamPage", () => {
  it("titles the browser tab with the project's name", async () => {
    render(
      <MemoryRouter initialEntries={["/muslicast-ios/issues"]}>
        <Routes>
          <Route path="/:slug/issues" element={<IssueStreamPage />} />
        </Routes>
      </MemoryRouter>
    );

    await waitFor(() => expect(document.title).toBe("Issues · Muslicast iOS · Faultline"));
  });
});
