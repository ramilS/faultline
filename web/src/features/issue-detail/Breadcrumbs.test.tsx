import { fireEvent, render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import type { EventDetail } from "@/api";

import { Breadcrumbs } from "./Breadcrumbs";

function eventWithCrumbs(count: number): EventDetail {
  return {
    event_id: "evt-1",
    timestamp: "2026-09-09T10:00:10.000Z",
    received_at: "2026-09-09T10:00:11.000Z",
    level: "error",
    environment: "production",
    release: null,
    message: "TypeError: boom",
    request_id: null,
    symbolicated_exception: null,
    payload: {
      breadcrumbs: Array.from({ length: count }, (_, index) => ({
        timestamp: `2026-09-09T10:00:0${index}.000Z`,
        category: index % 2 === 0 ? "navigation" : "http",
        level: "info",
        message: `step ${index}`
      }))
    }
  };
}

function listedMessages(container: HTMLElement): string[] {
  return [...container.querySelectorAll("li")].map((item) => item.textContent ?? "");
}

describe("Breadcrumbs", () => {
  it("shows the steps nearest the error inline, ending on the error, timed from it", () => {
    const { container } = render(<Breadcrumbs event={eventWithCrumbs(8)} />);

    const rows = listedMessages(container);
    expect(rows).toHaveLength(5);
    expect(rows[0]).toContain("step 4");
    expect(rows[0]).toContain("−6.0s");
    expect(rows.at(-1)).toContain("TypeError: boom");
  });

  it("opens the whole trail, filterable, from View all", () => {
    render(<Breadcrumbs event={eventWithCrumbs(8)} />);

    fireEvent.click(screen.getByRole("button", { name: "View all 8" }));
    const dialog = screen.getByRole("dialog");
    expect(within(dialog).getAllByRole("listitem")).toHaveLength(9);

    fireEvent.change(within(dialog).getByRole("textbox", { name: "Filter breadcrumbs" }), { target: { value: "HTTP" } });
    expect(within(dialog).getAllByRole("listitem").map((item) => item.textContent)).toEqual(
      expect.arrayContaining([expect.stringContaining("step 1"), expect.stringContaining("step 7")])
    );
    expect(within(dialog).getAllByRole("listitem")).toHaveLength(4);
  });

  it("offers no View all when the whole trail already fits", () => {
    render(<Breadcrumbs event={eventWithCrumbs(4)} />);

    expect(screen.queryByRole("button", { name: /View all/ })).toBeNull();
  });

  it("says so when the SDK sent no breadcrumbs", () => {
    render(<Breadcrumbs event={eventWithCrumbs(0)} />);

    expect(screen.getByText("No breadcrumbs")).toBeDefined();
  });
});
