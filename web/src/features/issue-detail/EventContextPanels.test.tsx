import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { EventContextPanels } from "./EventContextPanels";
import { readPayloadFields } from "./payloadReaders";

/** The panels render one parse of the payload, exactly as the page does. */
function renderPanels(payload: unknown) {
  return render(<EventContextPanels fields={readPayloadFields(payload)} />);
}

describe("EventContextPanels", () => {
  it("renders contexts, sdk, extra, tags, and modules panels with a visible value from each", () => {
    renderPanels({
      contexts: { runtime: { name: "CPython", version: "3.12.1" } },
      sdk: { name: "sentry.python", version: "1.40.0" },
      extra: { orderId: "order-42" },
      tags: { environment: "staging" },
      modules: { requests: "2.31.0" }
    });

    expect(screen.getByText("Runtime")).toBeDefined();
    expect(screen.getByText("CPython")).toBeDefined();
    expect(screen.getByText("SDK")).toBeDefined();
    expect(screen.getByText("sentry.python")).toBeDefined();
    expect(screen.getByText("Additional data")).toBeDefined();
    expect(screen.getByText("order-42")).toBeDefined();
    expect(screen.getByText("Tags")).toBeDefined();
    expect(screen.getByText("staging")).toBeDefined();
    expect(screen.getByText("Dependencies")).toBeDefined();
    expect(screen.getByText("2.31.0")).toBeDefined();
  });

  it("renders none of the technical panels when the payload carries none of them", () => {
    const { container } = renderPanels({ message: "boom" });

    expect(screen.queryByText("Tags")).toBeNull();
    expect(screen.queryByText("SDK")).toBeNull();
    expect(screen.queryByText("Additional data")).toBeNull();
    expect(screen.queryByText("Dependencies")).toBeNull();
    expect(container.textContent).toBe("");
  });

  it("groups every context in one Contexts card rather than a card of two rows each, titling the os context 'OS'", () => {
    const { container } = renderPanels({
      contexts: {
        os: { name: "Linux", version: "6.8" },
        runtime: { name: "CPython", version: "3.12.1" }
      }
    });

    const cards = container.querySelectorAll("#section-contexts");
    expect(cards).toHaveLength(1);
    expect(screen.getByText("Contexts")).toBeDefined();
    expect(screen.getByRole("heading", { name: "OS" })).toBeDefined();
    expect(screen.getByRole("heading", { name: "Runtime" })).toBeDefined();
    expect(cards[0].textContent).toContain("Linux");
    expect(cards[0].textContent).toContain("CPython");
  });

  it("renders no Contexts card when the event carries no context", () => {
    renderPanels({ message: "boom", contexts: {} });

    expect(screen.queryByText("Contexts")).toBeNull();
  });

  it("renders without throwing when the payload is not an object", () => {
    expect(() => renderPanels(null)).not.toThrow();
  });

  it("renders both values when tags array contains repeated keys", () => {
    renderPanels({
      tags: [
        ["release", "1.0"],
        ["release", "2.0"]
      ]
    });

    expect(screen.getByText("Tags")).toBeDefined();
    expect(screen.getByText("1.0")).toBeDefined();
    expect(screen.getByText("2.0")).toBeDefined();
  });
});
