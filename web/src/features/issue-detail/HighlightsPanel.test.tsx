import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import type { EventDetail } from "@/api";

import { buildHighlights } from "./highlights";
import { HighlightsPanel } from "./HighlightsPanel";
import { readPayloadFields } from "./payloadReaders";

function buildEvent(overrides: Partial<EventDetail> = {}): EventDetail {
  return {
    event_id: "evt-1",
    timestamp: "2026-09-09T00:00:00Z",
    received_at: "2026-09-09T00:00:01Z",
    level: "error",
    environment: "production",
    release: "1.2.3",
    message: "boom",
    request_id: null,
    payload: {},
    symbolicated_exception: null,
    ...overrides
  };
}

/** The strip renders what one parse of the event yields, as the page does. */
function renderPanelFor(event: EventDetail) {
  return render(<HighlightsPanel highlights={buildHighlights(event, readPayloadFields(event.payload))} />);
}

describe("HighlightsPanel", () => {
  it("names the user, transaction and server the event happened to", () => {
    renderPanelFor(
      buildEvent({ payload: { user: { email: "a@example.com" }, transaction: "GET /orders", server_name: "web-1" } })
    );

    expect(screen.getByRole("region", { name: "Highlights" })).toBeDefined();
    expect(screen.getByText("a@example.com")).toBeDefined();
    expect(screen.getByText("GET /orders")).toBeDefined();
    expect(screen.getByText("web-1")).toBeDefined();
  });

  it("renders nothing when the event names no user, transaction or server", () => {
    const { container } = renderPanelFor(buildEvent());

    expect(container.firstChild).toBeNull();
  });
});
