import { render } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";

import { documentTitle, useDocumentTitle } from "./useDocumentTitle";

function Page({ title }: { title: string | null }) {
  useDocumentTitle(title);
  return null;
}

describe("documentTitle", () => {
  it("puts the page first and the product last", () => {
    expect(documentTitle("Issues · shop")).toBe("Issues · shop · Faultline");
  });

  it("falls back to the product name when there is no page title", () => {
    expect(documentTitle(null)).toBe("Faultline");
    expect(documentTitle("  ")).toBe("Faultline");
  });
});

describe("useDocumentTitle", () => {
  afterEach(() => {
    document.title = "";
  });

  it("sets the tab title while mounted and restores the product name on unmount", () => {
    const { unmount } = render(<Page title="Account" />);
    expect(document.title).toBe("Account · Faultline");

    unmount();
    expect(document.title).toBe("Faultline");
  });

  it("follows the title as it changes", () => {
    const { rerender } = render(<Page title={null} />);
    expect(document.title).toBe("Faultline");

    rerender(<Page title="TypeError: x is undefined" />);
    expect(document.title).toBe("TypeError: x is undefined · Faultline");
  });
});
