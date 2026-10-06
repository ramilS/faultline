import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("@/apiClient", () => ({
  api: { getGlobalScrubbing: vi.fn(), updateGlobalScrubbing: vi.fn() }
}));

import { api } from "@/apiClient";

import { GlobalDataScrubbingPage } from "./GlobalDataScrubbingPage";

const getGlobal = vi.mocked(api.getGlobalScrubbing);
const updateGlobal = vi.mocked(api.updateGlobalScrubbing);

function renderPage() {
  return render(
    <MemoryRouter initialEntries={["/settings/data-scrubbing"]}>
      <Routes>
        <Route path="/settings/data-scrubbing" element={<GlobalDataScrubbingPage />} />
      </Routes>
    </MemoryRouter>
  );
}

describe("GlobalDataScrubbingPage", () => {
  beforeEach(() => {
    getGlobal.mockReset();
    updateGlobal.mockReset();
  });

  it("shows the stored instance-wide rules", async () => {
    getGlobal.mockResolvedValue({ rules: [{ name: "Telegram", pattern: "bot\\d+", replacement: "" }] });

    renderPage();

    expect(await screen.findByLabelText("Rule 1 pattern")).toHaveProperty("value", "bot\\d+");
  });

  it("saves the edited rules", async () => {
    getGlobal.mockResolvedValue({ rules: [] });
    updateGlobal.mockImplementation(async (input) => input);

    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: "Add AWS access key ID" }));
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(updateGlobal).toHaveBeenCalledWith({ rules: [expect.objectContaining({ name: "AWS access key ID" })] })
    );
    expect(await screen.findByText("Saved.")).toBeDefined();
  });

  it("clears Saved. once a rule is edited", async () => {
    getGlobal.mockResolvedValue({ rules: [{ name: "n", pattern: "p", replacement: "" }] });
    updateGlobal.mockImplementation(async (input) => input);

    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: "Save" }));
    await screen.findByText("Saved.");
    fireEvent.change(screen.getByLabelText("Rule 1 pattern"), { target: { value: "q" } });

    expect(screen.queryByText("Saved.")).toBeNull();
  });

  it("does not call the server while a rule has a blank pattern", async () => {
    getGlobal.mockResolvedValue({ rules: [{ name: "named", pattern: "", replacement: "" }] });

    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: "Save" }));

    expect((await screen.findByRole("alert")).textContent).toContain("Rule 1: pattern is required.");
    expect(updateGlobal).not.toHaveBeenCalled();
  });

  it("shows the server's validation message", async () => {
    getGlobal.mockResolvedValue({ rules: [] });
    updateGlobal.mockRejectedValue(new Error("rule 1: pattern is not valid RE2"));

    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: "Add AWS access key ID" }));
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    expect((await screen.findByRole("alert")).textContent).toContain("pattern is not valid RE2");
  });
});
