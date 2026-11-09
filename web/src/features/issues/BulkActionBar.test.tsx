import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { BulkActionBar } from "./BulkActionBar";

function renderBar() {
  const onIgnore = vi.fn();
  render(
    <BulkActionBar selectedCount={3} busy={false} error={null} onResolve={() => {}} onIgnore={onIgnore} onClear={() => {}} />
  );
  fireEvent.pointerDown(screen.getByRole("button", { name: "Ignore" }), { button: 0, ctrlKey: false, pointerType: "mouse" });
  return { onIgnore };
}

describe("BulkActionBar", () => {
  it("offers the same ignore conditions as the issue page", () => {
    renderBar();

    expect(screen.getAllByRole("menuitem").map((item) => item.textContent)).toEqual([
      "Forever",
      "For 1 hour",
      "For 24 hours",
      "For 7 days",
      "For 30 days",
      "Until 10 more events",
      "Until 100 more events",
      "Until 1,000 more events",
      "Until 10 more users",
      "Until 100 more users",
      "Until 1,000 more users"
    ]);
  });

  it("hands the picked condition to the bulk action", () => {
    const { onIgnore } = renderBar();

    fireEvent.click(screen.getByRole("menuitem", { name: "For 24 hours" }));

    expect(onIgnore).toHaveBeenCalledWith({ duration: "24h" });
  });

  it("ignores forever when Forever is picked", () => {
    const { onIgnore } = renderBar();

    fireEvent.click(screen.getByRole("menuitem", { name: "Forever" }));

    expect(onIgnore).toHaveBeenCalledWith(null);
  });
});
