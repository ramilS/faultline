import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { EventSectionNav } from "./EventSectionNav";

describe("EventSectionNav", () => {
  it("renders a link per supplied section, each pointing at its anchor", () => {
    render(
      <EventSectionNav
        sections={[
          { id: "section-highlights", label: "Highlights" },
          { id: "section-tags", label: "Tags" },
          { id: "section-contexts", label: "Contexts" }
        ]}
      />
    );

    expect(screen.getByRole("link", { name: "Highlights" })).toHaveProperty("hash", "#section-highlights");
    expect(screen.getByRole("link", { name: "Tags" })).toHaveProperty("hash", "#section-tags");
    expect(screen.getByRole("link", { name: "Contexts" })).toHaveProperty("hash", "#section-contexts");
  });

  it("is announced as navigation to assistive tech", () => {
    render(
      <EventSectionNav
        sections={[
          { id: "section-highlights", label: "Highlights" },
          { id: "section-tags", label: "Tags" }
        ]}
      />
    );

    expect(screen.getByRole("navigation", { name: "Jump to section" })).toBeDefined();
  });
});
