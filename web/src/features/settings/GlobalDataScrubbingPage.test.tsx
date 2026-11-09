import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("@/apiClient", () => ({
  api: { getGlobalScrubbing: vi.fn(), updateGlobalScrubbing: vi.fn(), previewGlobalScrubbing: vi.fn() }
}));

import { api } from "@/apiClient";

import { GlobalDataScrubbingPage } from "./GlobalDataScrubbingPage";

const getGlobal = vi.mocked(api.getGlobalScrubbing);
const updateGlobal = vi.mocked(api.updateGlobalScrubbing);
const previewGlobal = vi.mocked(api.previewGlobalScrubbing);
const defaultFields = ["password", "token"];

async function addPreset(name: string) {
  fireEvent.click(await screen.findByRole("button", { name: "Add rule" }));
  fireEvent.click(screen.getByRole("button", { name: /A known kind of value/ }));
  fireEvent.click(screen.getByRole("button", { name: `Add ${name}` }));
}

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
    previewGlobal.mockReset();
  });

  it("shows the stored instance-wide rules", async () => {
    getGlobal.mockResolvedValue({ rules: [{ name: "Telegram", pattern: "bot\\d+", replacement: "" }], default_fields: defaultFields });

    renderPage();

    expect(await screen.findByLabelText("Rule 1 pattern")).toHaveProperty("value", "bot\\d+");
  });

  it("saves the edited rules", async () => {
    getGlobal.mockResolvedValue({ rules: [], default_fields: defaultFields });
    updateGlobal.mockImplementation(async (input) => input);

    renderPage();
    await addPreset("AWS access key ID");
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(updateGlobal).toHaveBeenCalledWith({ rules: [expect.objectContaining({ name: "AWS access key ID" })] })
    );
    expect(await screen.findByText("Saved.")).toBeDefined();
  });

  it("clears Saved. once a rule is edited", async () => {
    getGlobal.mockResolvedValue({ rules: [{ name: "n", pattern: "p", replacement: "" }], default_fields: defaultFields });
    updateGlobal.mockImplementation(async (input) => input);

    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: "Save" }));
    await screen.findByText("Saved.");
    fireEvent.change(screen.getByLabelText("Rule 1 pattern"), { target: { value: "q" } });

    expect(screen.queryByText("Saved.")).toBeNull();
  });

  it("does not call the server while a rule has a blank pattern", async () => {
    getGlobal.mockResolvedValue({ rules: [{ name: "named", pattern: "", replacement: "" }], default_fields: defaultFields });

    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: "Save" }));

    expect((await screen.findByRole("alert")).textContent).toContain("Rule 1: pattern is required.");
    expect(updateGlobal).not.toHaveBeenCalled();
  });

  it("shows the server's validation message", async () => {
    getGlobal.mockResolvedValue({ rules: [], default_fields: defaultFields });
    updateGlobal.mockRejectedValue(new Error("rule 1: pattern is not valid RE2"));

    renderPage();
    await addPreset("AWS access key ID");
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    expect((await screen.findByRole("alert")).textContent).toContain("pattern is not valid RE2");
  });

  it("opens with the defaults and names itself as applying to all projects", async () => {
    getGlobal.mockResolvedValue({ rules: [], default_fields: defaultFields });

    renderPage();

    const defaults = await screen.findByRole("list", { name: "Default sensitive field names" });
    expect(defaults.textContent).toBe("passwordtoken");
    expect(screen.getByRole("heading", { name: "Scrubbing (all projects)" })).toBeTruthy();
    expect(screen.getByText("No custom rules. The defaults above still apply.")).toBeTruthy();
  });

  it("previews the draft rules through the server and offers no latest event", async () => {
    getGlobal.mockResolvedValue({ rules: [], default_fields: defaultFields });
    previewGlobal.mockResolvedValue({ scrubbed: "[Filtered] ok", segments: [{ text: "[Filtered]", redacted: true }, { text: " ok", redacted: false }] });

    renderPage();
    await addPreset("AWS access key ID");
    fireEvent.change(screen.getByLabelText("Test sample"), { target: { value: "AKIAIOSFODNN7EXAMPLE ok" } });
    fireEvent.click(screen.getByRole("button", { name: "Preview sample" }));

    expect((await screen.findByLabelText("Result")).querySelector("mark")?.textContent).toBe("[Filtered]");
    expect(previewGlobal).toHaveBeenCalledWith([expect.objectContaining({ name: "AWS access key ID" })], "AKIAIOSFODNN7EXAMPLE ok");
    expect(screen.queryByRole("button", { name: "Use latest event" })).toBeNull();
    expect(screen.queryByRole("button", { name: /A field/ })).toBeNull();
  });
});
