import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

const signedIn = vi.hoisted(() => ({ role: "owner" as "owner" | "member" }));

vi.mock("@/features/auth/SessionProvider", () => ({
  useSession: () => ({
    session: { state: "authenticated", user: { id: 1, name: "Ada", email: "ada@example.com", role: signedIn.role } }
  })
}));

vi.mock("@/apiClient", () => ({
  api: { getDataScrubbing: vi.fn(), updateDataScrubbing: vi.fn() }
}));

import { ApiError, type DataScrubbing } from "@/api";
import { api } from "@/apiClient";

import { DataScrubbingPage } from "./DataScrubbingPage";

const getDataScrubbing = vi.mocked(api.getDataScrubbing);
const updateDataScrubbing = vi.mocked(api.updateDataScrubbing);

const current: DataScrubbing = { enabled: true, extra_fields: ["ssn"], safe_fields: [], rules: [] };

function renderPage() {
  return render(
    <MemoryRouter initialEntries={["/acme/settings/data-scrubbing"]}>
      <Routes>
        <Route path="/:slug/settings/data-scrubbing" element={<DataScrubbingPage />} />
      </Routes>
    </MemoryRouter>
  );
}

describe("DataScrubbingPage", () => {
  beforeEach(() => {
    signedIn.role = "owner";
    getDataScrubbing.mockReset();
    updateDataScrubbing.mockReset();
  });

  it("shows the stored configuration", async () => {
    getDataScrubbing.mockResolvedValue(current);

    renderPage();

    expect(await screen.findByLabelText("Additional sensitive fields")).toHaveProperty("value", "ssn");
    expect(getDataScrubbing).toHaveBeenCalledWith("acme");
  });

  it("saves the edited lists as parsed field names", async () => {
    getDataScrubbing.mockResolvedValue(current);
    updateDataScrubbing.mockResolvedValue({ enabled: true, extra_fields: ["ssn", "pin"], safe_fields: ["token"], rules: [] });

    renderPage();
    fireEvent.change(await screen.findByLabelText("Additional sensitive fields"), { target: { value: "ssn\npin" } });
    fireEvent.change(screen.getByLabelText("Safe fields"), { target: { value: "token" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(updateDataScrubbing).toHaveBeenCalledWith("acme", {
        enabled: true,
        extra_fields: ["ssn", "pin"],
        safe_fields: ["token"],
        rules: []
      })
    );
    expect(await screen.findByText("Saved.")).toBeDefined();
  });

  it("clears Saved. once a field is edited again", async () => {
    getDataScrubbing.mockResolvedValue(current);
    updateDataScrubbing.mockImplementation(async (_slug, input) => input);

    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: "Save" }));
    await screen.findByText("Saved.");
    fireEvent.change(screen.getByLabelText("Safe fields"), { target: { value: "token" } });

    expect(screen.queryByText("Saved.")).toBeNull();
  });

  it("clears Saved. once a rule is edited", async () => {
    getDataScrubbing.mockResolvedValue({ ...current, rules: [{ name: "n", pattern: "p", replacement: "" }] });
    updateDataScrubbing.mockImplementation(async (_slug, input) => input);

    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: "Save" }));
    await screen.findByText("Saved.");
    fireEvent.change(screen.getByLabelText("Rule 1 pattern"), { target: { value: "q" } });

    expect(screen.queryByText("Saved.")).toBeNull();
  });

  it("saves a rule added from a preset", async () => {
    getDataScrubbing.mockResolvedValue(current);
    updateDataScrubbing.mockImplementation(async (_slug, input) => input);

    renderPage();
    await screen.findByLabelText("Additional sensitive fields");
    fireEvent.click(screen.getByRole("button", { name: "Add Telegram bot token" }));
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(updateDataScrubbing).toHaveBeenCalledWith("acme", expect.objectContaining({
        rules: [expect.objectContaining({ name: "Telegram bot token", replacement: "bot[Filtered]" })]
      }))
    );
  });

  it("does not call the server for a name over the length limit", async () => {
    getDataScrubbing.mockResolvedValue(current);

    renderPage();
    fireEvent.change(await screen.findByLabelText("Safe fields"), { target: { value: "x".repeat(65) } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    expect((await screen.findByRole("alert")).textContent).toContain("Safe fields");
    expect(updateDataScrubbing).not.toHaveBeenCalled();
  });

  it("does not call the server while a rule has a blank pattern", async () => {
    getDataScrubbing.mockResolvedValue(current);

    renderPage();
    await screen.findByLabelText("Additional sensitive fields");
    fireEvent.click(screen.getByRole("button", { name: "Add rule" }));
    fireEvent.change(screen.getByLabelText("Rule 1 name"), { target: { value: "named" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    expect((await screen.findByRole("alert")).textContent).toContain("Rule 1: pattern is required.");
    expect(updateDataScrubbing).not.toHaveBeenCalled();
  });

  it("shows the server's message when saving fails", async () => {
    getDataScrubbing.mockResolvedValue(current);
    updateDataScrubbing.mockRejectedValue(new ApiError(500, "internal server error"));

    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: "Save" }));

    expect((await screen.findByRole("alert")).textContent).toContain("internal server error");
  });

  it("disables the field lists while scrubbing is switched off", async () => {
    getDataScrubbing.mockResolvedValue(current);

    renderPage();
    const extra = await screen.findByLabelText("Additional sensitive fields");
    expect(extra).toHaveProperty("disabled", false);
    fireEvent.click(screen.getByRole("checkbox", { name: "Scrub sensitive data" }));

    expect(extra).toHaveProperty("disabled", true);
    expect(screen.getByLabelText("Safe fields")).toHaveProperty("disabled", true);
  });

  it("is read-only for a member", async () => {
    signedIn.role = "member";
    getDataScrubbing.mockResolvedValue(current);

    renderPage();

    expect(await screen.findByLabelText("Additional sensitive fields")).toHaveProperty("disabled", true);
    expect(screen.queryByRole("button", { name: "Save" })).toBeNull();
  });
});
