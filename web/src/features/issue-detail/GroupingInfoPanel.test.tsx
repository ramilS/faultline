import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { GroupingInfoPanel } from "./GroupingInfoPanel";

describe("GroupingInfoPanel", () => {
  it("renders the fingerprint once the details are expanded", () => {
    render(<GroupingInfoPanel fingerprint="abc123def456" />);

    expect(screen.getByText("Grouping information")).toBeDefined();
    expect(screen.queryByText("abc123def456")).toBeNull();

    fireEvent.click(screen.getByRole("button", { name: /details/i }));

    expect(screen.getByText("abc123def456")).toBeDefined();
  });
});
