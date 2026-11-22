import { fireEvent, render, screen, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { api } from "@/apiClient";
import { DEFAULT_FILTERS, type StreamFilters } from "@/lib/issueFilters";

import { IssueFilterBar } from "./IssueFilterBar";
import type { UrlFilterControls } from "./useUrlFilters";

vi.mock("@/apiClient", () => ({
  api: { listTagValues: vi.fn(), listTagKeys: vi.fn() }
}));

const TAG_VALUE = { times_seen: 3, first_seen: "2026-11-01T00:00:00Z", last_seen: "2026-11-20T00:00:00Z" };

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

describe("IssueFilterBar device and OS filters", () => {
  beforeEach(() => {
    vi.mocked(api.listTagKeys).mockResolvedValue([]);
    vi.mocked(api.listTagValues).mockImplementation(async (_slug, key) =>
      key === "device" ? [{ value: "Pixel 7", ...TAG_VALUE }] : []
    );
  });

  it("adds a picked device model as a device tag filter and keeps the other tags", async () => {
    const controls = controlsFor({ tags: [{ key: "os", value: "Android 14" }] });
    render(<IssueFilterBar slug="mobile" controls={controls} />);

    fireEvent.click(await screen.findByRole("button", { name: "Device" }));
    fireEvent.click(await within(screen.getByRole("dialog")).findByText("Pixel 7"));

    expect(controls.setTags).toHaveBeenCalledWith([
      { key: "os", value: "Android 14" },
      { key: "device", value: "Pixel 7" }
    ]);
  });

  it("hides the OS filter while the project has no os values", async () => {
    render(<IssueFilterBar slug="mobile" controls={controlsFor({})} />);

    await screen.findByRole("button", { name: "Device" });
    expect(screen.queryByRole("button", { name: "OS" })).toBeNull();
  });
});
