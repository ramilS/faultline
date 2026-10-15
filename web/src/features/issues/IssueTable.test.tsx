import { render, screen, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it } from "vitest";

import type { IssueSummary } from "@/api";

import { IssueTable } from "./IssueTable";

function buildIssue(overrides: Partial<IssueSummary>): IssueSummary {
  return {
    id: 1,
    project_slug: "web",
    fingerprint: "fp",
    title: "TypeError: x is undefined",
    culprit: "app.render",
    level: "error",
    status: "unresolved",
    assignee: null,
    times_seen: 1,
    users_seen: 0,
    first_seen: "2026-09-01T00:00:00Z",
    last_seen: "2026-09-09T00:00:00Z",
    counts_24h: [],
    counts_14d: [],
    ...overrides
  };
}

function renderTable(issues: readonly IssueSummary[], projectNames: ReadonlyMap<string, string> | null) {
  return render(
    <MemoryRouter>
      <IssueTable
        issues={issues}
        projectNames={projectNames}
        selectedIds={new Set()}
        windowLabel="24h"
        sort="last_seen"
        onSortChange={() => undefined}
        onSelectedChange={() => undefined}
        onSelectAllChange={() => undefined}
      />
    </MemoryRouter>
  );
}

describe("IssueTable", () => {
  it("links every row to its own project and names that project when listing every project", () => {
    renderTable(
      [
        buildIssue({ id: 7, project_slug: "web", title: "Web failure" }),
        buildIssue({ id: 9, project_slug: "billing", title: "Billing failure" })
      ],
      new Map([
        ["web", "Web storefront"],
        ["billing", "Billing API"]
      ])
    );

    screen.getByRole("columnheader", { name: "Project" });
    expect(screen.getByRole("link", { name: "Billing failure" }).getAttribute("href")).toBe("/billing/issues/9");
    const billingRow = screen.getByRole("link", { name: "Billing failure" }).closest("tr");
    expect(billingRow).not.toBeNull();
    expect(within(billingRow as HTMLElement).getAllByText("Billing API").length).toBeGreaterThan(0);
  });

  it("falls back to the slug for a project the caller's list does not name", () => {
    renderTable([buildIssue({ project_slug: "archived" })], new Map());

    expect(screen.getAllByText("archived").length).toBeGreaterThan(0);
  });

  it("has no project column inside a single project", () => {
    renderTable([buildIssue({ id: 7, project_slug: "web", title: "Web failure" })], null);

    expect(screen.queryByRole("columnheader", { name: "Project" })).toBeNull();
    expect(screen.getByRole("link", { name: "Web failure" }).getAttribute("href")).toBe("/web/issues/7");
  });
});
