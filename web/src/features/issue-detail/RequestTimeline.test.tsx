import { render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("@/apiClient", () => ({
  api: { getTimeline: vi.fn() }
}));

import type { Span } from "@/api";
import { api } from "@/apiClient";

import { RequestTimeline } from "./RequestTimeline";

const getTimeline = vi.mocked(api.getTimeline);

const SPAN: Span = {
  span_id: "s1",
  parent_span_id: null,
  name: "GET /cart",
  start_time: "2026-09-09T10:00:00.000Z",
  duration_ms: 12,
  status: "ok",
  attributes: {}
};

describe("RequestTimeline", () => {
  beforeEach(() => {
    getTimeline.mockReset();
  });

  it("collapses to one line under the same section anchor when nothing was traced", async () => {
    getTimeline.mockResolvedValue([]);
    const { container } = render(<RequestTimeline eventId="evt-1" requestId="req-7" />);
    const anchor = container.querySelector("#section-timeline");
    expect(anchor?.textContent).toContain("Request timeline");

    await waitFor(() => expect(anchor?.textContent).toMatch(/^No spans traced for request req-7/));
    expect(container.querySelector("#section-timeline")).toBe(anchor);
    expect(screen.queryByText("Request timeline")).toBeNull();
  });

  it("keeps the card when spans arrived", async () => {
    getTimeline.mockResolvedValue([SPAN]);
    const { container } = render(<RequestTimeline eventId="evt-1" requestId="req-7" />);

    await screen.findByRole("list", { name: "Request timeline" });
    expect(container.querySelector("#section-timeline")?.textContent).toContain("GET /cart");
  });
});
