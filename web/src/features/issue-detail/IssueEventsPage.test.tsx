import { fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

// IssueEventsPage drives every fetch through the shared `api` client. Mocking
// it here is the only way to pin exactly which cursor reaches each
// `listIssueEvents` call without standing up a server — the same pattern
// EventNavigator.test.tsx uses.
vi.mock("@/apiClient", () => ({
  api: {
    listIssueEvents: vi.fn()
  }
}));

import type { EventSummary } from "@/api";
import { api } from "@/apiClient";
import { absoluteTime } from "@/lib/time";

import { IssueEventsPage } from "./IssueEventsPage";

const listIssueEvents = vi.mocked(api.listIssueEvents);

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

function renderPage(search = "") {
  return render(
    <MemoryRouter initialEntries={[`/acme/issues/42/events${search}`]}>
      <Routes>
        <Route path="/:slug/issues/:issueId/events" element={<IssueEventsPage />} />
      </Routes>
    </MemoryRouter>
  );
}

const SCOPED_SEARCH = "?environment=staging&start=2026-09-01T00:00:00.000Z&end=2026-09-08T00:00:00.000Z";

describe("IssueEventsPage", () => {
  beforeEach(() => {
    listIssueEvents.mockReset();
  });

  it("renders a row per event on the initial page", async () => {
    listIssueEvents.mockResolvedValue({
      events: [buildEvent({ event_id: "evt-1" }), buildEvent({ event_id: "evt-2", environment: "staging" })],
      next_cursor: null
    });

    renderPage();

    expect(await screen.findByRole("link", { name: "evt-1" })).toBeDefined();
    expect(screen.getByRole("link", { name: "evt-2" })).toBeDefined();
    expect(screen.getByText("staging")).toBeDefined();
    expect(listIssueEvents).toHaveBeenCalledWith(42, { order: "newest" });
  });

  it("shows what each event said and which release produced it, so events can be told apart without opening each", async () => {
    listIssueEvents.mockResolvedValue({
      events: [buildEvent({ event_id: "evt-1", message: "card declined by issuer", release: "2.4.1" })],
      next_cursor: null
    });

    renderPage();

    expect(await screen.findByText("card declined by issuer")).toBeDefined();
    expect(screen.getByText("2.4.1")).toBeDefined();
  });

  it("leaves the Message column out when no event on the page carries a message, as with exceptions", async () => {
    listIssueEvents.mockResolvedValue({
      events: [buildEvent({ event_id: "evt-1", message: "" }), buildEvent({ event_id: "evt-2", message: "  " })],
      next_cursor: null
    });

    renderPage();
    await screen.findByRole("link", { name: "evt-1" });

    expect(screen.queryByRole("columnheader", { name: "Message" })).toBeNull();
  });

  it("keeps the Message column, with a dash for the events that have none, once any event carries one", async () => {
    listIssueEvents.mockResolvedValue({
      events: [buildEvent({ event_id: "evt-1", message: "card declined" }), buildEvent({ event_id: "evt-2", message: "" })],
      next_cursor: null
    });

    renderPage();
    await screen.findByRole("link", { name: "evt-1" });

    expect(screen.getByRole("columnheader", { name: "Message" })).toBeDefined();
    expect(screen.getByText("card declined")).toBeDefined();
  });

  it("prints the exact time an event occurred rather than leaving it to a hover tooltip", async () => {
    listIssueEvents.mockResolvedValue({
      events: [buildEvent({ event_id: "evt-1", timestamp: "2026-09-08T00:00:00Z" })],
      next_cursor: null
    });

    renderPage();

    expect(await screen.findByText(absoluteTime("2026-09-08T00:00:00Z"))).toBeDefined();
  });

  it("appends the next page and sends the previous page's cursor when 'Load more' is clicked", async () => {
    listIssueEvents.mockResolvedValueOnce({ events: [buildEvent({ event_id: "evt-1" })], next_cursor: "cursor-1" });
    listIssueEvents.mockResolvedValueOnce({ events: [buildEvent({ event_id: "evt-2" })], next_cursor: null });

    renderPage();
    await screen.findByRole("link", { name: "evt-1" });

    fireEvent.click(screen.getByRole("button", { name: "Load more" }));

    expect(await screen.findByRole("link", { name: "evt-2" })).toBeDefined();
    expect(screen.getByRole("link", { name: "evt-1" })).toBeDefined();
    expect(listIssueEvents).toHaveBeenCalledTimes(2);
    expect(listIssueEvents).toHaveBeenNthCalledWith(2, 42, { order: "newest", cursor: "cursor-1" });
  });

  /**
   * The rows and "Load more" have to land in the same commit. `nextCursor` was
   * state synced by an effect, so the first page committed once with rows and
   * no button: a frame of missing control on every load, and the reason the two
   * click tests above flaked on React 19 but passed on 18 — `findByRole` for a
   * row could resolve inside that window, and React 19's timing widened it.
   *
   * Asserted at DOM-mutation granularity because that is where the artifact is
   * visible. The polling helpers flush effects before they return, so they
   * cannot observe the intermediate commit at all.
   */
  it("commits the event rows and 'Load more' together", async () => {
    listIssueEvents.mockResolvedValue({
      events: [buildEvent({ event_id: "evt-1" })],
      next_cursor: "cursor-1"
    });

    const { container } = renderPage();

    const rowsWithoutLoadMore: number[] = [];
    const observer = new MutationObserver(() => {
      const rows = container.querySelectorAll("tbody tr").length;
      const hasLoadMore = [...container.querySelectorAll("button")].some(
        (button) => button.textContent?.trim() === "Load more"
      );
      if (rows > 0 && !hasLoadMore) {
        rowsWithoutLoadMore.push(rows);
      }
    });
    observer.observe(container, { childList: true, subtree: true });

    await screen.findByRole("button", { name: "Load more" });
    observer.disconnect();

    expect(rowsWithoutLoadMore).toEqual([]);
  });

  it("does not render 'Load more' once the API returns no next cursor", async () => {
    listIssueEvents.mockResolvedValue({ events: [buildEvent()], next_cursor: null });

    renderPage();
    await screen.findByRole("link", { name: "evt-1" });

    expect(screen.queryByRole("button", { name: "Load more" })).toBeNull();
  });

  it("renders an empty state when the issue has no events", async () => {
    listIssueEvents.mockResolvedValue({ events: [], next_cursor: null });

    renderPage();

    expect(await screen.findByText("No events")).toBeDefined();
    expect(
      screen.getByText("This issue has no stored events. They may have aged out of retention.")
    ).toBeDefined();
    expect(screen.queryByRole("table")).toBeNull();
  });

  it("requests the scope the pager handed over, on the first page and on 'Load more'", async () => {
    listIssueEvents.mockResolvedValueOnce({ events: [buildEvent({ event_id: "evt-1" })], next_cursor: "cursor-1" });
    listIssueEvents.mockResolvedValueOnce({ events: [buildEvent({ event_id: "evt-2" })], next_cursor: null });

    renderPage(SCOPED_SEARCH);
    await screen.findByRole("link", { name: "evt-1" });

    fireEvent.click(screen.getByRole("button", { name: "Load more" }));
    await screen.findByRole("link", { name: "evt-2" });

    const scope = {
      environment: "staging",
      start: "2026-09-01T00:00:00.000Z",
      end: "2026-09-08T00:00:00.000Z"
    };
    expect(listIssueEvents).toHaveBeenNthCalledWith(1, 42, { order: "newest", ...scope });
    expect(listIssueEvents).toHaveBeenNthCalledWith(2, 42, { order: "newest", cursor: "cursor-1", ...scope });
  });

  it("lists the newest events first, since that is what a triager opens the list for", async () => {
    listIssueEvents.mockResolvedValue({ events: [buildEvent()], next_cursor: null });

    renderPage();
    await screen.findByRole("link", { name: "evt-1" });

    expect(screen.getByRole("button", { name: "Newest first" }).getAttribute("aria-pressed")).toBe("true");
    expect(screen.getByRole("button", { name: "Oldest first" }).getAttribute("aria-pressed")).toBe("false");
  });

  it("restarts from the first page in the other direction when the order is switched, keeping the scope", async () => {
    listIssueEvents.mockResolvedValueOnce({ events: [buildEvent({ event_id: "evt-new" })], next_cursor: "cursor-1" });
    listIssueEvents.mockResolvedValueOnce({ events: [buildEvent({ event_id: "evt-old" })], next_cursor: null });

    renderPage(SCOPED_SEARCH);
    await screen.findByRole("link", { name: "evt-new" });

    fireEvent.click(screen.getByRole("button", { name: "Oldest first" }));

    expect(await screen.findByRole("link", { name: "evt-old" })).toBeDefined();
    expect(screen.queryByRole("link", { name: "evt-new" })).toBeNull();
    expect(listIssueEvents).toHaveBeenNthCalledWith(2, 42, {
      order: "oldest",
      environment: "staging",
      start: "2026-09-01T00:00:00.000Z",
      end: "2026-09-08T00:00:00.000Z"
    });
    expect(screen.getByRole("button", { name: "Oldest first" }).getAttribute("aria-pressed")).toBe("true");
  });

  it("says which scope the list is narrowed to", async () => {
    listIssueEvents.mockResolvedValue({ events: [buildEvent()], next_cursor: null });

    renderPage(SCOPED_SEARCH);
    await screen.findByRole("link", { name: "evt-1" });

    expect(screen.getByText(/Only events in staging received between/)).toBeDefined();
  });

  it("claims no scope when the link carried none", async () => {
    listIssueEvents.mockResolvedValue({ events: [buildEvent()], next_cursor: null });

    renderPage();
    await screen.findByRole("link", { name: "evt-1" });

    expect(screen.queryByText(/Only events/)).toBeNull();
  });

  it("blames the carried-over filters, not retention, for an empty scoped list", async () => {
    listIssueEvents.mockResolvedValue({ events: [], next_cursor: null });

    renderPage(SCOPED_SEARCH);

    expect(await screen.findByText("No stored event matches the filters carried over from the issue page.")).toBeDefined();
  });

  it("refuses a non-positive issue id before it reaches the API", () => {
    render(
      <MemoryRouter initialEntries={["/acme/issues/0/events"]}>
        <Routes>
          <Route path="/:slug/issues/:issueId/events" element={<IssueEventsPage />} />
        </Routes>
      </MemoryRouter>
    );

    expect(screen.getByText("Not an issue")).toBeDefined();
    expect(screen.getByRole("link", { name: "Back to issues" })).toHaveProperty("pathname", "/acme/issues");
    expect(listIssueEvents).not.toHaveBeenCalled();
  });
});
