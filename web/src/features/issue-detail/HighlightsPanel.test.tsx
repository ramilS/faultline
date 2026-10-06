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

/** The panel renders what one parse of the event yields, as the page does. */
function renderPanelFor(event: EventDetail) {
  return render(<HighlightsPanel highlights={buildHighlights(event, readPayloadFields(event.payload))} />);
}

describe("HighlightsPanel", () => {
  it("renders a Highlights card with the exception, environment, release, and user", () => {
    renderPanelFor(
      buildEvent({
        symbolicated_exception: { type: "TimeoutError", value: "upstream timed out", cause: null, frames: [] },
        payload: { user: { email: "a@example.com" } }
      })
    );

    expect(screen.getByText("Highlights")).toBeDefined();
    expect(screen.getByText("TimeoutError: upstream timed out")).toBeDefined();
    expect(screen.getByText("production")).toBeDefined();
    expect(screen.getByText("1.2.3")).toBeDefined();
    expect(screen.getByText("a@example.com")).toBeDefined();
  });

  it("still renders the environment when the event has no exception, release, or user", () => {
    renderPanelFor(buildEvent({ release: null }));

    expect(screen.getByText("Highlights")).toBeDefined();
    expect(screen.getByText("production")).toBeDefined();
    expect(screen.queryByText("Release")).toBeNull();
  });

  it("renders nothing when the event has no exception, release, environment, or user", () => {
    const { container } = render(
      <HighlightsPanel highlights={[]} />
    );

    expect(container.firstChild).toBeNull();
  });
});
