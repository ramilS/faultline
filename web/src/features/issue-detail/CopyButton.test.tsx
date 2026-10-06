import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

// CopyButton's only side effect is the clipboard write. Mocking it here is
// the only way to observe exactly what was copied, and when, without a real
// clipboard — the same module-mocking pattern EventNavigator.test.tsx uses
// for `@/apiClient`.
vi.mock("@/lib/clipboard", () => ({
  copyToClipboard: vi.fn()
}));

import { copyToClipboard } from "@/lib/clipboard";

import { CopyButton } from "./CopyButton";

const mockedCopyToClipboard = vi.mocked(copyToClipboard);

describe("CopyButton", () => {
  beforeEach(() => {
    mockedCopyToClipboard.mockReset();
  });

  it("does not call a thunk value during render, and copies the value it returns at click time", () => {
    const thunk = vi.fn().mockReturnValueOnce("first-value").mockReturnValueOnce("second-value");

    render(<CopyButton value={thunk} label="Issue link" />);

    expect(thunk).not.toHaveBeenCalled();
    expect(mockedCopyToClipboard).not.toHaveBeenCalled();

    const button = screen.getByRole("button", { name: "Copy Issue link" });

    fireEvent.click(button);
    expect(thunk).toHaveBeenCalledTimes(1);
    expect(mockedCopyToClipboard).toHaveBeenNthCalledWith(1, "first-value", "Issue link");

    fireEvent.click(button);
    expect(thunk).toHaveBeenCalledTimes(2);
    expect(mockedCopyToClipboard).toHaveBeenNthCalledWith(2, "second-value", "Issue link");
  });

  it("copies a plain string value on click", () => {
    render(<CopyButton value="evt-abc123" label="Event ID" />);

    fireEvent.click(screen.getByRole("button", { name: "Copy Event ID" }));

    expect(mockedCopyToClipboard).toHaveBeenCalledWith("evt-abc123", "Event ID");
  });

  it("exposes a 'Copy {label}' accessible name when no children are given", () => {
    render(<CopyButton value="evt-abc123" label="Event ID" />);

    expect(screen.getByRole("button", { name: "Copy Event ID" })).toBeDefined();
  });

  it("uses the visible children text as the accessible name, with no conflicting aria-label", () => {
    render(
      <CopyButton value="https://example.com/acme/issues/42" label="Issue link">
        Share
      </CopyButton>
    );

    // If the `Copy Issue link` aria-label survived alongside the visible
    // "Share" text, it would win the accessible-name computation and this
    // query would fail to find the button by its visible text.
    expect(screen.getByRole("button", { name: "Share" })).toBeDefined();
    expect(screen.queryByRole("button", { name: "Copy Issue link" })).toBeNull();
  });
});
