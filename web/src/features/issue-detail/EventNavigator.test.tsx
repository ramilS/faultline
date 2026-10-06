import { fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

// EventNavigator drives every fetch through the shared `api` client. Mocking
// it here is the only way to pin exactly which filters reach each of its
// three `listIssueEvents` call sites (the initial load, "further" paging, and
// an anchor jump) without standing up a server. This is the first module mock
// in this test suite — keep it to exactly what EventNavigator calls.
vi.mock("@/apiClient", () => ({
  api: {
    listIssueEvents: vi.fn()
  }
}));

import type { EventSummary } from "@/api";
import { api } from "@/apiClient";
import type { TimeRangeValue } from "@/features/issues/TimeRangePicker";

import { EventNavigator } from "./EventNavigator";

const listIssueEvents = vi.mocked(api.listIssueEvents);

const SCOPED_RANGE: TimeRangeValue = {
  statsPeriod: "",
  start: "2026-09-01T00:00:00.000Z",
  end: "2026-09-08T00:00:00.000Z"
};

function buildEvent(overrides: Partial<EventSummary> = {}): EventSummary {
  return {
    event_id: "evt-1",
    timestamp: "2026-09-08T00:00:00Z",
    received_at: "2026-09-08T00:00:01Z",
    level: "error",
    environment: "production",
    release: null,
    message: "boom",
    ...overrides
  };
}

/**
 * Mounted under the real detail route rather than a bare router: the "View
 * all" link is resolved relative to that route, so a bare `<MemoryRouter>`
 * would resolve it against "/" and could not catch a wrong target.
 */
function renderNavigator(environment: string, timeRange: TimeRangeValue) {
  return render(
    <MemoryRouter initialEntries={["/acme/issues/42"]}>
      <Routes>
        <Route
          path="/:slug/issues/:issueId"
          element={
            <EventNavigator issueId={42} initialEventId={null} onSelect={() => {}} environment={environment} timeRange={timeRange} />
          }
        />
      </Routes>
    </MemoryRouter>
  );
}

describe("EventNavigator filter forwarding", () => {
  beforeEach(() => {
    listIssueEvents.mockReset();
  });

  it("sends the environment and time range on the initial fetch", async () => {
    listIssueEvents.mockResolvedValue({ events: [buildEvent()], next_cursor: null });

    renderNavigator("production", SCOPED_RANGE);

    await screen.findByText("evt-1");

    expect(listIssueEvents).toHaveBeenCalledTimes(1);
    expect(listIssueEvents).toHaveBeenCalledWith(42, {
      environment: "production",
      start: SCOPED_RANGE.start,
      end: SCOPED_RANGE.end
    });
  });

  it("carries the same filters, plus the cursor, when paging to a further event", async () => {
    listIssueEvents.mockResolvedValueOnce({ events: [buildEvent({ event_id: "evt-1" })], next_cursor: "cursor-1" });
    listIssueEvents.mockResolvedValueOnce({ events: [buildEvent({ event_id: "evt-2" })], next_cursor: null });

    renderNavigator("production", SCOPED_RANGE);
    await screen.findByText("evt-1");

    fireEvent.click(screen.getByRole("button", { name: "Older event" }));
    await screen.findByText("evt-2");

    expect(listIssueEvents).toHaveBeenCalledTimes(2);
    expect(listIssueEvents).toHaveBeenNthCalledWith(2, 42, {
      cursor: "cursor-1",
      order: "newest",
      environment: "production",
      start: SCOPED_RANGE.start,
      end: SCOPED_RANGE.end
    });
  });

  it("carries the filters when jumping to the oldest anchor", async () => {
    listIssueEvents.mockResolvedValueOnce({ events: [buildEvent({ event_id: "evt-1" })], next_cursor: null });
    listIssueEvents.mockResolvedValueOnce({ events: [buildEvent({ event_id: "evt-oldest" })], next_cursor: null });

    renderNavigator("production", SCOPED_RANGE);
    await screen.findByText("evt-1");

    fireEvent.click(screen.getByRole("button", { name: "Oldest" }));
    await screen.findByText("evt-oldest");

    expect(listIssueEvents).toHaveBeenCalledTimes(2);
    expect(listIssueEvents).toHaveBeenNthCalledWith(2, 42, {
      order: "oldest",
      environment: "production",
      start: SCOPED_RANGE.start,
      end: SCOPED_RANGE.end
    });
  });

  it("points 'View all' at the issue's own events route, carrying the active scope", async () => {
    listIssueEvents.mockResolvedValue({ events: [buildEvent()], next_cursor: null });

    renderNavigator("production", SCOPED_RANGE);
    await screen.findByText("evt-1");

    const viewAll = screen.getByRole("link", { name: "View all" });
    expect(viewAll).toHaveProperty("pathname", "/acme/issues/42/events");
    const carried = new URLSearchParams((viewAll as HTMLAnchorElement).search);
    expect(carried.get("environment")).toBe("production");
    expect(carried.get("start")).toBe(SCOPED_RANGE.start);
    expect(carried.get("end")).toBe(SCOPED_RANGE.end);
  });

  it("carries no scope on 'View all' when the pager is unfiltered", async () => {
    listIssueEvents.mockResolvedValue({ events: [buildEvent()], next_cursor: null });

    renderNavigator("", { statsPeriod: "", start: "", end: "" });
    await screen.findByText("evt-1");

    expect(screen.getByRole("link", { name: "View all" })).toHaveProperty("search", "");
  });

  it("restarts the walk with the new filters, and without a cursor, when a filter changes", async () => {
    listIssueEvents.mockResolvedValueOnce({ events: [buildEvent({ event_id: "evt-1" })], next_cursor: "cursor-1" });
    listIssueEvents.mockResolvedValueOnce({
      events: [buildEvent({ event_id: "evt-staging", environment: "staging" })],
      next_cursor: null
    });

    const { rerender } = renderNavigator("production", SCOPED_RANGE);
    await screen.findByText("evt-1");

    rerender(
      <MemoryRouter initialEntries={["/acme/issues/42"]}>
        <Routes>
          <Route
            path="/:slug/issues/:issueId"
            element={
              <EventNavigator issueId={42} initialEventId={null} onSelect={() => {}} environment="staging" timeRange={SCOPED_RANGE} />
            }
          />
        </Routes>
      </MemoryRouter>
    );
    await screen.findByText("evt-staging");

    expect(listIssueEvents).toHaveBeenCalledTimes(2);
    expect(listIssueEvents).toHaveBeenNthCalledWith(2, 42, {
      environment: "staging",
      start: SCOPED_RANGE.start,
      end: SCOPED_RANGE.end
    });
  });
});

describe("EventNavigator event summary", () => {
  beforeEach(() => {
    listIssueEvents.mockReset();
  });

  it("shows the message the event carried", async () => {
    listIssueEvents.mockResolvedValue({ events: [buildEvent({ message: "card declined by issuer" })], next_cursor: null });

    renderNavigator("", { statsPeriod: "", start: "", end: "" });

    expect(await screen.findByText("card declined by issuer")).toBeDefined();
    expect(screen.getByText("Message")).toBeDefined();
  });

  it("leaves out the Message field for an exception event that carries no message, rather than a label over nothing", async () => {
    listIssueEvents.mockResolvedValue({ events: [buildEvent({ message: "" })], next_cursor: null });

    renderNavigator("", { statsPeriod: "", start: "", end: "" });
    await screen.findByText("evt-1");

    expect(screen.queryByText("Message")).toBeNull();
  });

  it("leaves out the Message field when the message is only whitespace", async () => {
    listIssueEvents.mockResolvedValue({ events: [buildEvent({ message: "   " })], next_cursor: null });

    renderNavigator("", { statsPeriod: "", start: "", end: "" });
    await screen.findByText("evt-1");

    expect(screen.queryByText("Message")).toBeNull();
  });
});

