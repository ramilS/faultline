import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("@/lib/clipboard", () => ({ copyToClipboard: vi.fn() }));

import { copyToClipboard } from "@/lib/clipboard";

import { SdkSetupCard } from "./SdkSetupCard";

const DSN = "http://abc123@localhost:8080/1";

describe("SdkSetupCard", () => {
  beforeEach(() => {
    vi.mocked(copyToClipboard).mockReset();
  });

  it("shows the project's own DSN in the platform's snippet", () => {
    render(<SdkSetupCard platform="jvm-spring" dsn={DSN} />);

    expect(screen.getByText(`sentry.dsn=${DSN}`)).toBeDefined();
  });

  it("copies the snippet as shown", () => {
    render(<SdkSetupCard platform="jvm-spring" dsn={DSN} />);

    fireEvent.click(screen.getByRole("button", { name: "Copy" }));

    expect(copyToClipboard).toHaveBeenCalledWith(`sentry.dsn=${DSN}`, "Snippet");
  });

  it("shows a placeholder and no copy button when the viewer cannot see a DSN, so nothing unusable is copied", () => {
    render(<SdkSetupCard platform="jvm-spring" dsn={null} />);

    expect(screen.getByText("sentry.dsn=<your DSN>")).toBeDefined();
    expect(screen.queryByRole("button", { name: "Copy" })).toBeNull();
  });

  it("renders nothing for a platform without a checked snippet", () => {
    const { container } = render(<SdkSetupCard platform="other" dsn={DSN} />);

    expect(container.textContent).toBe("");
  });
});
