import { render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

const pending = () => new Promise<never>(() => undefined);

vi.mock("@/apiClient", () => ({
  api: new Proxy(
    { getIssue: vi.fn(), recordIssueView: vi.fn() },
    { get: (target, key: string) => (key in target ? target[key as keyof typeof target] : vi.fn(pending)) }
  )
}));

vi.mock("@/features/projects/ProjectsProvider", () => ({
  useProjects: () => ({ state: { status: "loading" }, reload: vi.fn() })
}));

vi.mock("@/features/auth/SessionProvider", () => ({
  useSession: () => ({ session: { state: "authenticated", user: { id: 1, role: "member" } }, signOut: vi.fn() })
}));

import type { IssueDetail, MergedIssue } from "@/api";
import { api } from "@/apiClient";

import { IssueDetailPage } from "./IssueDetailPage";

const getIssue = vi.mocked(api.getIssue);
const recordIssueView = vi.mocked(api.recordIssueView);

function buildDetail(): IssueDetail {
  return {
    issue: {
      id: 42,
      project_slug: "acme",
      fingerprint: "fp-1",
      title: "TimeoutError: upstream timed out",
      culprit: "billing.charge_customer",
      display_title: "",
      display_culprit: "",
      level: "error",
      status: "unresolved",
      assignee: null,
      times_seen: 12,
      users_seen: 3,
      first_seen: "2026-09-01T00:00:00Z",
      last_seen: "2026-09-09T00:00:00Z",
      ignore_until: null
    },
    environments: [],
    releases: [],
    tags: [],
    regression: null
  };
}

function renderPage() {
  return render(
    <MemoryRouter initialEntries={["/acme/issues/42"]}>
      <Routes>
        <Route path="/:slug/issues/:issueId" element={<IssueDetailPage />} />
      </Routes>
    </MemoryRouter>
  );
}

describe("IssueDetailPage", () => {
  beforeEach(() => {
    getIssue.mockReset();
    recordIssueView.mockReset();
    recordIssueView.mockResolvedValue(undefined);
  });

  it("records a view once the issue has loaded", async () => {
    getIssue.mockResolvedValue(buildDetail());

    renderPage();

    await screen.findByRole("heading", { name: "TimeoutError: upstream timed out" });
    expect(recordIssueView).toHaveBeenCalledExactlyOnceWith(42);
  });

  it("records no view for an issue merged into another", async () => {
    const merged: MergedIssue = { merged_into_id: 7 };
    getIssue.mockResolvedValue(merged);

    renderPage();

    await screen.findByText(/merged/i);
    expect(recordIssueView).not.toHaveBeenCalled();
  });
});
