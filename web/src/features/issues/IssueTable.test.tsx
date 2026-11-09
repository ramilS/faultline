import { render, screen, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it } from "vitest";

import type { ListedIssue } from "@/api";

import { IssueTable } from "./IssueTable";

function buildIssue(overrides: Partial<ListedIssue>): ListedIssue {
  return {
    id: 1,
    project_slug: "web",
    fingerprint: "fp",
    title: "TypeError: x is undefined",
    culprit: "app.render",
    display_title: "",
    display_culprit: "",
    level: "error",
    status: "unresolved",
    assignee: null,
    times_seen: 1,
    users_seen: 0,
    first_seen: "2026-09-01T00:00:00Z",
    last_seen: "2026-09-09T00:00:00Z",
    ignore_until: null,
    counts_24h: [],
    counts_14d: [],
    unseen: false,
    ...overrides
  };
}

function renderTable(issues: readonly ListedIssue[], projectNames: ReadonlyMap<string, string> | null) {
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
  it("lists an issue under its symbolicated labels once it has them", () => {
    renderTable(
      [
        buildIssue({
          id: 3,
          title: "bu4: Source error",
          culprit: "Muslicast+0xfa6408",
          display_title: "SourceException: Source error",
          display_culprit: "PlaybackController.invoke (PlaybackController.swift)"
        }),
        buildIssue({ id: 4, title: "TypeError: x is undefined", culprit: "app.render" })
      ],
      null
    );

    expect(screen.getByRole("link", { name: "SourceException: Source error" }).getAttribute("href")).toBe("/web/issues/3");
    screen.getByText("PlaybackController.invoke (PlaybackController.swift)");
    expect(screen.queryByText("bu4: Source error")).toBeNull();
    expect(screen.queryByText("Muslicast+0xfa6408")).toBeNull();
    screen.getByRole("link", { name: "TypeError: x is undefined" });
    screen.getByText("app.render");
  });

  it("shows an ignored issue's progress toward its condition on the status badge", () => {
    renderTable(
      [buildIssue({ status: "ignored", ignore_until: { kind: "events", until: null, count: 100, so_far: 99 } })],
      null
    );

    const badges = screen.getAllByText("Ignored · 99 of 100 events");
    expect(badges.map((badge) => badge.getAttribute("title"))).toEqual([
      "Ignored until 100 more events · 99 so far",
      "Ignored until 100 more events · 99 so far"
    ]);
  });

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

  it("marks an unseen issue with a named dot and a semibold title, and leaves a seen one regular", () => {
    renderTable(
      [
        buildIssue({ id: 1, title: "Fresh failure", unseen: true }),
        buildIssue({ id: 2, title: "Known failure", unseen: false })
      ],
      null
    );

    const freshLink = screen.getByRole("link", { name: "Fresh failure" });
    const freshRow = freshLink.closest("tr") as HTMLElement;
    within(freshRow).getByRole("img", { name: "Not viewed yet" });
    expect(freshLink.className).toContain("font-semibold");

    const knownLink = screen.getByRole("link", { name: "Known failure" });
    const knownRow = knownLink.closest("tr") as HTMLElement;
    expect(within(knownRow).queryByRole("img", { name: "Not viewed yet" })).toBeNull();
    expect(knownLink.className).toContain("font-normal");
    expect(knownLink.className).not.toContain("font-semibold");
    expect(knownRow.className).not.toMatch(/opacity|text-muted/);
  });
});
