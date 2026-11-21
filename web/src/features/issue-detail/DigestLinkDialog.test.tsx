import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));
vi.mock("@/lib/clipboard", () => ({ copyToClipboard: vi.fn() }));
vi.mock("@/apiClient", () => ({
  api: { createDigestLink: vi.fn(), revokeDigestLinks: vi.fn() }
}));

import { toast } from "sonner";

import { ApiError } from "@/api";
import { api } from "@/apiClient";

import { DigestLinkDialog } from "./DigestLinkDialog";

const createDigestLink = vi.mocked(api.createDigestLink);
const revokeDigestLinks = vi.mocked(api.revokeDigestLinks);
const LINK_URL = "https://faultline.example/api/v1/digest-links/fl_dig_secret";

afterEach(() => {
  vi.clearAllMocks();
});

describe("DigestLinkDialog", () => {
  it("creates a link with the chosen lifetime and shows it", async () => {
    createDigestLink.mockResolvedValue({ url: LINK_URL, expires_at: "2026-11-21T10:00:00Z" });
    render(<DigestLinkDialog issueId={42} open onOpenChange={() => {}} />);

    fireEvent.click(screen.getByRole("button", { name: "1 hour" }));
    fireEvent.click(screen.getByRole("button", { name: "Create link" }));

    expect(await screen.findByText(LINK_URL)).toBeDefined();
    expect(createDigestLink).toHaveBeenCalledWith(42, "1h");
    expect(screen.getByRole("button", { name: "1 hour" }).getAttribute("aria-pressed")).toBe("true");
    expect(screen.getByText(/^Expires /)).toBeDefined();
  });

  it("defaults to a 24-hour lifetime", async () => {
    createDigestLink.mockResolvedValue({ url: LINK_URL, expires_at: "2026-11-22T09:00:00Z" });
    render(<DigestLinkDialog issueId={42} open onOpenChange={() => {}} />);

    fireEvent.click(screen.getByRole("button", { name: "Create link" }));

    await waitFor(() => expect(createDigestLink).toHaveBeenCalledWith(42, "24h"));
  });

  it("says why a link could not be created and shows none", async () => {
    createDigestLink.mockRejectedValue(new ApiError(404, "issue not found", null));
    render(<DigestLinkDialog issueId={42} open onOpenChange={() => {}} />);

    fireEvent.click(screen.getByRole("button", { name: "Create link" }));

    await waitFor(() => expect(toast.error).toHaveBeenCalledWith(expect.stringContaining("issue not found")));
    expect(screen.queryByText(LINK_URL)).toBeNull();
  });

  it("revokes every link and hides the one shown", async () => {
    createDigestLink.mockResolvedValue({ url: LINK_URL, expires_at: "2026-11-21T10:00:00Z" });
    revokeDigestLinks.mockResolvedValue(undefined);
    render(<DigestLinkDialog issueId={42} open onOpenChange={() => {}} />);
    fireEvent.click(screen.getByRole("button", { name: "Create link" }));
    await screen.findByText(LINK_URL);

    fireEvent.click(screen.getByRole("button", { name: "Revoke all links" }));

    await waitFor(() => expect(screen.queryByText(LINK_URL)).toBeNull());
    expect(revokeDigestLinks).toHaveBeenCalledWith(42);
    expect(toast.success).toHaveBeenCalled();
  });
});
