import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import type { IssueStatus } from "@/api";
import { DEFAULT_FILTERS, type StreamFilters } from "@/lib/issueFilters";

import { IssueFilterBar } from "./IssueFilterBar";
import type { UrlFilterControls } from "./useUrlFilters";

function controlsFor(overrides: Partial<StreamFilters>): UrlFilterControls {
  return {
    filters: { ...DEFAULT_FILTERS, ...overrides },
    queryDraft: "",
    setQueryDraft: vi.fn(),
    setStatuses: vi.fn(),
    setLevels: vi.fn(),
    setAssigned: vi.fn(),
    setEnvironments: vi.fn(),
    setReleases: vi.fn(),
    setTags: vi.fn(),
    setSort: vi.fn(),
    setTimeRange: vi.fn(),
    setUnseen: vi.fn(),
    clear: vi.fn()
  };
}

describe("IssueFilterBar trigger labels", () => {
  it.each<{ statuses: IssueStatus[]; label: string }>([
    { statuses: ["unresolved"], label: "Status: Unresolved" },
    { statuses: ["ignored"], label: "Status: Ignored" },
    { statuses: ["unresolved", "ignored"], label: "Status: 2 selected" },
    { statuses: ["unresolved", "resolved", "ignored"], label: "Status: 3 selected" }
  ])("names the status filter $label", ({ statuses, label }) => {
    render(<IssueFilterBar slug={null} controls={controlsFor({ statuses })} />);

    screen.getByRole("button", { name: label });
  });

  it.each([
    { assigned: "", value: "Anyone" },
    { assigned: "me", value: "Assigned to me" },
    { assigned: "none", value: "Unassigned" }
  ])("labels the assignee filter with its value $value", ({ assigned, value }) => {
    render(<IssueFilterBar slug={null} controls={controlsFor({ assigned })} />);

    expect(screen.getByRole("combobox", { name: "Filter by assignee" }).textContent).toBe(`Assignee:${value}`);
  });
});
