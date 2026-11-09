import { render, screen, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { IssueSummary, Regression } from "@/api";

import { IssueHeader } from "./IssueHeader";
import { STICKY_HEADER_HEIGHT_PROPERTY } from "./stickyHeader";

vi.mock("@/features/projects/ProjectsProvider", () => ({
  useProjects: () => ({ state: { status: "ready", projects: [{ id: 1, slug: "acme", name: "Acme" }] }, reload: vi.fn() })
}));

function buildIssue(overrides: Partial<IssueSummary> = {}): IssueSummary {
  return {
    id: 42,
    project_slug: "web",
    fingerprint: "fp-1",
    title: "TimeoutError: upstream timed out",
    culprit: "billing.charge_customer",
    level: "error",
    status: "unresolved",
    assignee: null,
    times_seen: 12,
    users_seen: 3,
    first_seen: "2026-09-01T00:00:00Z",
    last_seen: "2026-09-09T00:00:00Z",
    ignore_until: null,
    ...overrides
  };
}

function renderHeader(issue: IssueSummary, latestMessage: string | null, regression: Regression | null = null) {
  return render(
    <MemoryRouter>
      <IssueHeader issue={issue} slug="acme" latestMessage={latestMessage} regression={regression} actions={null} />
    </MemoryRouter>
  );
}

describe("IssueHeader", () => {
  it("spells out the condition an ignored issue comes back on, with its progress", () => {
    renderHeader(
      buildIssue({ status: "ignored", ignore_until: { kind: "events", until: null, count: 100, so_far: 37 } }),
      null
    );

    expect(screen.getByText("Ignored until 100 more events · 37 so far")).toBeDefined();
  });

  it("says only Ignored for an issue ignored forever", () => {
    renderHeader(buildIssue({ status: "ignored" }), null);

    expect(screen.getByText("Ignored")).toBeDefined();
  });

  it("renders a breadcrumb trail with Projects and the project name as links, and the issue id as the current page", () => {
    renderHeader(buildIssue(), null);

    const trail = screen.getByRole("navigation", { name: "Breadcrumb" });

    const projectsLink = within(trail).getByRole("link", { name: "Projects" });
    expect(projectsLink).toHaveProperty("pathname", "/projects");

    const projectLink = within(trail).getByRole("link", { name: "Acme" });
    expect(projectLink).toHaveProperty("pathname", "/acme/issues");
    expect(projectLink.getAttribute("title")).toBe("acme");

    // The current segment is text, not a link, and is marked current for assistive tech.
    expect(within(trail).queryByRole("link", { name: "#42" })).toBeNull();
    const current = within(trail).getByText("#42");
    expect(current.getAttribute("aria-current")).toBe("page");
  });

  it("shows the lifetime event and user counts as labelled figures", () => {
    renderHeader(buildIssue({ times_seen: 1284, users_seen: 312 }), null);

    const counts = Object.fromEntries(
      [...document.querySelectorAll("header dl > div")].map((stat) => [
        stat.querySelector("dt")?.textContent,
        stat.querySelector("dd")?.textContent
      ])
    );
    expect(counts).toEqual({ Events: (1284).toLocaleString(), Users: "312" });
  });

  it("says the issue came back, and in which release", () => {
    renderHeader(buildIssue(), null, { at: "2026-09-09T00:00:00Z", release: "shop@2.4.1" });

    expect(screen.getByText("Regressed in shop@2.4.1")).toBeDefined();
  });

  it("says the issue came back even when the event named no release", () => {
    renderHeader(buildIssue(), null, { at: "2026-09-09T00:00:00Z", release: null });

    expect(screen.getByText("Regressed")).toBeDefined();
  });

  it("shows no regression mark on an issue that has not come back", () => {
    renderHeader(buildIssue(), null);

    expect(screen.queryByText(/Regressed/)).toBeNull();
  });

  it("renders the message line when it differs from the title", () => {
    renderHeader(buildIssue({ title: "TimeoutError: upstream timed out" }), "upstream timed out after 30s");

    expect(screen.getByText("upstream timed out after 30s")).toBeDefined();
    // The message and the culprit each render as their own body line.
    expect(screen.getAllByRole("paragraph")).toHaveLength(2);
  });

  it("does not render a message line when the message equals the title", () => {
    const issue = buildIssue({ title: "TimeoutError: upstream timed out" });

    renderHeader(issue, issue.title);

    // Only the culprit renders as body text below the heading — no duplicate message line.
    expect(screen.getAllByRole("paragraph")).toHaveLength(1);
  });

  it("does not render a message line when the message is empty", () => {
    renderHeader(buildIssue(), "");

    expect(screen.getAllByRole("paragraph")).toHaveLength(1);
  });

  it("does not render a message line when the message is null", () => {
    renderHeader(buildIssue(), null);

    expect(screen.getAllByRole("paragraph")).toHaveLength(1);
  });

  it("does not render a message line when the message is whitespace only", () => {
    renderHeader(buildIssue(), "   ");

    expect(screen.getAllByRole("paragraph")).toHaveLength(1);
  });

  it("does not render a message line when the message equals the title plus trailing whitespace", () => {
    const issue = buildIssue({ title: "TimeoutError: upstream timed out" });

    renderHeader(issue, `${issue.title}  `);

    expect(screen.getAllByRole("paragraph")).toHaveLength(1);
  });

  describe("published height for jump-to-section anchors", () => {
    const measuredHeight = 180;

    afterEach(() => {
      vi.restoreAllMocks();
      document.documentElement.style.removeProperty(STICKY_HEADER_HEIGHT_PROPERTY);
    });

    function layOutHeaderAs(position: "sticky" | "static") {
      vi.spyOn(HTMLElement.prototype, "offsetHeight", "get").mockReturnValue(measuredHeight);
      vi.spyOn(window, "getComputedStyle").mockReturnValue({ position } as CSSStyleDeclaration);
    }

    it("publishes its height while it is pinned to the top of the viewport", () => {
      layOutHeaderAs("sticky");

      renderHeader(buildIssue(), null);

      expect(document.documentElement.style.getPropertyValue(STICKY_HEADER_HEIGHT_PROPERTY)).toBe(
        `${measuredHeight}px`
      );
    });

    it("publishes nothing while it scrolls away with the page, so anchors are not pushed down by a header that is not covering them", () => {
      layOutHeaderAs("static");

      renderHeader(buildIssue(), null);

      expect(document.documentElement.style.getPropertyValue(STICKY_HEADER_HEIGHT_PROPERTY)).toBe("");
    });
  });
});
